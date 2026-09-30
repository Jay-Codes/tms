DELETE FROM platform_templates WHERE kind = 'contract_amendment';
DELETE FROM notification_log WHERE kind = 'contract_amendment';
ALTER TABLE notification_log DROP CONSTRAINT notification_log_kind_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_kind_check
    CHECK (kind IN ('reminder_7d', 'reminder_due', 'overdue_daily', 'thank_you',
                    'otp', 'custom', 'link_approved', 'link_rejected',
                    'contract_ready', 'welcome', 'contract_terminated',
                    'unsigned_reminder', 'proof_rejected',
                    'name_corrected', 'backfill_done',
                    'notice_received', 'eviction_demand', 'eviction_notice', 'eviction_withdrawn',
                    'payment_reversed', 'payment_corrected'));

DROP INDEX IF EXISTS contracts_amendment_stage_idx;
DROP INDEX contracts_one_open_amendment;
CREATE UNIQUE INDEX contracts_one_open_amendment ON contracts (supersedes_contract_id)
    WHERE status = 'pending_signature' AND amendment_effective_date IS NOT NULL
      AND deleted_at IS NULL;

ALTER TABLE contracts
    DROP COLUMN amendment_stage,
    DROP COLUMN amendment_note_sw,
    DROP COLUMN amendment_note_en,
    DROP COLUMN amendment_body_html,
    DROP COLUMN amendment_drafted_by,
    DROP COLUMN amendment_submitted_by,
    DROP COLUMN amendment_submitted_at,
    DROP COLUMN amendment_reviewed_by,
    DROP COLUMN amendment_reviewed_at,
    DROP COLUMN amendment_review_note,
    DROP COLUMN amendment_declined_at,
    DROP COLUMN amendment_decline_reason;
