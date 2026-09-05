# LOADTEST.md — Phase 8 load pass

The p95 check on the hot read endpoints ([PLAN.md](../PLAN.md) Phase 8), run
against the seeded load-test fixture.

- Harness: `make loadtest` → `backend/cmd/loadtest` (net/http only, no k6/wrk).
- Fixture: `make seed` → `backend/cmd/seed` (org **Load Test Estates**,
  5 properties, 50 units, 40 renters, 35 active contracts, 201 schedules,
  38 payments of which 2 reversed, 5 pending link requests, 62 notification rows).
- Budget: **p95 < 300 ms** per endpoint on a development laptop.

## How to reproduce

```sh
make up          # postgres, redis, minio
make migrate     # through 000011_load_indexes
make api         # API on :8081 (the proxy on :8080 forwards /api/*)
make seed        # idempotent; add SEED_ARGS=-reset to start clean
make loadtest    # 20 workers, 20s; exits non-zero if any p95 breaches the budget
```

Flags: `make loadtest LOADTEST_ARGS="-duration 60s -workers 40 -base http://localhost:8081"`.

## Results

Run of 5 Sep 2026. Apple M4 Pro, macOS 26.4.1, Postgres 17.11 in Docker
Desktop, API and dev proxy running natively. 20 workers, 20 s, requests picked
uniformly at random per worker, through the dev proxy on `:8080`.

| Endpoint | reqs | p50 | p95 | p99 | max | rps | errors |
|---|---:|---:|---:|---:|---:|---:|---:|
| `GET /units?status=vacant` | 5924 | 2.1 ms | **3.6 ms** | 5.1 ms | 31.1 ms | 296 | 0 |
| `GET /schedules?status=overdue` | 5756 | 4.6 ms | **7.5 ms** | 11.6 ms | 31.9 ms | 288 | 0 |
| `GET /reports/summary` | 5732 | 8.4 ms | **11.3 ms** | 16.2 ms | 42.4 ms | 286 | 0 |
| `GET /public/units/{code}` (unauth) | 5839 | 5.1 ms | **7.2 ms** | 10.0 ms | 32.2 ms | 292 | 0 |
| `GET /contracts` | 5791 | 45.8 ms | **58.0 ms** | 75.8 ms | 110.5 ms | 289 | 0 |

Aggregate 29 042 requests, ~1 451 rps, zero errors. **Every endpoint is inside
the 300 ms budget**, with the slowest at roughly a fifth of it.

### A note on the public endpoint

`GET /public/units/{code}` is rate-limited to 60 requests per minute per client
IP, which is right for a phone scanning a sticker and wrong for measuring the
handler. The harness therefore presents a distinct `X-Forwarded-For` per
request — loopback callers are trusted to set it (`TRUSTED_PROXY_CIDRS`) — so
the numbers above are the handler under concurrency, as real QR traffic from
many handsets would be. Throttled responses are counted separately and excluded
from the latency sample; a first run without the rotating address produced
4 596 × 429 out of 5 796, confirming the limiter works.

## Slow query analysis

`GET /contracts` is an order of magnitude slower than everything else, so it
got the `EXPLAIN (ANALYZE, BUFFERS)` treatment (via `docker compose exec
postgres psql`) on `ListContracts` — the query behind it.

### What the plan showed

```
Limit (actual time=1.686..1.692 rows=35)  Buffers: shared hit=658
  ->  Nested Loop Left Join
        ->  Aggregate (loops=35)
              ->  Seq Scan on payment_schedules s        <-- 175 buffers
                    Filter: deleted_at IS NULL AND contract_id = c.id AND org_id = c.org_id
                    Rows Removed by Filter: 230
        ->  Limit (loops=35)
              ->  Sort -> Seq Scan on payment_schedules  <-- 178 buffers
```

Three things had no index to use:

1. Both `LATERAL` subqueries (the schedule roll-up, and the next-due lookup)
   filter `payment_schedules` on `org_id + contract_id + deleted_at IS NULL`.
   The only `contract_id`-leading index was `(contract_id, period_start)` — no
   `org_id`, no partial predicate — and the org-leading ones lead with
   `due_date` or `status`. The planner fell back to a sequential scan per
   contract.
2. `ListContractSignatures` is called **once per contract** by the handler, and
   `contract_signatures` had only a bare `org_id` index for its
   `(org_id, contract_id)` lookup.
3. The list paginates on `(created_at DESC, id DESC)` within an org with no
   matching index, so every page sorted the org's whole contract set.

### Migration `000011_load_indexes`

```sql
CREATE INDEX payment_schedules_org_contract_idx
    ON payment_schedules (org_id, contract_id, due_date, period_start)
    WHERE deleted_at IS NULL;
CREATE INDEX contract_signatures_org_contract_idx
    ON contract_signatures (org_id, contract_id);
CREATE INDEX contracts_org_created_idx
    ON contracts (org_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;
```

All three lead with `org_id`, per SPEC §2.1.

After the migration the next-due lateral switched to
`Index Scan using payment_schedules_org_contract_idx`, and the query's buffer
count fell from 658 to 424 shared hits. The roll-up aggregate still chooses a
sequential scan — at 201 rows across 8 pages that is the cheaper plan, and it
will flip to the index as the table grows.

### The real cost of `GET /contracts` — an N+1, not a missing index

The index did not move the endpoint's p95 (58 ms before and after), which is
the useful finding. Measured serially, the endpoint answers in **12 ms** for 35
contracts (45 KB of JSON); under 20-way concurrency it becomes ~46 ms. The
query itself takes 0.8 ms.

The gap is `handleListContracts` in
`backend/internal/httpserver/contract_handlers.go`: after the list query it
loops over the rows calling `ListContractSignatures` for each one — 1 + N round
trips per request, 36 for this fixture. The pgx pool is capped at 10
connections (`backend/internal/db/db.go`, `defaultMaxConns`), so 20 concurrent
requests queue ~700 sequential round trips behind 10 connections. Latency is
connection-pool queueing, which no index can fix.

**Recommended follow-ups** (outside the seeding/UAT lane, so recorded rather
than done):

- Batch the signature lookup: one `ListContractSignaturesForContracts(org_id,
  contract_ids[])` and a group-by in Go turns 1 + N queries into 2. This is the
  fix that matters — it is also the difference between a page that stays fast at
  500 contracts and one that does not.
- Raise `defaultMaxConns` from 10 once the N+1 is gone, and size it against the
  deployment's Postgres `max_connections`.
- The same 1 + N shape is worth a look on any other list handler that enriches
  rows in a loop.

Neither is a Phase 8 blocker: the endpoint is comfortably inside budget at the
fixture's size, and both notes are about headroom rather than a defect.

## Fixture shape (what the numbers were measured against)

| | |
|---|---|
| Org | `Load Test Estates` (`load-test-estates`), owner `load@tms.local` / `password123` |
| Properties | 5 |
| Units | 50, priced TZS 120 000 – 360 000 per 30 days |
| Renters | 40, phones `+255760000001…40`, PIN `1234`, KYC `submitted` |
| Contracts | 35 active, cadences 30 / 90 / 180 days, terms 360 / 180 / 365 days, starts spread over the last 120 days |
| Schedules | 201 — a mix of `paid`, `partial`, `overdue` and `pending` |
| Payments | 38 (≈60% of the schedules already due), 2 reversed |
| Link requests | 5 pending, for the renters without a tenancy |
| Notifications | 62 `sent` rows (`reminder_due`, `overdue_daily`, `thank_you`) |

The seeder writes through the same rules the handlers use — `contract.Generate`
for schedules, `payment.Allocate` for money, `payment.FlipOverdue` for the
overdue sweep, `notify.Render` for message bodies — so the fixture exercises the
same query shapes production traffic will.
