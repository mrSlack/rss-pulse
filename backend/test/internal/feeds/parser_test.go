package feeds_test

import (
	"net/url"
	"testing"
	"time"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"

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

func TestParseDecodesKOI8RFeed(t *testing.T) {
	base, _ := url.Parse("http://example.com/feed.rss")
	data := []byte(`<?xml version="1.0" encoding="KOI8-R"?><rss><channel><title>Новости</title><item><guid>one</guid><title>Обновление</title><description>Описание</description></item></channel></rss>`)
	encoded, _, err := transform.Bytes(charmap.KOI8R.NewEncoder(), data)
	if err != nil {
		t.Fatalf("encode KOI8-R fixture: %v", err)
	}
	document, err := feeds.Parse(encoded, base)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if document.Title != "Новости" || len(document.Items) != 1 || document.Items[0].Title != "Обновление" || document.Items[0].Summary != "Описание" {
		t.Fatalf("Parse() = %#v, want Russian feed title and item decoded from KOI8-R", document)
	}
}

func TestParseRecognizesRealWorldPublicationDates(t *testing.T) {
	base, _ := url.Parse("https://example.com/feed.xml")
	cases := []struct {
		raw  string
		want string
	}{
		{"Mon, 02 Jan 2006 15:04:05 GMT", "2006-01-02T15:04:05Z"},
		{"Mon, 2 Jan 2006 15:04:05 GMT", "2006-01-02T15:04:05Z"},
		{"Mon, 2 Jan 2006 15:04:05 +0300", "2006-01-02T12:04:05Z"},
		{"Mon,  2 Jan 2006  15:04:05   +0000", "2006-01-02T15:04:05Z"},
		{"2 Jan 2006 15:04:05 +0000", "2006-01-02T15:04:05Z"},
		{"Mon, 02 Jan 2006 15:04 +0000", "2006-01-02T15:04:00Z"},
		{"Mon, 02 Jan 06 15:04:05 +0000", "2006-01-02T15:04:05Z"},
		{"02 Jan 06 15:04 +0000", "2006-01-02T15:04:00Z"},
		{"02 Jan 06 15:04 GMT", "2006-01-02T15:04:00Z"},
		{"02 Jan 06 15:04:05 +0000", "2006-01-02T15:04:05Z"},
		{"02 Jan 06 15:04:05 EST", "2006-01-02T20:04:05Z"},
		{"Mon, 02 Jan 06 15:04 +0000", "2006-01-02T15:04:00Z"},
		{"Mon, 02 Jan 06 15:04 PDT", "2006-01-02T22:04:00Z"},
		{"02 Jan 2006 15:04 GMT", "2006-01-02T15:04:00Z"},
		{"Mon, 02 Jan 2006 15:04 EST", "2006-01-02T20:04:00Z"},
		{"Mon, 02 Jan 2006 15:04:05 EST", "2006-01-02T20:04:05Z"},
		{"Mon, 02 Jan 2006 15:04:05 PDT", "2006-01-02T22:04:05Z"},
		{"Mon, 02 Jan 2006 15:04:05 UT", "2006-01-02T15:04:05Z"},
		{"Mon, 02 Jan 2006 15:04:05 +03:00", "2006-01-02T12:04:05Z"},
		{"2006-01-02T15:04:05Z", "2006-01-02T15:04:05Z"},
		{"2006-01-02T15:04:05.123+02:00", "2006-01-02T13:04:05.123Z"},
		{"2006-01-02T15:04:05", "2006-01-02T15:04:05Z"},
		{"2006-01-02 15:04:05", "2006-01-02T15:04:05Z"},
		{"2006-01-02", "2006-01-02T00:00:00Z"},
	}
	for _, testCase := range cases {
		t.Run(testCase.raw, func(t *testing.T) {
			data := []byte(`<rss><channel><title>Feed</title><item><guid>one</guid><title>Item</title><pubDate>` + testCase.raw + `</pubDate></item></channel></rss>`)
			document, err := feeds.Parse(data, base)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			item := document.Items[0]
			if !item.HasPublishedAt {
				t.Fatalf("HasPublishedAt = false for %q", testCase.raw)
			}
			want, _ := time.Parse(time.RFC3339Nano, testCase.want)
			if !item.PublishedAt.Equal(want) {
				t.Fatalf("PublishedAt = %s, want %s", item.PublishedAt.UTC().Format(time.RFC3339Nano), testCase.want)
			}
		})
	}
}

func TestParseRejectsUnrecognizedPublicationDate(t *testing.T) {
	base, _ := url.Parse("https://example.com/feed.xml")
	document, err := feeds.Parse([]byte(`<rss><channel><title>Feed</title><item><guid>one</guid><title>Item</title><pubDate>yesterday</pubDate></item></channel></rss>`), base)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if document.Items[0].HasPublishedAt {
		t.Fatal("HasPublishedAt = true for an unparseable date")
	}
}

func TestParseAtomKeepsXHTMLContentMarkup(t *testing.T) {
	base, _ := url.Parse("https://example.com/feed.xml")
	data := []byte(`<feed xmlns="http://www.w3.org/2005/Atom"><title>Atom source</title>
<entry><id>urn:xhtml-content</id><title>XHTML content</title>
<content type="xhtml"><div xmlns="http://www.w3.org/1999/xhtml"><p>Hello <b>world</b> &amp; friends</p><img src="/a.png"/></div></content></entry>
<entry><id>urn:xhtml-summary</id><title>XHTML summary</title>
<summary type="xhtml">
  <div xmlns="http://www.w3.org/1999/xhtml"><p>Short <em>summary</em></p></div>
</summary></entry>
<entry><id>urn:html</id><title>Escaped HTML</title>
<content type="html">&lt;p&gt;Escaped &amp;amp; decoded once&lt;/p&gt;</content></entry>
<entry><id>urn:text</id><title>Plain text</title><summary>Just text</summary></entry>
</feed>`)
	document, err := feeds.Parse(data, base)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want := map[string]string{
		"urn:xhtml-content": `<p>Hello <b>world</b> &amp; friends</p><img src="/a.png"/>`,
		"urn:xhtml-summary": `<p>Short <em>summary</em></p>`,
		"urn:html":          `<p>Escaped &amp; decoded once</p>`,
		"urn:text":          `Just text`,
	}
	if len(document.Items) != len(want) {
		t.Fatalf("got %d items, want %d", len(document.Items), len(want))
	}
	for _, item := range document.Items {
		if item.Summary != want[item.GUID] {
			t.Errorf("%s summary = %q, want %q", item.GUID, item.Summary, want[item.GUID])
		}
	}
}

func TestParseRDFUsesAboutAttributeAsGUID(t *testing.T) {
	base, _ := url.Parse("https://example.com/feed.rdf")
	data := []byte(`<?xml version="1.0"?>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns="http://purl.org/rss/1.0/">
<channel rdf:about="https://example.com/"><title>RDF source</title><link>https://example.com/</link></channel>
<item rdf:about="https://example.com/items/1#stable"><title>First</title><link>https://example.com/items/1</link></item>
<item><title>Second</title><link>https://example.com/items/2</link></item>
</rdf:RDF>`)
	document, err := feeds.Parse(data, base)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(document.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(document.Items))
	}
	if document.Items[0].GUID != "https://example.com/items/1#stable" {
		t.Errorf("first GUID = %q, want rdf:about", document.Items[0].GUID)
	}
	if document.Items[1].GUID != "https://example.com/items/2" {
		t.Errorf("second GUID = %q, want link fallback", document.Items[1].GUID)
	}
}
