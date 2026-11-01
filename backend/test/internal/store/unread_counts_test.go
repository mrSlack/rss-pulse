package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pulse-reader/app/internal/store"
)

// referenceFeedCounts and referenceFolderCounts are the original join-everything
// counter queries; the optimized store queries must return identical numbers.
func referenceFeedCounts(t *testing.T, pool *pgxpool.Pool, userID int64) map[int64]int {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT uf.id, COUNT(a.id) FILTER (WHERE COALESCE(state.is_read, FALSE) = FALSE)::int
		FROM user_feeds uf
		JOIN feed_sources fs ON fs.id = uf.feed_source_id
		LEFT JOIN articles a ON a.feed_source_id = fs.id
		LEFT JOIN user_article_states state ON state.user_id = uf.user_id AND state.article_id = a.id
		WHERE uf.user_id = $1
		GROUP BY uf.id`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[int64]int{}
	for rows.Next() {
		var id int64
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			t.Fatal(err)
		}
		result[id] = count
	}
	return result
}

func referenceFolderCounts(t *testing.T, pool *pgxpool.Pool, userID int64) map[int64]int {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT d.id, COUNT(a.id) FILTER (WHERE COALESCE(state.is_read, FALSE) = FALSE)::int
		FROM folders d
		LEFT JOIN user_feeds uf ON uf.folder_id = d.id AND uf.user_id = $1
		LEFT JOIN feed_sources fs ON fs.id = uf.feed_source_id
		LEFT JOIN articles a ON a.feed_source_id = fs.id
		LEFT JOIN user_article_states state ON state.user_id = $1 AND state.article_id = a.id
		WHERE d.owner_user_id = $1
		GROUP BY d.id`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[int64]int{}
	for rows.Next() {
		var id int64
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			t.Fatal(err)
		}
		result[id] = count
	}
	return result
}

func TestUnreadCountersMatchReferenceInEdgeCases(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPostgres(t)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	adminID, err := store.EnsureInitialAdministrator(ctx, pool, "admin@example.com", "a-long-bootstrap-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateSharedSources(ctx, pool, adminID); err != nil {
		t.Fatal(err)
	}
	s := store.New(pool)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	id := func(query string, args ...any) int64 {
		t.Helper()
		var value int64
		if err := pool.QueryRow(ctx, query, args...).Scan(&value); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return value
	}
	readerID := id(`INSERT INTO users (email,password_hash,role) VALUES ('reader@example.com','test-hash','Reader') RETURNING id`)
	source := func(name string) int64 {
		return id(`INSERT INTO feed_sources (normalized_url,title,url) VALUES ($1,$2,$1) RETURNING id`, "https://"+name+".example/rss", name)
	}
	published := time.Now().UTC().Add(-time.Hour)
	article := func(sourceID int64, guid string) int64 {
		return id(`INSERT INTO articles (feed_source_id,guid,title,summary,published_at) VALUES ($1,$2,$2,'',$3) RETURNING id`, sourceID, guid, published)
	}
	state := func(userID, articleID int64, read, saved bool) {
		exec(`INSERT INTO user_article_states (user_id,article_id,is_read,is_saved) VALUES ($1,$2,$3,$4)`, userID, articleID, read, saved)
	}
	folder := func(owner int64, name string) int64 {
		return id(`INSERT INTO folders (owner_user_id,name,color) VALUES ($1,$2,'amber') RETURNING id`, owner, name)
	}
	subscribe := func(userID, sourceID int64, folderID *int64) int64 {
		return id(`INSERT INTO user_feeds (user_id,feed_source_id,folder_id) VALUES ($1,$2,$3) RETURNING id`, userID, sourceID, folderID)
	}

	mixed, partial, empty, unsubscribed, unfoldered := source("mixed"), source("partial"), source("empty"), source("unsubscribed"), source("unfoldered")
	m1, m2, m3, m4 := article(mixed, "m1"), article(mixed, "m2"), article(mixed, "m3"), article(mixed, "m4")
	p1, p2 := article(partial, "p1"), article(partial, "p2")
	u1 := article(unsubscribed, "u1")
	article(unfoldered, "f1")
	article(unfoldered, "f2")

	news, emptyFeedsFolder, noFeedsFolder := folder(adminID, "Zeta news"), folder(adminID, "Empty sources"), folder(adminID, "No feeds")
	readerFolder := folder(readerID, "Reader folder")
	mixedFeed := subscribe(adminID, mixed, &news)
	partialFeed := subscribe(adminID, partial, &news)
	emptyFeed := subscribe(adminID, empty, &emptyFeedsFolder)
	unfolderedFeed := subscribe(adminID, unfoldered, nil)
	subscribe(readerID, mixed, &readerFolder)
	subscribe(readerID, unsubscribed, &readerFolder)

	state(adminID, m1, true, false)  // read
	state(adminID, m2, false, true)  // saved only: still unread
	state(adminID, m3, false, false) // explicit unread row
	state(adminID, p1, true, true)   // read and saved
	state(adminID, u1, true, false)  // state for an article outside the admin's subscriptions
	state(readerID, m1, false, false)
	state(readerID, m2, true, false)
	state(readerID, m3, true, false)
	state(readerID, m4, true, true)
	_ = p2

	wantFeeds := map[int64]map[int64]int{
		adminID:  {mixedFeed: 3, partialFeed: 1, emptyFeed: 0, unfolderedFeed: 2},
		readerID: nil,
	}
	wantFolders := map[int64]map[int64]int{
		adminID:  {news: 4, emptyFeedsFolder: 0, noFeedsFolder: 0},
		readerID: {readerFolder: 2}, // m1 (explicit unread row) and u1 (read only by the admin)
	}
	for _, userID := range []int64{adminID, readerID} {
		referenceFeeds := referenceFeedCounts(t, pool, userID)
		feedsForUser, err := s.ListFeedsForUser(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		if len(feedsForUser) != len(referenceFeeds) {
			t.Fatalf("user %d feeds = %d, reference %d", userID, len(feedsForUser), len(referenceFeeds))
		}
		for _, feed := range feedsForUser {
			if feed.UnreadCount != referenceFeeds[feed.ID] {
				t.Errorf("user %d feed %d unread = %d, reference %d", userID, feed.ID, feed.UnreadCount, referenceFeeds[feed.ID])
			}
			if want, ok := wantFeeds[userID][feed.ID]; ok && feed.UnreadCount != want {
				t.Errorf("user %d feed %d unread = %d, want %d", userID, feed.ID, feed.UnreadCount, want)
			}
		}

		referenceFolders := referenceFolderCounts(t, pool, userID)
		foldersForUser, err := s.ListFoldersForUser(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		if len(foldersForUser) != len(referenceFolders) {
			t.Fatalf("user %d folders = %d, reference %d", userID, len(foldersForUser), len(referenceFolders))
		}
		for _, folder := range foldersForUser {
			if folder.UnreadCount != referenceFolders[folder.ID] {
				t.Errorf("user %d folder %d unread = %d, reference %d", userID, folder.ID, folder.UnreadCount, referenceFolders[folder.ID])
			}
			if want, ok := wantFolders[userID][folder.ID]; ok && folder.UnreadCount != want {
				t.Errorf("user %d folder %q unread = %d, want %d", userID, folder.Name, folder.UnreadCount, want)
			}
		}
	}

	updated, err := s.UpdateFolderForUser(ctx, adminID, news, "Renamed news", "blue")
	if err != nil {
		t.Fatal(err)
	}
	if updated.UnreadCount != 4 {
		t.Fatalf("UpdateFolderForUser unread = %d, want 4", updated.UnreadCount)
	}
	updatedEmpty, err := s.UpdateFolderForUser(ctx, adminID, noFeedsFolder, "Still no feeds", "teal")
	if err != nil {
		t.Fatal(err)
	}
	if updatedEmpty.UnreadCount != 0 {
		t.Fatalf("UpdateFolderForUser unread for folder without feeds = %d, want 0", updatedEmpty.UnreadCount)
	}
	if _, err := s.UpdateFolderForUser(ctx, readerID, news, "Not mine", "blue"); err != store.ErrNotFound {
		t.Fatalf("UpdateFolderForUser for another owner error = %v, want ErrNotFound", err)
	}
}
