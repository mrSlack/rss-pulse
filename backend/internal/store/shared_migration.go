package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type legacyFeedRow struct {
	id            int64
	title         string
	url           string
	siteURL       string
	folderID      *int64
	lastCheckedAt *time.Time
	lastError     *string
}

type legacyArticleRow struct {
	id          int64
	feedID      int64
	guid        string
	title       string
	url         string
	summary     string
	publishedAt time.Time
	createdAt   time.Time
	isRead      bool
	isSaved     bool
}

func MigrateSharedSources(ctx context.Context, pool *pgxpool.Pool, administratorID int64) error {
	if err := applyMigrationPhase(ctx, pool, "shared_sources", "003_shared_sources.sql", func(ctx context.Context, tx pgx.Tx) error {
		return migrateLegacyLibrary(ctx, tx, administratorID)
	}); err != nil {
		return err
	}
	if err := applyMigrationPhase(ctx, pool, "user_profile", "004_user_profile.sql", nil); err != nil {
		return err
	}
	if err := applyMigrationPhase(ctx, pool, "feed_customization", "005_feed_customization.sql", nil); err != nil {
		return err
	}
	return applyMigrationFiles(ctx, pool)
}

func migrateLegacyLibrary(ctx context.Context, tx pgx.Tx, administratorID int64) error {
	rows, err := tx.Query(ctx, `SELECT id, title, url, site_url, folder_id, last_checked_at, last_error FROM legacy_feeds ORDER BY id`)
	if err != nil {
		return err
	}
	legacyFeeds := make([]legacyFeedRow, 0)
	for rows.Next() {
		var feed legacyFeedRow
		if err := rows.Scan(&feed.id, &feed.title, &feed.url, &feed.siteURL, &feed.folderID, &feed.lastCheckedAt, &feed.lastError); err != nil {
			rows.Close()
			return err
		}
		legacyFeeds = append(legacyFeeds, feed)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	sourceByFeed := make(map[int64]int64, len(legacyFeeds))
	sourceByURL := make(map[string]int64, len(legacyFeeds))
	for _, feed := range legacyFeeds {
		normalizedURL, err := NormalizeFeedURL(feed.url)
		if err != nil {
			return fmt.Errorf("normalize legacy feed %d: %w", feed.id, err)
		}
		sourceID, exists := sourceByURL[normalizedURL]
		if !exists {
			var lastSuccess *time.Time
			if feed.lastCheckedAt != nil && feed.lastError == nil {
				lastSuccess = feed.lastCheckedAt
			}
			if err := tx.QueryRow(ctx, `INSERT INTO feed_sources (normalized_url, title, url, site_url, last_checked_at, last_success_at, last_error)
				VALUES ($1, $2, $1, $3, $4, $5, $6) RETURNING id`, normalizedURL, feed.title, feed.siteURL, feed.lastCheckedAt, lastSuccess, feed.lastError).Scan(&sourceID); err != nil {
				return err
			}
			sourceByURL[normalizedURL] = sourceID
		} else {
			var latestChecked, latestSuccess *time.Time
			if err := tx.QueryRow(ctx, `SELECT last_checked_at,last_success_at FROM feed_sources WHERE id = $1`, sourceID).Scan(&latestChecked, &latestSuccess); err != nil {
				return err
			}
			if feed.lastCheckedAt != nil && (latestChecked == nil || feed.lastCheckedAt.After(*latestChecked)) {
				if _, err := tx.Exec(ctx, `UPDATE feed_sources SET title = $2, site_url = $3, last_checked_at = $4, last_error = $5 WHERE id = $1`, sourceID, feed.title, feed.siteURL, feed.lastCheckedAt, feed.lastError); err != nil {
					return err
				}
			}
			if feed.lastCheckedAt != nil && feed.lastError == nil && (latestSuccess == nil || feed.lastCheckedAt.After(*latestSuccess)) {
				if _, err := tx.Exec(ctx, `UPDATE feed_sources SET last_success_at = $2 WHERE id = $1`, sourceID, feed.lastCheckedAt); err != nil {
					return err
				}
			}
		}
		sourceByFeed[feed.id] = sourceID
		if _, err := tx.Exec(ctx, `INSERT INTO user_feeds (user_id, feed_source_id, folder_id) VALUES ($1, $2, $3) ON CONFLICT (user_id, feed_source_id) DO NOTHING`, administratorID, sourceID, feed.folderID); err != nil {
			return err
		}
	}

	articleRows, err := tx.Query(ctx, `SELECT id, feed_id, guid, title, url, summary, published_at, created_at, is_read, is_saved FROM legacy_articles ORDER BY id`)
	if err != nil {
		return err
	}
	legacyArticles := make([]legacyArticleRow, 0)
	for articleRows.Next() {
		var article legacyArticleRow
		if err := articleRows.Scan(&article.id, &article.feedID, &article.guid, &article.title, &article.url, &article.summary, &article.publishedAt, &article.createdAt, &article.isRead, &article.isSaved); err != nil {
			articleRows.Close()
			return err
		}
		legacyArticles = append(legacyArticles, article)
	}
	if err := articleRows.Err(); err != nil {
		articleRows.Close()
		return err
	}
	articleRows.Close()
	for _, article := range legacyArticles {
		sourceID, found := sourceByFeed[article.feedID]
		if !found {
			return fmt.Errorf("legacy article %d has no feed %d", article.id, article.feedID)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO articles (id, feed_source_id, guid, title, url, summary, published_at, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (feed_source_id, guid) DO UPDATE SET
			title = CASE WHEN EXCLUDED.published_at > articles.published_at THEN EXCLUDED.title ELSE articles.title END,
			url = CASE WHEN EXCLUDED.published_at > articles.published_at THEN EXCLUDED.url ELSE articles.url END,
			summary = CASE WHEN EXCLUDED.published_at > articles.published_at THEN EXCLUDED.summary ELSE articles.summary END,
			created_at = LEAST(articles.created_at, EXCLUDED.created_at),
			published_at = GREATEST(articles.published_at, EXCLUDED.published_at)`, article.id, sourceID, article.guid, article.title, article.url, article.summary, article.publishedAt, article.createdAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO user_article_states (user_id, article_id, is_read, is_saved)
			SELECT $1, a.id, $2, $3 FROM articles a WHERE a.feed_source_id = $4 AND a.guid = $5
			ON CONFLICT (user_id, article_id) DO UPDATE SET
			is_read = user_article_states.is_read AND EXCLUDED.is_read,
			is_saved = user_article_states.is_saved OR EXCLUDED.is_saved`, administratorID, article.isRead, article.isSaved, sourceID, article.guid); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence('articles', 'id'), COALESCE(MAX(id), 1), MAX(id) IS NOT NULL) FROM articles`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DROP TABLE legacy_articles`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DROP TABLE legacy_feeds`); err != nil {
		return err
	}
	return nil
}
