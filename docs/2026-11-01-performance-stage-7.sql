-- Queries changed by stage 7 (search, unread counters), copied from backend/internal/store
-- with literal parameters, for EXPLAIN (ANALYZE, BUFFERS). The unchanged queries are in
-- 2026-11-01-performance-baseline.sql. See docs/2026-11-01-performance-baseline.md,
-- section "After stage 7".
-- Usage: psql -v uid=<user id> -v pattern=<LIKE pattern, e.g. %market%> -v query=<name> -f this file

\set ON_ERROR_STOP on
SELECT :'query' = 'articles_search' AS q_articles_search,
       :'query' = 'feeds' AS q_feeds,
       :'query' = 'folders' AS q_folders \gset

-- ListArticlesForUser(filter=all, query -> search_text LIKE pattern, limit=20 -> LIMIT 21, offset=0)
\if :q_articles_search
EXPLAIN (ANALYZE, BUFFERS)
SELECT a.id, uf.id, COALESCE(uf.title_override, fs.title), fs.url, a.title, a.url, a.summary, a.published_at,
	COALESCE(state.is_read, FALSE), COALESCE(state.is_saved, FALSE)
	FROM articles a JOIN user_feeds uf ON uf.feed_source_id = a.feed_source_id
	JOIN feed_sources fs ON fs.id = a.feed_source_id
	LEFT JOIN user_article_states state ON state.user_id = uf.user_id AND state.article_id = a.id
	WHERE uf.user_id = :uid AND a.search_text LIKE :'pattern' ESCAPE '\'
	ORDER BY a.published_at DESC, a.id DESC LIMIT 21 OFFSET 0;
\endif

-- ListFeedsForUser
\if :q_feeds
EXPLAIN (ANALYZE, BUFFERS)
WITH subs AS NOT MATERIALIZED (
	SELECT uf.id, uf.feed_source_id, uf.folder_id FROM user_feeds uf WHERE uf.user_id = :uid
),
read_counts AS (
	SELECT a.feed_source_id, COUNT(*) AS n
	FROM subs
	JOIN articles a ON a.feed_source_id = subs.feed_source_id
	JOIN user_article_states state ON state.user_id = :uid AND state.article_id = a.id
	WHERE state.is_read
	GROUP BY a.feed_source_id
),
unread AS (
	SELECT subs.id AS feed_id, subs.folder_id,
	       (SELECT COUNT(*) FROM articles a WHERE a.feed_source_id = subs.feed_source_id) - COALESCE(read_counts.n, 0) AS n
	FROM subs LEFT JOIN read_counts ON read_counts.feed_source_id = subs.feed_source_id
)
SELECT uf.id, COALESCE(uf.title_override, fs.title), fs.url, uf.folder_id, d.name,
       unread.n::int, fs.last_checked_at, fs.last_error
FROM user_feeds uf
JOIN unread ON unread.feed_id = uf.id
JOIN feed_sources fs ON fs.id = uf.feed_source_id
LEFT JOIN folders d ON d.id = uf.folder_id AND d.owner_user_id = uf.user_id
WHERE uf.user_id = :uid
ORDER BY uf.created_at DESC, uf.id DESC;
\endif

-- ListFoldersForUser
\if :q_folders
EXPLAIN (ANALYZE, BUFFERS)
WITH subs AS NOT MATERIALIZED (
	SELECT uf.id, uf.feed_source_id, uf.folder_id FROM user_feeds uf
	WHERE uf.user_id = :uid AND uf.folder_id IS NOT NULL
),
read_counts AS (
	SELECT a.feed_source_id, COUNT(*) AS n
	FROM subs
	JOIN articles a ON a.feed_source_id = subs.feed_source_id
	JOIN user_article_states state ON state.user_id = :uid AND state.article_id = a.id
	WHERE state.is_read
	GROUP BY a.feed_source_id
),
unread AS (
	SELECT subs.id AS feed_id, subs.folder_id,
	       (SELECT COUNT(*) FROM articles a WHERE a.feed_source_id = subs.feed_source_id) - COALESCE(read_counts.n, 0) AS n
	FROM subs LEFT JOIN read_counts ON read_counts.feed_source_id = subs.feed_source_id
)
SELECT d.id, d.name, d.color, COALESCE(folder_unread.n, 0)::int
FROM folders d
LEFT JOIN (SELECT folder_id, SUM(n) AS n FROM unread GROUP BY folder_id) folder_unread ON folder_unread.folder_id = d.id
WHERE d.owner_user_id = :uid
ORDER BY LOWER(d.name), d.id;
\endif
