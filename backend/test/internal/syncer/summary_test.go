package syncer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/pulse-reader/app/internal/feeds"
	"github.com/pulse-reader/app/internal/store"
	"github.com/pulse-reader/app/internal/syncer"
)

// lockedBuffer lets the test read log output while syncer workers write to it.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *lockedBuffer) records(t *testing.T) []map[string]any {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	var records []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(b.buffer.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func cycleSummaries(t *testing.T, logs *lockedBuffer) []map[string]any {
	var summaries []map[string]any
	for _, record := range logs.records(t) {
		if record["msg"] == "rss refresh cycle finished" {
			summaries = append(summaries, record)
		}
	}
	return summaries
}

func TestScheduledCycleLogsOneSummary(t *testing.T) {
	repository := &fakeRefreshRepository{
		dueSources:  []store.FeedTarget{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5}},
		claimLeases: true,
		unclaimed:   map[int64]bool{5: true},
	}
	fetch := func(_ context.Context, url string, validators feeds.Validators) (feeds.Result, error) {
		switch url {
		case "https://example.com/feed/1":
			return feeds.Result{NotModified: true, Validators: validators}, nil
		case "https://example.com/feed/2":
			return feeds.Result{}, errors.New("HTTP 500")
		default:
			return feeds.Result{Document: feeds.Document{Title: "New"}}, nil
		}
	}
	var logs lockedBuffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	control := syncer.NewControl()
	service := syncer.New(repository, control, 2, manualOnly, syncer.WithFetcher(fetch), syncer.WithLogger(logger))
	ctx, cancel := context.WithCancel(context.Background())
	done := service.Start(ctx)
	defer func() { cancel(); <-done }()
	control.RequestCycle()

	waitFor(t, func() bool { return len(cycleSummaries(t, &logs)) > 0 })
	summaries := cycleSummaries(t, &logs)
	if len(summaries) != 1 {
		t.Fatalf("summaries = %v, want one", summaries)
	}
	want := map[string]any{"level": "INFO", "trigger": "schedule", "targets": float64(5), "refreshed": float64(2), "not_modified": float64(1), "failed": float64(1), "skipped": float64(1)}
	for key, value := range want {
		if summaries[0][key] != value {
			t.Fatalf("summary %s = %#v, want %#v (%v)", key, summaries[0][key], value, summaries[0])
		}
	}
	if duration, ok := summaries[0]["duration_ms"].(float64); !ok || duration < 0 {
		t.Fatalf("summary duration_ms = %#v", summaries[0]["duration_ms"])
	}
}

func TestEmptyScheduledCycleSummaryIsDebugOnly(t *testing.T) {
	repository := &fakeRefreshRepository{}
	var logs lockedBuffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	control := syncer.NewControl()
	service := syncer.New(repository, control, 1, manualOnly, syncer.WithFetcher(noFetch), syncer.WithLogger(logger))
	ctx, cancel := context.WithCancel(context.Background())
	done := service.Start(ctx)
	defer func() { cancel(); <-done }()
	control.RequestCycle()
	waitFor(t, func() bool { return len(cycleSummaries(t, &logs)) > 0 })
	record := cycleSummaries(t, &logs)[0]
	if record["level"] != "DEBUG" || record["targets"] != float64(0) {
		t.Fatalf("empty cycle record = %v, want a DEBUG summary", record)
	}
}

func TestManualRefreshLogsSummaryWithUser(t *testing.T) {
	repository := &fakeRefreshRepository{
		userSources: map[int64][]store.FeedTarget{7: {{ID: 1}}},
		claimLeases: true,
	}
	fetch := func(_ context.Context, _ string, validators feeds.Validators) (feeds.Result, error) {
		return feeds.Result{NotModified: true, Validators: validators}, nil
	}
	var logs lockedBuffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	control := syncer.NewControl()
	service := syncer.New(repository, control, 1, manualOnly, syncer.WithFetcher(fetch), syncer.WithLogger(logger))
	ctx, cancel := context.WithCancel(context.Background())
	done := service.Start(ctx)
	defer func() { cancel(); <-done }()
	control.RequestUser(7)
	waitFor(t, func() bool { return len(cycleSummaries(t, &logs)) > 0 })
	record := cycleSummaries(t, &logs)[0]
	if record["trigger"] != "user" || record["user_id"] != float64(7) || record["targets"] != float64(1) || record["not_modified"] != float64(1) {
		t.Fatalf("manual refresh summary = %v", record)
	}
}
