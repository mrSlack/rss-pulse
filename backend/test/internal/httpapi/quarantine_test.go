package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/pulse-reader/app/internal/httpapi"
	"github.com/pulse-reader/app/internal/store"
	"github.com/pulse-reader/app/internal/syncer"
)

func TestQuarantineAdministratorModeration(t *testing.T) {
	ctx := context.Background()
	pool, adminID, _ := isolatedAPIDatabase(t)
	handler := httpapi.New(store.New(pool), syncer.NewControl(), false)
	admin := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	reader := loginForTest(t, handler, "reader@example.com", "a-long-reader-password", false)
	published := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	var id, legacyID int64
	if err := pool.QueryRow(ctx, `INSERT INTO blocked_feed_articles
		(feed_source_id,article_guid,article_title,article_url,feed_title,feed_url,matched_term,summary,published_at)
		SELECT id,'quarantine','Blocked title','https://example.com/blocked','Admin feed',url,'adult','Full summary',$1
		FROM feed_sources WHERE normalized_url='https://example.com/rss' RETURNING id`, published).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO blocked_feed_articles
		(feed_source_id,article_guid,article_title,feed_title,feed_url,matched_term,last_blocked_at)
		SELECT id,'legacy','Legacy title','Admin feed',url,'adult',NOW()-INTERVAL '1 day'
		FROM feed_sources WHERE normalized_url='https://example.com/rss' RETURNING id`).Scan(&legacyID); err != nil {
		t.Fatal(err)
	}
	list := func(limit string) []store.BlockedFeedArticle {
		t.Helper()
		response := authenticatedRequest(t, handler, http.MethodGet, "/api/admin/blocked-articles?limit="+limit, "", admin)
		var rows []store.BlockedFeedArticle
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &rows) != nil {
			t.Fatalf("list = %d %s", response.Code, response.Body.String())
		}
		return rows
	}
	rows := list("100")
	if len(rows) != 2 || rows[1].ID != legacyID || rows[1].CanModerate || rows[1].PublishedAt != nil {
		t.Fatalf("listing legacy/recent records = %+v", rows)
	}
	row := rows[0]
	if row.ID != id || row.ArticleGUID != "quarantine" || row.Summary != "Full summary" || row.PublishedAt == nil || !row.PublishedAt.Equal(published) || row.Status != "pending" || !row.CanModerate || row.DecidedAt != nil {
		t.Fatalf("listing payload = %+v", row)
	}
	for _, endpoint := range []struct{ method, path string }{
		{http.MethodGet, "/api/admin/blocked-articles"},
		{http.MethodPost, "/api/admin/blocked-articles/" + intString(id) + "/allow"},
		{http.MethodPost, "/api/admin/blocked-articles/" + intString(id) + "/reject"},
	} {
		t.Run("reader_"+endpoint.method+endpoint.path, func(t *testing.T) {
			response := authenticatedRequest(t, handler, endpoint.method, endpoint.path, "", reader)
			if response.Code != http.StatusForbidden {
				t.Fatalf("Reader request = %d %s, want 403", response.Code, response.Body.String())
			}
		})
	}
	for _, action := range []string{"reject", "allow", "allow"} {
		t.Run(action, func(t *testing.T) {
			response := authenticatedRequest(t, handler, http.MethodPost, "/api/admin/blocked-articles/"+intString(id)+"/"+action, "", admin)
			if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
				t.Fatalf("moderation = %d %s, want empty 204", response.Code, response.Body.String())
			}
			want := "allowed"
			if action == "reject" {
				want = "rejected"
			}
			var status string
			var moderator int64
			var decided time.Time
			if err := pool.QueryRow(ctx, `SELECT status,decided_by_user_id,decided_at FROM blocked_feed_articles WHERE id=$1`, id).Scan(&status, &moderator, &decided); err != nil {
				t.Fatal(err)
			}
			if status != want || moderator != adminID || decided.IsZero() {
				t.Fatalf("decision = %s by %d at %v, want %s by %d", status, moderator, decided, want, adminID)
			}
			var promoted int
			if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM articles WHERE guid='quarantine'`).Scan(&promoted); err != nil {
				t.Fatal(err)
			}
			if (want == "allowed" && promoted != 1) || (want == "rejected" && promoted != 0) {
				t.Fatalf("status %s promoted %d articles", want, promoted)
			}
		})
	}
	rows = list("1")
	if len(rows) != 1 || rows[0].ID != legacyID || rows[0].Status != "pending" {
		t.Fatalf("pending-first limited listing = %+v", rows)
	}
	for _, action := range []string{"allow", "reject"} {
		for _, item := range []struct {
			id     string
			status int
		}{
			{"bad", http.StatusBadRequest}, {"0", http.StatusBadRequest}, {"-1", http.StatusBadRequest},
			{"9223372036854775808", http.StatusBadRequest}, {"999999", http.StatusNotFound}, {intString(legacyID), http.StatusNotFound},
		} {
			t.Run(action+"_"+item.id, func(t *testing.T) {
				response := authenticatedRequest(t, handler, http.MethodPost, "/api/admin/blocked-articles/"+item.id+"/"+action, "", admin)
				var failure map[string]string
				if response.Code != item.status || json.Unmarshal(response.Body.Bytes(), &failure) != nil || failure["code"] == "" || failure["error"] == "" {
					t.Fatalf("moderation error = %d %s, want JSON %d", response.Code, response.Body.String(), item.status)
				}
			})
		}
	}
	for _, limit := range []string{"0", "201", "bad"} {
		response := authenticatedRequest(t, handler, http.MethodGet, "/api/admin/blocked-articles?limit="+limit, "", admin)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid limit %s = %d", limit, response.Code)
		}
	}
	// Removing the relation leaves authentication operational while store operations fail.
	if _, err := pool.Exec(ctx, `ALTER TABLE blocked_feed_articles RENAME TO unavailable_quarantine`); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []struct{ method, path string }{
		{http.MethodGet, "/api/admin/blocked-articles"},
		{http.MethodPost, "/api/admin/blocked-articles/" + intString(id) + "/allow"},
		{http.MethodPost, "/api/admin/blocked-articles/" + intString(id) + "/reject"},
	} {
		t.Run("infrastructure_"+endpoint.path, func(t *testing.T) {
			response := authenticatedRequest(t, handler, endpoint.method, endpoint.path, "", admin)
			var failure map[string]string
			if response.Code != http.StatusInternalServerError || json.Unmarshal(response.Body.Bytes(), &failure) != nil || failure["code"] == "" {
				t.Fatalf("infrastructure error = %d %s, want JSON 500", response.Code, response.Body.String())
			}
		})
	}
}
