package metrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/pulse-reader/app/internal/metrics/metricstest"
	"github.com/pulse-reader/app/internal/store"
)

func TestOperationalGaugesDisappearOnceCachedValuesAreStale(t *testing.T) {
	clock := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	failing := false
	const ttl = 15 * time.Second
	collector := newOperationalCollector(func(context.Context) (store.OperationalStats, error) {
		if failing {
			return store.OperationalStats{}, errors.New("database down")
		}
		return store.OperationalStats{SourcesDue: 4, PendingFeedRequests: 3}, nil
	}, ttl)
	collector.now = func() time.Time { return clock }
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)

	if got := metricstest.Value(t, registry, "pulse_feed_sources_due", nil); got != 4 {
		t.Fatalf("sources due = %v, want 4", got)
	}
	if got := metricstest.Value(t, registry, "pulse_operational_stats_up", nil); got != 1 {
		t.Fatalf("up after a successful query = %v, want 1", got)
	}

	failing = true
	clock = clock.Add(2 * ttl) // expired cache, failed query, values still recent
	if got := metricstest.Value(t, registry, "pulse_feed_requests_pending", nil); got != 3 {
		t.Fatalf("pending feed requests within the staleness bound = %v, want last value 3", got)
	}
	if got := metricstest.Value(t, registry, "pulse_operational_stats_up", nil); got != 0 {
		t.Fatalf("up after a failed query = %v, want 0", got)
	}

	clock = clock.Add(2 * ttl) // the last success is now 4 TTLs old
	for _, name := range []string{"pulse_feed_sources_due", "pulse_feed_sources_overdue", "pulse_feed_oldest_overdue_seconds", "pulse_feed_active_leases", "pulse_feed_requests_pending", "pulse_quarantine_pending"} {
		if value, ok := metricstest.Lookup(t, registry, name, nil); ok {
			t.Errorf("%s = %v is still exported although the last successful query is older than %v", name, value, operationalStatsStaleAfter*ttl)
		}
	}
	if got := metricstest.Value(t, registry, "pulse_operational_stats_up", nil); got != 0 {
		t.Fatalf("up while the database is down = %v, want 0", got)
	}
	// Every lookup gathers, and every gather after the cache expired queries again.
	if got := metricstest.Value(t, registry, "pulse_operational_stats_errors_total", nil); got < 2 {
		t.Fatalf("errors = %v, want at least 2", got)
	}

	failing = false
	clock = clock.Add(time.Second)
	if got := metricstest.Value(t, registry, "pulse_feed_sources_due", nil); got != 4 {
		t.Fatalf("sources due after recovery = %v, want 4", got)
	}
	if got := metricstest.Value(t, registry, "pulse_operational_stats_up", nil); got != 1 {
		t.Fatalf("up after recovery = %v, want 1", got)
	}
}
