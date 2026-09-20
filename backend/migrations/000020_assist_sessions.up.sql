-- Phase 18: landlord-assisted onboarding (PLAN2 Phase 18, SPEC §3/§4/§5.15,
-- FLOWS 2b).
--
-- Beem accepted an OTP and left it `pending`; the renter never got the code and
-- had no way forward. Landlord and renter are usually in the same room at
-- onboarding — the QR is on the door — so the landlord's screen becomes the
-- code channel. Nothing is sent, and the renter still acts on their own device,
-- so the account, the PIN and the signature stay theirs.
--
-- This table is the *session*, never the code: the code itself lives only in
-- Redis, in the same slot the SMS path writes (`otp:{purpose}:{phone}`), which
-- is what leaves POST /auth/otp/verify, POST /auth/register/renter and
-- POST /contracts/{id}/sign untouched. What is durable here is the audit-worthy
-- shape of the encounter: which org user opened it, for which unit and number,
-- how many codes were shown, and how far the renter got.

CREATE TABLE assist_sessions (
    id      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id  UUID NOT NULL REFERENCES orgs (id)  ON DELETE CASCADE,
    unit_id UUID NOT NULL REFERENCES units (id) ON DELETE CASCADE,

    -- The renter's number in E.164, as every other phone column holds it. It
    -- is the join to the Redis slot, so it is stored exactly as validated.
    phone   TEXT NOT NULL,
    -- Which slot the code goes into: `register` for a number with no account,
    -- `login` for one that already has a renter account. Derived by the server
    -- at open time, never supplied by the client.
    purpose TEXT NOT NULL CHECK (purpose IN ('register', 'login')),

    started_by_user_id UUID NOT NULL REFERENCES users (id),

    -- Stamped as the renter progresses, which is the whole of `status_detail`:
    -- the account they created, then the application they filed.
    renter_user_id  UUID REFERENCES users (id)              ON DELETE SET NULL,
    link_request_id UUID REFERENCES unit_link_requests (id) ON DELETE SET NULL,

    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),

    -- How many codes this session has shown. Capped in the handler (10), and
    -- the number that names the `dedupe_key` of the notification_log row.
    code_issued_count INT NOT NULL DEFAULT 0 CHECK (code_issued_count >= 0),
    last_code_at      TIMESTAMPTZ,
    -- 30 minutes from the open, extended by each new code. A session past its
    -- expiry reads as closed everywhere, without a sweep having to run.
    expires_at        TIMESTAMPTZ NOT NULL,
    closed_at         TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One open session per (org, phone). Two landlords onboarding the same number
-- at once would write into one Redis slot and cancel each other's code, so the
-- second is refused (409 `assist_open`) rather than allowed to race.
CREATE UNIQUE INDEX assist_sessions_org_phone_open_key
    ON assist_sessions (org_id, phone) WHERE status = 'open';

-- The landlord's list: one org's open sessions, newest first.
CREATE INDEX assist_sessions_org_created_idx
    ON assist_sessions (org_id, created_at DESC, id DESC);

-- The hooks look a session up by what the renter's own request carries: the
-- phone they verified with, or the account and unit they applied for.
CREATE INDEX assist_sessions_open_phone_idx
    ON assist_sessions (phone) WHERE status = 'open';
CREATE INDEX assist_sessions_open_renter_idx
    ON assist_sessions (renter_user_id) WHERE status = 'open';

CREATE TRIGGER assist_sessions_set_updated_at BEFORE UPDATE ON assist_sessions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- --------------------------------------------------------- the witness --

-- FLOWS 2b.6: the renter signs on their own device with a code the landlord
-- showed them. The signature is still the renter's — this column records who
-- was standing there when it happened, and nothing more. The landlord cannot
-- sign for the renter through this path; that stays the deliberate, separately
-- audited `landlord_recorded` activation (FLOWS 3.6).
ALTER TABLE contract_signatures
    ADD COLUMN witnessed_by_user_id UUID REFERENCES users (id);

-- ---------------------------------------------------- notification_log --

-- The assisted code is shown, not sent. It still writes a delivery-log row,
-- because "a code was revealed for this number" is exactly the event an
-- operator investigating an account takeover needs to find — but the row
-- carries an empty body (the code is never persisted), costs no credit, and
-- can never be claimed by the SMS worker, whose claim requires `queued`.
ALTER TABLE notification_log DROP CONSTRAINT notification_log_channel_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_channel_check
    CHECK (channel IN ('sms', 'in_person'));

ALTER TABLE notification_log DROP CONSTRAINT notification_log_status_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_status_check
    CHECK (status IN ('queued', 'sending', 'sent', 'failed', 'held_no_credit', 'shown'));

-- `shown` belongs to the in-person channel alone, and an SMS row can never
-- carry it: the two constraints above would each pass a `shown` SMS row, so
-- the pairing is stated once, here.
ALTER TABLE notification_log ADD CONSTRAINT notification_log_channel_status_check
    CHECK (channel = 'sms' OR status = 'shown');
