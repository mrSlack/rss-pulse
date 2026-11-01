package syncer_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pulse-reader/app/internal/feeds"
	"github.com/pulse-reader/app/internal/store"
	"github.com/pulse-reader/app/internal/syncer"
)

type fakeRefreshRepository struct {
	dueSources  []store.FeedTarget
	userSources map[int64][]store.FeedTarget

	mu             sync.Mutex
	userIDs        []int64
	dueCalls       int
	claimCount     atomic.Int32
	activeClaims   atomic.Int32
	maxActive      atomic.Int32
	claimHook      func(context.Context, int64)
	claimLeases    bool
	unclaimed      map[int64]bool
	claimErrors    map[int64]bool
	abandoned      []abandonCall
	released       []store.SourceLease
	committed      []store.SourceLease
	notModified    []store.SourceLease
	commitCounts   store.RefreshCounts
	firstUserEnter chan struct{}
	allowUserQuery chan struct{}
}

type abandonCall struct {
	lease       store.SourceLease
	hasDeadline bool
	err         error
}

func (f *fakeRefreshRepository) DueSources(context.Context) ([]store.FeedTarget, error) {
	f.mu.Lock()
	f.dueCalls++
	f.mu.Unlock()
	return f.dueSources, nil
}

func (f *fakeRefreshRepository) dueCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dueCalls
}

func (f *fakeRefreshRepository) SourcesForUser(_ context.Context, userID int64) ([]store.FeedTarget, error) {
	f.mu.Lock()
	f.userIDs = append(f.userIDs, userID)
	callNumber := len(f.userIDs)
	f.mu.Unlock()
	if callNumber == 1 && f.firstUserEnter != nil {
		close(f.firstUserEnter)
		<-f.allowUserQuery
	}
	return f.userSources[userID], nil
}

func (f *fakeRefreshRepository) ClaimSource(ctx context.Context, sourceID int64, _ *int64) (store.SourceLease, bool, error) {
	f.claimCount.Add(1)
	if f.claimHook != nil {
		f.claimHook(ctx, sourceID)
	}
	if f.claimErrors[sourceID] {
		return store.SourceLease{}, false, errors.New("database unavailable")
	}
	if !f.claimLeases || f.unclaimed[sourceID] {
		return store.SourceLease{}, false, nil
	}
	return store.SourceLease{ID: sourceID, URL: "https://example.com/feed/" + strconv.FormatInt(sourceID, 10), Token: "token", Validators: feeds.Validators{ETag: `"old"`}}, true, nil
}

func (f *fakeRefreshRepository) ReleaseSourceLease(_ context.Context, lease store.SourceLease, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, lease)
	return nil
}

func (f *fakeRefreshRepository) AbandonSourceLease(ctx context.Context, lease store.SourceLease) error {
	_, hasDeadline := ctx.Deadline()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.abandoned = append(f.abandoned, abandonCall{lease: lease, hasDeadline: hasDeadline, err: ctx.Err()})
	return nil
}

func (f *fakeRefreshRepository) CommitSourceRefresh(_ context.Context, lease store.SourceLease, _ feeds.Document) (store.RefreshCounts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.committed = append(f.committed, lease)
	return f.commitCounts, nil
}

func (f *fakeRefreshRepository) CommitSourceNotModified(_ context.Context, lease store.SourceLease) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notModified = append(f.notModified, lease)
	return nil
}

// manualOnly keeps the periodic schedule out of tests that drive refreshes
// through the Control.
var manualOnly = syncer.WithSchedule(time.Hour, time.Hour)

func noFetch(context.Context, string, feeds.Validators) (feeds.Result, error) {
	return feeds.Result{}, errors.New("unexpected fetch")
}

func TestControlQueuesDistinctUsersAndReportsPerUserStatus(t *testing.T) {
	repository := &fakeRefreshRepository{
		firstUserEnter: make(chan struct{}),
		allowUserQuery: make(chan struct{}),
	}
	control := syncer.NewControl()
	service := syncer.New(repository, control, 2, manualOnly, syncer.WithFetcher(noFetch))
	ctx, cancel := context.WithCancel(context.Background())
	done := service.Start(ctx)
	defer func() { cancel(); <-done }()

	if control.IsUserRefreshing(42) {
		t.Fatal("user 42 reported refreshing before any request")
	}
	if !control.RequestUser(42) {
		t.Fatal("first user's refresh was not queued")
	}
	select {
	case <-repository.firstUserEnter:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not begin the first user's refresh")
	}
	if !control.IsUserRefreshing(42) {
		t.Fatal("user 42 should be refreshing while the store is blocked")
	}
	if control.RequestUser(42) {
		t.Fatal("a user already in progress must not be queued twice")
	}
	if !control.RequestUser(99) {
		t.Fatal("another user's refresh must be queued while the first runs")
	}
	if control.RequestUser(99) {
		t.Fatal("a queued user must not be queued twice")
	}
	if !control.IsUserRefreshing(99) || control.IsUserRefreshing(7) {
		t.Fatal("status must be reported per user")
	}
	close(repository.allowUserQuery)
	waitFor(t, func() bool {
		return !control.IsUserRefreshing(42) && !control.IsUserRefreshing(99)
	})
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if len(repository.userIDs) != 2 || repository.userIDs[0] != 42 || repository.userIDs[1] != 99 {
		t.Fatalf("processed user IDs = %v, want [42 99]", repository.userIDs)
	}
}

func TestConcurrentUserRequestsAreAllAcceptedAndProcessed(t *testing.T) {
	repository := &fakeRefreshRepository{}
	control := syncer.NewControl()
	service := syncer.New(repository, control, 2, manualOnly, syncer.WithFetcher(noFetch))
	ctx, cancel := context.WithCancel(context.Background())
	done := service.Start(ctx)
	defer func() { cancel(); <-done }()

	var accepted atomic.Int32
	var requests sync.WaitGroup
	for userID := int64(1); userID <= 8; userID++ {
		requests.Go(func() {
			if control.RequestUser(userID) {
				accepted.Add(1)
			}
		})
	}
	requests.Wait()
	if accepted.Load() != 8 {
		t.Fatalf("accepted %d of 8 distinct user requests", accepted.Load())
	}
	waitFor(t, func() bool {
		repository.mu.Lock()
		defer repository.mu.Unlock()
		return len(repository.userIDs) == 8
	})
	waitFor(t, func() bool {
		for userID := int64(1); userID <= 8; userID++ {
			if control.IsUserRefreshing(userID) {
				return false
			}
		}
		return true
	})
}

func TestSchedulerRunsShortlyAfterStartAndThenOnEveryTick(t *testing.T) {
	repository := &fakeRefreshRepository{}
	service := syncer.New(repository, syncer.NewControl(), 1, syncer.WithSchedule(5*time.Millisecond, 20*time.Millisecond), syncer.WithFetcher(noFetch))
	ctx, cancel := context.WithCancel(context.Background())
	done := service.Start(ctx)
	defer func() { cancel(); <-done }()
	waitFor(t, func() bool { return repository.dueCallCount() >= 1 })
	waitFor(t, func() bool { return repository.dueCallCount() >= 3 })
}

func TestSchedulerWaitsForInitialDelay(t *testing.T) {
	repository := &fakeRefreshRepository{}
	service := syncer.New(repository, syncer.NewControl(), 1, manualOnly, syncer.WithFetcher(noFetch))
	ctx, cancel := context.WithCancel(context.Background())
	done := service.Start(ctx)
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done
	if calls := repository.dueCallCount(); calls != 0 {
		t.Fatalf("DueSources called %d times before the initial delay", calls)
	}
}

func TestCancellationDuringFetchAbandonsLeaseWithBoundedContext(t *testing.T) {
	repository := &fakeRefreshRepository{dueSources: []store.FeedTarget{{ID: 7}}, claimLeases: true}
	fetchStarted := make(chan struct{})
	fetch := func(ctx context.Context, _ string, _ feeds.Validators) (feeds.Result, error) {
		close(fetchStarted)
		<-ctx.Done()
		return feeds.Result{}, ctx.Err()
	}
	control := syncer.NewControl()
	service := syncer.New(repository, control, 1, manualOnly, syncer.WithFetcher(fetch))
	ctx, cancel := context.WithCancel(context.Background())
	done := service.Start(ctx)
	if !control.RequestCycle() {
		t.Fatal("cycle was not queued")
	}
	select {
	case <-fetchStarted:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("fetch did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return promptly after cancellation")
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if len(repository.abandoned) != 1 || repository.abandoned[0].lease.ID != 7 {
		t.Fatalf("abandoned leases = %#v, want source 7", repository.abandoned)
	}
	if call := repository.abandoned[0]; !call.hasDeadline || call.err != nil {
		t.Fatalf("lease cleanup context: deadline=%v err=%v, want a live context with a deadline", call.hasDeadline, call.err)
	}
	if len(repository.released) != 0 || len(repository.committed) != 0 {
		t.Fatalf("cancelled fetch recorded an attempt: released=%v committed=%v", repository.released, repository.committed)
	}
}

func TestNotModifiedAndChangedFeedsCommitWithResponseValidators(t *testing.T) {
	repository := &fakeRefreshRepository{dueSources: []store.FeedTarget{{ID: 1}, {ID: 2}}, claimLeases: true}
	var calls atomic.Int32
	fetch := func(_ context.Context, _ string, validators feeds.Validators) (feeds.Result, error) {
		if calls.Add(1) == 1 {
			return feeds.Result{NotModified: true, Validators: validators}, nil
		}
		return feeds.Result{Document: feeds.Document{Title: "New"}, Validators: feeds.Validators{ETag: `"new"`}}, nil
	}
	control := syncer.NewControl()
	service := syncer.New(repository, control, 1, manualOnly, syncer.WithFetcher(fetch))
	ctx, cancel := context.WithCancel(context.Background())
	done := service.Start(ctx)
	defer func() { cancel(); <-done }()
	control.RequestCycle()
	waitFor(t, func() bool {
		repository.mu.Lock()
		defer repository.mu.Unlock()
		return len(repository.notModified) == 1 && len(repository.committed) == 1
	})
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if got := repository.notModified[0].Validators.ETag; got != `"old"` {
		t.Fatalf("304 commit ETag = %q, want the claimed validator", got)
	}
	if got := repository.committed[0].Validators.ETag; got != `"new"` {
		t.Fatalf("200 commit ETag = %q, want the response validator", got)
	}
}

func TestScheduledCycleAndUserRequestShareTheWorkerBudget(t *testing.T) {
	repository := &fakeRefreshRepository{
		dueSources:  []store.FeedTarget{{ID: 1}, {ID: 2}, {ID: 3}},
		userSources: map[int64][]store.FeedTarget{5: {{ID: 4}, {ID: 5}, {ID: 6}}},
	}
	release := make(chan struct{})
	repository.claimHook = func(ctx context.Context, _ int64) {
		active := repository.activeClaims.Add(1)
		defer repository.activeClaims.Add(-1)
		for previous := repository.maxActive.Load(); active > previous; previous = repository.maxActive.Load() {
			if repository.maxActive.CompareAndSwap(previous, active) {
				break
			}
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	control := syncer.NewControl()
	service := syncer.New(repository, control, 2, manualOnly, syncer.WithFetcher(noFetch))
	ctx, cancel := context.WithCancel(context.Background())
	done := service.Start(ctx)
	defer func() { cancel(); <-done }()
	control.RequestCycle()
	control.RequestUser(5)
	waitFor(t, func() bool { return repository.activeClaims.Load() == 2 })
	time.Sleep(20 * time.Millisecond)
	close(release)
	waitFor(t, func() bool {
		return repository.claimCount.Load() == 6 && repository.activeClaims.Load() == 0 && !control.IsUserRefreshing(5)
	})
	if got := repository.maxActive.Load(); got != 2 {
		t.Fatalf("maximum concurrent claims across cycle and user request = %d, want 2", got)
	}
}

func TestSyncerUsesConfiguredWorkersAndProcessesEverySource(t *testing.T) {
	repository := &fakeRefreshRepository{}
	for id := int64(1); id <= 6; id++ {
		repository.dueSources = append(repository.dueSources, store.FeedTarget{ID: id})
	}
	started := make(chan struct{}, len(repository.dueSources))
	release := make(chan struct{})
	repository.claimHook = func(ctx context.Context, _ int64) {
		active := repository.activeClaims.Add(1)
		defer repository.activeClaims.Add(-1)
		for previous := repository.maxActive.Load(); active > previous; previous = repository.maxActive.Load() {
			if repository.maxActive.CompareAndSwap(previous, active) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	control := syncer.NewControl()
	service := syncer.New(repository, control, 3, manualOnly)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go service.Run(ctx)
	if !control.RequestCycle() {
		t.Fatal("scheduled refresh was not accepted")
	}
	for worker := 0; worker < 3; worker++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			t.Fatalf("only %d workers started, want 3 in parallel", worker)
		}
	}
	close(release)
	waitFor(t, func() bool {
		return repository.claimCount.Load() == int32(len(repository.dueSources)) && repository.activeClaims.Load() == 0
	})
	if got := repository.maxActive.Load(); got != 3 {
		t.Fatalf("maximum concurrent source claims = %d, want 3", got)
	}
}

func TestSyncerStopsSourceWorkAfterCancellation(t *testing.T) {
	repository := &fakeRefreshRepository{dueSources: []store.FeedTarget{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}}}
	started := make(chan struct{}, 4)
	repository.claimHook = func(ctx context.Context, _ int64) {
		started <- struct{}{}
		<-ctx.Done()
	}
	control := syncer.NewControl()
	service := syncer.New(repository, control, 2, manualOnly)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		service.Run(ctx)
		close(done)
	}()
	if !control.RequestCycle() {
		t.Fatal("scheduled refresh was not accepted")
	}
	for worker := 0; worker < 2; worker++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			cancel()
			t.Fatalf("only %d active workers started before cancellation", worker)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop after cancellation")
	}
	if got := repository.claimCount.Load(); got > 2 {
		t.Fatalf("%d sources reached claim during shutdown, want at most 2", got)
	}
}

func TestConcurrencyFromEnvUsesDefaultAndBounds(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  int
	}{
		{name: "default", want: 4},
		{name: "configured", value: "7", want: 7},
		{name: "invalid", value: "many", want: 4},
		{name: "below minimum", value: "0", want: 4},
		{name: "above maximum", value: "99", want: 32},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("RSS_REFRESH_CONCURRENCY", test.value)
			if got := syncer.ConcurrencyFromEnv(); got != test.want {
				t.Fatalf("ConcurrencyFromEnv() = %d, want %d", got, test.want)
			}
		})
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
