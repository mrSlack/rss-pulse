package maintenance_test

import (
	"context"
	"testing"

	"github.com/pulse-reader/app/internal/maintenance"
	"github.com/pulse-reader/app/internal/metrics"
	"github.com/pulse-reader/app/internal/metrics/metricstest"
)

func TestJobRunOnceRecordsMetrics(t *testing.T) {
	m := metrics.New()
	config := maintenance.Config{ArticleRetentionDays: 180, QuarantineRetentionDays: 90, UnsubscribedSourceRetentionDays: 30}
	job := maintenance.Job{Repository: &fakeRepository{}, Config: config, Metrics: m}
	if err := job.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	for kind, want := range map[string]float64{
		metrics.MaintenanceArticles: 3, metrics.MaintenanceQuarantine: 2, metrics.MaintenanceUnsubscribedSources: 1,
		metrics.MaintenanceUnsubscribedSourceArticles: 4, metrics.MaintenanceSessions: 1,
	} {
		if got := metricstest.Value(t, m.Registry(), "pulse_maintenance_deleted_rows_total", map[string]string{"kind": kind}); got != want {
			t.Errorf("deleted %s = %v, want %v", kind, got, want)
		}
	}
	if got := metricstest.Value(t, m.Registry(), "pulse_maintenance_runs_total", map[string]string{"outcome": "success"}); got != 1 {
		t.Fatalf("successful runs = %v, want 1", got)
	}
	if got := metricstest.Value(t, m.Registry(), "pulse_maintenance_last_success_timestamp_seconds", nil); got == 0 {
		t.Fatal("last success timestamp was not set")
	}

	failing := maintenance.Job{Repository: &fakeRepository{failing: true}, Config: config, Metrics: m}
	if err := failing.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce() error = nil, want the article cleanup failure")
	}
	if got := metricstest.Value(t, m.Registry(), "pulse_maintenance_runs_total", map[string]string{"outcome": "failure"}); got != 1 {
		t.Fatalf("failed runs = %v, want 1", got)
	}
}
