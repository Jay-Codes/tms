-- Phase 1: full SPEC §4 schema.
--
-- Conventions (consistent across every table):
--   * id UUID PK DEFAULT gen_random_uuid()  (pgcrypto, enabled in 000001)
--   * created_at / updated_at TIMESTAMPTZ NOT NULL DEFAULT now(); updated_at
--     maintained by the set_updated_at() trigger.
--   * Enumerations are TEXT + CHECK constraints (not PG enum types) so adding a
--     value later is a one-line ALTER ... DROP/ADD CONSTRAINT instead of an
--     ALTER TYPE that cannot run inside some transactional migrations.
--   * Org-scoped tables carry org_id UUID NOT NULL REFERENCES orgs(id) and an
--     index on org_id (SPEC §2.1).
--   * User-visible entities carry deleted_at TIMESTAMPTZ for soft delete.

-- ---------------------------------------------------------------- helpers --

CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- ------------------------------------------------------------------- orgs --

CREATE TABLE orgs (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL,
    slug       TEXT NOT NULL UNIQUE,
    status     TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    settings   JSONB NOT NULL DEFAULT '{
        "auto_approve_links": false,
        "due_day": null,
        "grace_days": 3,
        "reminder_offsets_days": [7, 0],
        "unsigned_reminder_days": 7,
        "sms_language": "sw"
    }'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);
CREATE TRIGGER orgs_set_updated_at BEFORE UPDATE ON orgs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- -------------------------------------------------------- payment_periods --

CREATE TABLE payment_periods (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    label           TEXT NOT NULL,
    days            INT NOT NULL CHECK (days > 0),
    is_recommended  BOOLEAN NOT NULL DEFAULT false,
    sort_order      INT NOT NULL DEFAULT 0,
    active          BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ
);
CREATE INDEX payment_periods_org_id_idx ON payment_periods (org_id);
CREATE TRIGGER payment_periods_set_updated_at BEFORE UPDATE ON payment_periods
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ------------------------------------------------------------ org_branding --

CREATE TABLE org_branding (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id                 UUID NOT NULL UNIQUE REFERENCES orgs (id) ON DELETE CASCADE,
    display_name           TEXT NOT NULL,
    logo_object_key        TEXT,
    letterhead_object_key  TEXT,
    theme                  JSONB NOT NULL DEFAULT '{"primary_color": "#1B4DB1", "font_id": "bricolage"}'::jsonb,
    dashboard_prefs        JSONB NOT NULL DEFAULT '{}'::jsonb,
    document_footer_text   TEXT,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX org_branding_org_id_idx ON org_branding (org_id);
CREATE TRIGGER org_branding_set_updated_at BEFORE UPDATE ON org_branding
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ------------------------------------------------------------------ users --

CREATE TABLE users (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind              TEXT NOT NULL CHECK (kind IN ('renter', 'org_user', 'platform_admin')),
    phone             TEXT,
    email             TEXT,
    full_name         TEXT NOT NULL DEFAULT '',
    -- renters authenticate with a PIN, org users / admins with a password.
    pin_hash          TEXT,
    password_hash     TEXT,
    email_verified_at TIMESTAMPTZ,
    status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ
);
-- Phone/email are unique among live rows only, so a soft-deleted account does
-- not permanently burn an identifier.
CREATE UNIQUE INDEX users_phone_key ON users (phone) WHERE phone IS NOT NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX users_email_key ON users (lower(email)) WHERE email IS NOT NULL AND deleted_at IS NULL;
CREATE TRIGGER users_set_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ------------------------------------------------------------- org_members --

CREATE TABLE org_members (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('org_owner', 'org_manager')),
    status     TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX org_members_org_id_idx ON org_members (org_id);
CREATE INDEX org_members_user_id_idx ON org_members (user_id);
CREATE UNIQUE INDEX org_members_org_user_key ON org_members (org_id, user_id) WHERE deleted_at IS NULL;
CREATE TRIGGER org_members_set_updated_at BEFORE UPDATE ON org_members
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- --------------------------------------------------------- renter_profiles --

-- nida_number is encrypted at rest with pgcrypto (SPEC §8). The symmetric key
-- comes from the NIDA_ENC_KEY env var and is passed as a query parameter —
-- it is never stored in the database.
CREATE TABLE renter_profiles (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            UUID NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    full_name          TEXT NOT NULL DEFAULT '',
    nida_number_enc    BYTEA,
    next_of_kin_name   TEXT,
    next_of_kin_phone  TEXT,
    kyc_status         TEXT NOT NULL DEFAULT 'incomplete'
                       CHECK (kyc_status IN ('incomplete', 'submitted', 'verified', 'rejected')),
    kyc_doc_object_key TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ
);
CREATE TRIGGER renter_profiles_set_updated_at BEFORE UPDATE ON renter_profiles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ------------------------------------------------------------- properties --

CREATE TABLE properties (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    location_text TEXT NOT NULL DEFAULT '',
    lat           DOUBLE PRECISION,
    lng           DOUBLE PRECISION,
    notes         TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ
);
CREATE INDEX properties_org_id_idx ON properties (org_id);
CREATE TRIGGER properties_set_updated_at BEFORE UPDATE ON properties
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ------------------------------------------------------------------ units --

CREATE TABLE units (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    property_id        UUID NOT NULL REFERENCES properties (id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    unit_code          TEXT NOT NULL UNIQUE,
    status             TEXT NOT NULL DEFAULT 'vacant'
                       CHECK (status IN ('vacant', 'occupied', 'unlisted', 'maintenance')),
    allowed_period_ids UUID[],
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ
);
CREATE INDEX units_org_id_idx ON units (org_id);
CREATE INDEX units_property_id_idx ON units (property_id);
CREATE TRIGGER units_set_updated_at BEFORE UPDATE ON units
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ------------------------------------------------------------ price_plans --

CREATE TABLE price_plans (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id         UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    unit_id        UUID NOT NULL REFERENCES units (id) ON DELETE CASCADE,
    amount         BIGINT NOT NULL CHECK (amount >= 0),
    currency       TEXT NOT NULL DEFAULT 'TZS',
    period_days    INT NOT NULL DEFAULT 30 CHECK (period_days > 0),
    effective_from DATE NOT NULL DEFAULT CURRENT_DATE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ
);
CREATE INDEX price_plans_org_id_idx ON price_plans (org_id);
CREATE INDEX price_plans_unit_id_idx ON price_plans (unit_id, effective_from DESC);
CREATE TRIGGER price_plans_set_updated_at BEFORE UPDATE ON price_plans
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ------------------------------------------------------ contract_templates --

CREATE TABLE contract_templates (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    body_html  TEXT NOT NULL DEFAULT '',
    is_default BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX contract_templates_org_id_idx ON contract_templates (org_id);
CREATE TRIGGER contract_templates_set_updated_at BEFORE UPDATE ON contract_templates
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- -------------------------------------------------------------- contracts --

CREATE TABLE contracts (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id               UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    unit_id              UUID NOT NULL REFERENCES units (id),
    renter_user_id       UUID NOT NULL REFERENCES users (id),
    template_id          UUID REFERENCES contract_templates (id),
    terms_snapshot_html  TEXT NOT NULL DEFAULT '',
    rent_amount          BIGINT NOT NULL CHECK (rent_amount >= 0),
    rent_period_days     INT NOT NULL DEFAULT 30 CHECK (rent_period_days > 0),
    payment_period_id    UUID REFERENCES payment_periods (id),
    payment_period_days  INT NOT NULL CHECK (payment_period_days > 0),
    term_days            INT NOT NULL CHECK (term_days > 0),
    start_date           DATE NOT NULL,
    end_date             DATE NOT NULL,
    due_day              INT CHECK (due_day IS NULL OR (due_day BETWEEN 1 AND 28)),
    status               TEXT NOT NULL DEFAULT 'draft'
                         CHECK (status IN ('draft', 'pending_signature', 'active', 'expiring', 'ended', 'terminated')),
    snapshot_hash        TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at           TIMESTAMPTZ
);
CREATE INDEX contracts_org_id_idx ON contracts (org_id);
CREATE INDEX contracts_unit_id_idx ON contracts (unit_id);
CREATE INDEX contracts_renter_idx ON contracts (renter_user_id);
CREATE TRIGGER contracts_set_updated_at BEFORE UPDATE ON contracts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ----------------------------------------------------- contract_signatures --

CREATE TABLE contract_signatures (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id                UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    contract_id           UUID NOT NULL REFERENCES contracts (id) ON DELETE CASCADE,
    party                 TEXT NOT NULL CHECK (party IN ('renter', 'landlord')),
    user_id               UUID REFERENCES users (id),
    method                TEXT NOT NULL CHECK (method IN ('otp_accept', 'drawn', 'landlord_recorded')),
    otp_ref               TEXT,
    signature_object_key  TEXT,
    snapshot_hash         TEXT NOT NULL,
    ip                    TEXT,
    user_agent            TEXT,
    signed_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX contract_signatures_org_id_idx ON contract_signatures (org_id);
CREATE UNIQUE INDEX contract_signatures_contract_party_key ON contract_signatures (contract_id, party);

-- ------------------------------------------------------ unit_link_requests --

CREATE TABLE unit_link_requests (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id         UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    unit_id        UUID NOT NULL REFERENCES units (id) ON DELETE CASCADE,
    renter_user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    reason         TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ
);
CREATE INDEX unit_link_requests_org_id_idx ON unit_link_requests (org_id);
CREATE INDEX unit_link_requests_unit_id_idx ON unit_link_requests (unit_id);
CREATE TRIGGER unit_link_requests_set_updated_at BEFORE UPDATE ON unit_link_requests
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ------------------------------------------------------- payment_schedules --

CREATE TABLE payment_schedules (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    contract_id  UUID NOT NULL REFERENCES contracts (id) ON DELETE CASCADE,
    period_start DATE NOT NULL,
    period_end   DATE NOT NULL,
    due_date     DATE NOT NULL,
    amount       BIGINT NOT NULL CHECK (amount >= 0),
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'paid', 'partial', 'overdue', 'waived')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ
);
CREATE INDEX payment_schedules_org_id_idx ON payment_schedules (org_id);
CREATE INDEX payment_schedules_contract_idx ON payment_schedules (contract_id, period_start);
CREATE INDEX payment_schedules_due_idx ON payment_schedules (due_date) WHERE status IN ('pending', 'partial');
CREATE TRIGGER payment_schedules_set_updated_at BEFORE UPDATE ON payment_schedules
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ----------------------------------------------------------------- payments --

CREATE TABLE payments (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id              UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    contract_id         UUID NOT NULL REFERENCES contracts (id) ON DELETE CASCADE,
    schedule_id         UUID REFERENCES payment_schedules (id),
    amount              BIGINT NOT NULL CHECK (amount > 0),
    -- 'gateway' is reserved for the post-MVP seam (SPEC §5.7).
    method              TEXT NOT NULL CHECK (method IN ('cash', 'bank_transfer', 'mobile_money_manual', 'gateway')),
    reference           TEXT,
    paid_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    recorded_by_user_id UUID REFERENCES users (id),
    note                TEXT,
    status              TEXT NOT NULL DEFAULT 'recorded'
                        CHECK (status IN ('recorded', 'confirmed', 'reversed')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at          TIMESTAMPTZ
);
CREATE INDEX payments_org_id_idx ON payments (org_id);
CREATE INDEX payments_contract_idx ON payments (contract_id, paid_at DESC);
CREATE TRIGGER payments_set_updated_at BEFORE UPDATE ON payments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ------------------------------------------------------- notification_log --

CREATE TABLE notification_log (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    user_id         UUID REFERENCES users (id) ON DELETE SET NULL,
    kind            TEXT NOT NULL
                    CHECK (kind IN ('reminder_7d', 'reminder_due', 'overdue_daily', 'thank_you', 'otp', 'custom')),
    channel         TEXT NOT NULL DEFAULT 'sms' CHECK (channel IN ('sms')),
    dedupe_key      TEXT NOT NULL UNIQUE,
    payload         JSONB NOT NULL DEFAULT '{}'::jsonb,
    provider_msg_id TEXT,
    status          TEXT NOT NULL DEFAULT 'queued'
                    CHECK (status IN ('queued', 'sent', 'failed')),
    sent_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX notification_log_org_id_idx ON notification_log (org_id);
CREATE TRIGGER notification_log_set_updated_at BEFORE UPDATE ON notification_log
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- --------------------------------------------------------------- audit_log --

-- Append-only. SPEC §8 asks for a DB role with no UPDATE/DELETE grants; roles
-- are not portable across environments (the compose superuser owns everything
-- in dev), so append-only is enforced structurally by a trigger that raises on
-- any UPDATE or DELETE. A privileged operator can still drop the trigger for a
-- retention job — deliberately, and visibly.
CREATE TABLE audit_log (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id         UUID REFERENCES orgs (id) ON DELETE SET NULL,
    actor_user_id  UUID REFERENCES users (id) ON DELETE SET NULL,
    action         TEXT NOT NULL,
    entity_type    TEXT NOT NULL,
    entity_id      UUID,
    before         JSONB,
    after          JSONB,
    ip             TEXT,
    user_agent     TEXT,
    at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_org_id_idx ON audit_log (org_id);
CREATE INDEX audit_log_org_at_idx ON audit_log (org_id, at DESC, id DESC);
CREATE INDEX audit_log_entity_idx ON audit_log (org_id, entity_type, entity_id);
CREATE INDEX audit_log_actor_idx ON audit_log (org_id, actor_user_id);

CREATE OR REPLACE FUNCTION audit_log_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only: % is not permitted', TG_OP
        USING ERRCODE = 'insufficient_privilege';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_append_only();
CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_append_only();

-- ---------------------------------------------------------------- sessions --

-- Postgres fallback for Redis-backed sessions (SPEC §3). audience/role are
-- stored so a Principal can be rebuilt without extra joins after a Redis flush.
CREATE TABLE sessions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash TEXT NOT NULL UNIQUE,
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    org_id     UUID REFERENCES orgs (id) ON DELETE CASCADE,
    audience   TEXT NOT NULL CHECK (audience IN ('renter', 'org', 'admin')),
    role       TEXT,
    ip         TEXT,
    user_agent TEXT,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
CREATE TRIGGER sessions_set_updated_at BEFORE UPDATE ON sessions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Note: email verification tokens and OTP state live in Redis only (no table).
