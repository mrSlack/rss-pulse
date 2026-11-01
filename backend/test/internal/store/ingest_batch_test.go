package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/pulse-reader/app/internal/feeds"
)

type storedArticle struct {
	guid, title, url, summary string
	published                 time.Time
}

func (f dedupFixture) storedArticles(t *testing.T) []storedArticle {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), `SELECT guid,title,url,summary,published_at FROM articles WHERE feed_source_id=$1 ORDER BY id`, f.sourceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []storedArticle
	for rows.Next() {
		var a storedArticle
		if err := rows.Scan(&a.guid, &a.title, &a.url, &a.summary, &a.published); err != nil {
			t.Fatal(err)
		}
		result = append(result, a)
	}
	return result
}

// The refresh processes items in feed order, so later items see the effects of
// earlier ones in the same refresh: inserts, GUID renames, and updates.
func TestSourceRefreshAppliesItemsInOrderWithinOneRefresh(t *testing.T) {
	f := newDedupFixture(t)
	t0 := time.Date(2026, 10, 30, 12, 0, 0, 123456789, time.UTC)

	f.refresh(t,
		feeds.Item{GUID: "x", Title: "Inserted", URL: "https://example.com/u1", Summary: "s", PublishedAt: t0, HasPublishedAt: true},
		feeds.Item{GUID: "y", Title: "Merged into earlier insert", URL: "https://example.com/u1", Summary: "s2", PublishedAt: t0.Add(10 * time.Minute), HasPublishedAt: true},
		feeds.Item{GUID: "d", Title: "first", URL: "https://example.com/d", Summary: "one", PublishedAt: t0.Add(2 * time.Hour), HasPublishedAt: true},
		feeds.Item{GUID: "d", Title: "second", URL: "https://example.com/d2", Summary: "two", PublishedAt: t0.Add(3 * time.Hour), HasPublishedAt: false},
	)
	got := f.storedArticles(t)
	if len(got) != 2 {
		t.Fatalf("articles = %+v, want 2", got)
	}
	if got[0].guid != "y" || got[0].title != "Merged into earlier insert" || !got[0].published.Equal(t0.Add(10*time.Minute).Truncate(time.Microsecond)) {
		t.Fatalf("first article = %+v, want merged GUID y at t0+10m", got[0])
	}
	if got[1].guid != "d" || got[1].title != "second" || got[1].url != "https://example.com/d2" || !got[1].published.Equal(t0.Add(2*time.Hour).Truncate(time.Microsecond)) {
		t.Fatalf("second article = %+v, want second version with first publication time", got[1])
	}
}

func TestSourceRefreshRenamesChainWithoutConflicts(t *testing.T) {
	f := newDedupFixture(t)
	t0 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	f.refresh(t,
		feeds.Item{GUID: "g1", Title: "A", URL: "https://example.com/a", Summary: "a", PublishedAt: t0, HasPublishedAt: true},
		feeds.Item{GUID: "g2", Title: "B", URL: "https://example.com/b", Summary: "b", PublishedAt: t0.Add(time.Minute), HasPublishedAt: true},
	)
	ids := f.articleIDs(t)
	f.markReadAndSaved(t, ids[0])

	// g3 takes over article A by URL; then g1 is unknown and takes over B by URL.
	f.refresh(t,
		feeds.Item{GUID: "g3", Title: "A2", URL: "https://example.com/a", Summary: "a", PublishedAt: t0, HasPublishedAt: true},
		feeds.Item{GUID: "g1", Title: "B2", URL: "https://example.com/b", Summary: "b", PublishedAt: t0.Add(time.Minute), HasPublishedAt: true},
		feeds.Item{GUID: "g4", Title: "New", URL: "https://example.com/new", Summary: "n", PublishedAt: t0.Add(5 * time.Hour), HasPublishedAt: true},
	)
	got := f.storedArticles(t)
	if len(got) != 3 || got[0].guid != "g3" || got[0].title != "A2" || got[1].guid != "g1" || got[1].title != "B2" || got[2].guid != "g4" {
		t.Fatalf("articles = %+v, want A as g3, B as g1, new g4", got)
	}
	f.assertReadAndSaved(t, ids[0], "g3")
}

func TestSourceRefreshUnchangedItemsKeepArticles(t *testing.T) {
	f := newDedupFixture(t)
	t0 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	items := []feeds.Item{
		{GUID: "a", Title: "A", URL: "https://example.com/a", Summary: "<p>a</p>", PublishedAt: t0, HasPublishedAt: true},
		{GUID: "b", Title: "B", URL: "", Summary: "b", PublishedAt: t0.Add(time.Hour), HasPublishedAt: true},
	}
	f.refresh(t, items...)
	before := f.articleIDs(t)
	f.refresh(t, items...)
	undated := items[1]
	undated.HasPublishedAt = false
	undated.PublishedAt = time.Now()
	f.refresh(t, items[0], undated)
	after := f.articleIDs(t)
	if len(after) != 2 || after[0] != before[0] || after[1] != before[1] {
		t.Fatalf("article ids changed from %v to %v", before, after)
	}
	got := f.storedArticles(t)
	if !got[1].published.Equal(t0.Add(time.Hour)) {
		t.Fatalf("undated refresh changed publication time to %v", got[1].published)
	}
}

func TestSourceRefreshQuarantinesRepeatedGUIDWithinOneRefresh(t *testing.T) {
	f := newDedupFixture(t)
	ctx := context.Background()
	if _, err := f.store.AddStopWord(ctx, "forbidden"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	f.refresh(t,
		feeds.Item{GUID: "q", Title: "Forbidden first", URL: "https://example.com/q", Summary: "one", PublishedAt: now, HasPublishedAt: true},
		feeds.Item{GUID: "q", Title: "Harmless second", URL: "https://example.com/q", Summary: "two", PublishedAt: now, HasPublishedAt: true},
		feeds.Item{GUID: "ok", Title: "Harmless", URL: "https://example.com/ok", Summary: "three", PublishedAt: now, HasPublishedAt: true},
	)
	got := f.storedArticles(t)
	if len(got) != 1 || got[0].guid != "ok" {
		t.Fatalf("articles = %+v, want only the harmless article", got)
	}
	blocked, err := f.store.ListBlockedFeedArticles(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) != 1 || blocked[0].ArticleTitle != "Harmless second" || blocked[0].MatchedTerm != "forbidden" || blocked[0].Summary != "two" || blocked[0].Status != "pending" {
		t.Fatalf("blocked = %+v, want one pending record updated by the repeated GUID", blocked)
	}
}
