package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pulse-reader/app/internal/feeds"
	"github.com/pulse-reader/app/internal/store"
)

func TestNormalizeFeedURLKeepsMeaningfulPartsAndRemovesTransportNoise(t *testing.T) {
	got, err := store.NormalizeFeedURL("HTTPS://Example.COM:443/news.xml?topic=go#latest")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.com/news.xml?topic=go" {
		t.Fatalf("NormalizeFeedURL() = %q", got)
	}
	other, err := store.NormalizeFeedURL("https://example.com/other.xml?topic=go")
	if err != nil {
		t.Fatal(err)
	}
	if got == other {
		t.Fatal("different feed paths must remain distinct")
	}
}

func TestSourceLeasesFenceConcurrentAndStaleRefreshesAndHonorTTL(t *testing.T) {
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
	var sourceID int64
	if err := pool.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url) VALUES ('https://example.com/rss','Example','https://example.com/rss') RETURNING id`).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_feeds (user_id,feed_source_id) VALUES ($1,$2)`, adminID, sourceID); err != nil {
		t.Fatal(err)
	}
	var readerID, secondSourceID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email,password_hash,role) VALUES ('reader@example.com','test-hash','Reader') RETURNING id`).Scan(&readerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url) VALUES ('https://second.example/rss','Second','https://second.example/rss') RETURNING id`).Scan(&secondSourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_feeds (user_id,feed_source_id) VALUES ($1,$2)`, readerID, secondSourceID); err != nil {
		t.Fatal(err)
	}
	adminSources, err := store.New(pool).SourcesForUser(ctx, adminID)
	if err != nil || len(adminSources) != 1 || adminSources[0].ID != sourceID {
		t.Fatalf("SourcesForUser(admin) = %#v, %v", adminSources, err)
	}
	readerSources, err := store.New(pool).SourcesForUser(ctx, readerID)
	if err != nil || len(readerSources) != 1 || readerSources[0].ID != secondSourceID {
		t.Fatalf("SourcesForUser(reader) = %#v, %v", readerSources, err)
	}
	dueSources, err := store.New(pool).DueSources(ctx)
	if err != nil || len(dueSources) != 2 {
		t.Fatalf("DueSources() = %#v, %v; want both subscribed canonical sources", dueSources, err)
	}
	var wg sync.WaitGroup
	twins := make([]bool, 2)
	leases := make([]store.SourceLease, 2)
	for index := range twins {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			lease, claimed, err := store.New(pool).ClaimSource(ctx, sourceID, nil)
			if err != nil {
				t.Errorf("ClaimSource() error = %v", err)
				return
			}
			twins[index] = claimed
			leases[index] = lease
		}(index)
	}
	wg.Wait()
	if twins[0] == twins[1] {
		t.Fatalf("concurrent source claims = %v, want exactly one claim owner", twins)
	}
	activeIndex := 0
	if !twins[activeIndex] {
		activeIndex = 1
	}
	staleLease := leases[activeIndex]
	otherLease, otherClaimed, err := store.New(pool).ClaimSource(ctx, secondSourceID, nil)
	if err != nil || !otherClaimed {
		t.Fatalf("a different source should be claimable while the first is leased: (%v, %v)", otherLease, err)
	}
	if err := store.New(pool).ReleaseSourceLease(ctx, otherLease, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE feed_sources SET lease_expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, sourceID); err != nil {
		t.Fatal(err)
	}
	newLease, claimed, err := store.New(pool).ClaimSource(ctx, sourceID, nil)
	if err != nil || !claimed || newLease.Token == staleLease.Token {
		t.Fatalf("expired lease reclaim = (%v, %v, %v), want a new token", newLease, claimed, err)
	}
	document := feeds.Document{Title: "Updated", SiteURL: "https://example.com", Items: []feeds.Item{{GUID: "first", Title: "First article", URL: "https://example.com/a"}}}
	if err := store.New(pool).CommitSourceRefresh(ctx, staleLease, document); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("stale commit error = %v, want ErrLeaseLost", err)
	}
	if err := store.New(pool).ReleaseSourceLease(ctx, newLease, "temporary failure"); err != nil {
		t.Fatal(err)
	}
	var lastSuccess *time.Time
	if err := pool.QueryRow(ctx, `SELECT last_success_at FROM feed_sources WHERE id=$1`, sourceID).Scan(&lastSuccess); err != nil {
		t.Fatal(err)
	}
	if lastSuccess != nil {
		t.Fatalf("failed fetch advanced TTL to %v", lastSuccess)
	}
	newLease, claimed, err = store.New(pool).ClaimSource(ctx, sourceID, nil)
	if err != nil || !claimed {
		t.Fatalf("claim after failed fetch = (%v, %v), want retry lease", newLease, err)
	}
	if err := store.New(pool).CommitSourceRefresh(ctx, newLease, document); err != nil {
		t.Fatalf("valid lease commit: %v", err)
	}
	if _, claimed, err := store.New(pool).ClaimSource(ctx, sourceID, nil); err != nil || claimed {
		t.Fatalf("claim within successful TTL = (%v, %v), want suppressed", claimed, err)
	}
}

func TestLegacyFeedMigrationMergesSharedArticlesAndKeepsPrivateState(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPostgres(t)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	adminID, err := store.EnsureInitialAdministrator(ctx, pool, "admin@example.com", "a-long-bootstrap-password")
	if err != nil {
		t.Fatal(err)
	}
	var folderID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM folders WHERE owner_user_id = $1 ORDER BY id LIMIT 1`, adminID).Scan(&folderID); err != nil {
		t.Fatal(err)
	}
	var firstFeedID, secondFeedID int64
	if err := pool.QueryRow(ctx, `INSERT INTO feeds (title, url, folder_id, last_checked_at) VALUES ('Old', 'HTTPS://Example.com:443/rss#old', $1, NOW()-INTERVAL '2 minutes') RETURNING id`, folderID).Scan(&firstFeedID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO feeds (title, url, folder_id, last_checked_at, last_error) VALUES ('New', 'https://example.com/rss', $1, NOW()-INTERVAL '1 minute', 'temporary failure') RETURNING id`, folderID).Scan(&secondFeedID); err != nil {
		t.Fatal(err)
	}
	firstPublished := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	secondPublished := firstPublished.Add(24 * time.Hour)
	if _, err := pool.Exec(ctx, `INSERT INTO articles (feed_id, guid, title, published_at, is_read, is_saved) VALUES ($1, 'same-guid', 'Older copy', $2, FALSE, FALSE)`, firstFeedID, firstPublished); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO articles (feed_id, guid, title, published_at, is_read, is_saved) VALUES ($1, 'same-guid', 'Latest copy', $2, TRUE, TRUE)`, secondFeedID, secondPublished); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateSharedSources(ctx, pool, adminID); err != nil {
		t.Fatalf("MigrateSharedSources() error = %v", err)
	}
	var folderMigrationAppliedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT applied_at FROM schema_migrations WHERE name='006_folder_customization.sql'`).Scan(&folderMigrationAppliedAt); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateSharedSources(ctx, pool, adminID); err != nil {
		t.Fatalf("repeat shared-source migration error = %v", err)
	}
	var folderMigrationApplied bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name='006_folder_customization.sql')`).Scan(&folderMigrationApplied); err != nil {
		t.Fatal(err)
	}
	if !folderMigrationApplied {
		t.Fatal("folder customization migration was not recorded by its file name")
	}
	var folderMigrationAppliedAtAfterRepeat time.Time
	if err := pool.QueryRow(ctx, `SELECT applied_at FROM schema_migrations WHERE name='006_folder_customization.sql'`).Scan(&folderMigrationAppliedAtAfterRepeat); err != nil {
		t.Fatal(err)
	}
	if !folderMigrationAppliedAtAfterRepeat.Equal(folderMigrationAppliedAt) {
		t.Fatal("already-applied folder migration timestamp changed on repeat")
	}
	var sourceCount, subscriptionCount, articleCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM feed_sources`).Scan(&sourceCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_feeds`).Scan(&subscriptionCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM articles`).Scan(&articleCount); err != nil {
		t.Fatal(err)
	}
	if sourceCount != 1 || subscriptionCount != 1 || articleCount != 1 {
		t.Fatalf("migrated counts = sources:%d subscriptions:%d articles:%d, want 1 each", sourceCount, subscriptionCount, articleCount)
	}
	var lastCheckedAfterSuccess bool
	var lastError *string
	var hasLastSuccess bool
	if err := pool.QueryRow(ctx, `SELECT COALESCE(last_checked_at > last_success_at,FALSE),last_error,last_success_at IS NOT NULL FROM feed_sources`).Scan(&lastCheckedAfterSuccess, &lastError, &hasLastSuccess); err != nil {
		t.Fatal(err)
	}
	if !lastCheckedAfterSuccess || lastError == nil || *lastError != "temporary failure" || !hasLastSuccess {
		t.Fatalf("merged source check history = later:%v error:%v has_success:%v, want failed latest attempt and preserved earlier success", lastCheckedAfterSuccess, lastError, hasLastSuccess)
	}
	var title string
	var publishedAt time.Time
	var isRead, isSaved bool
	if err := pool.QueryRow(ctx, `SELECT a.title, a.published_at, s.is_read, s.is_saved FROM articles a JOIN user_article_states s ON s.article_id = a.id WHERE s.user_id = $1`, adminID).Scan(&title, &publishedAt, &isRead, &isSaved); err != nil {
		t.Fatal(err)
	}
	if title != "Latest copy" || !publishedAt.Equal(secondPublished) || isRead || !isSaved {
		t.Fatalf("merged article = (%q, %v, read:%v saved:%v), want latest content and unread/saved state", title, publishedAt, isRead, isSaved)
	}

	var readerID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, role) VALUES ('reader@example.com', 'test-hash', 'Reader') RETURNING id`).Scan(&readerID); err != nil {
		t.Fatal(err)
	}
	readerFolder, err := store.New(pool).CreateFolderForUser(ctx, readerID, "My folder", store.DefaultFolderColor)
	if err != nil {
		t.Fatal(err)
	}
	var normalizedURL string
	if err := pool.QueryRow(ctx, `SELECT normalized_url FROM feed_sources LIMIT 1`).Scan(&normalizedURL); err != nil {
		t.Fatal(err)
	}
	readerFeed, err := store.New(pool).SubscribeFeed(ctx, readerID, normalizedURL, feeds.Document{Title: "ignored"}, &readerFolder.ID)
	if err != nil {
		t.Fatal(err)
	}
	adminFeeds, err := store.New(pool).ListFeedsForUser(ctx, adminID)
	if err != nil || len(adminFeeds) != 1 {
		t.Fatalf("ListFeedsForUser(admin) = %#v, %v; want one subscription", adminFeeds, err)
	}
	readerFeeds, err := store.New(pool).ListFeedsForUser(ctx, readerID)
	if err != nil || len(readerFeeds) != 1 || readerFeeds[0].ID != readerFeed.ID {
		t.Fatalf("ListFeedsForUser(reader) = %#v, %v; want only reader subscription", readerFeeds, err)
	}
	adminFolders, err := store.New(pool).ListFoldersForUser(ctx, adminID)
	if err != nil {
		t.Fatal(err)
	}
	readerFolders, err := store.New(pool).ListFoldersForUser(ctx, readerID)
	if err != nil || len(readerFolders) != 1 || readerFolders[0].ID != readerFolder.ID {
		t.Fatalf("ListFoldersForUser(reader) = %#v, %v; want only reader folder", readerFolders, err)
	}
	if len(adminFolders) == 0 || adminFolders[0].ID == readerFolder.ID {
		t.Fatalf("admin folders include reader's folder: %#v", adminFolders)
	}
	readerArticles, err := store.New(pool).ListArticlesForUser(ctx, readerID, "all", nil, nil, "", nil, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(readerArticles) != 1 || readerArticles[0].FeedID != readerFeed.ID || readerArticles[0].IsRead || readerArticles[0].IsSaved {
		t.Fatalf("reader articles = %#v, want shared article with private default state and reader subscription id", readerArticles)
	}
	read := true
	if _, err := store.New(pool).UpdateArticleForUser(ctx, readerID, readerArticles[0].ID, &read, nil); err != nil {
		t.Fatal(err)
	}
	adminArticle, err := store.New(pool).ListArticlesForUser(ctx, adminID, "all", nil, nil, "", nil, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(adminArticle) != 1 || adminArticle[0].IsRead || !adminArticle[0].IsSaved {
		t.Fatalf("admin state changed after reader update: %#v", adminArticle)
	}
	if _, err := store.New(pool).SubscribeFeed(ctx, readerID, normalizedURL, feeds.Document{}, &folderID); err == nil {
		t.Fatal("subscription to another user's folder must be rejected")
	}
}

func TestFolderMigrationRecognizesPreviousPhaseMarker(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPostgres(t)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	adminID, err := store.EnsureInitialAdministrator(ctx, pool, "admin@example.com", "a-long-bootstrap-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE folders ADD COLUMN color TEXT NOT NULL DEFAULT 'amber'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations(name) VALUES ('001_pulse.sql:folder_customization')`); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateSharedSources(ctx, pool, adminID); err != nil {
		t.Fatal(err)
	}
	var fileMarkerApplied bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name='006_folder_customization.sql')`).Scan(&fileMarkerApplied); err != nil {
		t.Fatal(err)
	}
	if !fileMarkerApplied {
		t.Fatal("legacy phase marker was not upgraded to the migration filename marker")
	}
}
