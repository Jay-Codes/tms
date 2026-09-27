-- Phase 27: landlords buy SMS credits with mobile money (Snippe).
--
-- The org-facing reads and the order insert are org-scoped like every other
-- `/org/…` query. The webhook and the reconciliation job hold no session: they
-- reach an order by Snippe's reference, by its own order code or by id, and
-- those statements carry a `guard-exempt` line saying so.

-- name: ListActiveSMSCreditPackages :many
SELECT * FROM sms_credit_packages
WHERE active
ORDER BY sort_order, price, id;

-- name: GetActiveSMSCreditPackage :one
SELECT * FROM sms_credit_packages
WHERE id = sqlc.arg(id) AND active;

-- InsertSMSCreditOrder writes the order before Snippe is called, so the order
-- code (the Idempotency-Key) exists whatever happens to the request.
-- name: InsertSMSCreditOrder :one
INSERT INTO sms_credit_orders (org_id, package_id, package_name, credits, amount,
                               payer_phone, order_code, created_by_user_id)
VALUES (sqlc.arg(org_id), sqlc.arg(package_id), sqlc.arg(package_name), sqlc.arg(credits)::int,
        sqlc.arg(amount)::bigint, sqlc.arg(payer_phone), sqlc.arg(order_code),
        sqlc.narg(created_by_user_id))
RETURNING *;

-- name: SetSMSCreditOrderReference :one
UPDATE sms_credit_orders
SET snippe_reference = sqlc.arg(snippe_reference), last_checked_at = now()
WHERE id = sqlc.arg(id) AND org_id = sqlc.arg(org_id)
RETURNING *;

-- FailNewSMSCreditOrder closes an order Snippe refused outright at creation.
-- name: FailNewSMSCreditOrder :one
UPDATE sms_credit_orders
SET status = 'failed', failure_reason = sqlc.arg(failure_reason)
WHERE id = sqlc.arg(id) AND org_id = sqlc.arg(org_id) AND status = 'pending'
RETURNING *;

-- name: ListOrgSMSCreditOrders :many
SELECT * FROM sms_credit_orders
WHERE org_id = sqlc.arg(org_id)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetOrgSMSCreditOrder :one
SELECT * FROM sms_credit_orders
WHERE id = sqlc.arg(id) AND org_id = sqlc.arg(org_id);

-- CountRecentOrgSMSCreditOrders backs the per-org order limit.
-- name: CountRecentOrgSMSCreditOrders :one
SELECT count(*)::bigint FROM sms_credit_orders
WHERE org_id = sqlc.arg(org_id) AND created_at >= sqlc.arg(since)::timestamptz;

-- ------------------------------------------------ webhook / reconciliation --

-- guard-exempt: the Snippe webhook has no session; Snippe's reference is the key, and the order row names its org.
-- name: LockSMSCreditOrderByReference :one
SELECT * FROM sms_credit_orders
WHERE snippe_reference = sqlc.arg(snippe_reference)
FOR UPDATE;

-- guard-exempt: the Snippe webhook has no session; the order code we sent in the metadata is the key.
-- name: LockSMSCreditOrderByCode :one
SELECT * FROM sms_credit_orders
WHERE order_code = sqlc.arg(order_code)
FOR UPDATE;

-- guard-exempt: the reconciliation job runs across orgs with no session, one order at a time by id.
-- name: LockSMSCreditOrder :one
SELECT * FROM sms_credit_orders
WHERE id = sqlc.arg(id)
FOR UPDATE;

-- CompleteSMSCreditOrder is the one transition that credits. It matches only
-- an order not yet completed (a late completion after the local expiry still
-- credits — the money was taken), so a replay finds no row and credits
-- nothing.
-- guard-exempt: runs under the webhook/reconciler with the order row already locked by id.
-- name: CompleteSMSCreditOrder :one
UPDATE sms_credit_orders
SET status = 'completed', credited_at = now(), failure_reason = NULL,
    snippe_reference = COALESCE(snippe_reference, sqlc.narg(snippe_reference))
WHERE id = sqlc.arg(id) AND status IN ('pending', 'expired', 'failed')
RETURNING *;

-- CloseSMSCreditOrder moves a pending order to failed, expired or mismatch.
-- guard-exempt: runs under the webhook/reconciler with the order row already locked by id.
-- name: CloseSMSCreditOrder :one
UPDATE sms_credit_orders
SET status = sqlc.arg(status), failure_reason = sqlc.narg(failure_reason),
    snippe_reference = COALESCE(snippe_reference, sqlc.narg(snippe_reference)),
    last_checked_at = now()
WHERE id = sqlc.arg(id) AND status = 'pending'
RETURNING *;

-- guard-exempt: the reconciliation job records that it asked Snippe about this order.
-- name: TouchSMSCreditOrder :exec
UPDATE sms_credit_orders SET last_checked_at = now()
WHERE id = sqlc.arg(id);

-- ListSMSCreditOrdersToReconcile is the poller's batch: pending orders old
-- enough that the webhook should have arrived, not asked about recently.
-- guard-exempt: the reconciliation job sweeps every org's pending orders by design.
-- name: ListSMSCreditOrdersToReconcile :many
SELECT * FROM sms_credit_orders
WHERE status = 'pending'
  AND created_at < sqlc.arg(created_before)::timestamptz
  AND (last_checked_at IS NULL OR last_checked_at < sqlc.arg(checked_before)::timestamptz)
ORDER BY created_at, id
LIMIT sqlc.arg(row_limit);

-- InsertSnippeWebhookEvent is the dedupe: a second delivery of the same event
-- id inserts nothing and comes back with no row.
-- name: InsertSnippeWebhookEvent :one
INSERT INTO snippe_webhook_events (event_id, event_type, reference, outcome, payload)
VALUES (sqlc.arg(event_id), sqlc.arg(event_type), sqlc.narg(reference), 'received', sqlc.arg(payload))
ON CONFLICT (event_id) DO NOTHING
RETURNING event_id;

-- name: SetSnippeWebhookEventOutcome :exec
UPDATE snippe_webhook_events
SET outcome = sqlc.arg(outcome), order_id = sqlc.narg(order_id)
WHERE event_id = sqlc.arg(event_id);
