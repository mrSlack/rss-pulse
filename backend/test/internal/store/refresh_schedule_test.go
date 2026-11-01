package store_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pulse-reader/app/internal/feeds"
	"github.com/pulse-reader/app/internal/store"
)

type scheduleFixture struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	store   *store.Store
	adminID int64
}

func newScheduleFixture(t *testing.T, ttlMinutes, globalMinutes int) scheduleFixture {
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
	if _, err := pool.Exec(ctx, `UPDATE system_settings SET value=CASE key WHEN 'shared_feed_ttl_minutes' THEN $1::int ELSE $2::int END`, ttlMinutes, globalMinutes); err != nil {
		t.Fatal(err)
	}
	return scheduleFixture{ctx: ctx, pool: pool, store: store.New(pool), adminID: adminID}
}

// user creates a reader with an optional interval override (nil inherits the global interval).
func (f scheduleFixture) user(t *testing.T, email string, override *int) int64 {
	t.Helper()
	var id int64
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO users (email,password_hash,role,refresh_interval_minutes) VALUES ($1,'test-hash','Reader',$2) RETURNING id`, email, override).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// source creates a source whose last success and last check happened the
// given number of minutes ago (nil = never), subscribed by the given users.
func (f scheduleFixture) source(t *testing.T, name string, successAgo, checkedAgo *int, subscribers ...int64) int64 {
	t.Helper()
	url := "https://" + name + ".example/rss"
	var id int64
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO feed_sources (normalized_url,title,url,last_success_at,last_checked_at)
		VALUES ($1,$2,$1,NOW()-make_interval(mins => $3::int),NOW()-make_interval(mins => $4::int)) RETURNING id`, url, name, successAgo, checkedAgo).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for _, userID := range subscribers {
		if _, err := f.pool.Exec(f.ctx, `INSERT INTO user_feeds (user_id,feed_source_id) VALUES ($1,$2)`, userID, id); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func minutes(value int) *int { return &value }

func TestRefreshScheduleRules(t *testing.T) {
	// TTL 20 minutes, global scheduler interval 60 minutes.
	f := newScheduleFixture(t, 20, 60)
	global := f.user(t, "global@example.com", nil)    // 60 (inherited)
	fast := f.user(t, "fast@example.com", minutes(5)) // below the TTL floor
	medium := f.user(t, "medium@example.com", minutes(25))

	sources := map[string]int64{
		"never-fetched":        f.source(t, "never", nil, nil, global),
		"override-shorter-due": f.source(t, "override", minutes(30), minutes(30), medium),
		"global-not-due":       f.source(t, "global", minutes(30), minutes(30), global),
		"global-due":           f.source(t, "globaldue", minutes(61), minutes(61), global),
		"ttl-floor-not-due":    f.source(t, "floor", minutes(10), minutes(10), fast),
		"ttl-floor-due":        f.source(t, "floordue", minutes(21), minutes(21), fast),
		"minimum-subscriber":   f.source(t, "minimum", minutes(30), minutes(30), global, medium),
		"failing-backoff":      f.source(t, "failing", minutes(50), minutes(10), medium),
		"failing-retry-due":    f.source(t, "retry", minutes(50), minutes(26), medium),
		"never-ok-backoff":     f.source(t, "neverok", nil, minutes(5), fast),
		"never-ok-retry-due":   f.source(t, "neverokdue", nil, minutes(21), fast),
		"unsubscribed-not-due": f.source(t, "orphan", minutes(600), minutes(600)),
	}
	wantDue := []string{"never-fetched", "override-shorter-due", "global-due", "ttl-floor-due", "minimum-subscriber", "failing-retry-due", "never-ok-retry-due"}

	due, err := f.store.DueSources(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[int64]string, len(sources))
	for name, id := range sources {
		names[id] = name
	}
	got := make([]string, 0, len(due))
	for _, target := range due {
		got = append(got, names[target.ID])
	}
	sort.Strings(got)
	sort.Strings(wantDue)
	if !equalStrings(got, wantDue) {
		t.Fatalf("DueSources() = %v, want %v", got, wantDue)
	}

	// Scheduled claims use exactly the same eligibility.
	claimed := make([]string, 0)
	for name, id := range sources {
		lease, ok, err := f.store.ClaimSource(f.ctx, id, nil)
		if err != nil {
			t.Fatalf("ClaimSource(%s) error = %v", name, err)
		}
		if ok {
			claimed = append(claimed, name)
			if err := f.store.AbandonSourceLease(f.ctx, lease); err != nil {
				t.Fatal(err)
			}
		}
	}
	sort.Strings(claimed)
	if !equalStrings(claimed, wantDue) {
		t.Fatalf("scheduled claims = %v, want %v", claimed, wantDue)
	}

	// Manual claims use only the TTL, from the last attempt, for subscribers only.
	for _, test := range []struct {
		source string
		user   int64
		want   bool
	}{
		{"global-not-due", global, true},        // 30 min since success >= TTL 20
		{"ttl-floor-not-due", fast, false},      // 10 min < TTL 20
		{"failing-backoff", medium, false},      // failed 10 min ago < TTL 20
		{"never-ok-backoff", fast, false},       // failed 5 min ago < TTL 20
		{"global-not-due", medium, false},       // not a subscriber
		{"unsubscribed-not-due", global, false}, // not a subscriber
	} {
		userID := test.user
		lease, ok, err := f.store.ClaimSource(f.ctx, sources[test.source], &userID)
		if err != nil {
			t.Fatal(err)
		}
		if ok != test.want {
			t.Errorf("manual claim of %s by user %d = %v, want %v", test.source, test.user, ok, test.want)
		}
		if ok {
			if err := f.store.AbandonSourceLease(f.ctx, lease); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestRefreshScheduleFollowsSettingChanges(t *testing.T) {
	f := newScheduleFixture(t, 15, 15)
	reader := f.user(t, "reader@example.com", nil)
	source := f.source(t, "changing", minutes(20), minutes(20), reader)
	if due, err := f.store.DueSources(f.ctx); err != nil || len(due) != 1 || due[0].ID != source {
		t.Fatalf("DueSources() with 15-minute interval = %v, %v", due, err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE system_settings SET value=30 WHERE key='refresh_interval_minutes'`); err != nil {
		t.Fatal(err)
	}
	if due, err := f.store.DueSources(f.ctx); err != nil || len(due) != 0 {
		t.Fatalf("DueSources() after raising the global interval = %v, %v; want none", due, err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE users SET refresh_interval_minutes=10 WHERE id=$1`, reader); err != nil {
		t.Fatal(err)
	}
	if due, err := f.store.DueSources(f.ctx); err != nil || len(due) != 1 {
		t.Fatalf("DueSources() after a shorter user override = %v, %v; want the source", due, err)
	}
}

func TestRefreshScheduleIgnoresDisabledSubscribers(t *testing.T) {
	// TTL 20 minutes, global scheduler interval 60 minutes.
	f := newScheduleFixture(t, 20, 60)
	active := f.user(t, "active@example.com", nil)             // 60 (inherited)
	disabled := f.user(t, "disabled@example.com", minutes(20)) // short override, but disabled
	if _, err := f.pool.Exec(f.ctx, `UPDATE users SET disabled_at=NOW() WHERE id=$1`, disabled); err != nil {
		t.Fatal(err)
	}
	shared := f.source(t, "shared", minutes(30), minutes(30), active, disabled)
	onlyDisabled := f.source(t, "onlydisabled", nil, nil, disabled)

	due, err := f.store.DueSources(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("DueSources() = %v; a disabled subscriber's override must not shorten the interval and a source followed only by disabled users must not be scheduled", due)
	}
	for _, id := range []int64{shared, onlyDisabled} {
		if _, ok, err := f.store.ClaimSource(f.ctx, id, nil); err != nil || ok {
			t.Fatalf("scheduled ClaimSource(%d) = %v, %v; want not claimed", id, ok, err)
		}
	}
}

func TestConditionalRefreshCommits(t *testing.T) {
	f := newScheduleFixture(t, 15, 15)
	source := f.source(t, "conditional", nil, nil, f.adminID)

	lease, ok, err := f.store.ClaimSource(f.ctx, source, nil)
	if err != nil || !ok {
		t.Fatalf("first claim = %v, %v", ok, err)
	}
	if lease.Validators != (feeds.Validators{}) {
		t.Fatalf("new source validators = %#v, want none", lease.Validators)
	}
	lease.Validators = feeds.Validators{ETag: `"v1"`, LastModified: "Sun, 01 Nov 2026 10:00:00 GMT"}
	document := feeds.Document{Title: "Conditional", Items: []feeds.Item{{GUID: "a", Title: "Article", URL: "https://conditional.example/a"}}}
	if _, err := f.store.CommitSourceRefresh(f.ctx, lease, document); err != nil {
		t.Fatal(err)
	}

	// Make the source due again and record a failure that a 304 must clear.
	if _, err := f.pool.Exec(f.ctx, `UPDATE feed_sources SET last_success_at=NOW()-INTERVAL '1 hour', last_checked_at=NOW()-INTERVAL '1 hour', last_error='old failure' WHERE id=$1`, source); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE articles SET title='Locally kept' WHERE feed_source_id=$1`, source); err != nil {
		t.Fatal(err)
	}
	lease, ok, err = f.store.ClaimSource(f.ctx, source, nil)
	if err != nil || !ok {
		t.Fatalf("second claim = %v, %v", ok, err)
	}
	if lease.Validators.ETag != `"v1"` || lease.Validators.LastModified != "Sun, 01 Nov 2026 10:00:00 GMT" {
		t.Fatalf("claimed validators = %#v, want the stored ones", lease.Validators)
	}
	lease.Validators.ETag = `"v2"`
	before := time.Now().Add(-time.Minute)
	if err := f.store.CommitSourceNotModified(f.ctx, lease); err != nil {
		t.Fatal(err)
	}
	var lastSuccess, lastChecked time.Time
	var lastError, leaseToken, etag *string
	if err := f.pool.QueryRow(f.ctx, `SELECT last_success_at,last_checked_at,last_error,lease_token,etag FROM feed_sources WHERE id=$1`, source).
		Scan(&lastSuccess, &lastChecked, &lastError, &leaseToken, &etag); err != nil {
		t.Fatal(err)
	}
	if lastSuccess.Before(before) || !lastChecked.Equal(lastSuccess) || lastError != nil || leaseToken != nil || etag == nil || *etag != `"v2"` {
		t.Fatalf("after 304: success=%v checked=%v error=%v lease=%v etag=%v", lastSuccess, lastChecked, lastError, leaseToken, etag)
	}
	var title string
	var count int
	if err := f.pool.QueryRow(f.ctx, `SELECT MIN(title), COUNT(*) FROM articles WHERE feed_source_id=$1`, source).Scan(&title, &count); err != nil {
		t.Fatal(err)
	}
	if count != 1 || title != "Locally kept" {
		t.Fatalf("articles after 304 = %d titled %q, want the untouched article", count, title)
	}
	if err := f.store.CommitSourceNotModified(f.ctx, lease); err != store.ErrLeaseLost {
		t.Fatalf("second 304 commit with a released lease = %v, want ErrLeaseLost", err)
	}
}

func TestAbandonedLeaseKeepsSchedule(t *testing.T) {
	f := newScheduleFixture(t, 15, 15)
	source := f.source(t, "abandoned", minutes(30), minutes(30), f.adminID)
	lease, ok, err := f.store.ClaimSource(f.ctx, source, nil)
	if err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	if err := f.store.AbandonSourceLease(f.ctx, lease); err != nil {
		t.Fatal(err)
	}
	if due, err := f.store.DueSources(f.ctx); err != nil || len(due) != 1 {
		t.Fatalf("DueSources() after an abandoned lease = %v, %v; want the source still due", due, err)
	}
	if err := f.store.AbandonSourceLease(f.ctx, lease); err != store.ErrLeaseLost {
		t.Fatalf("abandoning twice = %v, want ErrLeaseLost", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
