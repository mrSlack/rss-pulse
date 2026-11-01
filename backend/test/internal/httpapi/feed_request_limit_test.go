package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/pulse-reader/app/internal/httpapi"
	"github.com/pulse-reader/app/internal/store"
	"github.com/pulse-reader/app/internal/syncer"
)

func TestPendingFeedRequestsAreCappedPerUser(t *testing.T) {
	ctx := context.Background()
	pool, _, readerID := isolatedAPIDatabase(t)
	repository := store.New(pool)
	handler := httpapi.New(repository, syncer.NewControl(), false)
	readerCookie := loginForTest(t, handler, "reader@example.com", "a-long-reader-password", false)

	var sourceID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM feed_sources WHERE normalized_url='https://example.com/rss'`).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	feed, err := repository.SubscribeSource(ctx, readerID, sourceID, nil)
	if err != nil {
		t.Fatal(err)
	}
	// One replacement request plus nine new-feed requests fill the quota of ten.
	replacement := authenticatedRequest(t, handler, http.MethodPatch, "/api/feeds/"+intString(feed.ID), `{"title":"Replacement","url":"https://pending.example/replacement.xml","folderId":null}`, readerCookie)
	if replacement.Code != http.StatusAccepted {
		t.Fatalf("replacement request = %d %s, want 202", replacement.Code, replacement.Body.String())
	}
	for index := 1; index < store.MaxPendingFeedRequestsPerUser; index++ {
		response := authenticatedRequest(t, handler, http.MethodPost, "/api/feeds", `{"url":"https://pending.example/feed-`+strconv.Itoa(index)+`.xml","folderId":null}`, readerCookie)
		if response.Code != http.StatusAccepted {
			t.Fatalf("pending request %d = %d %s, want 202", index+1, response.Code, response.Body.String())
		}
	}

	eleventh := authenticatedRequest(t, handler, http.MethodPost, "/api/feeds", `{"url":"https://pending.example/one-too-many.xml","folderId":null}`, readerCookie)
	assertErrorCode(t, eleventh, http.StatusConflict, "feed.request_limit_reached")
	replacementOverLimit := authenticatedRequest(t, handler, http.MethodPatch, "/api/feeds/"+intString(feed.ID), `{"title":"Replacement","url":"https://pending.example/another-replacement.xml","folderId":null}`, readerCookie)
	assertErrorCode(t, replacementOverLimit, http.StatusConflict, "feed.request_limit_reached")

	// Repeating a request that is already pending stays idempotent at the cap.
	repeat := authenticatedRequest(t, handler, http.MethodPost, "/api/feeds", `{"url":"https://pending.example/feed-1.xml","folderId":null}`, readerCookie)
	if repeat.Code != http.StatusAccepted {
		t.Fatalf("repeating a pending request = %d %s, want 202", repeat.Code, repeat.Body.String())
	}
	// Approved sources are subscribed directly and do not need a request slot.
	if _, err := pool.Exec(ctx, `INSERT INTO feed_sources (normalized_url,title,url) VALUES ('https://approved.example/feed.xml','Approved','https://approved.example/feed.xml')`); err != nil {
		t.Fatal(err)
	}
	approved := authenticatedRequest(t, handler, http.MethodPost, "/api/feeds", `{"url":"https://approved.example/feed.xml","folderId":null}`, readerCookie)
	if approved.Code != http.StatusCreated {
		t.Fatalf("subscribing to an approved source at the request cap = %d %s, want 201", approved.Code, approved.Body.String())
	}
	var pending int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM feed_request_requesters WHERE user_id=$1`, readerID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != store.MaxPendingFeedRequestsPerUser {
		t.Fatalf("pending requests = %d, want %d", pending, store.MaxPendingFeedRequestsPerUser)
	}
}

type feedRequestPage struct {
	Requests   []store.FeedRequest `json:"requests"`
	Total      int                 `json:"total"`
	NextCursor *string             `json:"nextCursor"`
}

func TestAdminFeedRequestListIsPagedByCursorWithTotal(t *testing.T) {
	ctx := context.Background()
	pool, _, readerID := isolatedAPIDatabase(t)
	repository := store.New(pool)
	handler := httpapi.New(repository, syncer.NewControl(), false)
	for index := 0; index < 5; index++ {
		if _, err := repository.RequestFeedApproval(ctx, readerID, "https://pending.example/feed-"+strconv.Itoa(index)+".xml", "", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	adminCookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	list := func(query string) feedRequestPage {
		t.Helper()
		response := authenticatedRequest(t, handler, http.MethodGet, "/api/admin/feed-requests"+query, "", adminCookie)
		var page feedRequestPage
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Requests == nil {
			t.Fatalf("list %q = %d %s, want a page object", query, response.Code, response.Body.String())
		}
		return page
	}
	urls := func(page feedRequestPage) string {
		result := ""
		for _, request := range page.Requests {
			result += strings.TrimSuffix(strings.TrimPrefix(request.URL, "https://pending.example/feed-"), ".xml")
		}
		return result
	}

	if page := list(""); urls(page) != "01234" || page.Total != 5 || page.NextCursor != nil {
		t.Fatalf("default list = %q, total %d, next %v; want all 5 oldest first and no cursor", urls(page), page.Total, page.NextCursor)
	}
	first := list("?limit=2")
	if urls(first) != "01" || first.Total != 5 || first.NextCursor == nil {
		t.Fatalf("limit=2 = %q, total %d, next %v; want the two oldest requests, total 5 and a cursor", urls(first), first.Total, first.NextCursor)
	}
	// Another administrator decides a request on the loaded page. Offset paging
	// would now skip request 2; the cursor continues after the last loaded request.
	if response := authenticatedRequest(t, handler, http.MethodDelete, "/api/admin/feed-requests/"+intString(first.Requests[0].ID), "", adminCookie); response.Code != http.StatusNoContent {
		t.Fatalf("reject = %d, want 204", response.Code)
	}
	second := list("?limit=2&after=" + *first.NextCursor)
	if urls(second) != "23" || second.Total != 4 || second.NextCursor == nil {
		t.Fatalf("second page = %q, total %d, next %v; want requests 2 and 3, total 4 and a cursor", urls(second), second.Total, second.NextCursor)
	}
	last := list("?limit=2&after=" + *second.NextCursor)
	if urls(last) != "4" || last.Total != 4 || last.NextCursor != nil {
		t.Fatalf("last page = %q, total %d, next %v; want request 4 and no cursor", urls(last), last.Total, last.NextCursor)
	}
	// A cursor of a request that has since been decided still marks a position.
	if response := authenticatedRequest(t, handler, http.MethodPost, "/api/admin/feed-requests/"+intString(second.Requests[1].ID)+"/approve", "{}", adminCookie); response.Code != http.StatusNoContent {
		t.Fatalf("approve = %d, want 204", response.Code)
	}
	if page := list("?limit=2&after=" + *second.NextCursor); urls(page) != "4" || page.Total != 3 {
		t.Fatalf("page after a decided request = %q, total %d; want request 4, total 3", urls(page), page.Total)
	}
	for _, raw := range []string{"0", "201", "-1", "abc", "1.5"} {
		response := authenticatedRequest(t, handler, http.MethodGet, "/api/admin/feed-requests?limit="+raw, "", adminCookie)
		assertErrorCode(t, response, http.StatusBadRequest, "feed.requests_limit_invalid")
	}
	for _, raw := range []string{"", "abc", "MTph", "-1", *first.NextCursor + "="} {
		response := authenticatedRequest(t, handler, http.MethodGet, "/api/admin/feed-requests?after="+raw, "", adminCookie)
		assertErrorCode(t, response, http.StatusBadRequest, "feed.requests_cursor_invalid")
	}
	if response := authenticatedRequest(t, handler, http.MethodGet, "/api/admin/feed-requests?limit=200", "", adminCookie); response.Code != http.StatusOK {
		t.Fatalf("limit=200 = %d, want 200", response.Code)
	}
}

func assertErrorCode(t *testing.T, response interface {
	Result() *http.Response
}, status int, code string) {
	t.Helper()
	result := response.Result()
	defer result.Body.Close()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(result.Body).Decode(&body); err != nil || result.StatusCode != status || body.Code != code {
		t.Fatalf("response = %d code %q, want %d %q", result.StatusCode, body.Code, status, code)
	}
}
