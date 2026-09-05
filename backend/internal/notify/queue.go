package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
)

// QueueKey is the Redis list the worker pops notification ids off.
//
// Postgres holds the message; Redis holds only the fact that it is waiting.
// That split is what makes losing Redis survivable: the rows are still there,
// still `queued`, and RecoverQueued re-pushes them (SPEC §2.2, TECHSTACK —
// Redis is ephemeral).
const QueueKey = "sms:queue"

// staleAfter is how long a `queued` row may sit before the startup sweep
// assumes its Redis entry was lost and re-enqueues it.
const staleAfter = time.Minute

// recoverLimit caps one recovery sweep, so a large backlog is drained in
// batches rather than loaded into memory at once.
const recoverLimit = 500

// Msg is one SMS to queue. Body is the already-rendered message text: the
// template (and the org's language) is resolved by the caller, because only
// the caller knows the entity the message is about.
type Msg struct {
	OrgID     string
	UserID    string
	Kind      string
	DedupeKey string
	Phone     string
	Body      string
}

// ErrDuplicate reports that a message with the same dedupe key already exists,
// so nothing was written. It is not a failure: it is the deduplication working.
var ErrDuplicate = errors.New("notify: duplicate dedupe key")

// Queue writes one notification_log row through the supplied queries handle.
//
// Pass a transaction-bound handle so the message shares the fate of the change
// it announces: a rolled-back approval must not send an SMS saying it happened.
// The Redis push therefore belongs AFTER the commit — see Enqueue.
//
// A repeated dedupe_key returns ErrDuplicate and writes nothing (SPEC §6).
func Queue(ctx context.Context, q *sqlc.Queries, m Msg) (string, error) {
	if q == nil {
		return "", fmt.Errorf("notify: nil queries handle")
	}
	payload, err := json.Marshal(map[string]any{
		"kind": m.Kind, "to": m.Phone, "body": m.Body,
	})
	if err != nil {
		return "", fmt.Errorf("notify: marshal payload: %w", err)
	}

	orgID, err := db.ParseUUID(m.OrgID)
	if err != nil {
		return "", fmt.Errorf("notify: org id %q: %w", m.OrgID, err)
	}
	var userID pgtype.UUID
	if m.UserID != "" {
		if userID, err = db.ParseUUID(m.UserID); err != nil {
			return "", fmt.Errorf("notify: user id %q: %w", m.UserID, err)
		}
	}

	row, err := q.InsertNotification(ctx, sqlc.InsertNotificationParams{
		OrgID:     orgID,
		UserID:    userID,
		Kind:      m.Kind,
		DedupeKey: m.DedupeKey,
		Payload:   payload,
		ToPhone:   m.Phone,
		Body:      m.Body,
	})
	// ON CONFLICT DO NOTHING returns no row when the key was already taken.
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrDuplicate
	}
	if err != nil {
		return "", fmt.Errorf("notify: insert %s: %w", m.Kind, err)
	}
	return db.UUIDString(row.ID), nil
}

// Enqueue pushes notification ids onto the Redis work list. Call it after the
// transaction commits.
//
// A push failure is logged, not returned: the row is already durable and the
// startup sweep will pick it up, so a flaky Redis must not fail the request
// that queued the message.
func Enqueue(ctx context.Context, rdb *redis.Client, logger *slog.Logger, ids ...string) {
	if rdb == nil || len(ids) == 0 {
		return
	}
	values := make([]any, 0, len(ids))
	for _, id := range ids {
		if id != "" {
			values = append(values, id)
		}
	}
	if len(values) == 0 {
		return
	}
	if err := rdb.RPush(ctx, QueueKey, values...).Err(); err != nil && logger != nil {
		logger.Warn("notification queued in postgres but not pushed to redis; the startup sweep will retry",
			"count", len(values), "error", err)
	}
}

// Worker drains the Redis list and records each send's outcome in Postgres.
type Worker struct {
	Q        *sqlc.Queries
	Redis    *redis.Client
	SMS      SMSProvider
	Logger   *slog.Logger
	PollWait time.Duration // BRPOP timeout; 0 means 5 seconds
}

// RunWorker starts the notification worker and blocks until ctx is cancelled.
//
// It is deliberately minimal for Phase 3: one goroutine, one attempt per
// message, no backoff. Phase 6 replaces it with the scheduler and a retrying
// worker pool; the notification_log contract (queued → sent | failed) is
// already the one that scheduler will use.
func RunWorker(ctx context.Context, w Worker) {
	logger := w.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if w.Q == nil || w.Redis == nil || w.SMS == nil {
		logger.Warn("notification worker not started: needs postgres, redis and an sms provider")
		return
	}
	wait := w.PollWait
	if wait <= 0 {
		wait = 5 * time.Second
	}

	// Anything left `queued` from a previous run (or from a Redis restart)
	// goes back on the list before the loop starts.
	RecoverQueued(ctx, w.Q, w.Redis, logger)

	logger.Info("notification worker started", "queue", QueueKey)
	for {
		if ctx.Err() != nil {
			return
		}
		res, err := w.Redis.BRPop(ctx, wait, QueueKey).Result()
		if errors.Is(err, redis.Nil) {
			continue // idle window elapsed
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Warn("notification worker: pop failed", "error", err)
			// Back off a little so an unreachable Redis does not spin.
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			continue
		}
		if len(res) < 2 {
			continue
		}
		w.deliver(ctx, logger, res[1])
	}
}

// deliver sends one notification and records the outcome.
func (w Worker) deliver(ctx context.Context, logger *slog.Logger, rawID string) {
	id, err := db.ParseUUID(rawID)
	if err != nil {
		logger.Warn("notification worker: malformed id on queue", "id", rawID)
		return
	}
	row, err := w.Q.GetNotificationForSend(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return // deleted between queue and pop
	}
	if err != nil {
		logger.Error("notification worker: load failed", "id", rawID, "error", err)
		return
	}
	if row.Status != "queued" {
		return // already handled (a duplicate push, or the recovery sweep racing)
	}

	msgID, sendErr := w.SMS.Send(ctx, row.ToPhone, row.Body)
	if sendErr != nil {
		reason := sendErr.Error()
		if err := w.Q.MarkNotificationFailed(ctx, sqlc.MarkNotificationFailedParams{
			ID: id, Error: &reason,
		}); err != nil {
			logger.Error("notification worker: mark failed", "id", rawID, "error", err)
		}
		logger.Warn("notification send failed", "id", rawID, "kind", row.Kind, "error", sendErr)
		return
	}
	if err := w.Q.MarkNotificationSent(ctx, sqlc.MarkNotificationSentParams{
		ID: id, ProviderMsgID: db.Str(msgID),
	}); err != nil {
		logger.Error("notification worker: mark sent", "id", rawID, "error", err)
		return
	}
	logger.Info("notification sent", "id", rawID, "kind", row.Kind, "provider_msg_id", msgID)
}

// RecoverQueued re-pushes rows that are still `queued` well after they were
// written — the messages whose Redis entry was lost with the cache.
func RecoverQueued(ctx context.Context, q *sqlc.Queries, rdb *redis.Client, logger *slog.Logger) {
	if q == nil || rdb == nil {
		return
	}
	ids, err := q.ListStaleQueuedNotifications(ctx, sqlc.ListStaleQueuedNotificationsParams{
		OlderThan: interval(staleAfter),
		RowLimit:  recoverLimit,
	})
	if err != nil {
		if logger != nil {
			logger.Warn("notification recovery sweep failed", "error", err)
		}
		return
	}
	if len(ids) == 0 {
		return
	}
	strs := make([]string, 0, len(ids))
	for _, id := range ids {
		strs = append(strs, db.UUIDString(id))
	}
	Enqueue(ctx, rdb, logger, strs...)
	if logger != nil {
		logger.Info("re-enqueued notifications left queued", "count", len(strs))
	}
}

// interval converts a duration to the pgtype.Interval the query expects.
func interval(d time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: d.Microseconds(), Valid: true}
}
