-- Unread counters (PERF-2) count a subscription's articles and the user's read states
-- for them. Both indexes allow index-only scans for that: the article index adds id to the
-- per-source publication order (it replaces articles_source_published_idx, whose leading
-- columns it repeats), and the partial state index holds only read states.
CREATE INDEX IF NOT EXISTS articles_source_published_id_idx ON articles (feed_source_id, published_at DESC, id DESC);
DROP INDEX IF EXISTS articles_source_published_idx;
CREATE INDEX IF NOT EXISTS user_article_states_read_idx ON user_article_states (user_id, article_id) WHERE is_read;
