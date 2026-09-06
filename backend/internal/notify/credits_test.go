package notify_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/notify"
	"tms/backend/internal/testutil"
)

// creditOrg seeds an org, a renter and a credit balance.
func creditOrg(t *testing.T, q *sqlc.Queries, pool *db.Pool, tag string, balance int) (orgID, userID string) {
	t.Helper()
	ctx := context.Background()
	org, err := q.CreateOrg(ctx, sqlc.CreateOrgParams{
		Name: "Credit " + tag, Slug: "credit-" + strings.ToLower(tag), Settings: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	phone := "+2557991" + tag
	user, err := q.CreateUser(ctx, sqlc.CreateUserParams{
		Kind: "renter", Phone: &phone, FullName: "Credit Renter " + tag,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO org_sms_credits (org_id, balance) VALUES ($1, $2)
		 ON CONFLICT (org_id) DO UPDATE SET balance = EXCLUDED.balance`,
		org.ID, balance); err != nil {
		t.Fatalf("seed credits: %v", err)
	}
	return db.UUIDString(org.ID), db.UUIDString(user.ID)
}

func balanceOf(t *testing.T, pool *db.Pool, orgID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT balance FROM org_sms_credits WHERE org_id = $1`, orgID).Scan(&n); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return n
}

func countStatus(t *testing.T, pool *db.Pool, orgID, status string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM notification_log WHERE org_id = $1 AND status = $2`,
		orgID, status).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", status, err)
	}
	return n
}

// waitForTerminal blocks until no message of the org is still queued or
// sending, so the assertions run against a settled pool rather than a race.
func waitForTerminal(t *testing.T, pool *db.Pool, orgID string, total int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var done int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM notification_log
			 WHERE org_id = $1 AND status IN ('sent', 'failed', 'held_no_credit')`,
			orgID).Scan(&done); err == nil && done == total {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("messages did not settle: %d sent, %d held, %d queued",
		countStatus(t, pool, orgID, "sent"),
		countStatus(t, pool, orgID, notify.StatusHeldNoCredit),
		countStatus(t, pool, orgID, "queued"))
}

// TestWorkerDebitsAtomicallyUnderConcurrency is the risk PLAN2 names: three
// workers draining one org's queue against a balance that covers only some of
// it. Exactly `balance` messages must be sent and the rest held — never a
// fourth send paid for by a balance that went negative, and never a credit
// taken for a message nobody sent.
//
// Run it under `-race`: the guarantee is not in the Go code but in the single
// conditional UPDATE, and the race detector is what proves the Go side around
// it is not quietly sharing state.
func TestWorkerDebitsAtomicallyUnderConcurrency(t *testing.T) {
	pool := testutil.Pool(t)
	redis := testutil.Redis(t)
	q := sqlc.New(pool)
	const (
		messages = 12
		balance  = 5
	)
	orgID, userID := creditOrg(t, q, pool, "race", balance)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var ids []string
	for i := 0; i < messages; i++ {
		id, err := notify.Queue(ctx, q, notify.Msg{
			OrgID: orgID, UserID: userID, Kind: notify.KindReminderDue,
			DedupeKey: fmt.Sprintf("reminder_due:race:%d", i),
			Phone:     "+255799000001", Body: "kodi yako inatakiwa leo",
		})
		if err != nil {
			t.Fatalf("Queue %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	notify.Enqueue(ctx, redis.Client, testutil.Logger(), ids...)

	sms := &countingProvider{}
	go notify.RunWorker(ctx, notify.Worker{
		Q: q, Redis: redis.Client, SMS: sms, Logger: testutil.Logger(),
		PollWait: 50 * time.Millisecond, Workers: 3,
		Pool: pool, Exempt: notify.ParseExemptKinds(""),
	})
	waitForTerminal(t, pool, orgID, messages)

	if got := countStatus(t, pool, orgID, "sent"); got != balance {
		t.Errorf("sent = %d, want exactly the %d the balance covered", got, balance)
	}
	if got := countStatus(t, pool, orgID, notify.StatusHeldNoCredit); got != messages-balance {
		t.Errorf("held = %d, want %d", got, messages-balance)
	}
	if got := balanceOf(t, pool, orgID); got != 0 {
		t.Errorf("balance = %d, want 0 — every credit spent, none overdrawn", got)
	}
	if got := sms.count(); got != balance {
		t.Errorf("the provider saw %d sends, want %d", got, balance)
	}

	// One debit row per send, each carrying the notification it paid for and
	// the balance it left behind, so the balance reconciles from its movements.
	rows, err := pool.Query(ctx,
		`SELECT delta, balance_after, reason, notification_id FROM sms_credit_ledger
		 WHERE org_id = $1 ORDER BY balance_after DESC`, orgID)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var (
			delta, after int
			reason       string
			notifyID     *string
		)
		if err := rows.Scan(&delta, &after, &reason, &notifyID); err != nil {
			t.Fatalf("scan ledger: %v", err)
		}
		seen++
		if reason != notify.ReasonDebit || delta != -1 {
			t.Errorf("ledger row %d: reason=%q delta=%d, want debit/-1", seen, reason, delta)
		}
		if notifyID == nil {
			t.Errorf("ledger row %d carries no notification_id", seen)
		}
		// Ordered by balance_after, the rows must walk the balance down one
		// credit at a time with no gap and no repeat — which is the same
		// statement as "no two debits saw the same balance".
		if after != balance-seen {
			t.Errorf("ledger row %d: balance_after = %d, want %d", seen, after, balance-seen)
		}
	}
	if seen != balance {
		t.Errorf("%d ledger rows, want %d", seen, balance)
	}
}

// TestExemptKindSendsWithoutCredit: a verification code goes out at a zero
// balance and costs nothing. A renter locked out of their own account because
// their landlord ran out of credit would be the product punishing the wrong
// person.
func TestExemptKindSendsWithoutCredit(t *testing.T) {
	pool := testutil.Pool(t)
	redis := testutil.Redis(t)
	q := sqlc.New(pool)
	orgID, userID := creditOrg(t, q, pool, "otp", 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	id, err := notify.Queue(ctx, q, notify.Msg{
		OrgID: orgID, UserID: userID, Kind: notify.KindOTP,
		DedupeKey: "otp:exempt:1", Phone: "+255799000001", Body: "code 123456",
	})
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	notify.Enqueue(ctx, redis.Client, testutil.Logger(), id)

	go notify.RunWorker(ctx, notify.Worker{
		Q: q, Redis: redis.Client, SMS: &countingProvider{}, Logger: testutil.Logger(),
		PollWait: 50 * time.Millisecond, Workers: 1,
		Pool: pool, Exempt: notify.ParseExemptKinds(""),
	})
	waitForStatus(t, pool, id, "sent")

	var ledger int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM sms_credit_ledger WHERE org_id = $1`, orgID).Scan(&ledger); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	if ledger != 0 {
		t.Errorf("an exempt send wrote %d ledger rows, want 0", ledger)
	}
	if got := balanceOf(t, pool, orgID); got != 0 {
		t.Errorf("balance = %d, want 0 (unchanged)", got)
	}
}

// TestReleaseHeldIsOldestFirst: after a top-up the backlog leaves in the order
// it arrived, so the renter who has been waiting longest is texted first.
func TestReleaseHeldIsOldestFirst(t *testing.T) {
	pool := testutil.Pool(t)
	q := sqlc.New(pool)
	orgID, userID := creditOrg(t, q, pool, "hold", 0)
	ctx := context.Background()

	var want []string
	for i := 0; i < 4; i++ {
		id, err := notify.Queue(ctx, q, notify.Msg{
			OrgID: orgID, UserID: userID, Kind: notify.KindOverdueDaily,
			DedupeKey: fmt.Sprintf("overdue_daily:hold:%d", i),
			Phone:     "+255799000001", Body: "bado haijalipwa",
		})
		if err != nil {
			t.Fatalf("Queue %d: %v", i, err)
		}
		// Age each row so `ORDER BY created_at` has something to order by:
		// four inserts in one millisecond would otherwise tie.
		if _, err := pool.Exec(ctx,
			`UPDATE notification_log SET status = 'held_no_credit',
			 created_at = now() - ($2::int * interval '1 minute') WHERE id = $1`,
			id, 10-i); err != nil {
			t.Fatalf("hold %d: %v", i, err)
		}
		want = append(want, id)
	}

	orgUUID, err := db.ParseUUID(orgID)
	if err != nil {
		t.Fatalf("parse org id: %v", err)
	}
	got, err := notify.ReleaseHeld(ctx, q, orgUUID)
	if err != nil {
		t.Fatalf("ReleaseHeld: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("released %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("release order[%d] = %s, want %s — the oldest held message goes first",
				i, got[i], want[i])
		}
	}
	if n := countStatus(t, pool, orgID, "queued"); n != len(want) {
		t.Errorf("%d rows are queued after the release, want %d", n, len(want))
	}
	if n := countStatus(t, pool, orgID, notify.StatusHeldNoCredit); n != 0 {
		t.Errorf("%d rows are still held after the release, want 0", n)
	}
}

// TestDebitNeverGoesNegative pins the conditional directly: a debit larger
// than the balance takes nothing and reports ErrNoCredit.
func TestDebitNeverGoesNegative(t *testing.T) {
	pool := testutil.Pool(t)
	q := sqlc.New(pool)
	orgID, _ := creditOrg(t, q, pool, "floor", 3)
	ctx := context.Background()
	orgUUID, _ := db.ParseUUID(orgID)

	if _, err := notify.Debit(ctx, q, orgUUID, pgtype.UUID{}, 4, "too big"); err != notify.ErrNoCredit {
		t.Fatalf("Debit(4) over a balance of 3 = %v, want ErrNoCredit", err)
	}
	if got := balanceOf(t, pool, orgID); got != 3 {
		t.Errorf("balance = %d, want 3 — a refused debit takes nothing", got)
	}
	balance, err := notify.Debit(ctx, q, orgUUID, pgtype.UUID{}, 3, "exact")
	if err != nil {
		t.Fatalf("Debit(3): %v", err)
	}
	if balance != 0 {
		t.Errorf("balance after the exact debit = %d, want 0", balance)
	}
}

// TestCreditsLazyRow: an org that has never been topped up still has a
// balance — zero — and the default watermark, rather than a missing row every
// caller has to special-case.
func TestCreditsLazyRow(t *testing.T) {
	pool := testutil.Pool(t)
	q := sqlc.New(pool)
	ctx := context.Background()
	org, err := q.CreateOrg(ctx, sqlc.CreateOrgParams{
		Name: "Lazy Credits", Slug: "lazy-credits", Settings: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	row, err := notify.EnsureCredits(ctx, q, org.ID)
	if err != nil {
		t.Fatalf("EnsureCredits: %v", err)
	}
	if row.Balance != 0 {
		t.Errorf("balance = %d, want 0", row.Balance)
	}
	if row.LowWatermark != 50 {
		t.Errorf("low_watermark = %d, want the 50 the schema defaults to", row.LowWatermark)
	}
	// A second call is a read, not a second row.
	if _, err := notify.EnsureCredits(ctx, q, org.ID); err != nil {
		t.Fatalf("second EnsureCredits: %v", err)
	}
}

// TestLedgerIsAppendOnly verifies the migration 000012 trigger is doing its
// job: a balance nobody can reconstruct from its movements is not an
// accounting record.
func TestLedgerIsAppendOnly(t *testing.T) {
	pool := testutil.Pool(t)
	q := sqlc.New(pool)
	orgID, _ := creditOrg(t, q, pool, "append", 10)
	ctx := context.Background()
	orgUUID, _ := db.ParseUUID(orgID)

	if _, err := notify.Debit(ctx, q, orgUUID, pgtype.UUID{}, 1, "one"); err != nil {
		t.Fatalf("Debit: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE sms_credit_ledger SET delta = 0 WHERE org_id = $1`, orgID); err == nil {
		t.Error("an UPDATE on sms_credit_ledger succeeded; the append-only trigger is missing")
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM sms_credit_ledger WHERE org_id = $1`, orgID); err == nil {
		t.Error("a DELETE on sms_credit_ledger succeeded; the append-only trigger is missing")
	}
}
