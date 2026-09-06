-- Phase 14: seed the platform SMS catalogue (PLAN2 Phase 14, SPEC §5.12).
--
-- Migration 000012 created platform_templates empty, which left the code map in
-- internal/notify/templates.go as the only wording anyone had. This migration
-- makes the table authoritative from day one: every kind the code knows,
-- byte-identical to its Go default, with the placeholders that kind's sentence
-- may name and `otp` locked against org overrides.
--
-- ON CONFLICT DO NOTHING throughout, so re-running it never overwrites wording
-- an admin has since edited. The same catalogue is re-applied on every API
-- startup (notify.SeedPlatformTemplates), which is how a kind added in a later
-- phase reaches the table without another migration.
--
-- `thank_you_settled` is deliberately absent. It is a second wording of
-- `thank_you` — the sentence used when there is no next instalment to name —
-- not a notification kind, and there is one row per kind on the wire. It stays
-- a code default; an admin editing `thank_you` changes the instalment wording,
-- and the settled sentence keeps the platform's.

INSERT INTO platform_templates (kind, sw, en, variables, locked) VALUES
    ('contract_ready',
     'Mkataba wako wa {{unit}} katika {{org}} uko tayari kusainiwa. Fungua {{link}}',
     'Your contract for {{unit}} at {{org}} is ready to sign. Open {{link}}',
     ARRAY['amount', 'due_date', 'link', 'name', 'next_due_date', 'org', 'property', 'unit']::text[], false),
    ('contract_terminated',
     'Upangaji wako wa {{unit}} katika {{org}} umesitishwa: {{reason}}',
     'Your tenancy of {{unit}} at {{org}} has been ended: {{reason}}',
     ARRAY['amount', 'due_date', 'link', 'name', 'next_due_date', 'org', 'property', 'reason', 'unit']::text[], false),
    ('link_approved',
     'Ombi lako la {{unit}} katika {{org}} limekubaliwa. Mkataba wako utakuwa tayari kusainiwa hivi karibuni.',
     'Your request for {{unit}} at {{org}} was approved. Your contract will be ready to sign soon.',
     ARRAY['amount', 'due_date', 'link', 'name', 'next_due_date', 'org', 'property', 'unit']::text[], false),
    ('link_rejected',
     'Ombi lako la {{unit}} katika {{org}} halikukubaliwa: {{reason}}',
     'Your request for {{unit}} at {{org}} was not approved: {{reason}}',
     ARRAY['amount', 'due_date', 'link', 'name', 'next_due_date', 'org', 'property', 'reason', 'unit']::text[], false),
    ('otp',
     'TMS: msimbo wako wa uthibitisho ni {{code}}. Utaisha baada ya dakika 5.',
     'TMS: your verification code is {{code}}. It expires in 5 minutes.',
     ARRAY['code']::text[], true),
    ('overdue_daily',
     'Habari {{name}}, kodi yako ya {{amount}} kwa {{unit}} katika {{org}} ilitakiwa kulipwa tarehe {{due_date}} na bado haijalipwa.',
     'Hello {{name}}, your rent of {{amount}} for {{unit}} at {{org}} was due on {{due_date}} and is still outstanding.',
     ARRAY['amount', 'due_date', 'link', 'name', 'next_due_date', 'org', 'property', 'unit']::text[], false),
    ('reminder_7d',
     'Habari {{name}}, kodi yako ya {{amount}} kwa {{unit}} katika {{org}} inatakiwa kulipwa ifikapo tarehe {{due_date}}.',
     'Hello {{name}}, your rent of {{amount}} for {{unit}} at {{org}} is due on {{due_date}}.',
     ARRAY['amount', 'due_date', 'link', 'name', 'next_due_date', 'org', 'property', 'unit']::text[], false),
    ('reminder_due',
     'Habari {{name}}, kodi yako ya {{amount}} kwa {{unit}} katika {{org}} inatakiwa kulipwa leo, tarehe {{due_date}}.',
     'Hello {{name}}, your rent of {{amount}} for {{unit}} at {{org}} is due today, {{due_date}}.',
     ARRAY['amount', 'due_date', 'link', 'name', 'next_due_date', 'org', 'property', 'unit']::text[], false),
    ('thank_you',
     'Tumepokea malipo ya {{amount}} kwa {{unit}} katika {{org}}. Asante. Malipo yajayo ya {{next_amount}} yanatakiwa ifikapo {{next_due_date}}.',
     'Payment of {{amount}} for {{unit}} at {{org}} received. Thank you. Next payment {{next_amount}} due {{next_due_date}}.',
     ARRAY['amount', 'due_date', 'link', 'name', 'next_amount', 'next_due_date', 'org', 'property', 'unit']::text[], false),
    ('unsigned_reminder',
     'Habari {{name}}, mkataba wako wa {{unit}} katika {{org}} bado unasubiri saini yako. Fungua {{link}}',
     'Hello {{name}}, your contract for {{unit}} at {{org}} is still waiting for your signature. Open {{link}}',
     ARRAY['amount', 'due_date', 'link', 'name', 'next_due_date', 'org', 'property', 'unit']::text[], false),
    ('welcome',
     'Karibu {{org}}. Upangaji wako wa {{unit}} unaanza tarehe {{start_date}}. Malipo ya kwanza ya {{amount}} yanatakiwa ifikapo {{due_date}}.',
     'Welcome to {{org}}. Your tenancy at {{unit}} starts {{start_date}}. First payment {{amount}} due {{due_date}}.',
     ARRAY['amount', 'due_date', 'link', 'name', 'next_due_date', 'org', 'property', 'start_date', 'unit']::text[], false)
ON CONFLICT (kind) DO NOTHING;
