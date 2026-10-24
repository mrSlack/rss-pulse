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
