package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
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

// stuckSendingAfter is how long a row may stay claimed (`sending`) before the
// sweep decides the worker holding it died and returns it to the queue.
const stuckSendingAfter = 5 * time.Minute

// recoverLimit caps one recovery sweep, so a large backlog is drained in
// batches rather than loaded into memory at once.
const recoverLimit = 500

// DefaultWorkers is the size of the sending pool (API.md Phase 6: N=3). It is
// overridable with NOTIFY_WORKERS.
const DefaultWorkers = 3

// DefaultBackoff is the retry schedule for one message: three attempts, the
// second and third preceded by a pause, so a provider blip does not turn into
// a failed send (SPEC §6: "retry/backoff, 3 attempts").
//
// The three rungs are the schedule; with MaxAttempts at its default of 3 the
// sends land at 0s, 1s and 6s, and the 25s rung is what a fourth attempt would
// wait — it stays in the table so the schedule is written down in one place.
//
//nolint:gochecknoglobals // fixed retry schedule, read-only.
var DefaultBackoff = []time.Duration{time.Second, 5 * time.Second, 25 * time.Second}

// DefaultMaxAttempts is how many times one message is sent before it is
// recorded `failed` (API.md Phase 6: a 3-attempt backoff).
const DefaultMaxAttempts = 3

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
	// BatchID groups the rows of one landlord broadcast (API.md:
	// `POST /notifications/custom`). Empty for every other kind.
	BatchID string
	// Language is the language Body was rendered in (Phase 13). It is stored
	// on the row so the delivery log can answer "which language did this
	// renter get?" without reading the prose. Empty is stored as Swahili, the
	// platform default.
	Language string
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
	lang := LanguageFor(m.Language, "")
	payload, err := json.Marshal(map[string]any{
		"kind": m.Kind, "to": m.Phone, "body": m.Body, "language": lang,
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
	var batchID pgtype.UUID
	if m.BatchID != "" {
		if batchID, err = db.ParseUUID(m.BatchID); err != nil {
			return "", fmt.Errorf("notify: batch id %q: %w", m.BatchID, err)
		}
	}

	row, err := q.InsertBatchNotification(ctx, sqlc.InsertBatchNotificationParams{
		OrgID:     orgID,
		UserID:    userID,
		Kind:      m.Kind,
		DedupeKey: m.DedupeKey,
		Payload:   payload,
		ToPhone:   m.Phone,
		Body:      m.Body,
		BatchID:   batchID,
		Language:  lang,
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

	// Workers is the pool size; 0 means DefaultWorkers.
	Workers int
	// Backoff is the pause before each retry; nil means DefaultBackoff.
	Backoff []time.Duration
	// MaxAttempts is how many sends one message gets; 0 means
	// DefaultMaxAttempts.
	MaxAttempts int
	// Sleep waits out a backoff pause. Tests inject a no-op so a 3-attempt
	// failure does not take six seconds of wall clock.
	Sleep func(ctx context.Context, d time.Duration)
}

// RunWorker starts the notification worker pool and blocks until ctx is
// cancelled.
//
// N goroutines each BRPOP the same list and claim their row atomically, so a
// message is sent exactly once even when the same id is pushed twice. Before
// the pool starts, the recovery sweep re-enqueues everything Postgres still
// believes is unsent — the rows a lost Redis, or a killed worker, left behind
// (SPEC §2.2).
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
	workers := w.Workers
	if workers <= 0 {
		workers = DefaultWorkers
	}

	// Anything left `queued` from a previous run (or from a Redis restart),
	// and anything a dead worker left claimed, goes back on the list before
	// the pool starts.
	RecoverQueued(ctx, w.Q, w.Redis, logger)

	logger.Info("notification worker pool started", "queue", QueueKey, "workers", workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			w.loop(ctx, logger, wait)
		}(i)
	}
	wg.Wait()
}

// loop is one worker: pop an id, deliver it, repeat until ctx is cancelled.
func (w Worker) loop(ctx context.Context, logger *slog.Logger, wait time.Duration) {
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

// deliver claims one notification, sends it with backoff, and records the
// outcome.
//
// The claim is the whole of the concurrency story: `UPDATE … SET
// status='sending' WHERE id=$1 AND status='queued' RETURNING` returns a row to
// exactly one caller, so three workers holding the same id produce one SMS.
func (w Worker) deliver(ctx context.Context, logger *slog.Logger, rawID string) {
	id, err := db.ParseUUID(rawID)
	if err != nil {
		logger.Warn("notification worker: malformed id on queue", "id", rawID)
		return
	}

	row, err := w.Q.ClaimNotification(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		// Already claimed, already sent, or deleted between push and pop.
		return
	}
	if err != nil {
		logger.Error("notification worker: claim failed", "id", rawID, "error", err)
		return
	}

	msgID, attempts, sendErr := w.send(ctx, row)
	if sendErr != nil {
		// A cancelled context is a shutdown, not a provider failure: put the
		// message back rather than burning it.
		if ctx.Err() != nil {
			if relErr := w.Q.ReleaseNotification(context.WithoutCancel(ctx), id); relErr != nil {
				logger.Error("notification worker: release failed", "id", rawID, "error", relErr)
			}
			return
		}
		reason := sendErr.Error()
		if err := w.Q.MarkNotificationFailed(ctx, sqlc.MarkNotificationFailedParams{
			ID: id, Attempts: attempts, Error: &reason,
		}); err != nil {
			logger.Error("notification worker: mark failed", "id", rawID, "error", err)
		}
		logger.Warn("notification send failed",
			"id", rawID, "kind", row.Kind, "attempts", attempts, "error", sendErr)
		return
	}
	// The SMS is already gone; recording that fact is not cancellable. A row
	// left `sending` because the pool was shutting down would be re-queued by
	// the startup sweep and sent to the renter a second time.
	if err := w.Q.MarkNotificationSent(context.WithoutCancel(ctx), sqlc.MarkNotificationSentParams{
		ID: id, Attempts: attempts, ProviderMsgID: db.Str(msgID),
	}); err != nil {
		logger.Error("notification worker: mark sent", "id", rawID, "error", err)
		return
	}
	logger.Info("notification sent",
		"id", rawID, "kind", row.Kind, "attempts", attempts, "provider_msg_id", msgID)
}

// send tries one message up to MaxAttempts times, pausing between attempts,
// and reports the provider message id, how many attempts it took, and the last
// error.
func (w Worker) send(ctx context.Context, row sqlc.ClaimNotificationRow) (string, int32, error) {
	backoff := w.Backoff
	if backoff == nil {
		backoff = DefaultBackoff
	}
	maxAttempts := w.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	sleep := w.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			sleep(ctx, backoff[(attempt-2)%len(backoff)])
			if ctx.Err() != nil {
				return "", int32(attempt - 1), ctx.Err()
			}
		}
		msgID, err := w.SMS.Send(ctx, row.ToPhone, row.Body, row.SenderName)
		if err == nil {
			return msgID, int32(attempt), nil
		}
		lastErr = err
	}
	return "", int32(maxAttempts), lastErr
}

// sleepCtx waits for d unless the context is cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// RecoverQueued re-pushes the messages Postgres still believes are unsent:
// rows still `queued` well after they were written (their Redis entry went
// with the cache) and rows left `sending` by a worker that died holding them.
func RecoverQueued(ctx context.Context, q *sqlc.Queries, rdb *redis.Client, logger *slog.Logger) {
	if q == nil || rdb == nil {
		return
	}

	// Rows a dead worker left claimed come back to `queued` first, so the
	// sweep below can pick them up in the same pass.
	stuck, err := q.ListStaleSendingNotifications(ctx, sqlc.ListStaleSendingNotificationsParams{
		OlderThan: interval(stuckSendingAfter),
		RowLimit:  recoverLimit,
	})
	if err != nil && logger != nil {
		logger.Warn("notification stuck-sending sweep failed", "error", err)
	}
	for _, id := range stuck {
		if err := q.RequeueSendingNotification(ctx, id); err != nil && logger != nil {
			logger.Warn("notification requeue failed", "id", db.UUIDString(id), "error", err)
		}
	}
	if len(stuck) > 0 && logger != nil {
		logger.Info("returned stranded notifications to the queue", "count", len(stuck))
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
