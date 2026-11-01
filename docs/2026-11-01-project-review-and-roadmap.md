# Project Review and Improvement Roadmap — November 1, 2026

Scope: full read-through of the backend (`backend/`), frontend (`frontend/`),
Compose/Docker/Nginx configuration, scripts, tests, and project documentation
at commit `713df34`. This is a planning document only: no application code was
changed. It complements, and does not repeat, the targeted
[reliability review](2026-10-25-reliability-review.md) and the
[architecture reference](PROJECT.md).

Each finding has a stable ID (`SEC-`, `REL-`, `PERF-`, `FE-`, `OPS-`, `DOC-`,
`FEAT-`) so that roadmap tasks, commits, and future reviews can refer to it.

## 1. Overall assessment

Pulse is in good shape for a small self-hosted product. The foundations are
deliberate and mostly correct:

- Clear separation of shared canonical sources/articles from per-user
  subscriptions, folders, and article state; ownership is enforced in SQL.
- SSRF defences are layered: URL validation, DNS resolution checks, a dialer
  that re-checks every resolved IP, redirect pinning to host/port, HTTPS
  downgrade protection, response size and time limits.
- Argon2id password hashing with bounded parameters, opaque hashed session
  tokens, `HttpOnly` + `SameSite=Strict` cookies, and an origin check on
  mutating requests.
- Refresh concurrency is bounded and fenced with database leases.
- Strict JSON decoding (single object, unknown fields rejected, 1 MiB cap).
- Feed HTML is never injected into the DOM; only safe image URLs are rendered,
  with `referrerPolicy="no-referrer"`.
- Good test culture: Go integration tests against real PostgreSQL, Vitest unit
  tests, and Playwright visual/E2E tests on an isolated stack.

The main gaps are: no brute-force protection on login, several data-correctness
issues in feed ingestion and pagination, unbounded data growth, queries that
will degrade as article volume grows, a monolithic `App.tsx`, no automated checks (CI is deferred by owner decision), and a
few repository hygiene problems (a committed 18 MB binary).

## 2. Findings

Severity: **High** — security exposure or user-visible data loss/incorrectness;
**Medium** — reliability, scalability, or maintainability risk with a realistic
trigger; **Low** — hygiene, polish, or future-proofing.

### 2.1 Security

| ID | Sev | Finding | Evidence | Recommendation |
| --- | --- | --- | --- | --- |
| SEC-1 | High | No rate limiting or lockout on `POST /api/auth/login`. Online password guessing is unlimited. Each attempt also costs ~64 MiB of Argon2id memory, so a burst of parallel logins can exhaust API memory (resource-exhaustion DoS). | `backend/internal/httpapi/auth.go:83`, `backend/internal/auth/password.go` | Add a per-IP and per-account limiter (token bucket in memory is enough for a single instance; e.g. 5 attempts/min per account, 20/min per IP) returning `429` with a stable error code. Bound concurrent Argon2 computations with a semaphore (e.g. `GOMAXPROCS`). Trust `X-Real-IP` only from the Nginx network. |
| SEC-2 | Medium | Login timing reveals whether an account exists: unknown e-mails return immediately, known ones run Argon2id (~tens of ms). | `backend/internal/httpapi/auth.go:96-99` | On `ErrNotFound`, verify against a fixed dummy hash so both paths cost the same. |
| SEC-3 | Medium | No way to disable an account or revoke its sessions. A compromised or departed user keeps access for up to 14 days; an admin cannot force sign-out. Account deletion is an explicit non-goal, but disabling is not covered. | `docs/PROJECT.md` (non-goals), `backend/internal/store/accounts.go` | Add `users.disabled_at`; reject disabled users in `UserCredentialByEmail`/`UserForSession`; revoke sessions on disable, role change, and admin password reset. Add admin "sign out everywhere". |
| SEC-4 | Medium | SSRF blocklist misses several special-purpose ranges: CGNAT `100.64.0.0/10`, `192.0.0.0/24`, benchmarking `198.18.0.0/15`, reserved `240.0.0.0/4`, NAT64 `64:ff9b::/96` and 6to4 `2002::/16` (which can embed private IPv4), and documentation ranges. | `backend/internal/feeds/fetch.go:179` | Rewrite `isPublicIP` with `net/netip` and an explicit deny-prefix table; unwrap NAT64/6to4-embedded IPv4 and re-check. Table-driven tests. |
| SEC-5 | Medium | Nginx sends no security headers: no `Content-Security-Policy`, `X-Frame-Options`/`frame-ancestors`, `Referrer-Policy`, `Permissions-Policy`. With feed-supplied URLs and images in the page, a CSP is cheap defence in depth. | `frontend/nginx.conf` | Add `default-src 'self'; img-src https: http: data:; object-src 'none'; frame-ancestors 'none'; base-uri 'self'` (move the inline theme script to a hashed or external script), `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`. HSTS belongs to the TLS-terminating proxy and should be documented there. |
| SEC-6 | Low | Feed request queue can be spammed: pending requests are unbounded per user and do not count against the 100-feed limit; `ListPendingFeedRequests` has no limit. | `backend/internal/store/feed_approval.go` | Cap pending requests per user (e.g. 10) and paginate the admin list. |
| SEC-7 | Low | Remote images in the reader are loaded directly from third-party hosts, which exposes reader IP addresses and read times to tracking pixels. Accepted trade-off today, but worth an explicit decision. | `frontend/src/App.tsx:666` | Document the trade-off; optionally add a per-user "load remote images" setting, or a hardened image proxy later. |

### 2.2 Correctness and reliability

| ID | Sev | Finding | Evidence | Recommendation |
| --- | --- | --- | --- | --- |
| REL-1 | High | **RSS dates with a one-digit day are not parsed.** RFC 822 allows `Mon, 2 Jan 2006 15:04:05 GMT`, which is common in real feeds. Go's `RFC1123` layout requires two digits, so these items fall back to `time.Now()`; their order in the reader reflects first-seen time instead of publication time. Verified with Go 1.27: `"Mon, 2 Jan 2006 15:04:05 GMT"` fails `RFC1123`, `RFC1123Z`, and `RFC3339`; zone-less ISO timestamps (`2006-01-02T15:04:05`) also fail. | `backend/internal/feeds/parser.go:184` | Add layouts with `_2`/`2` day and without weekday, zone-less ISO variants, and `-07:00` numeric offsets; normalise whitespace. Add a table-driven test of real-world date samples. |
| REL-2 | High | **Infinite scroll skips articles in the "Unread" view.** Opening an article marks it read (`App.tsx:197`), but it stays in the local list; the next page is requested with `offset = articles.length` (`App.tsx:473`). The server's unread set has shrunk, so the offset jumps past unread articles that the user never sees. New articles arriving between page loads cause the same skip/duplicate effect in every view. | `frontend/src/App.tsx:197`, `frontend/src/App.tsx:473`, `backend/internal/store/user_library.go:395` | Replace offset paging with keyset (cursor) paging on `(published_at, id)`: the API returns `nextCursor`; the client sends `before=<cursor>`. Keep `offset` temporarily for compatibility. Add a Playwright/Go test that marks items read between page loads. |
| REL-3 | Medium | Fuzzy article de-duplication can merge distinct articles. When the GUID changes, any existing article in the same source within ±30 minutes that shares the **title or summary** is overwritten. Feeds with repeated titles ("Daily digest", "Podcast episode") or empty-ish summaries can lose articles and transfer read/saved state to the wrong item. | `backend/internal/store/user_library.go` (`upsertSourceItems`) | Restrict the fallback to URL match (or title **and** URL), require non-trivial values, and log merges. Add regression tests with repeated titles. |
| REL-4 | Medium | Toggling read/saved triggers a full reload of the article list and structure (`refreshVersion++`), which resets pagination and scroll position and multiplies requests. | `frontend/src/App.tsx:207-218` | Update local state optimistically and refresh only counters (or receive updated counts in the PATCH response). |
| REL-5 | Medium | Refresh control is global. While any refresh (scheduled or another user's manual refresh) runs, every other user's manual refresh returns `409 feed.refresh_busy`, and `GET /api/refresh` reports the global state. | `backend/internal/httpapi/article_handlers.go:112`, `backend/internal/syncer/syncer.go` | Queue per-user requests (set of pending user IDs) instead of a single slot, and report per-user status. |
| REL-6 | Medium | The scheduler first runs 15 minutes after start-up, so a restart leaves feeds stale for up to 15 minutes; saved refresh intervals are still inactive (already documented). | `backend/internal/syncer/syncer.go:128` | Run one cycle shortly after start; then deliver the deferred scheduling task (see Phase 3). |
| REL-7 | Medium | Graceful shutdown does not wait for the syncer; lease release uses `context.Background()` without timeout (noted on 2026-10-25, still open). | `backend/cmd/api/main.go:49`, `backend/internal/syncer/syncer.go:185` | Track the syncer with a `WaitGroup`/done channel, use a bounded cleanup context, and wait within the shutdown deadline. |
| REL-8 | Low | Feed URL change (`PATCH /api/feeds/{id}`) fetches the feed synchronously inside the HTTP request (up to 15 s, server write timeout 25 s). Slow sources lead to timeouts and a confusing UX. | `backend/internal/httpapi/library_handlers.go:294` | Commit the subscription change, enqueue a refresh for that source, and let the UI show "refreshing". |
| REL-9 | Low | Feed request edge cases: a second user requesting a URL already pending for someone else gets `409 feed.request_pending` and is never subscribed after approval; approving a `replace` request when the user is already subscribed to the target source fails with a generic 500 (unique violation). | `backend/internal/store/feed_approval.go:83`, `ApproveFeedRequest` | Model requests as (url, requester) pairs or attach additional requesters; map the unique violation to `ErrDuplicate`. |
| REL-10 | Low | Atom parsing gaps: `<content type="xhtml">` child markup is dropped (only character data is read), RDF `rdf:about` is ignored as an identifier, and the 200-item cap is applied after parsing the whole document. | `backend/internal/feeds/parser.go` | Capture inner XML for Atom content; use `rdf:about` as GUID fallback; add fixtures. |

### 2.3 Performance and data growth

| ID | Sev | Finding | Evidence | Recommendation |
| --- | --- | --- | --- | --- |
| PERF-1 | High | **No retention for articles, quarantine records, or expired data.** `articles`, `user_article_states`, and `blocked_feed_articles` grow forever. Every other query below gets slower over time. | schema in `backend/internal/store/migrations/` | Add a retention job: delete articles older than N days (configurable, default e.g. 180) that are not saved by any user; prune `blocked_feed_articles` decided records after N days. Document the policy. |
| PERF-2 | Medium | Unread counters in `ListFeedsForUser` / `ListFoldersForUser` join all articles of every subscribed source on each call, and they are called after every read/save toggle (REL-4). Cost is O(total articles in the user's sources). | `backend/internal/store/user_library.go` | First apply retention (PERF-1). Then compute counts only within the retention window, or maintain counters (the `(user_id, article_id)` primary key already serves the state lookup). Measure with `EXPLAIN ANALYZE` on a seeded dataset. |
| PERF-3 | Medium | Search is `STRPOS(LOWER(title || ' ' || summary), LOWER($q))` — a sequential scan over all reachable articles, including full HTML summaries. | `backend/internal/store/user_library.go:379` | Add a generated `tsvector` (`simple` config, multi-language content) with a GIN index, or `pg_trgm` GIN on title/plain-text summary. Strip HTML into a stored plain-text column at ingest. |
| PERF-4 | Medium | `UserForSession` runs `DELETE FROM sessions WHERE expires_at <= NOW()` on **every authenticated request** — a write per read request and lock contention under load. | `backend/internal/store/accounts.go:51` | Move expiry cleanup to a periodic job (with retention, PERF-1); the `expires_at > NOW()` predicate already protects correctness. |
| PERF-5 | Low | Ingestion runs 2–4 queries per item (200 items → up to ~800 round trips per source, inside one transaction holding the source lock), plus one quarantine lookup per item. | `upsertSourceItems`, `filterAndLogBlockedItems` | Batch with `pgx.Batch` or a single `INSERT … ON CONFLICT (feed_source_id, guid)` statement; load quarantine statuses for all GUIDs in one query. |
| PERF-6 | Low | No conditional GET: every refresh downloads the full feed. | `backend/internal/feeds/fetch.go` | Store `ETag`/`Last-Modified` per source and send `If-None-Match`/`If-Modified-Since`; treat `304` as success. |
| PERF-7 | Low | Nginx serves Vite's hashed assets with `Cache-Control: no-cache` and without gzip/brotli. | `frontend/nginx.conf` | `location /assets/ { expires 1y; add_header Cache-Control "public, immutable"; }`, keep `no-cache` for `index.html`; enable `gzip` for JS/CSS/JSON. |
| PERF-8 | Low | Pool size (`MaxConns = 12`) and timeouts are hard-coded. | `backend/cmd/api/main.go:115` | Make configurable via environment with the current values as defaults. |

### 2.4 Frontend architecture and UX

| ID | Sev | Finding | Evidence | Recommendation |
| --- | --- | --- | --- | --- |
| FE-1 | Medium | `App.tsx` is 775 lines with ~45 `useState` hooks covering session, library, articles, pagination, seven modals, profile, and folder management. Hard to test and change safely; duplicated query-building logic between initial load and `loadMore`. | `frontend/src/App.tsx` | Extract hooks (`useSession`, `useLibrary`, `useArticleFeed`) and per-modal components (`FeedModal`, `FolderModal`, `ProfileModal`, `FolderManager`). Consider TanStack Query for caching, cancellation, and invalidation. Refactor behind existing Playwright screenshots. |
| FE-2 | Medium | No global handling of `401`: when a session expires, requests show error toasts but the user is not returned to the sign-in screen. | `frontend/src/api.ts` | Throw a typed `ApiError` with `status`/`code`; on `401` clear the session and show the sign-in screen with a localized "session expired" notice. |
| FE-3 | Low | No request cancellation (`AbortController`); stale responses are ignored with flags but still consume bandwidth during fast filter/search changes. | `frontend/src/App.tsx` | Pass `signal` through `api.request`. Comes for free with FE-1 if a query library is adopted. |
| FE-4 | Low | No ESLint (React hooks rules would flag the missing `t` dependencies in effects). | `frontend/package.json` | Add ESLint flat config with `typescript-eslint` and `eslint-plugin-react-hooks`; run locally via `npm run lint`. |
| FE-5 | Low | `vite` and `@vitejs/plugin-react` are runtime `dependencies`; several dependencies use caret ranges while others are pinned. | `frontend/package.json` | Move build tools to `devDependencies`; choose one pinning policy (lockfile already pins). |
| FE-6 | Low | Common reader features are missing: "mark all as read" (per feed/folder/view), keyboard navigation (`j`/`k`, `m`, `s`, `o`), OPML import/export. | — | See Phase 5. |

### 2.5 Operations, tooling, and repository hygiene

| ID | Sev | Finding | Evidence | Recommendation |
| --- | --- | --- | --- | --- |
| OPS-1 | High | An 18 MB Go executable `backend/api` (with debug info) is committed. `.gitignore` ignores `backend/pulse-reader` but not `backend/api`. It bloats every clone and can be mistaken for the current build. | `git ls-files backend/api` | `git rm --cached backend/api`, ignore `/backend/api`. History rewrite is optional and must be agreed separately. |
| OPS-2 | Deferred | No CI. Nothing enforces `gofmt`, `go vet`, Go tests (with PostgreSQL), frontend tests, type checking, or the build on push/PR. | no `.github/` or other CI configuration | **Deferred by owner decision (2026-11-01): CI is out of scope until explicitly requested.** Until then, checks run manually with the documented commands (see task 0.4). |
| OPS-3 | Medium | The documented Go test command in `AGENTS.md` does not set `TEST_DATABASE_URL`, so all integration tests are **silently skipped**; `SESSION_HANDOFF.md` has the correct command. | `AGENTS.md`, `backend/test/internal/store/store_test.go:60` | Update `AGENTS.md`; make skipped integration tests fail when an env flag such as `REQUIRE_INTEGRATION=1` is set, so manual runs can prove they actually ran. |
| OPS-4 | Medium | The development API container runs `go mod tidy` on every start against the bind-mounted source, silently rewriting `go.mod`/`go.sum`. The dev `Dockerfile` copies `go.mod` without `go.sum`. | `compose.backend.yml:9`, `backend/Dockerfile:11` | Run `go run ./cmd/api` only; copy `go.sum`; run `go mod tidy` explicitly (and check with `git diff --exit-code` before committing). |
| OPS-5 | Medium | Observability is minimal: `log.Printf` only, no request/access logging, no structured fields (request ID is generated but never logged), no metrics. Refresh failures are only visible per-feed in the UI. | `backend/cmd/api/main.go`, `backend/internal/httpapi/handler.go` | Switch to `log/slog` (JSON in production), add an access-log middleware with request ID, user ID, status, and latency; log refresh cycle summaries. Optional `/metrics` (Prometheus) on an internal port. Split health into liveness and readiness. |
| OPS-6 | Medium | Backups are created only by `update-production.sh`; there is no scheduled backup, rotation, or tested restore procedure. | `scripts/update-production.sh` | Add a backup script for cron/systemd timers with rotation, document and periodically test restore. |
| OPS-7 | Low | Migrations run only forward with no down scripts and no pre-deploy compatibility check; the API runs migrations at start-up in every replica. | `backend/internal/store/store.go` | Acceptable for a single instance. Document "backup before upgrade" (already in the update script) and a manual rollback procedure; keep the advisory lock. |
| OPS-8 | Low | Dead code kept only for tests: `SubscribeFeed`, `EnsureFeedSourceForUser` (used only in tests), `UpdateUserRole`, `UpdateUserRefreshInterval` (unused). | `backend/internal/store/` | Remove and rewrite affected tests against the production code paths. |
| OPS-9 | Low | Repeated error-mapping blocks in handlers (`ErrDuplicate` → 409 etc. appear 3–4 times per handler). | `backend/internal/httpapi/library_handlers.go` | Introduce a single `writeStoreError(w, err, context)` mapping table. |

### 2.6 Documentation consistency

| ID | Sev | Finding | Recommendation |
| --- | --- | --- | --- |
| DOC-1 | Low | `docs/PROJECT.md` says the `.agents/skills/` directory is included in the repository, but `.gitignore` ignores `.agents/skills/` and no files are tracked. | Decide whether skills are vendored; align `.gitignore` and the document. |
| DOC-2 | Low | `AGENTS.md` says "Keep Go tests beside the package", while most tests live in `backend/test/internal/...` (only two `_test.go` files are beside their packages). | Document the actual convention (external test packages under `backend/test/`), or move tests. |
| DOC-3 | Low | `docs/PROJECT.md` "Database migration file design" mentions only `006_…`; files `007`–`010` exist. The principle is correct, but an index of migrations would help. | Add a short migration list or point to the directory. |
| DOC-4 | Low | No API reference beyond prose in `PROJECT.md`. | Add an OpenAPI description (can be hand-written) and validate it against handler tests. |

## 3. Improvement roadmap

The phases are ordered by risk reduction per unit of effort. Each task lists the
findings it closes and the acceptance criteria. Estimates are rough
engineering days for one developer familiar with the codebase and include
tests. Every task follows the existing workflow: feature branch, regression
test first, `gofmt`, documented test commands, and screenshots for visual
changes.

### Phase 0 — Repository hygiene and local checks (≈1 day)

CI is **not** part of this plan; it will be added only on an explicit request
from the project owner.

| # | Task | Closes | Acceptance criteria | Est. |
| --- | --- | --- | --- | --- |
| 0.1 | Remove `backend/api` from the index and ignore it. | OPS-1 | `git ls-files backend/api` is empty; `.gitignore` covers build outputs. | 0.1 |
| 0.2 | Stop `go mod tidy` at container start; copy `go.sum` in the dev image. | OPS-4 | Starting the API leaves the working tree clean. | 0.2 |
| 0.3 | Fix the documented Go test command; add `REQUIRE_INTEGRATION`. | OPS-3, DOC-2 | With `REQUIRE_INTEGRATION=1` and no database, tests fail instead of skipping. | 0.3 |
| 0.4 | Document a single manual pre-merge checklist in `AGENTS.md` (`gofmt -l`, `go vet`, Go tests with `TEST_DATABASE_URL`, frontend tests, build, optional `govulncheck`/`npm audit`). No CI configuration. | OPS-3 | Checklist exists and every command in it has been run once successfully. | 0.2 |
| 0.5 | ESLint for the frontend (`npm run lint`), added to the manual checklist. | FE-4, FE-5 | `npm run lint` passes; hooks rules enabled. | 0.4 |

### Phase 1 — Security hardening (≈3 days)

| # | Task | Closes | Acceptance criteria | Est. |
| --- | --- | --- | --- | --- |
| 1.1 | Login rate limiting (per IP and per account) and an Argon2 concurrency semaphore. | SEC-1 | Unit tests: 6th attempt within a minute returns `429 auth.rate_limited`; a correct password after the window succeeds; translations added. | 1.0 |
| 1.2 | Constant-cost login for unknown accounts. | SEC-2 | Test asserts `VerifyPassword` is called for unknown e-mails. | 0.2 |
| 1.3 | `netip`-based public-address check with an explicit deny list incl. NAT64/6to4 unwrapping. | SEC-4 | Table-driven test for every listed range, IPv4 and IPv6. | 0.5 |
| 1.4 | Account disable + session revocation (on disable, role change, admin action). | SEC-3 | Disabled user's existing session receives `401` on next request; cannot log in; last-administrator rule applies to disabling. Migration + admin UI + Playwright screenshot. | 1.0 |
| 1.5 | Nginx security headers and CSP (externalize or hash the inline theme script). | SEC-5 | Browser E2E suite passes with CSP enabled and reports no CSP violations in console checks. | 0.3 |
| 1.6 | Limit pending feed requests per user; paginate the admin list. | SEC-6 | 11th pending request returns `409 feed.request_limit`. | 0.3 |

### Phase 2 — Data correctness (≈3 days)

| # | Task | Closes | Acceptance criteria | Est. |
| --- | --- | --- | --- | --- |
| 2.1 | Robust date parsing with a real-world fixture table. | REL-1 | Single-digit days, missing weekday, zone-less ISO, and named zones parse; unparseable dates keep the current fallback. | 0.5 |
| 2.2 | Keyset pagination for articles (`before` cursor; `offset` kept temporarily). | REL-2 | Go test: marking items read between pages never skips unread items; Playwright test for the Unread view; client switched to cursors. | 1.0 |
| 2.3 | Tighten fuzzy de-duplication. | REL-3 | Test: two items with equal titles and different URLs/GUIDs within 30 minutes remain separate; GUID change with same URL still merges. | 0.5 |
| 2.4 | Local state update for read/saved instead of full reload; return updated counters. | REL-4 | Toggling read keeps scroll position and loaded pages; one PATCH plus at most one counters request. | 0.5 |
| 2.5 | Parser gaps: Atom XHTML content, RDF `rdf:about`. | REL-10 | Fixtures for both formats. | 0.3 |
| 2.6 | Feed request edge cases (multiple requesters, duplicate on replace). | REL-9 | Second requester is subscribed on approval; duplicate replace returns `409`. | 0.5 |

### Phase 3 — Refresh engine and lifecycle (≈3–4 days)

| # | Task | Closes | Acceptance criteria | Est. |
| --- | --- | --- | --- | --- |
| 3.1 | Graceful shutdown: wait for syncer, bounded cleanup context. | REL-7 | Test: cancelling the context releases leases within the timeout; `main` waits for the syncer. | 0.5 |
| 3.2 | Per-user manual refresh queue and per-user status. | REL-5 | Two users can request refresh concurrently; neither receives `refresh_busy`. | 0.75 |
| 3.3 | Scheduler semantics: initial run after start, tick derived from the minimum TTL, and activation of saved global/per-user intervals in `DueSources` (deferred task in `PROJECT.md`). Requires a short design note first. | REL-6 | Design note approved; deterministic tests with an injected clock; admin UI labels updated from "inactive". | 1.5 |
| 3.4 | Conditional GET (ETag / Last-Modified). | PERF-6 | `304` updates `last_success_at` without touching articles; migration adds two columns. | 0.5 |
| 3.5 | Asynchronous fetch on feed URL change. | REL-8 | `PATCH` returns without network I/O; UI shows the refreshing state. | 0.5 |

### Phase 4 — Performance and data lifecycle (≈3–4 days)

| # | Task | Closes | Acceptance criteria | Est. |
| --- | --- | --- | --- | --- |
| 4.1 | Seed script for a realistic dataset (e.g. 50 users, 500 sources, 500 k articles) and a baseline of `EXPLAIN ANALYZE` for the hot queries. | — | Baseline numbers stored in `docs/`. | 0.5 |
| 4.2 | Retention job (articles not saved by anyone older than N days, decided quarantine records, expired sessions); configurable via environment and documented. | PERF-1, PERF-4 | Integration test proves saved articles survive; session cleanup removed from the request path. | 1.0 |
| 4.3 | Full-text search (plain-text column + `tsvector`/GIN or `pg_trgm`). | PERF-3 | Search latency on the seeded dataset under an agreed budget (e.g. p95 < 100 ms); behaviour for Cyrillic and German confirmed by tests. | 1.0 |
| 4.4 | Cheaper unread counters; batch ingestion statements. | PERF-2, PERF-5 | Counter query and source commit time measurably lower than the 4.1 baseline. | 1.0 |
| 4.5 | Static asset caching and compression in Nginx; configurable DB pool. | PERF-7, PERF-8 | Hashed assets served with `immutable`; `index.html` with `no-cache`. | 0.25 |

### Phase 5 — Frontend architecture and reader features (≈5–7 days)

| # | Task | Closes | Acceptance criteria | Est. |
| --- | --- | --- | --- | --- |
| 5.1 | Typed `ApiError`, global `401` handling, `AbortController` support. | FE-2, FE-3 | Expired session returns to sign-in with a notice; unit tests. | 0.5 |
| 5.2 | Split `App.tsx` into hooks and modal components (optionally adopt TanStack Query). No visual change. | FE-1 | `App.tsx` < 250 lines; all Playwright screenshots unchanged; unit tests per extracted hook. | 2.0 |
| 5.3 | "Mark all as read" for the current view (feed / folder / filter), with a server endpoint using the same filter semantics and an upper time bound. | FE-6 | Go and Playwright tests; translations in en/de/ru. | 1.0 |
| 5.4 | Keyboard shortcuts with a help overlay. | FE-6 | Shortcuts disabled while typing in inputs; accessible help dialog. | 0.75 |
| 5.5 | OPML import/export (import goes through the approval queue for unknown sources and respects the 100-feed limit). | FE-6 | Round-trip test; size limits; validation errors localized. | 1.5 |
| 5.6 | Optional per-user remote-image setting. | SEC-7 | Setting stored server-side; images replaced by a placeholder when disabled. | 0.5 |

### Phase 6 — Operations and documentation (≈2–3 days, can run in parallel)

| # | Task | Closes | Acceptance criteria | Est. |
| --- | --- | --- | --- | --- |
| 6.1 | Structured logging (`slog`), access log with request ID, refresh cycle summary. | OPS-5 | Logs are JSON in production; request ID is returned in a response header and logged. | 0.75 |
| 6.2 | Liveness vs readiness endpoints; optional Prometheus metrics (refresh duration/errors, HTTP latency). | OPS-5 | Compose health checks use readiness. | 0.5 |
| 6.3 | Scheduled backup script with rotation, documented and tested restore in all three README editions. | OPS-6, OPS-7 | Restore rehearsal recorded in the docs. | 0.5 |
| 6.4 | Remove dead store functions; central store-error mapping in handlers. | OPS-8, OPS-9 | No behavioural change; tests updated. | 0.5 |
| 6.5 | Documentation fixes and an OpenAPI description. | DOC-1, DOC-3, DOC-4 | Docs consistent with the repository; spec validated by a test. | 0.75 |

### Execution stages (agreed 2026-11-01)

The phases above group work by topic. Work is executed in the following
stages instead; each stage is one branch, test-first, verified with the manual
commands from `AGENTS.md`.

| Stage | Name | Tasks | Depends on | Est. |
| --- | --- | --- | --- | --- |
| 1 | Quick fixes — **done** (branch `stage-1/quick-fixes`) | 0.1–0.3, 2.1 (REL-1), 1.2 (SEC-2), 1.3 (SEC-4) | — | 1–1.5 |
| 2 | Sign-in protection — **done** (`stage-2/sign-in-protection`) | 1.1 (SEC-1), 1.5 (SEC-5), 1.6 (SEC-6), 0.5 | 1 | 1.5–2 |
| 3 | Reading correctness — **done** (`stage-3/reading-correctness`) | 2.2 + 2.4 together (REL-2, REL-4), 2.3 (REL-3) | 1 | 2–2.5 |
| 4 | Data growth — **done** (`stage-4/data-growth`) | 4.1 (baseline), 4.2 (retention, session cleanup) | decision Q1 | 1.5 |
| 5 | Refresh engine | 3.1, 3.2, 3.4, 3.5, then design note + 3.3 | decision Q3 | 3–3.5 |
| 6 | Accounts and requests | 1.4 (SEC-3), 2.6 (REL-9), 2.5 (REL-10) | decision Q2 | 2 |
| 7 | Performance | 4.3, 4.4, 4.5 | 4 | 3 |
| 8 | Frontend | 5.1, 5.2, then 5.3–5.5 (5.6 per decision Q4) | 3 | 5–6 |
| 9 | Operations and cleanup | 0.4, 6.1–6.5 | 1; may run in parallel | 2–2.5 |

Total ≈ 4 weeks. Stages 1–3 close every High finding except PERF-1, which is
closed by stage 4.

## 4. Open questions for the product owner

Decisions (2026-11-01, delegated to the implementer by the owner): Q1 — 180 days
for unsaved articles, 90 days for decided quarantine records; Q2 — add account
disabling; Q3 — shortest subscriber interval, bounded below by the shared TTL;
Q4 — keep loading remote images directly, task 5.6 deferred; Q5 — remove the
binary going forward only. Stage branches are merged into `improvements`.

1. Retention period for unsaved articles and for decided quarantine records
   (proposal: 180 and 90 days).
2. Should account **disabling** be added even though deletion remains a
   non-goal? (Recommended: yes.)
3. Scheduler semantics for per-user intervals with shared sources: should a
   source be refreshed at the **shortest** interval among its subscribers?
   (Recommended: yes, bounded below by the shared TTL.)
4. Is loading remote images directly an accepted privacy trade-off, or should
   an opt-out/proxy be planned?
5. Should the committed binary be purged from Git history (requires a force
   push and re-cloning), or only removed going forward?
