package syncer_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pulse-reader/app/internal/feeds"
	"github.com/pulse-reader/app/internal/metrics"
	"github.com/pulse-reader/app/internal/metrics/metricstest"
	"github.com/pulse-reader/app/internal/store"
	"github.com/pulse-reader/app/internal/syncer"
)

func TestRefreshRecordsOutcomesFetchDurationsAndArticleCounts(t *testing.T) {
	repository := &fakeRefreshRepository{
		claimLeases:  true,
		userSources:  map[int64][]store.FeedTarget{5: {{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5}}},
		unclaimed:    map[int64]bool{4: true}, // claimed elsewhere: not an attempt
		claimErrors:  map[int64]bool{5: true}, // claim query failed: not an attempt either
		commitCounts: store.RefreshCounts{Inserted: 2, Quarantined: 1},
	}
	fetch := func(_ context.Context, url string, _ feeds.Validators) (feeds.Result, error) {
		switch {
		case strings.HasSuffix(url, "/1"):
			return feeds.Result{Document: feeds.Document{Title: "One"}}, nil
		case strings.HasSuffix(url, "/2"):
			return feeds.Result{NotModified: true}, nil
		default:
			return feeds.Result{}, errors.New("connection refused")
		}
	}
	m := metrics.New()
	control := syncer.NewControl()
	service := syncer.New(repository, control, 2, manualOnly, syncer.WithFetcher(fetch), syncer.WithMetrics(m))
	ctx, cancel := context.WithCancel(context.Background())
	done := service.Start(ctx)
	defer func() { cancel(); <-done }()

	control.RequestUser(5)
	waitFor(t, func() bool {
		repository.mu.Lock()
		defer repository.mu.Unlock()
		return len(repository.userIDs) == 1 && !control.IsUserRefreshing(5)
	})

	for outcome, want := range map[string]float64{metrics.RefreshOK: 1, metrics.RefreshNotModified: 1, metrics.RefreshError: 1} {
		if got := metricstest.Value(t, m.Registry(), "pulse_feed_refresh_attempts_total", map[string]string{"outcome": outcome}); got != want {
			t.Errorf("refresh attempts %s = %v, want %v", outcome, got, want)
		}
	}
	if got := metricstest.Value(t, m.Registry(), "pulse_feed_claim_errors_total", nil); got != 1 {
		t.Errorf("claim errors = %v, want 1", got)
	}
	if got := metricstest.Value(t, m.Registry(), "pulse_feed_fetch_duration_seconds", nil); got != 3 {
		t.Errorf("fetch duration samples = %v, want 3", got)
	}
	if got := metricstest.Value(t, m.Registry(), "pulse_feed_articles_inserted_total", nil); got != 2 {
		t.Errorf("inserted articles = %v, want 2", got)
	}
	if got := metricstest.Value(t, m.Registry(), "pulse_feed_articles_quarantined_total", nil); got != 1 {
		t.Errorf("quarantined articles = %v, want 1", got)
	}
}

func TestControlReportsQueuedManualRefreshes(t *testing.T) {
	control := syncer.NewControl()
	if got := control.QueueLength(); got != 0 {
		t.Fatalf("empty queue length = %d", got)
	}
	control.RequestUser(1)
	control.RequestUser(2)
	control.RequestUser(2)
	if got := control.QueueLength(); got != 2 {
		t.Fatalf("queue length = %d, want 2", got)
	}
}
