-- Phase 8 load pass: the three indexes the p95 measurements asked for.
--
-- Measured on the seeded load-test org (5 properties, 50 units, 40 renters,
-- 35 contracts, 201 schedules) with `make loadtest` — 20 workers, 20 seconds,
-- results in docs/LOADTEST.md. GET /contracts was an order of magnitude slower
-- than every other hot endpoint, and EXPLAIN (ANALYZE, BUFFERS) named the
-- reasons:
--
--  1. Both LATERAL subqueries in ListContracts (the schedule roll-up and the
--     next-due lookup) filter payment_schedules on org_id + contract_id, and
--     the planner fell back to a Seq Scan per contract: the only index leading
--     with contract_id is (contract_id, period_start), which carries neither
--     org_id nor the partial `deleted_at IS NULL` predicate, and the org-led
--     indexes lead with due_date or status instead.
--
--  2. GET /contracts fetches signatures one contract at a time, and
--     contract_signatures had nothing better than a bare org_id index for the
--     (org_id, contract_id) lookup that does. The N+1 itself is a handler
--     concern (noted in docs/LOADTEST.md); the index makes each of its round
--     trips an index scan rather than a filter over the org's whole set.
--
--  3. The contracts list paginates on (created_at DESC, id DESC) inside an org
--     and had no index to walk, so every page sorted the org's contracts.
--
-- All three lead with org_id, per SPEC §2.1.

-- Serves both LATERAL blocks of ListContracts, plus LockSchedulesForContract
-- and ListSchedulesForContract on the payment path.
CREATE INDEX IF NOT EXISTS payment_schedules_org_contract_idx
    ON payment_schedules (org_id, contract_id, due_date, period_start)
    WHERE deleted_at IS NULL;

-- Serves ListContractSignatures, called once per contract by GET /contracts
-- and once by GET /contracts/{id} and the document view.
CREATE INDEX IF NOT EXISTS contract_signatures_org_contract_idx
    ON contract_signatures (org_id, contract_id);

-- Serves the ORDER BY / keyset cursor of ListContracts.
CREATE INDEX IF NOT EXISTS contracts_org_created_idx
    ON contracts (org_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;
