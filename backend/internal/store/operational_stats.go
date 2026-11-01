package store

import (
	"context"
	"time"
)

// OperationalStats describes the refresh backlog and the moderation queues for
// monitoring.
type OperationalStats struct {
	// SourcesDue counts sources with an enabled subscriber whose refresh interval
	// has elapsed (the sources DueSources returns).
	SourcesDue int64
	// SourcesOverdue counts due sources that have been due for longer than the
	// tolerance passed to OperationalStats.
	SourcesOverdue int64
	// OldestOverdue is how long the longest-waiting due source has been due; a source
	// that was never fetched is due since it was created. Zero without due sources.
	OldestOverdue time.Duration
	// ActiveLeases counts sources with an unexpired refresh lease.
	ActiveLeases int64
	// PendingFeedRequests counts feed requests waiting for an administrator.
	PendingFeedRequests int64
	// PendingQuarantine counts quarantined articles without a decision.
	PendingQuarantine int64
}

// OperationalStats reads the monitoring values in one statement. The due-source part
// costs about as much as one DueSources call of the scheduler, so callers should cache
// the result (the metrics collector queries at most every few seconds).
func (s *Store) OperationalStats(ctx context.Context, overdueTolerance time.Duration) (OperationalStats, error) {
	var stats OperationalStats
	var oldestSeconds float64
	err := s.pool.QueryRow(ctx, `WITH `+refreshSettingsSQL+`, `+duePolicySQL+`, due AS (
			SELECT COALESCE(GREATEST(fs.last_checked_at, fs.last_success_at) + p.effective, fs.created_at) AS due_at
			FROM feed_sources fs JOIN policy p ON p.source_id=fs.id
			WHERE `+sourceDueSQL+`
		)
		SELECT (SELECT COUNT(*) FROM due),
			(SELECT COUNT(*) FROM due WHERE due_at < NOW() - $1 * INTERVAL '1 second'),
			COALESCE((SELECT GREATEST(EXTRACT(EPOCH FROM NOW() - MIN(due_at)), 0) FROM due), 0)::float8,
			(SELECT COUNT(*) FROM feed_sources WHERE lease_expires_at > NOW()),
			(SELECT COUNT(*) FROM feed_requests fr WHERE EXISTS(SELECT 1 FROM feed_request_requesters r WHERE r.request_id=fr.id)),
			(SELECT COUNT(*) FROM blocked_feed_articles WHERE status='pending')`, overdueTolerance.Seconds()).
		Scan(&stats.SourcesDue, &stats.SourcesOverdue, &oldestSeconds, &stats.ActiveLeases, &stats.PendingFeedRequests, &stats.PendingQuarantine)
	if err != nil {
		return OperationalStats{}, err
	}
	stats.OldestOverdue = time.Duration(oldestSeconds * float64(time.Second))
	return stats, nil
}
