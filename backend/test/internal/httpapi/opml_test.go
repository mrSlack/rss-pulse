package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/pulse-reader/app/internal/httpapi"
	"github.com/pulse-reader/app/internal/opml"
	"github.com/pulse-reader/app/internal/store"
	"github.com/pulse-reader/app/internal/syncer"
)

type opmlImportResult struct {
	Subscribed int `json:"subscribed"`
	Requested  int `json:"requested"`
	Skipped    int `json:"skipped"`
	Failed     []struct {
		URL    string `json:"url"`
		Reason string `json:"reason"`
	} `json:"failed"`
}

func importOPML(t *testing.T, handler http.Handler, cookie *http.Cookie, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/opml", strings.NewReader(body))
	request.Host = "example.test"
	request.Header.Set("Origin", "http://example.test")
	request.Header.Set("Content-Type", contentType)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeImport(t *testing.T, response *httptest.ResponseRecorder) opmlImportResult {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("import = %d %s, want 200", response.Code, response.Body.String())
	}
	var result opmlImportResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func opmlDocument(body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><opml version="2.0"><head><title>t</title></head><body>` + body + `</body></opml>`
}

func TestOPMLExportListsPersonalSubscriptionsByFolder(t *testing.T) {
	ctx := context.Background()
	pool, adminID, readerID := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false)
	cookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	var folderName string
	if err := pool.QueryRow(ctx, `SELECT name FROM folders WHERE owner_user_id=$1`, adminID).Scan(&folderName); err != nil {
		t.Fatal(err)
	}
	var sourceID int64
	if err := pool.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url) VALUES ('https://loose.example/rss','Loose source','https://loose.example/rss') RETURNING id`).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_feeds (user_id,feed_source_id,title_override) VALUES ($1,$2,'My loose name')`, adminID, sourceID); err != nil {
		t.Fatal(err)
	}
	// Another user's subscriptions are never exported.
	var readerSource int64
	if err := pool.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url) VALUES ('https://private.example/rss','Private','https://private.example/rss') RETURNING id`).Scan(&readerSource); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_feeds (user_id,feed_source_id) VALUES ($1,$2)`, readerID, readerSource); err != nil {
		t.Fatal(err)
	}

	response := authenticatedRequest(t, handler, http.MethodGet, "/api/opml", "", cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("export = %d %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "text/x-opml; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := response.Header().Get("Content-Disposition"); got != `attachment; filename="pulse-subscriptions.opml"` {
		t.Errorf("Content-Disposition = %q", got)
	}
	if !strings.Contains(response.Body.String(), `<opml version="2.0">`) {
		t.Errorf("export is not OPML 2.0:\n%s", response.Body.String())
	}
	feeds, err := opml.Parse(strings.NewReader(response.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	want := []opml.Feed{
		{URL: "https://loose.example/rss", Title: "My loose name"},
		{URL: "https://example.com/rss", Title: "Admin feed", Folder: folderName},
	}
	if !reflect.DeepEqual(feeds, want) {
		t.Fatalf("exported feeds = %+v, want %+v", feeds, want)
	}
}

func TestOPMLImportSubscribesRequestsAndMapsFolders(t *testing.T) {
	ctx := context.Background()
	pool, _, readerID := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false)
	cookie := loginForTest(t, handler, "reader@example.com", "a-long-reader-password", false)
	var newsFolderID int64
	if err := pool.QueryRow(ctx, `INSERT INTO folders (owner_user_id,name,color) VALUES ($1,'News','blue') RETURNING id`, readerID).Scan(&newsFolderID); err != nil {
		t.Fatal(err)
	}
	var subscribedSource int64
	if err := pool.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url) VALUES ('https://mine.example/rss','Mine','https://mine.example/rss') RETURNING id`).Scan(&subscribedSource); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_feeds (user_id,feed_source_id) VALUES ($1,$2)`, readerID, subscribedSource); err != nil {
		t.Fatal(err)
	}
	document := opmlDocument(`
		<outline text="NEWS">
			<outline text="Approved" xmlUrl="https://EXAMPLE.com:443/rss"/>
			<outline text="Duplicate in file" xmlUrl="https://example.com/rss"/>
		</outline>
		<outline text="Tech">
			<outline text="Unknown" xmlUrl="https://unknown.example/feed.xml"/>
			<outline text="Bad" xmlUrl="ftp://bad.example/feed"/>
		</outline>
		<outline text="Unused">
			<outline text="Only invalid" xmlUrl="not a url"/>
		</outline>
		<outline text="Already mine" xmlUrl="https://mine.example/rss"/>`)
	result := decodeImport(t, importOPML(t, handler, cookie, "text/x-opml", document))
	if result.Subscribed != 1 || result.Requested != 1 || result.Skipped != 2 || len(result.Failed) != 2 {
		t.Fatalf("import = %+v, want 1 subscribed, 1 requested, 2 skipped, 2 failed", result)
	}
	for index, url := range []string{"ftp://bad.example/feed", "not a url"} {
		if result.Failed[index].URL != url || result.Failed[index].Reason != "feed.url_invalid" {
			t.Errorf("failed[%d] = %+v, want %s feed.url_invalid", index, result.Failed[index], url)
		}
	}
	var folderID *int64
	if err := pool.QueryRow(ctx, `SELECT uf.folder_id FROM user_feeds uf JOIN feed_sources fs ON fs.id=uf.feed_source_id WHERE uf.user_id=$1 AND fs.normalized_url='https://example.com/rss'`, readerID).Scan(&folderID); err != nil {
		t.Fatal(err)
	}
	if folderID == nil || *folderID != newsFolderID {
		t.Fatalf("approved feed folder = %v, want existing News folder %d", folderID, newsFolderID)
	}
	var requestFolder *int64
	var techName string
	if err := pool.QueryRow(ctx, `SELECT r.folder_id, f.name FROM feed_request_requesters r JOIN feed_requests fr ON fr.id=r.request_id
		JOIN folders f ON f.id=r.folder_id WHERE r.user_id=$1 AND fr.normalized_url='https://unknown.example/feed.xml'`, readerID).Scan(&requestFolder, &techName); err != nil {
		t.Fatal(err)
	}
	if techName != "Tech" {
		t.Fatalf("request folder = %q, want new folder Tech", techName)
	}
	var folders int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM folders WHERE owner_user_id=$1`, readerID).Scan(&folders); err != nil || folders != 2 {
		t.Fatalf("reader folders = %d, %v; want News and Tech only", folders, err)
	}
	var sources int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM feed_sources WHERE normalized_url LIKE '%unknown.example%'`).Scan(&sources); err != nil || sources != 0 {
		t.Fatalf("unapproved URL created %d sources, %v", sources, err)
	}

	// Importing the same file again changes nothing.
	again := decodeImport(t, importOPML(t, handler, cookie, "application/xml", document))
	if again.Subscribed != 0 || again.Requested != 1 || again.Skipped != 3 || len(again.Failed) != 2 {
		t.Fatalf("second import = %+v", again)
	}
}

func TestOPMLImportRespectsFeedAndRequestLimits(t *testing.T) {
	ctx := context.Background()
	pool, _, readerID := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false)
	cookie := loginForTest(t, handler, "reader@example.com", "a-long-reader-password", false)

	var unknown strings.Builder
	for index := range store.MaxPendingFeedRequestsPerUser + 2 {
		unknown.WriteString(`<outline text="u" xmlUrl="https://pending.example/` + strconv.Itoa(index) + `.xml"/>`)
	}
	result := decodeImport(t, importOPML(t, handler, cookie, "text/x-opml", opmlDocument(unknown.String())))
	if result.Requested != store.MaxPendingFeedRequestsPerUser || len(result.Failed) != 2 || result.Failed[0].Reason != "feed.request_limit_reached" {
		t.Fatalf("over the request cap = %+v", result)
	}

	if _, err := pool.Exec(ctx, `WITH sources AS (
			INSERT INTO feed_sources (normalized_url,title,url)
			SELECT 'https://bulk.example/' || n, 'Bulk', 'https://bulk.example/' || n FROM generate_series(1, $2::int) n RETURNING id)
		INSERT INTO user_feeds (user_id,feed_source_id) SELECT $1, id FROM sources`, readerID, store.MaxFeedsPerUser); err != nil {
		t.Fatal(err)
	}
	full := decodeImport(t, importOPML(t, handler, cookie, "text/x-opml", opmlDocument(
		`<outline text="Approved" xmlUrl="https://example.com/rss"/><outline text="Mine" xmlUrl="https://bulk.example/1"/>`)))
	if full.Subscribed != 0 || full.Skipped != 1 || len(full.Failed) != 1 || full.Failed[0].Reason != "feed.limit_reached" {
		t.Fatalf("at the feed limit = %+v", full)
	}

	var invalid strings.Builder
	for index := range 150 {
		invalid.WriteString(`<outline text="x" xmlUrl="mailto:` + strconv.Itoa(index) + `@example.com"/>`)
	}
	capped := decodeImport(t, importOPML(t, handler, cookie, "text/x-opml", opmlDocument(invalid.String())))
	if len(capped.Failed) != 100 {
		t.Fatalf("failed list length = %d, want capped at 100", len(capped.Failed))
	}
}

func TestOPMLImportRejectsInvalidUploads(t *testing.T) {
	pool, _, _ := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false)
	cookie := loginForTest(t, handler, "reader@example.com", "a-long-reader-password", false)
	tooDeep := opmlDocument(strings.Repeat(`<outline text="g">`, 6) + strings.Repeat(`</outline>`, 6))
	tooMany := opmlDocument(strings.Repeat(`<outline text="g"/>`, opml.MaxOutlines+1))
	cases := []struct {
		name        string
		contentType string
		body        string
		status      int
		code        string
	}{
		{"malformed", "text/x-opml", "<opml><body>", http.StatusBadRequest, "opml.invalid"},
		{"not opml", "application/xml", "<rss/>", http.StatusBadRequest, "opml.invalid"},
		{"too deep", "text/x-opml", tooDeep, http.StatusBadRequest, "opml.invalid"},
		{"too many", "text/x-opml", tooMany, http.StatusBadRequest, "opml.invalid"},
		{"too large", "text/x-opml", opmlDocument(`<!--` + strings.Repeat("x", 1<<20) + `-->`), http.StatusRequestEntityTooLarge, "opml.too_large"},
		{"wrong type", "application/json", `{}`, http.StatusUnsupportedMediaType, "opml.content_type_invalid"},
	}
	for _, tc := range cases {
		response := importOPML(t, handler, cookie, tc.contentType, tc.body)
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		if response.Code != tc.status || body.Code != tc.code {
			t.Errorf("%s: import = %d %s, want %d %s", tc.name, response.Code, response.Body.String(), tc.status, tc.code)
		}
	}
	anonymous := authenticatedRequest(t, handler, http.MethodGet, "/api/opml", "", nil)
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous export = %d, want 401", anonymous.Code)
	}
}
