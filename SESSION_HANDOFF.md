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
