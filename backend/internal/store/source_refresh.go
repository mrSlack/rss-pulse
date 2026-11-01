package store

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/pulse-reader/app/internal/auth"
	"github.com/pulse-reader/app/internal/feeds"
)

type SourceLease struct {
	ID    int64
	URL   string
	Token string
	// Validators are the stored HTTP cache validators when the lease is claimed.
	// The refresher replaces them with the response's validators before a
	// commit, which stores them on the source.
	Validators feeds.Validators
}

// refreshSettingsSQL selects the shared-source TTL and the global scheduler
// interval in minutes. See docs/2026-11-01-scheduler-design.md.
const refreshSettingsSQL = `settings AS (
	SELECT (SELECT value FROM system_settings WHERE key='shared_feed_ttl_minutes') AS ttl_minutes,
		(SELECT value FROM system_settings WHERE key='refresh_interval_minutes') AS global_minutes
)`

// duePolicySQL selects, for every source with at least one enabled subscriber, the
// effective scheduled interval p.effective: the shortest subscriber interval
// (per-user override or global default), but never shorter than the shared-source
// TTL. It needs refreshSettingsSQL.
const duePolicySQL = `policy AS (
	SELECT uf.feed_source_id AS source_id,
		GREATEST(MIN(st.ttl_minutes), MIN(COALESCE(u.refresh_interval_minutes, st.global_minutes))) * INTERVAL '1 minute' AS effective
	FROM user_feeds uf JOIN users u ON u.id=uf.user_id AND u.disabled_at IS NULL CROSS JOIN settings st
	GROUP BY uf.feed_source_id
)`

// sourceDueSQL is true when no refresh attempt (successful or failed) happened
// within the interval p.effective. A success sets last_checked_at and
// last_success_at together and a failure sets only last_checked_at, so this
// covers both the success interval and the failure backoff.
const sourceDueSQL = `(GREATEST(fs.last_checked_at, fs.last_success_at) IS NULL
	OR GREATEST(fs.last_checked_at, fs.last_success_at) + p.effective <= NOW())`

// DueSources lists sources with at least one enabled subscriber that are due
// for a scheduled refresh (subscriptions of disabled accounts are ignored): the
// effective interval is the shortest subscriber interval (per-user override or
// global default), but never shorter than the shared-source TTL.
func (s *Store) DueSources(ctx context.Context) ([]FeedTarget, error) {
	rows, err := s.pool.Query(ctx, `WITH `+refreshSettingsSQL+`, `+duePolicySQL+`
		SELECT fs.id, fs.url FROM feed_sources fs JOIN policy p ON p.source_id=fs.id
		WHERE `+sourceDueSQL+`
		ORDER BY fs.last_success_at NULLS FIRST, fs.id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]FeedTarget, 0)
	for rows.Next() {
		var target FeedTarget
		if err := rows.Scan(&target.ID, &target.URL); err != nil {
			return nil, err
		}
		result = append(result, target)
	}
	return result, rows.Err()
}

func (s *Store) SourcesForUser(ctx context.Context, userID int64) ([]FeedTarget, error) {
	rows, err := s.pool.Query(ctx, `SELECT fs.id, fs.url FROM user_feeds uf JOIN feed_sources fs ON fs.id=uf.feed_source_id WHERE uf.user_id=$1 ORDER BY fs.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]FeedTarget, 0)
	for rows.Next() {
		var target FeedTarget
		if err := rows.Scan(&target.ID, &target.URL); err != nil {
			return nil, err
		}
		result = append(result, target)
	}
	return result, rows.Err()
}

func (s *Store) ClaimSource(ctx context.Context, sourceID int64, userID *int64) (SourceLease, bool, error) {
	token, err := auth.NewSessionToken()
	if err != nil {
		return SourceLease{}, false, err
	}
	var lease SourceLease
	// A scheduled claim (userID nil) needs a subscriber and uses the same
	// effective interval as DueSources. A manual claim needs the requester's
	// subscription and uses only the TTL, so "refresh now" can run before the
	// scheduler interval; both measure from the last attempt.
	err = s.pool.QueryRow(ctx, `WITH `+refreshSettingsSQL+`, policy AS (
			SELECT CASE WHEN $3::bigint IS NULL
					THEN GREATEST(st.ttl_minutes, MIN(COALESCE(u.refresh_interval_minutes, st.global_minutes)))
					ELSE st.ttl_minutes END * INTERVAL '1 minute' AS effective,
				BOOL_OR(uf.user_id = $3::bigint) AS requester_subscribed
			FROM user_feeds uf JOIN users u ON u.id=uf.user_id AND u.disabled_at IS NULL CROSS JOIN settings st
			WHERE uf.feed_source_id=$1
			GROUP BY st.ttl_minutes, st.global_minutes
		)
		UPDATE feed_sources fs SET lease_token=$2, lease_expires_at=NOW()+INTERVAL '45 seconds'
		FROM policy p
		WHERE fs.id=$1
		AND `+sourceDueSQL+`
		AND (fs.lease_expires_at IS NULL OR fs.lease_expires_at <= NOW())
		AND ($3::bigint IS NULL OR p.requester_subscribed)
		RETURNING fs.id,fs.url,fs.lease_token,COALESCE(fs.etag,''),COALESCE(fs.last_modified,'')`, sourceID, token, userID).
		Scan(&lease.ID, &lease.URL, &lease.Token, &lease.Validators.ETag, &lease.Validators.LastModified)
	if errors.Is(err, pgx.ErrNoRows) {
		return SourceLease{}, false, nil
	}
	if err != nil {
		return SourceLease{}, false, err
	}
	return lease, true, nil
}

// RefreshCounts reports what a committed refresh stored.
type RefreshCounts struct {
	// Inserted is the number of new articles.
	Inserted int
	// Quarantined is the number of items newly held for moderation by the stop-word
	// filter (items already in quarantine are not counted again).
	Quarantined int
}

// CommitSourceRefresh stores a fetched document for a leased source and releases the
// lease. The counts are only meaningful when err is nil.
func (s *Store) CommitSourceRefresh(ctx context.Context, lease SourceLease, document feeds.Document) (RefreshCounts, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RefreshCounts{}, err
	}
	defer tx.Rollback(ctx)
	var ownedID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM feed_sources WHERE id=$1 AND lease_token=$2 AND lease_expires_at>NOW() FOR UPDATE`, lease.ID, lease.Token).Scan(&ownedID); errors.Is(err, pgx.ErrNoRows) {
		return RefreshCounts{}, ErrLeaseLost
	} else if err != nil {
		return RefreshCounts{}, err
	}
	items, err := dropItemsBeyondRetention(ctx, tx, lease.ID, document.Items, s.ArticleRetentionDays())
	if err != nil {
		return RefreshCounts{}, err
	}
	var counts RefreshCounts
	allowedItems, quarantined, err := filterAndLogBlockedItems(ctx, tx, lease.ID, document.Title, items)
	if err != nil {
		return RefreshCounts{}, err
	}
	inserted, err := upsertSourceItems(ctx, tx, lease.ID, allowedItems)
	if err != nil {
		return RefreshCounts{}, err
	}
	command, err := tx.Exec(ctx, `UPDATE feed_sources SET title=$3,site_url=$4,last_checked_at=NOW(),last_success_at=NOW(),last_error=NULL,
		etag=NULLIF($5,''),last_modified=NULLIF($6,''),lease_token=NULL,lease_expires_at=NULL
		WHERE id=$1 AND lease_token=$2 AND lease_expires_at>NOW()`, lease.ID, lease.Token, document.Title, document.SiteURL, lease.Validators.ETag, lease.Validators.LastModified)
	if err != nil {
		return RefreshCounts{}, err
	}
	if command.RowsAffected() != 1 {
		return RefreshCounts{}, ErrLeaseLost
	}
	if err := tx.Commit(ctx); err != nil {
		return RefreshCounts{}, err
	}
	counts.Inserted, counts.Quarantined = inserted, quarantined
	return counts, nil
}

// CommitSourceNotModified records a successful 304 response: the source counts
// as refreshed, its error is cleared, and its articles are left untouched.
func (s *Store) CommitSourceNotModified(ctx context.Context, lease SourceLease) error {
	command, err := s.pool.Exec(ctx, `UPDATE feed_sources SET last_checked_at=NOW(),last_success_at=NOW(),last_error=NULL,
		etag=NULLIF($3,''),last_modified=NULLIF($4,''),lease_token=NULL,lease_expires_at=NULL
		WHERE id=$1 AND lease_token=$2 AND lease_expires_at>NOW()`, lease.ID, lease.Token, lease.Validators.ETag, lease.Validators.LastModified)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

// AbandonSourceLease releases a lease without recording an attempt, for
// refreshes interrupted by shutdown. The source keeps its schedule.
func (s *Store) AbandonSourceLease(ctx context.Context, lease SourceLease) error {
	command, err := s.pool.Exec(ctx, `UPDATE feed_sources SET lease_token=NULL,lease_expires_at=NULL WHERE id=$1 AND lease_token=$2`, lease.ID, lease.Token)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Store) ReleaseSourceLease(ctx context.Context, lease SourceLease, message string) error {
	command, err := s.pool.Exec(ctx, `UPDATE feed_sources SET last_checked_at=NOW(),last_error=$3,lease_token=NULL,lease_expires_at=NULL WHERE id=$1 AND lease_token=$2`, lease.ID, lease.Token, limitError(message))
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Store) SubscribeSource(ctx context.Context, userID, sourceID int64, folderID *int64) (Feed, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Feed{}, err
	}
	defer tx.Rollback(ctx)
	var lockedUserID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&lockedUserID); errors.Is(err, pgx.ErrNoRows) {
		return Feed{}, ErrNotFound
	} else if err != nil {
		return Feed{}, err
	}
	var alreadySubscribed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_feeds WHERE user_id=$1 AND feed_source_id=$2)`, userID, sourceID).Scan(&alreadySubscribed); err != nil {
		return Feed{}, err
	}
	if alreadySubscribed {
		return Feed{}, ErrDuplicate
	}
	var feedCount int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM user_feeds WHERE user_id=$1`, userID).Scan(&feedCount); err != nil {
		return Feed{}, err
	}
	if feedCount >= MaxFeedsPerUser {
		return Feed{}, ErrFeedLimitReached
	}
	if folderID != nil {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM folders WHERE id=$1 AND owner_user_id=$2)`, *folderID, userID).Scan(&exists); err != nil {
			return Feed{}, err
		}
		if !exists {
			return Feed{}, ErrFolderUnavailable
		}
	}
	var feedID int64
	err = tx.QueryRow(ctx, `INSERT INTO user_feeds (user_id,feed_source_id,folder_id) VALUES ($1,$2,$3) ON CONFLICT (user_id,feed_source_id) DO NOTHING RETURNING id`, userID, sourceID, folderID).Scan(&feedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Feed{}, ErrDuplicate
	}
	if err != nil {
		return Feed{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Feed{}, err
	}
	feedsForUser, err := s.ListFeedsForUser(ctx, userID)
	if err != nil {
		return Feed{}, err
	}
	for _, feed := range feedsForUser {
		if feed.ID == feedID {
			return feed, nil
		}
	}
	return Feed{}, ErrNotFound
}

func limitError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 500 {
		return message[:500]
	}
	return message
}
