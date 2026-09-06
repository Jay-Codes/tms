-- Phase 14: prepaid SMS credit per org (SPEC §4, §5.12, PLAN2 Phase 14).
--
-- The balance lives in org_sms_credits, one row per org, created lazily on
-- first read or first send. Every movement writes an append-only
-- sms_credit_ledger row carrying the balance it left behind, so the balance
-- can always be reconstructed from its movements.

-- EnsureOrgSMSCredits creates the org's credit row if it has none and returns
-- it either way. A landlord who has never been topped up still has a balance —
-- zero — and a low watermark, rather than a missing row every caller has to
-- special-case.
-- name: EnsureOrgSMSCredits :one
INSERT INTO org_sms_credits (org_id) VALUES (sqlc.arg(org_id))
ON CONFLICT (org_id) DO UPDATE SET org_id = org_sms_credits.org_id
RETURNING *;

-- name: GetOrgSMSCredits :one
SELECT * FROM org_sms_credits WHERE org_id = sqlc.arg(org_id);

-- DebitOrgSMSCredits is the whole of the credit concurrency story: one
-- conditional statement, taken inside the worker's claim transaction. Three
-- workers racing the last two credits produce exactly one debit, because the
-- `balance >= n` predicate is evaluated against the row each of them locks in
-- turn. No row back means the org cannot pay for this message.
-- guard-exempt: keyed by org_id; the worker holds no session and this IS the org filter.
-- name: DebitOrgSMSCredits :one
UPDATE org_sms_credits
SET balance = balance - sqlc.arg(credits)::int
WHERE org_id = sqlc.arg(org_id) AND balance >= sqlc.arg(credits)::int
RETURNING balance;

-- AddOrgSMSCredits applies a signed movement (a top-up or an admin
-- adjustment). The CHECK on the column is the backstop; the caller refuses a
-- negative result before it gets here so the landlord sees a 400 rather than a
-- 500.
-- name: AddOrgSMSCredits :one
UPDATE org_sms_credits
SET balance = balance + sqlc.arg(delta)::int
WHERE org_id = sqlc.arg(org_id)
RETURNING balance;

-- name: SetOrgSMSLowWatermark :one
UPDATE org_sms_credits
SET low_watermark = sqlc.arg(low_watermark)::int
WHERE org_id = sqlc.arg(org_id)
RETURNING *;

-- InsertSMSCreditLedger records one movement. The table is append-only
-- (trigger, migration 000012): a correcting `adjust` row is how a mistake is
-- undone, never an UPDATE.
-- guard-exempt: keyed by org_id; the worker writes the debit row with no session.
-- name: InsertSMSCreditLedger :one
INSERT INTO sms_credit_ledger (org_id, delta, balance_after, reason, notification_id, admin_user_id, note)
VALUES (sqlc.arg(org_id), sqlc.arg(delta)::int, sqlc.arg(balance_after)::int,
        sqlc.arg(reason), sqlc.narg(notification_id), sqlc.narg(admin_user_id),
        sqlc.arg(note))
RETURNING *;

-- ListSMSCreditLedger is the newest hundred movements, which is what the
-- admin's SMS tab shows.
-- name: ListSMSCreditLedger :many
SELECT l.id, l.delta, l.balance_after, l.reason, l.notification_id, l.note, l.created_at,
       COALESCE(u.full_name, '')::text AS admin_name
FROM sms_credit_ledger l
LEFT JOIN users u ON u.id = l.admin_user_id
WHERE l.org_id = sqlc.arg(org_id)
ORDER BY l.created_at DESC, l.id DESC
LIMIT sqlc.arg(row_limit);

-- SMSCreditsUsed30d is the debited total over the last thirty days, reported
-- as a positive number of credits.
-- name: SMSCreditsUsed30d :one
SELECT COALESCE(-sum(delta), 0)::bigint AS used
FROM sms_credit_ledger
WHERE org_id = sqlc.arg(org_id) AND reason = 'debit'
  AND created_at >= now() - INTERVAL '30 days';

-- name: CountHeldNotifications :one
SELECT count(*)::bigint FROM notification_log
WHERE org_id = sqlc.arg(org_id) AND status = 'held_no_credit';

-- ListHeldNotifications is the release order after a top-up: oldest first, so
-- the renter who has been waiting longest is texted first.
-- name: ListHeldNotifications :many
SELECT id, body, kind FROM notification_log
WHERE org_id = sqlc.arg(org_id) AND status = 'held_no_credit'
ORDER BY created_at, id
LIMIT sqlc.arg(row_limit);

-- ReleaseHeldNotification puts one held message back on the queue. It does not
-- debit: the worker debits at send time, which is the only moment the message
-- is actually going out.
-- name: ReleaseHeldNotification :exec
UPDATE notification_log SET status = 'queued'
WHERE id = sqlc.arg(id) AND org_id = sqlc.arg(org_id) AND status = 'held_no_credit';

-- HoldNotificationNoCredit is what the worker writes when the conditional
-- debit found no row: the message is held, not failed, so the retry loop
-- leaves it alone and the next top-up releases it unchanged.
-- guard-exempt: the worker holds one claimed notification by id and has no org context.
-- name: HoldNotificationNoCredit :exec
UPDATE notification_log SET status = 'held_no_credit'
WHERE id = sqlc.arg(id) AND status IN ('queued', 'sending');

-- GetOrgOwnerContact resolves who is told when an org's balance crosses its
-- low watermark: the owner's name and email, plus the org's display name for
-- the subject line.
-- name: GetOrgOwnerContact :one
SELECT o.name AS org_name,
       COALESCE(b.display_name, o.name)::text AS display_name,
       COALESCE(u.full_name, '')::text AS owner_name,
       COALESCE(u.email, '')::text AS owner_email
FROM orgs o
LEFT JOIN org_branding b ON b.org_id = o.id
LEFT JOIN LATERAL (
    SELECT su.full_name, su.email
    FROM org_members m
    JOIN users su ON su.id = m.user_id
    WHERE m.org_id = o.id AND m.role = 'org_owner' AND m.deleted_at IS NULL
    ORDER BY m.created_at, m.id
    LIMIT 1
) u ON true
WHERE o.id = sqlc.arg(org_id) AND o.deleted_at IS NULL;
