# Feed Article Quarantine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Quarantine stop-word matches for administrator review, with reversible rejection and approval into the source's normal subscriber-only article stream at the original publication time.

**Architecture:** Extend `blocked_feed_articles` with article content and moderation state, and atomically promote allowed items into `articles`. Keep source/GUID identity and per-user visibility through existing `user_feeds` queries. Add administrator endpoints and a localized moderation view.

**Tech Stack:** Go, PostgreSQL, Chi, React, TypeScript, i18next, Go tests, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-26-feed-article-quarantine-design.md`

## Global Constraints

- Use the existing Go, PostgreSQL, Chi, React, TypeScript, and i18next stack.
- Keep SQL parameterized and preserve the current per-user subscription boundary.
- Refreshes must not reset a prior moderation decision.
- Allow must preserve the article's original `published_at`.

## Review Focus

- Repeated refreshes after rejection must update quarantined content without changing rejected status; cover in store refresh integration test.
- Re-allowing a rejected article must promote exactly one article and retain publication time; cover in store moderation integration test.
- A reader subscribed to source A must not see an item from source B; cover in store moderation integration test using existing user-scoped list query.
- Concurrent or repeated allow requests must be idempotent; cover in store moderation integration test.
- Legacy quarantine rows lack summary/publication time; ensure migration leaves them historical and the admin UI does not offer moderation actions; cover with migration/store test or API response fixture.

## File Map

- `backend/internal/store/migrations/009_feed_article_quarantine.sql`: add nullable legacy-compatible article fields, status and decision metadata, and status/recent indexes.
- `backend/internal/store/feed_filter.go`: enrich quarantine model and refresh upsert, list moderation records, and implement transactional decisions.
- `backend/test/internal/store/feed_filter_test.go`: refresh, deduplication, legacy row, status preservation, and source visibility behavior.
- `backend/internal/httpapi/admin_handlers.go`, `handler.go`: administrator-only moderation list and decision endpoints.
- `backend/test/internal/httpapi/auth_test.go`: authorization and request/response behavior for moderation routes.
- `frontend/src/api.ts`: quarantine types and API calls.
- `frontend/src/AdminPanel.tsx`: status-aware moderation actions, refresh list after decisions.
- `frontend/test/src/AdminPanel.test.tsx`: pending/rejected actions, allowed read-only state, and failure handling.
- `frontend/src/locales/{en,de,ru}.json`: localized labels and messages.
- `docs/PROJECT.md`, `README.ru.md`: describe quarantine and subscriber-scoped release.

### Task 1: Persist complete quarantined articles and decisions

**Interfaces:** Extend `BlockedFeedArticle` with `articleGuid`, `summary`, `publishedAt`, `status`, `decidedAt`, and `canModerate`. Add `ListBlockedFeedArticles(ctx context.Context, limit int)`, preserving current method name/signature, and `ModerateBlockedFeedArticle(ctx context.Context, id int64, allow bool, moderatorID int64) error`.

- [x] Write integration tests proving quarantine stores summary and source publication timestamp, refresh keeps rejected status, refresh updates feed content, legacy rows remain non-moderatable, and allowed items promote once with original publication timestamp.
- [x] Run focused store test and confirm it fails on missing fields/behavior.
- [x] Add migration `009_feed_article_quarantine.sql`; retain legacy rows with nullable publication time and set `canModerate` false when required payload fields are unavailable.
- [x] Implement refresh upsert so pending rows remain pending, rejected rows remain rejected, and allowed rows are not re-quarantined; do not delete an existing shared article merely because a newer feed version matches.
- [x] Implement transactional `ModerateBlockedFeedArticle`; reject records as rejected and allow pending/rejected records by upserting the original item into `articles` and marking it allowed. Repeated allow is idempotent; decision must be scoped by record ID and persist moderator/time.
- [x] Run focused store tests and the full backend suite.

### Task 2: Expose administrator moderation endpoints

**Interfaces:** `GET /api/admin/blocked-articles?limit=100` returns enriched records ordered pending first then recent. `POST /api/admin/blocked-articles/{id}/allow` and `/reject` return `204`; nonexistent or non-moderatable records return `404`.

- [x] Add API tests for administrator success, reader forbidden, invalid IDs, missing/legacy item, and successful status transition.
- [x] Run focused API tests and confirm they fail because routes are absent.
- [x] Register administrator-only routes and handlers; pass authenticated administrator ID to the store method and map store errors to established JSON error responses.
- [x] Run focused API tests and full backend suite.

### Task 3: Add administration moderation UI

- [x] Add component tests for pending and rejected rows showing allow/reject, allowed rows read-only, and an API failure preserving the row and showing an error.
- [x] Run the focused component test and confirm it fails because moderation UI is absent.
- [x] Add TypeScript quarantine types and API calls; show summary, source, matched term, original publication time, and state. Keep actions available on pending and rejected items, and hide them for legacy and allowed items.
- [x] Add English, German, and Russian strings for moderation states, labels, actions, and notices.
- [x] Run focused frontend tests, all frontend unit tests, and the production build.

### Task 4: Document and verify end-to-end behavior

- [x] Update `docs/PROJECT.md` and `README.ru.md` with quarantine, reversible decisions, original publication date, and subscription-scoped visibility.
- [x] Run the full Go suite, frontend unit suite, frontend build, and `git diff --check`.
- [x] Inspect migration, API authorization, status transitions, and UI behavior against the approved design; report any environment-dependent verification not run.
