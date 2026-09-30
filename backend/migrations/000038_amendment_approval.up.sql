-- Phase 31 — contract changes need an owner's approval (maker-checker).
--
-- An amendment (or renewal) is now written as a draft the renter cannot see:
-- a manager or owner drafts it with a mandatory note in Swahili and English,
-- an owner approves it (or returns / rejects it), and only then does it become
-- `pending_signature` and reach the renter, who signs or declines.
--
--   amendment_stage          draft | submitted | approved | rejected | declined | withdrawn
--                            (NULL on a contract that amends nothing)
--   amendment_note_sw / _en  the note to the renter, both languages
--   amendment_body_html      this contract's own wording (template form, with
--                            {{variables}}), NULL = render the template
--   amendment_review_note    the owner's reason on return / reject
--   amendment_decline_reason the renter's reason on decline
ALTER TABLE contracts
    ADD COLUMN amendment_stage TEXT
        CHECK (amendment_stage IN ('draft', 'submitted', 'approved', 'rejected', 'declined', 'withdrawn')),
    ADD COLUMN amendment_note_sw        TEXT CHECK (char_length(amendment_note_sw) <= 200),
    ADD COLUMN amendment_note_en        TEXT CHECK (char_length(amendment_note_en) <= 200),
    ADD COLUMN amendment_body_html      TEXT,
    ADD COLUMN amendment_drafted_by     UUID REFERENCES users (id),
    ADD COLUMN amendment_submitted_by   UUID REFERENCES users (id),
    ADD COLUMN amendment_submitted_at   TIMESTAMPTZ,
    ADD COLUMN amendment_reviewed_by    UUID REFERENCES users (id),
    ADD COLUMN amendment_reviewed_at    TIMESTAMPTZ,
    ADD COLUMN amendment_review_note    TEXT CHECK (char_length(amendment_review_note) <= 200),
    ADD COLUMN amendment_declined_at    TIMESTAMPTZ,
    ADD COLUMN amendment_decline_reason TEXT CHECK (char_length(amendment_decline_reason) <= 200);

-- Amendments written before this phase went straight to the renter.
UPDATE contracts SET amendment_stage = 'approved' WHERE amendment_effective_date IS NOT NULL;

-- One open amendment per contract now counts drafts as well.
DROP INDEX contracts_one_open_amendment;
CREATE UNIQUE INDEX contracts_one_open_amendment ON contracts (supersedes_contract_id)
    WHERE status IN ('draft', 'pending_signature') AND amendment_effective_date IS NOT NULL
      AND deleted_at IS NULL;

CREATE INDEX contracts_amendment_stage_idx ON contracts (org_id, amendment_stage)
    WHERE amendment_stage IN ('draft', 'submitted') AND deleted_at IS NULL;

ALTER TABLE notification_log DROP CONSTRAINT notification_log_kind_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_kind_check
    CHECK (kind IN ('reminder_7d', 'reminder_due', 'overdue_daily', 'thank_you',
                    'otp', 'custom', 'link_approved', 'link_rejected',
                    'contract_ready', 'welcome', 'contract_terminated',
                    'unsigned_reminder', 'proof_rejected',
                    'name_corrected', 'backfill_done',
                    'notice_received', 'eviction_demand', 'eviction_notice', 'eviction_withdrawn',
                    'payment_reversed', 'payment_corrected', 'contract_amendment'));
