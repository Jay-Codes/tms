-- notification_log is the durable side of SMS delivery: Postgres is the truth,
-- Redis only carries the work item. A row is written inside the transaction
-- that caused it, so a rolled-back approval sends nothing.
--
-- The worker's queries are keyed by the notification's own id — it pops ids
-- off a Redis list and has no org context — so they are guard-exempt.

-- name: InsertNotification :one
INSERT INTO notification_log (org_id, user_id, kind, channel, dedupe_key, payload, to_phone, body)
VALUES (
    sqlc.arg(org_id), sqlc.narg(user_id), sqlc.arg(kind), 'sms',
    sqlc.arg(dedupe_key), sqlc.arg(payload), sqlc.arg(to_phone), sqlc.arg(body)
)
ON CONFLICT (dedupe_key) DO NOTHING
RETURNING *;

-- guard-exempt: the worker pops a notification id off Redis and has no org context.
-- name: GetNotificationForSend :one
SELECT id, org_id, user_id, kind, dedupe_key, to_phone, body, status, attempts
FROM notification_log
WHERE id = sqlc.arg(id);

-- guard-exempt: the worker records the provider's answer against one row it already claimed.
-- name: MarkNotificationSent :exec
UPDATE notification_log
SET status = 'sent', provider_msg_id = sqlc.narg(provider_msg_id),
    sent_at = now(), attempts = sqlc.arg(attempts), error = NULL
WHERE id = sqlc.arg(id);

-- guard-exempt: the worker records the provider's answer against one row it already claimed.
-- name: MarkNotificationFailed :exec
UPDATE notification_log
SET status = 'failed', attempts = sqlc.arg(attempts), error = sqlc.narg(error)
WHERE id = sqlc.arg(id);

-- ListStaleQueuedNotifications is the Redis-loss safety net: rows still queued
-- well after they were written are re-pushed onto the list at startup.
-- guard-exempt: startup recovery sweeps every org's unsent messages.
-- name: ListStaleQueuedNotifications :many
SELECT id FROM notification_log
WHERE status = 'queued' AND created_at < now() - sqlc.arg(older_than)::interval
ORDER BY created_at
LIMIT sqlc.arg(row_limit);


-- ------------------------------------------------- Phase 6: worker pool --

-- ClaimNotification is the atomic claim three workers race for: the row moves
-- from `queued` to `sending` in one statement, and only the worker whose UPDATE
-- returned a row goes on to call the provider. Without it two workers popping
-- the same id (a re-push, the recovery sweep) would both send.
--
-- It resolves the org's sender ID at the same time, so the send path stays one
-- round trip: the org's own approved name if it has set one, else the platform
-- default the provider supplies.
-- guard-exempt: the worker claims one notification by id and has no org context.
-- name: ClaimNotification :one
UPDATE notification_log n
SET status = 'sending'
FROM orgs o
WHERE n.id = sqlc.arg(id) AND n.status = 'queued' AND o.id = n.org_id
RETURNING n.id, n.org_id, n.user_id, n.kind, n.dedupe_key, n.to_phone, n.body,
          n.attempts, COALESCE(o.settings #>> '{notifications,sender_name}', '')::text AS sender_name;

-- ReleaseNotification puts a claimed row back on the queue — the path taken
-- when the worker is shutting down mid-flight, so the message is retried
-- rather than stranded in `sending`.
-- guard-exempt: the worker releases one notification it already claimed.
-- name: ReleaseNotification :exec
UPDATE notification_log SET status = 'queued'
WHERE id = sqlc.arg(id) AND status = 'sending';

-- ListStaleSendingNotifications finds rows a worker claimed and never
-- finished — a crash between the claim and the provider's answer. The startup
-- sweep flips them back to `queued` and re-pushes them.
-- guard-exempt: startup recovery sweeps every org's stranded messages.
-- name: ListStaleSendingNotifications :many
SELECT id FROM notification_log
WHERE status = 'sending' AND updated_at < now() - sqlc.arg(older_than)::interval
ORDER BY updated_at
LIMIT sqlc.arg(row_limit);

-- guard-exempt: startup recovery returns one stranded message to the queue.
-- name: RequeueSendingNotification :exec
UPDATE notification_log SET status = 'queued'
WHERE id = sqlc.arg(id) AND status = 'sending';

-- ------------------------------------------- Phase 6: scheduler targets --

-- ListActiveOrgs feeds the notification scheduler, which walks every live
-- tenant once per tick and applies that org's own settings.
-- name: ListActiveOrgs :many
SELECT o.id, o.name, o.settings,
       COALESCE(b.display_name, o.name)::text AS display_name
FROM orgs o
LEFT JOIN org_branding b ON b.org_id = o.id
WHERE o.status = 'active' AND o.deleted_at IS NULL
ORDER BY o.created_at, o.id;

-- ListScheduleReminderTargets resolves, for one org, the payment schedules a
-- reminder is owed on: everything a message needs in one row, so the scheduler
-- renders without a second query per renter.
--
-- `due_on` selects a single date (reminder_7d, reminder_due); `overdue_only`
-- takes every unresolved row past its due date (overdue_daily). Contracts that
-- are not running are excluded — a terminated tenancy is not chased for rent.
-- name: ListScheduleReminderTargets :many
SELECT s.id, s.contract_id, s.due_date, s.amount, s.paid_amount, s.status,
       c.renter_user_id,
       u.name AS unit_name, p.name AS property_name,
       ru.full_name AS renter_name, ru.phone AS renter_phone,
       nd.due_date AS next_due_date
FROM payment_schedules s
JOIN contracts c  ON c.id = s.contract_id AND c.org_id = s.org_id AND c.deleted_at IS NULL
JOIN units u      ON u.id = c.unit_id AND u.org_id = c.org_id
JOIN properties p ON p.id = u.property_id AND p.org_id = c.org_id
JOIN users ru     ON ru.id = c.renter_user_id
LEFT JOIN LATERAL (
    SELECT s2.due_date FROM payment_schedules s2
    WHERE s2.contract_id = c.id AND s2.org_id = c.org_id AND s2.deleted_at IS NULL
      AND s2.status IN ('pending', 'partial', 'overdue') AND s2.due_date > s.due_date
    ORDER BY s2.due_date, s2.period_start LIMIT 1
) nd ON true
WHERE s.org_id = sqlc.arg(org_id) AND s.deleted_at IS NULL
  AND c.status IN ('active', 'expiring')
  AND s.status = ANY(sqlc.arg(statuses)::text[])
  AND (sqlc.narg(due_on)::date IS NULL OR s.due_date = sqlc.narg(due_on)::date)
  AND (sqlc.narg(due_before)::date IS NULL OR s.due_date <= sqlc.narg(due_before)::date)
ORDER BY s.due_date, s.id;

-- ListUnsignedContractsForOrg finds contracts still waiting on the renter's
-- signature after the org's `after_days` cushion — the nudge of API.md's
-- `unsigned_reminder`. A contract the renter has already signed is excluded by
-- the absence of a `renter` signature row, not by its status, so a contract
-- awaiting only the landlord is never chased.
-- name: ListUnsignedContractsForOrg :many
SELECT c.id, c.created_at, c.renter_user_id, c.rent_amount,
       u.name AS unit_name, p.name AS property_name,
       ru.full_name AS renter_name, ru.phone AS renter_phone
FROM contracts c
JOIN units u      ON u.id = c.unit_id AND u.org_id = c.org_id
JOIN properties p ON p.id = u.property_id AND p.org_id = c.org_id
JOIN users ru     ON ru.id = c.renter_user_id
WHERE c.org_id = sqlc.arg(org_id) AND c.deleted_at IS NULL
  AND c.status = 'pending_signature'
  AND c.created_at <= sqlc.arg(created_before)
  AND NOT EXISTS (
      SELECT 1 FROM contract_signatures sig
      WHERE sig.contract_id = c.id AND sig.org_id = c.org_id AND sig.party = 'renter'
  )
ORDER BY c.created_at, c.id;

-- ------------------------------------------- Phase 6: custom bulk sends --

-- ListActiveRenterRecipients is `recipients: "all_active"`: every renter the
-- org has a running (or expiring) contract with, once each even when they rent
-- several units, with the unit and property of their most recent contract for
-- the `{{unit}}` / `{{property}}` variables.
-- name: ListActiveRenterRecipients :many
SELECT DISTINCT ON (c.renter_user_id)
       c.renter_user_id, ru.full_name AS renter_name, ru.phone AS renter_phone,
       u.name AS unit_name, p.name AS property_name
FROM contracts c
JOIN units u      ON u.id = c.unit_id AND u.org_id = c.org_id
JOIN properties p ON p.id = u.property_id AND p.org_id = c.org_id
JOIN users ru     ON ru.id = c.renter_user_id
WHERE c.org_id = sqlc.arg(org_id) AND c.deleted_at IS NULL
  AND c.status IN ('active', 'expiring')
ORDER BY c.renter_user_id, c.created_at DESC;

-- ListSelectedRenterRecipients is `recipients: "selected"`: the named renters,
-- but only those this org actually knows (a contract or a link request). An id
-- from another org's directory simply does not come back, and is counted as
-- skipped — never as a send, and never as a probe that confirms it exists.
-- name: ListSelectedRenterRecipients :many
SELECT DISTINCT ON (ru.id)
       ru.id AS renter_user_id, ru.full_name AS renter_name, ru.phone AS renter_phone,
       COALESCE(u.name, '')::text AS unit_name,
       COALESCE(p.name, '')::text AS property_name
FROM users ru
LEFT JOIN LATERAL (
    SELECT c.unit_id FROM contracts c
    WHERE c.renter_user_id = ru.id AND c.org_id = sqlc.arg(org_id) AND c.deleted_at IS NULL
    ORDER BY c.created_at DESC LIMIT 1
) lc ON true
LEFT JOIN units u      ON u.id = lc.unit_id AND u.org_id = sqlc.arg(org_id)
LEFT JOIN properties p ON p.id = u.property_id AND p.org_id = sqlc.arg(org_id)
WHERE ru.id = ANY(sqlc.arg(user_ids)::uuid[]) AND ru.deleted_at IS NULL
  AND (EXISTS (SELECT 1 FROM contracts c2
               WHERE c2.renter_user_id = ru.id AND c2.org_id = sqlc.arg(org_id) AND c2.deleted_at IS NULL)
    OR EXISTS (SELECT 1 FROM unit_link_requests lr
               WHERE lr.renter_user_id = ru.id AND lr.org_id = sqlc.arg(org_id) AND lr.deleted_at IS NULL))
ORDER BY ru.id;

-- InsertBatchNotification is Queue's bulk twin: it carries the batch a custom
-- send belongs to, so the log can group and count one broadcast.
-- name: InsertBatchNotification :one
INSERT INTO notification_log (org_id, user_id, kind, channel, dedupe_key, payload, to_phone, body, batch_id)
VALUES (
    sqlc.arg(org_id), sqlc.narg(user_id), sqlc.arg(kind), 'sms',
    sqlc.arg(dedupe_key), sqlc.arg(payload), sqlc.arg(to_phone), sqlc.arg(body),
    sqlc.narg(batch_id)
)
ON CONFLICT (dedupe_key) DO NOTHING
RETURNING *;

-- ------------------------------------------ Phase 6: the landlord's log --

-- ListOrgNotifications is GET /notifications/log: one org's sends, newest
-- first, filtered and cursor-paged by (created_at, id). The renter's name is
-- resolved here so the landlord's log reads as people rather than user ids.
-- name: ListOrgNotifications :many
SELECT n.id, n.user_id, n.kind, n.dedupe_key, n.to_phone, n.body, n.status,
       n.provider_msg_id, n.error, n.attempts, n.batch_id, n.sent_at, n.created_at,
       COALESCE(ru.full_name, '')::text AS renter_name
FROM notification_log n
LEFT JOIN users ru ON ru.id = n.user_id
WHERE n.org_id = sqlc.arg(org_id)
  AND (sqlc.narg(kind)::text IS NULL OR n.kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(status)::text IS NULL OR n.status = sqlc.narg(status)::text)
  AND (sqlc.narg(user_id)::uuid IS NULL OR n.user_id = sqlc.narg(user_id)::uuid)
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR n.created_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR n.created_at <= sqlc.narg(to_at)::timestamptz)
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (n.created_at, n.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY n.created_at DESC, n.id DESC
LIMIT sqlc.arg(row_limit);

-- GetOrgNotification resolves one row inside its org, so another org's
-- notification is a 404 rather than something a landlord can read or retry.
-- name: GetOrgNotification :one
SELECT n.id, n.user_id, n.kind, n.dedupe_key, n.to_phone, n.body, n.status,
       n.provider_msg_id, n.error, n.attempts, n.batch_id, n.sent_at, n.created_at,
       COALESCE(ru.full_name, '')::text AS renter_name
FROM notification_log n
LEFT JOIN users ru ON ru.id = n.user_id
WHERE n.org_id = sqlc.arg(org_id) AND n.id = sqlc.arg(id);

-- RetryOrgNotification is POST /notifications/log/{id}/retry: only a `failed`
-- row goes back on the queue, and only inside its own org. The attempt counter
-- is left standing — the log should show that the first three tries happened.
-- name: RetryOrgNotification :one
UPDATE notification_log
SET status = 'queued', error = NULL
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND status = 'failed'
RETURNING id;
