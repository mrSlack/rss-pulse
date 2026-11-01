# Pulse — project architecture and design

**Operator guides:** [English](../README.md) · [Deutsch](../README.de.md) · [Русский](../README.ru.md)

This document is the consolidated project reference for Pulse. It records the product scope, architecture, data and security model, reader behavior, refresh design, localization, and implementation decisions. Commands for local operation, production deployment, backup, and shutdown are maintained in the linked README editions.

## Product scope

Pulse is a self-hosted, multi-user browser application for reading RSS and Atom feeds. Readers can subscribe to feeds, edit their subscription name, URL, and folder, organize feeds in personal folders, search articles, mark articles read or unread, save articles for later, and open article content in the reader. Administrators manage accounts, roles, and refresh settings.

The application is designed for a local server and can be exposed through Nginx. It does not depend on a hosted application service.

## Architecture and deployment

The project uses React and TypeScript for the browser application, a Go API using Chi, PostgreSQL for persistence, and Nginx to serve the frontend and proxy API requests. Docker Compose is split into independent frontend, backend, and database files for local development, plus a production Compose definition.

The local Compose groups share the external Docker network `rss-reader-net`. Frontend and backend source directories are mounted into their containers, and PostgreSQL data is stored in a host-mounted directory. In local development, the frontend container runs Vite's build watch mode and automatically refreshes `frontend/dist/` after source changes; Nginx serves the latest build and the browser page can be refreshed to view it. Nginx also proxies `/api/` to the Go API. The browser calls the API through relative same-origin paths.

Project-scoped agent skills live in `.agents/skills/`, which `.gitignore` excludes: the skills are not stored in the repository, and a fresh clone has none. `skills-lock.json` records the externally maintained skills and their upstream sources. Run `bash scripts/sync-skills.sh install` to restore the lock-tracked skills, or `bash scripts/sync-skills.sh update` to refresh them; the script reapplies the project-specific handoff rules after either operation. Skills that are not in the lock file, such as the Superpowers workflow skills from `obra/superpowers-skills` and `diagnosing-superpowers`, exist only in local copies and must be installed into `.agents/skills/` by hand.

The frontend is a static React build. The backend owns authentication, authorization, input validation, RSS/Atom fetching and parsing, refresh coordination, and database access. PostgreSQL migrations are applied by the API at startup.

## Logging and health checks

The API logs through Go's `log/slog`; `cmd/api` builds the logger from `LOG_FORMAT` (`text` by default, `json` in `compose.production.yml`) and `LOG_LEVEL` (`debug`, `info` by default, `warn`, `error`), rejects invalid values at startup, and installs it as the default logger, so the syncer, the maintenance job, and `net/http` server errors write through the same handler.

The router's middleware chain is `requestID`, `accessLog`, `instrument` (request metrics, only when metrics are enabled), chi's `Recoverer`, and `sameOrigin`. `requestID` reuses an incoming `X-Request-ID` only when it matches `^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$` and otherwise generates 16 random bytes in hex; it stores the ID where chi's `middleware.GetReqID` finds it and returns it in the `X-Request-ID` response header. `accessLog` writes one `http request` record per request with `method`, `path` (`r.URL.Path`; the query string is never logged because it can contain search terms), `status`, `bytes`, `duration_ms`, `request_id`, and `user_id`. The user is only known inside `authenticate`, which runs later in route-specific middleware, so `accessLog` places a mutable per-request holder in the context and `authenticate` records the user ID in it. `Recoverer` runs inside `accessLog` and `instrument`, so a recovered panic is logged and counted with status 500. Requests under `/api/health` are logged at debug level. The bundled Nginx forwards its own `$request_id` as `X-Request-ID` and writes it to its access log (`request_id=`), so proxy and API log lines can be matched; like the API, its log format records the path without the query string.

The syncer logs one `rss refresh cycle finished` record per scheduled cycle (`trigger=schedule`) or manual refresh (`trigger=user`, `user_id`) with `targets`, `refreshed`, `not_modified`, `failed`, `skipped` (not claimed or cancelled), and `duration_ms`; cycles without targets are logged at debug level. Individual fetch failures are logged at debug level because they are expected and shown per feed in the UI; claim, commit, and lease errors are logged as errors. The maintenance job logs its deletion counts.

`GET /api/health/live` answers `200 {"status":"ok"}` without touching the database (liveness). `GET /api/health/ready` pings PostgreSQL with a 2-second timeout and answers `503 system.database_unavailable` when that fails (readiness). `GET /api/health` is kept as an alias of the readiness check for existing monitors. The health checks of `compose.backend.yml`, `compose.production.yml`, and `compose.e2e.yml` call `/api/health/ready`. Prometheus metrics are served on a separate listener (see "Metrics").

## Metrics

The API exports Prometheus metrics when `METRICS_ADDR` is set (empty by default: metrics disabled, nothing is recorded and no listener is opened). `METRICS_ADDR` is a `host:port` or `:port` for a second HTTP listener, for example `:9090`; it must not use the API port 8080, and an invalid value stops the API at startup. That listener serves only `GET /metrics` (404 for every other path); the API router on port 8080 has no metrics route. The bundled Nginx proxies only `/api/` to `api:8080`, so metrics are never reachable through the public site. `compose.backend.yml` and `compose.production.yml` pass `METRICS_ADDR` through and declare port 9090 with `expose` only (no host port): a Prometheus container on the same Docker network (`rss-reader-net` locally, `pulse-production_default` in production) scrapes `api:9090`. `expose` does not restrict access; every container on that network can reach the port, so attach only trusted services, and publish a host port only bound to loopback if a host-level Prometheus needs it. The metrics contain no user data, but they reveal activity such as sign-in failures.

The implementation in `internal/metrics` uses `github.com/prometheus/client_golang` (pinned in `go.mod`). It adds seven small modules (client_model, common, procfs, perks, xxhash, goautoneg, protobuf) and brings the standard Go runtime and process collectors, correct histogram and text-format encoding, and `promhttp`; a hand-written exposition would have had to reimplement those. All methods of a nil `*metrics.Metrics` do nothing, so the syncer, the maintenance job, and the router run unchanged without metrics.

Every label has a small fixed set of values: `route` is the chi route pattern (for example `/api/feeds/{id}`; requests that match no route are `unmatched`), `method` is a standard HTTP method or `OTHER`, `status_class` is `2xx`…`5xx`, and `outcome` and `kind` are the values listed below. User IDs, e-mail addresses, URLs, and feed IDs are never labels; a test fails when a `pulse_` metric gains any other label name.

| Metric | Type | Meaning |
| --- | --- | --- |
| `pulse_http_requests_total{route,method,status_class}` | counter | API requests. |
| `pulse_http_request_duration_seconds{route,method,status_class}` | histogram | API request duration (default Prometheus buckets, 5 ms–10 s). |
| `pulse_feed_refresh_attempts_total{outcome}` | counter | Refreshes of a claimed source: `ok` (new content committed), `not_modified` (HTTP 304), `error` (fetch, claim, or commit failed). Sources claimed by another worker or not yet due, and refreshes canceled by shutdown, are not counted. |
| `pulse_feed_fetch_duration_seconds` | histogram | Feed download duration, successful or not (buckets 0.1–15 s; the fetch timeout is 15 s). |
| `pulse_feed_articles_inserted_total` | counter | New articles stored by refreshes (updates and merged duplicates are not counted). |
| `pulse_feed_articles_quarantined_total` | counter | Items newly held in quarantine by the stop-word filter; items already in quarantine are not counted again. Quarantine is per article, so it is not a refresh outcome. |
| `pulse_feed_sources_due` | gauge | Sources with an enabled subscriber whose refresh interval has elapsed (what the next scheduler cycle would pick up). Briefly non-zero between cycles is normal. |
| `pulse_feed_sources_overdue` | gauge | Due sources that have been due for more than 5 minutes: the scheduler is not keeping up. |
| `pulse_feed_oldest_overdue_seconds` | gauge | How long the longest-waiting due source has been due; a never-fetched source counts from its creation. 0 without due sources. |
| `pulse_feed_active_leases` | gauge | Sources being refreshed right now (unexpired lease). |
| `pulse_feed_manual_refresh_queue_length` | gauge | Manual "refresh now" requests waiting for the worker (the one being processed is not included). |
| `pulse_feed_requests_pending` | gauge | Feed requests waiting for an administrator (same count as the administration screen). |
| `pulse_quarantine_pending` | gauge | Quarantined articles without a decision. |
| `pulse_operational_stats_errors_total` | counter | Failed queries for the five database-derived gauges above. After a failure the last successful values are reported; before the first success the gauges are absent. |
| `pulse_signin_attempts_total{outcome}` | counter | `POST /api/auth/login` by outcome: `success` (session created), `failure` (malformed request, wrong credentials, or a disabled account), `rate_limited` (HTTP 429), `error` (database or session failure). |
| `pulse_maintenance_runs_total{outcome}` | counter | Retention maintenance runs, `success` or `failure` (any step failed). |
| `pulse_maintenance_last_run_duration_seconds` | gauge | Duration of the latest run. |
| `pulse_maintenance_last_success_timestamp_seconds` | gauge | Unix time of the latest run without failures; 0 until the first one (the first run starts one minute after start-up, then every 6 hours). |
| `pulse_maintenance_deleted_rows_total{kind}` | counter | Rows deleted by maintenance: `articles`, `quarantine`, `unsubscribed_sources`, `unsubscribed_source_articles`, `sessions`, `login_attempts`. |
| `pulse_search_upgrade_done` | gauge | 1 once the background article search upgrade has completed (immediately on later starts), else 0. |
| `pulse_search_upgrade_backfilled_articles_total` | counter | Articles whose search key the upgrade filled since start. The remaining number is not exported: counting rows without a key needs a full scan of `articles`. |
| `pulse_db_pool_acquired_connections`, `pulse_db_pool_idle_connections`, `pulse_db_pool_total_connections`, `pulse_db_pool_max_connections` | gauge | pgx pool state (`pgxpool.Stat`). |
| `pulse_db_pool_acquires_total`, `pulse_db_pool_acquire_duration_seconds_total` | counter | Successful acquisitions and their total time. |
| `pulse_db_pool_empty_acquires_total`, `pulse_db_pool_empty_acquire_wait_seconds_total` | counter | Acquisitions that had to wait for a connection, and the total wait. A steadily rising wait means `DB_MAX_CONNS` is too small for the load. |
| `pulse_db_pool_canceled_acquires_total` | counter | Acquisitions canceled while waiting. |
| `go_*`, `process_*`, `promhttp_metric_handler_*` | | Standard Go runtime, process, and scrape-handler collectors. |

The pool, queue-length, and database-derived gauges are read during a scrape. The database-derived ones (`sources_due` … `quarantine_pending`) come from one statement, `store.OperationalStats`, whose due-source part reuses the scheduler's `DueSources` policy and costs about as much as one scheduler cycle; the result is cached for 15 seconds, and concurrent scrapes share one query (3-second timeout), so frequent or parallel scrapes do not load the database.

Example Prometheus scrape configuration for a Prometheus container on the same Docker network:

```yaml
scrape_configs:
  - job_name: pulse
    scrape_interval: 30s
    static_configs:
      - targets: ["api:9090"]
```

Suggested alert rules (thresholds are starting points):

```yaml
groups:
  - name: pulse
    rules:
      - alert: PulseFeedRefreshErrorsHigh
        expr: |
          sum(rate(pulse_feed_refresh_attempts_total{outcome="error"}[30m]))
            / sum(rate(pulse_feed_refresh_attempts_total[30m])) > 0.5
        for: 30m
        annotations:
          summary: More than half of feed refreshes fail (network or DNS problem?)
      - alert: PulseRefreshBacklog
        expr: pulse_feed_oldest_overdue_seconds > 1800
        for: 15m
        annotations:
          summary: A feed source has waited more than 30 minutes past its schedule
      - alert: PulseMaintenanceStale
        expr: |
          time() - pulse_maintenance_last_success_timestamp_seconds > 86400
            and on() time() - process_start_time_seconds > 86400
        annotations:
          summary: Retention maintenance has not succeeded for 24 hours
      - alert: PulseSignInRateLimitedSpike
        expr: increase(pulse_signin_attempts_total{outcome="rate_limited"}[15m]) > 20
        annotations:
          summary: Many throttled sign-in attempts (password guessing?)
      - alert: PulseDatabasePoolWaiting
        expr: rate(pulse_db_pool_empty_acquire_wait_seconds_total[5m]) > 0.5
        for: 10m
        annotations:
          summary: Requests wait for database connections; consider raising DB_MAX_CONNS
```

## Reader experience and interaction

The reader uses a persistent navigation sidebar and a centered article feed. The article list and selected story form a two-panel reading workspace on wide screens. On narrow screens the reading flow becomes sequential and navigation can open as a drawer.

Feed sources are shown as children of their folders in a collapsible sidebar tree. Empty folders are hidden from the tree but remain available in the folder manager and source folder picker. Sources without a folder appear under a virtual “No folder” directory. The folder settings control opens a manager where readers can rename, recolor, and delete folders. Deleting a populated folder requires choosing whether to remove those subscriptions from the reader's library or move them to another folder. Removing subscriptions also clears that user's article state for those sources; canonical sources, shared articles, and other users' subscriptions remain intact. The unread marker in article metadata is vertically aligned with the source badge at desktop and mobile sizes.

A normal left click on an article headline opens the story locally. The headline remains a real link to the original article: middle-click, Ctrl/Cmd-click, and other modified browser navigation continue to open the source URL. The reader drawer can be dismissed with Escape, its close button, or a click on the backdrop. It traps keyboard focus while open and restores focus when closed.

The article feed loads 20 articles at a time, newest first (publication time, then article ID). As the reader approaches the end of the loaded list, the frontend requests the next page with the cursor returned by the previous page and appends it, skipping any article already loaded; a retry control appears if a page request fails. Cursor (keyset) paging continues strictly after the last loaded article, so articles read in the Unread view or new articles arriving between page loads neither skip nor repeat entries.

Search matches the query as a case-insensitive substring of the article title and the plain text of its summary: partial words match, Cyrillic and German text is lower-cased (`ß` and `ẞ` match each other, `ss` does not match `ß`), whitespace runs (and the character `ǂ`, which the search index uses as its word separator) count as one space, and `%`, `_`, and `\` are literal characters. HTML tag names, attribute values such as link targets, and entity spellings such as `&amp;` are not searchable; their decoded text is. See "Performance notes" for the index behind it.

Marking an article read or unread (including opening it) and saving or un-saving it update the loaded article in place and are rolled back if the request fails. They do not reload the article list, so loaded pages and the scroll position are kept; read changes refresh only the feed and folder unread counters. An article read in the Unread view or un-saved in the Read later view therefore stays visible until the list is next loaded (changing view, searching, or refreshing).

"Mark all read" in the article list toolbar marks every article of the current view (filter, feed or folder, search, and the Today start) as read on the server, including articles not loaded yet, but only articles that had been ingested when the list was loaded (the `latestArticleId` bound), so articles that arrived afterwards stay unread even when their publication date is older than the loaded ones. It asks for confirmation when more articles exist than are loaded or more than ten loaded articles are unread. The loaded list is updated in place and only the counters are reloaded.

Keyboard shortcuts work while no dialog is open, the administration view is closed, and focus is not in a text field, select, or editable element; keys pressed with Ctrl, Alt, or Meta are ignored. `j`/`k` open the next/previous article in the reader (loading the next page near the end), `o` (or Enter when no control has focus) opens the original in a new tab, `m` toggles read, `s` toggles saved, `r` refreshes, `/` focuses search (closing the reader first), and `?` opens a focus-trapped help dialog that Escape closes. The help dialog is also available from the profile menu.

The profile menu offers "Export subscriptions" (an OPML 2.0 download of the user's subscriptions with folders as outline groups and personal feed names) and "Import subscriptions" (an OPML upload). Imports follow the add-feed rules: URLs of sources that already exist are subscribed directly, other URLs become or join approval requests (the 10-pending-request cap applies), and the 100-feed limit applies per feed. The nearest enclosing outline without `xmlUrl` names the folder, matched case-insensitively or created (names longer than 60 characters, or empty, import without a folder). Feeds already followed and repeated URLs are skipped; invalid URLs and limit failures are listed in the result summary. Personal feed names in the file are not applied.

When any authenticated request returns 401 (session expired or revoked), the reader closes and the sign-in screen explains that the session expired. Pending article, library, and administration loads are cancelled when the view changes or the reader unmounts; cancelled requests never show an error.

Article titles, feed names, summaries, folder names, and source content are treated as feed or user data and are not translated. Article summary HTML is not injected into the application. The frontend extracts only safe HTTP(S) image URLs, resolves relative image paths against the article URL, and renders images as normal image elements. Arbitrary source HTML, scripts, and styles are never executed.

The interface supports light and dark themes. The first visit follows the system theme preference; an explicit choice is stored in browser local storage and applied before the application mounts to prevent a flash of the wrong palette. Theme styling uses semantic CSS variables, keyboard-visible focus, responsive layouts, and reduced-motion support.

The add-feed folder picker renders its options in a viewport-positioned listbox so the menu remains visible on narrow screens and can be checked in browser screenshots.

Application and form errors appear in a dismissible, screen-reader-announced toast above the current view. Errors remain visible until dismissed.

### Installation and offline reading

Pulse is an installable web app: `index.html` links `manifest.webmanifest` (name "Pulse", standalone display, the favicon SVG plus 192/512 px PNG and a maskable 512 px icon). Production builds register a service worker, `/sw.js`, built from `frontend/src/sw/sw.ts`; the Vite dev server registers none. Service workers need a secure context (HTTPS, or `localhost`); elsewhere Pulse works as before without offline support.

- **App shell.** On installation the worker precaches every file of the build (`index.html`, all hashed `/assets/` chunks including lazy ones, and the `public/` files). Hashed assets are served cache first; navigations and other files go to the network first and fall back to the precached copy when it fails.
- **Offline reading.** `GET /api/articles` (any query, including later pages), `GET /api/feeds` and `GET /api/folders` go to the network first; successful JSON responses (status 200, no `Cache-Control: no-store`) are stored in the cache `pulse-api-v1`, keyed by full URL, and returned when the network fails. Only the 60 most recently stored responses are kept. Read and saved changes, mark-all-read, and every other request still need the network and fail with the usual error toast.
- **Hanging connections.** When such a request has no answer after 4 seconds (`API_NETWORK_TIMEOUT_MS` in `src/sw/policy.ts`) and a stored copy exists, the worker returns the copy (a possibly older version of the same URL), so a hanging connection (captive portal, dead mobile link) shows saved articles. The network request keeps running and its response refreshes the cache; the generation counter (below) still keeps a response from before a cache clear out of it. Without a stored copy the worker keeps waiting for the network.
- **Stored copies are never shown as fresh.** A merely slow server also trips the 4-second limit, so every answer from the cache (on a network failure or on the time limit) carries the header `X-Pulse-Cache: stale` (`STALE_HEADER`). `src/api.ts` reports article, feed and folder responses to `src/staleData.ts`; while the last one was a stored copy and the browser is online, the status pill says "The server is not responding. Showing articles saved earlier." (`offline.stale`). When the network answer for a request already answered from the cache arrives, the worker posts a message to that page: `pulse-api-fresh` for a 2xx answer, after which the reader reloads the library and the article list once (late answers within 200 ms are combined) and the notice disappears with the fresh responses; `pulse-session-expired` for a 401, after which the page runs the normal expired-session flow (sign-in screen with "Your session has expired", offline data and remember choice deleted) that the hidden 401 would otherwise never have triggered. Other late errors change nothing; the notice stays. To keep that reload from getting the stored copy again, the worker drops the time limit for two minutes after a late answer (`SLOW_SERVER_WINDOW_MS`, `fallbackTimeout`): requests then wait for the network and use the stored copy only when it fails. A failed network request ends that window, so a connection that starts hanging later falls back again. The window lives in the worker's memory and restarts with it. Independently, `src/api.ts` gives every request a time limit of 40 seconds (`REQUEST_TIMEOUT_MS`), longer than nginx's `proxy_read_timeout` (30 s) and the API's write timeout (25 s), so it only ends a connection that hangs; the request then fails with "The server did not respond in time" (`apiErrors.timeout`, status 0 like a network failure). The session check `/api/auth/me` gives up after 15 seconds (`SESSION_CHECK_TIMEOUT_MS`), after which Pulse starts as it does offline. OPML imports have no limit, because their upload time depends on the file size and the link.
- **Never cached:** non-GET requests, `/api/auth/*`, `/api/admin/*`, `/api/opml`, `/api/refresh`, health checks, other API paths, other origins, and `/sw.js` itself.
- **Remember me on this device for offline use.** A checkbox on the sign-in form and a toggle under "This device" in the profile settings. The choice is stored in local storage under `pulse.offlineRemember` (`true`/`false`). Without a stored choice it is **on in the installed app** (display mode `standalone` or `window-controls-overlay`, or iOS `navigator.standalone`), because people install Pulse on their own devices to use it offline, and **off in a browser tab**, which may be a shared or public computer. Signing out deletes the choice, and so does every other end of the session (a 401 from any request or from the session check, also one the worker reports late, and the administrator ending their own sessions), so the next person signing in on that browser starts from the default. If the profile cannot store the choice (local storage blocked or full), the toggle shows the choice actually stored and an error message (`profile.rememberSaveFailed`).
  - **On:** the account is remembered (see "Offline start") and stored API responses are kept between visits until sign-out, a 401, or a different account.
  - **Off:** the account is never stored. The worker still stores API responses during the visit, so a connection that drops or hangs mid-visit falls back to them, but they are deleted at the next start of Pulse (before the session check), on sign-out, and on a 401. Browsers give no reliable event for "the last tab closed", so until the next start they remain in the browser's cache storage; a second tab opened meanwhile also deletes them (the first tab simply loads them again).
  - **Signing in with it off, or turning it off in the profile,** deletes the remembered account and all stored API responses at once; turning it on in the profile remembers the signed-in account at once.
- **Offline start.** The session check `/api/auth/me` is never cached. When it fails for lack of a network (or hangs, see above), Pulse opens the reader for the account last signed in on this device if the device is remembered, stored in local storage under `pulse.offlineUser` (id, e-mail, role, names); otherwise it shows the sign-in form. The first request that reaches the server checks the session again; a 401 ends it as usual.
- **Privacy on shared devices.** The stored API responses and `pulse.offlineUser` are deleted on sign-out (also when the sign-out request itself fails), when any authenticated request or the session check returns 401, when the administrator ends their own sessions, when a different account signs in, and as described above when the device is not remembered. The worker independently deletes its API cache when it sees a sign-in or sign-out request or a 401 response, and a generation counter keeps a response that was still loading at that moment from being stored afterwards.
- **Indicator.** While the browser reports no connection, a status pill at the bottom of the screen says so (in the reader: "You are offline. Articles loaded earlier remain readable."). Online, the same pill says when the reader shows stored copies (see "Stored copies are never shown as fresh").
- **Updates.** Every deployment changes `sw.js`: it embeds a version derived from the content of all precached files. Nginx serves `sw.js` and the manifest with `Cache-Control: no-cache`, and open tabs also check for an update hourly. A new version installs in the background but waits: the page shows "A new version of Pulse is available" with a Reload button, which activates it and reloads. Until then the open page keeps the precached files of the version it was loaded with, so its lazily loaded chunks remain available although the server no longer has them. Activation deletes the previous version's shell cache.

## Accounts, roles, and authorization

There are two roles:

| Capability | Administrator | Reader |
| --- | --- | --- |
| Read and update articles in the user's own library | Yes | Yes |
| Create personal folders and feed subscriptions | Yes | Yes |
| Create accounts and assign roles | Yes | No |
| Disable or re-enable accounts and sign users out everywhere | Yes | No |
| Change global and per-user refresh settings | Yes | No |
| Read another user's library | No | No |

An administrator's reading library is personal; administrator status does not grant access to other users' folders, subscriptions, or article state. Public registration and account deletion are not available; accounts are disabled instead, which keeps their library, shared sources, and history intact.

Account status rules (`PATCH /api/admin/users/{id}` with `disabled`, `role`, and/or `refreshIntervalMinutes`; unknown fields return `400 admin.user_field_unknown`):

- **Disabling** sets `users.disabled_at` and `disabled_by_user_id` and deletes all of the user's sessions in the same transaction, so their next request receives `401`. Session lookup also rejects disabled accounts as a second safeguard. Re-enabling clears both columns; the user signs in again normally.
- **Signing in to a disabled account** returns `403 auth.account_disabled`, but only after the password has been verified, and creates no session. Unknown accounts and wrong passwords keep returning the same `401 auth.invalid_credentials` (rate limiting still applies first), so the disabled state is not revealed to someone without the password.
- **The last active Administrator** (role Administrator and not disabled) can be neither demoted nor disabled: `409 admin.last_administrator`. Disabled administrators do not count.
- **Self-protection:** administrators cannot disable their own account (`409 admin.cannot_disable_self`) or change their own role (`409 admin.cannot_change_own_role`); another Administrator must do it. This keeps an administrator from revoking the session they are using and, together with the rule above, from locking the system out.
- **Role changes** delete all of the target user's sessions in the same transaction, so the new role applies from their next sign-in and no session keeps the old privileges. Setting the same role again, or changing only the refresh interval, keeps sessions.
- **Sign out everywhere:** `POST /api/admin/users/{id}/sign-out` deletes every session of that user (`204`, `404 user.not_found` for an unknown ID). When administrators use it on themselves, the response also clears their cookie and the interface returns to sign-in.

The administration panel marks disabled accounts, asks for confirmation before disabling an account or signing a user out everywhere, and hides the disable control and locks the role selector on the administrator's own row.

On an empty database, the API bootstraps the first Administrator from `INITIAL_ADMIN_EMAIL` and `INITIAL_ADMIN_PASSWORD`. Both are required for that first startup only. Passwords are stored as Argon2id hashes. A sign-in attempt for an unknown e-mail address performs the same Argon2id work as one for an existing account and returns the same error, so responses do not reveal registered addresses. Login creates a cryptographically random server-side session; the browser receives only an opaque HttpOnly cookie with `SameSite=Strict`. Sessions expire after 14 days and do not slide on activity. Password changes require the current password, revoke the user's other sessions, and preserve the current session. Mutating API requests are checked for same-origin behavior in addition to cookie protections.

The health endpoint is public. Library and administrative endpoints require authentication, and administrative endpoints additionally require the Administrator role. Authorization and ownership checks are enforced by the API and database queries, not only by hiding controls in the frontend.

### Sign-in throttling and request limits

- **Login rate limiting.** `POST /api/auth/login` allows at most 10 failed attempts per account (normalized e-mail address, including unknown addresses) within a rolling 15 minutes, and at most 30 attempts of any outcome per client IP per minute. Further attempts receive `429` with code `auth.rate_limited` and a `Retry-After` header in seconds, even when the password is correct. A successful sign-in clears the account's failures. Limits are checked before any Argon2id work; an attempt is provisionally counted as a failure when it starts, so concurrent guesses cannot bypass the account limit. The limiter state is stored in PostgreSQL (`login_attempts`, migration `020_login_attempts.sql`), so it survives API restarts and is shared by every API instance using the database. Each allowed attempt adds one `ip` row and one `account` row (the provisional failure) with the attempt time; a successful sign-in deletes the account's rows, and a server error during the account lookup deletes the provisional row. Check and record are atomic across instances: one transaction takes transaction-scoped advisory locks for the IP key and the account key (in ascending order, so transactions cannot deadlock), counts the attempts inside each window, and inserts the rows only if both limits allow. Concurrent requests for the same IP or account therefore cannot exceed a limit, whichever instance they reach. Attempt times and `Retry-After` come from the API clock, so instances need synchronized clocks (NTP). Keys are not stored in clear: `key_hash` is HMAC-SHA256 of the kind and the normalized e-mail address or client IP under a per-installation 32-byte secret generated by the migration (`login_rate_limit_secret`). A database dump thus does not list attempted addresses (which may be typos or misplaced passwords) or client IPs; someone who also has the secret from the same database can still test guessed values. Each API instance deletes rows that have left their window at most once a minute, on the next sign-in attempt; while sign-ins arrive, rows therefore live at most 15 minutes plus that interval. The maintenance job deletes the remaining expired rows every 6 hours, so they do not stay until the next sign-in once sign-ins stop. Rows are only added for attempts the per-IP limit allowed, and every allowed attempt that reaches an account pays the Argon2id verification, so a flood of distinct e-mail addresses grows the table by at most two small rows per accepted request and the table shrinks again once the flood stops. There is no key cap and no eviction, so a flood cannot erase an active lockout. **Database unavailable:** if the limiter state cannot be read or written, sign-in fails closed with `500 auth.account_check_failed` before any password verification, and no session is created. Sign-in needs the database for the account lookup and the session anyway.
- **Client address and trusted proxies.** The limiter uses the TCP peer address. Only when that peer is inside `TRUSTED_PROXY_CIDRS` does the API use the `X-Real-IP` header, which the bundled Nginx overwrites with its own peer address. The default trusts loopback, RFC 1918 and `fc00::/7` (the Docker networks between Nginx and the API); `none` disables the header, and an invalid value stops the API at startup. Behind an additional host-level proxy, the bundled Nginx reports that proxy's address, so the per-IP limit then applies to all clients of that proxy together; the per-account limit is unaffected.
- **Argon2id concurrency.** Password hashing and verification share a semaphore sized to `GOMAXPROCS` (at least 1). Each computation uses 64 MiB, so this bounds Argon2id memory regardless of how many sign-in requests arrive; callers wait for a free slot, and the wait is not cancelled with the request.
- **Pending feed requests.** Each user can be a requester on at most 10 pending feed approval requests, counting new feeds, URL replacements of existing subscriptions, and pending requests they joined; the next request returns `409 feed.request_limit_reached`. Repeating a request that is already pending stays accepted and does not use another slot; it keeps the requester's place in the queue but replaces their folder, title, and the subscription to replace (or none, for a new feed) with the latest request. `GET /api/admin/feed-requests` returns `{"requests": [...], "total": n, "nextCursor": "..." | null}`: the oldest requests first (by creation time, then ID), limited by `limit` (default 100, 1–200; other values return `400 feed.requests_limit_invalid`), and the total number of pending requests. Paging uses a keyset cursor: `nextCursor` is set when more requests follow, and passing it as `after` returns the requests strictly after the last one of the previous page (a malformed cursor returns `400 feed.requests_cursor_invalid`). Offset paging was removed because a request approved or rejected by another administrator between two pages shifted the offset and skipped a request; a cursor stays valid even when its own request has been decided. A request created by a transaction that committed after a later request was listed can still sort before the cursor and appears after a reload. The administration view shows the total in its badges and loads further pages with a "Load more" button that passes the last `nextCursor` and skips requests it already shows.

### Browser security headers

Nginx adds these headers to every response, including API responses and errors: a Content Security Policy of `default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' https: http: data:; connect-src 'self'; font-src 'self'; worker-src 'self'; manifest-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `Permissions-Policy: camera=(), microphone=(), geolocation=()`, and `X-Frame-Options: DENY`. `add_header_inherit merge` keeps them on locations that add their own headers. Feed images may load from any HTTP(S) host; scripts, styles, fonts, API calls, the service worker and the web app manifest are same-origin only. The colour-theme bootstrap is the external `/theme-init.js`, loaded synchronously in `<head>` so the theme applies before first paint without an inline script; the built `index.html` must not gain inline `<script>`/`<style>` elements or `style` attributes. React `style` props are applied through the DOM and are allowed. HSTS is not sent by the bundled Nginx because it does not terminate TLS; the TLS-terminating proxy must add it.

## Data model and shared subscriptions

A feed source is a canonical shared entity identified by a normalized URL. Normalization lowercases scheme and hostname, removes default ports and URL fragments, and preserves the path and query. Distinct paths or query strings remain distinct sources.

One source owns one set of RSS/Atom articles. A user subscription links a user to a source and optionally to one of that user's folders. A source can have many user subscriptions, and one user can subscribe to many sources. Folder names are unique per owner, not globally. Each personal folder stores a color from the supported palette or a custom six-digit HEX value; readers can rename and recolor folders without changing their feed assignments. The read and saved flags live in per-user article state, so all subscribers see the same refreshed article data while retaining private reading progress.

Each user may subscribe to at most 100 feeds. The API checks this limit before fetching a newly added source and enforces it again transactionally when creating the personal subscription, so concurrent requests cannot exceed the cap. Administrators do not need access to users' personal feed lists to apply this limit.

New feed URLs that are not already in `feed_sources` are stored as pending approval requests only; Pulse does not resolve, fetch, parse, or persist feed content before an Administrator approves the URL. There is one request per normalized URL with any number of requesters (`feed_request_requesters`); each requester keeps their own folder, title, and the subscription a URL change would replace. A user who requests a URL that is already pending joins that request (`202`, status `pending`) instead of being refused. The approval queue exposes the submitted URL, the requesters' e-mail addresses (`requesters`, in request order; `requestedBy`, `folderId`, `folderName`, and `title` describe the first requester for compatibility), and the first requester's folder, but not the requesters' other subscriptions or article data. Approval creates the shared source and subscribes every requester in one transaction; background refresh can fetch it afterward, and future subscriptions to that canonical source reuse the approved source. A requester who has meanwhile reached the 100-feed limit, whose subscription to replace no longer exists, or whose URL change would duplicate a subscription they already have to the approved source is skipped (both of their subscriptions stay as they were); the approval still succeeds with `204`, and the API logs each skipped user. Rejection deletes the request for all requesters. A request whose last requester disappears (for example because the subscription it would replace was removed) is deleted automatically. Changing a subscription to a not-yet-approved URL follows the same queue and leaves the current subscription in place until approval. Sources already present before this migration remain usable to preserve existing libraries.

Feed name overrides and folder assignments belong to the user subscription. Editing a name or folder affects only that user's library. Changing a subscription URL moves only that user's subscription to an already approved source; a new URL must first be approved, without changing the previous source or its other subscribers. A user cannot add the same canonical source twice.

The legacy single-user library is migrated to the first bootstrapped Administrator. If source URL normalization merges duplicate feeds, duplicate article identifiers are consolidated. The migration preserves saved state if any duplicate was saved, unread state if any duplicate was unread, and the latest publication timestamp for merged article content. Existing user-created names and content are not translated or rewritten by localization changes.

## Feed refresh and concurrency

Refresh work uses a bounded Go worker pool. `RSS_REFRESH_CONCURRENCY` configures the number of concurrent unique sources; the default is 4, values are limited to 1–32, and invalid values fall back safely. Scheduled cycles and manual requests share this budget, so at most that many sources are fetched at once in one API process. Each feed refresh retains its own timeout and error persistence. A failure for one source does not prevent other queued sources from being processed.

A database-backed source lease prevents concurrent requests for the same canonical source, including across API processes. The lease has an expiry so a crashed worker can be retried, and a unique lease token fences stale workers from overwriting data after another worker takes ownership. Failed refreshes do not advance the successful-refresh time, but they do record the attempt (`last_checked_at`) for failure backoff.

The shared source TTL defaults to 15 minutes and accepts values from 5 to 1,440 minutes. A successful fetch updates the canonical source once and makes unblocked articles visible to its subscribers. A subscription to a source refreshed within its TTL reuses the existing data rather than issuing another HTTP request.

The background scheduler runs its first cycle about 10 seconds after start-up and then checks for due sources every minute. A shared source is refreshed at the shortest effective interval among its subscribers, where a subscriber's interval is their per-user override or else the global scheduler interval, and never more often than the shared-source TTL: `effective = max(TTL, min over subscribers of interval)`. A source is due when no attempt (successful or failed) happened within its effective interval, so a never-checked source is due immediately and a failing source is retried once per effective interval rather than on every tick. Sources without subscribers are not scheduled, and subscriptions of disabled accounts are ignored both for the interval and for that check. Interval and TTL changes apply on the next tick. The full rule, SQL, and edge cases are in [the scheduler design note](2026-11-01-scheduler-design.md).

Manual refresh (`POST /api/refresh`) is scoped to the signed-in user's subscriptions and always returns `202`. Requests are queued per user: a user who is already queued or being processed is not queued twice, and different users never block each other. `GET /api/refresh` returns `{"running": bool}` for the calling user only, true while that user's request is queued or being processed. Manual claims use only the shared-source TTL, measured from the last attempt, so "refresh now" can fetch sooner than the scheduler interval but does not hit a failing source again within the TTL. Source leases keep scheduled and manual work from fetching the same source concurrently.

Refreshes use conditional HTTP requests. The `ETag` and `Last-Modified` validators of the last successful response are stored per source (migration `013_conditional_fetch.sql`) and sent as `If-None-Match` and `If-Modified-Since`. A `304 Not Modified` response counts as a successful refresh: it advances `last_checked_at` and `last_success_at`, clears `last_error`, and leaves articles untouched.

Changing a subscription URL to an already approved source (`PATCH /api/feeds/{id}`) does not fetch inside the request: the subscription change is committed, a manual refresh is queued for the user, and the response returns the updated feed. The reader follows that refresh through `GET /api/refresh` and reloads the library when it finishes.

On shutdown the API stops accepting requests, then waits, within the same shutdown deadline, for the refresh syncer and the maintenance job to stop. An in-flight fetch is cancelled and its lease is released with a bounded 5-second cleanup context without recording an attempt, so a restart does not put the source into failure backoff.

Only public HTTP/HTTPS feed URLs are accepted. Loopback, private, link-local, multicast, carrier-grade NAT, documentation, benchmarking, reserved, and other IANA special-purpose IPv4/IPv6 ranges are blocked to prevent a feed request from reaching internal services; IPv4 addresses embedded in IPv4-mapped, NAT64 (`64:ff9b::/96`), and 6to4 (`2002::/16`) addresses are checked as IPv4. Every resolved address is checked again when the connection is dialed. The API stores article metadata and summaries; the full article remains on the source website. Publication dates are parsed from RFC 3339/ISO 8601 and RFC 822/1123 forms, including one-digit days, missing weekday or seconds, two-digit years, RFC 822 zone names, and zone-less timestamps (treated as UTC). Items without a recognizable date use the first-seen time. Atom `content` and `summary` with `type="xhtml"` keep their inline XHTML markup (the wrapping `<div>` required by RFC 4287 is removed when it is a plain namespace-only wrapper); `type="html"` and text content are decoded once. RSS 1.0 (RDF) items without a `guid` use their `rdf:about` identifier before falling back to the link.

Within a source, an item is matched to an existing article by GUID. If the GUID is unknown, the refresh looks for the same article republished under a new GUID within ±30 minutes of its publication time and updates it in place (keeping every subscriber's read and saved state) only when the non-empty URL is equal, or when both the non-empty title and the non-empty summary are equal. A shared title or a shared summary alone never merges articles, so repeated titles such as "Daily digest" remain separate articles. Items are applied in feed order, so a later item also sees articles inserted, updated, or renamed by an earlier item of the same refresh. The commit runs a fixed number of statements instead of several per item: one query loads the quarantine status of every incoming GUID, one locked query loads every stored article an item could match (same GUID, or the merge rule above), the rules are applied in memory, and only the resulting quarantine upserts and article inserts and updates are sent, in order, as pgx batches. Updates that would not change a row are skipped.

Before article upserts, each fetched item's title and summary are checked against the administrator-managed stop-word dictionary. Matching is case-insensitive and uses whole words; punctuation and HTML markup separate words, so phrases can match across punctuation. The seeded dictionary includes Russian, English, and German spam/fraud and erotic/pornographic terms. Administrators can add or remove words.

Matching articles are retained in quarantine with their GUID, title, URL, summary, original publication timestamp, source, matched term, and moderation state. Newly encountered matching articles stay out of the shared article table while pending or rejected, and clean articles from the same feed continue to be stored. Articles already stored in the shared article table are retained if a later refresh matches a stop word. Repeated refreshes update quarantine content and match metadata without duplicating records or resetting moderation decisions. Deleting a stop word does not itself restore quarantined articles.

The administration panel lists up to 100 quarantine records with pending items first, including source ownership, publication and last-blocked dates, article summary, and status. Administrators can allow or reject pending items; rejection is reversible and a rejected item can later be allowed. Allowing an item transactionally inserts or updates the shared article with its original publication timestamp, so its place in the reader follows publication time rather than the moderation date. It becomes visible only in libraries subscribed to that source, including the administrator's library only if subscribed. Allowed records remain read-only history and are not returned to quarantine by later matching refreshes. Decisions record the administrator and decision time. Legacy log entries without the retained summary and publication timestamp remain visible as history without moderation actions.

## Administrative settings

Administrators can create users with an email address, initial password, and either supported role. The password is presented only during account creation and must be shared separately. Administrators can edit the global scheduler default, a nullable per-user interval override, and the shared-source TTL. A user with no explicit interval inherits the global value; an explicit override is unaffected by changes to the default.

The scheduler interval, the per-user intervals, and the shared TTL are all active and labeled as such; the panel explains the shortest-subscriber-interval rule described under [Feed refresh and concurrency](#feed-refresh-and-concurrency).

## Internationalization

The user interface supports English (`en`), German (`de`), and Russian (`ru`). English is the source language and default for a new browser profile. Language selection is available on both the sign-in screen and the reader toolbar and is stored in browser local storage under `pulse.language`. A missing or invalid saved language resolves to English.

Translation files are plain UTF-8 JSON catalogs in `frontend/src/locales/`. English is bundled with the application as the fallback; the other catalogs are separate chunks that `setLanguage` in `frontend/src/i18n.ts` loads before switching. At startup the saved language's catalog is loaded before the first render, so the interface does not flash English; if it cannot be loaded, English is shown and the saved choice is kept. Their keys are flat dotted strings. `i18next` and `react-i18next` provide React integration, English fallback, interpolation, and locale-specific plural rules. The selected locale updates the document `lang`, page title, dates, relative times, and translated interface text. Feed-provided content remains unchanged.

To add a language, copy `frontend/src/locales/en.json`, translate its values, register the language code and a catalog loader in `frontend/src/i18n.ts` (`supportedLanguages` and `catalogLoaders`), and add the language's native display name in `frontend/src/LanguageSelector.tsx`. Keep all application source strings, backend messages, and non-localized project documentation in English. API errors include a stable dotted code and an English fallback; the frontend looks up `apiErrors.<code>` in the active catalog and uses the fallback for unknown codes.

## API surface

The API is served under `/api/`. The complete reference, with request and response schemas and the error codes of every response (`x-error-codes`), is the OpenAPI 3.1 description [`backend/openapi/openapi.json`](../backend/openapi/openapi.json). `backend/test/internal/httpapi/openapi_test.go` walks the chi router returned by `httpapi.New` and fails when a registered route is missing from the description or a documented route is not registered, and when an error code returned by a handler is not listed or a listed code is not returned. The description is JSON so the test needs no YAML dependency, and it lives under `backend/` because the Go test commands mount only that directory into the container.

The API includes public health endpoints; authentication and profile operations; folder, feed, article, and refresh operations; and administrator-only account and settings operations. The principal route groups are:

- `/api/auth/login`, `/api/auth/logout`, `/api/auth/me`, `/api/auth/password`, and `/api/auth/profile` for sessions and profile management.
- `/api/folders` and `/api/feeds` for the signed-in user's library, including `PATCH /api/feeds/{id}` for personal subscription edits and `DELETE /api/folders/{id}` for folder removal. The delete body uses `{"action":"delete_sources"}` to remove the user's subscriptions and their article state, or `{"action":"move_sources","targetFolderId":<id>}` to move subscriptions into another folder owned by the same user. The folder deletion and subscription changes occur in one transaction; shared source and article records are retained.
- `/api/articles` and `/api/articles/{id}` for filtered article listing and per-user read/saved updates. `GET /api/articles` returns `{"items": [...], "hasMore": bool, "nextCursor": string|null, "latestArticleId": int}`; `latestArticleId` is the highest article ID that existed when the page was listed and serves as the ingestion bound for mark-read. Pass `nextCursor` back as `before=<cursor>` to load the next page; the cursor is opaque and malformed values return `400 article.cursor_invalid`. The older `offset` parameter is still accepted for compatibility but cannot be combined with `before` (`400 article.pagination_conflict`).
- `POST /api/articles/mark-read` with `{"filter": "all|unread|today|saved", "feedId"?, "folderId"?, "q"?, "since"?, "upToId"?: int, "upTo"?: RFC 3339}` marks every unread article that `GET /api/articles` would list for the same filter and that lies within the bounds as read: `upToId` (the `latestArticleId` of the loaded first page) excludes articles ingested later, whatever their publication date; `upTo` excludes articles published after it (older clients send only `upTo`; with both, both apply). At least one bound is required. It runs in one statement and returns `{"marked": n}`. Validation errors match the list (`article.filter_invalid`, `feed.id_invalid`, `folder.id_invalid`, `article.search_too_long`, `article.date_invalid` for an invalid `upTo` or `since` or when neither bound is given, `article.id_invalid` for a negative `upToId`, `request.invalid_json`).
- `GET /api/opml` downloads the user's subscriptions as OPML 2.0 (`Content-Disposition: attachment; filename="pulse-subscriptions.opml"`). `POST /api/opml` imports an OPML document sent as the raw body (`Content-Type: text/x-opml`, `application/xml`, or `text/xml`, otherwise `415 opml.content_type_invalid`; more than 1 MiB is `413 opml.too_large`; malformed XML, a root other than `<opml>`, more than 1,000 outlines, or outlines nested deeper than 5 is `400 opml.invalid`; other elements inside `<body>` are ignored but their outlines are still read) and returns `{"subscribed": n, "requested": n, "skipped": n, "failed": [{"url", "reason"}]}` with at most 100 failures listed; reasons are API error codes such as `feed.url_invalid`, `feed.limit_reached`, and `feed.request_limit_reached`.
- `/api/refresh` for the signed-in user's manual refresh (`POST`, always `202`) and its status (`GET`, `{"running": bool}` for that user).
- `/api/admin/users`, `/api/admin/users/{id}` (role, refresh interval, `disabled`), `POST /api/admin/users/{id}/sign-out`, `/api/admin/settings`, and `/api/admin/feed-requests` (optional `limit`, 1–200, default 100, and cursor `after`; returns `{requests, total, nextCursor}`; including approve and reject actions) for administrator operations. `/api/admin/stop-words` supports listing and adding terms; `/api/admin/stop-words/{id}` removes a term. `GET /api/admin/blocked-articles` lists quarantine records with pending items first; `POST /api/admin/blocked-articles/{id}/allow` and `/reject` apply moderation decisions. All quarantine endpoints require the Administrator role.
- `/api/health/live` (liveness), `/api/health/ready` (readiness with a database ping), and `/api/health` (alias of readiness); see [Logging and health checks](#logging-and-health-checks).

API errors use an object with a stable `code` and an English `error` message. Existing HTTP status codes communicate request outcome; localized text is resolved in the frontend. Handlers translate store sentinel errors with `writeStoreError`, which takes an ordered list of sentinel/response pairs and a fallback response, so each response is defined once and shared responses (for example `feed.already_added`) are reused.

## Data retention and maintenance

`backend/internal/maintenance` runs a background job started by the API with the process context: once about one minute after start, then every 6 hours. It stops when the context is cancelled, and shutdown waits for it. Each step is independent, logs its deletion count, and a failing step does not skip the others.

- **Articles** (`ARTICLE_RETENTION_DAYS`, default 180, valid 30–3,650, `0` disables): an article is deleted when both `published_at` and `created_at` are older than the period and no user has it saved. Deletion runs in batches of 5,000 ids per statement (`FOR UPDATE SKIP LOCKED`) to keep locks short; `user_article_states` rows cascade. Migration `012_article_retention.sql` indexes `user_article_states (article_id)` for the cascade and the saved check.
- **Churn prevention:** a refresh commit drops dated items (`HasPublishedAt`) older than the article period whose GUID is not already stored for that source, before stop-word filtering and upserts. Otherwise a feed that still lists old items would re-insert deleted articles as unread. Already stored articles with that GUID are still updated; undated items are unaffected.
- **Quarantine** (`QUARANTINE_RETENTION_DAYS`, default 90, valid 7–3,650, `0` disables): allowed or rejected records whose `decided_at` and `last_blocked_at` are both older than the period, and non-moderatable legacy records (`moderation_eligible = false` or no summary) whose `last_blocked_at` is older than the period, are deleted. Requiring an old `last_blocked_at` keeps a decision while the item is still listed by its feed, so a decided item cannot return as pending. Moderatable pending records are never deleted.
- **Sources without subscribers** (`UNSUBSCRIBED_SOURCE_RETENTION_DAYS`, default 30, valid 1–3,650, `0` disables): each run first maintains `feed_sources.unsubscribed_since` (migration `019_unsubscribed_sources.sql`): it is set to the current time for a source that has no subscription and cleared for a source that has one again. The grace period therefore starts when a run first sees the source without subscribers, up to one job interval (6 hours) after the last subscriber left; sources that were already unsubscribed before the upgrade start their grace period at the first run. A source is deleted when `unsubscribed_since` is older than the period and nothing still needs it: no subscription (subscriptions of disabled accounts count, so re-enabling an account restores its library), no pending feed request for the same normalized URL, no pending quarantine record an administrator can still moderate, and no unexpired refresh lease. Its articles are deleted in batches of 5,000, each batch in its own transaction that locks the source row (`FOR UPDATE SKIP LOCKED`) and checks these conditions again; the source row is deleted with the last batch, and read/saved states and decided quarantine records cascade. Saved articles do not keep a source: without a subscription they are not visible to their former reader. Adding the URL again (directly, by URL change, OPML import, or approval of a request) clears `unsubscribed_since` in the transaction that confirms the source is approved, before the subscription is created in its own transaction; a deletion batch that holds the row lock makes that confirmation wait, and a later batch sees the cleared value and stops, so the source survives with its remaining articles, which the next refresh tops up. Once a source has been deleted its URL is no longer approved: adding it again creates a new approval request. The scheduler never fetches a source without subscribers (see "Feed refresh and concurrency").
- **Sign-in attempts:** the job deletes `login_attempts` rows that have left their window (`store.DefaultLoginThrottle`: 1 minute for `ip` rows, 15 minutes for `account` rows). The sign-in rate limiter also deletes them, at most once a minute per API instance on the next sign-in attempt (see Sign-in throttling). Both sweeps are kept on purpose: the limiter's sweep bounds the table during heavy sign-in traffic such as a guessing flood, which can add thousands of rows between two job runs, while the job removes the rows left behind when sign-ins stop, which the limiter alone would keep until the next sign-in.
- **Sessions:** expired sessions are deleted by the job. Session lookup on authenticated requests is read-only; its `expires_at > NOW()` predicate rejects expired sessions.

Invalid retention values stop the API at startup.

## Performance notes

Measurements on a seeded dataset (500,000 articles, 50 users) and the procedure to repeat them are in [`2026-11-01-performance-baseline.md`](2026-11-01-performance-baseline.md).

- **Search** matches a search key: the words of the lower-cased title and plain-text summary (tags replaced by spaces, entities decoded) joined by the letter `ǂ` (U+01C2) instead of spaces. Ingestion stores it in `articles.search_key`, computed in Go by `store.ArticleSearchKey` whenever it inserts or updates an article, including quarantine approvals. Queries are converted the same way in Go and matched with `LIKE '%…%'` (wildcards and the escape character escaped) against `COALESCE(search_key, pulse_article_search_key(title, summary))`: rows without a stored key (existing articles until the background backfill reaches them, or rows inserted by other code) are matched through `pulse_article_search_key` (migration `018_article_search_key.sql`), the same normalization in SQL, so search is correct at every stage of the upgrade. The trigram GIN index `articles_search_key_trgm_idx` (`pg_trgm`) covers that expression. pg_trgm extracts trigrams from runs of letters and digits; because the separator is a letter, the whole key is one run and the index also records the trigrams that span word boundaries (`yǂc` and `ǂcl` for "energy climate"). With spaces, the index only knew which words an article contains, so a phrase of common words made every article containing all of its words a candidate (`energy climate` took 3.0 s on the seeded data and takes 0.7 s now; phrases with more varied word boundaries 0.07–0.2 s). `ǂ` in article text or in a query counts as whitespace, so it only ever marks a word boundary; the matching semantics are unchanged (see "Reader experience and interaction"). Queries whose key has no run of three letters or digits (counting `ǂ`) cannot use trigrams and are matched with `STRPOS`, which scans the user's visible articles. Searches are sent with `pgx.QueryExecModeCacheDescribe` so PostgreSQL plans each one for its actual pattern: a cached generic plan always read the trigram index and took seconds for patterns found in most articles, while a plan for the actual pattern scans the user's articles in those cases. The separator trick relies on the database's character classification treating `ǂ` (and Cyrillic or German letters) as letters, which the official image's `en_US.utf8` libc locale does; with a `C` ctype, search stays correct but phrases are not indexed better than single words. Go's `strings.ToLower` and PostgreSQL's `lower()` agree for Cyrillic and German under that collation; on the seeded 500,000 articles the SQL function produced exactly the Go key for every row. Ingestion overwrites a key filled by SQL whenever a refresh sees the article again. The index takes about 470 MB on the seeded data next to 760 MB of articles, and it adds work to every article insert.
- **Search upgrade without downtime.** Filling the search key and building the index take minutes on a large database, so they run after the API has started, in `store.ArticleSearchUpgrade` (started by `cmd/api` once the server listens, retried a minute after a failure, stopped on shutdown): it fills `search_key` in batches of 1,000 articles in id order (each batch its own short statement; rows locked by another transaction are skipped and stay correct through the SQL fallback), builds `articles_search_key_trgm_idx` with `CREATE INDEX CONCURRENTLY` (after dropping an invalid index left by an interrupted build), runs `VACUUM (ANALYZE) articles` (the planner needs statistics of the index expression; the vacuum makes the row versions replaced by the backfill reusable and marks the pages all-visible for index-only scans, see "Database maintenance"), and records `018_article_search_key.sql:background` in `schema_migrations`, so later starts skip it. A session advisory lock per schema serializes concurrent runs. The API logs `article search upgrade started`, backfill progress every 30 seconds, and `article search upgrade completed`. Until then searches are correct but slower (about 3 s per search on 500,000 articles while the keys are filled, because every visible row is normalized in SQL). On the seeded 500,000 articles the whole step took about 3 minutes 45 seconds (see the performance baseline). The seed tool runs the same step before it returns.
- **`pg_trgm`** ships with the official PostgreSQL images (contrib) and has been a trusted extension since PostgreSQL 13, so a database owner without superuser rights can create it; the migration needs the `CREATE` privilege on the database, which the owner has. The extension is installed in `public` and the index names the schema the extension actually lives in.
- **Unread counters** (`ListFeedsForUser`, `ListFoldersForUser`, `UpdateFolderForUser`) compute a subscription's unread count as its source's article count minus the user's read states for that source. Migration `017_unread_counters.sql` makes both counts index-only: `articles_source_published_id_idx (feed_source_id, published_at DESC, id DESC)` replaces `articles_source_published_idx`, and `user_article_states_read_idx (user_id, article_id) WHERE is_read` holds only read states. The cost is proportional to the subscribed sources' article count and the user's read states, without sorting or touching article rows.
- **Ingestion** runs a fixed number of statements per refresh (see "Feed refresh and concurrency").
- **Connection pool:** `DB_MAX_CONNS` sets the API's maximum pgx pool size (default 12, valid 2–100; invalid values stop the API at startup).
- **Static assets:** the bundled Nginx serves Vite's content-hashed files under `/assets/` with `Cache-Control: public, max-age=31536000, immutable` (a missing asset is a 404, never the `index.html` fallback), while `index.html`, `sw.js`, `manifest.webmanifest` (served as `application/manifest+json`, which nginx's MIME table lacks), and the other `public/` files keep `no-cache`; `/sw.js` and the manifest are never answered with the `index.html` fallback. HTML, CSS, JavaScript, JSON (including API responses and the manifest), and SVG responses of at least 1 KiB are gzip-compressed. The security headers apply to every location.
- **Bundle splitting:** the administration workspace, the profile, OPML import, folder management and shortcut dialogs, and the German and Russian catalogs are lazy chunks; third-party code (React, i18next) is a separate `vendor` chunk that stays cached when only application code changes. JavaScript loaded on first visit went from 449 kB (128 kB gzip) in one file to 360 kB (110 kB gzip) in the entry, vendor and runtime chunks (357 kB before the offline support was added).

## Project structure

- `frontend/src/` — React/TypeScript application, locale catalogs, and styles. `App.tsx` only chooses between the session check, sign-in, and the signed-in `Reader.tsx` shell (keyed by user, so signing out discards all library state). State lives in hooks under `src/hooks/`: `useSession` (current user, sign-in/out, expired-session handling), `useLibrary` (feeds, folders, counters), `useLibraryView` (selected filter/feed/folder, admin view, sidebar state), `useArticleFeed` (cursor-paged, cancellable article list, selection, read/saved/mark-all actions), `useRefresh` (manual refresh and status polling), `useKeyboardShortcuts`, and `useFocusTrap`. Dialogs are separate components rendered through `ReaderDialogs.tsx`; rarely used ones are lazy-loaded. `src/sw/` is the service worker and its caching policy (bundled only into `sw.js`), `pwa.ts` registers it and tracks waiting updates, `offlineData.ts` keeps and clears the remembered account and API cache, `staleData.ts` tracks stored copies shown while online and receives the worker's messages about late answers, and `AppStatus.tsx` shows the offline/stale indicator and update prompt. `frontend/vite-plugin-service-worker.ts` builds `sw.js` and its precache list. `api.ts` throws `ApiError` (`status`, `code`, localized `message`), accepts an `AbortSignal` on read requests, ends requests that get no response after a time limit, and reports 401 responses of authenticated calls to the handler registered by `useSession`.
- `frontend/test/` — frontend unit and browser tests, kept in a parallel component-oriented structure.
- `frontend/eslint.config.js` — ESLint flat config (`npm run lint`): `@eslint/js`, `typescript-eslint` and React hooks rules. `typescript-eslint` needs the TypeScript 6 compiler API, which TypeScript 7 does not ship, so the lint process resolves `typescript` to `@typescript/typescript6`; type checking uses TypeScript 7 through `npm run typecheck`.
- `backend/cmd/api/` — API entry point and configuration.
- `backend/internal/httpapi/` — HTTP routing, authentication, authorization, and handlers.
- `backend/internal/opml/` — OPML 2.0 parsing (with outline count and depth limits) and writing.
- `backend/internal/feeds/` — feed URL validation, fetching, and RSS/Atom parsing.
- `backend/internal/store/` — PostgreSQL persistence, account/library operations, and migrations.
- `backend/internal/syncer/` — scheduled and manual refresh coordination and worker pool.
- `backend/internal/maintenance/` — periodic retention cleanup of articles, quarantine records, sources without subscribers, sessions, and expired sign-in attempts.
- `backend/internal/metrics/` — Prometheus collectors and the `/metrics` handler of the separate metrics listener; `metricstest` reads sample values in tests.
- `backend/cmd/seed/` — development tool that fills an empty database with a large synthetic dataset for performance measurements.
- `backend/cmd/perfbench/` — development tool that times searches and 200-item refresh commits on a seeded database.
- `backend/test/internal/<package>/` — external Go test packages (`package <name>_test`) for the exported API of `backend/internal/<package>`; most backend tests, including the PostgreSQL integration tests, live here. Tests of unexported code stay beside their package as `*_internal_test.go` (or `*_test.go` in the same package), for example in `internal/httpapi`, `internal/store`, `internal/feeds`, `internal/auth`, `cmd/api`, and `cmd/seed`.
- `backend/openapi/openapi.json` — OpenAPI 3.1 description of the HTTP API, checked against the router by a test.
- `data/` — bind-mounted PostgreSQL data for local development.
- `compose.frontend.yml`, `compose.backend.yml`, and `compose.database.yml` — separate local Compose groups.
- `compose.production.yml` — production images and deployment definition.
- `scripts/update-production.sh` — production update helper; it takes a backup with `backup-production.sh` first.
- `scripts/backup-production.sh` and `scripts/restore-production.sh` — production backup with rotation and restore; see the README editions for scheduling, the restore procedure, and the recorded restore rehearsal.
- `scripts/sync-skills.sh` — restores or updates the lock-tracked agent skills.

## Database migration file design

The initial schema and historical upgrades are retained in the consolidated file `backend/internal/store/migrations/001_pulse.sql`. Its named phases preserve the bootstrap boundary and recognize the former migration names (`001_initial.sql` through `005_feed_customization.sql`) so existing databases do not repeat work.

New schema changes are added as separately numbered SQL files in `backend/internal/store/migrations/`:

| File | Change |
| --- | --- |
| `001_pulse.sql` | Consolidated initial schema and historical phases: folders, legacy feeds and articles, accounts and sessions, system settings, shared feed sources with per-user subscriptions and article states, profiles, feed customization. |
| `006_folder_customization.sql` | Folder color. |
| `007_feed_approval.sql` | Feed requests awaiting administrator approval. |
| `008_feed_word_filter.sql` | Source owner (`added_by_user_id`), stop-word dictionary with seeded terms, blocked-article log. |
| `009_feed_article_quarantine.sql` | Quarantine content and moderation state (`pending`, `rejected`, `allowed`) for blocked articles. |
| `010_quarantine_eligibility.sql` | `moderation_eligible` flag fixed when a quarantine record is created. |
| `011` | Number not used. |
| `012_article_retention.sql` | Index on `user_article_states (article_id)` for retention deletes. |
| `013_conditional_fetch.sql` | `ETag`/`Last-Modified` validators per source. |
| `014_account_disable.sql` | Account disabling (`disabled_at`, `disabled_by_user_id`). |
| `015_feed_request_requesters.sql` | Several requesters per pending feed request. |
| `016_article_search.sql` | `pg_trgm`. (The first version also added `articles.search_text`, filled it, and built its trigram index in the startup transaction; `018` removes them.) |
| `017_unread_counters.sql` | Indexes for index-only unread counts. |
| `018_article_search_key.sql` | Drops `search_text` (and its index) if present, adds the nullable `articles.search_key`, and `pulse_article_search_key`; catalog changes only. `store.ArticleSearchUpgrade` fills the column and builds the index after start. |
| `019_unsubscribed_sources.sql` | `feed_sources.unsubscribed_since`, the start of a source's grace period without subscribers. |
| `020_login_attempts.sql` | Shared sign-in rate-limit state (`login_attempts`) and its HMAC secret (`login_rate_limit_secret`). |

After the historical phases, startup discovers and applies these files in filename order, each transactionally under the migration advisory lock, and records the filename in `schema_migrations`; later startups skip files already recorded there. To support databases that ran an earlier development build, the runner also recognizes the former marker `001_pulse.sql:folder_customization` as equivalent to `006_folder_customization.sql`.

Migrations only move forward; there are no down scripts. `update-production.sh` backs up the database before every update, and the README editions describe the rollback: build the previous commit's images, then restore that backup with `restore-production.sh`, so the old API never starts against the newer schema. The advisory lock keeps concurrent API starts from applying migrations twice.

Migrations run before the API listens, so each must stay short: on the seeded 500,000-article dataset, migrations 013–018 applied to a database at 012 together took about one second. Work that rewrites or indexes large tables runs afterwards in the background (see "Search upgrade without downtime" in the performance notes). The production API health check therefore keeps a 70-second start period and the update and restore scripts wait 180 seconds for health checks by default (`DEPLOY_WAIT_TIMEOUT` overrides it).

## Deferred work and explicit non-goals

- Account deletion, self-registration, email invitations, verification, password-reset email, and social login.
- Cross-user library sharing and administrator access to user article content.
- Arbitrary HTML rendering of feed content.
- Translating RSS/user-created content or synchronizing language preferences between devices.
- Distributed refresh scheduling across independent backend deployments.

## Operational instructions

Use the language-specific README guides for requirements, local startup, Docker Compose build commands, production deployment, updates, scheduled backups (cron and systemd timer examples), restore and upgrade rollback, the recorded restore rehearsal, logs, and shutdown:

- [English operator guide](../README.md)
- [German operator guide](../README.de.md)
- [Russian operator guide](../README.ru.md)

The production commands below open `psql` in the `db` container of `compose.production.yml`:

```sh
docker compose --env-file .env.production -f compose.production.yml exec db sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
```

### Database maintenance

PostgreSQL's MVCC writes a new row version for every `UPDATE`; the old version stays in the table file until `VACUUM` marks its space reusable. A plain `VACUUM` never returns space to the operating system except empty pages at the end of the file, so the file keeps its largest size and later inserts and updates fill the free space. Autovacuum (enabled by default) vacuums and analyzes a table once about 20 % of its rows are dead, which suffices for normal operation: refreshes insert articles, retention deletes them in batches, and the freed space is reused.

**After the search-key backfill.** The background search upgrade (see "Performance notes") writes a new version of every article. On the seeded 500,000 articles (measured on PostgreSQL 18.6 with default settings): the table without search keys took 639 MB after `VACUUM FULL`; after the backfill it took 1,699 MB (autovacuum ran once during the backfill, so part of the dead rows was reused; without it the table roughly doubles), while a compact table with keys takes 1,184 MB. The upgrade therefore ends with `VACUUM (ANALYZE) articles` (9.5 seconds on that data; it takes a `SHARE UPDATE EXCLUSIVE` lock, which does not block reads or writes and waits only for another vacuum, analyze, index build, or schema change on the table): the dead rows become reusable, the visibility map is complete again (index-only scans of the unread counters do not visit the table), and the planner has statistics for the search index. The file stays at its size; new articles fill the free space over time, so with a steady article inflow no further action is needed. Reclaim the space only when the disk is tight or the table will not grow back (for example after lowering `ARTICLE_RETENTION_DAYS`).

Check table and index sizes, dead rows, and the last vacuum:

```sql
SELECT relname,
       pg_size_pretty(pg_table_size(relid))          AS table_size,
       pg_size_pretty(pg_indexes_size(relid))        AS index_size,
       pg_size_pretty(pg_total_relation_size(relid)) AS total_size,
       n_live_tup, n_dead_tup, last_vacuum, last_autovacuum, last_analyze, last_autoanalyze
FROM pg_stat_user_tables
ORDER BY pg_total_relation_size(relid) DESC
LIMIT 10;

SELECT indexrelname, pg_size_pretty(pg_relation_size(indexrelid)) AS index_size
FROM pg_stat_user_indexes
WHERE relname = 'articles'
ORDER BY pg_relation_size(indexrelid) DESC;
```

A large `table_size` with a small `n_dead_tup` after a vacuum means free space inside the file, not a problem by itself. Run a manual vacuum when `n_dead_tup` stays high (autovacuum has not caught up) or after a bulk change:

```sql
VACUUM (ANALYZE, VERBOSE) articles;
```

It is safe while Pulse runs. To return the free space to the operating system:

- **`VACUUM FULL`** rewrites the table and all its indexes into new files. It holds an `ACCESS EXCLUSIVE` lock for the whole run, which blocks every read and write of the table: article lists, search, unread counters, and refreshes stall until it finishes. On the seeded 500,000 articles it took 2 minutes 13 seconds and shrank the table from 1,699 MB to 1,184 MB and the indexes from 583 MB to 520 MB. It needs free disk space for a full copy of the table and its indexes. Plan a maintenance window: create a backup (`scripts/backup-production.sh`), stop the API and the web server (`docker compose --env-file .env.production -f compose.production.yml stop web api`), run `VACUUM (FULL, ANALYZE) articles;`, and start them again (`... up -d --wait`).
- **`pg_repack`** rebuilds a table online and holds an exclusive lock only briefly at the start and end. It is not part of the official PostgreSQL image: it needs the `pg_repack` extension installed in the server (a custom image) and a matching client binary, so Pulse does not ship it. Consider it only for databases too large for a maintenance window.

### Slow-query visibility

`compose.production.yml` starts PostgreSQL with these settings (as `postgres -c` arguments):

| Setting | Value | Effect |
| --- | --- | --- |
| `shared_preload_libraries` | `pg_stat_statements` | Collects execution statistics of every normalized statement (constants replaced by `$n`) in shared memory. |
| `pg_stat_statements.track` | `top` | Counts statements sent by clients, not statements inside functions. |
| `log_min_duration_statement` | `DB_LOG_MIN_DURATION_MS` from `.env.production`, default `500` | Logs every statement that runs at least that many milliseconds to the container log (`-1` disables, `0` logs every statement). |
| `log_parameter_max_length` | `0` | Slow-statement log lines never contain bind parameter values (e-mail addresses, search terms, hashes). |
| `track_io_timing` | `on` | Adds block read/write times to `pg_stat_statements`, `EXPLAIN (ANALYZE, BUFFERS)`, and `pg_stat_database`. |

Command-line settings override `postgresql.conf` and `ALTER SYSTEM`. `shared_preload_libraries` is read only at server start. A changed `command` (including a changed `DB_LOG_MIN_DURATION_MS`) changes the container configuration, so `docker compose ... up -d`, and therefore `scripts/update-production.sh`, recreates the `db` container and restarts PostgreSQL (verified with Compose; the data directory is kept). The first update with these settings, and every change of `DB_LOG_MIN_DURATION_MS`, interrupts database access for a few seconds; the API's connection pool reconnects. An invalid value stops PostgreSQL from starting; `docker compose ... logs db` shows the reason. The development, test, and end-to-end databases do not preload the library and are unaffected.

The `pg_stat_statements` view needs the extension in the database. No migration creates it: creating it needs superuser rights (it is not a trusted extension), which an installation using `DATABASE_URL` with a non-superuser role may lack, and the development and test databases do not preload the library. Statistics are collected by the preloaded library whether or not the extension exists, so creating it when first needed loses nothing. In the production container `POSTGRES_USER` is a superuser:

```sql
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

-- Statements by total execution time
SELECT calls,
       round(total_exec_time::numeric, 1)                      AS total_ms,
       round(mean_exec_time::numeric, 2)                       AS mean_ms,
       round(max_exec_time::numeric, 1)                        AS max_ms,
       rows,
       shared_blks_hit, shared_blks_read,
       round((shared_blk_read_time + shared_blk_write_time)::numeric, 1) AS io_ms,
       left(regexp_replace(query, '\s+', ' ', 'g'), 120)       AS query
FROM pg_stat_statements
WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
ORDER BY total_exec_time DESC
LIMIT 15;
```

Order by `mean_exec_time` to find individually slow statements. `SELECT pg_stat_statements_reset();` starts a new measurement period, for example after an update. Slow statements in the log: `docker compose --env-file .env.production -f compose.production.yml logs db | grep 'duration:'`. Statistics are kept across restarts only on a clean shutdown.
