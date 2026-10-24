package syncer_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pulse-reader/app/internal/feeds"
	"github.com/pulse-reader/app/internal/store"
	"github.com/pulse-reader/app/internal/syncer"
)

type fakeRefreshRepository struct {
	dueSources []store.FeedTarget

	mu             sync.Mutex
	userIDs        []int64
	claimCount     atomic.Int32
	activeClaims   atomic.Int32
	maxActive      atomic.Int32
	claimHook      func(context.Context, int64)
	firstUserEnter chan struct{}
	allowUserQuery chan struct{}
}

func (f *fakeRefreshRepository) DueSources(context.Context) ([]store.FeedTarget, error) {
	return f.dueSources, nil
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
	return nil, nil
}

func (f *fakeRefreshRepository) ClaimSource(ctx context.Context, sourceID int64, _ *int64) (store.SourceLease, bool, error) {
	f.claimCount.Add(1)
	if f.claimHook != nil {
		f.claimHook(ctx, sourceID)
	}
	return store.SourceLease{}, false, nil
}

func (*fakeRefreshRepository) ReleaseSourceLease(context.Context, store.SourceLease, string) error {
	return nil
}

func (*fakeRefreshRepository) CommitSourceRefresh(context.Context, store.SourceLease, feeds.Document) error {
	return nil
}

func TestControlQueuesOneRefreshWithUserScope(t *testing.T) {
	repository := &fakeRefreshRepository{
		firstUserEnter: make(chan struct{}),
		allowUserQuery: make(chan struct{}),
	}
	control := syncer.NewControl()
	service := syncer.New(repository, control, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go service.Run(ctx)

	if !control.RequestUser(42) {
		t.Fatal("first user's refresh was not accepted")
	}
	select {
	case <-repository.firstUserEnter:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not begin the first user's refresh")
	}
	if !control.IsRunning() {
		t.Fatal("refresh should remain marked as running while the store is blocked")
	}
	if !control.RequestUser(42) {
		t.Fatal("one pending refresh for the same user should be queued")
	}
	if control.RequestUser(99) {
		t.Fatal("a second pending user refresh should be rejected")
	}
	close(repository.allowUserQuery)
	waitFor(t, func() bool {
		repository.mu.Lock()
		defer repository.mu.Unlock()
		return len(repository.userIDs) == 2 && !control.IsRunning()
	})
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if len(repository.userIDs) != 2 || repository.userIDs[0] != 42 || repository.userIDs[1] != 42 {
		t.Fatalf("scheduled user IDs = %v, want [42 42]", repository.userIDs)
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
	service := syncer.New(repository, control, 3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go service.Run(ctx)
	if !control.Request(false) {
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
		return repository.claimCount.Load() == int32(len(repository.dueSources)) && !control.IsRunning()
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
	service := syncer.New(repository, control, 2)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		service.Run(ctx)
		close(done)
	}()
	if !control.Request(false) {
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
