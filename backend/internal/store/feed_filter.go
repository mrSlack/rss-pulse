package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pulse-reader/app/internal/feeds"
)

type StopWord struct {
	ID        int64  `json:"id"`
	Term      string `json:"term"`
	CreatedAt string `json:"createdAt"`
}

type BlockedFeedArticle struct {
	ArticleGUID  string     `json:"articleGuid"`
	Summary      string     `json:"summary"`
	PublishedAt  *time.Time `json:"publishedAt"`
	Status       string     `json:"status"`
	DecidedAt    *time.Time `json:"decidedAt"`
	CanModerate  bool       `json:"canModerate"`
	ID           int64      `json:"id"`
	ArticleTitle string     `json:"articleTitle"`
	ArticleURL   string     `json:"articleUrl"`
	FeedTitle    string     `json:"feedTitle"`
	FeedURL      string     `json:"feedUrl"`
	AddedBy      string     `json:"addedBy"`
	MatchedTerm  string     `json:"matchedTerm"`
	FirstBlocked string     `json:"firstBlockedAt"`
	LastBlocked  string     `json:"lastBlockedAt"`
}

func (s *Store) ListStopWords(ctx context.Context) ([]StopWord, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,term,created_at::text FROM feed_stop_words ORDER BY term,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]StopWord, 0)
	for rows.Next() {
		var item StopWord
		if err := rows.Scan(&item.ID, &item.Term, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) AddStopWord(ctx context.Context, rawTerm string) (StopWord, error) {
	term, err := normalizeStopWordInput(rawTerm)
	if err != nil {
		return StopWord{}, err
	}
	var item StopWord
	err = s.pool.QueryRow(ctx, `INSERT INTO feed_stop_words(term) VALUES ($1) RETURNING id,term,created_at::text`, term).
		Scan(&item.ID, &item.Term, &item.CreatedAt)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			return StopWord{}, ErrDuplicate
		}
	}
	return item, err
}

func (s *Store) DeleteStopWord(ctx context.Context, id int64) error {
	command, err := s.pool.Exec(ctx, `DELETE FROM feed_stop_words WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListBlockedFeedArticles(ctx context.Context, limit int) ([]BlockedFeedArticle, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT ba.id,ba.article_title,ba.article_url,ba.feed_title,ba.feed_url,
		COALESCE(u.email,''),ba.matched_term,ba.first_blocked_at::text,ba.last_blocked_at::text,ba.article_guid,COALESCE(ba.summary,''),ba.published_at,ba.status,ba.decided_at,
 (ba.moderation_eligible AND ba.status IN ('pending','rejected') AND ba.summary IS NOT NULL AND ba.published_at IS NOT NULL AND ba.article_guid<>'')
		FROM blocked_feed_articles ba LEFT JOIN users u ON u.id=ba.added_by_user_id
		ORDER BY (ba.status='pending') DESC,ba.last_blocked_at DESC,ba.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]BlockedFeedArticle, 0)
	for rows.Next() {
		var item BlockedFeedArticle
		if err := rows.Scan(&item.ID, &item.ArticleTitle, &item.ArticleURL, &item.FeedTitle, &item.FeedURL,
			&item.AddedBy, &item.MatchedTerm, &item.FirstBlocked, &item.LastBlocked,
			&item.ArticleGUID, &item.Summary, &item.PublishedAt, &item.Status, &item.DecidedAt, &item.CanModerate); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func filterAndLogBlockedItems(ctx context.Context, tx pgx.Tx, sourceID int64, currentTitle string, items []feeds.Item) ([]feeds.Item, error) {
	rows, err := tx.Query(ctx, `SELECT term FROM feed_stop_words ORDER BY term,id`)
	if err != nil {
		return nil, err
	}
	terms := make([]string, 0)
	for rows.Next() {
		var term string
		if err := rows.Scan(&term); err != nil {
			rows.Close()
			return nil, err
		}
		terms = append(terms, term)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	var feedTitle, feedURL string
	var addedByUserID *int64
	if err := tx.QueryRow(ctx, `SELECT title,url,added_by_user_id FROM feed_sources WHERE id=$1`, sourceID).
		Scan(&feedTitle, &feedURL, &addedByUserID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(currentTitle) != "" {
		feedTitle = strings.TrimSpace(currentTitle)
	}
	allowed := make([]feeds.Item, 0, len(items))
	for _, item := range items {
		var status string
		err := tx.QueryRow(ctx, `SELECT status FROM blocked_feed_articles WHERE feed_source_id=$1 AND article_guid=$2`, sourceID, item.GUID).Scan(&status)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		matchedTerm := feeds.FindStopWordMatch(item.Title+" "+item.Summary, terms)
		if matchedTerm == "" && status == "" {
			allowed = append(allowed, item)
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO blocked_feed_articles
			(feed_source_id,article_guid,article_title,article_url,feed_title,feed_url,added_by_user_id,matched_term,summary,published_at,moderation_eligible)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,TRUE)
			ON CONFLICT (feed_source_id,article_guid) DO UPDATE SET article_title=EXCLUDED.article_title,
				article_url=EXCLUDED.article_url,feed_title=EXCLUDED.feed_title,feed_url=EXCLUDED.feed_url,
				added_by_user_id=COALESCE(blocked_feed_articles.added_by_user_id,EXCLUDED.added_by_user_id),
				matched_term=CASE WHEN EXCLUDED.matched_term<>'' THEN EXCLUDED.matched_term ELSE blocked_feed_articles.matched_term END,summary=EXCLUDED.summary,published_at=CASE WHEN $11 THEN EXCLUDED.published_at ELSE blocked_feed_articles.published_at END,last_blocked_at=NOW()`,
			sourceID, item.GUID, item.Title, item.URL, feedTitle, feedURL, addedByUserID, matchedTerm, item.Summary, item.PublishedAt, item.HasPublishedAt); err != nil {
			return nil, err
		}
		if status == "allowed" {
			allowed = append(allowed, item)
		}
	}
	return allowed, nil
}

func normalizeStopWordInput(raw string) (string, error) {
	term, err := feeds.NormalizeStopWord(raw)
	if errors.Is(err, feeds.ErrInvalidStopWord) {
		return "", ErrInvalidStopWord
	}
	return term, err
}

// ModerateBlockedFeedArticle records a decision and promotion atomically. Source locking
// serializes decisions with refreshes, using the same lock order as refresh commits.
func (s *Store) ModerateBlockedFeedArticle(ctx context.Context, id int64, allow bool, moderatorID int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var sourceID int64
	err = tx.QueryRow(ctx, `SELECT feed_source_id FROM blocked_feed_articles WHERE id=$1`, id).Scan(&sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM feed_sources WHERE id=$1 FOR UPDATE`, sourceID); err != nil {
		return err
	}
	var item feeds.Item
	var summary *string
	var published *time.Time
	var status string
	var eligible bool
	err = tx.QueryRow(ctx, `SELECT article_guid,article_title,article_url,summary,published_at,status,moderation_eligible FROM blocked_feed_articles WHERE id=$1 FOR UPDATE`, id).Scan(&item.GUID, &item.Title, &item.URL, &summary, &published, &status, &eligible)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status == "allowed" {
		if allow {
			return tx.Commit(ctx)
		}
		return ErrQuarantineUnavailable
	}
	if !eligible || summary == nil || published == nil || item.GUID == "" {
		return ErrQuarantineUnavailable
	}
	status = "rejected"
	if allow {
		item.Summary = *summary
		item.PublishedAt = *published
		item.HasPublishedAt = true
		if err := upsertSourceItems(ctx, tx, sourceID, []feeds.Item{item}); err != nil {
			return err
		}
		status = "allowed"
	}
	if _, err = tx.Exec(ctx, `UPDATE blocked_feed_articles SET status=$2,decided_at=NOW(),decided_by_user_id=$3 WHERE id=$1`, id, status, moderatorID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
