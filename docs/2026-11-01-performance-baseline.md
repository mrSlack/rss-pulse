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

## After stage 7

Stage 7 (roadmap tasks 4.3–4.5; PERF-2, PERF-3, PERF-5, PERF-7, PERF-8) changes search, the unread
counters, and ingestion, and adds migrations `016_article_search.sql` and `017_unread_counters.sql`.
Measured on 2026-11-01 with the same image, settings, seed flags, and representative user (id 17).

Both databases ran side by side in one `tmpfs` cluster (`pulse-s7-db`):

- **Before:** `pulse_before`, seeded by the base commit `4612291` (all migrations up to `012_`,
  including `user_article_states_article_idx`, so slightly different from the pre-stage-4 numbers
  above): 1,014 MB, the same volume as the baseline.
- **After:** `pulse_after`, seeded by stage 7 (the seed tool now fills `search_text` with
  `store.ArticleSearchText`; the random dataset is identical): 1,929 MB, of which `articles` is
  1,770 MB including the trigram index `articles_search_text_trgm_idx` (521 MB) and
  `articles_source_published_id_idx` (31 MB); `user_article_states_read_idx` is 35 MB. Seeding took
  5 min 32 s instead of 54 s because `COPY` maintains the trigram index.

Two databases no longer fit in the 128 MB of shared buffers, so plans show `shared read` from the
OS page cache (still memory on `tmpfs`). The load average was 1.1–5.2 during the runs (8 vCPU);
before and after runs were interleaved per query, and medians are reported.

### Queries

Every query ran once as a warm-up, then five times with `EXPLAIN (ANALYZE, BUFFERS)`. The list queries
are unchanged and come from [`2026-11-01-performance-baseline.sql`](2026-11-01-performance-baseline.sql);
the new search and counter queries are in
[`2026-11-01-performance-stage-7.sql`](2026-11-01-performance-stage-7.sql) (search with
`-v pattern=%market%`, the pattern the application builds for the query `market`).

| Query | Baseline (pre-stage 4, ms) | Before (ms) | After (ms) | After runs (ms) | Buffers before → after | Key plan nodes after |
| --- | ---: | ---: | ---: | --- | --- | --- |
| `ListArticlesForUser` all | 140.6 | 73.1 | 57.5 | 55.8 / 56.9 / 57.5 / 58.1 / 69.4 | 14,598 → 12,996 | unchanged shape, Index Scan `articles_source_published_id_idx` ×60 |
| `ListArticlesForUser` unread | 81.7 | 56.4 | 49.3 | 49.1 / 49.2 / 49.3 / 51.3 / 63.5 | — | unchanged |
| `ListArticlesForUser` saved | 55.4 | 42.5 | 40.0 | 38.1 / 39.4 / 40.0 / 40.0 / 52.5 | — | unchanged |
| `ListArticlesForUser` search `market` | 1,329.8 | 897.1 | 37.3 | 35.4 / 36.0 / 37.3 / 44.4 / 47.9 | 14,598 → 21,020 | Bitmap Index Scan `articles_search_text_trgm_idx` (9,938 candidates in all sources) → Parallel Bitmap Heap Scan → Hash Join `user_feeds` (1,213 rows) → top-N sort |
| `ListFeedsForUser` | 104.2 | 73.8 | 25.3 | 25.1 / 25.2 / 25.3 / 26.0 / 28.1 | 14,585 + temp 997/999 → 1,782, no temp | Index Only Scan `user_article_states_read_idx` (18,076 read states) hash-joined with Index Only Scan `articles_source_published_id_idx` ×60 → HashAggregate; per-source Index Only Scan count |
| `ListFoldersForUser` | 96.3 | 67.0 | 19.1 | 18.8 / 18.9 / 19.1 / 19.2 / 19.8 | 149,387 → 1,103 | same as the feed list for the 42 foldered subscriptions, grouped by folder |

Before-runs (ms): all 61.0 / 62.0 / 73.1 / 102.5 / 107.6; unread 54.6 / 56.1 / 56.4 / 57.3 / 83.0;
saved 40.7 / 41.2 / 42.5 / 43.0 / 69.3; search 886.2 / 892.5 / 897.1 / 906.8 / 914.3;
feeds 73.0 / 73.3 / 73.8 / 73.9 / 95.6; folders 65.8 / 66.5 / 67.0 / 71.2 / 96.2.
Median planning time stayed between 2 and 3.5 ms for every query.

The feed list no longer sorts 60,000 rows, so the 8 MB spill to disk is gone.

**Migration 017 is justified by these measurements.** The same new counter queries without its
indexes (dropped inside a rolled-back transaction on `pulse_after`, with the old
`articles_source_published_idx` recreated) took 25.4 ms (feeds) and 22.1 ms (folders) only because
PostgreSQL used two parallel workers; they read 73,322 and 72,951 buffers through 18,076 primary-key
lookups of articles, and with `max_parallel_workers_per_gather = 0` they took 77 ms and 85 ms. With the
indexes a single process takes 25.3 ms and 19.1 ms and reads 1,782 and 1,103 buffers. An earlier
version that left the subscription CTE materialized (the default for a CTE referenced twice) took
80 ms on a database upgraded through 016 and 017, because the planner then expected one
subscription and re-aggregated the read counts per subscription; the CTE is declared
`NOT MATERIALIZED`.

### Search through the application

`go run ./cmd/perfbench -mode search` calls `ListArticlesForUser` (page size 20) on a single
pooled connection after ten warm-up searches for `market`, so later queries reuse the cached
statement as a busy API connection would. Five runs each, medians in ms:

| Query | Before | After | After plan |
| --- | ---: | ---: | --- |
| `market` (2 % of articles) | 1,028.9 | 35.8 | trigram index |
| `straße` (common seed word) | 1,013.4 | 456.6 | per-source scan of `search_text` (planner choice) |
| `безопасность` (common seed word) | 989.2 | 472.5 | per-source scan |
| `ma` (two letters) | 1,051.4 | 439.1 | `STRPOS`, per-source scan |
| `ent` (in most articles) | 924.6 | 434.8 | per-source scan |
| `energy climate` (two common words) | 983.3 | 3,092.2 | trigram index, 205,229 candidates for 417 matches of the user |

Without the stage 7 query mode (`pgx.QueryExecModeCacheDescribe`), the cached statement switched to
a generic plan after five executions and always read the trigram index: `ma` then took 7.7 s and
`ent`, `data`, or `energy` about 2 s. For patterns whose words are all shorter than three
characters the index cannot narrow the search (`%ma%`: 346,690 candidates, 7.4 s with a forced
index plan), so they use `STRPOS` instead of `LIKE`.

The trigram index serves selective patterns of three or more characters; the planner falls back to
scanning the user's articles for patterns that occur in most articles, which is still about twice
as fast as before because `search_text` is shorter than the HTML summary and already lower-cased.
The remaining slow case is a phrase of words that each occur in a large share of articles: trigrams
cannot express word adjacency, so every article containing both words is a candidate that must be
rechecked, and the planner does not see that cost. The seed vocabulary (about 100 words per
language) makes this extreme; with real feeds two common words co-occur far less often.

The SQL backfill of migration 016 produced exactly the Go value (`store.ArticleSearchText`) for all
500,000 seeded articles (mixed English, German, and Russian HTML-like summaries; collation
`en_US.utf8`, libc provider). PostgreSQL's `lower()` and Go's `strings.ToLower` differ only for rare
characters such as `İ` (PostgreSQL `i`, Go `i̇`); ingestion overwrites the backfilled value with the
Go value whenever a refresh sees the article again.

### Ingestion

`go run ./cmd/perfbench -mode ingest` times `CommitSourceRefresh` (retention 180 days) for 200 items
of seeded source 17: all stored and unchanged; 20 new and 180 stored; and a new source with 200 new
items whose summaries have the seeded length (about 1,400 characters). Three interleaved rounds of
seven runs after one warm-up; the table shows the median of the round medians in ms (round medians in
parentheses). "No trigram index" is a copy of `pulse_after` without `articles_search_text_trgm_idx`.

| Scenario | Before | After | After, no trigram index |
| --- | ---: | ---: | ---: |
| Existing source, 200 known | 91.6 (116.9 / 89.2 / 91.6) | 41.5 (79.1 / 37.4 / 41.5) | 39.3 (49.9 / 39.3 / 38.8) |
| Existing source, 20 new | 94.1 (219.3 / 94.1 / 91.0) | 48.9 (90.5 / 41.2 / 48.9) | 41.2 (46.2 / 38.2 / 41.2) |
| New source, 200 new | 135.7 (315.7 / 116.7 / 135.7) | 164.6 (317.2 / 164.6 / 141.9) | 37.9 (58.2 / 37.9 / 37.6) |

Round 1 ran under higher load. The batched statements and the faster stop-word matching (45 ms → 11 ms
of CPU for 200 items and 45 terms; it was most of the remaining time) halve the commit time of a
regular refresh. Inserting 200 long new articles now costs about 100–300 ms of trigram index
maintenance: GIN's pending list (`fastupdate`) absorbs inserts until it exceeds 4 MB, and the insert
that crosses the limit merges it into the index, so new-source runs are bimodal (90–120 ms or
140–340 ms). This happens once per new source; a regular refresh adds few new articles.

### Migration time

Applied with `psql -1` to a copy of `pulse_before` (500,000 articles), load average 4–6:
`016_article_search.sql` took 3 min 16 s in total, of which the `search_text` backfill `UPDATE` took
87–110 s and the GIN build 90 s with the default `maintenance_work_mem` of 64 MB or 59 s with the
256 MB that the migration now sets for its own transaction. `017_unread_counters.sql` took 0.6 s. The
API applies migrations before it listens, so the first start after the upgrade is delayed by that
time. `scripts/update-production.sh` waits 180 s for health checks; on a database of this size it can
report a timeout while the API is still migrating, so check `docker compose ps` and the API log
afterwards.

Migration 016 also ran as a non-superuser database owner (`CREATE ROLE … NOSUPERUSER`, database
owned by that role): the full store and seed integration test suites passed, and `pg_trgm` was
created in `public`, owned by that role.

### Commands

```sh
docker network create pulse-s7-net
docker run -d --name pulse-s7-db --network pulse-s7-net --tmpfs /var/lib/postgresql \
  -e POSTGRES_USER=pulse -e POSTGRES_PASSWORD=test-only -e POSTGRES_DB=pulse_test postgres:18.6-alpine3.24
docker exec pulse-s7-db psql -U pulse -d pulse_test -c "CREATE DATABASE pulse_before" -c "CREATE DATABASE pulse_after"

# from backend/ of a checkout of 4612291 ("before") and of stage 7 ("after"), default seed flags
go run ./cmd/seed -confirm-destroy-data -dsn 'postgres://pulse:test-only@pulse-s7-db:5432/pulse_before?sslmode=disable'
go run ./cmd/seed -confirm-destroy-data -dsn 'postgres://pulse:test-only@pulse-s7-db:5432/pulse_after?sslmode=disable'
docker exec pulse-s7-db psql -U pulse -d pulse_before -c "VACUUM (ANALYZE)"
docker exec pulse-s7-db psql -U pulse -d pulse_after -c "VACUUM (ANALYZE)"

# one plan measurement; repeat 1 + 5 times per query name
docker exec -i pulse-s7-db psql -X -q -U pulse -d pulse_after -v uid=17 -v pattern=%market% \
  -v query=feeds -f - < docs/2026-11-01-performance-stage-7.sql

# application-level search and ingestion; for "before", copy backend/cmd/perfbench into the old checkout
go run ./cmd/perfbench -mode search -runs 5 -dsn 'postgres://pulse:test-only@pulse-s7-db:5432/pulse_after?sslmode=disable'
go run ./cmd/perfbench -mode ingest -runs 7 -dsn 'postgres://pulse:test-only@pulse-s7-db:5432/pulse_after?sslmode=disable'
```

The Go commands ran in `golang:1.27.3-alpine3.24` on the same Docker network.
