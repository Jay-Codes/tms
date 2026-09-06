-- Reverses 000017: the held-message index goes, and the Phase 11 payments
-- index comes back exactly as 000014 wrote it (unused predicate and all —
-- a down migration restores the previous state, it does not improve on it).
DROP INDEX IF EXISTS payments_org_paid_at_recorded_idx;

CREATE INDEX IF NOT EXISTS payments_org_paid_at_live_idx
    ON payments (org_id, paid_at)
    WHERE deleted_at IS NULL AND reversed_at IS NULL;

DROP INDEX IF EXISTS notification_log_org_held_idx;
