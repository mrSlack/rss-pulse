package store

import (
	"context"
	"errors"
	"strings"
)

// SubscriptionImport is one feed of an imported subscription list.
type SubscriptionImport struct {
	URL string
	// Folder is the folder name to file the feed under, or "" for none. A
	// folder is matched case-insensitively and created when missing.
	Folder string
}

// SubscriptionImportFailure reports a feed that could not be imported. Reason
// is a stable API error code such as "feed.url_invalid".
type SubscriptionImportFailure struct {
	URL    string `json:"url"`
	Reason string `json:"reason"`
}

// SubscriptionImportResult summarizes ImportSubscriptions.
type SubscriptionImportResult struct {
	Subscribed int                         `json:"subscribed"`
	Requested  int                         `json:"requested"`
	Skipped    int                         `json:"skipped"`
	Failed     []SubscriptionImportFailure `json:"failed"`
}

// MaxImportFailuresReported caps the failures listed in an import result.
const MaxImportFailuresReported = 100

const maxImportURLLength = 2048

// ImportSubscriptions adds feeds to the user's library the way POST
// /api/feeds does: a URL of an existing (approved) source is subscribed
// directly, any other URL becomes or joins a pending approval request. The
// feed and pending-request limits apply per feed. URLs the user already
// follows and repeated URLs are skipped. A missing folder is created when the
// first feed with a valid URL refers to it.
func (s *Store) ImportSubscriptions(ctx context.Context, userID int64, feeds []SubscriptionImport) (SubscriptionImportResult, error) {
	result := SubscriptionImportResult{Failed: []SubscriptionImportFailure{}}
	fail := func(url, reason string) {
		if len(result.Failed) < MaxImportFailuresReported {
			result.Failed = append(result.Failed, SubscriptionImportFailure{URL: url, Reason: reason})
		}
	}
	seen, err := s.subscribedNormalizedURLs(ctx, userID)
	if err != nil {
		return SubscriptionImportResult{}, err
	}
	folders, err := s.ListFolderNamesForUser(ctx, userID)
	if err != nil {
		return SubscriptionImportResult{}, err
	}
	folderIDs := make(map[string]int64, len(folders))
	for _, folder := range folders {
		folderIDs[strings.ToLower(folder.Name)] = folder.ID
	}
	folderFor := func(name string) (*int64, error) {
		if name == "" {
			return nil, nil
		}
		key := strings.ToLower(name)
		if id, ok := folderIDs[key]; ok {
			return &id, nil
		}
		folder, err := s.CreateFolderForUser(ctx, userID, name, DefaultFolderColor)
		if errors.Is(err, ErrDuplicate) {
			// Created concurrently under another spelling: use that folder.
			err = s.pool.QueryRow(ctx, `SELECT id FROM folders WHERE owner_user_id=$1 AND LOWER(name)=LOWER($2)`, userID, name).Scan(&folder.ID)
		}
		if err != nil {
			return nil, err
		}
		folderIDs[key] = folder.ID
		return &folder.ID, nil
	}

	for _, feed := range feeds {
		if len(feed.URL) > maxImportURLLength {
			fail(feed.URL, "feed.url_too_long")
			continue
		}
		normalized, err := NormalizeFeedURL(feed.URL)
		if err != nil {
			fail(feed.URL, "feed.url_invalid")
			continue
		}
		if seen[normalized] {
			result.Skipped++
			continue
		}
		seen[normalized] = true
		folderID, err := folderFor(feed.Folder)
		if err != nil {
			return SubscriptionImportResult{}, err
		}
		request, err := s.RequestFeedApproval(ctx, userID, normalized, "", folderID, nil)
		switch {
		case errors.Is(err, ErrFeedLimitReached):
			fail(feed.URL, "feed.limit_reached")
			continue
		case errors.Is(err, ErrFeedRequestLimitReached):
			fail(feed.URL, "feed.request_limit_reached")
			continue
		case err != nil:
			return SubscriptionImportResult{}, err
		}
		if !request.Approved {
			result.Requested++
			continue
		}
		_, err = s.SubscribeSource(ctx, userID, request.SourceID, folderID)
		switch {
		case errors.Is(err, ErrDuplicate):
			result.Skipped++
		case errors.Is(err, ErrFeedLimitReached):
			fail(feed.URL, "feed.limit_reached")
		case err != nil:
			return SubscriptionImportResult{}, err
		default:
			result.Subscribed++
		}
	}
	return result, nil
}

func (s *Store) subscribedNormalizedURLs(ctx context.Context, userID int64) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT fs.normalized_url FROM user_feeds uf JOIN feed_sources fs ON fs.id=uf.feed_source_id WHERE uf.user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			return nil, err
		}
		result[url] = true
	}
	return result, rows.Err()
}

// FolderName is a folder's ID and name, without unread counts.
type FolderName struct {
	ID   int64
	Name string
}

// ListFolderNamesForUser returns the user's folders in folder list order
// (case-insensitive name, then ID). Unlike ListFoldersForUser it does not
// count unread articles.
func (s *Store) ListFolderNamesForUser(ctx context.Context, userID int64) ([]FolderName, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name FROM folders WHERE owner_user_id=$1 ORDER BY LOWER(name), id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]FolderName, 0)
	for rows.Next() {
		var folder FolderName
		if err := rows.Scan(&folder.ID, &folder.Name); err != nil {
			return nil, err
		}
		result = append(result, folder)
	}
	return result, rows.Err()
}

// SubscriptionSummary is a subscription's personal title, feed URL, and
// folder, without unread counts or refresh status.
type SubscriptionSummary struct {
	Title    string
	URL      string
	FolderID *int64
}

// ListSubscriptionsForUser returns the user's subscriptions, newest first
// like ListFeedsForUser, without counting unread articles.
func (s *Store) ListSubscriptionsForUser(ctx context.Context, userID int64) ([]SubscriptionSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(uf.title_override, fs.title), fs.url, uf.folder_id
		FROM user_feeds uf JOIN feed_sources fs ON fs.id = uf.feed_source_id
		WHERE uf.user_id = $1
		ORDER BY uf.created_at DESC, uf.id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]SubscriptionSummary, 0)
	for rows.Next() {
		var subscription SubscriptionSummary
		if err := rows.Scan(&subscription.Title, &subscription.URL, &subscription.FolderID); err != nil {
			return nil, err
		}
		result = append(result, subscription)
	}
	return result, rows.Err()
}
