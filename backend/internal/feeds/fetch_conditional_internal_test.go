package feeds

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

const conditionalTestFeed = `<?xml version="1.0"?><rss version="2.0"><channel><title>Conditional</title><link>https://example.com/</link>
<item><guid>one</guid><title>First</title><link>https://example.com/1</link></item></channel></rss>`

// loopbackFetcher talks to an httptest server. Production fetches always use
// defaultFetcher, whose client dials public addresses only.
func loopbackFetcher(server *httptest.Server) fetcher {
	return fetcher{client: server.Client(), validate: func(_ context.Context, raw string) (*url.URL, error) {
		return url.Parse(raw)
	}}
}

func TestFetchStoresValidatorsAndHandlesNotModified(t *testing.T) {
	var lastIfNoneMatch, lastIfModifiedSince string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastIfNoneMatch = r.Header.Get("If-None-Match")
		lastIfModifiedSince = r.Header.Get("If-Modified-Since")
		if lastIfNoneMatch == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Sun, 01 Nov 2026 10:00:00 GMT")
		_, _ = w.Write([]byte(conditionalTestFeed))
	}))
	defer server.Close()
	fetch := loopbackFetcher(server)

	first, err := fetch.fetch(context.Background(), server.URL+"/rss", Validators{})
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if lastIfNoneMatch != "" || lastIfModifiedSince != "" {
		t.Fatalf("unconditional fetch sent validators %q / %q", lastIfNoneMatch, lastIfModifiedSince)
	}
	if first.NotModified || first.Document.Title != "Conditional" || len(first.Document.Items) != 1 {
		t.Fatalf("first fetch = %#v, want parsed document", first)
	}
	want := Validators{ETag: `"v1"`, LastModified: "Sun, 01 Nov 2026 10:00:00 GMT"}
	if first.Validators != want {
		t.Fatalf("validators = %#v, want %#v", first.Validators, want)
	}

	second, err := fetch.fetch(context.Background(), server.URL+"/rss", first.Validators)
	if err != nil {
		t.Fatalf("conditional fetch: %v", err)
	}
	if lastIfNoneMatch != `"v1"` || lastIfModifiedSince != want.LastModified {
		t.Fatalf("conditional headers = %q / %q", lastIfNoneMatch, lastIfModifiedSince)
	}
	if !second.NotModified || len(second.Document.Items) != 0 {
		t.Fatalf("conditional fetch = %#v, want NotModified without a document", second)
	}
	if second.Validators != want {
		t.Fatalf("304 validators = %#v, want the previous validators %#v", second.Validators, want)
	}
}

func TestFetchUsesUpdatedValidatorsFromNotModifiedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v2"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()
	result, err := loopbackFetcher(server).fetch(context.Background(), server.URL, Validators{ETag: `"v1"`, LastModified: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.NotModified || result.Validators != (Validators{ETag: `"v2"`, LastModified: "old"}) {
		t.Fatalf("result = %#v, want the refreshed ETag and the kept Last-Modified", result)
	}
}

func TestFetchRejectsNotModifiedWithoutValidators(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()
	if _, err := loopbackFetcher(server).fetch(context.Background(), server.URL, Validators{}); err == nil {
		t.Fatal("a 304 to an unconditional request must be an error")
	}
}

func TestDefaultFetcherStillRejectsLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(conditionalTestFeed))
	}))
	defer server.Close()
	if _, err := Fetch(context.Background(), server.URL, Validators{}); err == nil {
		t.Fatal("Fetch reached a loopback test server")
	}
}
