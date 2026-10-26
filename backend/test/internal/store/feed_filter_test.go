package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/pulse-reader/app/internal/feeds"
	"github.com/pulse-reader/app/internal/store"
)

func TestSourceRefreshLogsAndSkipsBlockedArticles(t *testing.T) {
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
	var readerID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email,password_hash,role) VALUES ('reader@example.com','test-hash','Reader') RETURNING id`).Scan(&readerID); err != nil {
		t.Fatal(err)
	}
	var sourceID int64
	if err := pool.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url,added_by_user_id,lease_token,lease_expires_at)
		VALUES ('https://example.com/rss','Example News','https://example.com/rss',$1,'test-lease',NOW()+INTERVAL '1 minute') RETURNING id`, readerID).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_feeds (user_id,feed_source_id) VALUES ($1,$2)`, readerID, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO feed_stop_words (term) VALUES ('very bad phrase'),('hidden phrase')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO articles (feed_source_id,guid,title,url) VALUES ($1,'blocked','Previously stored matching article','https://example.com/blocked')`, sourceID); err != nil {
		t.Fatal(err)
	}
	lease := store.SourceLease{ID: sourceID, Token: "test-lease"}
	document := feeds.Document{Title: "Example News", Items: []feeds.Item{
		{GUID: "blocked", Title: "New Very Bad-Phrase promotion", URL: "https://example.com/blocked", PublishedAt: time.Now().UTC()},
		{GUID: "blocked-summary", Title: "Plain headline", URL: "https://example.com/blocked-summary", Summary: "<p>Hidden&nbsp;phrase in the description</p>", PublishedAt: time.Now().UTC()},
		{GUID: "allowed", Title: "Ordinary science news", URL: "https://example.com/allowed", PublishedAt: time.Now().UTC()},
	}}
	if err := store.New(pool).CommitSourceRefresh(ctx, lease, document); err != nil {
		t.Fatalf("CommitSourceRefresh() error = %v", err)
	}
	var articleCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM articles WHERE feed_source_id=$1`, sourceID).Scan(&articleCount); err != nil {
		t.Fatal(err)
	}
	if articleCount != 2 {
		t.Fatalf("stored article count = %d, want the clean and previously stored articles", articleCount)
	}
	var articleTitle, feedTitle, feedURL, addedBy, term string
	if err := pool.QueryRow(ctx, `SELECT ba.article_title,ba.feed_title,ba.feed_url,u.email,ba.matched_term
		FROM blocked_feed_articles ba LEFT JOIN users u ON u.id=ba.added_by_user_id WHERE ba.feed_source_id=$1 AND ba.article_guid='blocked'`, sourceID).
		Scan(&articleTitle, &feedTitle, &feedURL, &addedBy, &term); err != nil {
		t.Fatal(err)
	}
	if articleTitle != document.Items[0].Title || feedTitle != document.Title || feedURL != "https://example.com/rss" || addedBy != "reader@example.com" || term != "very bad phrase" {
		t.Fatalf("blocked log = (%q,%q,%q,%q,%q), want article/feed/submitter/term details", articleTitle, feedTitle, feedURL, addedBy, term)
	}
	var summaryTerm string
	if err := pool.QueryRow(ctx, `SELECT matched_term FROM blocked_feed_articles WHERE feed_source_id=$1 AND article_guid='blocked-summary'`, sourceID).Scan(&summaryTerm); err != nil {
		t.Fatal(err)
	}
	if summaryTerm != "hidden phrase" {
		t.Fatalf("summary-blocked term = %q, want %q", summaryTerm, "hidden phrase")
	}
	if _, err := pool.Exec(ctx, `UPDATE feed_sources SET lease_token='second-lease',lease_expires_at=NOW()+INTERVAL '1 minute' WHERE id=$1`, sourceID); err != nil {
		t.Fatal(err)
	}
	if err := store.New(pool).CommitSourceRefresh(ctx, store.SourceLease{ID: sourceID, Token: "second-lease"}, document); err != nil {
		t.Fatal(err)
	}
	var logCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM blocked_feed_articles WHERE feed_source_id=$1`, sourceID).Scan(&logCount); err != nil {
		t.Fatal(err)
	}
	if logCount != 2 {
		t.Fatalf("blocked log rows after repeated refresh = %d, want two deduplicated rows", logCount)
	}
}
