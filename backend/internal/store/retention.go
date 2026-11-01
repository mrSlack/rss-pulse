package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pulse-reader/app/internal/feeds"
)

// SetArticleRetentionDays configures the cutoff used by refresh commits to avoid
// re-inserting articles that the retention job has already deleted. 0 disables it.
func (s *Store) SetArticleRetentionDays(days int) {
	if days < 0 {
		days = 0
	}
	s.articleRetentionDays.Store(int32(days))
}

func (s *Store) ArticleRetentionDays() int {
	return int(s.articleRetentionDays.Load())
}

// DeleteExpiredArticles deletes, in batches of batchSize, articles whose publication and
// creation times are both older than retentionDays and that no user has saved. Per-user
// states cascade. It returns the total number of deleted articles; 0 days disables it.
func (s *Store) DeleteExpiredArticles(ctx context.Context, retentionDays, batchSize int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	if batchSize < 1 {
		batchSize = 5000
	}
	var total int64
	for {
		command, err := s.pool.Exec(ctx, `WITH expired AS (
				SELECT a.id FROM articles a
				WHERE a.published_at < NOW() - make_interval(days => $1)
				AND a.created_at < NOW() - make_interval(days => $1)
				AND NOT EXISTS (SELECT 1 FROM user_article_states state WHERE state.article_id = a.id AND state.is_saved)
				LIMIT $2
				FOR UPDATE OF a SKIP LOCKED
			)
			DELETE FROM articles a USING expired WHERE a.id = expired.id`, retentionDays, batchSize)
		if err != nil {
			return total, err
		}
		total += command.RowsAffected()
		if command.RowsAffected() < int64(batchSize) {
			return total, nil
		}
	}
}

// DeleteExpiredQuarantine removes decided quarantine records and non-moderatable legacy
// records that have not been seen in a refresh for retentionDays. Pending records that
// can still be moderated are never deleted. 0 days disables it.
func (s *Store) DeleteExpiredQuarantine(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	command, err := s.pool.Exec(ctx, `DELETE FROM blocked_feed_articles
		WHERE last_blocked_at < NOW() - make_interval(days => $1)
		AND (
			(status IN ('allowed','rejected') AND decided_at < NOW() - make_interval(days => $1))
			OR (status = 'pending' AND (moderation_eligible = FALSE OR summary IS NULL))
		)`, retentionDays)
	if err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	command, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= NOW()`)
	if err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}

// dropItemsBeyondRetention removes dated items older than the retention cutoff unless an
// article with the same GUID is already stored, so deleted articles do not reappear as unread.
func dropItemsBeyondRetention(ctx context.Context, tx pgx.Tx, sourceID int64, items []feeds.Item, retentionDays int) ([]feeds.Item, error) {
	if retentionDays <= 0 {
		return items, nil
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	expiredGUIDs := make([]string, 0)
	for _, item := range items {
		if item.HasPublishedAt && item.PublishedAt.Before(cutoff) {
			expiredGUIDs = append(expiredGUIDs, item.GUID)
		}
	}
	if len(expiredGUIDs) == 0 {
		return items, nil
	}
	rows, err := tx.Query(ctx, `SELECT guid FROM articles WHERE feed_source_id=$1 AND guid = ANY($2::text[])`, sourceID, expiredGUIDs)
	if err != nil {
		return nil, err
	}
	stored := make(map[string]bool)
	for rows.Next() {
		var guid string
		if err := rows.Scan(&guid); err != nil {
			rows.Close()
			return nil, err
		}
		stored[guid] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	kept := make([]feeds.Item, 0, len(items))
	for _, item := range items {
		if item.HasPublishedAt && item.PublishedAt.Before(cutoff) && !stored[item.GUID] {
			continue
		}
		kept = append(kept, item)
	}
	return kept, nil
}
