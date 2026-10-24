-- pulse:phase core
CREATE TABLE IF NOT EXISTS folders (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS folders_name_lower_unique ON folders (LOWER(name));

INSERT INTO folders (name) VALUES ('Technology'), ('Science'), ('Design')
ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS feeds (
    id BIGSERIAL PRIMARY KEY,
    title TEXT NOT NULL,
    url TEXT NOT NULL UNIQUE,
    site_url TEXT NOT NULL DEFAULT '',
    folder_id BIGINT REFERENCES folders(id) ON DELETE SET NULL,
    last_checked_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS articles (
    id BIGSERIAL PRIMARY KEY,
    feed_id BIGINT NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
    guid TEXT NOT NULL,
    title TEXT NOT NULL,
    url TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    published_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    is_read BOOLEAN NOT NULL DEFAULT FALSE,
    is_saved BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (feed_id, guid)
);

CREATE INDEX IF NOT EXISTS articles_feed_published_idx ON articles (feed_id, published_at DESC);
CREATE INDEX IF NOT EXISTS articles_unread_published_idx ON articles (published_at DESC) WHERE is_read = FALSE;
CREATE INDEX IF NOT EXISTS articles_saved_published_idx ON articles (published_at DESC) WHERE is_saved = TRUE;

-- pulse:phase accounts
CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    email TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('Administrator', 'Reader')),
    refresh_interval_minutes INTEGER CHECK (refresh_interval_minutes BETWEEN 5 AND 1440),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_unique ON users (LOWER(email));

CREATE TABLE IF NOT EXISTS sessions (
    token_hash TEXT PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS sessions_user_id_idx ON sessions (user_id);
CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON sessions (expires_at);

ALTER TABLE folders ADD COLUMN IF NOT EXISTS owner_user_id BIGINT REFERENCES users(id) ON DELETE CASCADE;

CREATE TABLE IF NOT EXISTS system_settings (
    key TEXT PRIMARY KEY,
    value INTEGER NOT NULL CHECK (value BETWEEN 5 AND 1440),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO system_settings (key, value) VALUES
    ('refresh_interval_minutes', 15),
    ('shared_feed_ttl_minutes', 15)
ON CONFLICT (key) DO NOTHING;

-- pulse:phase shared_sources
CREATE TABLE feed_sources (
    id BIGSERIAL PRIMARY KEY,
    normalized_url TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    url TEXT NOT NULL,
    site_url TEXT NOT NULL DEFAULT '',
    last_checked_at TIMESTAMPTZ,
    last_success_at TIMESTAMPTZ,
    last_error TEXT,
    lease_token TEXT,
    lease_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE user_feeds (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    feed_source_id BIGINT NOT NULL REFERENCES feed_sources(id) ON DELETE CASCADE,
    folder_id BIGINT REFERENCES folders(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, feed_source_id)
);

CREATE INDEX user_feeds_source_idx ON user_feeds (feed_source_id);
CREATE INDEX user_feeds_user_folder_idx ON user_feeds (user_id, folder_id);

ALTER TABLE articles RENAME TO legacy_articles;
ALTER TABLE feeds RENAME TO legacy_feeds;

CREATE TABLE articles (
    id BIGSERIAL PRIMARY KEY,
    feed_source_id BIGINT NOT NULL REFERENCES feed_sources(id) ON DELETE CASCADE,
    guid TEXT NOT NULL,
    title TEXT NOT NULL,
    url TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    published_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (feed_source_id, guid)
);

CREATE INDEX articles_source_published_idx ON articles (feed_source_id, published_at DESC);

CREATE TABLE user_article_states (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    article_id BIGINT NOT NULL REFERENCES articles(id) ON DELETE CASCADE,
    is_read BOOLEAN NOT NULL DEFAULT FALSE,
    is_saved BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, article_id)
);

ALTER TABLE folders ALTER COLUMN owner_user_id SET NOT NULL;
DROP INDEX IF EXISTS folders_name_lower_unique;
CREATE UNIQUE INDEX folders_owner_name_lower_unique ON folders (owner_user_id, LOWER(name));

-- pulse:phase user_profile
ALTER TABLE users
    ADD COLUMN first_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN last_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN nickname TEXT NOT NULL DEFAULT '';

-- pulse:phase feed_customization
ALTER TABLE user_feeds ADD COLUMN title_override TEXT;
