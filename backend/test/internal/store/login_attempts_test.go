package store_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pulse-reader/app/internal/store"
)

var testThrottle = store.LoginThrottle{
	AccountFailureLimit:  10,
	AccountFailureWindow: 15 * time.Minute,
	IPAttemptLimit:       30,
	IPAttemptWindow:      time.Minute,
}

var loginClock = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func newLoginAttemptStore(t *testing.T) (*pgxpool.Pool, *store.Store) {
	t.Helper()
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
	return pool, store.New(pool)
}

func beginAllowed(t *testing.T, repository *store.Store, ip, account string, now time.Time) store.LoginAttempt {
	t.Helper()
	attempt, wait, err := repository.BeginLoginAttempt(context.Background(), testThrottle, ip, account, now)
	if err != nil {
		t.Fatal(err)
	}
	if wait > 0 {
		t.Fatalf("attempt for %s from %s refused for %v", account, ip, wait)
	}
	return attempt
}

func beginRefused(t *testing.T, repository *store.Store, ip, account string, now time.Time) time.Duration {
	t.Helper()
	_, wait, err := repository.BeginLoginAttempt(context.Background(), testThrottle, ip, account, now)
	if err != nil {
		t.Fatal(err)
	}
	if wait <= 0 {
		t.Fatalf("attempt for %s from %s allowed, want refused", account, ip)
	}
	return wait
}

func TestLoginAttemptsLockAccountForRollingWindow(t *testing.T) {
	_, repository := newLoginAttemptStore(t)
	now := loginClock
	for attempt := 0; attempt < 10; attempt++ {
		beginAllowed(t, repository, fmt.Sprintf("192.0.2.%d", attempt), "reader@example.com", now)
		now = now.Add(10 * time.Second)
	}
	// The first failure was 100 seconds ago; it leaves the window in 800 seconds.
	if wait := beginRefused(t, repository, "198.51.100.1", "reader@example.com", now); wait != 800*time.Second {
		t.Fatalf("wait = %v, want 800s", wait)
	}
	beginAllowed(t, repository, "198.51.100.1", "other@example.com", now)
	now = now.Add(800 * time.Second)
	beginAllowed(t, repository, "198.51.100.2", "reader@example.com", now)
	// Only the oldest failure left the window; the account is at its limit again.
	beginRefused(t, repository, "198.51.100.3", "reader@example.com", now)
}

func TestLoginAttemptSuccessClearsAccountFailures(t *testing.T) {
	_, repository := newLoginAttemptStore(t)
	for attempt := 0; attempt < 9; attempt++ {
		beginAllowed(t, repository, fmt.Sprintf("192.0.2.%d", attempt), "reader@example.com", loginClock)
	}
	attempt := beginAllowed(t, repository, "192.0.2.100", "reader@example.com", loginClock)
	if err := repository.ClearLoginFailures(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 10; index++ {
		beginAllowed(t, repository, fmt.Sprintf("198.51.100.%d", index), "reader@example.com", loginClock)
	}
	beginRefused(t, repository, "203.0.113.1", "reader@example.com", loginClock)
}

func TestLoginAttemptCancelWithdrawsProvisionalFailure(t *testing.T) {
	_, repository := newLoginAttemptStore(t)
	for attempt := 0; attempt < 9; attempt++ {
		beginAllowed(t, repository, fmt.Sprintf("192.0.2.%d", attempt), "reader@example.com", loginClock)
	}
	attempt := beginAllowed(t, repository, "192.0.2.100", "reader@example.com", loginClock)
	if err := repository.CancelLoginAttempt(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}
	beginAllowed(t, repository, "192.0.2.101", "reader@example.com", loginClock)
	beginRefused(t, repository, "192.0.2.102", "reader@example.com", loginClock)
}

func TestLoginAttemptsLimitAttemptsPerIP(t *testing.T) {
	_, repository := newLoginAttemptStore(t)
	now := loginClock
	for index := 0; index < 30; index++ {
		attempt := beginAllowed(t, repository, "192.0.2.1", fmt.Sprintf("user%d@example.com", index), now)
		// Successful sign-ins still count towards the IP limit.
		if err := repository.ClearLoginFailures(context.Background(), attempt); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Second)
	}
	// The first attempt was 30 seconds ago and leaves the minute in 30 seconds.
	if wait := beginRefused(t, repository, "192.0.2.1", "fresh@example.com", now); wait != 30*time.Second {
		t.Fatalf("wait = %v, want 30s", wait)
	}
	beginAllowed(t, repository, "192.0.2.2", "fresh@example.com", now)
	beginAllowed(t, repository, "192.0.2.1", "fresh@example.com", now.Add(30*time.Second))
}

func TestLoginAttemptsStoreKeyedHashesInsteadOfAddresses(t *testing.T) {
	pool, repository := newLoginAttemptStore(t)
	beginAllowed(t, repository, "192.0.2.77", "secret.person@example.com", loginClock)
	var rows, rawMatches, wrongLength int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE position(convert_to('secret.person', 'UTF8') IN key_hash) > 0 OR position(convert_to('192.0.2.77', 'UTF8') IN key_hash) > 0),
		       COUNT(*) FILTER (WHERE length(key_hash) <> 32)
		FROM login_attempts`).Scan(&rows, &rawMatches, &wrongLength); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || rawMatches != 0 || wrongLength != 0 {
		t.Fatalf("rows=%d rawMatches=%d wrongLength=%d, want 2 hashed rows", rows, rawMatches, wrongLength)
	}
	// The hash is keyed by a per-installation secret, not a plain SHA-256.
	var plain int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM login_attempts WHERE key_hash IN (sha256(convert_to('secret.person@example.com', 'UTF8')), sha256(convert_to('192.0.2.77', 'UTF8')))`).Scan(&plain); err != nil {
		t.Fatal(err)
	}
	if plain != 0 {
		t.Fatalf("%d rows store an unkeyed SHA-256", plain)
	}
}

func TestDeleteExpiredLoginAttemptsKeepsLiveRows(t *testing.T) {
	pool, repository := newLoginAttemptStore(t)
	beginAllowed(t, repository, "192.0.2.1", "old@example.com", loginClock)
	beginAllowed(t, repository, "192.0.2.2", "recent@example.com", loginClock.Add(14*time.Minute))
	// 15 minutes after the first attempt: both IP rows and the first account row expired.
	deleted, err := repository.DeleteExpiredLoginAttempts(context.Background(), testThrottle, loginClock.Add(15*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d, want 3", deleted)
	}
	var left int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM login_attempts`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	// The recent IP row is 60 seconds old, which is exactly the end of its window.
	// Only the recent account row is still inside its window.
	if left != 1 {
		t.Fatalf("rows left = %d, want only the recent account row", left)
	}
}

// Two Store instances on separate connection pools stand for two API
// instances sharing one database.
func TestConcurrentLoginAttemptsOnTwoInstancesRespectLimits(t *testing.T) {
	pool, first := newLoginAttemptStore(t)
	secondPool, err := pgxpool.NewWithConfig(context.Background(), pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(secondPool.Close)
	instances := []*store.Store{first, store.New(secondPool)}

	run := func(count int, ip, account func(int) string) int {
		var allowed atomic.Int32
		var group sync.WaitGroup
		errs := make(chan error, count)
		for index := 0; index < count; index++ {
			group.Add(1)
			go func() {
				defer group.Done()
				_, wait, err := instances[index%2].BeginLoginAttempt(context.Background(), testThrottle, ip(index), account(index), loginClock)
				if err != nil {
					errs <- err
					return
				}
				if wait == 0 {
					allowed.Add(1)
				}
			}()
		}
		group.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
		return int(allowed.Load())
	}

	if got := run(40, func(index int) string { return fmt.Sprintf("192.0.2.%d", index) }, func(int) string { return "victim@example.com" }); got != 10 {
		t.Fatalf("concurrent attempts for one account allowed = %d, want 10", got)
	}
	if got := run(50, func(int) string { return "203.0.113.9" }, func(index int) string { return fmt.Sprintf("guess%d@example.com", index) }); got != 30 {
		t.Fatalf("concurrent attempts from one IP allowed = %d, want 30", got)
	}
}
