package httpserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"tms/backend/internal/config"
	"tms/backend/internal/platform"
	"tms/backend/internal/testutil"
)

// ------------------------------------------------------------- fixtures --

// paymentFixture is a contract that has been signed and activated, so its
// payment_schedules exist and money can be recorded against them.
type paymentFixture struct {
	contractFixture
	scheduleIDs []string
	amounts     []int64
}

// newPaymentFixture takes the Phase 4 fixture the rest of the way: the renter
// signs, the landlord activates, and the schedules are read back.
func (h *harness) newPaymentFixture(t *testing.T, tag, ownerPhone, renterPhone string) paymentFixture {
	t.Helper()
	base := h.newContractFixture(t, tag, ownerPhone, renterPhone)
	h.signAsRenter(t, base.renter, base.contractID, base.renterPhone)
	base.owner.do(http.MethodPost, "/contracts/"+base.contractID+"/activate", nil).
		mustStatus(t, http.StatusOK, "activate")

	out := paymentFixture{contractFixture: base}
	for _, row := range listOf(t, base.owner.do(http.MethodGet,
		"/contracts/"+base.contractID+"/schedules", nil).
		mustStatus(t, http.StatusOK, "schedules")) {
		id, _ := row["id"].(string)
		out.scheduleIDs = append(out.scheduleIDs, id)
		out.amounts = append(out.amounts, int64(mustFloat(t, row, "amount")))
	}
	if len(out.scheduleIDs) == 0 {
		t.Fatal("activation wrote no payment schedules")
	}
	return out
}

// recordPayment posts one payment against a contract.
func (c *client) recordPayment(body map[string]any) response {
	return c.do(http.MethodPost, "/payments", body)
}

// scheduleStatuses reads the contract's schedule statuses in due order.
func (f paymentFixture) statuses(t *testing.T) []string {
	t.Helper()
	rows := listOf(t, f.owner.do(http.MethodGet, "/contracts/"+f.contractID+"/schedules", nil).
		mustStatus(t, http.StatusOK, "schedules"))
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		s, _ := row["status"].(string)
		out = append(out, s)
	}
	return out
}

// dueInThePast rewrites a schedule's due date so the overdue sweep has
// something to find. Time travel in the database is how a one-day test watches
// a thirty-day tenancy lapse.
func (h *harness) dueInThePast(t *testing.T, scheduleID string, daysAgo int) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		"UPDATE payment_schedules SET due_date = CURRENT_DATE - $2::int WHERE id = $1",
		scheduleID, daysAgo); err != nil {
		t.Fatalf("backdate schedule: %v", err)
	}
}

// setGraceDays sets the org's grace period, the cushion the overdue sweep adds
// to every due date.
func (c *client) setGraceDays(t *testing.T, days int) {
	t.Helper()
	c.do(http.MethodPatch, "/org", map[string]any{
		"settings": map[string]any{"grace_days": days},
	}).mustStatus(t, http.StatusOK, "patch grace days")
}

// adminClient seeds and signs in the platform admin, the audience the job
// endpoints sit behind.
func (h *harness) adminClient(t *testing.T) *client {
	t.Helper()
	const email, password = "admin@tms.test", "supersecret-admin"
	if err := platform.SeedAdmin(context.Background(), h.pool,
		config.Config{AdminEmail: email, AdminPassword: password}, testutil.Logger()); err != nil {
		t.Fatalf("seed platform admin: %v", err)
	}
	c := h.client()
	c.do(http.MethodPost, "/auth/login", map[string]any{"email": email, "password": password}).
		mustStatus(t, http.StatusOK, "admin login")
	return c
}

// ------------------------------------------------------ recording money --

// TestRecordPaymentSettlesTheSchedule is FLOWS 7.2: the full amount arrives,
// the schedule flips `paid`, and the renter is thanked with the next due date.
func TestRecordPaymentSettlesTheSchedule(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Pay", "0715000100", "+255715000101")

	recorded := fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": fix.amounts[0],
		"method": "cash", "reference": "RCT-001", "note": "paid at the office",
	}).mustStatus(t, http.StatusCreated, "record payment")

	if status := recorded.str(t, "payment", "status"); status != "recorded" {
		t.Errorf("payment status = %q, want recorded", status)
	}
	applied := arrayOf(t, response{Body: recorded.Body["payment"].(map[string]any), Raw: recorded.Raw}, "applied")
	if len(applied) != 1 {
		t.Fatalf("applied = %+v, want one schedule", applied)
	}
	if got := int64(mustFloat(t, applied[0], "amount")); got != fix.amounts[0] {
		t.Errorf("applied amount = %d, want %d", got, fix.amounts[0])
	}
	if got := fix.statuses(t)[0]; got != "paid" {
		t.Errorf("first schedule status = %q, want paid", got)
	}

	// The contract summary must reflect the money, not just the schedule rows.
	summary := fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID, nil).
		mustStatus(t, http.StatusOK, "read contract")
	if got := num(t, summary, "contract", "schedules_summary", "paid_count"); got != 1 {
		t.Errorf("schedules_summary.paid_count = %v, want 1", got)
	}

	notes := ofKind(h.notifications(t), "thank_you")
	if len(notes) != 1 {
		t.Fatalf("thank_you notifications = %+v, want 1", notes)
	}
	// The thank-you names what comes next (SPEC §6).
	nextDue := listOf(t, fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID+"/schedules", nil).
		mustStatus(t, http.StatusOK, "schedules"))[1]["due_date"].(string)
	if !strings.Contains(notes[0].Body, nextDue) {
		t.Errorf("thank-you body %q does not name the next due date %q", notes[0].Body, nextDue)
	}
	if len(h.auditPayloads(t, "payment.record")) != 1 {
		t.Error("recording a payment wrote no payment.record audit row")
	}
}

// TestRecordPartialPayment: under the amount leaves the schedule `partial`, and
// a second instalment tops it up.
func TestRecordPartialPayment(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Part", "0715000110", "+255715000111")

	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": 100_000, "method": "mobile_money_manual",
	}).mustStatus(t, http.StatusCreated, "part payment")
	if got := fix.statuses(t)[0]; got != "partial" {
		t.Fatalf("status after an underpayment = %q, want partial", got)
	}

	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": fix.amounts[0] - 100_000, "method": "cash",
	}).mustStatus(t, http.StatusCreated, "balance")
	if got := fix.statuses(t)[0]; got != "paid" {
		t.Errorf("status after the balance = %q, want paid", got)
	}
}

// TestOverpaymentNeedsConfirmation is the confirm prompt of FLOWS 7.2: without
// the flag the excess is refused with a 409 naming it, with the flag it rolls
// forward across two schedules.
func TestOverpaymentNeedsConfirmation(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Over", "0715000120", "+255715000121")
	amount := fix.amounts[0] + 150_000

	refused := fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": amount, "method": "bank_transfer",
	}).mustStatus(t, http.StatusConflict, "overpay without confirmation")
	if got := refused.str(t, "type"); got != "overpay_confirm_required" {
		t.Fatalf("type = %q, want overpay_confirm_required", got)
	}
	if got := num(t, refused, "excess"); got != 150_000 {
		t.Errorf("excess = %v, want 150000", got)
	}
	if got := refused.str(t, "next_schedule", "id"); got != fix.scheduleIDs[1] {
		t.Errorf("next_schedule.id = %q, want the second schedule %q", got, fix.scheduleIDs[1])
	}
	// Nothing was written by the refusal.
	if got := fix.statuses(t)[0]; got != "pending" {
		t.Errorf("first schedule = %q after a refused overpayment, want pending", got)
	}

	accepted := fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": amount, "method": "bank_transfer",
		"allow_overpay_rollover": true,
	}).mustStatus(t, http.StatusCreated, "overpay with confirmation")
	applied := arrayOf(t, response{Body: accepted.Body["payment"].(map[string]any), Raw: accepted.Raw}, "applied")
	if len(applied) != 2 {
		t.Fatalf("applied = %+v, want two schedules", applied)
	}
	if got := int64(mustFloat(t, applied[1], "amount")); got != 150_000 {
		t.Errorf("rolled-forward amount = %d, want 150000", got)
	}
	if got := fix.statuses(t); got[0] != "paid" || got[1] != "partial" {
		t.Errorf("statuses = %v, want [paid partial …]", got)
	}
}

// TestPaymentBeyondTheContractBalance: money no schedule can absorb is refused
// outright, confirmation or not.
func TestPaymentBeyondTheContractBalance(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Excess", "0715000130", "+255715000131")

	var total int64
	for _, a := range fix.amounts {
		total += a
	}
	resp := fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": total + 1, "method": "cash",
		"allow_overpay_rollover": true,
	}).mustStatus(t, http.StatusConflict, "more than the contract owes")
	if got := resp.str(t, "type"); got != "exceeds_contract_balance" {
		t.Errorf("type = %q, want exceeds_contract_balance", got)
	}
}

// TestPaymentAgainstASettledSchedule: naming a schedule that owes nothing is a
// 409 rather than a silent roll-forward — the landlord picked the wrong row.
func TestPaymentAgainstASettledSchedule(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Settled", "0715000140", "+255715000141")

	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "schedule_id": fix.scheduleIDs[0],
		"amount": fix.amounts[0], "method": "cash",
	}).mustStatus(t, http.StatusCreated, "settle the first schedule")

	resp := fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "schedule_id": fix.scheduleIDs[0],
		"amount": 10_000, "method": "cash",
	}).mustStatus(t, http.StatusConflict, "pay a settled schedule")
	if got := resp.str(t, "type"); got != "schedule_paid" {
		t.Errorf("type = %q, want schedule_paid", got)
	}
}

// TestPaymentNeedsARunningContract: a contract awaiting signature owes nothing
// yet (API.md: contract must be active/expiring).
func TestPaymentNeedsARunningContract(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "NotLive", "0715000150", "+255715000151")

	resp := fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": 10_000, "method": "cash",
	}).mustStatus(t, http.StatusConflict, "pay an unsigned contract")
	if got := resp.str(t, "type"); got != "contract_not_active" {
		t.Errorf("type = %q, want contract_not_active", got)
	}
}

// ------------------------------------------------------------- reversal --

// TestReversePaymentRestoresStatuses is FLOWS 7.4: a correction takes the money
// back off every schedule it touched, and the payment stays on the record.
func TestReversePaymentRestoresStatuses(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Rev", "0715000160", "+255715000161")

	recorded := fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": fix.amounts[0] + 150_000,
		"method": "cash", "allow_overpay_rollover": true,
	}).mustStatus(t, http.StatusCreated, "record")
	paymentID := recorded.str(t, "payment", "id")

	reversed := fix.owner.do(http.MethodPost, "/payments/"+paymentID+"/reverse",
		map[string]any{"reason": "keyed against the wrong tenant"}).
		mustStatus(t, http.StatusOK, "reverse")
	if got := reversed.str(t, "payment", "status"); got != "reversed" {
		t.Errorf("status = %q, want reversed", got)
	}
	if reversed.Body["payment"].(map[string]any)["reversed_at"] == nil {
		t.Error("reversed_at is null after a reversal")
	}
	if got := fix.statuses(t); got[0] != "pending" || got[1] != "pending" {
		t.Errorf("statuses after reversal = %v, want both back to pending", got)
	}

	// A second reversal has nothing to undo.
	again := fix.owner.do(http.MethodPost, "/payments/"+paymentID+"/reverse",
		map[string]any{"reason": "again"}).
		mustStatus(t, http.StatusConflict, "reverse twice")
	if got := again.str(t, "type"); got != "already_reversed" {
		t.Errorf("type = %q, want already_reversed", got)
	}
	if len(h.auditPayloads(t, "payment.reverse")) != 1 {
		t.Error("the reversal wrote no payment.reverse audit row")
	}
}

// TestReverseRestoresOverdue: taking money back off a schedule whose due date
// has passed puts it back to `overdue`, not `pending` (API.md Phase 5).
func TestReverseRestoresOverdue(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "RevOd", "0715000170", "+255715000171")
	fix.owner.setGraceDays(t, 0)
	h.dueInThePast(t, fix.scheduleIDs[0], 10)

	recorded := fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "schedule_id": fix.scheduleIDs[0],
		"amount": fix.amounts[0], "method": "cash",
	}).mustStatus(t, http.StatusCreated, "settle an overdue schedule")
	if got := fix.statuses(t)[0]; got != "paid" {
		t.Fatalf("an overdue schedule paid in full = %q, want paid", got)
	}

	fix.owner.do(http.MethodPost, "/payments/"+recorded.str(t, "payment", "id")+"/reverse",
		map[string]any{"reason": "bounced"}).mustStatus(t, http.StatusOK, "reverse")
	if got := fix.statuses(t)[0]; got != "overdue" {
		t.Errorf("status after reversing a late payment = %q, want overdue", got)
	}
}

// ------------------------------------------------------- the overdue job --

// TestOverdueJobRespectsGraceDays: the sweep waits out the org's grace period
// before calling a schedule late.
func TestOverdueJobRespectsGraceDays(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Grace", "0715000180", "+255715000181")
	admin := h.adminClient(t)

	fix.owner.setGraceDays(t, 5)
	h.dueInThePast(t, fix.scheduleIDs[0], 3) // inside the grace period

	admin.do(http.MethodPost, "/admin/jobs/overdue", nil).
		mustStatus(t, http.StatusOK, "overdue job")
	if got := fix.statuses(t)[0]; got != "pending" {
		t.Errorf("status 3 days late with 5 days' grace = %q, want pending", got)
	}

	h.dueInThePast(t, fix.scheduleIDs[0], 9) // past it
	flipped := admin.do(http.MethodPost, "/admin/jobs/overdue", nil).
		mustStatus(t, http.StatusOK, "overdue job")
	if got := num(t, flipped, "flipped"); got < 1 {
		t.Errorf("flipped = %v, want at least 1", got)
	}
	if got := fix.statuses(t)[0]; got != "overdue" {
		t.Errorf("status 9 days late with 5 days' grace = %q, want overdue", got)
	}

	// A second run has nothing left to do — the sweep is idempotent.
	rerun := admin.do(http.MethodPost, "/admin/jobs/overdue", nil).
		mustStatus(t, http.StatusOK, "overdue job again")
	if got := num(t, rerun, "flipped"); got != 0 {
		t.Errorf("a second sweep flipped %v rows, want 0", got)
	}
	// The org's own read applies the sweep on demand, so the board agrees.
	board := listOf(t, fix.owner.do(http.MethodGet, "/schedules?status=overdue", nil).
		mustStatus(t, http.StatusOK, "overdue board"))
	if len(board) != 1 {
		t.Fatalf("overdue board holds %d rows, want 1", len(board))
	}
	if got := int(mustFloat(t, board[0], "days_overdue")); got != 9 {
		t.Errorf("days_overdue = %d, want 9", got)
	}
	contractBlock, _ := board[0]["contract"].(map[string]any)
	if contractBlock == nil || contractBlock["unit_name"] == nil {
		t.Errorf("the schedule carries no contract identity block: %v", board[0])
	}
}

// TestOverdueOnDemandOnRenterRead: the renter's own screen sweeps too, so a
// lapsed instalment is red the moment they open it.
func TestOverdueOnDemandOnRenterRead(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "OdRead", "0715000190", "+255715000191")
	fix.owner.setGraceDays(t, 0)
	h.dueInThePast(t, fix.scheduleIDs[0], 2)

	mine := fix.renter.do(http.MethodGet, "/me/schedules", nil).
		mustStatus(t, http.StatusOK, "my schedules")
	rows := listOf(t, mine)
	if got, _ := rows[0]["status"].(string); got != "overdue" {
		t.Errorf("status on the renter's screen = %q, want overdue", got)
	}
	if got := num(t, mine, "overdue_total"); got != float64(fix.amounts[0]) {
		t.Errorf("overdue_total = %v, want %d", got, fix.amounts[0])
	}
	if mine.Body["next_due"] == nil {
		t.Error("next_due is null while an instalment is outstanding")
	}
}

// ----------------------------------------------------------- bank account --

// TestBankAccountReachesTheRenter: the landlord sets the collection account and
// the renter's payment screen shows it (FLOWS 7.2).
func TestBankAccountReachesTheRenter(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Bank", "0715000200", "+255715000201")

	if fix.owner.do(http.MethodGet, "/org/bank-account", nil).
		mustStatus(t, http.StatusOK, "empty bank account").Body["bank_account"] != nil {
		t.Error("an org with no account set returns one anyway")
	}
	fix.owner.do(http.MethodPut, "/org/bank-account", map[string]any{
		"bank_name": "CRDB Bank", "account_name": "JJnE Rentals Ltd",
		"account_number": "0150123456789", "instructions": "Use your unit name as the reference.",
	}).mustStatus(t, http.StatusOK, "put bank account")

	mine := fix.renter.do(http.MethodGet, "/me/schedules", nil).
		mustStatus(t, http.StatusOK, "my schedules")
	if got := mine.str(t, "bank_account", "account_number"); got != "0150123456789" {
		t.Errorf("account_number on the renter's screen = %q", got)
	}
	// Settings the endpoint does not own are untouched by it.
	if got := num(t, fix.owner.do(http.MethodGet, "/org", nil).
		mustStatus(t, http.StatusOK, "read org"), "settings", "grace_days"); got != 3 {
		t.Errorf("grace_days = %v after setting the bank account, want the default 3", got)
	}
	if len(h.auditPayloads(t, "org.bank_account_update")) != 1 {
		t.Error("setting the bank account wrote no audit row")
	}
}

// --------------------------------------------------------- renter history --

// TestRenterSeesOnlyTheirOwnPayments: /me/payments is the renter's own money,
// and never anyone else's.
func TestRenterSeesOnlyTheirOwnPayments(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Mine", "0715000210", "+255715000211")
	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": fix.amounts[0], "method": "cash",
	}).mustStatus(t, http.StatusCreated, "record")

	mine := listOf(t, fix.renter.do(http.MethodGet, "/me/payments", nil).
		mustStatus(t, http.StatusOK, "my payments"))
	if len(mine) != 1 {
		t.Fatalf("the renter sees %d payments, want 1", len(mine))
	}
	rb, _ := mine[0]["recorded_by"].(map[string]any)
	if rb == nil || rb["name"] == nil {
		t.Fatalf("recorded_by is missing a name: %v", mine[0])
	}
	if _, leaked := rb["user_id"]; leaked {
		t.Error("the renter's history exposes the staff member's user id")
	}

	// A different renter of the same org sees none of it.
	stranger := h.registerRenter("+255715000219", "Stranger", defaultPIN)
	if got := listOf(t, stranger.do(http.MethodGet, "/me/payments", nil).
		mustStatus(t, http.StatusOK, "stranger payments")); len(got) != 0 {
		t.Errorf("a stranger sees %d payments, want 0", len(got))
	}
}

// ------------------------------------------------------------- isolation --

// TestPaymentIsolationBetweenOrgs is SPEC §8: another org's contract, schedule
// and payment are simply not there — 404, never 403.
func TestPaymentIsolationBetweenOrgs(t *testing.T) {
	h := newHarness(t)
	a := h.newPaymentFixture(t, "IsoA", "0715000220", "+255715000221")
	b := h.newOrgWithUnits("IsoB", "isob@jjne.test", "0715000230", []string{"Room 1"}, 250_000)

	recorded := a.owner.recordPayment(map[string]any{
		"contract_id": a.contractID, "amount": a.amounts[0], "method": "cash",
	}).mustStatus(t, http.StatusCreated, "org A records")
	paymentID := recorded.str(t, "payment", "id")

	t.Run("recording against another org's contract is a 404", func(t *testing.T) {
		b.client.recordPayment(map[string]any{
			"contract_id": a.contractID, "amount": 10_000, "method": "cash",
		}).mustStatus(t, http.StatusNotFound, "cross-org record")
	})
	t.Run("reading another org's payment is a 404", func(t *testing.T) {
		b.client.do(http.MethodGet, "/payments/"+paymentID, nil).
			mustStatus(t, http.StatusNotFound, "cross-org read")
	})
	t.Run("reversing another org's payment is a 404", func(t *testing.T) {
		b.client.do(http.MethodPost, "/payments/"+paymentID+"/reverse",
			map[string]any{"reason": "not mine"}).
			mustStatus(t, http.StatusNotFound, "cross-org reverse")
	})
	t.Run("the schedule board holds only the caller's org", func(t *testing.T) {
		if got := listOf(t, b.client.do(http.MethodGet, "/schedules", nil).
			mustStatus(t, http.StatusOK, "org B schedules")); len(got) != 0 {
			t.Errorf("org B sees %d schedules, want 0", len(got))
		}
		if got := listOf(t, b.client.do(http.MethodGet, "/payments", nil).
			mustStatus(t, http.StatusOK, "org B payments")); len(got) != 0 {
			t.Errorf("org B sees %d payments, want 0", len(got))
		}
	})
	t.Run("a schedule of another org cannot be named as the target", func(t *testing.T) {
		a.owner.recordPayment(map[string]any{
			"contract_id": a.contractID, "schedule_id": b.orgID, // any foreign id
			"amount": 10_000, "method": "cash",
		}).mustStatus(t, http.StatusNotFound, "foreign schedule id")
	})
	t.Run("the job endpoints refuse an org session", func(t *testing.T) {
		a.owner.do(http.MethodPost, "/admin/jobs/overdue", nil).
			mustStatus(t, http.StatusUnauthorized, "org session on an admin job")
	})
}

// ------------------------------------------------------------- filtering --

// TestScheduleAndPaymentFilters pins the query parameters the landlord's views
// depend on.
func TestScheduleAndPaymentFilters(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Filter", "0715000240", "+255715000241")
	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": fix.amounts[0], "method": "bank_transfer",
	}).mustStatus(t, http.StatusCreated, "record")

	all := listOf(t, fix.owner.do(http.MethodGet, "/schedules", nil).
		mustStatus(t, http.StatusOK, "all schedules"))
	if len(all) != len(fix.scheduleIDs) {
		t.Fatalf("board holds %d rows, want %d", len(all), len(fix.scheduleIDs))
	}
	paid := listOf(t, fix.owner.do(http.MethodGet, "/schedules?status=paid", nil).
		mustStatus(t, http.StatusOK, "paid schedules"))
	if len(paid) != 1 {
		t.Errorf("status=paid returned %d rows, want 1", len(paid))
	}
	byRenter := listOf(t, fix.owner.do(http.MethodGet, "/schedules?renter_user_id="+fix.renterID, nil).
		mustStatus(t, http.StatusOK, "by renter"))
	if len(byRenter) != len(all) {
		t.Errorf("renter filter returned %d rows, want %d", len(byRenter), len(all))
	}
	// A due window that ends before the first instalment holds nothing.
	past := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
	if got := listOf(t, fix.owner.do(http.MethodGet, "/schedules?due_to="+past, nil).
		mustStatus(t, http.StatusOK, "due window")); len(got) != 0 {
		t.Errorf("a window ending %s returned %d rows, want 0", past, len(got))
	}
	if got := listOf(t, fix.owner.do(http.MethodGet, "/payments?method=cash", nil).
		mustStatus(t, http.StatusOK, "cash payments")); len(got) != 0 {
		t.Errorf("method=cash returned %d rows, want 0 (the payment was a transfer)", len(got))
	}
	fix.owner.do(http.MethodGet, "/schedules?status=nonsense", nil).
		mustStatus(t, http.StatusBadRequest, "bad status filter")
}
