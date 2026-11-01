-- A pending feed request can have several requesters. Each requester keeps the
-- folder, title and (for URL changes) the subscription to replace that they chose.
CREATE TABLE feed_request_requesters (
    request_id BIGINT NOT NULL REFERENCES feed_requests(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    folder_id BIGINT REFERENCES folders(id) ON DELETE SET NULL,
    title TEXT NOT NULL DEFAULT '',
    replace_feed_id BIGINT REFERENCES user_feeds(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (request_id, user_id)
);

CREATE INDEX feed_request_requesters_user_idx ON feed_request_requesters (user_id);

INSERT INTO feed_request_requesters (request_id, user_id, folder_id, title, replace_feed_id, created_at)
SELECT id, requested_by, folder_id, title, replace_feed_id, created_at FROM feed_requests;

ALTER TABLE feed_requests
    DROP COLUMN requested_by,
    DROP COLUMN folder_id,
    DROP COLUMN title,
    DROP COLUMN replace_feed_id;

-- A request whose last requester disappears (account removed, or the subscription a
-- URL change would replace was deleted) is removed instead of lingering in the queue.
CREATE FUNCTION feed_request_drop_when_unrequested() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM feed_requests fr
    WHERE fr.id = OLD.request_id
      AND NOT EXISTS (SELECT 1 FROM feed_request_requesters r WHERE r.request_id = OLD.request_id);
    RETURN NULL;
END;
$$;

CREATE TRIGGER feed_request_requesters_drop_unrequested
    AFTER DELETE ON feed_request_requesters
    FOR EACH ROW EXECUTE FUNCTION feed_request_drop_when_unrequested();
