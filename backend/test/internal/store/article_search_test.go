package store_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/pulse-reader/app/internal/feeds"
	"github.com/pulse-reader/app/internal/store"
)

func (f dedupFixture) searchGUIDs(t *testing.T, query string) []string {
	t.Helper()
	ctx := context.Background()
	articles, err := f.store.ListArticlesForUser(ctx, f.userID, "all", nil, nil, query, nil, 200, 0, nil)
	if err != nil {
		t.Fatalf("ListArticlesForUser(q=%q) error = %v", query, err)
	}
	guids := make([]string, 0, len(articles))
	for _, article := range articles {
		var guid string
		if err := f.pool.QueryRow(ctx, `SELECT guid FROM articles WHERE id=$1`, article.ID).Scan(&guid); err != nil {
			t.Fatal(err)
		}
		guids = append(guids, guid)
	}
	sort.Strings(guids)
	return guids
}

func assertSearch(t *testing.T, f dedupFixture, query string, want ...string) {
	t.Helper()
	got := f.searchGUIDs(t, query)
	if len(got) != len(want) {
		t.Fatalf("search %q = %v, want %v", query, got, want)
	}
	sort.Strings(want)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("search %q = %v, want %v", query, got, want)
		}
	}
}

func searchFixtureItems() []feeds.Item {
	base := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	return []feeds.Item{
		{GUID: "ru", Title: "Москва: Новости дня", URL: "https://example.com/ru", Summary: "<p>Экономика <b>РАСТЁТ</b> быстрее</p>", PublishedAt: base, HasPublishedAt: true},
		{GUID: "de", Title: "Straße in MÜNCHEN gesperrt", URL: "https://example.com/de", Summary: "<p><b>Größere</b>&nbsp;Umleitung über Äcker</p>", PublishedAt: base.Add(2 * time.Hour), HasPublishedAt: true},
		{GUID: "markup", Title: "Plain words", URL: "https://example.com/markup", Summary: `<a href="https://tracker.example/x" class="headline">Link text</a> 100% sure_thing &amp; more C:\temp`, PublishedAt: base.Add(4 * time.Hour), HasPublishedAt: true},
		{GUID: "other", Title: "Percentages", URL: "https://example.com/other", Summary: "<p>One hundred percent, surely thinking</p>", PublishedAt: base.Add(6 * time.Hour), HasPublishedAt: true},
	}
}

func TestArticleSearchMatchesCyrillicAndGermanSubstringsCaseInsensitively(t *testing.T) {
	f := newDedupFixture(t)
	f.refresh(t, searchFixtureItems()...)

	assertSearch(t, f, "москва", "ru")
	assertSearch(t, f, "МОСКВА", "ru")
	assertSearch(t, f, "эконом", "ru")
	assertSearch(t, f, "растёт", "ru")
	assertSearch(t, f, "Новости ДНЯ", "ru")
	assertSearch(t, f, "straße", "de")
	assertSearch(t, f, "STRAẞE", "de")
	assertSearch(t, f, "münchen", "de")
	assertSearch(t, f, "größere umleitung", "de")
	assertSearch(t, f, "ÜBER ÄCKER", "de")
	assertSearch(t, f, "ünch", "de")
	assertSearch(t, f, "e", "de", "markup", "other")
}

func TestArticleSearchMatchesTextNotMarkupAndTreatsWildcardsLiterally(t *testing.T) {
	f := newDedupFixture(t)
	f.refresh(t, searchFixtureItems()...)

	assertSearch(t, f, "tracker")
	assertSearch(t, f, "href")
	assertSearch(t, f, "headline")
	assertSearch(t, f, "<b>")
	assertSearch(t, f, "&amp;")
	assertSearch(t, f, "link text", "markup")
	assertSearch(t, f, "& more", "markup")
	assertSearch(t, f, "100%", "markup")
	assertSearch(t, f, "%", "markup")
	assertSearch(t, f, "sure_th", "markup")
	assertSearch(t, f, "_", "markup")
	assertSearch(t, f, "sure%thing")
	assertSearch(t, f, "s_re")
	assertSearch(t, f, `c:\temp`, "markup")
	assertSearch(t, f, `\`, "markup")
	assertSearch(t, f, `\t`, "markup")
}

func TestArticleSearchFollowsUpdatedAndMergedArticles(t *testing.T) {
	f := newDedupFixture(t)
	published := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	f.refresh(t, feeds.Item{GUID: "a", Title: "First headline", URL: "https://example.com/a", Summary: "alpha", PublishedAt: published, HasPublishedAt: true})
	assertSearch(t, f, "alpha", "a")

	f.refresh(t, feeds.Item{GUID: "a", Title: "First headline", URL: "https://example.com/a", Summary: "bravo", PublishedAt: published, HasPublishedAt: true})
	assertSearch(t, f, "alpha")
	assertSearch(t, f, "bravo", "a")

	// A changed GUID with the same URL merges into the stored article, which must
	// then be found by its new text.
	f.refresh(t, feeds.Item{GUID: "a2", Title: "Renamed headline", URL: "https://example.com/a", Summary: "charlie", PublishedAt: published.Add(10 * time.Minute), HasPublishedAt: true})
	assertSearch(t, f, "bravo")
	assertSearch(t, f, "renamed", "a2")
}

func TestArticleSearchFindsArticlesAllowedFromQuarantine(t *testing.T) {
	f := newDedupFixture(t)
	ctx := context.Background()
	if _, err := f.store.AddStopWord(ctx, "forbidden"); err != nil {
		t.Fatal(err)
	}
	f.refresh(t, feeds.Item{GUID: "q", Title: "Forbidden fruit", URL: "https://example.com/q", Summary: "<p>Quarantined Ärger</p>", PublishedAt: time.Now().UTC(), HasPublishedAt: true})
	assertSearch(t, f, "ärger")
	blocked, err := f.store.ListBlockedFeedArticles(ctx, 10)
	if err != nil || len(blocked) != 1 {
		t.Fatalf("blocked = %v, err = %v", blocked, err)
	}
	if err := f.store.ModerateBlockedFeedArticle(ctx, blocked[0].ID, true, f.userID); err != nil {
		t.Fatal(err)
	}
	assertSearch(t, f, "ÄRGER", "q")
	assertSearch(t, f, "<p>")
}

func TestArticleSearchTextStripsMarkupAndLowerCases(t *testing.T) {
	cases := map[string][2]string{
		"привет мир":            {"ПРИВЕТ", "<p>Мир</p>"},
		"straße äöü":            {"STRAẞE", "<b>ÄÖÜ</b>"},
		"title a & b < c":       {"Title", "<div class=\"x\">a &amp; b &lt; c</div>"},
		"title one two":         {"Title", "one<br/>two"},
		"title spaced out text": {"Title", " spaced \n\t out <i></i> text "},
		"only title":            {"Only Title", ""},
	}
	for want, input := range cases {
		if got := store.ArticleSearchText(input[0], input[1]); got != want {
			t.Errorf("ArticleSearchText(%q, %q) = %q, want %q", input[0], input[1], got, want)
		}
	}
}
