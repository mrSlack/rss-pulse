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
- Run Go unit and PostgreSQL integration tests: `docker compose -f compose.backend.yml run --rm -e REQUIRE_INTEGRATION=1 api sh -c 'TEST_DATABASE_URL="postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@db:5432/$POSTGRES_DB?sslmode=disable" go test -count=1 ./...'` (requires the database project to be running; each test creates and drops its own temporary schema). Without `TEST_DATABASE_URL`, integration tests are skipped; `REQUIRE_INTEGRATION=1` turns such a skip into a failure so a run cannot silently omit them.
- Run frontend unit tests: `docker compose -f compose.frontend.yml exec frontend npm run test -- --run`
- Lint the frontend: `docker compose -f compose.frontend.yml exec frontend npm run lint` (must report zero errors)
- Run frontend Playwright visual and browser tests: `docker compose -f compose.frontend.yml exec frontend npm run test:e2e`
- Update Playwright screenshot baselines after intentional visual changes: `docker compose -f compose.frontend.yml exec frontend npm run test:e2e:update`
- Build the frontend: `docker compose -f compose.frontend.yml exec frontend npm run build`
- Rebuild frontend assets: `docker compose -f compose.frontend.yml restart frontend nginx`
- View service status/logs: `docker compose -f compose.frontend.yml ps` and `docker compose -f compose.backend.yml logs -f api`.

The application is served at `http://localhost:8090` by default. The frontend source is bind-mounted in its builder container; backend source is bind-mounted in the API container; PostgreSQL files are persisted in `data/postgres/`.

## Coding Style and Testing

Use `gofmt` for Go; keep frontend code typed and components focused. Add deterministic tests for behavior changes and run the documented Go test command from the repository root. Do not claim a command passes unless it has been run.

Go tests that use only exported APIs live in external test packages under `backend/test/internal/<package>/` (for example `backend/test/internal/store/` with `package store_test`). Tests of unexported code live beside their package as `*_internal_test.go` or `*_test.go` in the same package (for example `backend/internal/httpapi/store_errors_internal_test.go` and `backend/cmd/api/logging_test.go`). Integration tests take `TEST_DATABASE_URL` and create and drop their own schema or database. Frontend unit tests live in `frontend/test/`, Playwright tests in `frontend/test/e2e/`.

The HTTP API is described in `backend/openapi/openapi.json` (OpenAPI 3.1). When you add, remove, or rename a route or a handler error code, update that file; `backend/test/internal/httpapi/openapi_test.go` fails otherwise.

## Manual pre-merge checklist

There is no CI. Before merging, run every step from the repository root and fix all findings. The Go steps use the API container of `compose.backend.yml` (the database project must be running for the tests); any `golang:1.27` container with `backend/` mounted works the same way.

1. Go formatting, vet, and module tidiness — `gofmt -l` must print nothing, `go mod tidy -diff` must print no diff:
   ```sh
   docker compose -f compose.backend.yml run --rm api sh -c 'gofmt -l . && go vet ./... && go mod tidy -diff'
   ```
2. Go unit and PostgreSQL integration tests (no skipped integration tests):
   ```sh
   docker compose -f compose.backend.yml run --rm -e REQUIRE_INTEGRATION=1 api sh -c 'TEST_DATABASE_URL="postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@db:5432/$POSTGRES_DB?sslmode=disable" go test -count=1 ./...'
   ```
3. Frontend lint (zero errors), type check, unit tests, and production build:
   ```sh
   docker compose -f compose.frontend.yml exec frontend npm run lint
   docker compose -f compose.frontend.yml exec frontend npm run typecheck
   docker compose -f compose.frontend.yml exec frontend npm run test -- --run
   docker compose -f compose.frontend.yml exec frontend npm run build
   ```
4. Playwright end-to-end tests against a real API and database in the isolated `compose.e2e.yml` stack, then remove it:
   ```sh
   docker compose -f compose.e2e.yml run --build --rm e2e sh -c 'npm ci && npm run test:e2e'
   docker compose -f compose.e2e.yml down -v
   ```
5. Compose files and shell scripts, when changed:
   ```sh
   for file in compose.database.yml compose.backend.yml compose.frontend.yml compose.e2e.yml; do docker compose -f "$file" config -q; done
   POSTGRES_PASSWORD=check docker compose --env-file .env.production.example -f compose.production.yml config -q
   for script in scripts/*.sh; do bash -n "$script"; done
   docker run --rm -v "$PWD:/mnt:ro" -w /mnt koalaman/shellcheck:stable scripts/*.sh
   ```
6. Optional dependency audits:
   ```sh
   docker compose -f compose.backend.yml run --rm api go run golang.org/x/vuln/cmd/govulncheck@latest ./...
   docker compose -f compose.frontend.yml exec frontend npm audit --omit=dev
   ```

Update Playwright screenshot baselines only for intentional visual changes and review the changed images before committing.

## Security and Configuration

Never commit credentials, tokens, or machine-specific configuration. `.env.example` is the safe configuration template; `.env` and database runtime files are ignored. Keep SQL parameterized and validate feed URLs before server-side fetching.

## Changes

Use short, imperative commit subjects when Git metadata is available. Describe the reason for each change and list verification performed; include screenshots for visual changes.

## Session Handoffs

Save handoffs in `SESSION_HANDOFF.md` at the repository root, including when using the `handoff` skill. This project-specific user preference overrides the skill's temporary-directory default. Prepend each new entry with the current date (and time when useful), keeping entries in reverse chronological order. Preserve all existing entries. Write handoff content in English and reference existing documentation and diffs rather than duplicating them.

Keep each entry concise: a few bullets covering current project state, work completed, what is verified or broken/unverified, remaining work, and decisions with brief reasons. Include only what the next agent needs to continue; omit conversational history, repeated instructions, and long descriptions.
