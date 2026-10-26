package store_test

import (
	"context"
	"github.com/pulse-reader/app/internal/feeds"
	"github.com/pulse-reader/app/internal/store"
	"testing"
	"time"
)

func TestQuarantinePersistsPayloadAndDecisions(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPostgres(t)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	admin, err := store.EnsureInitialAdministrator(ctx, pool, "admin@example.com", "long-bootstrap-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateSharedSources(ctx, pool, admin); err != nil {
		t.Fatal(err)
	}
	var source int64
	if err := pool.QueryRow(ctx, `INSERT INTO feed_sources(normalized_url,url,title) VALUES ('https://example.com/rss','https://example.com/rss','News') RETURNING id`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	s := store.New(pool)
	published := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	item := feeds.Item{GUID: "blocked", Title: "adult content", URL: "https://example.com/a", Summary: "original summary", PublishedAt: published, HasPublishedAt: true}
	refresh := func() {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE feed_sources SET lease_token='test',lease_expires_at=NOW()+INTERVAL '1 minute' WHERE id=$1`, source); err != nil {
			t.Fatal(err)
		}
		if err := s.CommitSourceRefresh(ctx, store.SourceLease{ID: source, Token: "test"}, feeds.Document{Title: "News", Items: []feeds.Item{item}}); err != nil {
			t.Fatal(err)
		}
	}
	list := func() store.BlockedFeedArticle {
		t.Helper()
		rows, err := s.ListBlockedFeedArticles(ctx, 100)
		if err != nil || len(rows) != 1 {
			t.Fatalf("rows=%v err=%v", rows, err)
		}
		return rows[0]
	}
	refresh()
	row := list()
	if row.ArticleGUID != item.GUID || row.Summary != item.Summary || row.PublishedAt == nil || !row.PublishedAt.Equal(published) || row.Status != "pending" || !row.CanModerate {
		t.Fatalf("incomplete quarantine: %+v", row)
	}
	if err := s.ModerateBlockedFeedArticle(ctx, row.ID, false, admin); err != nil {
		t.Fatal(err)
	}
	item.Summary = "updated summary"
	refresh()
	row = list()
	if row.Status != "rejected" || row.Summary != item.Summary || row.DecidedAt == nil {
		t.Fatalf("refresh lost decision/content: %+v", row)
	}
	// A failed decision must roll back article promotion as well as the status.
	if err := s.ModerateBlockedFeedArticle(ctx, row.ID, true, 999999); err == nil {
		t.Fatal("invalid moderator succeeded")
	}
	if list().Status != "rejected" {
		t.Fatal("failed transaction changed status")
	}
	var premature int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM articles WHERE feed_source_id=$1`, source).Scan(&premature); err != nil {
		t.Fatal(err)
	}
	if premature != 0 {
		t.Fatal("failed transaction promoted article")
	}
	if err := s.ModerateBlockedFeedArticle(ctx, row.ID, true, admin); err != nil {
		t.Fatal(err)
	}
	if err := s.ModerateBlockedFeedArticle(ctx, row.ID, true, admin); err != nil {
		t.Fatal(err)
	}
	refresh()
	row = list()
	if row.Status != "allowed" {
		t.Fatalf("status=%s", row.Status)
	}
	var count int
	var stored time.Time
	if err := pool.QueryRow(ctx, `SELECT COUNT(*),MIN(published_at) FROM articles WHERE feed_source_id=$1`, source).Scan(&count, &stored); err != nil {
		t.Fatal(err)
	}
	if count != 1 || !stored.Equal(published) {
		t.Fatalf("promoted count=%d time=%v", count, stored)
	}
	var reader int64
	if err := pool.QueryRow(ctx, `INSERT INTO users(email,password_hash,role) VALUES ('reader@example.com','test','Reader') RETURNING id`).Scan(&reader); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubscribeSource(ctx, reader, source, nil); err != nil {
		t.Fatal(err)
	}
	visible, err := s.ListArticlesForUser(ctx, reader, "all", nil, nil, "", nil, 100, 0)
	if err != nil || len(visible) != 1 {
		t.Fatalf("subscriber articles=%v err=%v", visible, err)
	}
	hidden, err := s.ListArticlesForUser(ctx, admin, "all", nil, nil, "", nil, 100, 0)
	if err != nil || len(hidden) != 0 {
		t.Fatalf("unsubscribed articles=%v err=%v", hidden, err)
	}
	if list().CanModerate {
		t.Fatal("allowed history still moderatable")
	}
	if err := s.ModerateBlockedFeedArticle(ctx, row.ID, false, admin); err != store.ErrQuarantineUnavailable {
		t.Fatalf("allowed reject error=%v", err)
	}
	var legacy int64
	if err := pool.QueryRow(ctx, `INSERT INTO blocked_feed_articles(feed_source_id,article_guid,article_title,feed_title,feed_url,matched_term) VALUES ($1,'legacy','Legacy','News','https://example.com/rss','adult content') RETURNING id`, source).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListBlockedFeedArticles(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ID == legacy && (r.CanModerate || r.PublishedAt != nil) {
			t.Fatalf("legacy moderatable: %+v", r)
		}
	}
	if err := s.ModerateBlockedFeedArticle(ctx, legacy, true, admin); err == nil {
		t.Fatal("legacy allow succeeded")
	}
	if err := s.ModerateBlockedFeedArticle(ctx, 999999, true, admin); err != store.ErrNotFound {
		t.Fatalf("missing error=%v", err)
	}
}
