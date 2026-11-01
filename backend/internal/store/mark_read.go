package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ArticleFilter selects a user's articles the way GET /api/articles does.
type ArticleFilter struct {
	// Filter is "all", "unread", "today" or "saved"; other values act as "all".
	Filter   string
	FeedID   *int64
	FolderID *int64
	Query    string
	// Since is the start of the "today" filter; nil means midnight UTC.
	Since *time.Time
}

// articleQuery collects the WHERE conditions and positional arguments of a
// query over `articles a JOIN user_feeds uf ... LEFT JOIN user_article_states
// state` for one user, so the article list and bulk updates select exactly
// the same articles.
type articleQuery struct {
	conditions []string
	args       []any
	// searching reports a text search condition; such queries must be planned
	// with their actual pattern (see ListArticlesForUser).
	searching bool
}

func (q *articleQuery) add(value any) string {
	q.args = append(q.args, value)
	return "$" + itoa(len(q.args))
}

// newArticleQuery returns the conditions for filter. The user ID is always $1.
func newArticleQuery(userID int64, filter ArticleFilter) *articleQuery {
	q := &articleQuery{conditions: []string{"uf.user_id = $1"}, args: []any{userID}}
	switch filter.Filter {
	case "unread":
		q.conditions = append(q.conditions, "COALESCE(state.is_read, FALSE) = FALSE")
	case "saved":
		q.conditions = append(q.conditions, "COALESCE(state.is_saved, FALSE) = TRUE")
	case "today":
		if filter.Since != nil {
			q.conditions = append(q.conditions, "a.published_at >= "+q.add(*filter.Since))
		} else {
			q.conditions = append(q.conditions, "a.published_at >= date_trunc('day', NOW())")
		}
	}
	if filter.FeedID != nil {
		q.conditions = append(q.conditions, "uf.id = "+q.add(*filter.FeedID))
	}
	if filter.FolderID != nil {
		q.conditions = append(q.conditions, "uf.folder_id = "+q.add(*filter.FolderID))
	}
	if condition := searchCondition(filter.Query, q.add); condition != "" {
		q.conditions = append(q.conditions, condition)
		q.searching = true
	}
	return q
}

// MarkReadBound limits a bulk mark-read to what the caller has seen. At least
// one field must be set; with both, an article must satisfy both.
type MarkReadBound struct {
	// PublishedAt keeps articles published after it unread.
	PublishedAt *time.Time
	// ArticleID keeps articles ingested after the caller loaded its list (a
	// higher ID) unread, including ones with an old publication date.
	ArticleID *int64
}

// ErrMarkReadUnbounded reports a bulk mark-read without any bound.
var ErrMarkReadUnbounded = errors.New("mark-read needs an upper bound")

// LatestArticleID returns the highest article ID that exists, or 0. Clients
// pass it back as the ArticleID bound of a bulk mark-read.
func (s *Store) LatestArticleID(ctx context.Context) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(id), 0) FROM articles`).Scan(&id)
	return id, err
}

// MarkArticlesReadForUser marks every unread article of the user's
// subscriptions that matches filter and lies within bound as read, in one
// statement, and returns how many articles changed. The bound keeps articles
// that arrived after the caller loaded its list unread.
func (s *Store) MarkArticlesReadForUser(ctx context.Context, userID int64, filter ArticleFilter, bound MarkReadBound) (int64, error) {
	if bound.PublishedAt == nil && bound.ArticleID == nil {
		return 0, ErrMarkReadUnbounded
	}
	q := newArticleQuery(userID, filter)
	if bound.PublishedAt != nil {
		q.conditions = append(q.conditions, "a.published_at <= "+q.add(*bound.PublishedAt))
	}
	if bound.ArticleID != nil {
		q.conditions = append(q.conditions, "a.id <= "+q.add(*bound.ArticleID))
	}
	q.conditions = append(q.conditions, "COALESCE(state.is_read, FALSE) = FALSE")
	args := q.args
	if q.searching {
		args = append([]any{pgx.QueryExecModeCacheDescribe}, args...)
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO user_article_states (user_id, article_id, is_read, is_saved)
		SELECT uf.user_id, a.id, TRUE, FALSE
		FROM articles a JOIN user_feeds uf ON uf.feed_source_id = a.feed_source_id
		LEFT JOIN user_article_states state ON state.user_id = uf.user_id AND state.article_id = a.id
		WHERE `+strings.Join(q.conditions, " AND ")+`
		ON CONFLICT (user_id, article_id) DO UPDATE SET is_read = TRUE, updated_at = NOW()
		WHERE NOT user_article_states.is_read`, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
