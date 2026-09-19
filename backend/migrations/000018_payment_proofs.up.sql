-- Phase 16.1: proof of payment (PLAN2 §16.1, SPEC §6).
--
-- A proof is a *claim*, a payment is a *fact*. A renter who paid into the
-- landlord's bank account has no way to make the ledger say so; today they
-- phone. This table is that message, with the screenshot attached, and it
-- touches no schedule of its own: only an accepted proof runs the allocator,
-- and the payment it produces is the fact the ledger records.
--
-- The object itself lives in the `proofs` bucket under `{org_id}/{proof_id}.ext`
-- (SPEC §7), and `content_type`/`size_bytes` are what MinIO reported at
-- completion, never what the client claimed — the receipts rule.
--
-- `payment_id` is nullable and stays set once an accepted proof's payment is
-- reversed: the proof remains `accepted` with the payment stamped reversed,
-- because a claim that was believed and later corrected is part of the record.

CREATE TABLE payment_proofs (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id         UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    contract_id    UUID NOT NULL REFERENCES contracts (id) ON DELETE CASCADE,
    -- The instalment the renter says the money is for. Optional: a renter who
    -- does not know which row they are paying still has a claim worth filing,
    -- and the allocator's earliest-unpaid default is the right answer then.
    schedule_id    UUID REFERENCES payment_schedules (id) ON DELETE SET NULL,
    renter_user_id UUID NOT NULL REFERENCES users (id),

    amount  BIGINT      NOT NULL CHECK (amount > 0),
    paid_at TIMESTAMPTZ NOT NULL,
    -- Cash is deliberately absent: money handed over in person is recorded by
    -- the landlord, who was there. A proof is for the two methods that leave
    -- the renter holding the only evidence (FLOWS 7).
    method  TEXT        NOT NULL CHECK (method IN ('bank_transfer', 'mobile_money_manual')),

    reference TEXT CHECK (reference IS NULL OR char_length(reference) <= 80),
    note      TEXT CHECK (note      IS NULL OR char_length(note)      <= 500),

    object_key   TEXT   NOT NULL,
    content_type TEXT   NOT NULL CHECK (content_type IN ('image/jpeg', 'image/png', 'application/pdf')),
    size_bytes   BIGINT NOT NULL CHECK (size_bytes > 0),

    status     TEXT NOT NULL DEFAULT 'submitted'
               CHECK (status IN ('submitted', 'accepted', 'rejected')),
    payment_id UUID REFERENCES payments (id) ON DELETE SET NULL,

    reviewed_by_user_id UUID REFERENCES users (id),
    reviewed_at         TIMESTAMPTZ,
    rejection_reason    TEXT CHECK (rejection_reason IS NULL OR char_length(rejection_reason) <= 200),

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A decided proof carries who decided it and when, or it is still submitted:
-- half a review is not a state the API can produce.
ALTER TABLE payment_proofs ADD CONSTRAINT payment_proofs_review_check
    CHECK ((status =  'submitted' AND reviewed_at IS NULL     AND reviewed_by_user_id IS NULL)
        OR (status <> 'submitted' AND reviewed_at IS NOT NULL AND reviewed_by_user_id IS NOT NULL));

-- A rejection says why; an acceptance does not pretend to.
ALTER TABLE payment_proofs ADD CONSTRAINT payment_proofs_rejection_check
    CHECK ((status =  'rejected' AND rejection_reason IS NOT NULL)
        OR (status <> 'rejected' AND rejection_reason IS NULL));

-- One object per proof: the key is derived from the two ids the server holds,
-- so a duplicate would mean two rows pointing at one file.
CREATE UNIQUE INDEX payment_proofs_object_key_key ON payment_proofs (object_key);

-- The landlord's review queue: one org, one status, oldest claim first.
CREATE INDEX payment_proofs_org_status_created_idx
    ON payment_proofs (org_id, status, created_at DESC, id DESC);

-- The nav badge and the first tab: the pending set is a handful of rows in a
-- table that grows for the life of the org, so it gets its own partial index.
CREATE INDEX payment_proofs_org_pending_idx
    ON payment_proofs (org_id, created_at, id)
    WHERE status = 'submitted';

-- The renter's own history (GET /me/proofs), newest first, across every org
-- they rent from.
CREATE INDEX payment_proofs_renter_created_idx
    ON payment_proofs (renter_user_id, created_at DESC, id DESC);

CREATE INDEX payment_proofs_contract_idx ON payment_proofs (contract_id, created_at DESC);
CREATE INDEX payment_proofs_payment_idx  ON payment_proofs (payment_id);

CREATE TRIGGER payment_proofs_set_updated_at BEFORE UPDATE ON payment_proofs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ------------------------------------------------------------------ kinds --

-- `proof_rejected` is the one message a proof raises. Acceptance already has a
-- message — the existing `thank_you` the allocator queues — and submission
-- raises none: the landlord's badge is the signal, the same rule signing
-- follows (PLAN2 §16.1).
ALTER TABLE notification_log DROP CONSTRAINT notification_log_kind_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_kind_check
    CHECK (kind IN ('reminder_7d', 'reminder_due', 'overdue_daily', 'thank_you',
                    'otp', 'custom', 'link_approved', 'link_rejected',
                    'contract_ready', 'welcome', 'contract_terminated',
                    'unsigned_reminder', 'proof_rejected'));

-- ------------------------------------------------- platform template seed --

-- Byte-identical to the Go default in internal/notify/templates.go, the way
-- 000016 seeded the rest of the catalogue. `{{reason}}` is platform-only —
-- offered to this kind's sentence, never to the eight an org may use anywhere
-- — exactly as `contract_terminated` and `link_rejected` carry it.
INSERT INTO platform_templates (kind, sw, en, variables, locked) VALUES
    ('proof_rejected',
     'Uthibitisho wako wa malipo ya {{amount}} kwa {{unit}} katika {{org}} haujakubaliwa: {{reason}}',
     'Your payment proof of {{amount}} for {{unit}} at {{org}} was not accepted: {{reason}}',
     ARRAY['amount', 'due_date', 'link', 'name', 'next_due_date', 'org', 'property', 'reason', 'unit']::text[],
     false)
ON CONFLICT (kind) DO NOTHING;
