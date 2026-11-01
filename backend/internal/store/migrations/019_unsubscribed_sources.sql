-- Start of the period in which a shared source has had no subscribers, as first
-- observed by the maintenance job. NULL means subscribed (or not yet observed).
-- Sources unsubscribed for longer than UNSUBSCRIBED_SOURCE_RETENTION_DAYS are
-- deleted with their articles; see DeleteUnsubscribedSources.
ALTER TABLE feed_sources ADD COLUMN IF NOT EXISTS unsubscribed_since TIMESTAMPTZ;
