# Pulse — self-hosted RSS reader

**Languages:** [English](README.md) · [Deutsch](README.de.md) · [Русский](README.ru.md)

Pulse is a multi-user web application for reading RSS and Atom feeds. It runs locally with React and TypeScript, a Go/Chi API, PostgreSQL, and Nginx. Frontend, backend, and database use separate Docker Compose files.

## Requirements

- Docker Engine with the `docker compose` plugin
- Outbound HTTP/HTTPS access from the server to public feed URLs

The application containers include their own build tools; Go and Node.js do not need to be installed on the host. Versions are pinned in the project manifests and lock files.

## Local setup

Run commands from the repository root. Create local settings and the shared Docker network once:

```sh
cp .env.example .env
docker network create rss-reader-net
```

Set `INITIAL_ADMIN_EMAIL` and a unique `INITIAL_ADMIN_PASSWORD` of at least 12 characters in `.env` before the first API startup against an empty database. These values are used only to bootstrap the first administrator. For local HTTP, keep `SESSION_COOKIE_SECURE=false`.

Start the database, API, and web server in that order:

```sh
docker compose -f compose.database.yml up -d
docker compose -f compose.backend.yml up -d --build
docker compose -f compose.frontend.yml up -d --build
```

Open <http://localhost:8090>. Change `WEB_PORT` in `.env` if the port is occupied. To access Pulse from your LAN, set `WEB_BIND=0.0.0.0`. If Nginx is already running on the host, keep `WEB_BIND=127.0.0.1` and proxy to `http://127.0.0.1:8090`.

Frontend source is mounted into its container. In development, the container runs a TypeScript check and then Vite in watch mode, rebuilding `frontend/dist/` whenever source files change; Nginx serves the updated build, so refresh the browser to see changes. No manual rebuild is needed for source edits. Recreate the frontend container after dependency or Docker image changes with:

```sh
docker compose -f compose.frontend.yml up -d --build
```

Backend source is mounted into the API container. Restart the API after Go changes:

```sh
docker compose -f compose.backend.yml restart api
```

## Frontend tests

Run component tests and Playwright browser tests in the frontend container:

```sh
docker compose -f compose.frontend.yml exec frontend npm run lint
docker compose -f compose.frontend.yml exec frontend npm run test -- --run
docker compose -f compose.frontend.yml exec frontend npm run test:e2e
```

Playwright starts a local Vite server and uses deterministic API fixtures. The offline and installation tests (`frontend/test/e2e/pwa.spec.ts`) run against a production build that Playwright serves with `vite preview` on port 4173. Screenshot baselines are stored under `frontend/test/e2e/`. After an intentional visual change, review and update them with:

```sh
docker compose -f compose.frontend.yml exec frontend npm run test:e2e:update
```

The Compose files use the external `rss-reader-net` network so each service group can be started and stopped independently. PostgreSQL must be running before the API; the API waits for it during startup.

## Languages

The interface is available in English, German, and Russian. The first visit defaults to English. Choose a language from the selector in the toolbar or sign-in screen; the preference is stored in browser local storage. Translation catalogs are plain JSON files in `frontend/src/locales/`.

To add a language, create a locale JSON file using the keys in `en.json`, add its language code to `supportedLanguages` and a loader to `catalogLoaders` in `frontend/src/i18n.ts`, and add its native display name to `frontend/src/LanguageSelector.tsx`. English is the source catalog and fallback.

## Agent skills

Project-scoped skills live in `.agents/skills/`. That directory is listed in `.gitignore`, so the skills are not part of the repository and a fresh clone has none. `skills-lock.json` records the externally maintained skills and their upstream sources; the project script restores or updates them with Node.js 24:

```sh
bash scripts/sync-skills.sh install  # restore lock-tracked skills, if needed
bash scripts/sync-skills.sh update   # update lock-tracked skills
```

The script reapplies Pulse-specific rules to the project `handoff` skill after restoration or update. Skills that are not in `skills-lock.json`, such as the Superpowers workflow skills (`obra/superpowers-skills`) and `diagnosing-superpowers`, are not restored by the script; copy them into `.agents/skills/` yourself if you use them. Do not install the project skills globally.

## Production build and deployment

Production uses `compose.production.yml`; it builds release images and does not mount source code. Create a production environment file and replace all `CHANGE_ME` values:

```sh
cp .env.production.example .env.production
```

Set the initial administrator credentials before the first start. Use HTTPS through a TLS reverse proxy, keep `SESSION_COOKIE_SECURE=true`, and do not expose the app's internal HTTP port to an untrusted network. The Compose file binds the web service to `127.0.0.1:8090` by default.

Validate, build, and start production:

```sh
docker compose --env-file .env.production -f compose.production.yml config -q
docker compose --env-file .env.production -f compose.production.yml build --pull
docker compose --env-file .env.production -f compose.production.yml up -d
docker compose --env-file .env.production -f compose.production.yml ps
```

The API applies database migrations on startup. After the first administrator is created, remove `INITIAL_ADMIN_EMAIL` and `INITIAL_ADMIN_PASSWORD` from `.env.production` and recreate the API container:

```sh
docker compose --env-file .env.production -f compose.production.yml up -d --force-recreate api
```

Before updating production, back up the database. Then build and start the updated images with the commands above. The repository also provides an update script:

```sh
./scripts/update-production.sh
```

It requires a clean checkout on `master`, creates a database backup with `scripts/backup-production.sh` (see [Production backups](#production-backups); the update stops if the backup fails), fast-forwards from `origin/master`, validates Compose configuration, rebuilds the services, and waits for health checks. Do not run the local database Compose group against the production data directory.

Article search uses the PostgreSQL extension `pg_trgm`, which migration `016_article_search.sql` creates. It ships with the official PostgreSQL images and is a trusted extension, so the database owner can create it without superuser rights; with another PostgreSQL installation, make sure the contrib extensions are installed. The migrations themselves finish within seconds; the API then fills the search text of existing articles and builds the search index in the background while it already serves requests (about 5 minutes for 500,000 articles in testing). Search works during that time but is slower; the API log reports `article search upgrade completed` when it is done. The update script waits up to 180 seconds for health checks (override with `DEPLOY_WAIT_TIMEOUT=<seconds> scripts/update-production.sh`).

## Data, accounts, and refresh behavior

The first Administrator receives the existing library when the shared-feed migration runs. Administrators can create accounts with the `Administrator` or `Reader` role; public registration is disabled. Each user has their own folders, subscriptions, read states, and saved articles. A normalized feed URL and its articles are shared across users, while personal reading state stays private. Administrators cannot browse other users' libraries.

Each user can subscribe to up to 100 feeds. The API checks the limit before fetching a new source and enforces it again when saving the subscription, including concurrent requests.

The shared feed TTL defaults to 15 minutes (allowed range 5–1,440 minutes). The background scheduler starts about 10 seconds after API start and then checks every minute for due feeds. A shared feed is refreshed at the shortest interval among its subscribers (per-user interval, otherwise the global scheduler interval from the administration screen), but never more often than the shared TTL; a failing feed is retried after the same interval. Feeds are requested conditionally (`ETag`/`Last-Modified`), so unchanged feeds are not downloaded again. The API refreshes unique feeds concurrently using a bounded Go worker pool. Set `RSS_REFRESH_CONCURRENCY` in `.env` to a value from 1 to 32 (default 4). A database lease and the shared TTL prevent duplicate requests for a feed while a refresh is running or until its TTL expires. The API keeps at most `DB_MAX_CONNS` PostgreSQL connections open (default 12, allowed 2–100; keep it below the server's `max_connections`, 100 by default); an invalid value stops the API at startup.

Only public HTTP/HTTPS RSS and Atom URLs are accepted. The API blocks loopback, private, and local network addresses. Pulse stores feed metadata and summaries; the full article remains on its source website.

Before storing a fetched article, Pulse checks its title and summary against the administrator-managed blocked-word dictionary. Matching is case-insensitive and respects whole words; phrases also match across punctuation. Matching articles are skipped and shown in the administration panel with their feed, the matched term, and the account that first added the feed. Other articles from that feed continue to load. The initial Russian, English, and German dictionary covers spam/fraud and erotic/pornographic terms; administrators can add or remove words. The panel shows the 100 most recently blocked articles. Removing a dictionary entry does not restore previously blocked articles.

Sessions use a random HttpOnly cookie with `SameSite=Strict` and expire after 14 days. Passwords are stored as Argon2id hashes. Use HTTPS and `SESSION_COOKIE_SECURE=true` outside local loopback development.

Sign-in is rate limited: after 10 failed attempts for an account within 15 minutes, or 30 attempts from one client address within a minute, the API answers `429` until the window allows again. These counters are kept in PostgreSQL, so they survive restarts and apply across several API instances; if they cannot be read, sign-in fails with an error instead of skipping the check. The API reads the client address from `X-Real-IP` only when the request comes from a network in `TRUSTED_PROXY_CIDRS` (comma-separated CIDRs; empty uses loopback, RFC 1918 and `fc00::/7`, which covers the Docker network of the bundled Nginx; `none` ignores the header). An invalid value stops the API at startup. Each user may have at most 10 feed requests awaiting approval.

The bundled Nginx sends a Content Security Policy and other browser security headers. It does not terminate TLS and therefore does not send `Strict-Transport-Security`; configure HSTS on the TLS-terminating reverse proxy (for example `add_header Strict-Transport-Security "max-age=31536000" always;` in a host-level Nginx).

Pulse can be installed as an app and keeps recently loaded article lists readable offline. This uses a service worker, which browsers only allow over HTTPS (or on `localhost`). The stored articles and the remembered account are deleted from the device on sign-out, when the session expires, and when another account signs in. A reverse proxy in front of Pulse must not cache `/sw.js` or `/manifest.webmanifest` longer than the bundled Nginx does (`no-cache`); otherwise updates reach installed apps late. After a deployment, open pages offer a "Reload" button to switch to the new version. See "Installation and offline reading" in `docs/PROJECT.md`.

### Data retention

A background maintenance job runs about one minute after API start and then every 6 hours. It deletes articles whose publication and first-seen times are both older than `ARTICLE_RETENTION_DAYS` (default 180, allowed 30–3,650) unless at least one user has saved them; read/saved states of deleted articles are removed with them. A refresh does not re-insert dated items older than that period unless they are already stored. Decided (allowed/rejected) quarantine records and non-moderatable legacy records are deleted once neither their decision nor their last appearance in a refresh is newer than `QUARANTINE_RETENTION_DAYS` (default 90, allowed 7–3,650); pending records are kept. Shared feed sources that nobody has subscribed to for `UNSUBSCRIBED_SOURCE_RETENTION_DAYS` (default 30, allowed 1–3,650, counted from the first job run after the last subscriber left) are deleted with their articles, unless a feed request for the URL or a quarantined article awaiting moderation is still pending; subscribing again within that period keeps the source, and after its deletion the URL needs administrator approval again. Expired sessions and sign-in attempts older than their rate-limit window are removed by the same job. Set any of these variables to `0` to disable that cleanup; an invalid value stops the API at startup.

## Backups, logs, and shutdown

### Local database dump

Create a dump of the local development database before moving or upgrading data:

```sh
docker compose -f compose.database.yml exec -T db pg_dump -U pulse pulse > pulse-backup.sql
```

If `POSTGRES_USER` or `POSTGRES_DB` differs in `.env`, use those values instead.

### Production backups

`scripts/backup-production.sh` writes a compressed `pg_dump` archive (custom format) of the production database to `backups/production/pulse-<UTC timestamp>.dump`. The directory gets mode 700 and every backup mode 600. The dump is written to a temporary file and renamed only when it is not empty and `pg_restore --list` can read it. Afterwards the script deletes backups older than `BACKUP_KEEP_DAYS` days (default 14), including plain `.sql` dumps from older versions of the update script, but never the newest backup. The `db` service must be running. Progress goes to stderr and the path of the new backup to stdout.

```sh
./scripts/backup-production.sh
BACKUP_KEEP_DAYS=30 ./scripts/backup-production.sh
```

The backups are stored on the same host as the database. Copy `backups/production/` to another machine or storage regularly.

To back up daily with cron, add a line like this to the crontab of a user who may run `docker` (replace `/srv/pulse` with the checkout path):

```cron
17 3 * * * cd /srv/pulse && ./scripts/backup-production.sh >> /var/log/pulse-backup.log 2>&1
```

Or use a systemd timer. `/etc/systemd/system/pulse-backup.service`:

```ini
[Unit]
Description=Pulse production database backup
Requires=docker.service
After=docker.service

[Service]
Type=oneshot
WorkingDirectory=/srv/pulse
Environment=BACKUP_KEEP_DAYS=14
ExecStart=/srv/pulse/scripts/backup-production.sh
```

`/etc/systemd/system/pulse-backup.timer`:

```ini
[Unit]
Description=Daily Pulse production database backup

[Timer]
OnCalendar=*-*-* 03:17:00
RandomizedDelaySec=15min
Persistent=true

[Install]
WantedBy=timers.target
```

Enable it with `sudo systemctl daemon-reload && sudo systemctl enable --now pulse-backup.timer`, and check it with `systemctl list-timers pulse-backup.timer` and `journalctl -u pulse-backup.service`. The service runs as root unless you add `User=` with an account in the `docker` group.

### Restoring production

```sh
./scripts/restore-production.sh --yes backups/production/pulse-20261101T031700Z.dump
```

Without `--yes` the script only explains what it would replace. With it, the script checks that the archive is readable, saves the current database with `backup-production.sh` (`--skip-safety-backup` skips this when the current database cannot be dumped), stops `api` and `web`, drops and recreates the database, restores the archive with `pg_restore`, starts all services, and waits up to `DEPLOY_WAIT_TIMEOUT` seconds (default 180) for their health checks. Everything written after the backup is lost, including newer sessions, so users may have to sign in again.

Plain SQL dumps (`*.sql` from older versions of the update script) are not restored by the script. Stop `api` and `web`, recreate the database inside the `db` container (`dropdb --force` and `createdb` as `POSTGRES_USER`), load the file with `psql -v ON_ERROR_STOP=1 -U <user> -d <database> < <file>.sql` through `docker compose ... exec -T db`, and start the services again.

**Rolling back an upgrade.** Migrations only move forward; there are no down scripts. To return to the previous version, build the previous commit's images first and then restore the backup that `update-production.sh` made before the upgrade, so the old API never starts against the newer schema:

```sh
git checkout <previous-commit>
docker compose --env-file .env.production -f compose.production.yml build
./scripts/restore-production.sh --yes backups/production/<backup-made-by-the-update>.dump
```

Return to `master` (`git checkout master`) before the next `update-production.sh` run.

### Restore rehearsal

Test the restore regularly, for example every three months and after changing the backup setup, on a disposable copy and never on the production project. The rehearsal of 2026-11-01 used this procedure:

1. Copy `compose.production.yml` into an empty directory and change its `name:` to an unused project name (`pulse-s9-prod`). Copy `scripts/backup-production.sh` and `scripts/restore-production.sh` into a `scripts/` subdirectory, symlink `backend` and `frontend` to the checkout, and create a `.env.production` with a test password, test administrator credentials, and an unused `WEB_PORT`. The relative volume `./data/postgres-production` then points into the copy, so the real data directory is not touched.
2. `docker compose --env-file .env.production -f compose.production.yml up -d --build --wait`, sign in through the web port, and create a folder "Before backup".
3. `scripts/backup-production.sh` created a 42 KB archive with mode 600 in a directory with mode 700.
4. Create a folder "After backup". `scripts/restore-production.sh <archive>` without `--yes` refused; with `--yes` it took a safety backup, stopped `api` and `web`, recreated and restored the database, and all three services became healthy again.
5. After signing in again, the folder list contained "Before backup" and not "After backup": the restore returned the database to the backup. Rotation was checked as well: with `BACKUP_KEEP_DAYS=14`, backups whose modification time was 20 days old were deleted, while the newest backup was kept even with an old modification time.
6. Clean up with `docker compose --env-file .env.production -f compose.production.yml down -v --rmi local` and delete the copy.

### Logs and health checks

The API writes structured logs (Go `log/slog`) to stderr. `LOG_FORMAT` selects `text` (default, used by the local Compose files) or `json` (default in `compose.production.yml`); `LOG_LEVEL` is `debug`, `info` (default), `warn`, or `error`. An invalid value stops the API at startup. Every request produces one `http request` record with `method`, `path` (never the query string, which can contain search terms), `status`, `bytes`, `duration_ms`, `request_id`, and `user_id` for signed-in users; health checks are logged only at `debug`. Every response carries an `X-Request-ID` header with the same ID: a well-formed incoming `X-Request-ID` (up to 64 characters of letters, digits, `.`, `_`, `:` and `-`) is kept, anything else is replaced by a generated ID. Include it when reporting a problem. The refresh scheduler logs one `rss refresh cycle finished` record per cycle or manual refresh with the number of sources (`targets`) and how many were `refreshed`, `not_modified`, `failed`, or `skipped`; cycles without due sources are logged only at `debug`.

Follow logs with `docker compose -f <compose-file> logs -f api`. With JSON logs, a filter such as `docker compose --env-file .env.production -f compose.production.yml logs --no-log-prefix api | jq 'select(.status >= 500)'` shows failed requests.

`GET /api/health/live` reports that the API process is running without contacting the database. `GET /api/health/ready` also checks the database (2-second timeout) and answers `503` when it is unavailable; the Compose health checks use it. `GET /api/health` remains an alias of the readiness check. The API does not export Prometheus metrics yet.

### Database maintenance and slow queries

Open `psql` in the production database container with:

```sh
docker compose --env-file .env.production -f compose.production.yml exec db sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
```

Table and index sizes, dead rows, and the last vacuum:

```sql
SELECT relname,
       pg_size_pretty(pg_table_size(relid))          AS table_size,
       pg_size_pretty(pg_indexes_size(relid))        AS index_size,
       pg_size_pretty(pg_total_relation_size(relid)) AS total_size,
       n_live_tup, n_dead_tup, last_vacuum, last_autovacuum
FROM pg_stat_user_tables
ORDER BY pg_total_relation_size(relid) DESC
LIMIT 10;
```

Autovacuum is enabled by default and suffices for normal operation: deleted and updated rows leave free space in the table file that new articles reuse. The background search upgrade rewrites every article once, which makes `articles` grow by up to about the size of its data (from 1.2 GB to 1.7 GB for 500,000 articles in testing); it then runs `VACUUM (ANALYZE) articles` itself (about 10 seconds there, without blocking reads or writes), so the space becomes reusable but the file keeps its size. A manual `VACUUM (ANALYZE, VERBOSE) articles;` is always safe while Pulse runs. Reclaim the disk space only when it is tight: `VACUUM (FULL, ANALYZE) articles;` rewrites the table and blocks every read and write of it until it finishes (2 minutes 13 seconds for 500,000 articles in testing) and needs free space for a full copy, so back up first, stop `web` and `api` (`docker compose --env-file .env.production -f compose.production.yml stop web api`), run it, and start them again (`... up -d --wait`). `pg_repack` can do this online but is not part of the official PostgreSQL image. Details are in "Database maintenance" in `docs/PROJECT.md`.

The production database records query statistics with `pg_stat_statements`, logs statements that run at least `DB_LOG_MIN_DURATION_MS` milliseconds (`.env.production`, default 500; `-1` disables, `0` logs everything; parameter values are never logged), and measures I/O times (`track_io_timing`). These are PostgreSQL start arguments in `compose.production.yml`: the first update that adds them and every change of `DB_LOG_MIN_DURATION_MS` recreates the `db` container, which interrupts database access for a few seconds; `scripts/update-production.sh` handles this through `up -d`. Show the slowest statements in total (order by `mean_exec_time` for individually slow ones):

```sql
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

SELECT calls,
       round(total_exec_time::numeric, 1) AS total_ms,
       round(mean_exec_time::numeric, 2)  AS mean_ms,
       left(regexp_replace(query, '\s+', ' ', 'g'), 120) AS query
FROM pg_stat_statements
WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
ORDER BY total_exec_time DESC
LIMIT 15;
```

`SELECT pg_stat_statements_reset();` starts a new measurement period. Slow statements in the log: `docker compose --env-file .env.production -f compose.production.yml logs db | grep 'duration:'`. See "Slow-query visibility" in `docs/PROJECT.md`.

### Shutdown

Stop the local stack with:

```sh
docker compose -f compose.frontend.yml down
docker compose -f compose.backend.yml down
docker compose -f compose.database.yml down
```

PostgreSQL data remains in `data/postgres/`. Do not remove it if you want to keep accounts and subscriptions.

## Project layout

- `frontend/` — React/TypeScript app, Nginx config, and frontend container.
- `backend/` — Go API, RSS/Atom parser, and PostgreSQL migrations.
- `frontend/test/` and `backend/test/` — tests organized by component structure.
- `data/postgres/` — bind-mounted local PostgreSQL data.
- `compose.frontend.yml`, `compose.backend.yml`, `compose.database.yml` — independent local service groups.
- `compose.production.yml` — production build and deployment.
- `scripts/` — production update, backup, and restore scripts, and the agent-skill sync script.
- `backend/openapi/openapi.json` — OpenAPI 3.1 description of the HTTP API.
