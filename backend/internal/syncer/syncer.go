package syncer

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pulse-reader/app/internal/feeds"
	"github.com/pulse-reader/app/internal/store"
)

type Syncer struct {
	store       RefreshRepository
	control     *Control
	concurrency int
}

// RefreshRepository is the part of the store API used by the scheduler.
type RefreshRepository interface {
	DueSources(context.Context) ([]store.FeedTarget, error)
	SourcesForUser(context.Context, int64) ([]store.FeedTarget, error)
	ClaimSource(context.Context, int64, *int64) (store.SourceLease, bool, error)
	ReleaseSourceLease(context.Context, store.SourceLease, string) error
	CommitSourceRefresh(context.Context, store.SourceLease, feeds.Document) error
}

const (
	DefaultConcurrency = 4
	maxConcurrency     = 32
)

type Control struct {
	trigger chan RefreshRequest
	state   atomic.Uint32
}

type RefreshRequest struct {
	Force  bool
	UserID int64
}

func NewControl() *Control {
	return &Control{trigger: make(chan RefreshRequest, 1)}
}

func (c *Control) Request(force bool) bool {
	return c.enqueue(RefreshRequest{Force: force}, force)
}

func (c *Control) RequestUser(userID int64) bool {
	return c.enqueue(RefreshRequest{UserID: userID}, true)
}

func (c *Control) enqueue(request RefreshRequest, allowPending bool) bool {
	for {
		state := c.state.Load()
		switch state {
		case 0:
			if !c.state.CompareAndSwap(0, 1) {
				continue
			}
		case 2:
			if !allowPending || !c.state.CompareAndSwap(2, 3) {
				if allowPending && c.state.Load() != 2 {
					continue
				}
				return false
			}
		default:
			return false
		}
		select {
		case c.trigger <- request:
			return true
		default:
			if allowPending && state == 2 {
				c.state.CompareAndSwap(3, 2)
			} else {
				c.state.CompareAndSwap(1, 0)
			}
			return false
		}
	}
}

func (c *Control) finishRefresh() {
	if !c.state.CompareAndSwap(3, 1) {
		c.state.CompareAndSwap(2, 0)
	}
}

func (c *Control) IsRunning() bool {
	return c.state.Load() != 0
}

func New(repository RefreshRepository, control *Control, concurrency int) *Syncer {
	return &Syncer{store: repository, control: control, concurrency: normalizeConcurrency(concurrency)}
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

func (s *Syncer) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.control.state.CompareAndSwap(0, 2) {
				s.refresh(ctx, RefreshRequest{})
				s.control.finishRefresh()
			}
		case request := <-s.control.trigger:
			s.control.state.Store(2)
			s.refresh(ctx, request)
			s.control.finishRefresh()
		}
	}
}

func (s *Syncer) refresh(ctx context.Context, request RefreshRequest) {
	var targets []store.FeedTarget
	var err error
	if request.UserID != 0 {
		targets, err = s.store.SourcesForUser(ctx, request.UserID)
	} else {
		targets, err = s.store.DueSources(ctx)
	}
	if err != nil {
		log.Printf("rss refresh: list due feeds: %v", err)
		return
	}
	processTargets(ctx, targets, s.concurrency, func(ctx context.Context, target store.FeedTarget) {
		s.refreshSource(ctx, target, request.UserID)
	})
}

func (s *Syncer) refreshSource(ctx context.Context, target store.FeedTarget, userID int64) {
	if ctx.Err() != nil {
		return
	}
	var requester *int64
	if userID != 0 {
		requester = &userID
	}
	lease, claimed, err := s.store.ClaimSource(ctx, target.ID, requester)
	if err != nil {
		log.Printf("rss refresh: claim source %d: %v", target.ID, err)
		return
	}
	if !claimed {
		return
	}
	feedContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	document, fetchErr := feeds.Fetch(feedContext, lease.URL)
	cancel()
	if fetchErr != nil {
		if ctx.Err() != nil {
			_ = s.store.ReleaseSourceLease(context.Background(), lease, "")
			return
		}
		message := strings.TrimSpace(fetchErr.Error())
		if err := s.store.ReleaseSourceLease(ctx, lease, message); err != nil {
			log.Printf("rss refresh: release failed source %d: %v", target.ID, err)
		}
		return
	}
	if err := s.store.CommitSourceRefresh(ctx, lease, document); err != nil {
		log.Printf("rss refresh: commit source %d: %v", target.ID, err)
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
