package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pulse-reader/app/internal/httpapi"
	"github.com/pulse-reader/app/internal/store"
	"github.com/pulse-reader/app/internal/syncer"
)

// markReadFixture describes the library built by newMarkReadFixture. The
// administrator subscribes to the shared source (in a folder) and to a second,
// unfiled source; the reader subscribes to the shared source and to a source
// the administrator does not follow.
type markReadFixture struct {
	pool          *pgxpool.Pool
	handler       http.Handler
	adminCookie   *http.Cookie
	adminID       int64
	readerID      int64
	adminFolderID int64
	sharedFeedID  int64 // administrator's subscription to the shared source
	unfiledFeedID int64
	now           time.Time
	shared        []int64 // shared source articles, newest first (1h apart)
	unfiled       []int64 // unfiled source articles, newest first (1h apart)
	readerOnly    []int64
	preexisting   int64 // the fixture's saved, unread "only-admin" article
}

func insertMarkReadArticles(t *testing.T, pool *pgxpool.Pool, sourceID int64, prefix string, newest time.Time, count int) []int64 {
	t.Helper()
	ids := make([]int64, count)
	for i := range count {
		title := prefix + " story " + intString(int64(i))
		if err := pool.QueryRow(context.Background(), `INSERT INTO articles (feed_source_id,guid,title,published_at) VALUES ($1,$2,$3,$4) RETURNING id`,
			sourceID, prefix+intString(int64(i)), title, newest.Add(-time.Duration(i)*time.Hour)).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

func newMarkReadFixture(t *testing.T) markReadFixture {
	t.Helper()
	ctx := context.Background()
	pool, adminID, readerID := isolatedAPIDatabase(t)
	f := markReadFixture{pool: pool, adminID: adminID, readerID: readerID, now: time.Now().UTC().Truncate(time.Second)}
	f.handler = httpapi.New(store.New(pool), syncer.NewControl(), false)
	f.adminCookie = loginForTest(t, f.handler, "admin@example.com", "a-long-admin-password", false)
	var sharedSourceID int64
	if err := pool.QueryRow(ctx, `SELECT id, feed_source_id, folder_id FROM user_feeds WHERE user_id=$1`, adminID).Scan(&f.sharedFeedID, &sharedSourceID, &f.adminFolderID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM articles WHERE guid='only-admin'`).Scan(&f.preexisting); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE articles SET published_at=$2 WHERE id=$1`, f.preexisting, f.now.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var unfiledSourceID, readerSourceID int64
	if err := pool.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url,last_success_at) VALUES ('https://unfiled.example/rss','Unfiled','https://unfiled.example/rss',NOW()) RETURNING id`).Scan(&unfiledSourceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url,last_success_at) VALUES ('https://reader.example/rss','Reader only','https://reader.example/rss',NOW()) RETURNING id`).Scan(&readerSourceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO user_feeds (user_id,feed_source_id) VALUES ($1,$2) RETURNING id`, adminID, unfiledSourceID).Scan(&f.unfiledFeedID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_feeds (user_id,feed_source_id) VALUES ($1,$2),($1,$3)`, readerID, sharedSourceID, readerSourceID); err != nil {
		t.Fatal(err)
	}
	f.shared = insertMarkReadArticles(t, pool, sharedSourceID, "shared", f.now.Add(-time.Hour), 4)
	f.unfiled = insertMarkReadArticles(t, pool, unfiledSourceID, "unfiled", f.now.Add(-time.Hour), 3)
	f.readerOnly = insertMarkReadArticles(t, pool, readerSourceID, "readeronly", f.now.Add(-time.Hour), 2)
	return f
}

func (f markReadFixture) markRead(t *testing.T, body string) (int, string, int) {
	t.Helper()
	response := authenticatedRequest(t, f.handler, http.MethodPost, "/api/articles/mark-read", body, f.adminCookie)
	var decoded struct {
		Marked *int   `json:"marked"`
		Code   string `json:"code"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &decoded)
	marked := -1
	if decoded.Marked != nil {
		marked = *decoded.Marked
	}
	return response.Code, decoded.Code, marked
}

// readIDs returns the IDs of articles userID has marked read.
func readIDs(t *testing.T, pool *pgxpool.Pool, userID int64) map[int64]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT article_id FROM user_article_states WHERE user_id=$1 AND is_read`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		result[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func expectRead(t *testing.T, got map[int64]bool, want ...[]int64) {
	t.Helper()
	expected := map[int64]bool{}
	for _, ids := range want {
		for _, id := range ids {
			expected[id] = true
		}
	}
	for id := range expected {
		if !got[id] {
			t.Errorf("article %d is not read, want read", id)
		}
	}
	for id := range got {
		if !expected[id] {
			t.Errorf("article %d is read, want unread", id)
		}
	}
}

func rfc3339(value time.Time) string { return value.Format(time.RFC3339) }

func TestMarkReadAllRespectsUpToAndOwnership(t *testing.T) {
	f := newMarkReadFixture(t)
	// Already read articles are not counted again.
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO user_article_states (user_id,article_id,is_read) VALUES ($1,$2,TRUE)`, f.adminID, f.unfiled[2]); err != nil {
		t.Fatal(err)
	}
	upTo := f.now.Add(-2 * time.Hour) // excludes the newest article of each source
	status, code, marked := f.markRead(t, `{"filter":"all","upTo":"`+rfc3339(upTo)+`"}`)
	if status != http.StatusOK || marked != 5 {
		t.Fatalf("mark-read = %d %s marked=%d, want 200 marked=5", status, code, marked)
	}
	expectRead(t, readIDs(t, f.pool, f.adminID), f.shared[1:], f.unfiled[1:], []int64{f.preexisting})
	expectRead(t, readIDs(t, f.pool, f.readerID))

	var saved bool
	if err := f.pool.QueryRow(context.Background(), `SELECT is_saved FROM user_article_states WHERE user_id=$1 AND article_id=$2`, f.adminID, f.preexisting).Scan(&saved); err != nil || !saved {
		t.Fatalf("saved flag of a marked article = %v, %v; want kept", saved, err)
	}
	var foreignStates int
	if err := f.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM user_article_states WHERE user_id=$1 AND article_id = ANY($2)`, f.adminID, f.readerOnly).Scan(&foreignStates); err != nil || foreignStates != 0 {
		t.Fatalf("states for unsubscribed articles = %d, %v; want 0", foreignStates, err)
	}

	status, _, marked = f.markRead(t, `{"filter":"all","upTo":"`+rfc3339(upTo)+`"}`)
	if status != http.StatusOK || marked != 0 {
		t.Fatalf("repeated mark-read = %d marked=%d, want 200 marked=0", status, marked)
	}
}

func TestMarkReadIsScopedToFeedFolderAndFilter(t *testing.T) {
	cases := []struct {
		name string
		body func(f markReadFixture) string
		want func(f markReadFixture) [][]int64
	}{
		{"feed", func(f markReadFixture) string {
			return `{"filter":"all","feedId":` + intString(f.unfiledFeedID) + `,"upTo":"` + rfc3339(f.now) + `"}`
		}, func(f markReadFixture) [][]int64 { return [][]int64{f.unfiled} }},
		{"folder", func(f markReadFixture) string {
			return `{"filter":"unread","folderId":` + intString(f.adminFolderID) + `,"upTo":"` + rfc3339(f.now) + `"}`
		}, func(f markReadFixture) [][]int64 { return [][]int64{f.shared, {f.preexisting}} }},
		{"saved", func(f markReadFixture) string {
			return `{"filter":"saved","upTo":"` + rfc3339(f.now) + `"}`
		}, func(f markReadFixture) [][]int64 { return [][]int64{{f.preexisting}} }},
		{"today", func(f markReadFixture) string {
			return `{"filter":"today","since":"` + rfc3339(f.now.Add(-150*time.Minute)) + `","upTo":"` + rfc3339(f.now) + `"}`
		}, func(f markReadFixture) [][]int64 { return [][]int64{f.shared[:2], f.unfiled[:2]} }},
		{"search", func(f markReadFixture) string {
			return `{"filter":"all","q":"  UNFILED story 1 ","upTo":"` + rfc3339(f.now) + `"}`
		}, func(f markReadFixture) [][]int64 { return [][]int64{{f.unfiled[1]}} }},
		{"short search", func(f markReadFixture) string {
			return `{"filter":"all","q":"y 2","upTo":"` + rfc3339(f.now) + `"}`
		}, func(f markReadFixture) [][]int64 { return [][]int64{{f.shared[2], f.unfiled[2]}} }},
		{"other user's feed", func(f markReadFixture) string {
			var readerFeedID int64
			if err := f.pool.QueryRow(context.Background(), `SELECT id FROM user_feeds WHERE user_id=$1 ORDER BY id DESC LIMIT 1`, f.readerID).Scan(&readerFeedID); err != nil {
				panic(err)
			}
			return `{"filter":"all","feedId":` + intString(readerFeedID) + `,"upTo":"` + rfc3339(f.now) + `"}`
		}, func(f markReadFixture) [][]int64 { return nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newMarkReadFixture(t)
			want := tc.want(f)
			total := 0
			for _, ids := range want {
				total += len(ids)
			}
			status, code, marked := f.markRead(t, tc.body(f))
			if status != http.StatusOK || marked != total {
				t.Fatalf("mark-read = %d %s marked=%d, want 200 marked=%d", status, code, marked, total)
			}
			expectRead(t, readIDs(t, f.pool, f.adminID), want...)
			expectRead(t, readIDs(t, f.pool, f.readerID))
		})
	}
}

func TestMarkReadValidatesLikeArticleList(t *testing.T) {
	f := newMarkReadFixture(t)
	upTo := rfc3339(f.now)
	long := make([]byte, 201)
	for i := range long {
		long[i] = 'a'
	}
	cases := []struct {
		body string
		code string
	}{
		{`{"filter":"all"}`, "article.date_invalid"},
		{`{"filter":"all","upTo":"yesterday"}`, "article.date_invalid"},
		{`{"filter":"today","since":"nope","upTo":"` + upTo + `"}`, "article.date_invalid"},
		{`{"filter":"recent","upTo":"` + upTo + `"}`, "article.filter_invalid"},
		{`{"filter":"all","feedId":0,"upTo":"` + upTo + `"}`, "feed.id_invalid"},
		{`{"filter":"all","folderId":-3,"upTo":"` + upTo + `"}`, "folder.id_invalid"},
		{`{"filter":"all","q":"` + string(long) + `","upTo":"` + upTo + `"}`, "article.search_too_long"},
		{`{"filter":"all","upTo":"` + upTo + `","extra":true}`, "request.invalid_json"},
	}
	for _, tc := range cases {
		status, code, _ := f.markRead(t, tc.body)
		if status != http.StatusBadRequest || code != tc.code {
			t.Errorf("mark-read %s = %d %s, want 400 %s", tc.body, status, code, tc.code)
		}
	}
	expectRead(t, readIDs(t, f.pool, f.adminID))

	// An empty filter means "all", like GET /api/articles.
	status, code, marked := f.markRead(t, `{"upTo":"`+upTo+`"}`)
	if status != http.StatusOK || marked != 8 {
		t.Fatalf("mark-read without filter = %d %s marked=%d, want 200 marked=8", status, code, marked)
	}
	response := authenticatedRequest(t, f.handler, http.MethodPost, "/api/articles/mark-read", `{"upTo":"`+upTo+`"}`, nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous mark-read = %d, want 401", response.Code)
	}
}

// latestArticleID lists the administrator's articles and returns the
// latestArticleId bound the response reports.
func (f markReadFixture) latestArticleID(t *testing.T) int64 {
	t.Helper()
	response := authenticatedRequest(t, f.handler, http.MethodGet, "/api/articles?filter=all", "", f.adminCookie)
	var decoded struct {
		LatestArticleID *int64 `json:"latestArticleId"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || response.Code != http.StatusOK || decoded.LatestArticleID == nil {
		t.Fatalf("GET /api/articles = %d %s, want 200 with latestArticleId", response.Code, response.Body.String())
	}
	return *decoded.LatestArticleID
}

func TestMarkReadUpToIDLeavesArticlesIngestedLaterUnread(t *testing.T) {
	f := newMarkReadFixture(t)
	latest := f.latestArticleID(t)
	var maxID int64
	if err := f.pool.QueryRow(context.Background(), `SELECT MAX(id) FROM articles`).Scan(&maxID); err != nil || latest != maxID {
		t.Fatalf("latestArticleId = %d, want %d (%v)", latest, maxID, err)
	}
	var sharedSourceID int64
	if err := f.pool.QueryRow(context.Background(), `SELECT feed_source_id FROM user_feeds WHERE id=$1`, f.sharedFeedID).Scan(&sharedSourceID); err != nil {
		t.Fatal(err)
	}
	// Arrives after the list was loaded but carries an old publication date, so
	// a publication-time bound alone would mark it read unseen.
	backdated := insertMarkReadArticles(t, f.pool, sharedSourceID, "backdated", f.now.Add(-48*time.Hour), 1)

	status, code, marked := f.markRead(t, `{"filter":"all","upToId":`+intString(latest)+`}`)
	if status != http.StatusOK || marked != 8 {
		t.Fatalf("mark-read = %d %s marked=%d, want 200 marked=8", status, code, marked)
	}
	expectRead(t, readIDs(t, f.pool, f.adminID), f.shared, f.unfiled, []int64{f.preexisting})
	if readIDs(t, f.pool, f.adminID)[backdated[0]] {
		t.Fatal("backdated article ingested after loading was marked read")
	}
}

func TestMarkReadAppliesBothBounds(t *testing.T) {
	// With both bounds, an article must satisfy both.
	f := newMarkReadFixture(t)
	latest := f.latestArticleID(t)
	status, code, marked := f.markRead(t, `{"filter":"all","upTo":"`+rfc3339(f.now.Add(-2*time.Hour))+`","upToId":`+intString(latest)+`}`)
	if status != http.StatusOK || marked != 6 {
		t.Fatalf("mark-read with both bounds = %d %s marked=%d, want 200 marked=6", status, code, marked)
	}
	expectRead(t, readIDs(t, f.pool, f.adminID), f.shared[1:], f.unfiled[1:], []int64{f.preexisting})

	status, code, _ = f.markRead(t, `{"filter":"all","upToId":-1}`)
	if status != http.StatusBadRequest || code != "article.id_invalid" {
		t.Fatalf("mark-read with negative upToId = %d %s, want 400 article.id_invalid", status, code)
	}
}
