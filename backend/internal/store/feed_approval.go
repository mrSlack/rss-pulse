package store

import (
	"context"
	"errors"
	"net/url"

	"github.com/jackc/pgx/v5"
)

type FeedRequest struct {
	ID          int64   `json:"id"`
	URL         string  `json:"url"`
	RequestedBy string  `json:"requestedBy"`
	FolderID    *int64  `json:"folderId"`
	FolderName  *string `json:"folderName"`
	Title       string  `json:"title"`
	CreatedAt   string  `json:"createdAt"`
}

type FeedRequestResult struct {
	RequestID int64
	SourceID  int64
	Approved  bool
}

var ErrFeedRequestPending = errors.New("a request for this feed is already pending")

// RequestFeedApproval stores only the submitted URL and request metadata. It does not create a source.
func (s *Store) RequestFeedApproval(ctx context.Context, userID int64, rawURL, title string, folderID, replaceFeedID *int64) (FeedRequestResult, error) {
	normalizedURL, err := NormalizeFeedURL(rawURL)
	if err != nil {
		return FeedRequestResult{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return FeedRequestResult{}, err
	}
	defer tx.Rollback(ctx)
	if folderID != nil {
		var folderExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM folders WHERE id=$1 AND owner_user_id=$2)`, *folderID, userID).Scan(&folderExists); err != nil {
			return FeedRequestResult{}, err
		}
		if !folderExists {
			return FeedRequestResult{}, ErrFolderUnavailable
		}
	}
	if replaceFeedID != nil {
		var feedExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_feeds WHERE id=$1 AND user_id=$2)`, *replaceFeedID, userID).Scan(&feedExists); err != nil {
			return FeedRequestResult{}, err
		}
		if !feedExists {
			return FeedRequestResult{}, ErrNotFound
		}
	} else {
		var count int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM user_feeds WHERE user_id=$1`, userID).Scan(&count); err != nil {
			return FeedRequestResult{}, err
		}
		if count >= MaxFeedsPerUser {
			return FeedRequestResult{}, ErrFeedLimitReached
		}
	}
	var sourceID int64
	err = tx.QueryRow(ctx, `SELECT id FROM feed_sources WHERE normalized_url=$1`, normalizedURL).Scan(&sourceID)
	if err == nil {
		return FeedRequestResult{SourceID: sourceID, Approved: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return FeedRequestResult{}, err
	}
	var requestID int64
	err = tx.QueryRow(ctx, `INSERT INTO feed_requests (normalized_url,url,requested_by,folder_id,title,replace_feed_id)
		VALUES ($1,$1,$2,$3,$4,$5) ON CONFLICT (normalized_url) DO NOTHING RETURNING id`, normalizedURL, userID, folderID, title, replaceFeedID).Scan(&requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingRequester int64
		if err := tx.QueryRow(ctx, `SELECT id,requested_by FROM feed_requests WHERE normalized_url=$1`, normalizedURL).Scan(&requestID, &existingRequester); err != nil {
			return FeedRequestResult{}, err
		}
		if existingRequester != userID {
			return FeedRequestResult{}, ErrFeedRequestPending
		}
	} else if err != nil {
		return FeedRequestResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FeedRequestResult{}, err
	}
	return FeedRequestResult{RequestID: requestID}, nil
}

func (s *Store) ListPendingFeedRequests(ctx context.Context) ([]FeedRequest, error) {
	rows, err := s.pool.Query(ctx, `SELECT fr.id,fr.url,u.email,fr.folder_id,f.name,fr.title,fr.created_at::text
		FROM feed_requests fr JOIN users u ON u.id=fr.requested_by
		LEFT JOIN folders f ON f.id=fr.folder_id AND f.owner_user_id=fr.requested_by
		ORDER BY fr.created_at,fr.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	requests := make([]FeedRequest, 0)
	for rows.Next() {
		var request FeedRequest
		if err := rows.Scan(&request.ID, &request.URL, &request.RequestedBy, &request.FolderID, &request.FolderName, &request.Title, &request.CreatedAt); err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

func (s *Store) ApproveFeedRequest(ctx context.Context, requestID int64) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var normalizedURL, urlValue, title string
	var userID int64
	var folderID, replaceFeedID *int64
	err = tx.QueryRow(ctx, `SELECT normalized_url,url,requested_by,folder_id,title,replace_feed_id FROM feed_requests WHERE id=$1 FOR UPDATE`, requestID).Scan(&normalizedURL, &urlValue, &userID, &folderID, &title, &replaceFeedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if replaceFeedID == nil {
		var lockedUser int64
		if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&lockedUser); errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		} else if err != nil {
			return 0, err
		}
		var feedCount int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM user_feeds WHERE user_id=$1`, userID).Scan(&feedCount); err != nil {
			return 0, err
		}
		if feedCount >= MaxFeedsPerUser {
			return 0, ErrFeedLimitReached
		}
	}
	parsed, _ := url.Parse(normalizedURL)
	var sourceID int64
	err = tx.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url,added_by_user_id) VALUES ($1,$2,$1,$3)
		ON CONFLICT (normalized_url) DO UPDATE SET normalized_url=EXCLUDED.normalized_url RETURNING id`, normalizedURL, parsed.Hostname(), userID).Scan(&sourceID)
	if err != nil {
		return 0, err
	}
	if replaceFeedID != nil {
		command, err := tx.Exec(ctx, `UPDATE user_feeds SET feed_source_id=$3,folder_id=$4,title_override=NULLIF($5,'') WHERE id=$1 AND user_id=$2`, *replaceFeedID, userID, sourceID, folderID, title)
		if err != nil {
			return 0, err
		}
		if command.RowsAffected() == 0 {
			return 0, ErrNotFound
		}
	} else {
		if _, err := tx.Exec(ctx, `INSERT INTO user_feeds (user_id,feed_source_id,folder_id) VALUES ($1,$2,$3) ON CONFLICT (user_id,feed_source_id) DO NOTHING`, userID, sourceID, folderID); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM feed_requests WHERE id=$1`, requestID); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return sourceID, nil
}

func (s *Store) RejectFeedRequest(ctx context.Context, requestID int64) error {
	command, err := s.pool.Exec(ctx, `DELETE FROM feed_requests WHERE id=$1`, requestID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
