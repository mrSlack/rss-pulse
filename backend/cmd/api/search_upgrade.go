package main

import (
	"context"
	"log/slog"
	"time"
)

// backgroundUpgradeRetryDelay is the wait before a failed background upgrade step is
// retried, for example after a lock timeout or a lost database connection.
const backgroundUpgradeRetryDelay = time.Minute

// runBackgroundUpgrade runs a schema upgrade step that may take minutes (such as
// store.ArticleSearchUpgrade) while the API serves requests. A failure is logged and
// retried after retryDelay until the step succeeds or ctx is canceled. The returned
// channel is closed when it has stopped.
func runBackgroundUpgrade(ctx context.Context, logger *slog.Logger, name string, retryDelay time.Duration, run func(context.Context) error) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			err := run(ctx)
			if err == nil || ctx.Err() != nil {
				return
			}
			logger.Error("background upgrade failed; retrying", slog.String("upgrade", name), slog.String("error", err.Error()), slog.Duration("retry_in", retryDelay))
			timer := time.NewTimer(retryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	return done
}
