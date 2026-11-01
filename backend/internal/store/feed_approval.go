package store

import (
	"context"
	"errors"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// FeedRequest is one pending URL. RequestedBy, FolderID, FolderName and Title
// describe the first requester; Requesters lists every requester's e-mail address
// in the order they asked.
type FeedRequest struct {
	ID          int64    `json:"id"`
	URL         string   `json:"url"`
	RequestedBy string   `json:"requestedBy"`
	Requesters  []string `json:"requesters"`
	FolderID    *int64   `json:"folderId"`
	FolderName  *string  `json:"folderName"`
	Title       string   `json:"title"`
	CreatedAt   string   `json:"createdAt"`
}

// FeedApprovalSkip records a requester whose subscription was not created on approval.
type FeedApprovalSkip struct {
	UserID int64
	Reason string
}

// FeedApprovalResult describes an approved request.
type FeedApprovalResult struct {
	SourceID   int64
	Subscribed int
	Skipped    []FeedApprovalSkip
}

const (
	FeedApprovalSkipFeedLimit      = "feed limit reached"
	FeedApprovalSkipReplaceMissing = "subscription to replace no longer exists"
	FeedApprovalSkipDuplicate      = "already subscribed to the approved source"
)

type FeedRequestResult struct {
	RequestID int64
	SourceID  int64
	Approved  bool
}

var (
	ErrFeedRequestLimitReached = errors.New("the user has reached the pending feed request limit")
)

// MaxPendingFeedRequestsPerUser caps unapproved requests (new feeds and URL
// replacements) per requester so the administrator queue cannot be flooded.
const MaxPendingFeedRequestsPerUser = 10

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
	var unsubscribed bool
	err = tx.QueryRow(ctx, `SELECT id, unsubscribed_since IS NOT NULL FROM feed_sources WHERE normalized_url=$1`, normalizedURL).Scan(&sourceID, &unsubscribed)
	if err == nil && unsubscribed {
		// End the source's grace period before the caller subscribes in its own
		// transaction, so the maintenance job cannot delete the source in between. If
		// the job has just deleted it, the URL needs approval again.
		err = tx.QueryRow(ctx, `UPDATE feed_sources SET unsubscribed_since=NULL WHERE id=$1 RETURNING id`, sourceID).Scan(&sourceID)
		if err == nil {
			if err := tx.Commit(ctx); err != nil {
				return FeedRequestResult{}, err
			}
		}
	}
	if err == nil {
		return FeedRequestResult{SourceID: sourceID, Approved: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return FeedRequestResult{}, err
	}
	// Lock the requester so concurrent requests cannot both pass the count.
	var lockedUser int64
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&lockedUser); errors.Is(err, pgx.ErrNoRows) {
		return FeedRequestResult{}, ErrNotFound
	} else if err != nil {
		return FeedRequestResult{}, err
	}
	var requestID int64
	err = tx.QueryRow(ctx, `SELECT r.request_id FROM feed_request_requesters r JOIN feed_requests fr ON fr.id=r.request_id
		WHERE fr.normalized_url=$1 AND r.user_id=$2`, normalizedURL, userID).Scan(&requestID)
	if err == nil {
		// Asking again for a URL you already requested keeps your place in the queue
		// but replaces what approval will do for you: the latest folder, title, and
		// subscription to replace (or none, for a new feed) win.
		if _, err := tx.Exec(ctx, `UPDATE feed_request_requesters SET folder_id=$3,title=$4,replace_feed_id=$5
			WHERE request_id=$1 AND user_id=$2`, requestID, userID, folderID, title, replaceFeedID); err != nil {
			return FeedRequestResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return FeedRequestResult{}, err
		}
		return FeedRequestResult{RequestID: requestID}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return FeedRequestResult{}, err
	}
	var pendingCount int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM feed_request_requesters WHERE user_id=$1`, userID).Scan(&pendingCount); err != nil {
		return FeedRequestResult{}, err
	}
	if pendingCount >= MaxPendingFeedRequestsPerUser {
		return FeedRequestResult{}, ErrFeedRequestLimitReached
	}
	// Create the request or join the pending one. DO UPDATE locks an existing row, so
	// a concurrent approval or rejection either finishes first (and a new request is
	// inserted) or waits until this requester has been added.
	if err := tx.QueryRow(ctx, `INSERT INTO feed_requests (normalized_url,url) VALUES ($1,$1)
		ON CONFLICT (normalized_url) DO UPDATE SET normalized_url=EXCLUDED.normalized_url RETURNING id`, normalizedURL).Scan(&requestID); err != nil {
		return FeedRequestResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO feed_request_requesters (request_id,user_id,folder_id,title,replace_feed_id)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT (request_id,user_id) DO NOTHING`, requestID, userID, folderID, title, replaceFeedID); err != nil {
		return FeedRequestResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FeedRequestResult{}, err
	}
	return FeedRequestResult{RequestID: requestID}, nil
}

// ListPendingFeedRequests returns up to limit pending requests after skipping
// offset, oldest first, and the total number of pending requests.
func (s *Store) ListPendingFeedRequests(ctx context.Context, limit, offset int) ([]FeedRequest, int, error) {
	// Read the page and the total from one snapshot so they agree.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx)
	var total int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM feed_requests fr
		WHERE EXISTS(SELECT 1 FROM feed_request_requesters r WHERE r.request_id=fr.id)`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.Query(ctx, `SELECT fr.id,fr.url,fu.email,first.folder_id,f.name,first.title,fr.created_at::text,
			ARRAY(SELECT u.email FROM feed_request_requesters r JOIN users u ON u.id=r.user_id
				WHERE r.request_id=fr.id ORDER BY r.created_at,r.user_id)
		FROM feed_requests fr
		JOIN LATERAL (SELECT r.user_id,r.folder_id,r.title FROM feed_request_requesters r
			WHERE r.request_id=fr.id ORDER BY r.created_at,r.user_id LIMIT 1) first ON TRUE
		JOIN users fu ON fu.id=first.user_id
		LEFT JOIN folders f ON f.id=first.folder_id AND f.owner_user_id=first.user_id
		ORDER BY fr.created_at,fr.id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	requests, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (FeedRequest, error) {
		var request FeedRequest
		err := row.Scan(&request.ID, &request.URL, &request.RequestedBy, &request.FolderID, &request.FolderName, &request.Title, &request.CreatedAt, &request.Requesters)
		return request, err
	})
	if err != nil {
		return nil, 0, err
	}
	if requests == nil {
		requests = make([]FeedRequest, 0)
	}
	return requests, total, nil
}

type feedRequester struct {
	userID        int64
	folderID      *int64
	title         string
	replaceFeedID *int64
}

// ApproveFeedRequest creates (or reuses) the source and subscribes every requester in
// one transaction. A requester at the subscription limit, whose subscription to
// replace has vanished, or whose URL change would duplicate an existing subscription
// is skipped and reported instead of failing the whole approval.
func (s *Store) ApproveFeedRequest(ctx context.Context, requestID int64) (FeedApprovalResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return FeedApprovalResult{}, err
	}
	defer tx.Rollback(ctx)
	var normalizedURL string
	err = tx.QueryRow(ctx, `SELECT normalized_url FROM feed_requests WHERE id=$1 FOR UPDATE`, requestID).Scan(&normalizedURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return FeedApprovalResult{}, ErrNotFound
	}
	if err != nil {
		return FeedApprovalResult{}, err
	}
	rows, err := tx.Query(ctx, `SELECT user_id,folder_id,title,replace_feed_id FROM feed_request_requesters
		WHERE request_id=$1 ORDER BY created_at,user_id`, requestID)
	if err != nil {
		return FeedApprovalResult{}, err
	}
	requesters, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (feedRequester, error) {
		var requester feedRequester
		err := row.Scan(&requester.userID, &requester.folderID, &requester.title, &requester.replaceFeedID)
		return requester, err
	})
	if err != nil {
		return FeedApprovalResult{}, err
	}
	if len(requesters) == 0 {
		// Nobody is waiting for this URL any more; drop the stale request.
		if _, err := tx.Exec(ctx, `DELETE FROM feed_requests WHERE id=$1`, requestID); err != nil {
			return FeedApprovalResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return FeedApprovalResult{}, err
		}
		return FeedApprovalResult{}, ErrNotFound
	}
	parsed, _ := url.Parse(normalizedURL)
	result := FeedApprovalResult{}
	err = tx.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url,added_by_user_id) VALUES ($1,$2,$1,$3)
		ON CONFLICT (normalized_url) DO UPDATE SET unsubscribed_since=NULL RETURNING id`, normalizedURL, parsed.Hostname(), requesters[0].userID).Scan(&result.SourceID)
	if err != nil {
		return FeedApprovalResult{}, err
	}
	for _, requester := range requesters {
		reason, err := subscribeRequester(ctx, tx, requester, result.SourceID)
		if err != nil {
			return FeedApprovalResult{}, err
		}
		if reason != "" {
			result.Skipped = append(result.Skipped, FeedApprovalSkip{UserID: requester.userID, Reason: reason})
		} else {
			result.Subscribed++
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM feed_requests WHERE id=$1`, requestID); err != nil {
		return FeedApprovalResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FeedApprovalResult{}, err
	}
	return result, nil
}

// subscribeRequester applies one requester's subscription inside a savepoint and
// returns a skip reason instead of an error for expected per-user conflicts.
func subscribeRequester(ctx context.Context, tx pgx.Tx, requester feedRequester, sourceID int64) (string, error) {
	savepoint, err := tx.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer savepoint.Rollback(ctx)
	if requester.replaceFeedID != nil {
		command, err := savepoint.Exec(ctx, `UPDATE user_feeds SET feed_source_id=$3,folder_id=$4,title_override=NULLIF($5,'') WHERE id=$1 AND user_id=$2`,
			*requester.replaceFeedID, requester.userID, sourceID, requester.folderID, requester.title)
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && pgError.Code == "23505" {
			return FeedApprovalSkipDuplicate, nil
		}
		if err != nil {
			return "", err
		}
		if command.RowsAffected() == 0 {
			return FeedApprovalSkipReplaceMissing, nil
		}
		return "", savepoint.Commit(ctx)
	}
	var lockedUser int64
	if err := savepoint.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, requester.userID).Scan(&lockedUser); err != nil {
		return "", err
	}
	var alreadySubscribed bool
	var feedCount int
	if err := savepoint.QueryRow(ctx, `SELECT COUNT(*), COALESCE(BOOL_OR(feed_source_id=$2),FALSE) FROM user_feeds WHERE user_id=$1`, requester.userID, sourceID).Scan(&feedCount, &alreadySubscribed); err != nil {
		return "", err
	}
	if alreadySubscribed {
		return "", nil
	}
	if feedCount >= MaxFeedsPerUser {
		return FeedApprovalSkipFeedLimit, nil
	}
	if _, err := savepoint.Exec(ctx, `INSERT INTO user_feeds (user_id,feed_source_id,folder_id) VALUES ($1,$2,$3) ON CONFLICT (user_id,feed_source_id) DO NOTHING`,
		requester.userID, sourceID, requester.folderID); err != nil {
		return "", err
	}
	return "", savepoint.Commit(ctx)
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
