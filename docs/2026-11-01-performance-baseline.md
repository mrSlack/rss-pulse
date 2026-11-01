# Performance baseline (roadmap task 4.1)

Date: 2026-11-01. This is the reference for later performance work (roadmap tasks 4.3 and 4.4,
findings PERF-2, PERF-3, PERF-5). Compare new measurements only against numbers taken with the
same seed parameters, PostgreSQL image, settings, and procedure.

## Environment

- PostgreSQL `postgres:18.6-alpine3.24` (`PostgreSQL 18.6 on x86_64-pc-linux-musl`), data directory on
  `tmpfs`, image defaults: `shared_buffers=128MB`, `work_mem=4MB`, `effective_cache_size=4GB`, `jit=on`.
- Host: 8 vCPU, 30 GB RAM, shared with other workloads. The load average was 8–16 during the
  measurements, so absolute timings carry roughly ±30 % noise between runs; plan shapes and buffer
  counts are stable.
- Schema: all migrations up to `010_quarantine_eligibility.sql` (state of commit `ea2569f`).
  The seed tool also applies stage 4's `012_article_retention.sql`; its index
  `user_article_states_article_idx` was dropped before the baseline runs so that the baseline
  reflects the pre-stage-4 schema (see "After stage 4" for the effect of the index).
- Queries: copied from `backend/internal/store` at `ea2569f` into
  [`2026-11-01-performance-baseline.sql`](2026-11-01-performance-baseline.sql) with literal
  parameters (custom plans). The application sends them through pgx with bind parameters.

## Dataset

Seeded with `backend/cmd/seed` (commit `00ddd9e`) using the defaults:

| Flag | Value |
| --- | --- |
| `-users` | 50 (including `admin@seed.invalid`) |
| `-sources` | 500 |
| `-articles-per-source` | 1000 (500,000 articles) |
| `-subscriptions-per-user` | 60 ±25 % (actual 45–75, 2,925 subscriptions) |
| `-folders-per-user` | 6 (80 % of subscriptions are in a folder) |
| `-read-ratio` / `-saved-ratio` | 0.30 / 0.02 of each user's visible articles |
| `-search-term-ratio` | 0.02 of articles contain the word `market` |
| `-history-days` | 400 (publication dates evenly spread; `created_at` = publication + 0–120 min) |
| `-quarantine-records` | 2000 |
| `-expired-sessions-per-user` | 3 (plus one active session per user) |
| `-random-seed` | 20261101 |

Resulting volume after `VACUUM (ANALYZE)`: 500,000 articles (average summary 1,426 characters of
HTML-like mixed English/German/Russian text; 275,002 published more than 180 days ago),
919,440 `user_article_states` rows (878,233 read, 58,856 saved), 2,000 quarantine records,
200 sessions (150 expired). Database size 1,014 MB (`articles` 892 MB, `user_article_states` 110 MB);
the hot working set fits in shared buffers, so all runs are cache-hit only.

Representative user: id 17 (`user017@seed.invalid`), 60 subscriptions, 6 folders, 60,000 visible
articles, 18,076 read, 1,177 saved, 1,213 containing `market`. Its active session hash is
`6d72228edf6ca15ab5388211439db01c25b0095638fea04fa6412a2f68d2f6a9`
(`sha256("seed-session-17")`, see `SessionToken` in `backend/cmd/seed/seed.go`).

## Procedure

Every query ran once as a warm-up, then three times with `EXPLAIN (ANALYZE, BUFFERS)`; the tables
show the median execution time, the median planning time, and the three sorted execution times.
`ListArticlesForUser` uses the frontend page size (`limit=20`, so `LIMIT 21`, `OFFSET 0`).
The session `DELETE` ran inside `BEGIN … ROLLBACK` so that every run saw the same 150 expired rows.

```sh
docker network create pulse-s4-net
docker run -d --name pulse-s4-db --network pulse-s4-net --tmpfs /var/lib/postgresql \
  -e POSTGRES_USER=pulse -e POSTGRES_PASSWORD=test-only -e POSTGRES_DB=pulse_test postgres:18.6-alpine3.24
docker exec pulse-s4-db psql -U pulse -d pulse_test -c "CREATE DATABASE pulse_baseline"

# from the repository root (about 1.5 minutes)
docker run --rm --network pulse-s4-net -u "$(id -u):$(id -g)" -e HOME=/tmp -v "$PWD/backend:/src" -w /src \
  golang:1.27.3-alpine3.24 go run ./cmd/seed -confirm-destroy-data \
  -dsn 'postgres://pulse:test-only@pulse-s4-db:5432/pulse_baseline?sslmode=disable'

docker exec pulse-s4-db psql -U pulse -d pulse_baseline \
  -c "DROP INDEX user_article_states_article_idx" -c "VACUUM (ANALYZE)"

# one measurement; repeat 1 + 3 times per query name (see the labels in the .sql file)
docker exec -i pulse-s4-db psql -X -q -U pulse -d pulse_baseline -v uid=17 \
  -v token_hash=6d72228edf6ca15ab5388211439db01c25b0095638fea04fa6412a2f68d2f6a9 \
  -v query=articles_all -f - < docs/2026-11-01-performance-baseline.sql
```

## Results (pre-stage 4)

| Query | Median exec (ms) | Median plan (ms) | Runs (ms) | Shared buffers hit | Key plan nodes |
| --- | ---: | ---: | --- | ---: | --- |
| `ListArticlesForUser` all | 140.6 | 5.0 | 124.9 / 140.6 / 142.0 | 14,598 | Seq Scan `user_feeds` (60 rows) → Index Scan `articles_source_published_idx` ×60 (60,000 rows) → Hash Left Join with Bitmap Heap Scan `user_article_states` (18,893 rows) → top-N heapsort of 60,000 rows |
| `ListArticlesForUser` unread | 81.7 | 3.0 | 73.9 / 81.7 / 85.1 | 14,598 | Same plan; join filter removes 18,076 read rows, top-N over 41,924 |
| `ListArticlesForUser` saved | 55.4 | 3.0 | 52.1 / 55.4 / 58.7 | 14,598 | Same plan; join filter removes 58,823 rows, top-N over 1,177 |
| `ListArticlesForUser` search `market` | 1,329.8 | 2.9 | 1,314.6 / 1,329.8 / 1,346.2 | 14,598 | Same plan; `strpos(lower(title || ' ' || summary))` filter on all 60,000 rows (≈22 ms per source scan), 1,213 matches |
| `ListFeedsForUser` | 104.2 | 2.9 | 99.4 / 104.2 / 118.0 | 14,583 (+ temp 997 read / 999 written) | Merge Join `feed_sources`/`user_feeds` → Index Scan `articles_source_published_idx` ×60 (60,000 rows) → Hash Left Join states → Sort 60,000 rows (external merge, 7,976 kB on disk) → GroupAggregate |
| `ListFoldersForUser` | 96.3 | 2.6 | 86.8 / 96.3 / 106.5 | — | Merge Left Join folders/`user_feeds` (42 foldered subscriptions) → Index Scan articles ×42 (42,000 rows) → Index Scan `user_article_states_pkey` ×42,000 → GroupAggregate |
| `DueSources` | 0.65 | 1.6 | 0.58 / 0.65 / 1.00 | — | Seq Scan `feed_sources` (270 due of 500) → Nested Loop Semi Join with Index Only Scan `user_feeds_source_idx` → Sort |
| `UserForSession` statement 1: `DELETE FROM sessions WHERE expires_at <= NOW()` | 0.15 | 0.54 | 0.14 / 0.15 / 0.15 | — | Seq Scan `sessions` (200 rows, 150 deleted and rolled back) |
| `UserForSession` statement 2: session `SELECT` | 0.14 | 1.9 | 0.14 / 0.14 / 0.18 | — | Hash Join `users` with Seq Scan `sessions` filtered by `token_hash` and `expires_at` |

Observations for later stages:

- Every article list materializes all 60,000 articles visible to the user before the top-N sort,
  so its cost grows linearly with retained articles per subscription (PERF-1, PERF-2).
- Search costs about 1.3 s for one page, because the substring filter runs on every visible
  article including full HTML summaries (PERF-3).
- The unread counters in `ListFeedsForUser` and `ListFoldersForUser` read all visible articles and
  their states (about 0.1 s each). The feed list also spills its 60,000-row sort to disk with the
  default `work_mem` (PERF-2).
- The session statements are cheap in isolation; their cost is the extra round trip and a write
  statement on every authenticated request (PERF-4), measured below.

## After stage 4

Stage 4 removes the `DELETE` from `UserForSession`; expired sessions are deleted by the
maintenance job. Integration test `TestDeleteExpiredSessionsAndRequestPathDoesNotWrite`
(`backend/test/internal/store/retention_test.go`) proves that lookups leave expired rows in place
and still reject them. Measurements were taken on the same database after recreating
`user_article_states_article_idx` and running `ANALYZE user_article_states`:

| Query | Median exec (ms) | Median plan (ms) | Runs (ms) | Key plan nodes |
| --- | ---: | ---: | --- | --- |
| `UserForSession` (`SELECT` only) | 0.14 | 2.1 | 0.12 / 0.14 / 0.19 | unchanged: Hash Join `users` with Seq Scan `sessions` |

Request-path comparison with `pgbench` (8 clients, 4 threads, 15 s, `-M prepared`, the
representative token, three interleaved runs each; all expired sessions had been removed by the
first run, so the `DELETE` matched no rows, which is the steady state):

| Script | Median TPS | Median latency (ms) | Runs (TPS) |
| --- | ---: | ---: | --- |
| Before: `DELETE` expired sessions + session `SELECT` | 18,795 | 0.426 | 18,091 / 18,795 / 22,949 |
| After stage 4: session `SELECT` only | 37,300 | 0.214 | 29,052 / 37,300 / 45,135 |

```sh
docker exec pulse-s4-db pgbench -U pulse -n -M prepared -c 8 -j 4 -T 15 -f /tmp/session_before.sql pulse_baseline
docker exec pulse-s4-db pgbench -U pulse -n -M prepared -c 8 -j 4 -T 15 -f /tmp/session_after.sql pulse_baseline
```

`session_before.sql` contains statement 1 and statement 2 from the table above, `session_after.sql`
only statement 2, both with the representative token hash. A preceding, non-interleaved series of
three "before" runs (which deleted the 150 expired sessions) ranged from 14,707 to 38,638 TPS, which
shows the host noise; treat the ratio, not the absolute TPS, as the result.

The `012_article_retention.sql` index did not change any plan above (identical node structure for all
nine queries); timings with the index were within the run-to-run noise.

The first retention pass on the seeded data (180 days, batches of 5,000, the
`DeleteExpiredArticles` statement run from `psql`) deleted 244,704 articles in 49 batches in 30.6 s
wall time; the slowest batch took 1.3 s including `docker exec` start-up. 255,296 articles (old
articles saved by at least one user, and newer ones) and 498,190 states remained.
