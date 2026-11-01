-- HTTP cache validators of the last successful response, sent as
-- If-None-Match / If-Modified-Since on the next refresh of the source.
ALTER TABLE feed_sources ADD COLUMN IF NOT EXISTS etag TEXT;
ALTER TABLE feed_sources ADD COLUMN IF NOT EXISTS last_modified TEXT;
