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

func (s *Store) ListFoldersForUser(ctx context.Context, userID int64) ([]Folder, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.id, d.name, d.color, COUNT(a.id) FILTER (WHERE COALESCE(state.is_read, FALSE) = FALSE)::int
		FROM folders d
		LEFT JOIN user_feeds uf ON uf.folder_id = d.id AND uf.user_id = $1
		LEFT JOIN feed_sources fs ON fs.id = uf.feed_source_id
		LEFT JOIN articles a ON a.feed_source_id = fs.id
		LEFT JOIN user_article_states state ON state.user_id = $1 AND state.article_id = a.id
		WHERE d.owner_user_id = $1
		GROUP BY d.id
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
		)
		SELECT updated_folder.id, updated_folder.name, updated_folder.color,
		       COUNT(a.id) FILTER (WHERE COALESCE(state.is_read, FALSE) = FALSE)::int
		FROM updated_folder
		LEFT JOIN user_feeds uf ON uf.folder_id = updated_folder.id AND uf.user_id = $2
		LEFT JOIN feed_sources fs ON fs.id = uf.feed_source_id
		LEFT JOIN articles a ON a.feed_source_id = fs.id
		LEFT JOIN user_article_states state ON state.user_id = $2 AND state.article_id = a.id
		GROUP BY updated_folder.id, updated_folder.name, updated_folder.color`, folderID, userID, name, color).Scan(&folder.ID, &folder.Name, &folder.Color, &folder.UnreadCount)
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
		SELECT uf.id, COALESCE(uf.title_override, fs.title), fs.url, uf.folder_id, d.name,
		       COUNT(a.id) FILTER (WHERE COALESCE(state.is_read, FALSE) = FALSE)::int,
		       fs.last_checked_at, fs.last_error
		FROM user_feeds uf
		JOIN feed_sources fs ON fs.id = uf.feed_source_id
		LEFT JOIN folders d ON d.id = uf.folder_id AND d.owner_user_id = uf.user_id
		LEFT JOIN articles a ON a.feed_source_id = fs.id
		LEFT JOIN user_article_states state ON state.user_id = uf.user_id AND state.article_id = a.id
		WHERE uf.user_id = $1
		GROUP BY uf.id, fs.id, d.name
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

func (s *Store) ListArticlesForUser(ctx context.Context, userID int64, filter string, feedID, folderID *int64, query string, since *time.Time, limit, offset int) ([]Article, error) {
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
	if query != "" {
		conditions = append(conditions, "STRPOS(LOWER(a.title || ' ' || a.summary), LOWER("+add(query)+")) > 0")
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

func upsertSourceItems(ctx context.Context, tx pgx.Tx, sourceID int64, items []feeds.Item) error {
	for _, item := range items {
		var existingID int64
		err := tx.QueryRow(ctx, `UPDATE articles SET title=$3,url=$4,summary=$5,
			published_at=CASE WHEN $7 THEN $6 ELSE published_at END
			WHERE feed_source_id=$1 AND guid=$2 RETURNING id`,
			sourceID, item.GUID, item.Title, item.URL, item.Summary, item.PublishedAt, item.HasPublishedAt).Scan(&existingID)
		if err == nil {
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		err = tx.QueryRow(ctx, `SELECT id FROM articles
			WHERE feed_source_id=$1
			AND published_at BETWEEN $6::timestamptz - INTERVAL '30 minutes' AND $6::timestamptz + INTERVAL '30 minutes'
			AND (($2 <> '' AND guid=$2) OR ($3 <> '' AND title=$3) OR ($4 <> '' AND url=$4) OR ($5 <> '' AND summary=$5))
			ORDER BY CASE WHEN guid=$2 AND $2 <> '' THEN 0 WHEN url=$4 AND $4 <> '' THEN 1
				WHEN title=$3 AND $3 <> '' THEN 2 ELSE 3 END,
				ABS(EXTRACT(EPOCH FROM (published_at - $6)))
			LIMIT 1 FOR UPDATE`, sourceID, item.GUID, item.Title, item.URL, item.Summary, item.PublishedAt).Scan(&existingID)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE articles SET guid=$2,title=$3,url=$4,summary=$5,
				published_at=CASE WHEN $7 THEN $6 ELSE published_at END WHERE id=$1`,
				existingID, item.GUID, item.Title, item.URL, item.Summary, item.PublishedAt, item.HasPublishedAt)
			if err != nil {
				return err
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		if _, err := tx.Exec(ctx, `INSERT INTO articles (feed_source_id, guid, title, url, summary, published_at)
			VALUES ($1, $2, $3, $4, $5, $6)`, sourceID, item.GUID, item.Title, item.URL, item.Summary, item.PublishedAt); err != nil {
			return err
		}
	}
	return nil
}
