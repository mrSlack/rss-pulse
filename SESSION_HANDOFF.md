# Session handoff — 2026-11-01 (round 2: search, sources, rate limit, PWA)

- **State:** merged into `master` via `improve/integration`: `improve/search`, `improve/sources-and-requests`, `improve/persistent-rate-limit`, `improve/frontend-pwa`, plus a Vitest async-timeout setup (`frontend/test/setup.ts`) for load-related flakiness. Not deployed to production yet.
- **Changes:** migration 016 reduced to `pg_trgm`; 018 adds nullable `articles.search_key` filled in the background (`store.ArticleSearchUpgrade`, marker `018_article_search_key.sql:background`) with a concurrently built trigram index — no startup downtime; phrase search of common words ~4–14× faster; deploy wait back to 180 s / health start period 70 s. 019 + maintenance delete sources without subscribers after `UNSUBSCRIBED_SOURCE_RETENTION_DAYS` (30). Admin feed requests use keyset paging (`after`/`nextCursor`, `offset` removed). 020 moves sign-in limits to PostgreSQL (`login_attempts`, HMAC keys, advisory locks; fails closed). Frontend: lazy chunks + vendor chunk, de/ru catalogs lazy, PWA (manifest, `sw.js`, offline reading of cached lists, caches cleared on sign-out/401/user change, reload prompt), CSP `worker-src`/`manifest-src`; subscription-limit E2E 96 s → 8 s.
- **Verified on merged tree:** gofmt/vet/tidy clean, `REQUIRE_INTEGRATION=1 go test ./...` ok; lint, typecheck, 91/91 unit tests, build, `npm audit --omit=dev` 0; Playwright 22/22; compose configs and shellcheck clean. Upgrade rehearsal: old master (010) with 50k articles → new production image: API ready in 0.3 s, background search step done in ~4 s, data and unread counts intact, searches return results.
- **Known:** search key trick needs a UTF-8 database with a non-`C` locale for phrase speed (official image default is fine); backfill doubles `articles` table size until VACUUM; offline start remembers last account in localStorage (`pulse.offlineUser`); limiter rows are swept on sign-in only. CI still deferred.
- **Next:** on the production host: check `SHOW lc_ctype`, run `scripts/update-production.sh` (it backs up first), watch logs for the background search step.

---

# Session handoff — 2026-11-01 (code review fixes, merge to master)

- **State:** `improvements` reviewed as a whole (`master...improvements`); 9 findings confirmed with failing tests and fixed on `review-fix/{rate-limit,parsers-opml,article-feed,feed-requests}`, merged into `improvements`, then `improvements` merged into `master` (not pushed).
- **Fixes:** sign-in limiter no longer evicts locked/near-limit keys and fails closed when full of locks; RFC 822 dates with 2-digit year/no seconds parse again; OPML parser tracks wrapper elements; OPML import/export use count-free queries; article paging ignores stale load-more after a view change; mark-all-read is bounded by `latestArticleId` (`upToId`) instead of publish date only; repeated feed requests update the requester's intent; `GET /api/admin/feed-requests` now returns `{requests, total}` with `offset` paging (breaking for external clients) and the admin UI shows the true total with "Load more"; dead `Control.IsRunning`/`DiscardEmptySource` removed.
- **Verified on merged tree:** gofmt/vet/tidy clean, `REQUIRE_INTEGRATION=1 go test ./...` ok; frontend lint, typecheck, unit tests, build ok; Playwright 19/19; compose configs valid.
- **Known/remaining:** same as the stage 2–9 entry below (CI deferred, no staticcheck/govulncheck/race run, slow common-word phrase search). Mark-read ID bound has a tiny race with in-flight ingest transactions.
- **Next:** rebuild containers; back up production before upgrading (migration 016 rewrites all articles).

---

# Session handoff — 2026-11-01 (roadmap stages 2–9)

- **State:** all roadmap stages from `docs/2026-11-01-project-review-and-roadmap.md` are implemented. Each stage has its own branch (`stage-2/sign-in-protection` … `stage-9/operations`); all are merged with integration fixes into `improvements` (stacked on `stage-1/quick-fixes` and `docs/project-review-plan`). Nothing is pushed or merged into `master`.
- **Verified on `improvements` (final tree):** `gofmt -l` empty, `go vet`, `go mod tidy -diff` clean, `REQUIRE_INTEGRATION=1 go test -count=1 ./...` all packages ok; `npm run lint` 0 problems, 66/66 unit tests, typecheck and build ok, `npm audit --omit=dev` 0; Playwright 18/18 on `compose.e2e.yml`; all compose files validate; shellcheck clean; production images build; nginx headers/caching/gzip checked with curl; fresh-DB start applies 001–017; upgrade from a `master`-migrated database (010) with data to 017 succeeds with data intact.
- **Integration fixes made by the coordinator:** seed test signature after cursor paging; scheduler ignores disabled accounts; dead `ErrFeedRequestPending` removed; production update waits up to 900 s (`DEPLOY_WAIT_TIMEOUT`) and API health start period 900 s for long migration 016; admin users baseline screenshot updated; Nginx forwards/logs `request_id` without query strings; OpenAPI entries for mark-read and OPML routes.
- **Not done / known:** CI deferred by owner; staticcheck and govulncheck not run (staticcheck `@latest` blocked by permissions, pinned govulncheck crashes on Go 1.27); race detector not run (no cgo in alpine image); slow phrase searches of very common words; running dev containers were not rebuilt. `frontend/dist` and `frontend/test-results` may be root-owned from containers.
- **Next:** review and merge `improvements` into `master` (suggest reviewing per stage branch), rebuild containers, back up production before the first upgrade (migration 016 rewrites all articles).

---

# Session handoff — 2026-11-01 (stage 1: quick fixes)

- **Done (branch `stage-1/quick-fixes`, stacked on `docs/project-review-plan`):** untracked `backend/api` binary and ignored it (history not rewritten, per decision); dev API no longer runs `go mod tidy` at start and the dev image copies `go.sum`; `AGENTS.md` Go test command now sets `TEST_DATABASE_URL` and `REQUIRE_INTEGRATION=1` (skips become failures); REL-1 robust feed date parsing; SEC-2 dummy-hash Argon2id work for unknown sign-in e-mails; SEC-4 `netip`-based special-purpose range blocklist with NAT64/6to4 unwrapping.
- **Verified:** against a temporary tmpfs PostgreSQL 18 container: `gofmt -l` clean, `go vet` ok, `go mod tidy -diff` clean, `REQUIRE_INTEGRATION=1 go test -count=1 ./...` all packages ok; `REQUIRE_INTEGRATION=1` without a DSN fails as intended; dev backend image builds; `compose.backend.yml config` ok. Frontend untouched; Playwright not run. Running app containers were not rebuilt.
- **Next:** stage 2 (sign-in rate limiting, Nginx security headers/CSP, pending-request cap, ESLint) per `docs/2026-11-01-project-review-and-roadmap.md`.

---

# Session handoff — 2026-11-01 (project review and roadmap)

- **Done:** Full project review and phased improvement plan in `docs/2026-11-01-project-review-and-roadmap.md` (findings with IDs, roadmap Phases 0–6, open questions). Branch `docs/project-review-plan`; no application code changed.
- **Verified:** REL-1 reproduced in `golang:1.27.3` — `Mon, 2 Jan 2006 15:04:05 GMT` and zone-less ISO dates fail all parser layouts. Other findings come from code reading and are not yet covered by tests; no test suites were run (docs-only change).
- **Next:** Get answers to the document's open questions (§4), then start Milestone A (Phase 0 hygiene and local checks, Phase 1 security, tasks 2.1–2.2), writing a regression test first for each finding.
- **Decision:** CI is out of scope; add it only when the owner explicitly requests it.

---

# Session handoff — 2026-10-26 (feed article quarantine)

- **Done:** Added stop-word article quarantine with complete feed payloads, reversible Administrator decisions, and promotion to the shared source article table at the original publication time. Visibility remains limited to users subscribed to that source. Added moderation API/UI, translations, migration history, and an admin Playwright screenshot.
- **Verified:** Merged into `master` through `726ff3d`. Go suite passed; frontend unit tests 41/41; production build passed; isolated E2E suite 14/14 serially; `git diff --check` passed. Admin screenshot is `frontend/test/e2e/admin-workspace.spec.ts-snapshots/blocked-article-moderation-linux.png`.
- **Decision:** Rejected items remain available for later approval. Historical log rows without retained content/publication time remain visible but non-moderatable; the migration does not invent dates for them.
- **Remaining / environment:** Running application containers were not rebuilt/restarted after merge. Migrations `009_feed_article_quarantine.sql` and `010_quarantine_eligibility.sql` apply when the API starts. Feature worktree is preserved at `/tmp/rss-pulse-feed-quarantine` on `codex/feed-article-quarantine`; its last feature commit is `13951a8`, while the merged `master` also has the build fix `726ff3d`.
- **References:** `docs/PROJECT.md`, `docs/superpowers/specs/2026-10-26-feed-article-quarantine-design.md`, `docs/superpowers/plans/2026-10-26-feed-article-quarantine.md`, final review report in `/tmp/rss-pulse-feed-quarantine/.superpowers/sdd/2026-10-26-feed-article-quarantine/final-fix-report.md`.

---

# Session handoff — 2026-10-26 (E2E accounts and feed filtering)

- **Done:** Added stop-word filtering for incoming feed articles, an administrator dictionary and blocked-article log, and split admin sections behind a submenu. Added an isolated browser-test Compose stack with admin and reader accounts, global reader seeding, real login/workflow coverage, and browser checks for HTTP/network/JS/layout errors. Added the missing SVG favicon discovered by the browser run.
- **Verified:** Full Playwright suite on the isolated E2E stack: 14 passed. Real-account scenario rerun: 1 passed. Frontend production build, E2E Compose config, and `git diff --check` passed. Test credentials and the run command are in `frontend/test/e2e/README.md`.
- **Environment:** `compose.e2e.yml` uses a separate PostgreSQL container with temporary storage and no published ports or development database mount. Its services may still be running. Stop them with `docker compose -f compose.e2e.yml down`; this discards the test database.
- **Remaining / unverified:** All work is uncommitted alongside earlier feed-approval changes. The Go suite was not rerun during this E2E-account work. Review `git status` and the diff before committing; preserve the existing approval work and all tests under dedicated test directories.
- **References:** `frontend/test/e2e/README.md`, `frontend/test/e2e/real-accounts.spec.ts`, `frontend/test/e2e/admin-workspace.spec.ts`, `compose.e2e.yml`, `docs/PROJECT.md`, current working-tree diff.

---

# Session handoff — 2026-10-26

- **Done:** Added pre-fetch approval for new feed URLs. Readers submit requests; Administrators can approve or reject them. Approved feeds are subscribed for the requester and enter shared refresh. URL changes wait for approval and preserve the current subscription. Redirects stay on the approved host and port, with HTTPS downgrade blocked.
- **Verified:** Full Go suite passed; frontend unit tests passed (34); frontend production build passed; all 12 Playwright tests passed serially, including the 100-subscription scenario and new approval-request browser test.
- **Decisions and limits:** The approval queue shows URL, requester, and selected folder, not the rest of the user's library. Existing `feed_sources` remain usable for compatibility and were not reviewed or purged; content from approved feeds is not scanned and may change over time.
- **Remaining:** Backend migration `007_feed_approval.sql` applies on API startup. Running application services were not rebuilt or restarted. Review any existing sources separately if they must also require explicit approval.
- **State:** Changes remain uncommitted. The worktree already contained unrelated edits from earlier tasks; inspect `git status` and the diff before committing. Suggested subject: `Add administrator approval for new feed sources`.
- **References:** `docs/PROJECT.md`, `backend/internal/store/migrations/007_feed_approval.sql`, current working-tree diff.

---

# Session handoff — 2026-10-25

- **Done:** fixed backend acceptance of JSON `null` (could clear a profile) and frontend crashes on malformed API errors; added regression tests. Added EditorConfig, Prettier, VS Code settings, and English documentation.
- **Verified:** Go tests including PostgreSQL integration, `go vet`, 33 frontend tests, and frontend build passed. E2E not run this session; current runtime health is unverified. Temporary test database removed.
- **Decisions:** UI unchanged per user request. Use `gofmt` for Go and Prettier for frontend to keep IDE formatting consistent; no bulk reformatting. Test with isolated PostgreSQL to protect application data.
- **Remaining:** scheduler checks every 15 minutes despite a minimum TTL of 5 minutes; user intervals do not affect scheduling. Shutdown lease cleanup has no timeout. These are follow-up opportunities, not assigned work.
- **State:** assistant did not commit or push; inspect the working tree before continuing. No implementation task pending.
- **References:** `docs/2026-10-25-reliability-review.md`, `docs/code-formatting.md`, `AGENTS.md`.
- **Suggested skills:** Verification Before Completion; Systematic Debugging and TDD for further fixes.

---

# Pulse RSS reader — project handoff

## Project overview

Pulse is a self-hosted, multi-user RSS and Atom reader. The browser application is built with React, TypeScript, and Vite; the API uses Go and Chi; PostgreSQL stores application data; Nginx serves the frontend and proxies API requests.

Local development uses `compose.frontend.yml`, `compose.backend.yml`, and `compose.database.yml`. Production deployment uses `compose.production.yml`. Operator instructions are in `README.md`, `README.de.md`, and `README.ru.md`. The architecture and product decisions are documented in `docs/PROJECT.md`; repository workflows are in `AGENTS.md`.

## Product and architecture

- Readers manage personal feed subscriptions and folders, search and browse articles, mark articles read or unread, save articles, and open stories in an in-app reader.
- Feed sources and articles are canonical shared records. Subscriptions, folders, article read state, and saved state are private to each user's library.
- Roles are `Administrator` and `Reader`. Administrators manage accounts and system settings; they cannot inspect other users' libraries. Registration and account deletion are unavailable.
- Authentication uses Argon2id password hashes and opaque server-side sessions in protected cookies. The first administrator is bootstrapped from environment settings.
- Feed refresh uses a bounded worker pool, database-backed source leases, and a shared-source TTL. Only public HTTP and HTTPS feed URLs are accepted.
- The frontend supports light and dark themes and English, Russian, and German. Interface text is in `frontend/src/locales/`; user and feed content is not translated.

## Reader behavior and folder management

- The article list loads pages of twenty and requests more as the reader approaches the end. Failed page requests have a retry control.
- Application and form errors appear in a dismissible, announced toast.
- Sources appear as children in a collapsible sidebar tree. Empty folders are hidden there but remain available in the folder manager and source folder picker. Unfiled sources appear under a virtual “No folder” group.
- A settings control opens folder management. Readers can rename or delete folders. Deleting a folder with sources requires choosing to remove the subscriptions from the current user's library or move them to another folder. Removing subscriptions also clears that user's article state; shared articles and other users' subscriptions remain unchanged.
- Folder deletion is user-scoped and transactional. The API is `DELETE /api/folders/{id}` with either `{"action":"delete_sources"}` or `{"action":"move_sources","targetFolderId":<id>}`.
- Folder names and preset/custom colors are user-managed. Color selection is available when creating or editing a folder; folder color markers are not shown in the sidebar tree.
- The unread marker in each article metadata row is centered against the source badge. A Playwright geometry assertion covers desktop and mobile layouts.

## Development and verification

Run commands from the repository root. The frontend and backend source trees are mounted into their local containers; PostgreSQL data is persisted under `data/postgres/` and must remain local.

- Frontend build: `docker compose -f compose.frontend.yml exec frontend npm run build`
- Frontend unit tests: `docker compose -f compose.frontend.yml exec frontend npm run test -- --run`
- Playwright browser and visual tests: `docker compose -f compose.frontend.yml exec frontend npm run test:e2e`
- Refresh Playwright screenshots after intentional visual changes: `docker compose -f compose.frontend.yml exec frontend npm run test:e2e:update`
- Backend tests: `docker compose -f compose.backend.yml run --rm -e TEST_DATABASE_URL=postgres://pulse:pulse-local-only@db:5432/pulse?sslmode=disable api go test ./...`
- Rebuild/restart the backend: `docker compose -f compose.backend.yml up -d --build api`
- Inspect Compose services: `docker compose -f compose.database.yml ps`, `docker compose -f compose.backend.yml ps`, and `docker compose -f compose.frontend.yml ps`.

The latest frontend build, ten Playwright tests, and backend Go test suite passed. The last container inspection showed PostgreSQL, API, frontend, and Nginx healthy. Treat these as the most recently verified state; check services again before relying on their current runtime status.

## Continuation notes

- The worktree contains uncommitted changes from several related tasks. Review `git status` and the diff before changing or reverting files; do not assume all changes belong to one feature.
- Keep migrations in separately numbered SQL files under `backend/internal/store/migrations/` so the migration runner can track each applied file.
- Do not expose values from `.env` or `.env.production`. Keep SQL parameterized and preserve user ownership checks on library operations.
- For a new task, read `AGENTS.md` and the relevant sections of `docs/PROJECT.md`, then verify the affected behavior with the documented build or test command.

## Suggested skills

- `using-skills` at the start of a new session.
- `systematic-debugging` when investigating a reproducible failure.
- `verification-before-completion` before reporting implementation results.
- `frontend-design` or `ui-ux-pro-max` for substantial UI work.
