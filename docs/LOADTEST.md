# LOADTEST.md — the load pass

The p95 check on the hot read endpoints, run against the seeded load-test
fixture. Two passes are recorded here: [Phase 8](#phase-8-results)
([PLAN.md](../PLAN.md)) covered the Part 1 reads, and
[Part 2](#part-2-results-phase-15) ([PLAN2.md](../PLAN2.md) Phase 15) adds the
reports, expenses, credit and admin routes.

- Harness: `make loadtest` → `backend/cmd/loadtest` (net/http only, no k6/wrk).
- Fixture: `make seed` → `backend/cmd/seed` (org **Load Test Estates**,
  5 properties, 50 units, 40 renters, 35 active contracts, 201 schedules,
  38 payments of which 2 reversed, 5 pending link requests, 62 notification rows).
- Budget: **p95 < 300 ms** per endpoint on a development laptop.

## How to reproduce

```sh
make up          # postgres, redis, minio
make migrate     # through 000017_part2_indexes
make api         # API on :8081 (the proxy on :8080 forwards /api/*)
make seed        # idempotent; add SEED_ARGS=-reset to start clean
make loadtest    # 20 workers, 20s; exits non-zero if any p95 breaches the budget
```

Flags: `make loadtest LOADTEST_ARGS="-duration 60s -workers 40 -base http://localhost:8081"`.

## Phase 8 results

Run of 5 Sep 2026. Apple M4 Pro, macOS 26.4.1, Postgres 17.11 in Docker
Desktop, API and dev proxy running natively. 20 workers, 20 s, requests picked
uniformly at random per worker, through the dev proxy on `:8080`.

These are the numbers after the two fixes described under [Slow query
analysis](#slow-query-analysis) — the batched signature lookup and the larger
pgx pool.

| Endpoint | reqs | p50 | p95 | p99 | max | rps | errors |
|---|---:|---:|---:|---:|---:|---:|---:|
| `GET /units?status=vacant` | 12 372 | 3.0 ms | **6.0 ms** | 8.2 ms | 31.4 ms | 619 | 0 |
| `GET /schedules?status=overdue` | 12 217 | 4.7 ms | **8.1 ms** | 10.6 ms | 32.8 ms | 611 | 0 |
| `GET /reports/summary` | 12 147 | 9.8 ms | **14.5 ms** | 17.2 ms | 32.1 ms | 607 | 0 |
| `GET /public/units/{code}` (unauth) | 12 361 | 6.4 ms | **10.0 ms** | 12.4 ms | 27.0 ms | 618 | 0 |
| `GET /contracts` | 12 194 | 7.1 ms | **11.9 ms** | 15.6 ms | 33.5 ms | 610 | 0 |

Aggregate 61 291 requests, ~3 064 rps, zero errors. **Every endpoint is inside
the 300 ms budget**, the slowest at a twentieth of it.

The run before those two fixes, same machine and same fixture:

| Endpoint | reqs | p50 | p95 | p99 | max | rps | errors |
|---|---:|---:|---:|---:|---:|---:|---:|
| `GET /units?status=vacant` | 5924 | 2.1 ms | **3.6 ms** | 5.1 ms | 31.1 ms | 296 | 0 |
| `GET /schedules?status=overdue` | 5756 | 4.6 ms | **7.5 ms** | 11.6 ms | 31.9 ms | 288 | 0 |
| `GET /reports/summary` | 5732 | 8.4 ms | **11.3 ms** | 16.2 ms | 42.4 ms | 286 | 0 |
| `GET /public/units/{code}` (unauth) | 5839 | 5.1 ms | **7.2 ms** | 10.0 ms | 32.2 ms | 292 | 0 |
| `GET /contracts` | 5791 | 45.8 ms | **58.0 ms** | 75.8 ms | 110.5 ms | 289 | 0 |

Aggregate 29 042 requests, ~1 451 rps. `GET /contracts` p95 fell **58.0 ms →
11.9 ms** (‑79%) and the whole pass more than doubled its throughput: with the
N+1 gone, every endpoint gets a fair share of the pool instead of queueing
behind one greedy handler. The per-endpoint p50s rise slightly because the
harness is now pushing twice the load through the same laptop.

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
trips per request, 36 for this fixture. The pgx pool was capped at 10
connections (`backend/internal/db/db.go`), so 20 concurrent requests queued
~700 sequential round trips behind 10 connections. Latency was connection-pool
queueing, which no index can fix.

### The fix

Both follow-ups this analysis recommended are now in:

- **The signature lookup is batched.** `ListContractSignaturesForContracts(org_id,
  contract_ids[])` (and `…AnyOrg`, for `GET /me/contracts`, which spans every
  org the renter rents from — the same shape `ListAllocationsForPayments` already
  used) loads a whole page in one round trip, and `signaturesFor` in
  `contract_handlers.go` groups the rows by contract id in Go. The list is 2
  queries per request instead of 1 + N, whatever the page size — which is the
  difference between a page that stays fast at 500 contracts and one that does
  not.
- **The pgx pool is configurable and larger.** `DB_MAX_CONNS` (default 20, was a
  hard-coded 10) feeds `db.Open`; size it against the deployment's Postgres
  `max_connections`.

Measured effect: `GET /contracts` p95 58.0 ms → 11.9 ms, and the aggregate rate
1 451 → 3 064 rps. The single-contract paths (`GET /contracts/{id}`, the
document and verify endpoints) still use the per-contract
`ListContractSignatures`, which is one query there and correct.

Still worth a look, and not done: the same 1 + N shape on any other list handler
that enriches rows in a loop. A sweep of the handlers found no other read path
issuing a query per row — the remaining in-loop queries are writes inside a
transaction.

## Part 2 results (Phase 15)

Run of 6 Sep 2026, same machine as above (Apple M4 Pro, macOS 26.4.1, Postgres
17.11 in Docker Desktop, API and dev proxy native). 20 workers, 20 s, through
the dev proxy on `:8080`, against the **seed v2** fixture described below —
which is the material change since Phase 8: a year of expenses and a year of
payments, so the report endpoints scan a year instead of four months.

The pass now covers every Part 2 read route. Two of them are the platform
admin's and are exercised with a second cookie jar (`ADMIN_EMAIL` /
`ADMIN_PASSWORD`, skipped with a warning when unset — the admin is a different
audience, not a stronger role).

| Endpoint | reqs | p50 | p95 | p99 | max | rps | errors |
|---|---:|---:|---:|---:|---:|---:|---:|
| `GET /units?status=vacant` | 1 697 | 4.3 ms | **8.6 ms** | 13.1 ms | 32.1 ms | 85 | 0 |
| `GET /schedules?status=overdue` | 1 727 | 6.5 ms | **11.9 ms** | 17.0 ms | 33.2 ms | 86 | 0 |
| `GET /reports/summary` | 1 755 | 16.9 ms | **29.2 ms** | 46.4 ms | 81.9 ms | 88 | 0 |
| `GET /public/units/{code}` (unauth) | 1 755 | 10.7 ms | **19.5 ms** | 28.2 ms | 48.1 ms | 88 | 0 |
| `GET /contracts` | 1 706 | 8.9 ms | **16.5 ms** | 24.5 ms | 60.5 ms | 85 | 0 |
| `GET /reports/revenue?cadence=month` | 1 680 | 20.3 ms | **34.4 ms** | 47.5 ms | 90.0 ms | 84 | 0 |
| `GET /reports/revenue?cadence=year` | 1 776 | 53.4 ms | **79.1 ms** | 111.7 ms | 174.9 ms | 89 | 0 |
| `GET /reports/revenue?cadence=year&group_by=property` | 1 692 | 25.1 ms | **41.9 ms** | 62.1 ms | 96.6 ms | 85 | 0 |
| `GET /reports/occupancy?cadence=year` | 1 728 | 17.8 ms | **27.9 ms** | 45.9 ms | 90.6 ms | 86 | 0 |
| `GET /expenses?limit=50` | 1 770 | 9.7 ms | **16.5 ms** | 27.4 ms | 52.0 ms | 89 | 0 |
| `GET /expenses/summary?cadence=year&group_by=property` | 1 789 | 8.6 ms | **15.6 ms** | 23.4 ms | 57.8 ms | 89 | 0 |
| `GET /org/sms-credits` | 1 697 | 7.9 ms | **15.9 ms** | 24.5 ms | 89.9 ms | 85 | 0 |
| `GET /org/notification-settings` | 1 792 | 5.4 ms | **10.2 ms** | 14.6 ms | 55.3 ms | 90 | 0 |
| `GET /themes/presets` (unauth) | 1 755 | 1.4 ms | **3.8 ms** | 6.0 ms | 19.0 ms | 88 | 0 |
| `GET /admin/templates` (admin) | 1 671 | 3.3 ms | **7.0 ms** | 10.9 ms | 24.7 ms | 84 | 0 |
| `GET /admin/orgs/{id}/sms` (admin) | 1 723 | 12.8 ms | **22.9 ms** | 32.3 ms | 67.4 ms | 86 | 0 |

Aggregate 27 713 requests, ~1 386 rps, zero errors and zero 429s. **Every
endpoint is inside the 300 ms budget**; the PLAN2 target for the reports
(p95 < 300 ms) is met with room to spare — the worst of them,
`/reports/revenue?cadence=year`, sits at about a quarter of it.

Sixteen endpoints share the same 20 workers, so each gets roughly a sixteenth
of the load Phase 8's five endpoints each got. The per-endpoint p50s are
therefore not comparable with the Phase 8 table above; what is comparable is
that nothing has moved into a different order of magnitude now that the fixture
holds a year of data.

### The one endpoint worth a note

`GET /reports/revenue?cadence=year` is 4–5× everything else, and it is not the
database. Measured serially it answers in **11 ms** (the month variant: 6 ms),
and each of its queries runs in well under a millisecond:

```
GroupAggregate (actual time=0.336..0.357 rows=70)  Buffers: shared hit=7
  ->  Sort -> Seq Scan on payments p
        Rows Removed by Filter: 20
Execution Time: 0.438 ms
```

The endpoint issues **six sequential round trips** — collected, expected and
expenses, for the requested window and again for the `previous` one the Δ% is
computed against — and under 20-way concurrency those six queue behind the pgx
pool. That is the same shape Phase 8 found behind `GET /contracts`, in a milder
form: there the count grew with the page size (1 + N), here it is a fixed six
whatever the window. Fixed cost, inside budget, so it is recorded rather than
fixed; the change worth making if it ever matters is to run the three window
queries concurrently, or to fold the previous window into the same scan.

### Migration `000017_part2_indexes`

PLAN2 Phase 15 named four indexes. Three already existed and were not
duplicated — `expenses (org_id, incurred_on)` and
`expenses (org_id, property_id, incurred_on)` from `000012`, and
`payments (org_id, paid_at)` from `000007`. `\di` confirms them.

Two statements were added:

```sql
CREATE INDEX notification_log_org_held_idx
    ON notification_log (org_id, created_at, id)
    WHERE status = 'held_no_credit';

DROP INDEX payments_org_paid_at_live_idx;
CREATE INDEX payments_org_paid_at_recorded_idx
    ON payments (org_id, paid_at)
    WHERE deleted_at IS NULL AND status <> 'reversed';
```

**The held-message index** is the fourth PLAN2 named. `notification_log` grows
by a row per SMS for the life of the org; the held set is a handful and is read
on every landlord notification screen and every admin credit view. Before:

```
Seq Scan on notification_log (actual time=0.074..0.080 rows=2)
  Rows Removed by Filter: 269
```

After: `Bitmap Index Scan on notification_log_org_held_idx`, 2 rows found, none
removed. Two of 271 rows today; the ratio only gets worse.

**The payments index is a correction.** `000014` added
`payments_org_paid_at_live_idx` predicated `WHERE deleted_at IS NULL AND
reversed_at IS NULL` for the revenue series — but the series filters
`status <> 'reversed'`, and Postgres cannot prove one implies the other. With
`enable_seqscan = off` the planner reached *past* the partial index for the
bare `payments_org_id_idx` and re-filtered:

```
Bitmap Heap Scan on payments p
  Filter: (deleted_at IS NULL) AND (status <> 'reversed') AND (paid_at >= …)
  Rows Removed by Filter: 3
  ->  Bitmap Index Scan on payments_org_id_idx
        Index Cond: (org_id = …)
```

No query in `backend/internal/db/queries/**` mentions `reversed_at IS NULL` at
all — the predicate was written from the column rather than from the callers —
so the index had never been used by anything and cost every payment write. The
replacement carries the callers' own predicate, and with the competing indexes
hidden it is picked with the range in the index condition and nothing left to
re-filter:

```
Bitmap Index Scan on payments_org_paid_at_recorded_idx
  Index Cond: (org_id = …) AND (paid_at >= '2025-09-06') AND (paid_at < '2026-09-07')
Execution Time: 0.629 ms
```

Seven queries filter `status <> 'reversed'`: the revenue series (daily and by
property), the Phase 7 collection reports, and the admin metrics. At this
fixture's 91 payments the planner still prefers the bare org index — two heap
pages is two heap pages — so the measurable effect today is zero. The index is
there for the org with 100 000 payments, and the evidence above is that the one
it replaces could never have helped that org at all.

### Nothing else was slow

Every other Part 2 endpoint answers in under 30 ms at p95 with a plan that
already uses an index. `EXPLAIN (ANALYZE, BUFFERS)` on the expense summary, the
occupancy spans and the credit ledger reads showed index scans over the
`000012`/`000014` indexes, no sequential scan over a growing table, and no
sort that an index could have avoided. No further indexes were added: an index
that no measurement asked for is a write cost with no reader.

## Fixture shape (what the numbers were measured against)

| | |
|---|---|
| Org | `Load Test Estates` (`load-test-estates`), owner `load@tms.local` / `password123` |
| Properties | 5 |
| Units | 50, priced TZS 120 000 – 360 000 per 30 days |
| Renters | 40, phones `+255760000001…40`, PIN `1234`, KYC `submitted` |
| Renter language | 30 `sw` / 17 `en` — roughly 60/40, so a bulk send renders both |
| Contracts | 35, cadences 30 / 90 / 180 days, terms 360 / 180 / 365 days, **starts spread over the last 380 days** (11-day step) |
| Contract statuses | 25 `active`, 7 `ended` (term ran out; units returned to the vacancy board by the lifecycle sweep), 2 `expiring`, 1 `terminated` with a reason |
| Schedules | 201 — a mix of `paid`, `partial`, `overdue` and `pending` |
| Payments | 74 (≈60% of the schedules already due), 2 reversed, **`paid_at` spread across 14 months** |
| Expenses | 245 over 12 months across the 5 properties (2–6 a month each), 22 `voided` with a reason, 225 with a vendor and 130 with a reference |
| SMS credits | 500, with one `topup` row in `sms_credit_ledger` |
| Link requests | 5 pending, for the renters without a tenancy |
| Notifications | 196 `sent` rows (`reminder_due`, `overdue_daily`, `thank_you`) + 2 `held_no_credit` |

The seeder writes through the same rules the handlers use — `contract.Generate`
for schedules, `payment.Allocate` for money, `payment.FlipOverdue` for the
overdue sweep, `contract.RunLifecycle` for the tenancies whose term has run
out, `notify.Render` for message bodies, `theme.Validate` for the demo theme —
so the fixture exercises the same query shapes production traffic will.

**Seed v2 (Phase 15)** is what made the year of data. The `make seed` fixture
is idempotent on a natural key throughout: an expense by
(property, date, amount), a tenancy by whether the unit has ever had one, the
credit balance by what is already on the org. A second `make seed` against a
seeded database creates nothing — which is worth stating, because the obvious
implementation of "12 months of expenses" is not idempotent and writes a second
ledger beside the first. `make seed SEED_ARGS=-reset` starts clean (the reset
now takes the Part 2 tables — expenses, categories, themes, credits and the
credit ledger — with the org).

`make seed-demo` gives the JJnE demo org the same treatment: 12 months of
expenses on both blocks, two long-running tenancies with a year of payments
behind them, a saved theme (the `cool_slate` preset with its accent moved to
`#0f5c4a`, validated through `theme.Validate` so it is a theme the API would
have accepted), an edited-then-reverted `reminder_7d` platform template leaving
two rows in its version history, its renters split between Swahili and English,
and 600 SMS credits.
