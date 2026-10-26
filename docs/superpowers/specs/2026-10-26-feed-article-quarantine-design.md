# Feed article quarantine design

## Intent

Retain articles caught by the stop-word filter so an administrator can review
them later. An allowed article should enter the normal shared source article
store with its original publication time. The normal per-user subscription
queries then show it only to users subscribed to that source.

## Goals and behavior

- Store the article's GUID, title, URL, summary, original `published_at`, source
  metadata, matched term, and moderation state in the existing
  `blocked_feed_articles` table.
- Let administrators allow or reject quarantined items. A rejection is
  reversible: administrators may later allow that item. Refreshes update the
  stored feed content and match metadata without resetting a prior decision.
- On allow, insert or update the shared article using its original publication
  time, then mark the quarantine item allowed in one transaction. Existing
  library queries continue to scope visibility through user subscriptions.
- Keep pending, rejected, and allowed records available to administrators,
  with a pending-first view and moderation actions for pending and rejected
  items. Allowed items are read-only history.
- Preserve existing historical rows. They lack summary and publication time;
  no reliable backfill is possible. Their moderation can use a documented
  fallback only if explicitly needed; the preferred initial behavior is to
  leave them as historical log entries without moderation actions.

## Non-goals

- Per-user copies of shared articles or manual library insertion.
- Changing subscription visibility, feed approval, or the matching algorithm.
- Restoring an allowed item to quarantine if its term later remains matched.

## Approaches

1. Extend `blocked_feed_articles` into the quarantine and moderation record.
   This reuses its unique source/GUID identity and keeps refresh deduplication
   simple. **Recommended; this is the proposed approach for approval.**
2. Add a separate quarantine table and retain the current table as an audit
   log. This keeps concepts separate but duplicates item state and requires
   coordinating both tables on every refresh and moderation decision.

## Implementation calls

- Add a numbered migration for full article fields, moderation status, decision
  timestamps/user, and indexes for pending/recent moderation queries.
- Keep each state transition and article insertion transactional. A rejected
  item remains rejected across refreshes and can later transition to allowed.
- Expose administrator-only listing and allow/reject endpoints. Bound list
  size and validate identifiers as with existing admin endpoints.
- Add an administration section with article text, source, matched term,
  publication time, status, and allow/reject actions; localize it in all three
  supported languages.
- Cover refresh persistence, source-scoped visibility, original publication
  time, repeat refresh behavior, and reversible moderation in tests.

## Constraints

Use the existing Go, PostgreSQL, Chi, React, TypeScript, and i18next stack.
Keep SQL parameterized and preserve the current per-user subscription boundary.
