package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBackgroundUpgradeRetriesUntilSuccess(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	var calls atomic.Int32
	done := runBackgroundUpgrade(context.Background(), logger, "article search upgrade", time.Millisecond, func(context.Context) error {
		if calls.Add(1) < 3 {
			return errors.New("temporary failure")
		}
		return nil
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("background upgrade did not finish")
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("run called %d times, want 3", got)
	}
	if count := strings.Count(output.String(), "temporary failure"); count != 2 {
		t.Fatalf("logged %d failures, want 2:\n%s", count, output.String())
	}
}

func TestBackgroundUpgradeStopsWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	started := make(chan struct{})
	done := runBackgroundUpgrade(ctx, logger, "article search upgrade", time.Hour, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("background upgrade did not stop after cancellation")
	}
	if strings.Contains(output.String(), "level=ERROR") {
		t.Fatalf("cancellation was logged as an error:\n%s", output.String())
	}
}
