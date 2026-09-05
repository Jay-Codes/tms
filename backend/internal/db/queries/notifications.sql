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

-- guard-exempt: the worker records the provider's answer against one row it already popped.
-- name: MarkNotificationSent :exec
UPDATE notification_log
SET status = 'sent', provider_msg_id = sqlc.narg(provider_msg_id),
    sent_at = now(), attempts = attempts + 1, error = NULL
WHERE id = sqlc.arg(id);

-- guard-exempt: the worker records the provider's answer against one row it already popped.
-- name: MarkNotificationFailed :exec
UPDATE notification_log
SET status = 'failed', attempts = attempts + 1, error = sqlc.narg(error)
WHERE id = sqlc.arg(id);

-- ListStaleQueuedNotifications is the Redis-loss safety net: rows still queued
-- well after they were written are re-pushed onto the list at startup.
-- guard-exempt: startup recovery sweeps every org's unsent messages.
-- name: ListStaleQueuedNotifications :many
SELECT id FROM notification_log
WHERE status = 'queued' AND created_at < now() - sqlc.arg(older_than)::interval
ORDER BY created_at
LIMIT sqlc.arg(row_limit);

-- name: ListNotificationsForOrg :many
SELECT id, org_id, user_id, kind, dedupe_key, to_phone, body, status,
       provider_msg_id, error, attempts, sent_at, created_at
FROM notification_log
WHERE org_id = sqlc.arg(org_id)
ORDER BY created_at DESC
LIMIT sqlc.arg(row_limit);
