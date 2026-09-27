-- Phase 22 §22.5 (rest) — the tenancy's unhappy paths.
--
--   * a single period waived or discounted (rent relief, a repair, a deal),
--     reversibly: `original_amount` remembers what it was;
--   * the renter's own notice to leave, on the contract;
--   * a holdover check: a tenancy that ran out is confirmed moved-out (or
--     renewed) rather than silently assumed empty;
--   * eviction as stages — demand, notice to vacate, then vacated or withdrawn.

ALTER TABLE payment_schedules
    ADD COLUMN original_amount    BIGINT,
    ADD COLUMN adjustment_kind    TEXT CHECK (adjustment_kind IN ('waive', 'discount')),
    ADD COLUMN adjustment_reason  TEXT CHECK (char_length(adjustment_reason) <= 200),
    ADD COLUMN adjusted_at        TIMESTAMPTZ,
    ADD COLUMN adjusted_by_user_id UUID REFERENCES users (id);

ALTER TABLE contracts
    ADD COLUMN notice_given_at        TIMESTAMPTZ,
    ADD COLUMN notice_leave_on        DATE,
    ADD COLUMN notice_reason          TEXT CHECK (char_length(notice_reason) <= 200),
    ADD COLUMN moved_out_confirmed_at TIMESTAMPTZ;

CREATE TABLE eviction_cases (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    contract_id        UUID NOT NULL REFERENCES contracts (id) ON DELETE CASCADE,
    stage              TEXT NOT NULL CHECK (stage IN ('demand', 'notice', 'vacated', 'withdrawn')),
    arrears_at_open    BIGINT NOT NULL,
    notice_days        INT NOT NULL CHECK (notice_days BETWEEN 0 AND 365),
    demand_issued_at   TIMESTAMPTZ NOT NULL,
    pay_by             DATE NOT NULL,
    notice_issued_at   TIMESTAMPTZ,
    vacate_by          DATE,
    closed_at          TIMESTAMPTZ,
    close_reason       TEXT CHECK (char_length(close_reason) <= 200),
    opened_by_user_id  UUID REFERENCES users (id),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX eviction_cases_org_idx ON eviction_cases (org_id, stage);
-- One open case per contract.
CREATE UNIQUE INDEX eviction_cases_one_open ON eviction_cases (contract_id)
    WHERE stage IN ('demand', 'notice');

ALTER TABLE notification_log DROP CONSTRAINT notification_log_kind_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_kind_check
    CHECK (kind IN ('reminder_7d', 'reminder_due', 'overdue_daily', 'thank_you',
                    'otp', 'custom', 'link_approved', 'link_rejected',
                    'contract_ready', 'welcome', 'contract_terminated',
                    'unsigned_reminder', 'proof_rejected',
                    'name_corrected', 'backfill_done',
                    'notice_received', 'eviction_demand', 'eviction_notice', 'eviction_withdrawn'));
