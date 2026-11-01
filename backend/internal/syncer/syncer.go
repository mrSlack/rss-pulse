package syncer

import (
	"context"
	"errors"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pulse-reader/app/internal/feeds"
	"github.com/pulse-reader/app/internal/store"
)

// RefreshRepository is the part of the store API used by the scheduler.
type RefreshRepository interface {
	DueSources(context.Context) ([]store.FeedTarget, error)
	SourcesForUser(context.Context, int64) ([]store.FeedTarget, error)
	ClaimSource(context.Context, int64, *int64) (store.SourceLease, bool, error)
	ReleaseSourceLease(context.Context, store.SourceLease, string) error
	AbandonSourceLease(context.Context, store.SourceLease) error
	CommitSourceRefresh(context.Context, store.SourceLease, feeds.Document) error
	CommitSourceNotModified(context.Context, store.SourceLease) error
}

// FetchFunc downloads one feed. Production uses feeds.Fetch.
type FetchFunc func(context.Context, string, feeds.Validators) (feeds.Result, error)

const (
	DefaultConcurrency = 4
	maxConcurrency     = 32

	// DefaultInitialDelay is the wait before the first scheduled cycle after start-up.
	DefaultInitialDelay = 10 * time.Second
	// DefaultInterval is the scheduling resolution: how often due sources are checked.
	DefaultInterval = time.Minute

	fetchTimeout        = 15 * time.Second
	leaseCleanupTimeout = 5 * time.Second
)

// Control queues refresh work for the Syncer. Scheduled cycles are coalesced
// into one pending cycle; manual requests are queued per user, at most once per
// user while that user is queued or being processed.
type Control struct {
	mu             sync.Mutex
	cycleRequested bool
	cycleRunning   bool
	userQueue      []int64
	activeUsers    map[int64]struct{}
	cycleWake      chan struct{}
	userWake       chan struct{}
}

func NewControl() *Control {
	return &Control{
		activeUsers: make(map[int64]struct{}),
		cycleWake:   make(chan struct{}, 1),
		userWake:    make(chan struct{}, 1),
	}
}

// RequestCycle asks for a scheduled cycle over all due sources as soon as
// possible. It returns false when a cycle is already pending.
func (c *Control) RequestCycle() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cycleRequested {
		return false
	}
	c.cycleRequested = true
	signal(c.cycleWake)
	return true
}

// RequestUser queues a refresh of the user's subscriptions. It returns false
// when the user is already queued or being processed; the existing request
// covers the new one.
func (c *Control) RequestUser(userID int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, active := c.activeUsers[userID]; active {
		return false
	}
	c.activeUsers[userID] = struct{}{}
	c.userQueue = append(c.userQueue, userID)
	signal(c.userWake)
	return true
}

// IsUserRefreshing reports whether the user's manual refresh is queued or running.
func (c *Control) IsUserRefreshing(userID int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, active := c.activeUsers[userID]
	return active
}

// IsRunning reports whether any refresh work is queued or running.
func (c *Control) IsRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cycleRequested || c.cycleRunning || len(c.activeUsers) > 0
}

func (c *Control) beginCycle() {
	c.mu.Lock()
	c.cycleRequested = false
	c.cycleRunning = true
	c.mu.Unlock()
}

func (c *Control) finishCycle() {
	c.mu.Lock()
	c.cycleRunning = false
	c.mu.Unlock()
}

func (c *Control) nextUser() (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.userQueue) == 0 {
		return 0, false
	}
	userID := c.userQueue[0]
	c.userQueue = c.userQueue[1:]
	return userID, true
}

func (c *Control) finishUser(userID int64) {
	c.mu.Lock()
	delete(c.activeUsers, userID)
	c.mu.Unlock()
}

func signal(wake chan struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}

type Syncer struct {
	store        RefreshRepository
	control      *Control
	concurrency  int
	slots        chan struct{}
	fetch        FetchFunc
	initialDelay time.Duration
	interval     time.Duration
}

// Option customizes a Syncer created by New.
type Option func(*Syncer)

// WithSchedule sets the delay before the first scheduled cycle and the
// interval between later cycles (for tests).
func WithSchedule(initialDelay, interval time.Duration) Option {
	return func(s *Syncer) {
		s.initialDelay = initialDelay
		s.interval = interval
	}
}

// WithFetcher replaces the feed fetcher (for tests).
func WithFetcher(fetch FetchFunc) Option {
	return func(s *Syncer) { s.fetch = fetch }
}

func New(repository RefreshRepository, control *Control, concurrency int, options ...Option) *Syncer {
	concurrency = normalizeConcurrency(concurrency)
	s := &Syncer{
		store:        repository,
		control:      control,
		concurrency:  concurrency,
		slots:        make(chan struct{}, concurrency),
		fetch:        feeds.Fetch,
		initialDelay: DefaultInitialDelay,
		interval:     DefaultInterval,
	}
	for _, option := range options {
		option(s)
	}
	return s
}

func ConcurrencyFromEnv() int {
	value := strings.TrimSpace(os.Getenv("RSS_REFRESH_CONCURRENCY"))
	if value == "" {
		return DefaultConcurrency
	}
	concurrency, err := strconv.Atoi(value)
	if err != nil || concurrency < 1 {
		return DefaultConcurrency
	}
	return normalizeConcurrency(concurrency)
}

func normalizeConcurrency(concurrency int) int {
	if concurrency < 1 {
		return DefaultConcurrency
	}
	if concurrency > maxConcurrency {
		return maxConcurrency
	}
	return concurrency
}

// Start runs the syncer in a goroutine and returns a channel that is closed
// once Run has returned after ctx is cancelled.
func (s *Syncer) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx)
	}()
	return done
}

// Run processes scheduled cycles and manual user requests until ctx is
// cancelled, and returns after all in-flight source work has stopped.
func (s *Syncer) Run(ctx context.Context) {
	var loops sync.WaitGroup
	loops.Go(func() { s.runSchedule(ctx) })
	loops.Go(func() { s.runUserRequests(ctx) })
	loops.Wait()
}

func (s *Syncer) runSchedule(ctx context.Context) {
	timer := time.NewTimer(s.initialDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			timer.Reset(s.interval)
		case <-s.control.cycleWake:
		}
		if ctx.Err() != nil {
			return
		}
		s.control.beginCycle()
		s.refresh(ctx, 0)
		s.control.finishCycle()
	}
}

func (s *Syncer) runUserRequests(ctx context.Context) {
	for {
		userID, ok := s.control.nextUser()
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-s.control.userWake:
			}
			continue
		}
		if ctx.Err() == nil {
			s.refresh(ctx, userID)
		}
		s.control.finishUser(userID)
	}
}

func (s *Syncer) refresh(ctx context.Context, userID int64) {
	var targets []store.FeedTarget
	var err error
	if userID != 0 {
		targets, err = s.store.SourcesForUser(ctx, userID)
	} else {
		targets, err = s.store.DueSources(ctx)
	}
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("rss refresh: list due feeds: %v", err)
		}
		return
	}
	processTargets(ctx, targets, s.concurrency, func(ctx context.Context, target store.FeedTarget) {
		s.refreshSource(ctx, target, userID)
	})
}

func (s *Syncer) refreshSource(ctx context.Context, target store.FeedTarget, userID int64) {
	// Scheduled cycles and user requests share one budget of concurrent sources.
	select {
	case s.slots <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-s.slots }()
	if ctx.Err() != nil {
		return
	}
	var requester *int64
	if userID != 0 {
		requester = &userID
	}
	lease, claimed, err := s.store.ClaimSource(ctx, target.ID, requester)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("rss refresh: claim source %d: %v", target.ID, err)
		}
		return
	}
	if !claimed {
		return
	}
	feedContext, cancel := context.WithTimeout(ctx, fetchTimeout)
	result, fetchErr := s.fetch(feedContext, lease.URL, lease.Validators)
	cancel()
	if ctx.Err() != nil {
		s.abandon(ctx, lease)
		return
	}
	if fetchErr != nil {
		message := strings.TrimSpace(fetchErr.Error())
		if err := s.store.ReleaseSourceLease(ctx, lease, message); err != nil {
			log.Printf("rss refresh: release failed source %d: %v", target.ID, err)
		}
		return
	}
	lease.Validators = result.Validators
	if result.NotModified {
		err = s.store.CommitSourceNotModified(ctx, lease)
	} else {
		err = s.store.CommitSourceRefresh(ctx, lease, result.Document)
	}
	if err != nil {
		if ctx.Err() != nil {
			s.abandon(ctx, lease)
			return
		}
		log.Printf("rss refresh: commit source %d: %v", target.ID, err)
	}
}

// abandon gives up a lease after cancellation without recording an attempt.
// It uses a short cleanup context because ctx is already cancelled.
func (s *Syncer) abandon(ctx context.Context, lease store.SourceLease) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), leaseCleanupTimeout)
	defer cancel()
	if err := s.store.AbandonSourceLease(cleanup, lease); err != nil && !errors.Is(err, store.ErrLeaseLost) {
		log.Printf("rss refresh: abandon lease of source %d: %v", lease.ID, err)
	}
}

func processTargets(ctx context.Context, targets []store.FeedTarget, concurrency int, process func(context.Context, store.FeedTarget)) {
	workerCount := min(normalizeConcurrency(concurrency), len(targets))
	if workerCount == 0 {
		return
	}

	jobs := make(chan store.FeedTarget)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case target, open := <-jobs:
					if !open {
						return
					}
					if ctx.Err() != nil {
						return
					}
					process(ctx, target)
				}
			}
		}()
	}

	for _, target := range targets {
		select {
		case jobs <- target:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return
		}
	}
	close(jobs)
	workers.Wait()
}
