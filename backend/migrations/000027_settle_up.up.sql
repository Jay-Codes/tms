-- Phase 22 §22.5 — settling up when a tenancy ends, and deposits.
--
-- The contract's policy (§22.2) now drives termination: the period the
-- tenancy ends in is charged in full or pro rata, and rent already paid for
-- periods after the end is refunded or kept. A refund is money going back
-- out, so it gets its own ledger, and cash reports subtract it in the period it
-- was paid out. Deposits are not rent: they have their own ledger per contract.

CREATE TABLE rent_refunds (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id              UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    contract_id         UUID NOT NULL REFERENCES contracts (id) ON DELETE CASCADE,
    amount              BIGINT NOT NULL CHECK (amount > 0),
    method              TEXT NOT NULL CHECK (method IN ('cash', 'bank_transfer', 'mobile_money_manual')),
    reference           TEXT CHECK (char_length(reference) <= 80),
    reason              TEXT NOT NULL CHECK (char_length(reason) <= 200),
    refunded_at         TIMESTAMPTZ NOT NULL,
    recorded_by_user_id UUID REFERENCES users (id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX rent_refunds_org_idx ON rent_refunds (org_id, refunded_at);
CREATE INDEX rent_refunds_contract_idx ON rent_refunds (contract_id);

-- Which payments a refund took its money from: a payment that has been
-- partly refunded cannot then be reversed as if all of it were still held.
CREATE TABLE rent_refund_items (
    refund_id  UUID NOT NULL REFERENCES rent_refunds (id) ON DELETE CASCADE,
    org_id     UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    payment_id UUID NOT NULL REFERENCES payments (id) ON DELETE CASCADE,
    amount     BIGINT NOT NULL CHECK (amount > 0),
    PRIMARY KEY (refund_id, payment_id)
);
CREATE INDEX rent_refund_items_payment_idx ON rent_refund_items (payment_id);

CREATE TABLE deposit_entries (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id              UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    contract_id         UUID NOT NULL REFERENCES contracts (id) ON DELETE CASCADE,
    kind                TEXT NOT NULL CHECK (kind IN ('received', 'deduction', 'refund', 'applied_to_rent')),
    amount              BIGINT NOT NULL CHECK (amount > 0),
    method              TEXT CHECK (method IN ('cash', 'bank_transfer', 'mobile_money_manual')),
    reference           TEXT CHECK (char_length(reference) <= 80),
    reason              TEXT CHECK (char_length(reason) <= 200),
    payment_id          UUID REFERENCES payments (id),   -- applied_to_rent: the rent payment it became
    occurred_at         TIMESTAMPTZ NOT NULL,
    recorded_by_user_id UUID REFERENCES users (id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX deposit_entries_contract_idx ON deposit_entries (org_id, contract_id, occurred_at);

-- Deposit money applied to rent becomes an ordinary rent payment.
ALTER TABLE payments DROP CONSTRAINT payments_method_check;
ALTER TABLE payments ADD CONSTRAINT payments_method_check
    CHECK (method IN ('cash', 'bank_transfer', 'mobile_money_manual', 'gateway', 'deposit'));

-- The settlement a termination applied, for the contract page and the audit.
ALTER TABLE contracts ADD COLUMN settlement JSONB;
