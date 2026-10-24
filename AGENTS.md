# Repository Guidelines

## Project Structure

- `frontend/` contains the React, TypeScript, Vite build, nginx configuration, and frontend Dockerfile.
- `backend/` contains the Go API, RSS parsing and refresh workers, PostgreSQL migrations, and backend Dockerfile.
- `data/postgres/` is the bind-mounted PostgreSQL data directory. Keep its runtime contents local and out of version control.
- `docs/` contains the design and implementation plan.
- `compose.frontend.yml`, `compose.backend.yml`, and `compose.database.yml` are the independent Compose projects. They communicate over the external Docker network `rss-reader-net`.

## Build, Test, and Development

Run commands from the repository root. First copy `.env.example` to `.env` and create the shared network with `docker network create rss-reader-net`.

- Start PostgreSQL: `docker compose -f compose.database.yml up -d --build`
- Start the API: `docker compose -f compose.backend.yml up -d --build`
- Build and start frontend/nginx: `docker compose -f compose.frontend.yml up -d --build`
- Validate Compose configuration: `docker compose -f compose.database.yml config`, and likewise for the frontend and backend files.
- Run Go unit tests: `docker compose -f compose.backend.yml run --rm api go test ./...`
- Run frontend unit tests: `docker compose -f compose.frontend.yml exec frontend npm run test -- --run`
- Run frontend Playwright visual and browser tests: `docker compose -f compose.frontend.yml exec frontend npm run test:e2e`
- Update Playwright screenshot baselines after intentional visual changes: `docker compose -f compose.frontend.yml exec frontend npm run test:e2e:update`
- Build the frontend: `docker compose -f compose.frontend.yml exec frontend npm run build`
- Rebuild frontend assets: `docker compose -f compose.frontend.yml restart frontend nginx`
- View service status/logs: `docker compose -f compose.frontend.yml ps` and `docker compose -f compose.backend.yml logs -f api`.

The application is served at `http://localhost:8090` by default. The frontend source is bind-mounted in its builder container; backend source is bind-mounted in the API container; PostgreSQL files are persisted in `data/postgres/`.

## Coding Style and Testing

Use `gofmt` for Go. Keep Go tests beside the package in `*_test.go`; keep frontend code typed and components focused. Add deterministic tests for behavior changes and run the documented Go test command from the repository root. Do not claim a command passes unless it has been run.

## Security and Configuration

Never commit credentials, tokens, or machine-specific configuration. `.env.example` is the safe configuration template; `.env` and database runtime files are ignored. Keep SQL parameterized and validate feed URLs before server-side fetching.

## Changes

Use short, imperative commit subjects when Git metadata is available. Describe the reason for each change and list verification performed; include screenshots for visual changes.
