ALTER TABLE feed_sources
    ADD COLUMN added_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL;

UPDATE feed_sources fs
SET added_by_user_id = (
    SELECT uf.user_id
    FROM user_feeds uf
    WHERE uf.feed_source_id = fs.id
    ORDER BY uf.created_at, uf.id
    LIMIT 1
)
WHERE added_by_user_id IS NULL;

CREATE TABLE feed_stop_words (
    id BIGSERIAL PRIMARY KEY,
    term TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO feed_stop_words (term) VALUES
    ('заработок без вложений'),
    ('легкие деньги'),
    ('мошенник'),
    ('мошенничество'),
    ('обман'),
    ('развод'),
    ('скам'),
    ('adult content'),
    ('adult entertainment'),
    ('betrug'),
    ('betrüger'),
    ('escort'),
    ('erotic'),
    ('erotica'),
    ('erotik'),
    ('erotisch'),
    ('erwachseneninhalt'),
    ('hentai'),
    ('inhalte für erwachsene'),
    ('leichtes geld'),
    ('only fans'),
    ('onlyfans'),
    ('porn'),
    ('pornographic'),
    ('pornography'),
    ('pornografie'),
    ('pornografisch'),
    ('sex'),
    ('sex chat'),
    ('sex video'),
    ('sexchat'),
    ('sexvideo'),
    ('webcam sex'),
    ('xxx'),
    ('вебкам'),
    ('взрослый контент'),
    ('интим'),
    ('эротика'),
    ('эскорт'),
    ('порно'),
    ('порнографический'),
    ('порнография'),
    ('секс'),
    ('секс видео'),
    ('секс чат')
ON CONFLICT (term) DO NOTHING;

CREATE TABLE blocked_feed_articles (
    id BIGSERIAL PRIMARY KEY,
    feed_source_id BIGINT NOT NULL REFERENCES feed_sources(id) ON DELETE CASCADE,
    article_guid TEXT NOT NULL,
    article_title TEXT NOT NULL,
    article_url TEXT NOT NULL DEFAULT '',
    feed_title TEXT NOT NULL,
    feed_url TEXT NOT NULL,
    added_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    matched_term TEXT NOT NULL,
    first_blocked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_blocked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (feed_source_id, article_guid)
);

CREATE INDEX blocked_feed_articles_recent_idx ON blocked_feed_articles (last_blocked_at DESC, id DESC);
