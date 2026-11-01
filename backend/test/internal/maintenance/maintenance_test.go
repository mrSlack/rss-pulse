package maintenance_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pulse-reader/app/internal/auth"
	"github.com/pulse-reader/app/internal/maintenance"
	"github.com/pulse-reader/app/internal/store"
)

func TestConfigFromEnvDefaultsAndValidation(t *testing.T) {
	env := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}
	config, err := maintenance.ConfigFromEnv(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if config.ArticleRetentionDays != 180 || config.QuarantineRetentionDays != 90 || config.UnsubscribedSourceRetentionDays != 30 {
		t.Fatalf("defaults = %+v, want 180/90/30", config)
	}
	config, err = maintenance.ConfigFromEnv(env(map[string]string{"ARTICLE_RETENTION_DAYS": "0", "QUARANTINE_RETENTION_DAYS": " 0 ", "UNSUBSCRIBED_SOURCE_RETENTION_DAYS": "0"}))
	if err != nil || config.ArticleRetentionDays != 0 || config.QuarantineRetentionDays != 0 || config.UnsubscribedSourceRetentionDays != 0 {
		t.Fatalf("disabled config = %+v, %v", config, err)
	}
	config, err = maintenance.ConfigFromEnv(env(map[string]string{"ARTICLE_RETENTION_DAYS": "30", "QUARANTINE_RETENTION_DAYS": "3650", "UNSUBSCRIBED_SOURCE_RETENTION_DAYS": "1"}))
	if err != nil || config.ArticleRetentionDays != 30 || config.QuarantineRetentionDays != 3650 || config.UnsubscribedSourceRetentionDays != 1 {
		t.Fatalf("boundary config = %+v, %v", config, err)
	}
	config, err = maintenance.ConfigFromEnv(env(map[string]string{"UNSUBSCRIBED_SOURCE_RETENTION_DAYS": "3650"}))
	if err != nil || config.UnsubscribedSourceRetentionDays != 3650 {
		t.Fatalf("upper boundary config = %+v, %v", config, err)
	}
	invalid := []map[string]string{
		{"ARTICLE_RETENTION_DAYS": "29"},
		{"ARTICLE_RETENTION_DAYS": "3651"},
		{"ARTICLE_RETENTION_DAYS": "-1"},
		{"ARTICLE_RETENTION_DAYS": "180d"},
		{"QUARANTINE_RETENTION_DAYS": "6"},
		{"QUARANTINE_RETENTION_DAYS": "3651"},
		{"QUARANTINE_RETENTION_DAYS": "ninety"},
		{"UNSUBSCRIBED_SOURCE_RETENTION_DAYS": "-1"},
		{"UNSUBSCRIBED_SOURCE_RETENTION_DAYS": "3651"},
		{"UNSUBSCRIBED_SOURCE_RETENTION_DAYS": "30d"},
	}
	for _, values := range invalid {
		if _, err := maintenance.ConfigFromEnv(env(values)); err == nil {
			t.Fatalf("ConfigFromEnv(%v) succeeded, want error", values)
		}
	}
}

type fakeRepository struct {
	mu       sync.Mutex
	calls    []string
	articles []int
	sources  []int
	throttle store.LoginThrottle
	failing  bool
}

func (r *fakeRepository) record(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

func (r *fakeRepository) DeleteExpiredArticles(_ context.Context, days, batchSize int) (int64, error) {
	r.record("articles")
	r.mu.Lock()
	r.articles = append(r.articles, days, batchSize)
	r.mu.Unlock()
	if r.failing {
		return 0, errors.New("boom")
	}
	return 3, nil
}

func (r *fakeRepository) DeleteExpiredQuarantine(_ context.Context, days int) (int64, error) {
	r.record("quarantine")
	return 2, nil
}

func (r *fakeRepository) DeleteUnsubscribedSources(_ context.Context, days, batchSize int) (int64, int64, error) {
	r.record("sources")
	r.mu.Lock()
	r.sources = append(r.sources, days, batchSize)
	r.mu.Unlock()
	return 1, 4, nil
}

func (r *fakeRepository) DeleteExpiredSessions(context.Context) (int64, error) {
	r.record("sessions")
	return 1, nil
}

func (r *fakeRepository) DeleteExpiredLoginAttempts(_ context.Context, throttle store.LoginThrottle, _ time.Time) (int64, error) {
	r.record("login_attempts")
	r.mu.Lock()
	r.throttle = throttle
	r.mu.Unlock()
	return 5, nil
}

func (r *fakeRepository) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func TestRunOnceRunsEveryCleanupEvenAfterAFailure(t *testing.T) {
	repository := &fakeRepository{failing: true}
	err := maintenance.RunOnce(context.Background(), repository, maintenance.Config{ArticleRetentionDays: 180, QuarantineRetentionDays: 90, UnsubscribedSourceRetentionDays: 30})
	if err == nil {
		t.Fatal("RunOnce() error = nil, want the article cleanup failure")
	}
	if strings.Join(repository.calls, ",") != "articles,quarantine,sources,sessions,login_attempts" {
		t.Fatalf("calls = %v", repository.calls)
	}
	if repository.articles[0] != 180 || repository.articles[1] != maintenance.ArticleDeleteBatchSize {
		t.Fatalf("article cleanup arguments = %v", repository.articles)
	}
	if repository.sources[0] != 30 || repository.sources[1] != maintenance.ArticleDeleteBatchSize {
		t.Fatalf("source cleanup arguments = %v", repository.sources)
	}
	if repository.throttle != store.DefaultLoginThrottle {
		t.Fatalf("login attempt cleanup throttle = %+v, want the sign-in limits %+v", repository.throttle, store.DefaultLoginThrottle)
	}
}

func TestRunOnceSkipsDisabledRetention(t *testing.T) {
	repository := &fakeRepository{}
	if err := maintenance.RunOnce(context.Background(), repository, maintenance.Config{}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(repository.calls, ",") != "sessions,login_attempts" {
		t.Fatalf("calls = %v, want only session and sign-in attempt cleanup", repository.calls)
	}
}

func TestJobRunsPeriodicallyAndStopsOnCancel(t *testing.T) {
	repository := &fakeRepository{}
	ctx, cancel := context.WithCancel(context.Background())
	job := maintenance.Job{Repository: repository, Config: maintenance.Config{}, InitialDelay: time.Millisecond, Interval: 5 * time.Millisecond}
	done := job.Start(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for repository.callCount() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("job ran %d times, want at least 2", repository.callCount())
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("job did not stop after context cancellation")
	}
}

func TestJobStopsDuringInitialDelay(t *testing.T) {
	repository := &fakeRepository{}
	ctx, cancel := context.WithCancel(context.Background())
	done := maintenance.Job{Repository: repository, InitialDelay: time.Hour, Interval: time.Hour}.Start(ctx)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("job did not stop during its initial delay")
	}
	if repository.callCount() != 0 {
		t.Fatalf("job ran %d cleanups after cancellation", repository.callCount())
	}
}

func TestRunOnceRemovesExpiredSessionsAndSourcesFromDatabase(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPostgres(t)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	adminID, err := store.EnsureInitialAdministrator(ctx, pool, "admin@example.com", "a-long-bootstrap-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateSharedSources(ctx, pool, adminID); err != nil {
		t.Fatal(err)
	}
	repository := store.New(pool)
	if err := repository.CreateSession(ctx, adminID, auth.HashSessionToken("expired"), time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateSession(ctx, adminID, auth.HashSessionToken("active"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO feed_sources (normalized_url,title,url,unsubscribed_since)
		VALUES ('https://orphan.example/rss','Orphan','https://orphan.example/rss',NOW()-INTERVAL '31 days')`); err != nil {
		t.Fatal(err)
	}
	if err := maintenance.RunOnce(ctx, repository, maintenance.Config{ArticleRetentionDays: 180, QuarantineRetentionDays: 90, UnsubscribedSourceRetentionDays: 30}); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM feed_sources WHERE normalized_url='https://orphan.example/rss'`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatal("source unsubscribed beyond the grace period survived the maintenance job")
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM sessions WHERE token_hash=$1`, auth.HashSessionToken("expired")).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatal("expired session survived the maintenance job")
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("sessions = %d, want the active session only", remaining)
	}
}

func TestRunOnceRemovesExpiredLoginAttemptsFromDatabase(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPostgres(t)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	adminID, err := store.EnsureInitialAdministrator(ctx, pool, "admin@example.com", "a-long-bootstrap-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateSharedSources(ctx, pool, adminID); err != nil {
		t.Fatal(err)
	}
	repository := store.New(pool)
	throttle := store.DefaultLoginThrottle
	now := time.Now()
	hash := make([]byte, 32)
	// One row of each kind just outside its window, one just inside it.
	rows := []struct {
		kind string
		at   time.Time
	}{
		{"ip", now.Add(-throttle.IPAttemptWindow - time.Minute)},
		{"ip", now.Add(-throttle.IPAttemptWindow + 20*time.Second)},
		{"account", now.Add(-throttle.AccountFailureWindow - time.Minute)},
		{"account", now.Add(-throttle.AccountFailureWindow + time.Minute)},
	}
	for _, row := range rows {
		if _, err := pool.Exec(ctx, `INSERT INTO login_attempts (kind, key_hash, attempted_at) VALUES ($1, $2, $3)`, row.kind, hash, row.at); err != nil {
			t.Fatal(err)
		}
	}
	if err := maintenance.RunOnce(ctx, repository, maintenance.Config{}); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	var ipRemaining, accountRemaining int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FILTER (WHERE kind = 'ip'), COUNT(*) FILTER (WHERE kind = 'account') FROM login_attempts`).Scan(&ipRemaining, &accountRemaining); err != nil {
		t.Fatal(err)
	}
	if ipRemaining != 1 || accountRemaining != 1 {
		t.Fatalf("remaining attempts ip=%d account=%d, want only the one inside each window", ipRemaining, accountRemaining)
	}
}

func isolatedPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		if os.Getenv("REQUIRE_INTEGRATION") != "" {
			t.Fatal("REQUIRE_INTEGRATION is set but TEST_DATABASE_URL is empty")
		}
		t.Skip("TEST_DATABASE_URL is required for isolated PostgreSQL integration tests")
	}
	admin, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	schema := "test_maintenance_" + strings.ToLower(t.Name()) + "_" + time.Now().UTC().Format("150405000")
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+quotedSchema); err != nil {
		admin.Close()
		t.Fatalf("create isolated schema: %v", err)
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		admin.Close()
		t.Fatalf("parse test database URL: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		admin.Close()
		t.Fatalf("connect to isolated schema: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
		admin.Close()
	})
	return pool
}
