package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
)

// Ledger reasons (SPEC §4: sms_credit_ledger.reason).
const (
	ReasonTopup  = "topup"
	ReasonAdjust = "adjust"
	ReasonDebit  = "debit"
	ReasonRefund = "refund"
)

// StatusHeldNoCredit is the notification_log status of a message the org
// cannot pay for. It is deliberately not `failed`: nothing went wrong with the
// message, the retry loop must leave it alone, and the next top-up releases it
// unchanged (API.md Phase 14).
const StatusHeldNoCredit = "held_no_credit"

// ErrNoCredit reports that the conditional debit found no row to take from:
// the org's balance is below what this message costs.
var ErrNoCredit = errors.New("notify: insufficient sms credits")

// DefaultLedgerLimit is how many movements GET /admin/orgs/{id}/sms returns.
const DefaultLedgerLimit = 100

// ReleaseBatchLimit caps one top-up's release sweep. A backlog larger than
// this is drained by the next top-up or by the recovery sweep, rather than
// loaded into memory at once.
const ReleaseBatchLimit = 500

// EnsureCredits returns the org's credit row, creating it at zero with the
// default watermark if the org has never had one (PLAN2 Phase 14: "lazily
// created").
func EnsureCredits(ctx context.Context, q *sqlc.Queries, orgID pgtype.UUID) (sqlc.OrgSmsCredit, error) {
	return q.EnsureOrgSMSCredits(ctx, orgID)
}

// Debit takes `credits` from an org's balance and records the movement.
//
// The whole of the concurrency story is the single conditional UPDATE inside
// it: `SET balance = balance - n WHERE org_id = $1 AND balance >= n`. Three
// workers racing the last two credits serialise on the row lock and exactly
// one of them comes back with a row, so a balance can never go negative and no
// message is sent that was not paid for. Callers run it inside the same
// transaction as the claim, so a debit and the send it paid for commit or roll
// back together.
//
// It returns ErrNoCredit when the balance will not cover the message.
func Debit(
	ctx context.Context, q *sqlc.Queries, orgID, notificationID pgtype.UUID, credits int, note string,
) (balance int32, err error) {
	if credits <= 0 {
		return 0, fmt.Errorf("notify: debit of %d credits", credits)
	}
	if _, err := EnsureCredits(ctx, q, orgID); err != nil {
		return 0, err
	}
	balance, err = q.DebitOrgSMSCredits(ctx, sqlc.DebitOrgSMSCreditsParams{
		OrgID: orgID, Credits: int32(credits),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNoCredit
	}
	if err != nil {
		return 0, err
	}
	if _, err := q.InsertSMSCreditLedger(ctx, sqlc.InsertSMSCreditLedgerParams{
		OrgID:          orgID,
		Delta:          int32(-credits),
		BalanceAfter:   balance,
		Reason:         ReasonDebit,
		NotificationID: notificationID,
		Note:           note,
	}); err != nil {
		return 0, err
	}
	return balance, nil
}

// Add applies a signed movement — a top-up or an admin adjustment — and
// records it. It refuses to take a balance below zero: the ledger is an
// accounting record, and a negative prepaid balance is a bug rather than a
// state the product has.
func Add(
	ctx context.Context, q *sqlc.Queries, orgID pgtype.UUID, delta int, reason, note string,
	adminUserID pgtype.UUID,
) (balance int32, err error) {
	cur, err := EnsureCredits(ctx, q, orgID)
	if err != nil {
		return 0, err
	}
	if int(cur.Balance)+delta < 0 {
		return cur.Balance, ErrNoCredit
	}
	balance, err = q.AddOrgSMSCredits(ctx, sqlc.AddOrgSMSCreditsParams{
		OrgID: orgID, Delta: int32(delta),
	})
	if err != nil {
		return 0, err
	}
	if _, err := q.InsertSMSCreditLedger(ctx, sqlc.InsertSMSCreditLedgerParams{
		OrgID:        orgID,
		Delta:        int32(delta),
		BalanceAfter: balance,
		Reason:       reason,
		AdminUserID:  adminUserID,
		Note:         note,
	}); err != nil {
		return 0, err
	}
	return balance, nil
}

// ReleaseHeld returns an org's held messages to the queue, oldest first, and
// reports the ids so the caller can push them onto Redis after the commit.
//
// **Every** held row is released, not only the ones the new balance covers.
// The debit happens at send time, so releasing more than the org can pay for
// costs nothing: the worker holds the surplus again, in the same order, on the
// next attempt. Releasing only the affordable prefix would mean deciding here
// what a message will cost — a second, drifting copy of the segment count —
// and would leave a message behind even when a later top-up arrives before the
// worker gets to it.
func ReleaseHeld(
	ctx context.Context, q *sqlc.Queries, orgID pgtype.UUID,
) ([]string, error) {
	rows, err := q.ListHeldNotifications(ctx, sqlc.ListHeldNotificationsParams{
		OrgID: orgID, RowLimit: ReleaseBatchLimit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if err := q.ReleaseHeldNotification(ctx, sqlc.ReleaseHeldNotificationParams{
			ID: row.ID, OrgID: orgID,
		}); err != nil {
			return nil, err
		}
		out = append(out, db.UUIDString(row.ID))
	}
	return out, nil
}

// NeededFor is the credit cost of one send of body — the number of segments it
// occupies. It is the single definition of that cost: the bulk pre-check and
// the worker's debit both call it, so the shortfall a landlord is quoted is
// the amount that will actually be taken.
func NeededFor(body string) int { return Segments(body) }

// ------------------------------------------------- low-watermark warning --

// LowWatermarkNotice emails the org's owner once, when a debit takes the
// balance from at-or-above the watermark to below it.
//
// The crossing test is what makes it once rather than on every send: a balance
// already under the line does not send a second email, so an org running at
// zero does not mail its owner forty times a day.
func LowWatermarkNotice(
	ctx context.Context, q *sqlc.Queries, email EmailProvider, logger *slog.Logger,
	orgID pgtype.UUID, before, after, watermark int32,
) {
	if email == nil || q == nil || watermark <= 0 {
		return
	}
	if !(before >= watermark && after < watermark) {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	contact, err := q.GetOrgOwnerContact(ctx, orgID)
	if err != nil || contact.OwnerEmail == "" {
		logger.Warn("sms credits low but no owner email on file",
			"org_id", db.UUIDString(orgID), "balance", after, "error", err)
		return
	}
	subject := fmt.Sprintf("%s: SMS credits are running low", contact.DisplayName)
	body := fmt.Sprintf(
		"Hello %s,\n\n%s has %d SMS credits left, below its %d-credit warning level. "+
			"Messages still go out until the balance reaches zero; after that they are held "+
			"and sent as soon as the platform tops the account up.\n",
		contact.OwnerName, contact.DisplayName, after, watermark)
	if _, err := email.Send(ctx, contact.OwnerEmail, subject, "", body); err != nil {
		logger.Warn("low-credit email failed", "org_id", db.UUIDString(orgID), "error", err)
	}
}
