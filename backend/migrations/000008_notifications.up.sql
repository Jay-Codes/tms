-- Phase 6: the notification scheduler, the retrying worker pool and landlord
-- bulk SMS.
--
-- What lands here on top of the existing notification_log:
--   * kind `unsigned_reminder` — the nudge for a contract that has been
--     waiting for the renter's signature (API.md Phase 6). The scheduler's
--     other kinds (reminder_7d, reminder_due, overdue_daily) and `custom` were
--     already in the constraint from 000002.
--   * status `sending` — the claim the worker pool takes before it calls Beem.
--     Three workers race for the same row; the atomic
--     `UPDATE … SET status='sending' WHERE status='queued' RETURNING` is what
--     makes exactly one of them win, and it needs a status to move to.
--   * batch_id — one landlord bulk send is one batch. The dedupe key already
--     carries it (`custom:{batch_id}:{user_id}`), but a column is what lets the
--     notification log group and count a batch without parsing strings.
--   * an (org_id, created_at) index for GET /notifications/log, which pages a
--     single org's rows newest-first.
--   * a partial index on `sending` rows so the startup sweep can find the ones
--     a crashed worker left claimed.

-- --------------------------------------------------------------- kinds --

ALTER TABLE notification_log DROP CONSTRAINT notification_log_kind_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_kind_check
    CHECK (kind IN ('reminder_7d', 'reminder_due', 'overdue_daily', 'thank_you',
                    'otp', 'custom', 'link_approved', 'link_rejected',
                    'contract_ready', 'welcome', 'contract_terminated',
                    'unsigned_reminder'));

-- -------------------------------------------------------------- status --

-- `sending` is the in-flight state between the claim and the provider's answer.
-- A row can only leave it as `sent` or `failed`; the startup sweep re-queues
-- anything still claimed after five minutes (a worker that died mid-send).
ALTER TABLE notification_log DROP CONSTRAINT notification_log_status_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_status_check
    CHECK (status IN ('queued', 'sending', 'sent', 'failed'));

-- ------------------------------------------------------------ batch_id --

ALTER TABLE notification_log ADD COLUMN batch_id UUID;
CREATE INDEX notification_log_batch_idx ON notification_log (org_id, batch_id)
    WHERE batch_id IS NOT NULL;

-- ------------------------------------------------------------- indexes --

-- GET /notifications/log pages one org's rows by (created_at, id) descending.
CREATE INDEX notification_log_org_created_idx
    ON notification_log (org_id, created_at DESC, id DESC);

-- The startup sweep looks for rows a worker claimed and never finished.
CREATE INDEX notification_log_sending_idx
    ON notification_log (updated_at) WHERE status = 'sending';
