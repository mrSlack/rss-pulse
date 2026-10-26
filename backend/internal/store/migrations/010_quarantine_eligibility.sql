-- Eligibility is fixed when a record is created, never inferred again on refresh.
-- Existing historical records remain history even if their GUID reappears.
ALTER TABLE blocked_feed_articles
    ADD COLUMN moderation_eligible BOOLEAN NOT NULL DEFAULT FALSE;
