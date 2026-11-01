-- Article search with the search key of migration 018 (words joined by ǂ), copied from
-- backend/internal/store (searchCondition) with literal parameters, for
-- EXPLAIN (ANALYZE, BUFFERS). See docs/2026-11-01-performance-baseline.md, section
-- "Search key and non-blocking search upgrade".
-- Usage: psql -v uid=<user id> -v pattern=<LIKE pattern of the query key, e.g. %energyǂclimate%> -f this file

\set ON_ERROR_STOP on
EXPLAIN (ANALYZE, BUFFERS)
SELECT a.id, uf.id, COALESCE(uf.title_override, fs.title), fs.url, a.title, a.url, a.summary, a.published_at,
	COALESCE(state.is_read, FALSE), COALESCE(state.is_saved, FALSE)
	FROM articles a JOIN user_feeds uf ON uf.feed_source_id = a.feed_source_id
	JOIN feed_sources fs ON fs.id = a.feed_source_id
	LEFT JOIN user_article_states state ON state.user_id = uf.user_id AND state.article_id = a.id
	WHERE uf.user_id = :uid
	AND COALESCE(a.search_key, pulse_article_search_key(a.title, a.summary)) LIKE :'pattern' ESCAPE '\'
	ORDER BY a.published_at DESC, a.id DESC LIMIT 21 OFFSET 0;
