# Pulse — project architecture and design

**Operator guides:** [English](../README.md) · [Deutsch](../README.de.md) · [Русский](../README.ru.md)

This document is the consolidated project reference for Pulse. It records the product scope, architecture, data and security model, reader behavior, refresh design, localization, and implementation decisions. Commands for local operation, production deployment, backup, and shutdown are maintained in the linked README editions.

## Product scope

Pulse is a self-hosted, multi-user browser application for reading RSS and Atom feeds. Readers can subscribe to feeds, edit their subscription name, URL, and folder, organize feeds in personal folders, search articles, mark articles read or unread, save articles for later, and open article content in the reader. Administrators manage accounts, roles, and refresh settings.

The application is designed for a local server and can be exposed through Nginx. It does not depend on a hosted application service.

## Architecture and deployment

The project uses React and TypeScript for the browser application, a Go API using Chi, PostgreSQL for persistence, and Nginx to serve the frontend and proxy API requests. Docker Compose is split into independent frontend, backend, and database files for local development, plus a production Compose definition.

The local Compose groups share the external Docker network `rss-reader-net`. Frontend and backend source directories are mounted into their containers, and PostgreSQL data is stored in a host-mounted directory. In local development, the frontend container runs Vite's build watch mode and automatically refreshes `frontend/dist/` after source changes; Nginx serves the latest build and the browser page can be refreshed to view it. Nginx also proxies `/api/` to the Go API. The browser calls the API through relative same-origin paths.

Project-scoped agent skills are stored in `.agents/skills/` and included in the repository. `skills-lock.json` records upstream sources for externally maintained skills. A new developer opening the cloned project root in Codex gets vendored skills without a global installation. Run `bash scripts/sync-skills.sh install` to restore lock-tracked skills if needed, or `bash scripts/sync-skills.sh update` to refresh them. The script restores the project-specific handoff rules after either operation. Current Superpowers workflow skills are vendored from `obra/superpowers-skills`; `diagnosing-superpowers` remains as a project skill because upstream no longer publishes it. The legacy `using-superpowers` name is retained alongside the current `using-skills` entry.

The frontend is a static React build. The backend owns authentication, authorization, input validation, RSS/Atom fetching and parsing, refresh coordination, and database access. PostgreSQL migrations are applied by the API at startup.

## Reader experience and interaction

The reader uses a persistent navigation sidebar and a centered article feed. The article list and selected story form a two-panel reading workspace on wide screens. On narrow screens the reading flow becomes sequential and navigation can open as a drawer.

Feed sources are shown as children of their folders in a collapsible sidebar tree. Empty folders are hidden from the tree but remain available in the folder manager and source folder picker. Sources without a folder appear under a virtual “No folder” directory. The folder settings control opens a manager where readers can rename, recolor, and delete folders. Deleting a populated folder requires choosing whether to remove those subscriptions from the reader's library or move them to another folder. Removing subscriptions also clears that user's article state for those sources; canonical sources, shared articles, and other users' subscriptions remain intact. The unread marker in article metadata is vertically aligned with the source badge at desktop and mobile sizes.

A normal left click on an article headline opens the story locally. The headline remains a real link to the original article: middle-click, Ctrl/Cmd-click, and other modified browser navigation continue to open the source URL. The reader drawer can be dismissed with Escape, its close button, or a click on the backdrop. It traps keyboard focus while open and restores focus when closed.

The article feed loads 20 articles at a time. As the reader approaches the end of the loaded list, the frontend requests the next page from the API and appends it; a retry control appears if a page request fails.

Article titles, feed names, summaries, folder names, and source content are treated as feed or user data and are not translated. Article summary HTML is not injected into the application. The frontend extracts only safe HTTP(S) image URLs, resolves relative image paths against the article URL, and renders images as normal image elements. Arbitrary source HTML, scripts, and styles are never executed.

The interface supports light and dark themes. The first visit follows the system theme preference; an explicit choice is stored in browser local storage and applied before the application mounts to prevent a flash of the wrong palette. Theme styling uses semantic CSS variables, keyboard-visible focus, responsive layouts, and reduced-motion support.

The add-feed folder picker renders its options in a viewport-positioned listbox so the menu remains visible on narrow screens and can be checked in browser screenshots.

Application and form errors appear in a dismissible, screen-reader-announced toast above the current view. Errors remain visible until dismissed.

## Accounts, roles, and authorization

There are two roles:

| Capability | Administrator | Reader |
| --- | --- | --- |
| Read and update articles in the user's own library | Yes | Yes |
| Create personal folders and feed subscriptions | Yes | Yes |
| Create accounts and assign roles | Yes | No |
| Change global and per-user refresh settings | Yes | No |
| Read another user's library | No | No |

An administrator's reading library is personal; administrator status does not grant access to other users' folders, subscriptions, or article state. Public registration and account deletion are not available. The last Administrator cannot be demoted.

On an empty database, the API bootstraps the first Administrator from `INITIAL_ADMIN_EMAIL` and `INITIAL_ADMIN_PASSWORD`. Both are required for that first startup only. Passwords are stored as Argon2id hashes. Login creates a cryptographically random server-side session; the browser receives only an opaque HttpOnly cookie with `SameSite=Strict`. Sessions expire after 14 days and do not slide on activity. Password changes require the current password, revoke the user's other sessions, and preserve the current session. Mutating API requests are checked for same-origin behavior in addition to cookie protections.

The health endpoint is public. Library and administrative endpoints require authentication, and administrative endpoints additionally require the Administrator role. Authorization and ownership checks are enforced by the API and database queries, not only by hiding controls in the frontend.

## Data model and shared subscriptions

A feed source is a canonical shared entity identified by a normalized URL. Normalization lowercases scheme and hostname, removes default ports and URL fragments, and preserves the path and query. Distinct paths or query strings remain distinct sources.

One source owns one set of RSS/Atom articles. A user subscription links a user to a source and optionally to one of that user's folders. A source can have many user subscriptions, and one user can subscribe to many sources. Folder names are unique per owner, not globally. Each personal folder stores a color from the supported palette or a custom six-digit HEX value; readers can rename and recolor folders without changing their feed assignments. The read and saved flags live in per-user article state, so all subscribers see the same refreshed article data while retaining private reading progress.

Each user may subscribe to at most 100 feeds. The API checks this limit before fetching a newly added source and enforces it again transactionally when creating the personal subscription, so concurrent requests cannot exceed the cap. Administrators do not need access to users' personal feed lists to apply this limit.

New feed URLs that are not already in `feed_sources` are stored as pending approval requests only; Pulse does not resolve, fetch, parse, or persist feed content before an Administrator approves the URL. The approval queue exposes the submitted URL, requester, and selected folder, but not the requester's other subscriptions or article data. Approval creates the shared source and the requester's subscription; background refresh can fetch it afterward, and future subscriptions to that canonical source reuse the approved source. Rejection deletes the request. Changing a subscription to a not-yet-approved URL follows the same queue and leaves the current subscription in place until approval. Sources already present before this migration remain usable to preserve existing libraries.

Feed name overrides and folder assignments belong to the user subscription. Editing a name or folder affects only that user's library. Changing a subscription URL moves only that user's subscription to an already approved source; a new URL must first be approved, without changing the previous source or its other subscribers. A user cannot add the same canonical source twice.

The legacy single-user library is migrated to the first bootstrapped Administrator. If source URL normalization merges duplicate feeds, duplicate article identifiers are consolidated. The migration preserves saved state if any duplicate was saved, unread state if any duplicate was unread, and the latest publication timestamp for merged article content. Existing user-created names and content are not translated or rewritten by localization changes.

## Feed refresh and concurrency

Refresh work uses a bounded Go worker pool. `RSS_REFRESH_CONCURRENCY` configures the number of concurrent unique sources; the default is 4, values are limited to 1–32, and invalid values fall back safely. Each feed refresh retains its own timeout and error persistence. A failure for one source does not prevent other queued sources from being processed. The refresh controller prevents overlapping global cycles and preserves its active/pending request behavior.

A database-backed source lease prevents concurrent requests for the same canonical source, including across API processes. The lease has an expiry so a crashed worker can be retried, and a unique lease token fences stale workers from overwriting data after another worker takes ownership. Failed refreshes do not advance the successful-refresh TTL.

The shared source TTL defaults to 15 minutes and accepts values from 5 to 1,440 minutes. A successful fetch updates the canonical source once and makes new articles visible to all subscribers. A subscription to a source refreshed within its TTL reuses the existing data rather than issuing another HTTP request. Manual refresh is scoped to the signed-in user's subscriptions and observes the same source lease and TTL.

The background scheduler currently runs on its existing 15-minute cadence. Global scheduler and per-user refresh intervals are persisted and editable in the administration interface, but those values do not yet change due-source selection or scheduler timing. Connecting the saved intervals to scheduler behavior is a deferred implementation task. The shared-source TTL is active now.

Only public HTTP/HTTPS feed URLs are accepted. Loopback, private, and local network targets are blocked to prevent a feed request from reaching internal services. The API stores article metadata and summaries; the full article remains on the source website.

## Administrative settings

Administrators can create users with an email address, initial password, and either supported role. The password is presented only during account creation and must be shared separately. Administrators can edit the global scheduler default, a nullable per-user interval override, and the shared-source TTL. A user with no explicit interval inherits the global value; an explicit override is unaffected by changes to the default.

The scheduler and user interval controls are intentionally labeled as saved but inactive until scheduler integration is implemented. The shared TTL is labeled active because it currently gates source refreshes.

## Internationalization

The user interface supports English (`en`), German (`de`), and Russian (`ru`). English is the source language and default for a new browser profile. Language selection is available on both the sign-in screen and the reader toolbar and is stored in browser local storage under `pulse.language`. A missing or invalid saved language resolves to English.

Translation files are plain UTF-8 JSON catalogs in `frontend/src/locales/`. Their keys are flat dotted strings. `i18next` and `react-i18next` provide React integration, English fallback, interpolation, and locale-specific plural rules. The selected locale updates the document `lang`, page title, dates, relative times, and translated interface text. Feed-provided content remains unchanged.

To add a language, copy `frontend/src/locales/en.json`, translate its values, register the language code and catalog in `frontend/src/i18n.ts`, and add the language's native display name in `frontend/src/LanguageSelector.tsx`. Keep all application source strings, backend messages, and non-localized project documentation in English. API errors include a stable dotted code and an English fallback; the frontend looks up `apiErrors.<code>` in the active catalog and uses the fallback for unknown codes.

## API surface

The API is served under `/api/`. It includes a public health endpoint; authentication and profile operations; folder, feed, article, and refresh operations; and administrator-only account and settings operations. The principal route groups are:

- `/api/auth/login`, `/api/auth/logout`, `/api/auth/me`, `/api/auth/password`, and `/api/auth/profile` for sessions and profile management.
- `/api/folders` and `/api/feeds` for the signed-in user's library, including `PATCH /api/feeds/{id}` for personal subscription edits and `DELETE /api/folders/{id}` for folder removal. The delete body uses `{"action":"delete_sources"}` to remove the user's subscriptions and their article state, or `{"action":"move_sources","targetFolderId":<id>}` to move subscriptions into another folder owned by the same user. The folder deletion and subscription changes occur in one transaction; shared source and article records are retained.
- `/api/articles` and `/api/articles/{id}` for filtered article listing and per-user read/saved updates.
- `/api/refresh` for refresh control and status.
- `/api/admin/users`, `/api/admin/users/{id}`, `/api/admin/settings`, and `/api/admin/feed-requests` (including approve and reject actions) for administrator operations.
- `/api/health` for service health.

API errors use an object with a stable `code` and an English `error` message. Existing HTTP status codes communicate request outcome; localized text is resolved in the frontend.

## Project structure

- `frontend/src/` — React/TypeScript application, locale catalogs, and styles.
- `frontend/test/` — frontend unit and browser tests, kept in a parallel component-oriented structure.
- `backend/cmd/api/` — API entry point and configuration.
- `backend/internal/httpapi/` — HTTP routing, authentication, authorization, and handlers.
- `backend/internal/feeds/` — feed URL validation, fetching, and RSS/Atom parsing.
- `backend/internal/store/` — PostgreSQL persistence, account/library operations, and migrations.
- `backend/internal/syncer/` — scheduled and manual refresh coordination and worker pool.
- `backend/test/` — backend test files organized by component.
- `data/` — bind-mounted PostgreSQL data for local development.
- `compose.frontend.yml`, `compose.backend.yml`, and `compose.database.yml` — separate local Compose groups.
- `compose.production.yml` — production images and deployment definition.
- `scripts/update-production.sh` — production update helper.

## Database migration file design

The initial schema and historical upgrades are retained in the consolidated file `backend/internal/store/migrations/001_pulse.sql`. Its named phases preserve the bootstrap boundary and recognize the former migration names (`001_initial.sql` through `005_feed_customization.sql`) so existing databases do not repeat work.

New schema changes are added as separately numbered SQL files. The folder color change is `backend/internal/store/migrations/006_folder_customization.sql`. After the historical phases, startup discovers and applies these files in filename order, each transactionally under the migration advisory lock, and records the filename in `schema_migrations`; later startups skip files already recorded there. To support databases that ran an earlier development build, the runner also recognizes the former marker `001_pulse.sql:folder_customization` as equivalent to `006_folder_customization.sql`.

## Deferred work and explicit non-goals

- Connect persisted global and per-user refresh intervals to due-source selection and scheduler timing.
- Account deletion, self-registration, email invitations, verification, password-reset email, and social login.
- Cross-user library sharing and administrator access to user article content.
- Arbitrary HTML rendering of feed content.
- Translating RSS/user-created content or synchronizing language preferences between devices.
- Distributed refresh scheduling across independent backend deployments.

## Operational instructions

Use the language-specific README guides for requirements, local startup, Docker Compose build commands, production deployment, updates, backups, logs, and shutdown:

- [English operator guide](../README.md)
- [German operator guide](../README.de.md)
- [Russian operator guide](../README.ru.md)
