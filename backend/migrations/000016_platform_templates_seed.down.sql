-- Remove the seeded catalogue. Rows an admin has edited are recognisable by
-- their version having moved past 1, and are left alone: a rollback of the
-- seed must not throw away wording somebody wrote.
DELETE FROM platform_template_versions
WHERE kind IN (SELECT kind FROM platform_templates WHERE version = 1);
DELETE FROM platform_templates WHERE version = 1;
