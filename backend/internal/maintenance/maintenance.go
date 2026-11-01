// Package maintenance runs periodic data-retention cleanup: expired articles,
// decided quarantine records, sources without subscribers, expired sessions, and
// sign-in attempts that have left their rate-limit window.
package maintenance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/pulse-reader/app/internal/metrics"
	"github.com/pulse-reader/app/internal/store"
)

const (
	DefaultArticleRetentionDays    = 180
	DefaultQuarantineRetentionDays = 90
	// DefaultUnsubscribedSourceRetentionDays is the grace period before a shared
	// source that nobody subscribes to is deleted with its articles.
	DefaultUnsubscribedSourceRetentionDays = 30
	ArticleDeleteBatchSize                 = 5000
	DefaultInitialDelay                    = time.Minute
	DefaultInterval                        = 6 * time.Hour
)

// Config holds retention periods in days; 0 disables the corresponding cleanup.
type Config struct {
	ArticleRetentionDays            int
	QuarantineRetentionDays         int
	UnsubscribedSourceRetentionDays int
}

// ConfigFromEnv reads ARTICLE_RETENTION_DAYS, QUARANTINE_RETENTION_DAYS and
// UNSUBSCRIBED_SOURCE_RETENTION_DAYS. Unset values
// use the defaults; invalid values return an error so startup can fail fast.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	articleDays, err := retentionDays(getenv, "ARTICLE_RETENTION_DAYS", DefaultArticleRetentionDays, 30, 3650)
	if err != nil {
		return Config{}, err
	}
	quarantineDays, err := retentionDays(getenv, "QUARANTINE_RETENTION_DAYS", DefaultQuarantineRetentionDays, 7, 3650)
	if err != nil {
		return Config{}, err
	}
	sourceDays, err := retentionDays(getenv, "UNSUBSCRIBED_SOURCE_RETENTION_DAYS", DefaultUnsubscribedSourceRetentionDays, 1, 3650)
	if err != nil {
		return Config{}, err
	}
	return Config{ArticleRetentionDays: articleDays, QuarantineRetentionDays: quarantineDays, UnsubscribedSourceRetentionDays: sourceDays}, nil
}

func retentionDays(getenv func(string) string, key string, fallback, minimum, maximum int) (int, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || (value != 0 && (value < minimum || value > maximum)) {
		return 0, fmt.Errorf("%s must be 0 (disabled) or a whole number of days from %d to %d", key, minimum, maximum)
	}
	return value, nil
}

type Repository interface {
	DeleteExpiredArticles(ctx context.Context, retentionDays, batchSize int) (int64, error)
	DeleteExpiredQuarantine(ctx context.Context, retentionDays int) (int64, error)
	DeleteUnsubscribedSources(ctx context.Context, graceDays, batchSize int) (sources, articles int64, err error)
	DeleteExpiredSessions(ctx context.Context) (int64, error)
	DeleteExpiredLoginAttempts(ctx context.Context, throttle store.LoginThrottle, now time.Time) (int64, error)
}

// RunOnce performs every enabled cleanup. A failing step does not prevent the others.
func RunOnce(ctx context.Context, repository Repository, config Config) error {
	return Job{Repository: repository, Config: config}.RunOnce(ctx)
}

// RunOnce performs every enabled cleanup once and records the run in j.Metrics. A
// failing step does not prevent the others.
func (j Job) RunOnce(ctx context.Context) error {
	started := time.Now()
	deleted := make(map[string]int64)
	err := runCleanups(ctx, j.Repository, j.Config, deleted)
	j.Metrics.ObserveMaintenance(time.Since(started), deleted, err)
	return err
}

func runCleanups(ctx context.Context, repository Repository, config Config, deleted map[string]int64) error {
	var failures []error
	if config.ArticleRetentionDays > 0 {
		count, err := repository.DeleteExpiredArticles(ctx, config.ArticleRetentionDays, ArticleDeleteBatchSize)
		if err != nil {
			failures = append(failures, fmt.Errorf("article retention: %w", err))
		}
		deleted[metrics.MaintenanceArticles] = count
		slog.Info("maintenance: deleted expired articles", slog.Int64("count", count), slog.Int("retention_days", config.ArticleRetentionDays))
	}
	if config.QuarantineRetentionDays > 0 {
		count, err := repository.DeleteExpiredQuarantine(ctx, config.QuarantineRetentionDays)
		if err != nil {
			failures = append(failures, fmt.Errorf("quarantine retention: %w", err))
		}
		deleted[metrics.MaintenanceQuarantine] = count
		slog.Info("maintenance: deleted expired quarantine records", slog.Int64("count", count), slog.Int("retention_days", config.QuarantineRetentionDays))
	}
	if config.UnsubscribedSourceRetentionDays > 0 {
		sources, articles, err := repository.DeleteUnsubscribedSources(ctx, config.UnsubscribedSourceRetentionDays, ArticleDeleteBatchSize)
		if err != nil {
			failures = append(failures, fmt.Errorf("unsubscribed source cleanup: %w", err))
		}
		deleted[metrics.MaintenanceUnsubscribedSources] = sources
		deleted[metrics.MaintenanceUnsubscribedSourceArticles] = articles
		slog.Info("maintenance: deleted unsubscribed sources", slog.Int64("count", sources), slog.Int64("articles", articles),
			slog.Int("retention_days", config.UnsubscribedSourceRetentionDays))
	}
	count, err := repository.DeleteExpiredSessions(ctx)
	if err != nil {
		failures = append(failures, fmt.Errorf("session cleanup: %w", err))
	}
	deleted[metrics.MaintenanceSessions] = count
	slog.Info("maintenance: deleted expired sessions", slog.Int64("count", count))
	// The sign-in limiter also sweeps while sign-ins arrive; this removes the
	// attempts left behind once they stop (for example after an attack).
	count, err = repository.DeleteExpiredLoginAttempts(ctx, store.DefaultLoginThrottle, time.Now())
	if err != nil {
		failures = append(failures, fmt.Errorf("sign-in attempt cleanup: %w", err))
	}
	deleted[metrics.MaintenanceLoginAttempts] = count
	slog.Info("maintenance: deleted expired sign-in attempts", slog.Int64("count", count))
	return errors.Join(failures...)
}

// Job runs RunOnce after InitialDelay and then every Interval until its context ends.
type Job struct {
	Repository   Repository
	Config       Config
	InitialDelay time.Duration
	Interval     time.Duration
	// Metrics, when set, receives the duration, outcome and deleted rows of each run.
	Metrics *metrics.Metrics
}

// Start launches the job in a goroutine. The returned channel is closed once the
// job has stopped after ctx is cancelled, so callers can wait for it.
func (j Job) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	interval := j.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	go func() {
		defer close(done)
		timer := time.NewTimer(j.InitialDelay)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			if err := j.RunOnce(ctx); err != nil && ctx.Err() == nil {
				slog.Error("maintenance failed", slog.String("error", err.Error()))
			}
			timer.Reset(interval)
		}
	}()
	return done
}
