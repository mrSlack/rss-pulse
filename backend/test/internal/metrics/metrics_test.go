package metrics_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pulse-reader/app/internal/metrics"
	"github.com/pulse-reader/app/internal/metrics/metricstest"
)

// allowedLabels are the only label names Pulse metrics may use. Each has a small,
// fixed set of values; user IDs, e-mail addresses, URLs and feed IDs must never
// become labels.
var allowedLabels = map[string]bool{"route": true, "method": true, "status_class": true, "outcome": true, "kind": true}

func recordEverything(m *metrics.Metrics) {
	m.ObserveHTTPRequest("/api/feeds/{id}", http.MethodPatch, http.StatusNoContent, 12*time.Millisecond)
	m.ObserveRefresh(metrics.RefreshOK)
	m.ObserveFetchDuration(300 * time.Millisecond)
	m.AddArticles(3, 1)
	m.ObserveSignIn(metrics.SignInFailure)
	m.ObserveMaintenance(time.Second, map[string]int64{metrics.MaintenanceArticles: 5}, nil)
	m.SetSearchUpgradeDone()
	m.AddSearchKeysBackfilled(7)
	m.RegisterManualRefreshQueue(func() int { return 2 })
	m.RegisterOperationalStats(func(context.Context) (metrics.OperationalStats, error) {
		return metrics.OperationalStats{SourcesDue: 4, SourcesOverdue: 1, OldestOverdue: 90 * time.Second, ActiveLeases: 2, PendingFeedRequests: 3, PendingQuarantine: 6}, nil
	}, time.Minute)
}

func TestMetricsExposeEveryDocumentedFamily(t *testing.T) {
	m := metrics.New()
	recordEverything(m)
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, family := range families {
		names[family.GetName()] = true
	}
	for _, name := range []string{
		"pulse_http_requests_total", "pulse_http_request_duration_seconds",
		"pulse_feed_refresh_attempts_total", "pulse_feed_fetch_duration_seconds",
		"pulse_feed_articles_inserted_total", "pulse_feed_articles_quarantined_total",
		"pulse_feed_sources_due", "pulse_feed_sources_overdue", "pulse_feed_oldest_overdue_seconds",
		"pulse_feed_active_leases", "pulse_feed_manual_refresh_queue_length",
		"pulse_feed_requests_pending", "pulse_quarantine_pending", "pulse_operational_stats_errors_total",
		"pulse_signin_attempts_total",
		"pulse_maintenance_runs_total", "pulse_maintenance_last_run_duration_seconds",
		"pulse_maintenance_last_success_timestamp_seconds", "pulse_maintenance_deleted_rows_total",
		"pulse_search_upgrade_done", "pulse_search_upgrade_backfilled_articles_total",
		"go_goroutines", "process_cpu_seconds_total",
	} {
		if !names[name] {
			t.Errorf("metric family %s is missing", name)
		}
	}
}

func TestPulseMetricsUseOnlyLowCardinalityLabels(t *testing.T) {
	m := metrics.New()
	recordEverything(m)
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if !strings.HasPrefix(family.GetName(), "pulse_") {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if !allowedLabels[label.GetName()] {
					t.Errorf("%s has label %q, which is not in the low-cardinality allow list", family.GetName(), label.GetName())
				}
			}
		}
	}
}

func TestOutcomeAndKindLabelsArePreinitialized(t *testing.T) {
	m := metrics.New()
	for _, outcome := range []string{metrics.RefreshOK, metrics.RefreshNotModified, metrics.RefreshError} {
		if got := metricstest.Value(t, m.Registry(), "pulse_feed_refresh_attempts_total", map[string]string{"outcome": outcome}); got != 0 {
			t.Errorf("refresh %s = %v, want 0", outcome, got)
		}
	}
	for _, outcome := range []string{metrics.SignInSuccess, metrics.SignInFailure, metrics.SignInRateLimited, metrics.SignInError} {
		metricstest.Value(t, m.Registry(), "pulse_signin_attempts_total", map[string]string{"outcome": outcome})
	}
	for _, kind := range []string{metrics.MaintenanceArticles, metrics.MaintenanceQuarantine, metrics.MaintenanceUnsubscribedSources, metrics.MaintenanceUnsubscribedSourceArticles, metrics.MaintenanceSessions, metrics.MaintenanceLoginAttempts} {
		metricstest.Value(t, m.Registry(), "pulse_maintenance_deleted_rows_total", map[string]string{"kind": kind})
	}
}

func TestHTTPRequestLabelsAreNormalized(t *testing.T) {
	m := metrics.New()
	m.ObserveHTTPRequest("", "BREW", 418, time.Millisecond)
	m.ObserveHTTPRequest("/api/articles", http.MethodGet, 200, time.Millisecond)
	m.ObserveHTTPRequest("/api/articles", http.MethodGet, 0, time.Millisecond)
	if got := metricstest.Value(t, m.Registry(), "pulse_http_requests_total", map[string]string{"route": "unmatched", "method": "OTHER", "status_class": "4xx"}); got != 1 {
		t.Fatalf("unmatched OTHER 4xx = %v, want 1", got)
	}
	if got := metricstest.Value(t, m.Registry(), "pulse_http_requests_total", map[string]string{"route": "/api/articles", "method": "GET", "status_class": "2xx"}); got != 2 {
		t.Fatalf("/api/articles GET 2xx = %v, want 2 (status 0 means 200)", got)
	}
	if got := metricstest.Value(t, m.Registry(), "pulse_http_request_duration_seconds", map[string]string{"route": "/api/articles", "method": "GET", "status_class": "2xx"}); got != 2 {
		t.Fatalf("duration sample count = %v, want 2", got)
	}
}

func TestMaintenanceRecordsSuccessFailureAndRows(t *testing.T) {
	m := metrics.New()
	before := time.Now()
	m.ObserveMaintenance(2*time.Second, map[string]int64{metrics.MaintenanceArticles: 5, metrics.MaintenanceSessions: 2}, nil)
	if got := metricstest.Value(t, m.Registry(), "pulse_maintenance_last_success_timestamp_seconds", nil); got < float64(before.Unix()) {
		t.Fatalf("last success = %v, want >= %d", got, before.Unix())
	}
	m.ObserveMaintenance(time.Second, map[string]int64{metrics.MaintenanceArticles: 1}, errors.New("boom"))
	if got := metricstest.Value(t, m.Registry(), "pulse_maintenance_deleted_rows_total", map[string]string{"kind": metrics.MaintenanceArticles}); got != 6 {
		t.Fatalf("deleted articles = %v, want 6", got)
	}
	if got := metricstest.Value(t, m.Registry(), "pulse_maintenance_runs_total", map[string]string{"outcome": "failure"}); got != 1 {
		t.Fatalf("failed runs = %v, want 1", got)
	}
	if got := metricstest.Value(t, m.Registry(), "pulse_maintenance_runs_total", map[string]string{"outcome": "success"}); got != 1 {
		t.Fatalf("successful runs = %v, want 1", got)
	}
	if got := metricstest.Value(t, m.Registry(), "pulse_maintenance_last_run_duration_seconds", nil); got != 1 {
		t.Fatalf("last duration = %v, want 1", got)
	}
}

func TestOperationalStatsAreCachedBetweenScrapes(t *testing.T) {
	m := metrics.New()
	var calls atomic.Int32
	fail := atomic.Bool{}
	m.RegisterOperationalStats(func(context.Context) (metrics.OperationalStats, error) {
		calls.Add(1)
		if fail.Load() {
			return metrics.OperationalStats{}, errors.New("database down")
		}
		return metrics.OperationalStats{SourcesDue: 4, OldestOverdue: 90 * time.Second, PendingQuarantine: 6}, nil
	}, time.Hour)
	for range 3 {
		if got := metricstest.Value(t, m.Registry(), "pulse_feed_sources_due", nil); got != 4 {
			t.Fatalf("sources due = %v, want 4", got)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("stats queried %d times for three scrapes within the cache period, want 1", calls.Load())
	}
	if got := metricstest.Value(t, m.Registry(), "pulse_feed_oldest_overdue_seconds", nil); got != 90 {
		t.Fatalf("oldest overdue = %v, want 90", got)
	}
}

func TestOperationalStatsFailureKeepsLastValuesAndCountsError(t *testing.T) {
	m := metrics.New()
	var fail atomic.Bool
	m.RegisterOperationalStats(func(context.Context) (metrics.OperationalStats, error) {
		if fail.Load() {
			return metrics.OperationalStats{}, errors.New("database down")
		}
		return metrics.OperationalStats{PendingFeedRequests: 3}, nil
	}, 0) // no caching: every scrape queries
	metricstest.Value(t, m.Registry(), "pulse_feed_requests_pending", nil)
	fail.Store(true)
	if got := metricstest.Value(t, m.Registry(), "pulse_feed_requests_pending", nil); got != 3 {
		t.Fatalf("pending feed requests after a failed query = %v, want last value 3", got)
	}
	if got := metricstest.Value(t, m.Registry(), "pulse_operational_stats_errors_total", nil); got < 1 {
		t.Fatalf("operational stats errors = %v, want >= 1", got)
	}
}

func TestOperationalStatsBeforeFirstSuccessAreOmitted(t *testing.T) {
	m := metrics.New()
	m.RegisterOperationalStats(func(context.Context) (metrics.OperationalStats, error) {
		return metrics.OperationalStats{}, errors.New("database down")
	}, 0)
	if _, ok := metricstest.Lookup(t, m.Registry(), "pulse_feed_sources_due", nil); ok {
		t.Fatal("sources due is reported although no query has succeeded")
	}
}

func TestNilMetricsIgnoreObservations(t *testing.T) {
	var m *metrics.Metrics
	recordEverything(m)
}

func TestHandlerServesOnlyMetricsPath(t *testing.T) {
	m := metrics.New()
	m.ObserveSignIn(metrics.SignInSuccess)
	server := httptest.NewServer(m.Handler())
	defer server.Close()

	response, err := http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics = %d", response.StatusCode)
	}
	if !strings.Contains(string(body), `pulse_signin_attempts_total{outcome="success"} 1`) {
		t.Fatalf("metrics output lacks the sign-in counter:\n%s", body)
	}
	for _, path := range []string{"/", "/api/metrics", "/metrics/extra"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, response.StatusCode)
		}
	}
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/metrics", nil)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /metrics = %d, want 405", response.StatusCode)
	}
}
