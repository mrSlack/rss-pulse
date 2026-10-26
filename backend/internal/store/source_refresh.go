package store

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/pulse-reader/app/internal/auth"
	"github.com/pulse-reader/app/internal/feeds"
)

type SourceLease struct {
	ID    int64
	URL   string
	Token string
}

func (s *Store) DueSources(ctx context.Context) ([]FeedTarget, error) {
	rows, err := s.pool.Query(ctx, `SELECT fs.id, fs.url FROM feed_sources fs
		WHERE (fs.last_success_at IS NULL OR fs.last_success_at <= NOW() - ((SELECT value FROM system_settings WHERE key='shared_feed_ttl_minutes') * INTERVAL '1 minute'))
		AND EXISTS (SELECT 1 FROM user_feeds uf WHERE uf.feed_source_id = fs.id)
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

func (s *Store) CheckUserFeedLimit(ctx context.Context, userID int64) error {
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_feeds WHERE user_id=$1`, userID).Scan(&count); err != nil {
		return err
	}
	if count >= MaxFeedsPerUser {
		return ErrFeedLimitReached
	}
	return nil
}

func (s *Store) ClaimSource(ctx context.Context, sourceID int64, userID *int64) (SourceLease, bool, error) {
	token, err := auth.NewSessionToken()
	if err != nil {
		return SourceLease{}, false, err
	}
	var lease SourceLease
	err = s.pool.QueryRow(ctx, `UPDATE feed_sources fs SET lease_token=$2, lease_expires_at=NOW()+INTERVAL '45 seconds'
		WHERE fs.id=$1
		AND (fs.last_success_at IS NULL OR fs.last_success_at <= NOW() - ((SELECT value FROM system_settings WHERE key='shared_feed_ttl_minutes') * INTERVAL '1 minute'))
		AND (fs.lease_expires_at IS NULL OR fs.lease_expires_at <= NOW())
		AND ($3::bigint IS NULL OR EXISTS (SELECT 1 FROM user_feeds uf WHERE uf.feed_source_id=fs.id AND uf.user_id=$3))
		RETURNING fs.id,fs.url,fs.lease_token`, sourceID, token, userID).Scan(&lease.ID, &lease.URL, &lease.Token)
	if errors.Is(err, pgx.ErrNoRows) {
		return SourceLease{}, false, nil
	}
	if err != nil {
		return SourceLease{}, false, err
	}
	return lease, true, nil
}

func (s *Store) CommitSourceRefresh(ctx context.Context, lease SourceLease, document feeds.Document) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var ownedID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM feed_sources WHERE id=$1 AND lease_token=$2 AND lease_expires_at>NOW() FOR UPDATE`, lease.ID, lease.Token).Scan(&ownedID); errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	} else if err != nil {
		return err
	}
	if err := upsertSourceItems(ctx, tx, lease.ID, document.Items); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE feed_sources SET title=$3,site_url=$4,last_checked_at=NOW(),last_success_at=NOW(),last_error=NULL,lease_token=NULL,lease_expires_at=NULL WHERE id=$1 AND lease_token=$2 AND lease_expires_at>NOW()`, lease.ID, lease.Token, document.Title, document.SiteURL)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return tx.Commit(ctx)
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

func (s *Store) EnsureFeedSourceForUser(ctx context.Context, userID int64, rawURL string, folderID *int64) (int64, bool, error) {
	normalizedURL, err := NormalizeFeedURL(rawURL)
	if err != nil {
		return 0, false, err
	}
	if folderID != nil {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM folders WHERE id=$1 AND owner_user_id=$2)`, *folderID, userID).Scan(&exists); err != nil {
			return 0, false, err
		}
		if !exists {
			return 0, false, ErrFolderUnavailable
		}
	}
	var alreadySubscribed bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_feeds uf JOIN feed_sources fs ON fs.id=uf.feed_source_id WHERE uf.user_id=$1 AND fs.normalized_url=$2)`, userID, normalizedURL).Scan(&alreadySubscribed); err != nil {
		return 0, false, err
	}
	if alreadySubscribed {
		return 0, false, ErrDuplicate
	}
	if err := s.CheckUserFeedLimit(ctx, userID); err != nil {
		return 0, false, err
	}
	parsed, _ := url.Parse(normalizedURL)
	fallbackTitle := parsed.Hostname()
	var id int64
	err = s.pool.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url) VALUES ($1,$2,$1) ON CONFLICT (normalized_url) DO NOTHING RETURNING id`, normalizedURL, fallbackTitle).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, err
	}
	err = s.pool.QueryRow(ctx, `SELECT id FROM feed_sources WHERE normalized_url=$1`, normalizedURL).Scan(&id)
	return id, false, err
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

func (s *Store) DiscardEmptySource(ctx context.Context, sourceID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM feed_sources fs WHERE fs.id=$1 AND fs.last_success_at IS NULL AND NOT EXISTS (SELECT 1 FROM user_feeds uf WHERE uf.feed_source_id=fs.id)`, sourceID)
	return err
}

func limitError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 500 {
		return message[:500]
	}
	return message
}
