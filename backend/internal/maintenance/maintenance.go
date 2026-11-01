// Package maintenance runs periodic data-retention cleanup: expired articles,
// decided quarantine records, and expired sessions.
package maintenance

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultArticleRetentionDays    = 180
	DefaultQuarantineRetentionDays = 90
	ArticleDeleteBatchSize         = 5000
	DefaultInitialDelay            = time.Minute
	DefaultInterval                = 6 * time.Hour
)

// Config holds retention periods in days; 0 disables the corresponding cleanup.
type Config struct {
	ArticleRetentionDays    int
	QuarantineRetentionDays int
}

// ConfigFromEnv reads ARTICLE_RETENTION_DAYS and QUARANTINE_RETENTION_DAYS. Unset values
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
	return Config{ArticleRetentionDays: articleDays, QuarantineRetentionDays: quarantineDays}, nil
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
	DeleteExpiredSessions(ctx context.Context) (int64, error)
}

// RunOnce performs every enabled cleanup. A failing step does not prevent the others.
func RunOnce(ctx context.Context, repository Repository, config Config) error {
	var failures []error
	if config.ArticleRetentionDays > 0 {
		count, err := repository.DeleteExpiredArticles(ctx, config.ArticleRetentionDays, ArticleDeleteBatchSize)
		if err != nil {
			failures = append(failures, fmt.Errorf("article retention: %w", err))
		}
		log.Printf("maintenance: deleted %d articles older than %d days", count, config.ArticleRetentionDays)
	}
	if config.QuarantineRetentionDays > 0 {
		count, err := repository.DeleteExpiredQuarantine(ctx, config.QuarantineRetentionDays)
		if err != nil {
			failures = append(failures, fmt.Errorf("quarantine retention: %w", err))
		}
		log.Printf("maintenance: deleted %d quarantine records older than %d days", count, config.QuarantineRetentionDays)
	}
	count, err := repository.DeleteExpiredSessions(ctx)
	if err != nil {
		failures = append(failures, fmt.Errorf("session cleanup: %w", err))
	}
	log.Printf("maintenance: deleted %d expired sessions", count)
	return errors.Join(failures...)
}

// Job runs RunOnce after InitialDelay and then every Interval until its context ends.
type Job struct {
	Repository   Repository
	Config       Config
	InitialDelay time.Duration
	Interval     time.Duration
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
			if err := RunOnce(ctx, j.Repository, j.Config); err != nil && ctx.Err() == nil {
				log.Printf("maintenance failed: %v", err)
			}
			timer.Reset(interval)
		}
	}()
	return done
}
