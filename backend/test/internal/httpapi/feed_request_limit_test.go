package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
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

func TestAdminFeedRequestListIsLimited(t *testing.T) {
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

	all := authenticatedRequest(t, handler, http.MethodGet, "/api/admin/feed-requests", "", adminCookie)
	var requests []store.FeedRequest
	if all.Code != http.StatusOK || json.Unmarshal(all.Body.Bytes(), &requests) != nil || len(requests) != 5 {
		t.Fatalf("default list = %d %s, want a plain array of 5", all.Code, all.Body.String())
	}
	limited := authenticatedRequest(t, handler, http.MethodGet, "/api/admin/feed-requests?limit=2", "", adminCookie)
	requests = nil
	if limited.Code != http.StatusOK || json.Unmarshal(limited.Body.Bytes(), &requests) != nil || len(requests) != 2 {
		t.Fatalf("limit=2 list = %d %s, want 2 requests", limited.Code, limited.Body.String())
	}
	if requests[0].URL != "https://pending.example/feed-0.xml" {
		t.Fatalf("limited list must keep oldest-first order, got %q first", requests[0].URL)
	}
	for _, raw := range []string{"0", "201", "-1", "abc", "1.5"} {
		response := authenticatedRequest(t, handler, http.MethodGet, "/api/admin/feed-requests?limit="+raw, "", adminCookie)
		assertErrorCode(t, response, http.StatusBadRequest, "feed.requests_limit_invalid")
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
