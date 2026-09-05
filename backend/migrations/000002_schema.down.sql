-- Reverse of 000002_schema.up.sql. Order matters only where FKs are not
-- covered by CASCADE; DROP ... CASCADE keeps this robust.

DROP TRIGGER IF EXISTS audit_log_no_delete ON audit_log;
DROP TRIGGER IF EXISTS audit_log_no_update ON audit_log;

DROP TABLE IF EXISTS sessions CASCADE;
DROP TABLE IF EXISTS audit_log CASCADE;
DROP TABLE IF EXISTS notification_log CASCADE;
DROP TABLE IF EXISTS payments CASCADE;
DROP TABLE IF EXISTS payment_schedules CASCADE;
DROP TABLE IF EXISTS unit_link_requests CASCADE;
DROP TABLE IF EXISTS contract_signatures CASCADE;
DROP TABLE IF EXISTS contracts CASCADE;
DROP TABLE IF EXISTS contract_templates CASCADE;
DROP TABLE IF EXISTS price_plans CASCADE;
DROP TABLE IF EXISTS units CASCADE;
DROP TABLE IF EXISTS properties CASCADE;
DROP TABLE IF EXISTS renter_profiles CASCADE;
DROP TABLE IF EXISTS org_members CASCADE;
DROP TABLE IF EXISTS users CASCADE;
DROP TABLE IF EXISTS org_branding CASCADE;
DROP TABLE IF EXISTS payment_periods CASCADE;
DROP TABLE IF EXISTS orgs CASCADE;

DROP FUNCTION IF EXISTS audit_log_append_only();
DROP FUNCTION IF EXISTS set_updated_at();
