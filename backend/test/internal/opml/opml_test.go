package opml_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pulse-reader/app/internal/opml"
)

func TestParseMapsNestedOutlinesToFolders(t *testing.T) {
	document := `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0">
  <head><title>Export</title></head>
  <body>
    <outline type="rss" text="Top level" xmlUrl="https://top.example/feed"/>
    <outline text="Tech">
      <outline type="rss" text="Text name" title="Title name" xmlUrl=" https://tech.example/rss "/>
      <outline text="Deep">
        <outline text="Only text" xmlurl="https://deep.example/rss"/>
      </outline>
    </outline>
    <outline title="` + strings.Repeat("x", 61) + `">
      <outline text="Long folder" xmlUrl="https://long.example/rss"/>
    </outline>
    <outline text="   ">
      <outline text="Blank folder" xmlUrl="https://blank.example/rss"/>
    </outline>
    <outline text="Empty group"/>
  </body>
</opml>`
	feeds, err := opml.Parse(strings.NewReader(document))
	if err != nil {
		t.Fatal(err)
	}
	want := []opml.Feed{
		{URL: "https://top.example/feed", Title: "Top level"},
		{URL: "https://tech.example/rss", Title: "Title name", Folder: "Tech"},
		{URL: "https://deep.example/rss", Title: "Only text", Folder: "Deep"},
		{URL: "https://long.example/rss", Title: "Long folder"},
		{URL: "https://blank.example/rss", Title: "Blank folder"},
	}
	if !reflect.DeepEqual(feeds, want) {
		t.Fatalf("Parse() = %+v\nwant %+v", feeds, want)
	}
}

func nested(depth int) string {
	return strings.Repeat(`<outline text="g">`, depth-1) + `<outline text="f" xmlUrl="https://example.com/rss"/>` + strings.Repeat(`</outline>`, depth-1)
}

func TestParseEnforcesLimits(t *testing.T) {
	wrap := func(body string) string { return `<opml version="2.0"><body>` + body + `</body></opml>` }
	if feeds, err := opml.Parse(strings.NewReader(wrap(nested(opml.MaxDepth)))); err != nil || len(feeds) != 1 || feeds[0].Folder != "g" {
		t.Fatalf("depth %d = %+v, %v; want one feed", opml.MaxDepth, feeds, err)
	}
	if _, err := opml.Parse(strings.NewReader(wrap(nested(opml.MaxDepth + 1)))); !errors.Is(err, opml.ErrInvalid) {
		t.Fatalf("depth %d error = %v, want ErrInvalid", opml.MaxDepth+1, err)
	}
	many := strings.Repeat(`<outline text="f" xmlUrl="https://example.com/rss"/>`, opml.MaxOutlines)
	if feeds, err := opml.Parse(strings.NewReader(wrap(many))); err != nil || len(feeds) != opml.MaxOutlines {
		t.Fatalf("%d outlines = %d feeds, %v", opml.MaxOutlines, len(feeds), err)
	}
	if _, err := opml.Parse(strings.NewReader(wrap(many + `<outline text="g"/>`))); !errors.Is(err, opml.ErrInvalid) {
		t.Fatalf("%d outlines error = %v, want ErrInvalid", opml.MaxOutlines+1, err)
	}
}

func TestParseRejectsMalformedDocuments(t *testing.T) {
	for _, document := range []string{
		``,
		`not xml`,
		`<rss><channel/></rss>`,
		`<opml><body><outline text="a"></body></opml>`,
		`<?xml version="1.0" encoding="ISO-8859-1"?><opml><body/></opml>`,
		`<!DOCTYPE opml [<!ENTITY x SYSTEM "file:///etc/passwd">]><opml><body><outline text="&x;" xmlUrl="https://e.example/rss"/></body></opml>`,
	} {
		if feeds, err := opml.Parse(strings.NewReader(document)); !errors.Is(err, opml.ErrInvalid) {
			t.Errorf("Parse(%q) = %+v, %v; want ErrInvalid", document, feeds, err)
		}
	}
}

func TestWriteRoundTrips(t *testing.T) {
	var buffer bytes.Buffer
	created := time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC)
	err := opml.Write(&buffer, "Pulse subscriptions", created, []opml.Group{
		{Name: "", Subscriptions: []opml.Subscription{{Title: "Loose & free", URL: "https://loose.example/rss?a=1&b=2"}}},
		{Name: "News <daily>", Subscriptions: []opml.Subscription{{Title: "Daily", URL: "https://daily.example/rss"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	output := buffer.String()
	for _, fragment := range []string{`<?xml version="1.0" encoding="UTF-8"?>`, `<opml version="2.0">`, `<title>Pulse subscriptions</title>`, `<dateCreated>Sun, 01 Nov 2026 09:30:00 +0000</dateCreated>`, `xmlUrl="https://loose.example/rss?a=1&amp;b=2"`, `type="rss"`} {
		if !strings.Contains(output, fragment) {
			t.Errorf("output lacks %s:\n%s", fragment, output)
		}
	}
	feeds, err := opml.Parse(strings.NewReader(output))
	if err != nil {
		t.Fatal(err)
	}
	want := []opml.Feed{
		{URL: "https://loose.example/rss?a=1&b=2", Title: "Loose & free"},
		{URL: "https://daily.example/rss", Title: "Daily", Folder: "News <daily>"},
	}
	if !reflect.DeepEqual(feeds, want) {
		t.Fatalf("round trip = %+v, want %+v", feeds, want)
	}
}
