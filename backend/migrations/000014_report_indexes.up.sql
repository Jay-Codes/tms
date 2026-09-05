-- Phase 11: the indexes the revenue and occupancy series walk.
--
-- Each series is one range scan over one table inside one org, so each index
-- leads with org_id (SPEC §2.1) and carries the date the series is read by.
-- Two of the four the phase called for already existed and are not repeated
-- here: `expenses (org_id, property_id, incurred_on)` (000012) serves the
-- per-property breakdown, and `payment_schedules (org_id, due_date, id)`
-- (000007) serves the expected series.

-- The collected series scans forward by paid_at and drops reversals. The
-- existing payments_org_paid_at_idx is DESC with id, for the keyset cursor of
-- GET /payments; this one is ascending, narrower, and excludes the reversed
-- rows the series must not count, so the scan needs no recheck of the table.
CREATE INDEX IF NOT EXISTS payments_org_paid_at_live_idx
    ON payments (org_id, paid_at)
    WHERE deleted_at IS NULL AND reversed_at IS NULL;

-- The expenses series counts recorded rows only; a voided expense is a
-- correction, not a cost. The 000012 index is the general ledger's, over both
-- statuses.
CREATE INDEX IF NOT EXISTS expenses_org_incurred_recorded_idx
    ON expenses (org_id, incurred_on)
    WHERE status = 'recorded' AND deleted_at IS NULL;

-- The occupancy series asks which tenancies covered a given day. The contract
-- indexes that existed all lead with status, unit or created_at; none of them
-- can answer a span overlap.
CREATE INDEX IF NOT EXISTS contracts_org_span_idx
    ON contracts (org_id, start_date, end_date)
    WHERE deleted_at IS NULL;
