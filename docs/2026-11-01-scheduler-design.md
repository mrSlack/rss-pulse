# Refresh scheduler design (2026-11-01)

Roadmap items: REL-6 / task 3.3 (with REL-5, REL-7, PERF-6 in the same stage).

## Problem

- The scheduler ran its first cycle 15 minutes after start-up, so a restart left feeds stale for up to 15 minutes.
- `DueSources` and `ClaimSource` only used the shared-source TTL. The global scheduler interval (`system_settings.refresh_interval_minutes`) and the per-user override (`users.refresh_interval_minutes`) were saved but had no effect.
- A failing source was retried on every cycle, because a failure does not advance `last_success_at`.

## Rule (owner decision)

A shared source is refreshed at the **shortest effective interval among its subscribers**, and never more often than the shared-source TTL:

```
subscriber interval = COALESCE(users.refresh_interval_minutes, global refresh_interval_minutes)
effective interval  = GREATEST(shared_feed_ttl_minutes, MIN(subscriber interval over all subscribers))
```

A source without subscribers is never scheduled.

### Due condition (scheduled cycles)

A subscribed source is due when no attempt happened within its effective interval:

```
last_attempt = GREATEST(last_checked_at, last_success_at)   -- NULLs ignored
due          = last_attempt IS NULL OR last_attempt + effective <= NOW()
```

This is equivalent to the two separate rules:

- **Success rule:** `last_success_at IS NULL OR NOW() >= last_success_at + effective`.
- **Failure backoff:** if the last attempt failed (`last_checked_at > COALESCE(last_success_at, '-infinity')`), the source is not retried before `last_checked_at + effective`.

A successful refresh (and a `304 Not Modified`) sets `last_checked_at` and `last_success_at` to the same timestamp; a failed refresh sets only `last_checked_at`. Therefore `last_checked_at` is the last attempt whenever it is set, and the backoff interval equals the effective interval, so a failing feed is contacted at most once per effective interval instead of every minute. A source that was never checked is due immediately.

### Claims

`ClaimSource(source, requester)` re-checks eligibility atomically while taking the lease, so a stale `DueSources` list cannot cause an early fetch:

| Claim | Interval used | Subscriber requirement |
| --- | --- | --- |
| Scheduled (`requester = NULL`) | effective interval (above) | source has at least one subscriber |
| Manual (`requester = user`) | shared-source TTL only | the requester subscribes to the source |

In both cases the interval is measured from the last attempt (`GREATEST(last_checked_at, last_success_at)`). For manual refresh this means "refresh now" works as soon as the TTL has passed since the last successful fetch, but a failing source is not contacted again within the TTL of its last failed attempt. The lease (`lease_token`, `lease_expires_at`) is required in both cases, so a scheduled cycle and a manual request never fetch the same source concurrently.

## SQL

Both queries derive the policy from `system_settings` and the subscriber set:

```sql
WITH settings AS (
  SELECT (SELECT value FROM system_settings WHERE key='shared_feed_ttl_minutes') AS ttl_minutes,
         (SELECT value FROM system_settings WHERE key='refresh_interval_minutes') AS global_minutes
), policy AS (
  SELECT uf.feed_source_id AS source_id,
         GREATEST(MIN(st.ttl_minutes), MIN(COALESCE(u.refresh_interval_minutes, st.global_minutes)))
           * INTERVAL '1 minute' AS effective
  FROM user_feeds uf JOIN users u ON u.id = uf.user_id CROSS JOIN settings st
  GROUP BY uf.feed_source_id
)
SELECT fs.id, fs.url FROM feed_sources fs JOIN policy p ON p.source_id = fs.id
WHERE GREATEST(fs.last_checked_at, fs.last_success_at) IS NULL
   OR GREATEST(fs.last_checked_at, fs.last_success_at) + p.effective <= NOW()
ORDER BY fs.last_success_at NULLS FIRST, fs.id;
```

`ClaimSource` uses the same policy restricted to one source (`WHERE uf.feed_source_id = $1`), substitutes the TTL for manual claims, and performs the lease `UPDATE ... FROM policy`.

## Timing

- First cycle about 10 seconds after start-up (`syncer.DefaultInitialDelay`), then one cycle per minute (`syncer.DefaultInterval`). Each cycle is cheap when nothing is due: one `DueSources` query.
- One minute is the scheduling resolution: a source becomes eligible at `last_attempt + effective` and is fetched on the next tick, so the observed interval is at most one minute longer than configured.
- Both values are injectable (`syncer.WithSchedule`) for tests.

## Manual requests and status

Manual refresh requests are queued per user: a FIFO of distinct user IDs. A user that is already queued or being processed is not queued again; the request is still accepted (`202`). Scheduled cycles and user requests run in separate loops and share one worker budget (`RSS_REFRESH_CONCURRENCY` concurrent sources in total); source leases keep them from fetching the same source at the same time. `GET /api/refresh` reports `running` for the calling user only.

## Edge cases

- **Settings changes** take effect on the next tick, because the policy is evaluated in SQL each time.
- **Unsubscribed sources** are never scheduled; they keep their data until retention or deletion.
- **Shutdown:** a cancelled fetch abandons its lease without recording an attempt (`last_checked_at` unchanged), using a bounded 5-second cleanup context, so a restart does not push the source into failure backoff.
- **Conditional GET:** a `304` counts as a successful attempt (both timestamps advance, `last_error` cleared), without touching articles.
- **Clock:** all comparisons use the database clock (`NOW()`), so multiple API processes agree.
- **Approval of a new feed** queues a scheduled cycle; the new source is never checked and therefore due immediately.

## Disabled accounts

Added when account disabling was merged: subscriptions of disabled users are ignored when computing the effective interval and when deciding whether a source has subscribers, for both `DueSources` and scheduled `ClaimSource` (`JOIN users u ON u.id = uf.user_id AND u.disabled_at IS NULL`). Disabled users cannot request manual refreshes because their sessions are rejected.
