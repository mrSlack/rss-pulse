package store

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pulse-reader/app/internal/feeds"
)

func NormalizeFeedURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", ErrInvalidFeedURL
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		parsed.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		parsed.Host = "[" + host + "]"
	} else {
		parsed.Host = host
	}
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String(), nil
}

// unreadCountsCTE continues a WITH clause whose first CTE, subs(id,
// feed_source_id, folder_id), selects user_feeds rows of the user given by the
// SQL parameter userParam (such as "$1"). It defines
// unread(feed_id, folder_id, n): a subscription's unread count is the number of
// its source's articles minus the number the user has marked read, which equals
// counting articles without a read state. Both counts use index-only scans of
// articles_source_published_id_idx and user_article_states_read_idx instead of
// joining every article with its state.
// The user id is repeated as a parameter (not joined from subs) so the planner
// can use the partial index for that user.
func unreadCountsCTE(userParam string) string {
	return `
		read_counts AS (
			SELECT a.feed_source_id, COUNT(*) AS n
			FROM subs
			JOIN articles a ON a.feed_source_id = subs.feed_source_id
			JOIN user_article_states state ON state.user_id = ` + userParam + ` AND state.article_id = a.id
			WHERE state.is_read
			GROUP BY a.feed_source_id
		),
		unread AS (
			SELECT subs.id AS feed_id, subs.folder_id,
			       (SELECT COUNT(*) FROM articles a WHERE a.feed_source_id = subs.feed_source_id) - COALESCE(read_counts.n, 0) AS n
			FROM subs LEFT JOIN read_counts ON read_counts.feed_source_id = subs.feed_source_id
		)`
}

func (s *Store) ListFoldersForUser(ctx context.Context, userID int64) ([]Folder, error) {
	rows, err := s.pool.Query(ctx, `
		WITH subs AS (
			SELECT uf.id, uf.feed_source_id, uf.folder_id FROM user_feeds uf
			WHERE uf.user_id = $1 AND uf.folder_id IS NOT NULL
		),`+unreadCountsCTE("$1")+`
		SELECT d.id, d.name, d.color, COALESCE(folder_unread.n, 0)::int
		FROM folders d
		LEFT JOIN (SELECT folder_id, SUM(n) AS n FROM unread GROUP BY folder_id) folder_unread ON folder_unread.folder_id = d.id
		WHERE d.owner_user_id = $1
		ORDER BY LOWER(d.name), d.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Folder, 0)
	for rows.Next() {
		var folder Folder
		if err := rows.Scan(&folder.ID, &folder.Name, &folder.Color, &folder.UnreadCount); err != nil {
			return nil, err
		}
		result = append(result, folder)
	}
	return result, rows.Err()
}

func (s *Store) CreateFolderForUser(ctx context.Context, userID int64, name, color string) (Folder, error) {
	var folder Folder
	err := s.pool.QueryRow(ctx, `INSERT INTO folders (owner_user_id, name, color) VALUES ($1, $2, $3) ON CONFLICT (owner_user_id, LOWER(name)) DO NOTHING RETURNING id, name, color`, userID, name, color).Scan(&folder.ID, &folder.Name, &folder.Color)
	if errors.Is(err, pgx.ErrNoRows) {
		return Folder{}, ErrDuplicate
	}
	return folder, err
}

func (s *Store) UpdateFolderForUser(ctx context.Context, userID, folderID int64, name, color string) (Folder, error) {
	var folder Folder
	err := s.pool.QueryRow(ctx, `
		WITH updated_folder AS (
			UPDATE folders SET name=$3, color=$4
			WHERE id=$1 AND owner_user_id=$2
			RETURNING id, name, color
		),
		subs AS (
			SELECT uf.id, uf.feed_source_id, uf.folder_id FROM user_feeds uf
			WHERE uf.user_id = $2 AND uf.folder_id = $1
		),`+unreadCountsCTE("$2")+`
		SELECT updated_folder.id, updated_folder.name, updated_folder.color,
		       COALESCE((SELECT SUM(unread.n) FROM unread), 0)::int
		FROM updated_folder`, folderID, userID, name, color).Scan(&folder.ID, &folder.Name, &folder.Color, &folder.UnreadCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Folder{}, ErrNotFound
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		return Folder{}, ErrDuplicate
	}
	return folder, err
}

func (s *Store) DeleteFolderForUser(ctx context.Context, userID, folderID int64, action string, targetFolderID *int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM folders WHERE id=$1 AND owner_user_id=$2 FOR UPDATE)`, folderID, userID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}

	switch action {
	case "delete_sources":
		if targetFolderID != nil {
			return ErrInvalidFolderOperation
		}
		if _, err := tx.Exec(ctx, `DELETE FROM user_article_states state USING articles a, user_feeds uf WHERE state.user_id=$1 AND state.article_id=a.id AND a.feed_source_id=uf.feed_source_id AND uf.user_id=$1 AND uf.folder_id=$2`, userID, folderID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM user_feeds WHERE user_id=$1 AND folder_id=$2`, userID, folderID); err != nil {
			return err
		}
	case "move_sources":
		if targetFolderID == nil || *targetFolderID == folderID {
			return ErrInvalidFolderOperation
		}
		var targetExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM folders WHERE id=$1 AND owner_user_id=$2 FOR UPDATE)`, *targetFolderID, userID).Scan(&targetExists); err != nil {
			return err
		}
		if !targetExists {
			return ErrFolderUnavailable
		}
		if _, err := tx.Exec(ctx, `UPDATE user_feeds SET folder_id=$3 WHERE user_id=$1 AND folder_id=$2`, userID, folderID, *targetFolderID); err != nil {
			return err
		}
	default:
		return ErrInvalidFolderOperation
	}

	tag, err := tx.Exec(ctx, `DELETE FROM folders WHERE id=$1 AND owner_user_id=$2`, folderID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

func (s *Store) ListFeedsForUser(ctx context.Context, userID int64) ([]Feed, error) {
	rows, err := s.pool.Query(ctx, `
		WITH subs AS (
			SELECT uf.id, uf.feed_source_id, uf.folder_id FROM user_feeds uf WHERE uf.user_id = $1
		),`+unreadCountsCTE("$1")+`
		SELECT uf.id, COALESCE(uf.title_override, fs.title), fs.url, uf.folder_id, d.name,
		       unread.n::int, fs.last_checked_at, fs.last_error
		FROM user_feeds uf
		JOIN unread ON unread.feed_id = uf.id
		JOIN feed_sources fs ON fs.id = uf.feed_source_id
		LEFT JOIN folders d ON d.id = uf.folder_id AND d.owner_user_id = uf.user_id
		WHERE uf.user_id = $1
		ORDER BY uf.created_at DESC, uf.id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Feed, 0)
	for rows.Next() {
		var feed Feed
		if err := rows.Scan(&feed.ID, &feed.Title, &feed.URL, &feed.FolderID, &feed.FolderName, &feed.UnreadCount, &feed.LastCheckedAt, &feed.LastError); err != nil {
			return nil, err
		}
		result = append(result, feed)
	}
	return result, rows.Err()
}

func (s *Store) PrepareFeedUpdate(ctx context.Context, userID, feedID int64, rawURL string, folderID *int64) (int64, bool, bool, error) {
	normalizedURL, err := NormalizeFeedURL(rawURL)
	if err != nil {
		return 0, false, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, false, false, err
	}
	defer tx.Rollback(ctx)
	if folderID != nil {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM folders WHERE id=$1 AND owner_user_id=$2)`, *folderID, userID).Scan(&exists); err != nil {
			return 0, false, false, err
		}
		if !exists {
			return 0, false, false, ErrFolderUnavailable
		}
	}
	var currentSourceID int64
	var currentURL string
	err = tx.QueryRow(ctx, `SELECT fs.id, fs.normalized_url
		FROM user_feeds uf JOIN feed_sources fs ON fs.id=uf.feed_source_id
		WHERE uf.id=$1 AND uf.user_id=$2 FOR UPDATE OF uf`, feedID, userID).Scan(&currentSourceID, &currentURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, false, ErrNotFound
	}
	if err != nil {
		return 0, false, false, err
	}
	if currentURL == normalizedURL {
		return currentSourceID, false, false, nil
	}
	var alreadySubscribed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM user_feeds uf JOIN feed_sources fs ON fs.id=uf.feed_source_id
		WHERE uf.user_id=$1 AND uf.id<>$2 AND fs.normalized_url=$3)`, userID, feedID, normalizedURL).Scan(&alreadySubscribed); err != nil {
		return 0, false, false, err
	}
	if alreadySubscribed {
		return 0, false, false, ErrDuplicate
	}
	parsed, _ := url.Parse(normalizedURL)
	var sourceID int64
	err = tx.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url,title,url,added_by_user_id) VALUES ($1,$2,$1,$3)
		ON CONFLICT (normalized_url) DO NOTHING RETURNING id`, normalizedURL, parsed.Hostname(), userID).Scan(&sourceID)
	created := err == nil
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id FROM feed_sources WHERE normalized_url=$1`, normalizedURL).Scan(&sourceID)
	}
	if err != nil {
		return 0, false, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, false, false, err
	}
	return sourceID, created, true, nil
}

func (s *Store) UpdateFeedForUser(ctx context.Context, userID, feedID, sourceID int64, title string, folderID *int64) (Feed, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Feed{}, err
	}
	defer tx.Rollback(ctx)
	if folderID != nil {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM folders WHERE id=$1 AND owner_user_id=$2)`, *folderID, userID).Scan(&exists); err != nil {
			return Feed{}, err
		}
		if !exists {
			return Feed{}, ErrFolderUnavailable
		}
	}
	var updatedID int64
	err = tx.QueryRow(ctx, `UPDATE user_feeds uf SET feed_source_id=$3, folder_id=$4,
		title_override=NULLIF($5, (SELECT title FROM feed_sources WHERE id=$3))
		WHERE uf.id=$1 AND uf.user_id=$2 AND EXISTS(SELECT 1 FROM feed_sources WHERE id=$3)
		RETURNING uf.id`, feedID, userID, sourceID, folderID, title).Scan(&updatedID)
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		return Feed{}, ErrDuplicate
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Feed{}, ErrNotFound
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
		if feed.ID == updatedID {
			return feed, nil
		}
	}
	return Feed{}, ErrNotFound
}

func (s *Store) SubscribeFeed(ctx context.Context, userID int64, rawURL string, document feeds.Document, folderID *int64) (Feed, error) {
	normalizedURL, err := NormalizeFeedURL(rawURL)
	if err != nil {
		return Feed{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Feed{}, err
	}
	defer tx.Rollback(ctx)
	if folderID != nil {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM folders WHERE id = $1 AND owner_user_id = $2)`, *folderID, userID).Scan(&exists); err != nil {
			return Feed{}, err
		}
		if !exists {
			return Feed{}, ErrFolderUnavailable
		}
	}
	var sourceID int64
	err = tx.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url, title, url, site_url, last_checked_at, last_success_at, added_by_user_id)
		VALUES ($1, $2, $1, $3, NOW(), NOW(), $4) ON CONFLICT (normalized_url) DO NOTHING RETURNING id`, normalizedURL, document.Title, document.SiteURL, userID).Scan(&sourceID)
	newSource := err == nil
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id FROM feed_sources WHERE normalized_url = $1`, normalizedURL).Scan(&sourceID)
	}
	if err != nil {
		return Feed{}, err
	}
	if newSource {
		allowedItems, err := filterAndLogBlockedItems(ctx, tx, sourceID, document.Title, document.Items)
		if err != nil {
			return Feed{}, err
		}
		if err := upsertSourceItems(ctx, tx, sourceID, allowedItems); err != nil {
			return Feed{}, err
		}
	}
	var feed Feed
	err = tx.QueryRow(ctx, `INSERT INTO user_feeds (user_id, feed_source_id, folder_id) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, feed_source_id) DO NOTHING
		RETURNING id, folder_id`, userID, sourceID, folderID).
		Scan(&feed.ID, &feed.FolderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Feed{}, ErrDuplicate
	}
	if err != nil {
		return Feed{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT title, url FROM feed_sources WHERE id = $1`, sourceID).Scan(&feed.Title, &feed.URL); err != nil {
		return Feed{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Feed{}, err
	}
	feedsForUser, err := s.ListFeedsForUser(ctx, userID)
	if err != nil {
		return Feed{}, err
	}
	for _, subscribed := range feedsForUser {
		if subscribed.ID == feed.ID {
			return subscribed, nil
		}
	}
	return Feed{}, ErrNotFound
}

// ListArticlesForUser returns up to limit+1 articles in (published_at DESC,
// id DESC) order so callers can detect another page. With a non-nil before
// cursor (keyset paging) only articles strictly after that position are
// returned and offset is ignored; offset paging is kept for older clients.
func (s *Store) ListArticlesForUser(ctx context.Context, userID int64, filter string, feedID, folderID *int64, query string, since *time.Time, limit, offset int, before *ArticleCursor) ([]Article, error) {
	conditions := []string{"uf.user_id = $1"}
	args := []any{userID}
	add := func(value any) string {
		args = append(args, value)
		return "$" + itoa(len(args))
	}
	switch filter {
	case "unread":
		conditions = append(conditions, "COALESCE(state.is_read, FALSE) = FALSE")
	case "saved":
		conditions = append(conditions, "COALESCE(state.is_saved, FALSE) = TRUE")
	case "today":
		if since != nil {
			conditions = append(conditions, "a.published_at >= "+add(*since))
		} else {
			conditions = append(conditions, "a.published_at >= date_trunc('day', NOW())")
		}
	}
	if feedID != nil {
		conditions = append(conditions, "uf.id = "+add(*feedID))
	}
	if folderID != nil {
		conditions = append(conditions, "uf.folder_id = "+add(*folderID))
	}
	if pattern := searchPattern(query); pattern != "" {
		// Served by the trigram index articles_search_text_trgm_idx.
		conditions = append(conditions, "a.search_text LIKE "+add(pattern)+` ESCAPE '\'`)
	}
	if before != nil {
		conditions = append(conditions, "(a.published_at, a.id) < ("+add(before.PublishedAt)+"::timestamptz, "+add(before.ID)+"::bigint)")
		offset = 0
	}
	if limit < 1 || limit > 200 {
		limit = 20
	}
	if offset < 0 || offset > 100000 {
		offset = 0
	}
	limitArg := add(limit + 1)
	offsetArg := add(offset)
	rows, err := s.pool.Query(ctx, `SELECT a.id, uf.id, COALESCE(uf.title_override, fs.title), fs.url, a.title, a.url, a.summary, a.published_at,
		COALESCE(state.is_read, FALSE), COALESCE(state.is_saved, FALSE)
		FROM articles a JOIN user_feeds uf ON uf.feed_source_id = a.feed_source_id
		JOIN feed_sources fs ON fs.id = a.feed_source_id
		LEFT JOIN user_article_states state ON state.user_id = uf.user_id AND state.article_id = a.id
		WHERE `+strings.Join(conditions, " AND ")+`
		ORDER BY a.published_at DESC, a.id DESC LIMIT `+limitArg+` OFFSET `+offsetArg, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Article, 0)
	for rows.Next() {
		var article Article
		if err := rows.Scan(&article.ID, &article.FeedID, &article.FeedTitle, &article.FeedURL, &article.Title, &article.URL, &article.Summary, &article.PublishedAt, &article.IsRead, &article.IsSaved); err != nil {
			return nil, err
		}
		result = append(result, article)
	}
	return result, rows.Err()
}

func (s *Store) UpdateArticleForUser(ctx context.Context, userID, articleID int64, isRead, isSaved *bool) (Article, error) {
	var subscribed bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM articles a JOIN user_feeds uf ON uf.feed_source_id = a.feed_source_id WHERE a.id = $1 AND uf.user_id = $2)`, articleID, userID).Scan(&subscribed); err != nil {
		return Article{}, err
	}
	if !subscribed {
		return Article{}, ErrNotFound
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO user_article_states (user_id, article_id, is_read, is_saved)
		VALUES ($1, $2, COALESCE($3, FALSE), COALESCE($4, FALSE))
		ON CONFLICT (user_id, article_id) DO UPDATE SET
		is_read = COALESCE($3, user_article_states.is_read), is_saved = COALESCE($4, user_article_states.is_saved), updated_at = NOW()`, userID, articleID, isRead, isSaved); err != nil {
		return Article{}, err
	}
	return s.GetArticleForUser(ctx, userID, articleID)
}

func (s *Store) GetArticleForUser(ctx context.Context, userID, articleID int64) (Article, error) {
	var article Article
	err := s.pool.QueryRow(ctx, `SELECT a.id, uf.id, fs.title, fs.url, a.title, a.url, a.summary, a.published_at,
		COALESCE(state.is_read, FALSE), COALESCE(state.is_saved, FALSE)
		FROM articles a JOIN user_feeds uf ON uf.feed_source_id = a.feed_source_id AND uf.user_id = $2
		JOIN feed_sources fs ON fs.id = a.feed_source_id
		LEFT JOIN user_article_states state ON state.user_id = $2 AND state.article_id = a.id
		WHERE a.id = $1 LIMIT 1`, articleID, userID).
		Scan(&article.ID, &article.FeedID, &article.FeedTitle, &article.FeedURL, &article.Title, &article.URL, &article.Summary, &article.PublishedAt, &article.IsRead, &article.IsSaved)
	if errors.Is(err, pgx.ErrNoRows) {
		return Article{}, ErrNotFound
	}
	return article, err
}
