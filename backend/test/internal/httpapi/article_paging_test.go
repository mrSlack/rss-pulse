package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pulse-reader/app/internal/httpapi"
	"github.com/pulse-reader/app/internal/store"
	"github.com/pulse-reader/app/internal/syncer"
)

type articlePage struct {
	Items []struct {
		ID     int64 `json:"id"`
		IsRead bool  `json:"isRead"`
	} `json:"items"`
	HasMore    bool    `json:"hasMore"`
	NextCursor *string `json:"nextCursor"`
}

// seedPagingArticles adds count articles to the administrator's only source.
// Every third article shares its publication time with the next one so that
// page boundaries also fall between rows that differ only by ID.
func seedPagingArticles(t *testing.T, pool *pgxpool.Pool, adminID int64, prefix string, newest time.Time, count int) []int64 {
	t.Helper()
	ctx := context.Background()
	var sourceID int64
	if err := pool.QueryRow(ctx, `SELECT feed_source_id FROM user_feeds WHERE user_id=$1 ORDER BY id LIMIT 1`, adminID).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, count)
	for i := range count {
		publishedAt := newest.Add(-time.Duration(i-i%3/2) * time.Minute)
		if err := pool.QueryRow(ctx, `INSERT INTO articles (feed_source_id,guid,title,published_at) VALUES ($1,$2,$3,$4) RETURNING id`,
			sourceID, prefix+intString(int64(i)), prefix+" article "+intString(int64(i)), publishedAt).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

func unreadArticleIDs(t *testing.T, pool *pgxpool.Pool, userID int64) map[int64]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT a.id FROM articles a JOIN user_feeds uf ON uf.feed_source_id=a.feed_source_id
		LEFT JOIN user_article_states s ON s.user_id=uf.user_id AND s.article_id=a.id
		WHERE uf.user_id=$1 AND COALESCE(s.is_read,FALSE)=FALSE`, userID)
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
	return result
}

func orderedArticleIDs(t *testing.T, pool *pgxpool.Pool, userID int64) []int64 {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT a.id FROM articles a JOIN user_feeds uf ON uf.feed_source_id=a.feed_source_id
		WHERE uf.user_id=$1 ORDER BY a.published_at DESC, a.id DESC`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		result = append(result, id)
	}
	return result
}

func getArticlePage(t *testing.T, handler http.Handler, cookie *http.Cookie, params url.Values) articlePage {
	t.Helper()
	response := authenticatedRequest(t, handler, http.MethodGet, "/api/articles?"+params.Encode(), "", cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/articles?%s = %d %s", params.Encode(), response.Code, response.Body.String())
	}
	var page articlePage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.HasMore != (page.NextCursor != nil) {
		t.Fatalf("hasMore=%v but nextCursor=%v", page.HasMore, page.NextCursor)
	}
	return page
}

func TestUnreadCursorPagingDoesNotSkipArticlesMarkedReadBetweenPages(t *testing.T) {
	pool, adminID, _ := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false)
	cookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	seedPagingArticles(t, pool, adminID, "paging", time.Now().UTC().Add(-48*time.Hour), 23)
	initiallyUnread := unreadArticleIDs(t, pool, adminID)
	if len(initiallyUnread) != 24 {
		t.Fatalf("seeded unread count = %d, want 24", len(initiallyUnread))
	}

	seen := map[int64]int{}
	params := url.Values{"filter": {"unread"}, "limit": {"5"}}
	for pageNumber := 0; ; pageNumber++ {
		if pageNumber > 10 {
			t.Fatal("paging did not terminate")
		}
		page := getArticlePage(t, handler, cookie, params)
		for index, item := range page.Items {
			seen[item.ID]++
			if index%2 == 1 {
				continue
			}
			// Opening some displayed articles shrinks the server-side unread
			// set, which made offset paging skip unread articles.
			response := authenticatedRequest(t, handler, http.MethodPatch, "/api/articles/"+intString(item.ID), `{"isRead":true}`, cookie)
			if response.Code != http.StatusOK {
				t.Fatalf("mark %d read = %d %s", item.ID, response.Code, response.Body.String())
			}
		}
		if !page.HasMore {
			break
		}
		params.Set("before", *page.NextCursor)
	}
	for id := range initiallyUnread {
		if seen[id] != 1 {
			t.Errorf("article %d was returned %d times, want exactly once", id, seen[id])
		}
	}
	if len(seen) != len(initiallyUnread) {
		t.Fatalf("returned %d distinct articles, want %d", len(seen), len(initiallyUnread))
	}
}

func TestCursorPagingIgnoresArticlesInsertedBetweenPages(t *testing.T) {
	pool, adminID, _ := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false)
	cookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	newest := time.Now().UTC().Add(-48 * time.Hour)
	seedPagingArticles(t, pool, adminID, "original", newest, 12)
	ordered := orderedArticleIDs(t, pool, adminID)
	if len(ordered) != 13 {
		t.Fatalf("seeded article count = %d, want 13", len(ordered))
	}

	params := url.Values{"filter": {"all"}, "limit": {"6"}}
	first := getArticlePage(t, handler, cookie, params)
	if !first.HasMore || len(first.Items) != 6 {
		t.Fatalf("first page = %d items hasMore=%v, want 6 and more", len(first.Items), first.HasMore)
	}
	// Newer articles arrive from a refresh between the two page loads.
	seedPagingArticles(t, pool, adminID, "fresh", newest.Add(time.Hour), 4)

	params.Set("before", *first.NextCursor)
	second := getArticlePage(t, handler, cookie, params)
	got := make([]int64, 0, len(second.Items))
	for _, item := range second.Items {
		got = append(got, item.ID)
	}
	want := ordered[6:12]
	if len(got) != len(want) {
		t.Fatalf("second page ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("second page ids = %v, want %v", got, want)
		}
	}
	if !second.HasMore {
		t.Fatal("second page must report the remaining article")
	}
	params.Set("before", *second.NextCursor)
	last := getArticlePage(t, handler, cookie, params)
	if last.HasMore || last.NextCursor != nil || len(last.Items) != 1 {
		t.Fatalf("last page = %+v, want the single remaining article and no cursor", last)
	}
}

func TestArticleListRejectsInvalidAndConflictingPagination(t *testing.T) {
	pool, _, _ := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false)
	cookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	valid := store.EncodeArticleCursor(store.ArticleCursor{PublishedAt: time.Now(), ID: 1})
	cases := []struct {
		query string
		code  string
	}{
		{"before=not-a-cursor", "article.cursor_invalid"},
		{"before=", "article.cursor_invalid"},
		{"offset=20&before=" + url.QueryEscape(valid), "article.pagination_conflict"},
	}
	for _, tc := range cases {
		response := authenticatedRequest(t, handler, http.MethodGet, "/api/articles?"+tc.query, "", cookie)
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		if response.Code != http.StatusBadRequest || body.Code != tc.code {
			t.Errorf("GET /api/articles?%s = %d %s, want 400 %s", tc.query, response.Code, response.Body.String(), tc.code)
		}
	}
	legacy := getArticlePage(t, handler, cookie, url.Values{"offset": {"0"}})
	if len(legacy.Items) != 1 || legacy.HasMore {
		t.Fatalf("offset paging must keep working: %+v", legacy)
	}
}
