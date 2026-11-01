-- Retention deletes articles in batches; the cascade to user_article_states and the
-- "saved by anyone" check both look states up by article, which the primary key
-- (user_id, article_id) cannot serve.
CREATE INDEX IF NOT EXISTS user_article_states_article_idx ON user_article_states (article_id);
