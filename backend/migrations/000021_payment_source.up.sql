-- Phase 20 §20.3 — where a payment came from.
--
-- Until now "imported" was derived from `payments.import_batch_id` alone, which
-- answered one question (which spreadsheet?) and could not answer the one the
-- backfill introduces: was this money keyed in as it arrived, loaded from a
-- file, or written to close a period that predates TMS? `source` is that
-- answer, and it is what the ledger filter and the "imported"/"backfilled"
-- chips read. `import_batch_id` stays: it still names the batch and is still
-- what the 24 h undo walks.

ALTER TABLE payments
    ADD COLUMN source TEXT NOT NULL DEFAULT 'manual'
        CHECK (source IN ('manual', 'import', 'backfill'));

-- Every row that arrived on a CSV import is an `import` row: the derived chip
-- becomes the stored value, so the two can never disagree afterwards.
UPDATE payments SET source = 'import' WHERE import_batch_id IS NOT NULL;

-- `GET /payments?source=` is always org-scoped, and the ledger reads the newest
-- first, so the index carries the order the listing uses.
CREATE INDEX payments_org_source_idx ON payments (org_id, source, paid_at DESC)
    WHERE deleted_at IS NULL;

-- ------------------------------------------------------------------- kinds --

-- Two new messages, one per phase: `name_corrected` (§19.3) tells a renter that
-- somebody else changed the spelling of their name, and `backfill_done` (§20.3)
-- is the single text a settlement of pre-TMS history raises instead of one per
-- period. The rest of the list is 000018's, unchanged.
ALTER TABLE notification_log DROP CONSTRAINT notification_log_kind_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_kind_check
    CHECK (kind IN ('reminder_7d', 'reminder_due', 'overdue_daily', 'thank_you',
                    'otp', 'custom', 'link_approved', 'link_rejected',
                    'contract_ready', 'welcome', 'contract_terminated',
                    'unsigned_reminder', 'proof_rejected',
                    'name_corrected', 'backfill_done'));

-- ------------------------------------------------- platform template seed --

-- Byte-identical to the Go defaults in internal/notify/templates.go, the way
-- 000016 and 000018 seeded the rest of the catalogue. `{{date}}` is
-- platform-only — offered to this one sentence, never to the nine an org may
-- use anywhere — exactly as `{{start_date}}` is for `welcome`.
INSERT INTO platform_templates (kind, sw, en, variables, locked) VALUES
    ('name_corrected',
     'Jina lako katika {{org}} limerekebishwa kuwa {{name}}. Kama si sahihi, libadilishe kwenye Wasifu wako: {{link}}',
     'Your name at {{org}} was corrected to {{name}}. If that is wrong, fix it in your Profile: {{link}}',
     ARRAY['amount', 'due_date', 'link', 'name', 'next_due_date', 'org', 'pay_link',
           'property', 'unit']::text[],
     false),
    ('backfill_done',
     'Daftari lako la kodi katika {{org}} sasa linaonyesha historia hadi {{date}}. Iangalie hapa: {{pay_link}}',
     'Your rent book at {{org}} now shows history up to {{date}}. See it here: {{pay_link}}',
     ARRAY['amount', 'date', 'due_date', 'link', 'name', 'next_due_date', 'org', 'pay_link',
           'property', 'unit']::text[],
     false)
ON CONFLICT (kind) DO NOTHING;
