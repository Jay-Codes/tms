-- Phase 5: offline payment recording, allocation across schedules, reversal.
--
-- What lands here on top of the 000002 payments/payment_schedules tables:
--   * payments.reversed_at / reversal_reason / reversed_by_user_id — a
--     reversal is a correction the audit trail must be able to explain
--     (FLOWS 7, SPEC §5.6), so who reversed it and why live on the row.
--   * payment_allocations — one payment may settle more than one schedule
--     (an overpayment rolls forward, API.md Phase 5), so the `applied[]`
--     array of the payment shape needs a table of its own. It is also what a
--     reversal reads back to un-apply exactly what was applied.
--   * indexes for the two new sweeps: the overdue flip (org + status + due
--     date) and the payment history listing (org + paid_at cursor).
--
-- notification_log already accepts kind `thank_you` (000002), so Phase 5 adds
-- no constraint change there; orgs.settings.bank_account is JSON inside the
-- existing settings column and needs no schema change either.

-- ----------------------------------------------------------------- payments --

ALTER TABLE payments
    ADD COLUMN reversed_at         TIMESTAMPTZ,
    ADD COLUMN reversal_reason     TEXT,
    ADD COLUMN reversed_by_user_id UUID REFERENCES users (id);

-- A reversed payment carries both the timestamp and the reason, or neither:
-- half a reversal is not a state the API can produce.
ALTER TABLE payments ADD CONSTRAINT payments_reversal_check
    CHECK ((status <> 'reversed' AND reversed_at IS NULL AND reversal_reason IS NULL)
        OR (status =  'reversed' AND reversed_at IS NOT NULL AND reversal_reason IS NOT NULL));

-- GET /payments pages by (paid_at, id) inside one org.
CREATE INDEX payments_org_paid_at_idx ON payments (org_id, paid_at DESC, id DESC)
    WHERE deleted_at IS NULL;

-- ------------------------------------------------------- payment_allocations --

-- One row per schedule a payment settled, in the order it was applied. The
-- payment's own schedule_id names the target the landlord aimed at; the
-- allocations name every row the money actually reached.
CREATE TABLE payment_allocations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    payment_id  UUID NOT NULL REFERENCES payments (id) ON DELETE CASCADE,
    schedule_id UUID NOT NULL REFERENCES payment_schedules (id) ON DELETE CASCADE,
    amount      BIGINT NOT NULL CHECK (amount > 0),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX payment_allocations_org_id_idx ON payment_allocations (org_id);
CREATE INDEX payment_allocations_payment_idx ON payment_allocations (payment_id, created_at);
CREATE INDEX payment_allocations_schedule_idx ON payment_allocations (schedule_id);
-- A payment settles a given schedule once: the allocation loop walks forward
-- through distinct rows, so a repeat would be a bug rather than a business case.
CREATE UNIQUE INDEX payment_allocations_payment_schedule_key
    ON payment_allocations (payment_id, schedule_id);
CREATE TRIGGER payment_allocations_set_updated_at BEFORE UPDATE ON payment_allocations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ------------------------------------------------------- payment_schedules --

-- The overdue sweep selects unsettled rows by due date, per org when it runs
-- on demand for one org's read (API.md Phase 5) and across orgs on the ticker.
CREATE INDEX payment_schedules_org_status_due_idx
    ON payment_schedules (org_id, status, due_date) WHERE deleted_at IS NULL;

-- GET /schedules pages by (due_date, id).
CREATE INDEX payment_schedules_org_due_idx
    ON payment_schedules (org_id, due_date, id) WHERE deleted_at IS NULL;
