-- Hot queries of the Pulse store, copied from backend/internal/store at commit ea2569f
-- (pre-stage 4) with literal parameters, for EXPLAIN (ANALYZE, BUFFERS) baselines.
-- See docs/2026-11-01-performance-baseline.md for the procedure.
-- Usage: psql -v uid=<user id> -v token_hash=<sha256 hex> -v query=<name> -f this file
-- where <name> is one of the labels below; only the selected query is explained.

\set ON_ERROR_STOP on
SELECT :'query' = 'articles_all' AS q_articles_all,
       :'query' = 'articles_unread' AS q_articles_unread,
       :'query' = 'articles_saved' AS q_articles_saved,
       :'query' = 'articles_search' AS q_articles_search,
       :'query' = 'feeds' AS q_feeds,
       :'query' = 'folders' AS q_folders,
       :'query' = 'due_sources' AS q_due_sources,
       :'query' = 'session_delete' AS q_session_delete,
       :'query' = 'session_select' AS q_session_select \gset

-- ListArticlesForUser(filter=all, limit=20 -> LIMIT 21, offset=0)
\if :q_articles_all
EXPLAIN (ANALYZE, BUFFERS)
SELECT a.id, uf.id, COALESCE(uf.title_override, fs.title), fs.url, a.title, a.url, a.summary, a.published_at,
	COALESCE(state.is_read, FALSE), COALESCE(state.is_saved, FALSE)
	FROM articles a JOIN user_feeds uf ON uf.feed_source_id = a.feed_source_id
	JOIN feed_sources fs ON fs.id = a.feed_source_id
	LEFT JOIN user_article_states state ON state.user_id = uf.user_id AND state.article_id = a.id
	WHERE uf.user_id = :uid
	ORDER BY a.published_at DESC, a.id DESC LIMIT 21 OFFSET 0;
\endif

-- ListArticlesForUser(filter=unread)
\if :q_articles_unread
EXPLAIN (ANALYZE, BUFFERS)
SELECT a.id, uf.id, COALESCE(uf.title_override, fs.title), fs.url, a.title, a.url, a.summary, a.published_at,
	COALESCE(state.is_read, FALSE), COALESCE(state.is_saved, FALSE)
	FROM articles a JOIN user_feeds uf ON uf.feed_source_id = a.feed_source_id
	JOIN feed_sources fs ON fs.id = a.feed_source_id
	LEFT JOIN user_article_states state ON state.user_id = uf.user_id AND state.article_id = a.id
	WHERE uf.user_id = :uid AND COALESCE(state.is_read, FALSE) = FALSE
	ORDER BY a.published_at DESC, a.id DESC LIMIT 21 OFFSET 0;
\endif

-- ListArticlesForUser(filter=saved)
\if :q_articles_saved
EXPLAIN (ANALYZE, BUFFERS)
SELECT a.id, uf.id, COALESCE(uf.title_override, fs.title), fs.url, a.title, a.url, a.summary, a.published_at,
	COALESCE(state.is_read, FALSE), COALESCE(state.is_saved, FALSE)
	FROM articles a JOIN user_feeds uf ON uf.feed_source_id = a.feed_source_id
	JOIN feed_sources fs ON fs.id = a.feed_source_id
	LEFT JOIN user_article_states state ON state.user_id = uf.user_id AND state.article_id = a.id
	WHERE uf.user_id = :uid AND COALESCE(state.is_saved, FALSE) = TRUE
	ORDER BY a.published_at DESC, a.id DESC LIMIT 21 OFFSET 0;
\endif

-- ListArticlesForUser(filter=all, query="market")
\if :q_articles_search
EXPLAIN (ANALYZE, BUFFERS)
SELECT a.id, uf.id, COALESCE(uf.title_override, fs.title), fs.url, a.title, a.url, a.summary, a.published_at,
	COALESCE(state.is_read, FALSE), COALESCE(state.is_saved, FALSE)
	FROM articles a JOIN user_feeds uf ON uf.feed_source_id = a.feed_source_id
	JOIN feed_sources fs ON fs.id = a.feed_source_id
	LEFT JOIN user_article_states state ON state.user_id = uf.user_id AND state.article_id = a.id
	WHERE uf.user_id = :uid AND STRPOS(LOWER(a.title || ' ' || a.summary), LOWER('market')) > 0
	ORDER BY a.published_at DESC, a.id DESC LIMIT 21 OFFSET 0;
\endif

-- ListFeedsForUser
\if :q_feeds
EXPLAIN (ANALYZE, BUFFERS)
SELECT uf.id, COALESCE(uf.title_override, fs.title), fs.url, uf.folder_id, d.name,
	COUNT(a.id) FILTER (WHERE COALESCE(state.is_read, FALSE) = FALSE)::int,
	fs.last_checked_at, fs.last_error
	FROM user_feeds uf
	JOIN feed_sources fs ON fs.id = uf.feed_source_id
	LEFT JOIN folders d ON d.id = uf.folder_id AND d.owner_user_id = uf.user_id
	LEFT JOIN articles a ON a.feed_source_id = fs.id
	LEFT JOIN user_article_states state ON state.user_id = uf.user_id AND state.article_id = a.id
	WHERE uf.user_id = :uid
	GROUP BY uf.id, fs.id, d.name
	ORDER BY uf.created_at DESC, uf.id DESC;
\endif

-- ListFoldersForUser
\if :q_folders
EXPLAIN (ANALYZE, BUFFERS)
SELECT d.id, d.name, d.color, COUNT(a.id) FILTER (WHERE COALESCE(state.is_read, FALSE) = FALSE)::int
	FROM folders d
	LEFT JOIN user_feeds uf ON uf.folder_id = d.id AND uf.user_id = :uid
	LEFT JOIN feed_sources fs ON fs.id = uf.feed_source_id
	LEFT JOIN articles a ON a.feed_source_id = fs.id
	LEFT JOIN user_article_states state ON state.user_id = :uid AND state.article_id = a.id
	WHERE d.owner_user_id = :uid
	GROUP BY d.id
	ORDER BY LOWER(d.name), d.id;
\endif

-- DueSources
\if :q_due_sources
EXPLAIN (ANALYZE, BUFFERS)
SELECT fs.id, fs.url FROM feed_sources fs
	WHERE (fs.last_success_at IS NULL OR fs.last_success_at <= NOW() - ((SELECT value FROM system_settings WHERE key='shared_feed_ttl_minutes') * INTERVAL '1 minute'))
	AND EXISTS (SELECT 1 FROM user_feeds uf WHERE uf.feed_source_id = fs.id)
	ORDER BY fs.last_success_at NULLS FIRST, fs.id;
\endif

-- UserForSession, statement 1 (removed in stage 4). Rolled back so every run sees the same rows.
\if :q_session_delete
BEGIN;
EXPLAIN (ANALYZE, BUFFERS) DELETE FROM sessions WHERE expires_at <= NOW();
ROLLBACK;
\endif

-- UserForSession, statement 2 (the only statement after stage 4)
\if :q_session_select
EXPLAIN (ANALYZE, BUFFERS)
SELECT u.id,u.email,u.role,u.refresh_interval_minutes,u.first_name,u.last_name,u.nickname
	FROM sessions s JOIN users u ON u.id=s.user_id
	WHERE s.token_hash=:'token_hash' AND s.expires_at>NOW();
\endif
