package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// unsubscribedSourceExpiredSQL selects sources (alias fs) whose grace period of $1
// days without subscribers has ended and that nothing else still needs: no
// subscription (also of a disabled account), no pending feed request for the URL,
// no pending quarantine record an administrator can still moderate, and no active
// refresh lease.
const unsubscribedSourceExpiredSQL = `fs.unsubscribed_since IS NOT NULL
	AND fs.unsubscribed_since <= NOW() - make_interval(days => $1)
	AND NOT EXISTS (SELECT 1 FROM user_feeds uf WHERE uf.feed_source_id = fs.id)
	AND NOT EXISTS (SELECT 1 FROM feed_requests fr WHERE fr.normalized_url = fs.normalized_url)
	AND NOT EXISTS (SELECT 1 FROM blocked_feed_articles b WHERE b.feed_source_id = fs.id
		AND b.status = 'pending' AND b.moderation_eligible AND b.summary IS NOT NULL)
	AND (fs.lease_expires_at IS NULL OR fs.lease_expires_at <= NOW())`

// DeleteUnsubscribedSources maintains unsubscribed_since and deletes sources that
// have had no subscribers for graceDays (see unsubscribedSourceExpiredSQL). The
// grace period starts when a run first sees a source without subscribers, so it can
// begin up to one maintenance interval late. Articles are deleted in batches of
// batchSize, each in its own transaction that locks the source and checks again that
// it may be deleted; read and saved states and quarantine records cascade. When a
// user adds the source again in the meantime (which clears unsubscribed_since), the
// deletion stops and the source keeps its remaining articles. It returns the number
// of deleted sources and articles; 0 days disables it.
func (s *Store) DeleteUnsubscribedSources(ctx context.Context, graceDays, batchSize int) (int64, int64, error) {
	if graceDays <= 0 {
		return 0, 0, nil
	}
	if batchSize < 1 {
		batchSize = 5000
	}
	if _, err := s.pool.Exec(ctx, `UPDATE feed_sources fs SET unsubscribed_since = NULL
		WHERE fs.unsubscribed_since IS NOT NULL AND EXISTS (SELECT 1 FROM user_feeds uf WHERE uf.feed_source_id = fs.id)`); err != nil {
		return 0, 0, err
	}
	if _, err := s.pool.Exec(ctx, `UPDATE feed_sources fs SET unsubscribed_since = NOW()
		WHERE fs.unsubscribed_since IS NULL AND NOT EXISTS (SELECT 1 FROM user_feeds uf WHERE uf.feed_source_id = fs.id)`); err != nil {
		return 0, 0, err
	}
	rows, err := s.pool.Query(ctx, `SELECT fs.id FROM feed_sources fs WHERE `+unsubscribedSourceExpiredSQL+` ORDER BY fs.id`, graceDays)
	if err != nil {
		return 0, 0, err
	}
	candidates, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return 0, 0, err
	}
	var sources, articles int64
	for _, sourceID := range candidates {
		for {
			deleted, removed, err := s.deleteUnsubscribedSourceBatch(ctx, sourceID, graceDays, batchSize)
			articles += removed
			if err != nil {
				return sources, articles, err
			}
			if deleted {
				sources++
				break
			}
			if removed == 0 {
				// The source is locked or may no longer be deleted.
				break
			}
		}
	}
	return sources, articles, nil
}

// deleteUnsubscribedSourceBatch deletes up to batchSize articles of the source and,
// once none are left, the source. It does nothing when the source is locked or may
// no longer be deleted.
func (s *Store) deleteUnsubscribedSourceBatch(ctx context.Context, sourceID int64, graceDays, batchSize int) (deleted bool, removed int64, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback(ctx)
	// FOR UPDATE conflicts with the key-share lock a new subscription takes on the
	// source and with RequestFeedApproval clearing unsubscribed_since, so a concurrent
	// subscribe either waits for this batch or makes it skip the source.
	var locked int64
	err = tx.QueryRow(ctx, `SELECT fs.id FROM feed_sources fs WHERE fs.id = $2 AND `+unsubscribedSourceExpiredSQL+`
		FOR UPDATE OF fs SKIP LOCKED`, graceDays, sourceID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	command, err := tx.Exec(ctx, `DELETE FROM articles WHERE id IN (
		SELECT id FROM articles WHERE feed_source_id = $1 LIMIT $2)`, sourceID, batchSize)
	if err != nil {
		return false, 0, err
	}
	removed = command.RowsAffected()
	if removed < int64(batchSize) {
		if _, err := tx.Exec(ctx, `DELETE FROM feed_sources WHERE id = $1`, sourceID); err != nil {
			return false, 0, err
		}
		deleted = true
	}
	if err := tx.Commit(ctx); err != nil {
		return false, 0, err
	}
	return deleted, removed, nil
}
