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
docker compose -f compose.frontend.yml exec frontend npm run test -- --run
docker compose -f compose.frontend.yml exec frontend npm run test:e2e
```

Playwright starts a local Vite server and uses deterministic API fixtures. Screenshot baselines are stored under `frontend/test/e2e/`. After an intentional visual change, review and update them with:

```sh
docker compose -f compose.frontend.yml exec frontend npm run test:e2e:update
```

The Compose files use the external `rss-reader-net` network so each service group can be started and stopped independently. PostgreSQL must be running before the API; the API waits for it during startup.

## Languages

The interface is available in English, German, and Russian. The first visit defaults to English. Choose a language from the selector in the toolbar or sign-in screen; the preference is stored in browser local storage. Translation catalogs are plain JSON files in `frontend/src/locales/`.

To add a language, create a locale JSON file using the keys in `en.json`, add its language code to `supportedLanguages` and `resources` in `frontend/src/i18n.ts`, and add its native display name to `frontend/src/LanguageSelector.tsx`. English is the source catalog and fallback.

## Agent skills

Project-scoped skills are stored in `.agents/skills/` and included with the repository. After cloning, open the project root in Codex; no global installation is required. `skills-lock.json` tracks skills with upstream sources. Node.js 24 is used by the project script to restore or update those skills:

```sh
bash scripts/sync-skills.sh install  # restore lock-tracked skills, if needed
bash scripts/sync-skills.sh update   # update lock-tracked skills
```

The script reapplies Pulse-specific rules to the project `handoff` skill after restoration or update. Superpowers workflow skills are vendored from the current `obra/superpowers-skills` source; `diagnosing-superpowers` remains because it is no longer published there, and the older `using-superpowers` name is retained alongside its successor, `using-skills`. Do not install the project skills globally.

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

It requires a clean checkout on `master`, saves a database dump under `backups/production/`, fast-forwards from `origin/master`, validates Compose configuration, rebuilds the services, and waits for health checks. Do not run the local database Compose group against the production data directory.

## Data, accounts, and refresh behavior

The first Administrator receives the existing library when the shared-feed migration runs. Administrators can create accounts with the `Administrator` or `Reader` role; public registration is disabled. Each user has their own folders, subscriptions, read states, and saved articles. A normalized feed URL and its articles are shared across users, while personal reading state stays private. Administrators cannot browse other users' libraries.

The shared feed TTL defaults to 15 minutes (allowed range 5–1,440 minutes). The background scheduler currently runs every 15 minutes. Scheduler and per-user refresh intervals can be saved in the administration screen; they are not yet connected to scheduling. The API refreshes unique feeds concurrently using a bounded Go worker pool. Set `RSS_REFRESH_CONCURRENCY` in `.env` to a value from 1 to 32 (default 4). A database lease and the shared TTL prevent duplicate requests for a feed while a refresh is running or until its TTL expires.

Only public HTTP/HTTPS RSS and Atom URLs are accepted. The API blocks loopback, private, and local network addresses. Pulse stores feed metadata and summaries; the full article remains on its source website.

Sessions use a random HttpOnly cookie with `SameSite=Strict` and expire after 14 days. Passwords are stored as Argon2id hashes. Use HTTPS and `SESSION_COOKIE_SECURE=true` outside local loopback development.

## Backups, logs, and shutdown

Create a database dump before moving or upgrading data:

```sh
docker compose -f compose.database.yml exec -T db pg_dump -U pulse pulse > pulse-backup.sql
```

If `POSTGRES_USER` or `POSTGRES_DB` differs in `.env`, use those values instead. Follow logs with `docker compose -f <compose-file> logs -f`. Stop the local stack with:

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
