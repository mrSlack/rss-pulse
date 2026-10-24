package feeds_test

import (
	"net/url"
	"testing"

	"github.com/pulse-reader/app/internal/feeds"
)

func TestParseAcceptsTitledEmptyRSSFeed(t *testing.T) {
	base, _ := url.Parse("https://example.com/feed.xml")
	document, err := feeds.Parse([]byte(`<rss version="2.0"><channel><title>Quiet feed</title><link>https://example.com</link></channel></rss>`), base)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if document.Title != "Quiet feed" || len(document.Items) != 0 {
		t.Fatalf("Parse() = %#v, want titled feed with no items", document)
	}
}

func TestParseRejectsDocumentAfterFeed(t *testing.T) {
	base, _ := url.Parse("https://example.com/feed.xml")
	_, err := feeds.Parse([]byte(`<rss><channel><title>Feed</title></channel></rss><rss/>`), base)
	if err == nil {
		t.Fatal("Parse() accepted a second XML root element")
	}
}

func TestParseRejectsRSSWithoutChannel(t *testing.T) {
	base, _ := url.Parse("https://example.com/feed.xml")
	_, err := feeds.Parse([]byte(`<rss/>`), base)
	if err == nil {
		t.Fatal("Parse() accepted RSS without a channel title")
	}
}

func TestParseAtomResolvesArticleURLAndDate(t *testing.T) {
	base, _ := url.Parse("https://example.com/feed.xml")
	data := []byte(`<feed xmlns="http://www.w3.org/2005/Atom"><title>Atom source</title><link href="https://example.com/"/><entry><id>urn:article:1</id><title>New item</title><link rel="alternate" href="/articles/1"/><updated>2026-10-24T09:00:00Z</updated><summary>Short summary</summary></entry></feed>`)
	document, err := feeds.Parse(data, base)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(document.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(document.Items))
	}
	item := document.Items[0]
	if item.URL != "https://example.com/articles/1" {
		t.Errorf("item URL = %q, want absolute article URL", item.URL)
	}
	if !item.HasPublishedAt {
		t.Error("HasPublishedAt = false, want true")
	}
}

func TestParseDoesNotTreatMissingDateAsPublishedDate(t *testing.T) {
	base, _ := url.Parse("https://example.com/feed.xml")
	document, err := feeds.Parse([]byte(`<rss><channel><title>Feed</title><item><guid>one</guid><title>Undated</title></item></channel></rss>`), base)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if document.Items[0].HasPublishedAt {
		t.Fatal("HasPublishedAt = true, want false for an undated item")
	}
}
