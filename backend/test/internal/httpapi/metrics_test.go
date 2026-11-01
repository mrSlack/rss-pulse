package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pulse-reader/app/internal/httpapi"
	"github.com/pulse-reader/app/internal/metrics"
	"github.com/pulse-reader/app/internal/store"
	"github.com/pulse-reader/app/internal/syncer"
)

func TestPublicRouterDoesNotExposeMetrics(t *testing.T) {
	handler := httpapi.New(store.New(nil), syncer.NewControl(), false, httpapi.WithMetrics(metrics.New()))
	for _, path := range []string{"/metrics", "/api/metrics", "/api/metrics/", "/api/health/metrics"} {
		response := authenticatedRequest(t, handler, http.MethodGet, path, "", nil)
		if response.Code != http.StatusNotFound {
			t.Errorf("GET %s on the public router = %d, want 404", path, response.Code)
		}
		if strings.Contains(response.Body.String(), "pulse_http_requests_total") {
			t.Errorf("GET %s on the public router returned metrics", path)
		}
	}
}

func TestMetricsListenerReportsAPIRequestsAndSignIns(t *testing.T) {
	pool, _, _ := isolatedAPIDatabase(t)
	m := metrics.New()
	m.RegisterPool(pool.Stat)
	repository := store.New(pool)
	m.RegisterOperationalStats(func(ctx context.Context) (metrics.OperationalStats, error) {
		stats, err := repository.OperationalStats(ctx, metrics.OverdueTolerance)
		return metrics.OperationalStats(stats), err
	}, metrics.DefaultOperationalStatsTTL)
	clock := &testClock{now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	handler := httpapi.New(repository, syncer.NewControl(), false, httpapi.WithClock(clock.Now), httpapi.WithMetrics(m))

	if response := authenticatedRequest(t, handler, http.MethodGet, "/api/health/ready", "", nil); response.Code != http.StatusOK {
		t.Fatalf("GET /api/health/ready = %d", response.Code)
	}
	cookie := loginForTest(t, handler, "admin@example.com", "a-long-admin-password", false)
	if response := authenticatedRequest(t, handler, http.MethodPatch, "/api/feeds/987654", `{"title":"x"}`, cookie); response.Code < 400 {
		t.Fatalf("PATCH unknown feed = %d, want an error", response.Code)
	}
	authenticatedRequest(t, handler, http.MethodGet, "/api/no-such-route/probe-segment", "", nil)
	const wrong = `{"email":"reader@example.com","password":"not-the-password"}`
	for range 10 {
		authenticatedRequest(t, handler, http.MethodPost, "/api/auth/login", wrong, nil)
		clock.Advance(10 * time.Second)
	}
	if response := authenticatedRequest(t, handler, http.MethodPost, "/api/auth/login", wrong, nil); response.Code != http.StatusTooManyRequests {
		t.Fatalf("11th failed sign-in = %d, want 429", response.Code)
	}
	authenticatedRequest(t, handler, http.MethodPost, "/api/auth/login", `{"email":"not-an-address","password":"x"}`, nil)

	listener := httptest.NewServer(m.Handler())
	defer listener.Close()
	response, err := http.Get(listener.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	body := string(raw)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics = %d", response.StatusCode)
	}
	for _, line := range []string{
		`pulse_http_requests_total{method="GET",route="/api/health/ready",status_class="2xx"} 1`,
		`pulse_http_requests_total{method="POST",route="/api/auth/login",status_class="2xx"} 1`,
		`pulse_http_requests_total{method="PATCH",route="/api/feeds/{id}",status_class="4xx"} 1`,
		`pulse_http_requests_total{method="GET",route="unmatched",status_class="4xx"} 1`,
		`pulse_http_request_duration_seconds_count{method="GET",route="/api/health/ready",status_class="2xx"} 1`,
		`pulse_signin_attempts_total{outcome="success"} 1`,
		`pulse_signin_attempts_total{outcome="failure"} 11`,
		`pulse_signin_attempts_total{outcome="rate_limited"} 1`,
		`pulse_signin_attempts_total{outcome="error"} 0`,
		`pulse_feed_sources_due `,
		`pulse_feed_requests_pending 0`,
		`pulse_quarantine_pending 0`,
		`pulse_db_pool_max_connections `,
		`pulse_db_pool_acquires_total `,
		`go_goroutines `,
	} {
		if !strings.Contains(body, line) {
			t.Errorf("metrics output lacks %q", line)
		}
	}
	for _, private := range []string{"/api/feeds/987654", "probe-segment", "reader@example.com", "admin@example.com", "no-such-route"} {
		if strings.Contains(body, private) {
			t.Errorf("metrics output contains request data %q", private)
		}
	}
}
