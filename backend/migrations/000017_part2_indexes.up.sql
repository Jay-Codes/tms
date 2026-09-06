-- Phase 15 hardening: the two indexes the Part 2 load pass and the credit
-- screens asked for, and one correction.
--
-- PLAN2 Phase 15 named four indexes. Three of them already exist and are not
-- repeated here:
--
--   expenses (org_id, incurred_on)               — 000012 expenses_org_incurred_idx
--   expenses (org_id, property_id, incurred_on)  — 000012 expenses_org_property_incurred_idx
--   payments (org_id, paid_at)                   — 000007 payments_org_paid_at_idx
--
-- The fourth is below, along with a fix to the Phase 11 payments index, which
-- EXPLAIN showed no query can use.

-- 1. Held messages (PLAN2 Phase 15).
--
-- `notification_log` grows by one row per SMS for the life of the org, while
-- the set of messages parked for want of credit is a handful at most and is
-- read on every landlord notification screen (the low-credit banner) and on
-- every admin credit view. Today the count is a sequential scan discarding
-- 269 of 271 rows; the ratio only gets worse.
--
-- The trailing (created_at, id) is the order ListHeldNotifications releases
-- them in — oldest first — so the release path walks the index rather than
-- sorting what it finds.
CREATE INDEX IF NOT EXISTS notification_log_org_held_idx
    ON notification_log (org_id, created_at, id)
    WHERE status = 'held_no_credit';

-- 2. The revenue series' payments scan.
--
-- 000014 added `payments_org_paid_at_live_idx` for this query, predicated
-- `WHERE deleted_at IS NULL AND reversed_at IS NULL`. The query it was built
-- for filters `status <> 'reversed'`, and Postgres cannot prove one implies
-- the other: with `enable_seqscan = off` the planner reaches past the partial
-- index for the bare `payments_org_id_idx` and re-filters, which is the proof
-- that the index has never once been used. No query in
-- `internal/db/queries/**` mentions `reversed_at IS NULL` at all — the
-- predicate was written from the column rather than from the callers — so the
-- index costs every payment write and serves nobody.
--
-- Replaced by one whose predicate is the callers' own. Seven queries filter
-- `status <> 'reversed'`: the revenue series (collected, daily and by
-- property), the Phase 7 collection reports, and the admin metrics.
DROP INDEX IF EXISTS payments_org_paid_at_live_idx;

CREATE INDEX IF NOT EXISTS payments_org_paid_at_recorded_idx
    ON payments (org_id, paid_at)
    WHERE deleted_at IS NULL AND status <> 'reversed';
