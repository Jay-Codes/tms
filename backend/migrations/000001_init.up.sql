-- Phase 0: extensions only. Phase 1 lands the full SPEC §4 schema.
-- pgcrypto backs encrypted columns (e.g. renter_profiles.nida_number).
CREATE EXTENSION IF NOT EXISTS pgcrypto;
