-- Phase 24 — correcting payments, and telling people.
--
--   * `payments.idempotency_key`: a double-tap on "Record payment" replays the
--     first answer instead of writing the money twice;
--   * `payments.corrects_payment_id`: a correction is a reversal plus a new
--     payment, and the new one names what it corrects;
--   * `org_inbox`: the landlord's in-app notices (a bell), per org, read
--     per user — the free channel to landlords (no landlord SMS, no e-mail
--     provider yet);
--   * SMS kinds `payment_reversed` and `payment_corrected` for the renter.

ALTER TABLE payments
    ADD COLUMN idempotency_key     TEXT CHECK (char_length(idempotency_key) <= 80),
    ADD COLUMN corrects_payment_id UUID REFERENCES payments (id);
CREATE UNIQUE INDEX payments_idempotency_key ON payments (org_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE TABLE org_inbox (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    kind          TEXT NOT NULL,
    title         TEXT NOT NULL CHECK (char_length(title) <= 160),
    body          TEXT NOT NULL DEFAULT '' CHECK (char_length(body) <= 500),
    entity_type   TEXT,
    entity_id     UUID,
    link          TEXT,
    actor_user_id UUID REFERENCES users (id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX org_inbox_org_idx ON org_inbox (org_id, created_at DESC, id DESC);

CREATE TABLE org_inbox_reads (
    notification_id UUID NOT NULL REFERENCES org_inbox (id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    org_id          UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    read_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (notification_id, user_id)
);

ALTER TABLE notification_log DROP CONSTRAINT notification_log_kind_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_kind_check
    CHECK (kind IN ('reminder_7d', 'reminder_due', 'overdue_daily', 'thank_you',
                    'otp', 'custom', 'link_approved', 'link_rejected',
                    'contract_ready', 'welcome', 'contract_terminated',
                    'unsigned_reminder', 'proof_rejected',
                    'name_corrected', 'backfill_done',
                    'notice_received', 'eviction_demand', 'eviction_notice', 'eviction_withdrawn',
                    'payment_reversed', 'payment_corrected'));
