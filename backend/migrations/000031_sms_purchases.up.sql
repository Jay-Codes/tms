-- Phase 27 — landlords buy SMS credits with mobile money (Snippe), and the
-- platform tracks its own SMS stock.
--
--   * `sms_credit_packages`: the bundles on sale. Admin-managed rows — no
--     price lives in code, because the client has not set one yet.
--   * `sms_credit_orders`: one purchase attempt. Credits, price and name are
--     copied from the package at order time, so a later price edit cannot
--     change what an order was for. `order_code` (≤30 chars) is sent to Snippe
--     as the Idempotency-Key and in the payment metadata.
--   * `snippe_webhook_events`: every verified delivery, keyed by Snippe's own
--     event id — the dedupe that makes a retried webhook a no-op.
--   * `platform_sms_purchases`: the Beem bundles the platform buys, so stock
--     (bought − sent) can be set against what orgs hold (the liability).
--   * ledger reason `purchase`: credits an org paid for itself, distinct from
--     an admin `topup`.

ALTER TABLE sms_credit_ledger DROP CONSTRAINT sms_credit_ledger_reason_check;
ALTER TABLE sms_credit_ledger ADD CONSTRAINT sms_credit_ledger_reason_check
    CHECK (reason IN ('topup', 'adjust', 'debit', 'refund', 'purchase'));

CREATE TABLE sms_credit_packages (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
    credits             INT NOT NULL CHECK (credits > 0),
    -- Integer TZS. Snippe refuses anything under 500.
    price               BIGINT NOT NULL CHECK (price >= 500),
    active              BOOLEAN NOT NULL DEFAULT true,
    sort_order          INT NOT NULL DEFAULT 0,
    created_by_admin_id UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TRIGGER sms_credit_packages_set_updated_at BEFORE UPDATE ON sms_credit_packages
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE sms_credit_orders (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    package_id         UUID REFERENCES sms_credit_packages (id) ON DELETE SET NULL,
    package_name       TEXT NOT NULL,
    credits            INT NOT NULL CHECK (credits > 0),
    amount             BIGINT NOT NULL CHECK (amount >= 500),
    payer_phone        TEXT NOT NULL,
    order_code         TEXT NOT NULL UNIQUE CHECK (char_length(order_code) BETWEEN 8 AND 30),
    snippe_reference   TEXT UNIQUE,
    -- `mismatch`: Snippe reported a completed payment whose amount or
    -- currency is not the order's. Never credited automatically; an admin
    -- looks at it.
    status             TEXT NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending', 'completed', 'failed', 'expired', 'mismatch')),
    failure_reason     TEXT CHECK (char_length(failure_reason) <= 300),
    created_by_user_id UUID REFERENCES users (id) ON DELETE SET NULL,
    last_checked_at    TIMESTAMPTZ,
    credited_at        TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX sms_credit_orders_org_idx ON sms_credit_orders (org_id, created_at DESC, id DESC);
CREATE INDEX sms_credit_orders_pending_idx ON sms_credit_orders (created_at)
    WHERE status = 'pending';
CREATE TRIGGER sms_credit_orders_set_updated_at BEFORE UPDATE ON sms_credit_orders
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE snippe_webhook_events (
    event_id    TEXT PRIMARY KEY CHECK (char_length(event_id) BETWEEN 1 AND 200),
    event_type  TEXT NOT NULL,
    reference   TEXT,
    order_id    UUID REFERENCES sms_credit_orders (id) ON DELETE CASCADE,
    outcome     TEXT NOT NULL,
    payload     JSONB NOT NULL DEFAULT '{}'::jsonb,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE platform_sms_purchases (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sms_count           INT NOT NULL CHECK (sms_count > 0),
    -- Integer TZS actually paid to Beem; 0 for an opening balance.
    cost                BIGINT NOT NULL CHECK (cost >= 0),
    purchased_on        DATE NOT NULL,
    reference           TEXT CHECK (char_length(reference) <= 120),
    note                TEXT NOT NULL DEFAULT '' CHECK (char_length(note) <= 500),
    created_by_admin_id UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX platform_sms_purchases_date_idx ON platform_sms_purchases (purchased_on DESC, created_at DESC);
