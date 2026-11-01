package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pulse-reader/app/internal/feeds"
	"github.com/pulse-reader/app/internal/store"
)

type dedupFixture struct {
	pool     *pgxpool.Pool
	store    *store.Store
	userID   int64
	sourceID int64
}

func newDedupFixture(t *testing.T) dedupFixture {
	t.Helper()
	ctx := context.Background()
	pool := isolatedPostgres(t)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	userID, err := store.EnsureInitialAdministrator(ctx, pool, "admin@example.com", "a-long-bootstrap-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateSharedSources(ctx, pool, userID); err != nil {
		t.Fatal(err)
	}
	var sourceID int64
	if err := pool.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url) VALUES ('https://example.com/rss','Example','https://example.com/rss') RETURNING id`).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_feeds (user_id,feed_source_id) VALUES ($1,$2)`, userID, sourceID); err != nil {
		t.Fatal(err)
	}
	return dedupFixture{pool: pool, store: store.New(pool), userID: userID, sourceID: sourceID}
}

func (f dedupFixture) refresh(t *testing.T, items ...feeds.Item) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, `UPDATE feed_sources SET lease_token='dedup',lease_expires_at=NOW()+INTERVAL '1 minute' WHERE id=$1`, f.sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CommitSourceRefresh(ctx, store.SourceLease{ID: f.sourceID, Token: "dedup"}, feeds.Document{Title: "Example", Items: items}); err != nil {
		t.Fatalf("CommitSourceRefresh() error = %v", err)
	}
}

func (f dedupFixture) articleIDs(t *testing.T) []int64 {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), `SELECT id FROM articles WHERE feed_source_id=$1 ORDER BY id`, f.sourceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func (f dedupFixture) markReadAndSaved(t *testing.T, articleID int64) {
	t.Helper()
	read, saved := true, true
	if _, err := f.store.UpdateArticleForUser(context.Background(), f.userID, articleID, &read, &saved); err != nil {
		t.Fatal(err)
	}
}

func (f dedupFixture) assertReadAndSaved(t *testing.T, articleID int64, wantGUID string) {
	t.Helper()
	article, err := f.store.GetArticleForUser(context.Background(), f.userID, articleID)
	if err != nil {
		t.Fatal(err)
	}
	if !article.IsRead || !article.IsSaved {
		t.Fatalf("merged article state = (read:%v,saved:%v), want both preserved", article.IsRead, article.IsSaved)
	}
	var guid string
	if err := f.pool.QueryRow(context.Background(), `SELECT guid FROM articles WHERE id=$1`, articleID).Scan(&guid); err != nil {
		t.Fatal(err)
	}
	if guid != wantGUID {
		t.Fatalf("merged article guid = %q, want %q", guid, wantGUID)
	}
}

func TestSourceRefreshKeepsRepeatedTitlesWithDifferentURLsSeparate(t *testing.T) {
	f := newDedupFixture(t)
	published := time.Now().UTC().Truncate(time.Second)
	f.refresh(t, feeds.Item{GUID: "episode-1", Title: "Daily digest", URL: "https://example.com/digest/1", Summary: "Monday", PublishedAt: published, HasPublishedAt: true})
	first := f.articleIDs(t)
	if len(first) != 1 {
		t.Fatalf("article count after first refresh = %d, want 1", len(first))
	}
	f.markReadAndSaved(t, first[0])

	f.refresh(t, feeds.Item{GUID: "episode-2", Title: "Daily digest", URL: "https://example.com/digest/2", Summary: "Tuesday", PublishedAt: published.Add(10 * time.Minute), HasPublishedAt: true})
	ids := f.articleIDs(t)
	if len(ids) != 2 {
		t.Fatalf("article count = %d, want 2 distinct articles with the same title", len(ids))
	}
	f.assertReadAndSaved(t, first[0], "episode-1")
	unrelated, err := f.store.GetArticleForUser(context.Background(), f.userID, ids[1])
	if err != nil {
		t.Fatal(err)
	}
	if unrelated.IsRead || unrelated.IsSaved {
		t.Fatalf("new article inherited state (read:%v,saved:%v)", unrelated.IsRead, unrelated.IsSaved)
	}
}

func TestSourceRefreshDoesNotMergeOnSummaryAlone(t *testing.T) {
	f := newDedupFixture(t)
	published := time.Now().UTC().Truncate(time.Second)
	f.refresh(t, feeds.Item{GUID: "a", Title: "First", Summary: "Read more", PublishedAt: published, HasPublishedAt: true})
	f.refresh(t, feeds.Item{GUID: "b", Title: "Second", Summary: "Read more", PublishedAt: published.Add(5 * time.Minute), HasPublishedAt: true})
	if ids := f.articleIDs(t); len(ids) != 2 {
		t.Fatalf("article count = %d, want 2 for a shared summary only", len(ids))
	}
}

func TestSourceRefreshMergesChangedGUIDWithSameURL(t *testing.T) {
	f := newDedupFixture(t)
	published := time.Now().UTC().Truncate(time.Second)
	f.refresh(t, feeds.Item{GUID: "old-guid", Title: "Launch", URL: "https://example.com/launch", Summary: "Draft", PublishedAt: published, HasPublishedAt: true})
	first := f.articleIDs(t)
	f.markReadAndSaved(t, first[0])

	f.refresh(t, feeds.Item{GUID: "new-guid", Title: "Launch (updated)", URL: "https://example.com/launch", Summary: "Final", PublishedAt: published.Add(10 * time.Minute), HasPublishedAt: true})
	ids := f.articleIDs(t)
	if len(ids) != 1 || ids[0] != first[0] {
		t.Fatalf("article ids = %v, want the original article %d updated in place", ids, first[0])
	}
	f.assertReadAndSaved(t, first[0], "new-guid")
}

func TestSourceRefreshMergesChangedGUIDWithSameTitleAndSummaryWithoutURL(t *testing.T) {
	f := newDedupFixture(t)
	published := time.Now().UTC().Truncate(time.Second)
	f.refresh(t, feeds.Item{GUID: "old-guid", Title: "Status note", Summary: "All systems normal.", PublishedAt: published, HasPublishedAt: true})
	first := f.articleIDs(t)
	f.markReadAndSaved(t, first[0])

	f.refresh(t, feeds.Item{GUID: "new-guid", Title: "Status note", Summary: "All systems normal.", PublishedAt: published.Add(-10 * time.Minute), HasPublishedAt: true})
	ids := f.articleIDs(t)
	if len(ids) != 1 || ids[0] != first[0] {
		t.Fatalf("article ids = %v, want the original article %d updated in place", ids, first[0])
	}
	f.assertReadAndSaved(t, first[0], "new-guid")
}

func TestSourceRefreshDoesNotMergeEmptyTitleAndSummary(t *testing.T) {
	f := newDedupFixture(t)
	published := time.Now().UTC().Truncate(time.Second)
	f.refresh(t, feeds.Item{GUID: "a", PublishedAt: published, HasPublishedAt: true})
	f.refresh(t, feeds.Item{GUID: "b", PublishedAt: published.Add(time.Minute), HasPublishedAt: true})
	if ids := f.articleIDs(t); len(ids) != 2 {
		t.Fatalf("article count = %d, want 2 for items without URL, title, or summary", len(ids))
	}
}
