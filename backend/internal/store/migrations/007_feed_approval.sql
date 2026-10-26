CREATE TABLE feed_requests (
    id BIGSERIAL PRIMARY KEY,
    normalized_url TEXT NOT NULL UNIQUE,
    url TEXT NOT NULL,
    requested_by BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    folder_id BIGINT REFERENCES folders(id) ON DELETE SET NULL,
    title TEXT NOT NULL DEFAULT '',
    replace_feed_id BIGINT REFERENCES user_feeds(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX feed_requests_created_idx ON feed_requests (created_at, id);
