package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pulse-reader/app/internal/auth"
	"github.com/pulse-reader/app/internal/feeds"
	"github.com/pulse-reader/app/internal/store"
)

type retentionFixture struct {
	pool     *pgxpool.Pool
	store    *store.Store
	adminID  int64
	readerID int64
	sourceID int64
}

func newRetentionFixture(t *testing.T) retentionFixture {
	t.Helper()
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
	var readerID, sourceID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email,password_hash,role) VALUES ('reader@example.com','test-hash','Reader') RETURNING id`).Scan(&readerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url) VALUES ('https://example.com/rss','News','https://example.com/rss') RETURNING id`).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_feeds (user_id,feed_source_id) VALUES ($1,$2),($3,$2)`, adminID, sourceID, readerID); err != nil {
		t.Fatal(err)
	}
	return retentionFixture{pool: pool, store: store.New(pool), adminID: adminID, readerID: readerID, sourceID: sourceID}
}

// insertArticle stores an article whose publication and creation times are the given ages in days.
func (f retentionFixture) insertArticle(t *testing.T, guid string, publishedDaysAgo, createdDaysAgo int) int64 {
	t.Helper()
	var id int64
	if err := f.pool.QueryRow(context.Background(), `INSERT INTO articles (feed_source_id,guid,title,published_at,created_at)
		VALUES ($1,$2,$2,NOW()-make_interval(days=>$3),NOW()-make_interval(days=>$4)) RETURNING id`,
		f.sourceID, guid, publishedDaysAgo, createdDaysAgo).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f retentionFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestDeleteExpiredArticlesKeepsSavedAndRecentArticles(t *testing.T) {
	ctx := context.Background()
	f := newRetentionFixture(t)
	oldUnsaved := f.insertArticle(t, "old-unsaved", 400, 400)
	oldRead := f.insertArticle(t, "old-read", 400, 400)
	oldSaved := f.insertArticle(t, "old-saved", 400, 400)
	recent := f.insertArticle(t, "recent", 10, 10)
	oldButRecentlyCreated := f.insertArticle(t, "old-recently-created", 400, 5)
	recentButCreatedLongAgo := f.insertArticle(t, "recently-published", 5, 400)
	if _, err := f.pool.Exec(ctx, `INSERT INTO user_article_states (user_id,article_id,is_read,is_saved) VALUES
		($1,$3,TRUE,FALSE),($2,$3,TRUE,FALSE),($1,$4,TRUE,FALSE),($2,$4,FALSE,TRUE),($1,$5,TRUE,FALSE)`,
		f.adminID, f.readerID, oldRead, oldSaved, recent); err != nil {
		t.Fatal(err)
	}

	deleted, err := f.store.DeleteExpiredArticles(ctx, 180, 1)
	if err != nil {
		t.Fatalf("DeleteExpiredArticles() error = %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2 (old unsaved and old read article)", deleted)
	}
	for _, id := range []int64{oldUnsaved, oldRead} {
		if f.count(t, `SELECT COUNT(*) FROM articles WHERE id=$1`, id) != 0 {
			t.Fatalf("article %d survived retention", id)
		}
	}
	for _, id := range []int64{oldSaved, recent, oldButRecentlyCreated, recentButCreatedLongAgo} {
		if f.count(t, `SELECT COUNT(*) FROM articles WHERE id=$1`, id) != 1 {
			t.Fatalf("article %d was deleted but must be retained", id)
		}
	}
	if got := f.count(t, `SELECT COUNT(*) FROM user_article_states WHERE article_id=$1`, oldRead); got != 0 {
		t.Fatalf("states of deleted article = %d, want cascade delete", got)
	}
	if got := f.count(t, `SELECT COUNT(*) FROM user_article_states WHERE article_id IN ($1,$2)`, oldSaved, recent); got != 3 {
		t.Fatalf("states of retained articles = %d, want 3", got)
	}

	if deleted, err := f.store.DeleteExpiredArticles(ctx, 0, 5000); err != nil || deleted != 0 {
		t.Fatalf("disabled retention deleted %d, err %v", deleted, err)
	}
}

func TestDeleteExpiredQuarantineKeepsPendingAndRecentDecisions(t *testing.T) {
	ctx := context.Background()
	f := newRetentionFixture(t)
	insert := func(guid, status string, eligible, withSummary bool, decidedDaysAgo *int, lastBlockedDaysAgo int) {
		t.Helper()
		var summary *string
		if withSummary {
			value := "summary"
			summary = &value
		}
		if _, err := f.pool.Exec(ctx, `INSERT INTO blocked_feed_articles
			(feed_source_id,article_guid,article_title,feed_title,feed_url,matched_term,summary,published_at,status,decided_at,last_blocked_at,first_blocked_at,moderation_eligible)
			VALUES ($1,$2,$2,'News','https://example.com/rss','xxx',$3,NOW()-INTERVAL '400 days',$4,
				CASE WHEN $5::int IS NULL THEN NULL ELSE NOW()-make_interval(days=>$5::int) END,
				NOW()-make_interval(days=>$6),NOW()-INTERVAL '500 days',$7)`,
			f.sourceID, guid, summary, status, decidedDaysAgo, lastBlockedDaysAgo, eligible); err != nil {
			t.Fatal(err)
		}
	}
	days := func(value int) *int { return &value }
	insert("pending-old", "pending", true, true, nil, 400)
	insert("rejected-old", "rejected", true, true, days(200), 200)
	insert("allowed-old", "allowed", true, true, days(200), 200)
	insert("rejected-recent", "rejected", true, true, days(10), 10)
	insert("rejected-old-still-in-feed", "rejected", true, true, days(200), 1)
	insert("legacy-ineligible-old", "pending", false, true, nil, 200)
	insert("legacy-no-summary-old", "pending", true, false, nil, 200)
	insert("legacy-ineligible-recent", "pending", false, true, nil, 10)

	deleted, err := f.store.DeleteExpiredQuarantine(ctx, 90)
	if err != nil {
		t.Fatalf("DeleteExpiredQuarantine() error = %v", err)
	}
	if deleted != 4 {
		t.Fatalf("deleted = %d, want 4", deleted)
	}
	rows, err := f.pool.Query(ctx, `SELECT article_guid FROM blocked_feed_articles ORDER BY article_guid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	remaining := []string{}
	for rows.Next() {
		var guid string
		if err := rows.Scan(&guid); err != nil {
			t.Fatal(err)
		}
		remaining = append(remaining, guid)
	}
	want := []string{"legacy-ineligible-recent", "pending-old", "rejected-old-still-in-feed", "rejected-recent"}
	if len(remaining) != len(want) {
		t.Fatalf("remaining = %v, want %v", remaining, want)
	}
	for index := range want {
		if remaining[index] != want[index] {
			t.Fatalf("remaining = %v, want %v", remaining, want)
		}
	}
	if deleted, err := f.store.DeleteExpiredQuarantine(ctx, 0); err != nil || deleted != 0 {
		t.Fatalf("disabled quarantine retention deleted %d, err %v", deleted, err)
	}
}

func TestDeleteExpiredSessionsAndRequestPathDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	f := newRetentionFixture(t)
	expiredHash := auth.HashSessionToken("expired-token")
	activeHash := auth.HashSessionToken("active-token")
	if err := f.store.CreateSession(ctx, f.readerID, expiredHash, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreateSession(ctx, f.readerID, activeHash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if _, err := f.store.UserForSession(ctx, expiredHash); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("UserForSession(expired) error = %v, want ErrNotFound", err)
	}
	if user, err := f.store.UserForSession(ctx, activeHash); err != nil || user.ID != f.readerID {
		t.Fatalf("UserForSession(active) = %+v, %v", user, err)
	}
	if got := f.count(t, `SELECT COUNT(*) FROM sessions`); got != 2 {
		t.Fatalf("sessions after request-path lookups = %d, want 2 (no cleanup on the request path)", got)
	}

	deleted, err := f.store.DeleteExpiredSessions(ctx)
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteExpiredSessions() = %d, %v; want 1", deleted, err)
	}
	if got := f.count(t, `SELECT COUNT(*) FROM sessions WHERE token_hash=$1`, activeHash); got != 1 {
		t.Fatal("active session was deleted")
	}
}

func TestSourceRefreshDoesNotReinsertArticlesOlderThanRetention(t *testing.T) {
	ctx := context.Background()
	f := newRetentionFixture(t)
	existingID := f.insertArticle(t, "old-existing", 300, 300)
	f.store.SetArticleRetentionDays(180)
	old := time.Now().UTC().AddDate(0, 0, -300)
	document := feeds.Document{Title: "News", Items: []feeds.Item{
		{GUID: "old-existing", Title: "Updated old title", URL: "https://example.com/existing", PublishedAt: old, HasPublishedAt: true},
		{GUID: "old-new", Title: "Old article deleted by retention", URL: "https://example.com/old", PublishedAt: old, HasPublishedAt: true},
		{GUID: "undated", Title: "Undated article", URL: "https://example.com/undated", PublishedAt: time.Now().UTC()},
		{GUID: "fresh", Title: "Fresh article", URL: "https://example.com/fresh", PublishedAt: time.Now().UTC().Add(-time.Hour), HasPublishedAt: true},
	}}
	if _, err := f.pool.Exec(ctx, `UPDATE feed_sources SET lease_token='lease',lease_expires_at=NOW()+INTERVAL '1 minute' WHERE id=$1`, f.sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CommitSourceRefresh(ctx, store.SourceLease{ID: f.sourceID, Token: "lease"}, document); err != nil {
		t.Fatalf("CommitSourceRefresh() error = %v", err)
	}
	if got := f.count(t, `SELECT COUNT(*) FROM articles WHERE guid='old-new'`); got != 0 {
		t.Fatal("an article older than the retention period was inserted again")
	}
	if got := f.count(t, `SELECT COUNT(*) FROM articles WHERE guid IN ('undated','fresh')`); got != 2 {
		t.Fatalf("fresh/undated articles stored = %d, want 2", got)
	}
	if got := f.count(t, `SELECT COUNT(*) FROM articles WHERE id=$1 AND title='Updated old title'`, existingID); got != 1 {
		t.Fatal("existing old article was not updated by the refresh")
	}

	f.store.SetArticleRetentionDays(0)
	if _, err := f.pool.Exec(ctx, `UPDATE feed_sources SET lease_token='lease2',lease_expires_at=NOW()+INTERVAL '1 minute',last_success_at=NULL WHERE id=$1`, f.sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CommitSourceRefresh(ctx, store.SourceLease{ID: f.sourceID, Token: "lease2"}, document); err != nil {
		t.Fatalf("CommitSourceRefresh() error = %v", err)
	}
	if got := f.count(t, `SELECT COUNT(*) FROM articles WHERE guid='old-new'`); got != 1 {
		t.Fatal("disabled retention must store old articles")
	}
}
