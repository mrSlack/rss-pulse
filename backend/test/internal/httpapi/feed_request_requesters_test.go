package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pulse-reader/app/internal/httpapi"
	"github.com/pulse-reader/app/internal/store"
	"github.com/pulse-reader/app/internal/syncer"
)

func subscriptionsTo(t *testing.T, pool *pgxpool.Pool, userID int64, normalizedURL string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM user_feeds uf JOIN feed_sources fs ON fs.id=uf.feed_source_id WHERE uf.user_id=$1 AND fs.normalized_url=$2`, userID, normalizedURL).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func pendingRequests(t *testing.T, handler http.Handler, adminCookie *http.Cookie) []store.FeedRequest {
	t.Helper()
	response := authenticatedRequest(t, handler, http.MethodGet, "/api/admin/feed-requests", "", adminCookie)
	var requests []store.FeedRequest
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &requests) != nil {
		t.Fatalf("list feed requests = %d %s", response.Code, response.Body.String())
	}
	return requests
}

func TestSecondRequesterJoinsPendingRequestAndBothAreSubscribed(t *testing.T) {
	pool, _, readerID := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false)
	secondID := createTestUser(t, pool, "second@example.com", "a-long-second-password", "Reader")
	adminCookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	readerCookie := loginForTest(t, handler, "reader@example.com", "a-long-reader-password", false)
	secondCookie := loginForTest(t, handler, "second@example.com", "a-long-second-password", false)

	first := authenticatedRequest(t, handler, http.MethodPost, "/api/feeds", `{"url":"https://shared.example/rss","folderId":null}`, readerCookie)
	second := authenticatedRequest(t, handler, http.MethodPost, "/api/feeds", `{"url":"https://shared.example/rss","folderId":null}`, secondCookie)
	for name, response := range map[string]int{"first": first.Code, "second": second.Code} {
		if response != http.StatusAccepted {
			t.Fatalf("%s requester = %d, want 202 (%s / %s)", name, response, first.Body.String(), second.Body.String())
		}
	}
	requests := pendingRequests(t, handler, adminCookie)
	if len(requests) != 1 {
		t.Fatalf("pending requests = %+v, want one shared request", requests)
	}
	if requests[0].RequestedBy != "reader@example.com" || len(requests[0].Requesters) != 2 || requests[0].Requesters[1] != "second@example.com" {
		t.Fatalf("request requesters = %q / %q, want reader first and both listed", requests[0].RequestedBy, requests[0].Requesters)
	}
	approved := authenticatedRequest(t, handler, http.MethodPost, "/api/admin/feed-requests/"+intString(requests[0].ID)+"/approve", "{}", adminCookie)
	if approved.Code != http.StatusNoContent {
		t.Fatalf("approve = %d %s, want 204", approved.Code, approved.Body.String())
	}
	for _, userID := range []int64{readerID, secondID} {
		if count := subscriptionsTo(t, pool, userID, "https://shared.example/rss"); count != 1 {
			t.Fatalf("user %d subscriptions after approval = %d, want 1", userID, count)
		}
	}
	if remaining := pendingRequests(t, handler, adminCookie); len(remaining) != 0 {
		t.Fatalf("requests after approval = %+v, want none", remaining)
	}
}

func TestApprovalSkipsRequesterAtFeedLimit(t *testing.T) {
	ctx := context.Background()
	pool, _, readerID := isolatedAPIDatabase(t)
	repository := store.New(pool)
	handler := httpapi.New(repository, syncer.NewControl(), false)
	secondID := createTestUser(t, pool, "second@example.com", "a-long-second-password", "Reader")
	adminCookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	if _, err := repository.RequestFeedApproval(ctx, readerID, "https://shared.example/rss", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RequestFeedApproval(ctx, secondID, "https://shared.example/rss", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	// The reader reaches the subscription limit while the request is pending.
	for index := 0; index < store.MaxFeedsPerUser; index++ {
		var sourceID int64
		feedURL := "https://filler.example/" + strconv.Itoa(index)
		if err := pool.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url) VALUES ($1,'Filler',$1) RETURNING id`, feedURL).Scan(&sourceID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO user_feeds (user_id,feed_source_id) VALUES ($1,$2)`, readerID, sourceID); err != nil {
			t.Fatal(err)
		}
	}
	requests := pendingRequests(t, handler, adminCookie)
	approved := authenticatedRequest(t, handler, http.MethodPost, "/api/admin/feed-requests/"+intString(requests[0].ID)+"/approve", "{}", adminCookie)
	if approved.Code != http.StatusNoContent {
		t.Fatalf("approve with one requester at limit = %d %s, want 204", approved.Code, approved.Body.String())
	}
	if count := subscriptionsTo(t, pool, readerID, "https://shared.example/rss"); count != 0 {
		t.Fatalf("requester at limit was subscribed (%d)", count)
	}
	if count := subscriptionsTo(t, pool, secondID, "https://shared.example/rss"); count != 1 {
		t.Fatalf("second requester subscriptions = %d, want 1", count)
	}
}

func TestReplaceRequestForAlreadySubscribedSourceIsSkippedOnApproval(t *testing.T) {
	ctx := context.Background()
	pool, _, readerID := isolatedAPIDatabase(t)
	repository := store.New(pool)
	handler := httpapi.New(repository, syncer.NewControl(), false)
	secondID := createTestUser(t, pool, "second@example.com", "a-long-second-password", "Reader")
	adminCookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	readerCookie := loginForTest(t, handler, "reader@example.com", "a-long-reader-password", false)

	var oldSourceID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM feed_sources WHERE normalized_url='https://example.com/rss'`).Scan(&oldSourceID); err != nil {
		t.Fatal(err)
	}
	oldFeed, err := repository.SubscribeSource(ctx, readerID, oldSourceID, nil)
	if err != nil {
		t.Fatal(err)
	}
	replace := authenticatedRequest(t, handler, http.MethodPatch, "/api/feeds/"+intString(oldFeed.ID), `{"title":"Renamed","url":"https://target.example/rss","folderId":null}`, readerCookie)
	if replace.Code != http.StatusAccepted {
		t.Fatalf("replace request = %d %s", replace.Code, replace.Body.String())
	}
	if _, err := repository.RequestFeedApproval(ctx, secondID, "https://target.example/rss", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	// Before approval the target source appears by another path and the reader
	// subscribes to it, so applying the replacement would duplicate the subscription.
	var targetSourceID int64
	if err := pool.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url) VALUES ('https://target.example/rss','Target','https://target.example/rss') RETURNING id`).Scan(&targetSourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.SubscribeSource(ctx, readerID, targetSourceID, nil); err != nil {
		t.Fatal(err)
	}
	requests := pendingRequests(t, handler, adminCookie)
	if len(requests) != 1 {
		t.Fatalf("pending requests = %+v, want 1", requests)
	}
	approved := authenticatedRequest(t, handler, http.MethodPost, "/api/admin/feed-requests/"+intString(requests[0].ID)+"/approve", "{}", adminCookie)
	if approved.Code != http.StatusNoContent {
		t.Fatalf("approve duplicate replacement = %d %s, want 204", approved.Code, approved.Body.String())
	}
	// The skipped replacement leaves both of the reader's subscriptions as they were.
	var oldStillThere int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_feeds WHERE id=$1 AND feed_source_id=$2`, oldFeed.ID, oldSourceID).Scan(&oldStillThere); err != nil {
		t.Fatal(err)
	}
	if oldStillThere != 1 || subscriptionsTo(t, pool, readerID, "https://target.example/rss") != 1 {
		t.Fatalf("old subscription kept = %d, target subscriptions = %d; want 1 and 1", oldStillThere, subscriptionsTo(t, pool, readerID, "https://target.example/rss"))
	}
	if subscriptionsTo(t, pool, secondID, "https://target.example/rss") != 1 {
		t.Fatal("other requester must still be subscribed")
	}
}

func TestPendingRequestCapCountsJoinedRequests(t *testing.T) {
	ctx := context.Background()
	pool, adminID, readerID := isolatedAPIDatabase(t)
	repository := store.New(pool)
	handler := httpapi.New(repository, syncer.NewControl(), false)
	readerCookie := loginForTest(t, handler, "reader@example.com", "a-long-reader-password", false)
	// Requests created by another user count once the reader joins them.
	for index := 0; index < store.MaxPendingFeedRequestsPerUser; index++ {
		feedURL := "https://pending.example/feed-" + strconv.Itoa(index) + ".xml"
		if _, err := repository.RequestFeedApproval(ctx, adminID, feedURL, "", nil, nil); err != nil {
			t.Fatal(err)
		}
		joined := authenticatedRequest(t, handler, http.MethodPost, "/api/feeds", `{"url":"`+feedURL+`","folderId":null}`, readerCookie)
		if joined.Code != http.StatusAccepted {
			t.Fatalf("join request %d = %d %s", index, joined.Code, joined.Body.String())
		}
	}
	otherID := createTestUser(t, pool, "other@example.com", "a-long-other-password", "Reader")
	if _, err := repository.RequestFeedApproval(ctx, otherID, "https://pending.example/other-only.xml", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	joinOverCap := authenticatedRequest(t, handler, http.MethodPost, "/api/feeds", `{"url":"https://pending.example/other-only.xml","folderId":null}`, readerCookie)
	assertErrorCode(t, joinOverCap, http.StatusConflict, "feed.request_limit_reached")
	var readerRows int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM feed_request_requesters WHERE user_id=$1`, readerID).Scan(&readerRows); err != nil {
		t.Fatal(err)
	}
	if readerRows != store.MaxPendingFeedRequestsPerUser {
		t.Fatalf("reader requester rows = %d, want %d", readerRows, store.MaxPendingFeedRequestsPerUser)
	}
}

func TestRejectingSharedRequestRemovesItForAllRequesters(t *testing.T) {
	ctx := context.Background()
	pool, adminID, readerID := isolatedAPIDatabase(t)
	repository := store.New(pool)
	handler := httpapi.New(repository, syncer.NewControl(), false)
	adminCookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	for _, userID := range []int64{readerID, adminID} {
		if _, err := repository.RequestFeedApproval(ctx, userID, "https://shared.example/rss", "", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	requests := pendingRequests(t, handler, adminCookie)
	rejected := authenticatedRequest(t, handler, http.MethodDelete, "/api/admin/feed-requests/"+intString(requests[0].ID), "", adminCookie)
	if rejected.Code != http.StatusNoContent {
		t.Fatalf("reject = %d", rejected.Code)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM feed_request_requesters`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("requester rows after rejection = %d, want 0", rows)
	}
}

func TestRequestWithoutRemainingRequestersDisappears(t *testing.T) {
	ctx := context.Background()
	pool, _, readerID := isolatedAPIDatabase(t)
	repository := store.New(pool)
	handler := httpapi.New(repository, syncer.NewControl(), false)
	adminCookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	var sourceID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM feed_sources WHERE normalized_url='https://example.com/rss'`).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	feed, err := repository.SubscribeSource(ctx, readerID, sourceID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RequestFeedApproval(ctx, readerID, "https://replacement.example/rss", "", nil, &feed.ID); err != nil {
		t.Fatal(err)
	}
	// Unsubscribing removes the replacement target and with it the only requester.
	if _, err := pool.Exec(ctx, `DELETE FROM user_feeds WHERE id=$1`, feed.ID); err != nil {
		t.Fatal(err)
	}
	if requests := pendingRequests(t, handler, adminCookie); len(requests) != 0 {
		t.Fatalf("requests without requesters = %+v, want none", requests)
	}
}

func TestRepeatedRequestReplacesRequesterIntent(t *testing.T) {
	ctx := context.Background()
	pool, _, readerID := isolatedAPIDatabase(t)
	repository := store.New(pool)
	handler := httpapi.New(repository, syncer.NewControl(), false)
	adminCookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	readerCookie := loginForTest(t, handler, "reader@example.com", "a-long-reader-password", false)

	var oldSourceID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM feed_sources WHERE normalized_url='https://example.com/rss'`).Scan(&oldSourceID); err != nil {
		t.Fatal(err)
	}
	oldFeed, err := repository.SubscribeSource(ctx, readerID, oldSourceID, nil)
	if err != nil {
		t.Fatal(err)
	}
	folder, err := repository.CreateFolderForUser(ctx, readerID, "Moved", store.DefaultFolderColor)
	if err != nil {
		t.Fatal(err)
	}
	// The reader first asks for the URL as a new feed, then edits an existing
	// subscription to the same URL. The latest request must win on approval.
	added := authenticatedRequest(t, handler, http.MethodPost, "/api/feeds", `{"url":"https://target.example/rss","folderId":null}`, readerCookie)
	if added.Code != http.StatusAccepted {
		t.Fatalf("new-feed request = %d %s", added.Code, added.Body.String())
	}
	replace := authenticatedRequest(t, handler, http.MethodPatch, "/api/feeds/"+intString(oldFeed.ID), `{"title":"Renamed","url":"https://target.example/rss","folderId":`+intString(folder.ID)+`}`, readerCookie)
	if replace.Code != http.StatusAccepted {
		t.Fatalf("replace request = %d %s", replace.Code, replace.Body.String())
	}
	requests := pendingRequests(t, handler, adminCookie)
	if len(requests) != 1 || requests[0].Title != "Renamed" || requests[0].FolderName == nil || *requests[0].FolderName != "Moved" {
		t.Fatalf("pending requests = %+v, want one request with the latest title and folder", requests)
	}
	approved := authenticatedRequest(t, handler, http.MethodPost, "/api/admin/feed-requests/"+intString(requests[0].ID)+"/approve", "{}", adminCookie)
	if approved.Code != http.StatusNoContent {
		t.Fatalf("approve = %d %s", approved.Code, approved.Body.String())
	}
	if count := subscriptionsTo(t, pool, readerID, "https://target.example/rss"); count != 1 {
		t.Fatalf("subscriptions to target = %d, want 1", count)
	}
	var targetFeedID int64
	var titleOverride *string
	var folderID *int64
	if err := pool.QueryRow(ctx, `SELECT uf.id,uf.title_override,uf.folder_id FROM user_feeds uf JOIN feed_sources fs ON fs.id=uf.feed_source_id
		WHERE uf.user_id=$1 AND fs.normalized_url='https://target.example/rss'`, readerID).Scan(&targetFeedID, &titleOverride, &folderID); err != nil {
		t.Fatal(err)
	}
	if targetFeedID != oldFeed.ID || titleOverride == nil || *titleOverride != "Renamed" || folderID == nil || *folderID != folder.ID {
		t.Fatalf("target subscription = id %d title %v folder %v, want the replaced feed %d renamed into folder %d", targetFeedID, titleOverride, folderID, oldFeed.ID, folder.ID)
	}
	if count := subscriptionsTo(t, pool, readerID, "https://example.com/rss"); count != 0 {
		t.Fatalf("old URL subscriptions = %d, want 0 after the replacement", count)
	}
}
