# API Reliability Review — October 25, 2026

Scope: HTTP API, authentication and authorization, RSS fetching and parsing,
the scheduler, user library queries, and the API client. The UI was not changed.
This is a targeted code review, not a complete security audit or load test.

## Existing Design

- Personal subscriptions and article states are separate from shared RSS sources;
  library queries are scoped to the user ID.
- Source refreshes use leases with tokens and expiration times, and results are
  committed within transactions.
- RSS fetching limits request duration, response size, and redirects;
  addresses are also checked when establishing connections.
- Passwords are hashed with Argon2id; sessions use HttpOnly cookies,
  and requests that modify data undergo origin validation.

## Fixes

1. `decodeJSON` accepted `null`: the standard Go decoder left the struct with
   zero values without returning an error. For profile updates, this cleared
   the fields. API payloads must now be JSON objects; `null` is rejected with
   HTTP 400. The 1 MiB limit, rejection of unknown fields, and check for a second
   JSON value remain in place.
2. The client cast arbitrary error response bodies to a TypeScript type without
   runtime validation. `null` caused a TypeError, and an object instead of an
   `error` string produced an incorrect message. The response shape and the
   types of `code` and `error` are now checked; invalid data falls back to the
   existing localized message containing the HTTP status.

Both defects were reproduced with regression tests before being fixed.

## Verification

- `go test ./... -count=1` — passed with `TEST_DATABASE_URL` pointing to a
  separate temporary PostgreSQL 18 instance; integration tests ran.
- `go vet ./...` — passed.
- `npm run test -- --run` — 33 tests passed across 9 files.
- `npm run build` — passed, including TypeScript checking.
- `git diff --check` — passed. Browser E2E tests were not run.

## Follow-up Opportunities

- The scheduler wakes every 15 minutes, while the minimum source TTL is
  5 minutes: TTL determines refresh eligibility but does not guarantee a
  refresh every 5 minutes. Saved user intervals also do not affect source
  selection in `DueSources`. Changes to these rules should be addressed in
  a separate task with explicit scheduling semantics.
- During shutdown, lease release uses `context.Background()` without a timeout.
  A cleanup context with a timeout and waiting for the background worker to
  finish would make shutdown more predictable.
- Go integration tests require `TEST_DATABASE_URL` and are otherwise skipped.
  CI should explicitly provide a separate PostgreSQL instance and verify that
  these tests actually ran.
