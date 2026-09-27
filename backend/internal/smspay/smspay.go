// Package smspay settles SMS credit orders paid through Snippe (PLAN2 Phase
// 27): it turns a verified webhook or a status poll into an order state, and
// a completed payment into credits through the same path an admin top-up takes
// (notify.Add + notify.ReleaseHeld), in one transaction.
//
// The HTTP handlers and the reconciliation ticker both go through Service, so
// "what a completed payment does" is written once.
package smspay

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"tms/backend/internal/audit"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/notify"
	"tms/backend/internal/snippe"
)

// Order statuses (migration 000031).
const (
	StatusPending   = "pending"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusExpired   = "expired"
	StatusMismatch  = "mismatch"
)

// Timings of the reconciliation job (PLAN2 Phase 27).
const (
	// ReconcileAfter: an order younger than this is left to the webhook.
	ReconcileAfter = 5 * time.Minute
	// ExpireAfter: an order still pending this long is closed as expired.
	// Snippe expires unfinished payments after one to four hours.
	ExpireAfter = 4 * time.Hour
	// RecheckEvery: one order is asked about at most this often.
	RecheckEvery = 2 * time.Minute
	// ReconcileInterval is the ticker period.
	ReconcileInterval = time.Minute
	// ReconcileBatch caps one sweep, well inside Snippe's 60 requests/min.
	ReconcileBatch = 30
)

// Outcome is what one webhook delivery or status check did to an order.
type Outcome string

const (
	OutcomeCompleted        Outcome = "completed"
	OutcomeAlreadyCompleted Outcome = "already_completed"
	OutcomeFailed           Outcome = "failed"
	OutcomeExpired          Outcome = "expired"
	OutcomeMismatch         Outcome = "amount_mismatch"
	OutcomePending          Outcome = "pending"
	OutcomeIgnored          Outcome = "ignored"
	OutcomeUnknownOrder     Outcome = "unknown_order"
	OutcomeDuplicate        Outcome = "duplicate"
	OutcomeError            Outcome = "error"
)

// Service settles orders. Pool is required; Snippe is needed only for status
// polls; Redis (the SMS queue) may be nil, in which case released messages
// wait for the worker's startup sweep.
type Service struct {
	Pool   *db.Pool
	Snippe *snippe.Client
	Redis  *redis.Client
	Logger *slog.Logger
	// Now is the clock (tests may pin it). Nil means time.Now.
	Now func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) inTx(ctx context.Context, fn func(q *sqlc.Queries) error) error {
	if s == nil || s.Pool == nil {
		return errors.New("smspay: database unavailable")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(sqlc.New(s.Pool).WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ------------------------------------------------------------ order codes --

// codeAlphabet is Crockford-style base32 without I, L, O, U: nothing a
// landlord reading it off a screen to support can confuse.
const codeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewOrderCode returns "SMS-" plus ten random characters: 14 characters, well
// inside Snippe's 30-character Idempotency-Key.
func NewOrderCode() (string, error) {
	var raw [10]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("SMS-")
	for _, c := range raw {
		b.WriteByte(codeAlphabet[int(c)%len(codeAlphabet)])
	}
	return b.String(), nil
}

// ------------------------------------------------------------- crediting --

// apply moves a locked order to what Snippe reports and, for a completed
// payment of the right amount, credits the org. It returns the notification
// ids released from `held_no_credit`, to be queued after the commit.
func apply(
	ctx context.Context, q *sqlc.Queries, order sqlc.SmsCreditOrder, p snippe.Payment, source string,
) (Outcome, []string, error) {
	var ref *string
	if r := strings.TrimSpace(p.Reference); r != "" {
		ref = &r
	}
	switch p.Status {
	case snippe.StatusCompleted:
		if order.Status == StatusCompleted {
			return OutcomeAlreadyCompleted, nil, nil
		}
		if order.Status == StatusMismatch {
			return OutcomeMismatch, nil, nil
		}
		cur := p.CurrencyCode()
		if p.Amount.Value != order.Amount || (cur != "" && !strings.EqualFold(cur, snippe.Currency)) {
			reason := fmt.Sprintf("snippe reported %d %s, order is %d %s",
				p.Amount.Value, cur, order.Amount, snippe.Currency)
			if order.Status == StatusPending {
				if _, err := q.CloseSMSCreditOrder(ctx, sqlc.CloseSMSCreditOrderParams{
					ID: order.ID, Status: StatusMismatch, FailureReason: &reason, SnippeReference: ref,
				}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return "", nil, err
				}
			}
			return OutcomeMismatch, nil, nil
		}
		return credit(ctx, q, order, ref, source)

	case snippe.StatusFailed, snippe.StatusVoided:
		return closeOrder(ctx, q, order, StatusFailed, "payment "+p.Status, ref, OutcomeFailed)
	case snippe.StatusExpired:
		return closeOrder(ctx, q, order, StatusExpired, "payment expired at Snippe", ref, OutcomeExpired)
	case snippe.StatusPending, "":
		return OutcomePending, nil, nil
	default:
		return OutcomeIgnored, nil, nil
	}
}

func closeOrder(
	ctx context.Context, q *sqlc.Queries, order sqlc.SmsCreditOrder, status, reason string,
	ref *string, outcome Outcome,
) (Outcome, []string, error) {
	if order.Status != StatusPending {
		return OutcomeIgnored, nil, nil
	}
	if _, err := q.CloseSMSCreditOrder(ctx, sqlc.CloseSMSCreditOrderParams{
		ID: order.ID, Status: status, FailureReason: &reason, SnippeReference: ref,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OutcomeIgnored, nil, nil
		}
		return "", nil, err
	}
	return outcome, nil, nil
}

// credit is the purchase's half of the top-up path: the order is marked
// completed by a conditional UPDATE (a replay finds no row and credits
// nothing), the credits land through notify.Add with reason `purchase`, and
// every held message is released — the same three steps as an admin top-up,
// in the caller's transaction.
func credit(
	ctx context.Context, q *sqlc.Queries, order sqlc.SmsCreditOrder, ref *string, source string,
) (Outcome, []string, error) {
	done, err := q.CompleteSMSCreditOrder(ctx, sqlc.CompleteSMSCreditOrderParams{
		ID: order.ID, SnippeReference: ref,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return OutcomeAlreadyCompleted, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	before, err := notify.EnsureCredits(ctx, q, order.OrgID)
	if err != nil {
		return "", nil, err
	}
	balance, err := notify.Add(ctx, q, order.OrgID, int(done.Credits), notify.ReasonPurchase,
		"Order "+done.OrderCode, pgtype.UUID{})
	if err != nil {
		return "", nil, err
	}
	released, err := notify.ReleaseHeld(ctx, q, order.OrgID)
	if err != nil {
		return "", nil, err
	}
	if err := audit.Record(ctx, q, audit.Entry{
		OrgID:      db.UUIDString(order.OrgID),
		Action:     audit.ActionSMSCreditPurchase,
		EntityType: audit.EntitySMSCreditOrder,
		EntityID:   db.UUIDString(order.ID),
		Before:     map[string]any{"balance": before.Balance, "status": order.Status},
		After: map[string]any{
			"balance": balance, "credits": done.Credits, "amount": done.Amount,
			"order_code": done.OrderCode, "released": len(released), "source": source,
		},
	}); err != nil {
		return "", nil, err
	}
	return OutcomeCompleted, released, nil
}

func (s *Service) enqueue(ctx context.Context, ids []string) {
	notify.Enqueue(ctx, s.Redis, s.logger(), ids...)
}

// --------------------------------------------------------------- webhook --

// statusForEvent maps a webhook type to the payment status it announces.
func statusForEvent(eventType string) string {
	switch eventType {
	case snippe.EventCompleted:
		return snippe.StatusCompleted
	case snippe.EventFailed:
		return snippe.StatusFailed
	case snippe.EventVoided:
		return snippe.StatusVoided
	case snippe.EventExpired:
		return snippe.StatusExpired
	}
	return ""
}

// HandleWebhook applies one verified delivery. The event id is recorded first
// in the same transaction, so a retried delivery (Snippe retries up to five
// times) comes back OutcomeDuplicate and changes nothing.
func (s *Service) HandleWebhook(ctx context.Context, evt snippe.Event, raw []byte) (Outcome, error) {
	var (
		outcome  Outcome
		released []string
	)
	err := s.inTx(ctx, func(q *sqlc.Queries) error {
		var ref *string
		if r := strings.TrimSpace(evt.Data.Reference); r != "" {
			ref = &r
		}
		if _, err := q.InsertSnippeWebhookEvent(ctx, sqlc.InsertSnippeWebhookEventParams{
			EventID: evt.ID, EventType: evt.Type, Reference: ref, Payload: raw,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				outcome = OutcomeDuplicate
				return nil
			}
			return err
		}

		order, found, err := lockOrderFor(ctx, q, evt.Data)
		if err != nil {
			return err
		}
		if !found {
			outcome = OutcomeUnknownOrder
			return q.SetSnippeWebhookEventOutcome(ctx, sqlc.SetSnippeWebhookEventOutcomeParams{
				EventID: evt.ID, Outcome: string(outcome),
			})
		}

		p := evt.Data
		if st := statusForEvent(evt.Type); st != "" {
			p.Status = st
		} else {
			p.Status = "unknown:" + evt.Type
		}
		outcome, released, err = apply(ctx, q, order, p, "webhook")
		if err != nil {
			return err
		}
		return q.SetSnippeWebhookEventOutcome(ctx, sqlc.SetSnippeWebhookEventOutcomeParams{
			EventID: evt.ID, Outcome: string(outcome), OrderID: order.ID,
		})
	})
	if err != nil {
		return OutcomeError, err
	}
	s.enqueue(ctx, released)
	if outcome == OutcomeMismatch || outcome == OutcomeUnknownOrder {
		s.logger().Warn("snippe webhook needs a look", "event_id", evt.ID, "type", evt.Type,
			"reference", evt.Data.Reference, "outcome", string(outcome))
	}
	return outcome, nil
}

// lockOrderFor finds the order a payment belongs to: by Snippe's reference,
// else by the order code sent in the metadata (an order whose create call
// timed out never learned its reference).
func lockOrderFor(ctx context.Context, q *sqlc.Queries, p snippe.Payment) (sqlc.SmsCreditOrder, bool, error) {
	if ref := strings.TrimSpace(p.Reference); ref != "" {
		o, err := q.LockSMSCreditOrderByReference(ctx, &ref)
		if err == nil {
			return o, true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return sqlc.SmsCreditOrder{}, false, err
		}
	}
	if code := strings.TrimSpace(p.Meta("order_code")); code != "" {
		o, err := q.LockSMSCreditOrderByCode(ctx, code)
		if err == nil {
			return o, true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return sqlc.SmsCreditOrder{}, false, err
		}
	}
	return sqlc.SmsCreditOrder{}, false, nil
}

// ---------------------------------------------------------- reconciliation --

// CheckOrder asks Snippe about one order and applies the answer; an order
// pending past ExpireAfter is closed as expired. It is what the sweep runs per
// order, and what an order read runs on demand.
func (s *Service) CheckOrder(ctx context.Context, order sqlc.SmsCreditOrder) (Outcome, error) {
	if order.Status != StatusPending {
		return OutcomeIgnored, nil
	}
	tooOld := s.now().Sub(order.CreatedAt.Time) >= ExpireAfter

	var (
		payment snippe.Payment
		callErr error
		asked   bool
	)
	if order.SnippeReference != nil && s.Snippe.Configured() {
		payment, callErr = s.Snippe.GetPayment(ctx, *order.SnippeReference)
		asked = callErr == nil
	}

	var (
		outcome  Outcome
		released []string
	)
	err := s.inTx(ctx, func(q *sqlc.Queries) error {
		locked, err := q.LockSMSCreditOrder(ctx, order.ID)
		if err != nil {
			return err
		}
		if asked {
			outcome, released, err = apply(ctx, q, locked, payment, "reconcile")
			if err != nil {
				return err
			}
			if outcome != OutcomePending {
				return nil
			}
		}
		if locked.Status != StatusPending {
			outcome = OutcomeIgnored
			return nil
		}
		if tooOld {
			outcome, _, err = closeOrder(ctx, q, locked, StatusExpired,
				"not approved within 4 hours", nil, OutcomeExpired)
			return err
		}
		if outcome == "" {
			outcome = OutcomePending
		}
		return q.TouchSMSCreditOrder(ctx, locked.ID)
	})
	if err != nil {
		return OutcomeError, err
	}
	s.enqueue(ctx, released)
	if callErr != nil && outcome == OutcomePending {
		return OutcomeError, callErr
	}
	return outcome, nil
}

// ReconcileResult counts one sweep's outcomes.
type ReconcileResult struct {
	Checked   int `json:"checked"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Expired   int `json:"expired"`
	Mismatch  int `json:"mismatch"`
	Pending   int `json:"pending"`
	Errors    int `json:"errors"`
}

// Reconcile polls every pending order older than ReconcileAfter that has not
// been asked about within RecheckEvery. The webhook is the normal path; this
// catches deliveries that never arrived.
func (s *Service) Reconcile(ctx context.Context) (ReconcileResult, error) {
	var res ReconcileResult
	if s == nil || s.Pool == nil {
		return res, errors.New("smspay: database unavailable")
	}
	now := s.now()
	orders, err := sqlc.New(s.Pool).ListSMSCreditOrdersToReconcile(ctx, sqlc.ListSMSCreditOrdersToReconcileParams{
		CreatedBefore: pgtype.Timestamptz{Time: now.Add(-ReconcileAfter), Valid: true},
		CheckedBefore: pgtype.Timestamptz{Time: now.Add(-RecheckEvery), Valid: true},
		RowLimit:      ReconcileBatch,
	})
	if err != nil {
		return res, err
	}
	for _, o := range orders {
		res.Checked++
		outcome, err := s.CheckOrder(ctx, o)
		if err != nil {
			res.Errors++
			s.logger().Warn("sms order reconcile failed", "order_code", o.OrderCode, "error", err)
			continue
		}
		switch outcome {
		case OutcomeCompleted:
			res.Completed++
		case OutcomeFailed:
			res.Failed++
		case OutcomeExpired:
			res.Expired++
		case OutcomeMismatch:
			res.Mismatch++
		case OutcomePending:
			res.Pending++
		}
	}
	return res, nil
}

// RunTicker runs Reconcile once at startup and every ReconcileInterval until
// ctx ends — the same shape as the contract lifecycle job. A failed sweep is
// logged and retried on the next tick.
func (s *Service) RunTicker(ctx context.Context) {
	if s == nil || s.Pool == nil {
		slog.Warn("sms order reconciliation not started: postgres unavailable")
		return
	}
	if !s.Snippe.Configured() {
		s.logger().Info("sms order reconciliation idle: Snippe not configured")
	}
	run := func() {
		res, err := s.Reconcile(ctx)
		if err != nil {
			s.logger().Error("sms order reconciliation failed", "error", err)
			return
		}
		if res.Checked > 0 {
			s.logger().Info("sms order reconciliation", "checked", res.Checked,
				"completed", res.Completed, "failed", res.Failed, "expired", res.Expired,
				"mismatch", res.Mismatch, "errors", res.Errors)
		}
	}
	run()
	ticker := time.NewTicker(ReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
