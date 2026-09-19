package httpserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"tms/backend/internal/notify"
	"tms/backend/internal/tz"
)

// Phase 16 §16.3–§16.4 — due-date visibility and payment instructions.
//
// Every date here is placed relative to *today on the Dar es Salaam wall
// clock*, never `CURRENT_DATE`: the API counts the days a renter is living
// through, and between 21:00 and midnight UTC those two answers differ by one.
// A test that used the database's idea of today would pass all day and fail at
// night, which is the bug this phase exists to prevent.

// eatDay returns the calendar date n days from today in EAT, as the API writes
// it.
func eatDay(n int) string {
	return tz.LocalDate(time.Now().In(tz.Zone())).AddDate(0, 0, n).Format("2006-01-02")
}

// placeDue moves one schedule to a date relative to today in EAT, keeping its
// status.
func (h *harness) placeDue(t *testing.T, scheduleID string, daysFromToday int, status string) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE payment_schedules SET due_date = $2::date, status = $3 WHERE id = $1`,
		scheduleID, eatDay(daysFromToday), status); err != nil {
		t.Fatalf("place schedule: %v", err)
	}
}

// ------------------------------------------------------- days_until_due --

// TestDaysUntilDueIsCountedOnTheEATWallClock is the countdown chip: 0 on the
// day itself, positive before it, negative after — and the same number on the
// renter's list rows as on the `next_due` block, since they are one object.
func TestDaysUntilDueIsCountedOnTheEATWallClock(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Countdown", "0715160100", "+255715160101")

	h.parkSchedules(t, fix.contractID)
	h.placeDue(t, fix.scheduleIDs[0], -3, "overdue")
	h.placeDue(t, fix.scheduleIDs[1], 0, "pending")
	h.placeDue(t, fix.scheduleIDs[2], 5, "pending")

	mine := fix.renter.do(http.MethodGet, "/me/schedules", nil).
		mustStatus(t, http.StatusOK, "my schedules")

	want := map[string]float64{
		fix.scheduleIDs[0]: -3, fix.scheduleIDs[1]: 0, fix.scheduleIDs[2]: 5,
	}
	for _, row := range listOf(t, mine) {
		id, _ := row["id"].(string)
		expected, tracked := want[id]
		if !tracked {
			continue
		}
		if got := mustFloat(t, row, "days_until_due"); got != expected {
			t.Errorf("schedule due %s: days_until_due = %v, want %v",
				row["due_date"], got, expected)
		}
		delete(want, id)
	}
	if len(want) > 0 {
		t.Errorf("schedules missing from /me/schedules: %v — body: %s", want, mine.Raw)
	}

	// next_due is the earliest unsettled row — the one three days overdue —
	// and carries the same countdown.
	next, ok := mine.Body["next_due"].(map[string]any)
	if !ok {
		t.Fatalf("next_due is not an object — body: %s", mine.Raw)
	}
	if got, _ := next["id"].(string); got != fix.scheduleIDs[0] {
		t.Errorf("next_due.id = %q, want the overdue schedule %q", got, fix.scheduleIDs[0])
	}
	if got := mustFloat(t, next, "days_until_due"); got != -3 {
		t.Errorf("next_due.days_until_due = %v, want -3", got)
	}
	// No proof has been filed, so the block is present and null rather than
	// absent: the app branches on it.
	proof, present := next["proof"]
	if !present {
		t.Errorf("next_due carries no `proof` key — body: %s", mine.Raw)
	}
	if proof != nil {
		t.Errorf("next_due.proof = %v with no proof filed, want null", proof)
	}
}

// ---------------------------------------------------- GET /reports/upcoming --

// upcomingIDs reads the schedule ids of an upcoming response, in order.
func upcomingIDs(t *testing.T, r response) []string {
	t.Helper()
	out := []string{}
	for _, row := range listOf(t, r) {
		id, _ := row["id"].(string)
		out = append(out, id)
	}
	return out
}

// TestUpcomingWindowExcludesSettledAndFinished is the "Due soon" list: only
// money still owed on a running tenancy, only inside the window asked for.
func TestUpcomingWindowExcludesSettledAndFinished(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Upcoming", "0715160200", "+255715160201")

	h.parkSchedules(t, fix.contractID)
	h.placeDue(t, fix.scheduleIDs[0], 3, "pending")  // inside 7
	h.placeDue(t, fix.scheduleIDs[1], 10, "pending") // inside 14, outside 7
	h.placeDue(t, fix.scheduleIDs[2], 20, "pending") // inside 30 only
	h.placeDue(t, fix.scheduleIDs[3], 4, "paid")     // settled: never listed
	h.placeDue(t, fix.scheduleIDs[4], 5, "waived")   // waived: never listed

	week := fix.owner.do(http.MethodGet, "/reports/upcoming?days=7", nil).
		mustStatus(t, http.StatusOK, "upcoming 7")
	if got := upcomingIDs(t, week); len(got) != 1 || got[0] != fix.scheduleIDs[0] {
		t.Fatalf("days=7 returned %v, want only the schedule due in 3 days", got)
	}
	row := listOf(t, week)[0]
	if got := mustFloat(t, row, "days_until_due"); got != 3 {
		t.Errorf("days_until_due = %v, want 3", got)
	}
	if got, _ := row["unit_name"].(string); got == "" {
		t.Error("row carries no unit_name")
	}
	if got, _ := row["renter_name"].(string); got == "" {
		t.Error("row carries no renter_name")
	}
	if got := num(t, week, "total_due"); got != float64(fix.amounts[0]) {
		t.Errorf("total_due = %v, want the outstanding amount %d", got, fix.amounts[0])
	}
	if got := num(t, week, "count"); got != 1 {
		t.Errorf("count = %v, want 1", got)
	}
	if from := week.str(t, "window", "from"); from != eatDay(0) {
		t.Errorf("window.from = %q, want today %q", from, eatDay(0))
	}
	if to := week.str(t, "window", "to"); to != eatDay(7) {
		t.Errorf("window.to = %q, want %q", to, eatDay(7))
	}

	// The default window is 14 days (API.md §16.3).
	if got := upcomingIDs(t, fix.owner.do(http.MethodGet, "/reports/upcoming", nil).
		mustStatus(t, http.StatusOK, "upcoming default")); len(got) != 2 {
		t.Errorf("the default window returned %v, want the two rows inside 14 days", got)
	}
	if got := upcomingIDs(t, fix.owner.do(http.MethodGet, "/reports/upcoming?days=30", nil).
		mustStatus(t, http.StatusOK, "upcoming 30")); len(got) != 3 {
		t.Errorf("days=30 returned %v, want three rows", got)
	}

	// A finished tenancy owes nothing anybody is chasing.
	fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/terminate",
		map[string]any{"reason": "renter moved out"}).
		mustStatus(t, http.StatusOK, "terminate")
	after := fix.owner.do(http.MethodGet, "/reports/upcoming?days=30", nil).
		mustStatus(t, http.StatusOK, "upcoming after termination")
	if got := upcomingIDs(t, after); len(got) != 0 {
		t.Errorf("a terminated contract still lists %v", got)
	}
	if got := num(t, after, "total_due"); got != 0 {
		t.Errorf("total_due = %v after termination, want 0", got)
	}
}

// TestUpcomingValidatesDaysAndProperty covers the two refusals: a window the
// product does not offer, and a property that is not the caller's.
func TestUpcomingValidatesDaysAndProperty(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "UpcomingArgs", "0715160300", "+255715160301")
	h.parkSchedules(t, fix.contractID)
	h.placeDue(t, fix.scheduleIDs[0], 2, "pending")

	for _, bad := range []string{"1", "9", "0", "-7", "365", "seven"} {
		resp := fix.owner.do(http.MethodGet, "/reports/upcoming?days="+bad, nil).
			mustStatus(t, http.StatusBadRequest, "days="+bad)
		errs, ok := resp.Body["errors"].(map[string]any)
		if !ok || errs["days"] == nil {
			t.Errorf("days=%s: no errors.days field — body: %s", bad, resp.Raw)
		}
	}

	// The org's own property filters; an empty second property returns nothing
	// rather than everything.
	own := fix.owner.do(http.MethodPost, "/properties", map[string]any{
		"name": "Second Block", "location_text": "Dar es Salaam",
	}).mustStatus(t, http.StatusCreated, "second property").str(t, "property", "id")
	if got := upcomingIDs(t, fix.owner.do(http.MethodGet,
		"/reports/upcoming?days=7&property_id="+own, nil).
		mustStatus(t, http.StatusOK, "filter by the empty property")); len(got) != 0 {
		t.Errorf("the empty property returned %v, want nothing", got)
	}

	// A property belonging to somebody else is a 404, not an empty list.
	other := h.newOrgWithUnits("UpcomingOther", "upcomingother@jjne.test", "0715160310",
		[]string{"Room 1"}, 200_000)
	fix.owner.do(http.MethodGet, "/reports/upcoming?property_id="+other.propertyID, nil).
		mustStatus(t, http.StatusNotFound, "another org's property")
}

// TestUpcomingIsOrgScoped is the cross-org content probe: the route names no
// id, so isolation is a question about what comes back.
func TestUpcomingIsOrgScoped(t *testing.T) {
	h := newHarness(t)
	a := h.newPaymentFixture(t, "UpAlpha", "0715160400", "+255715160401")
	b := h.newPaymentFixture(t, "UpBeta", "0715160410", "+255715160411")

	h.parkSchedules(t, a.contractID)
	h.parkSchedules(t, b.contractID)
	h.placeDue(t, a.scheduleIDs[0], 1, "pending")
	h.placeDue(t, b.scheduleIDs[0], 2, "pending")

	seen := b.owner.do(http.MethodGet, "/reports/upcoming?days=7", nil).
		mustStatus(t, http.StatusOK, "org B upcoming")
	if strings.Contains(seen.Raw, a.scheduleIDs[0]) || strings.Contains(seen.Raw, a.contractID) {
		t.Fatalf("org B's upcoming report names org A's rows: %s", seen.Raw)
	}
	if got := upcomingIDs(t, seen); len(got) != 1 || got[0] != b.scheduleIDs[0] {
		t.Errorf("org B sees %v, want only its own schedule", got)
	}
}

// TestDashboardSummaryCarriesUpcoming7d: the card reads off the summary the
// dashboard already loads, so it costs no second call.
func TestDashboardSummaryCarriesUpcoming7d(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Card", "0715160500", "+255715160501")
	h.parkSchedules(t, fix.contractID)
	h.placeDue(t, fix.scheduleIDs[0], 2, "pending")
	h.placeDue(t, fix.scheduleIDs[1], 20, "pending") // outside the card's week

	summary := fix.owner.do(http.MethodGet, "/reports/summary", nil).
		mustStatus(t, http.StatusOK, "summary")
	if got := num(t, summary, "upcoming_7d", "count"); got != 1 {
		t.Errorf("upcoming_7d.count = %v, want 1", got)
	}
	if got := num(t, summary, "upcoming_7d", "total"); got != float64(fix.amounts[0]) {
		t.Errorf("upcoming_7d.total = %v, want %d", got, fix.amounts[0])
	}

	// Both Phase 16 card ids are in the allowlist the branding prefs validate
	// against; an unknown one is still refused.
	fix.owner.do(http.MethodPut, "/org/branding", map[string]any{
		"dashboard_prefs": map[string]any{"cards": []string{"overdue", "upcoming", "proofs"}},
	}).mustStatus(t, http.StatusOK, "save card order")
	fix.owner.do(http.MethodPut, "/org/branding", map[string]any{
		"dashboard_prefs": map[string]any{"cards": []string{"not_a_card"}},
	}).mustStatus(t, http.StatusBadRequest, "unknown card id")
}

// ------------------------------------------------- renters list / units board --

// TestRentersListReconcilesWithPaymentStatus is the reconciliation the plan
// asks for: the Next due column and the report are two renderings of one set of
// numbers, and they are computed from the same queries so they cannot drift.
func TestRentersListReconcilesWithPaymentStatus(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Reconcile", "0715160600", "+255715160601")

	h.parkSchedules(t, fix.contractID)
	h.placeDue(t, fix.scheduleIDs[0], -9, "overdue")
	h.placeDue(t, fix.scheduleIDs[1], 6, "pending")
	// A part payment on the overdue row, so the figures are not simply the
	// schedule amounts.
	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "schedule_id": fix.scheduleIDs[0],
		"amount": 50_000, "method": "cash",
	}).mustStatus(t, http.StatusCreated, "part payment")

	report := fix.owner.do(http.MethodGet, "/reports/payment-status", nil).
		mustStatus(t, http.StatusOK, "payment status")
	var reported map[string]any
	for _, row := range listOf(t, report) {
		if id, _ := row["renter_user_id"].(string); id == fix.renterID {
			reported = row
		}
	}
	if reported == nil {
		t.Fatalf("the renter is missing from the payment-status report: %s", report.Raw)
	}

	var listed map[string]any
	for _, row := range listOf(t, fix.owner.do(http.MethodGet, "/renters", nil).
		mustStatus(t, http.StatusOK, "renters")) {
		if id, _ := row["user_id"].(string); id == fix.renterID {
			listed = row
		}
	}
	if listed == nil {
		t.Fatalf("the renter is missing from GET /renters")
	}

	for _, field := range []string{"next_due_date", "next_due_amount", "overdue_amount"} {
		if listed[field] != reported[field] {
			t.Errorf("%s: renters list = %v, payment-status = %v — the two screens disagree",
				field, listed[field], reported[field])
		}
	}
	// And the figures are the ones the schedules actually say.
	if got, _ := listed["next_due_date"].(string); got != eatDay(-9) {
		t.Errorf("next_due_date = %v, want the unsettled overdue row %s", listed["next_due_date"], eatDay(-9))
	}
	if got := mustFloat(t, listed, "overdue_amount"); got != float64(fix.amounts[0]-50_000) {
		t.Errorf("overdue_amount = %v, want %d", got, fix.amounts[0]-50_000)
	}
}

// TestUnitsBoardCarriesNextDueDate: the chip appears on an occupied unit with
// something outstanding, and on nothing else.
func TestUnitsBoardCarriesNextDueDate(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Board", "0715160700", "+255715160701")
	h.parkSchedules(t, fix.contractID)
	h.placeDue(t, fix.scheduleIDs[0], 4, "pending")

	rows := listOf(t, fix.owner.do(http.MethodGet, "/units", nil).
		mustStatus(t, http.StatusOK, "units board"))
	var occupied, vacant map[string]any
	for _, row := range rows {
		switch id, _ := row["id"].(string); {
		case id == fix.unitIDs[0]:
			occupied = row
		case vacant == nil:
			vacant = row
		}
	}
	if occupied == nil {
		t.Fatalf("the let unit is missing from the board")
	}
	if got, _ := occupied["status"].(string); got != "occupied" {
		t.Fatalf("the let unit reads %q, want occupied", got)
	}
	if got, _ := occupied["next_due_date"].(string); got != eatDay(4) {
		t.Errorf("next_due_date = %v, want %s", occupied["next_due_date"], eatDay(4))
	}
	if vacant != nil && vacant["next_due_date"] != nil {
		t.Errorf("a vacant unit carries next_due_date = %v, want null", vacant["next_due_date"])
	}

	// Settling everything empties the chip: the unit is still occupied, but
	// nothing is owed on it.
	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "schedule_id": fix.scheduleIDs[0],
		"amount": fix.amounts[0], "method": "cash",
	}).mustStatus(t, http.StatusCreated, "settle")
	h.parkSchedules(t, fix.contractID)
	h.placeDue(t, fix.scheduleIDs[0], 4, "paid")
	for _, id := range fix.scheduleIDs[1:] {
		h.placeDue(t, id, 4, "waived")
	}
	for _, row := range listOf(t, fix.owner.do(http.MethodGet, "/units", nil).
		mustStatus(t, http.StatusOK, "units board again")) {
		if id, _ := row["id"].(string); id == fix.unitIDs[0] && row["next_due_date"] != nil {
			t.Errorf("a fully settled tenancy still shows next_due_date = %v", row["next_due_date"])
		}
	}
}

// ------------------------------------------------- payment instructions --

// TestMobileMoneyRoundTripsAndValidates is §16.4: the wallet is stored beside
// the bank account, survives PATCH /org, is cleared only when asked, and is
// refused when it is half filled in.
func TestMobileMoneyRoundTripsAndValidates(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Wallet", "wallet@jjne.test", "0715160800", []string{"Room 1"}, 200_000)
	c := fix.client

	before := c.do(http.MethodGet, "/org/bank-account", nil).
		mustStatus(t, http.StatusOK, "bank account before")
	if set, _ := before.Body["payment_instructions_set"].(bool); set {
		t.Error("payment_instructions_set is true before anything was saved")
	}

	account := map[string]any{
		"bank_name": "CRDB", "account_name": "JJnE Properties",
		"account_number": "0150123456789", "instructions": "Use your unit name as the reference",
		"mobile_money": map[string]any{
			"provider": "M-Pesa", "number": "0754123456", "name": "JJnE Properties",
		},
	}
	saved := c.do(http.MethodPut, "/org/bank-account", account).
		mustStatus(t, http.StatusOK, "save bank account")
	if got := saved.str(t, "mobile_money", "provider"); got != "M-Pesa" {
		t.Errorf("mobile_money.provider = %q, want M-Pesa", got)
	}
	// It rides inside the account block too, which is what the renter's card
	// renders from.
	if got := saved.str(t, "bank_account", "mobile_money", "number"); got != "0754123456" {
		t.Errorf("bank_account.mobile_money.number = %q, want 0754123456", got)
	}
	if set, _ := saved.Body["payment_instructions_set"].(bool); !set {
		t.Error("payment_instructions_set is false after saving instructions")
	}

	// PATCH /org must not drop a block it does not know about.
	c.do(http.MethodPatch, "/org", map[string]any{
		"settings": map[string]any{"grace_days": 5},
	}).mustStatus(t, http.StatusOK, "patch org")
	after := c.do(http.MethodGet, "/org/bank-account", nil).
		mustStatus(t, http.StatusOK, "bank account after patch")
	if got := after.str(t, "mobile_money", "number"); got != "0754123456" {
		t.Errorf("mobile_money.number = %q after PATCH /org, want it untouched", got)
	}
	if got := after.str(t, "bank_account", "bank_name"); got != "CRDB" {
		t.Errorf("bank_account.bank_name = %q after PATCH /org, want CRDB", got)
	}

	// A save that says nothing about the wallet keeps it; an explicit null
	// clears it.
	bankOnly := map[string]any{
		"bank_name": "NMB", "account_name": "JJnE Properties",
		"account_number": "2010987654321", "instructions": "",
	}
	kept := c.do(http.MethodPut, "/org/bank-account", bankOnly).
		mustStatus(t, http.StatusOK, "save without the wallet")
	if got := kept.str(t, "mobile_money", "provider"); got != "M-Pesa" {
		t.Errorf("mobile_money = %v after a bank-only save, want it kept", kept.Body["mobile_money"])
	}
	cleared := map[string]any{
		"bank_name": "NMB", "account_name": "JJnE Properties",
		"account_number": "2010987654321", "instructions": "", "mobile_money": nil,
	}
	got := c.do(http.MethodPut, "/org/bank-account", cleared).
		mustStatus(t, http.StatusOK, "clear the wallet")
	if got.Body["mobile_money"] != nil {
		t.Errorf("mobile_money = %v after an explicit null, want null", got.Body["mobile_money"])
	}

	// Half a wallet is not something a renter can pay into.
	for _, bad := range []map[string]any{
		{"provider": "M-Pesa", "number": "", "name": "JJnE"},
		{"provider": "", "number": "0754123456", "name": "JJnE"},
		{"provider": strings.Repeat("x", 41), "number": "0754123456", "name": "JJnE"},
		{"provider": "M-Pesa", "number": strings.Repeat("9", 21), "name": "JJnE"},
	} {
		body := map[string]any{
			"bank_name": "NMB", "account_name": "JJnE", "account_number": "1", "mobile_money": bad,
		}
		resp := c.do(http.MethodPut, "/org/bank-account", body).
			mustStatus(t, http.StatusBadRequest, "invalid wallet")
		errs, ok := resp.Body["errors"].(map[string]any)
		if !ok || len(errs) == 0 {
			t.Errorf("wallet %v: no field errors — body: %s", bad, resp.Raw)
		}
	}
}

// TestRenterSeesPaymentInstructions: the renter's money screen carries both
// blocks, so the pinned "How to pay" card and the proof sheet render from one
// payload.
func TestRenterSeesPaymentInstructions(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "HowToPay", "0715160900", "+255715160901")

	fix.owner.do(http.MethodPut, "/org/bank-account", map[string]any{
		"bank_name": "CRDB", "account_name": "JJnE Properties",
		"account_number": "0150123456789", "instructions": "Use your unit name",
		"mobile_money": map[string]any{
			"provider": "Airtel Money", "number": "0784000111", "name": "JJnE Properties",
		},
	}).mustStatus(t, http.StatusOK, "save instructions")

	mine := fix.renter.do(http.MethodGet, "/me/schedules", nil).
		mustStatus(t, http.StatusOK, "my schedules")
	if got := mine.str(t, "bank_account", "account_number"); got != "0150123456789" {
		t.Errorf("bank_account.account_number = %q", got)
	}
	if got := mine.str(t, "bank_account", "mobile_money", "provider"); got != "Airtel Money" {
		t.Errorf("bank_account.mobile_money.provider = %q, want Airtel Money", got)
	}
	if got := mine.str(t, "mobile_money", "number"); got != "0784000111" {
		t.Errorf("mobile_money.number = %q, want 0784000111", got)
	}
}

// ------------------------------------------------------------ {{pay_link}} --

// TestPayLinkRendersInRentReminders: the three rent reminders now say where to
// pay, resolved against the platform's own origin.
func TestPayLinkRendersInRentReminders(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "PayLink", "0715161000", "+255715161001")
	h.parkSchedules(t, fix.contractID)
	h.setDueDate(t, fix.scheduleIDs[0], schedulerToday(), "pending")

	admin := h.adminClient(t)
	admin.do(http.MethodPost, "/admin/jobs/notifications", map[string]any{"force_hour": true}).
		mustStatus(t, http.StatusOK, "run the scheduler")

	due := ofKind(h.notifications(t), notify.KindReminderDue)
	if len(due) != 1 {
		t.Fatalf("reminder_due notifications = %+v, want 1", due)
	}
	const want = "http://localhost:8080/enduser/payments"
	if !strings.Contains(due[0].Body, want) {
		t.Errorf("reminder body %q does not carry the pay link %q", due[0].Body, want)
	}
	if strings.Contains(due[0].Body, "{{pay_link}}") {
		t.Errorf("reminder body %q left the placeholder unresolved", due[0].Body)
	}
}

// TestPayLinkIsOnTheOrgWhitelist: an org may use the new variable in its own
// wording, and anything unknown is still a 400.
func TestPayLinkIsOnTheOrgWhitelist(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Whitelist", "whitelist@jjne.test", "0715161100", []string{"Room 1"}, 200_000)

	fix.client.do(http.MethodPut, "/org/notification-settings", map[string]any{
		"templates": map[string]any{"reminder_due": map[string]any{
			"en": "Hi {{name}}, {{amount}} is due today. Pay: {{pay_link}}",
			"sw": "Habari {{name}}, {{amount}} inatakiwa leo. Lipa: {{pay_link}}",
		}},
	}).mustStatus(t, http.StatusOK, "override using pay_link")

	resp := fix.client.do(http.MethodPut, "/org/notification-settings", map[string]any{
		"templates": map[string]any{"reminder_due": map[string]any{"en": "Pay at {{paylink}}"}},
	}).mustStatus(t, http.StatusBadRequest, "unknown variable")
	errs, ok := resp.Body["errors"].(map[string]any)
	if !ok || errs["templates.reminder_due.en"] == nil {
		t.Errorf("no errors.templates.reminder_due.en — body: %s", resp.Raw)
	}

	// The whitelist the admin editor's chips come from carries it too.
	if !containsString(notify.OrgVariables, "pay_link") {
		t.Error("notify.OrgVariables does not offer pay_link")
	}
	if len(notify.OrgVariables) != 9 {
		t.Errorf("the org whitelist has %d variables, want 9", len(notify.OrgVariables))
	}
	admin := h.adminClient(t)
	listed := admin.do(http.MethodGet, "/admin/templates", nil).
		mustStatus(t, http.StatusOK, "admin templates")
	for _, row := range listOf(t, listed) {
		kind, _ := row["kind"].(string)
		if kind != notify.KindReminderDue {
			continue
		}
		vars, _ := row["variables"].([]any)
		found := false
		for _, v := range vars {
			if s, _ := v.(string); s == "pay_link" {
				found = true
			}
		}
		if !found {
			t.Errorf("the admin editor's chips for %s do not include pay_link: %v", kind, vars)
		}
		body, _ := row["en"].(string)
		if !strings.Contains(body, "{{pay_link}}") {
			t.Errorf("the seeded %s default does not use pay_link: %q", kind, body)
		}
	}
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
