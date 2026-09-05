-- Phase 3: renter KYC and unit link requests.
--
-- A link request is the renter's application for a unit, made from the QR
-- landing page. It already carries the terms the renter chose (cadence, span,
-- start date), because Phase 4 turns an approved request into a contract
-- without asking again — the request IS the offer that was accepted.
--
--   * payment_period_id / term_days / start_date / end_date — the chosen terms
--   * accepted_terms_at   — when the renter ticked `accepted_terms`
--   * decided_at / decided_by_user_id / rejection_reason — the landlord's call
--   * status gains `cancelled` (the renter withdrawing their own request)

ALTER TABLE unit_link_requests
    ADD COLUMN payment_period_id   UUID REFERENCES payment_periods (id) ON DELETE SET NULL,
    ADD COLUMN term_days           INT CHECK (term_days IS NULL OR term_days > 0),
    ADD COLUMN start_date          DATE,
    ADD COLUMN end_date            DATE,
    ADD COLUMN rejection_reason    TEXT,
    ADD COLUMN decided_at          TIMESTAMPTZ,
    ADD COLUMN decided_by_user_id  UUID REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN accepted_terms_at   TIMESTAMPTZ;

-- `reason` from 000002 is superseded by `rejection_reason`; keeping both would
-- leave two places to look for the same fact.
ALTER TABLE unit_link_requests DROP COLUMN reason;

ALTER TABLE unit_link_requests DROP CONSTRAINT unit_link_requests_status_check;
ALTER TABLE unit_link_requests ADD CONSTRAINT unit_link_requests_status_check
    CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled'));

-- One live application per renter per unit: the 409 in POST
-- /units/{unit_code}/link is enforced by the database, not only by a
-- check-then-insert that two taps could race through.
CREATE UNIQUE INDEX unit_link_requests_pending_key
    ON unit_link_requests (unit_id, renter_user_id)
    WHERE status = 'pending' AND deleted_at IS NULL;

-- The landlord inbox pages by (created_at, id) inside an org, and the renter's
-- own list pages by the same key inside their own requests.
CREATE INDEX unit_link_requests_org_created_idx
    ON unit_link_requests (org_id, created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX unit_link_requests_renter_created_idx
    ON unit_link_requests (renter_user_id, created_at DESC, id DESC) WHERE deleted_at IS NULL;

-- Phase 3 sends two notification kinds; the scheduler kinds arrive in Phase 6.
ALTER TABLE notification_log DROP CONSTRAINT notification_log_kind_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_kind_check
    CHECK (kind IN ('reminder_7d', 'reminder_due', 'overdue_daily', 'thank_you',
                    'otp', 'custom', 'link_approved', 'link_rejected'));

-- The worker re-enqueues rows Redis lost, oldest first.
CREATE INDEX notification_log_queued_idx
    ON notification_log (created_at) WHERE status = 'queued';

-- notification_log carries the recipient's phone so the worker can send
-- without a join back to users (the number is already in `payload`, but a
-- column keeps the send path a single-row read).
ALTER TABLE notification_log ADD COLUMN to_phone TEXT NOT NULL DEFAULT '';
ALTER TABLE notification_log ADD COLUMN body TEXT NOT NULL DEFAULT '';
ALTER TABLE notification_log ADD COLUMN error TEXT;
ALTER TABLE notification_log ADD COLUMN attempts INT NOT NULL DEFAULT 0;
