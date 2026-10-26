# Browser tests with real accounts

The isolated browser stack uses a separate PostgreSQL container backed by
temporary memory storage. It does not mount `data/postgres/`, publish database
or API ports, or connect to the development database.

Run the complete browser suite from the repository root:

```sh
docker compose -f compose.e2e.yml run --build --rm e2e sh -c 'npm ci && npm run test:e2e'
```

The stack bootstraps an administrator and creates the reader account through
the administrator API during Playwright global setup. Test-only credentials:

| Role | Email | Password |
| --- | --- | --- |
| Administrator | `admin@pulse.test` | `test-only-admin-password-2026` |
| Reader | `reader@pulse.test` | `test-only-reader-password-2026` |

These fixed credentials are for the isolated E2E stack only. Never reuse them
outside tests. The test database is discarded when its Compose stack is
removed with `docker compose -f compose.e2e.yml down`.
