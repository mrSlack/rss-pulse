ALTER TABLE blocked_feed_articles
    ADD COLUMN summary TEXT,
    ADD COLUMN published_at TIMESTAMPTZ,
    ADD COLUMN status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','rejected','allowed')),
    ADD COLUMN decided_at TIMESTAMPTZ,
    ADD COLUMN decided_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX blocked_feed_articles_moderation_idx
    ON blocked_feed_articles ((status = 'pending') DESC, last_blocked_at DESC, id DESC);
