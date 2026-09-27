package httpserver_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"tms/backend/internal/contract"
)

// Phase 29 — a backfill that reaches back before the contract's start date.
//
// The fixture is a fresh contract (starting about today), the shape a landlord
// onboarding a long-standing renter actually has. `from` puts the real move-in
// on it; the assertions are about the periods that appear, which of them are
// settled, and that an undo leaves the book exactly as it was.

// history29 is what the tests need to know about the fixture's contract.
type history29 struct {
	start   time.Time
	cadence int
	dueDay  *int
}

func (h *harness) contractHistory29(t *testing.T, contractID string) history29 {
	t.Helper()
	var out history29
	var dueDay *int32
	if err := h.pool.QueryRow(context.Background(),
		`SELECT start_date, payment_period_days, due_day FROM contracts WHERE id = $1`, contractID).
		Scan(&out.start, &out.cadence, &dueDay); err != nil {
		t.Fatalf("read contract: %v", err)
	}
	if dueDay != nil {
		d := int(*dueDay)
		out.dueDay = &d
	}
	return out
}

func (h *harness) scheduleCount29(t *testing.T, contractID string) int {
	t.Helper()
	var n int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM payment_schedules WHERE contract_id = $1 AND deleted_at IS NULL`, contractID).
		Scan(&n); err != nil {
		t.Fatalf("count schedules: %v", err)
	}
	return n
}

const date29 = "2006-01-02"

// TestPhase29BackfillFromCreatesAndSettlesHistory is the main path: 90 days of
// history at an older rent, settled up to a date inside it, the rest overdue,
// and the undo removes the lot.
func TestPhase29BackfillFromCreatesAndSettlesHistory(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "History", "0729000100", "+255729000101")
	c := h.contractHistory29(t, fix.contractID)
	before := h.scheduleCount29(t, fix.contractID)

	from := c.start.AddDate(0, 0, -90)
	until := from.AddDate(0, 0, c.cadence) // the first two periods
	const oldRent = 60_000
	want := contract.Generate(oldRent, c.cadence, 90, c.cadence, from, c.dueDay)
	var wantSettled int
	var wantTotal int64
	for _, r := range want {
		if !r.DueDate.After(until) {
			wantSettled++
			wantTotal += r.Amount
		}
	}

	body := map[string]any{
		"until": until.Format(date29), "mode": "paid", "from": from.Format(date29),
		"period_amount": oldRent, "reference": "PAPER-CONTRACT",
	}

	// The dry run answers the same numbers and writes nothing.
	dry := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill",
		withKey(body, "dry_run", true)).mustStatus(t, http.StatusOK, "dry run")
	if got := int(mustFloat(t, dry.Body, "created")); got != len(want) {
		t.Errorf("dry run created = %d, want %d", got, len(want))
	}
	if got := int(mustFloat(t, dry.Body, "settled")); got != wantSettled {
		t.Errorf("dry run settled = %d, want %d", got, wantSettled)
	}
	if got := h.scheduleCount29(t, fix.contractID); got != before {
		t.Fatalf("schedules after a dry run = %d, want %d (nothing written)", got, before)
	}
	if got := len(backfillList29(t, fix)); got != 0 {
		t.Fatalf("backfills after a dry run = %d, want 0", got)
	}

	done := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", body).
		mustStatus(t, http.StatusOK, "backfill from")
	if got := int(mustFloat(t, done.Body, "created")); got != len(want) {
		t.Errorf("created = %d, want %d", got, len(want))
	}
	if got := int(mustFloat(t, done.Body, "settled")); got != wantSettled {
		t.Errorf("settled = %d, want %d", got, wantSettled)
	}
	if got := int64(mustFloat(t, done.Body, "total")); got != wantTotal {
		t.Errorf("total = %d, want %d at the old rent", got, wantTotal)
	}
	if got := h.scheduleCount29(t, fix.contractID); got != before+len(want) {
		t.Errorf("schedules = %d, want %d", got, before+len(want))
	}

	// The created periods sit first in the book: settled up to `until`, the
	// rest overdue — the truth until someone says otherwise.
	rows := listOf(t, fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID+"/schedules", nil).
		mustStatus(t, http.StatusOK, "schedules"))
	for i, r := range want {
		got := rows[i]
		if got["period_start"] != r.PeriodStart.Format(date29) {
			t.Errorf("row %d period_start = %v, want %s", i, got["period_start"], r.PeriodStart.Format(date29))
		}
		wantStatus := "overdue"
		if !r.DueDate.After(until) {
			wantStatus = "paid"
		}
		if got["status"] != wantStatus {
			t.Errorf("row %d status = %v, want %s", i, got["status"], wantStatus)
		}
	}

	// The contract document lists the signed terms, not the history.
	doc := fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID+"/document", nil).
		mustStatus(t, http.StatusOK, "document")
	if sched, _ := doc.Body["schedule"].([]any); len(sched) != before {
		t.Errorf("document schedule rows = %d, want %d (no created history)", len(sched), before)
	}

	item := backfillList29(t, fix)[0]
	if item["from"] != from.Format(date29) || int(mustFloat(t, item, "created_periods")) != len(want) {
		t.Errorf("list item = %v, want from and created_periods", item)
	}

	// A second `from` would overlap the first.
	again := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", body)
	if again.Code != http.StatusConflict || again.Body["type"] != "history_exists" {
		t.Errorf("second from = %d %v, want 409 history_exists", again.Code, again.Body["type"])
	}

	undone := fix.owner.do(http.MethodPost, "/backfills/"+done.str(t, "backfill_id")+"/undo",
		map[string]any{"reason": "wrong move-in date"}).mustStatus(t, http.StatusOK, "undo")
	if got := int(mustFloat(t, undone.Body, "periods_removed")); got != len(want) {
		t.Errorf("periods_removed = %d, want %d", got, len(want))
	}
	if got := h.scheduleCount29(t, fix.contractID); got != before {
		t.Errorf("schedules after the undo = %d, want %d", got, before)
	}
	// With the history gone, `from` is allowed again.
	fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", body).
		mustStatus(t, http.StatusOK, "backfill from again after the undo")
}

// TestPhase29UntilAloneSettlesCreatedHistory: once history exists, a later
// call without `from` can settle more of it, and may name a date before the
// contract's start.
func TestPhase29UntilAloneSettlesCreatedHistory(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "HistoryLater", "0729000200", "+255729000201")
	c := h.contractHistory29(t, fix.contractID)
	from := c.start.AddDate(0, 0, -90)

	fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", map[string]any{
		"until": from.Format(date29), "mode": "paid", "from": from.Format(date29),
	}).mustStatus(t, http.StatusOK, "first period only")

	later := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", map[string]any{
		"until": c.start.AddDate(0, 0, -1).Format(date29), "mode": "waived", "note": "landlord forgave",
	}).mustStatus(t, http.StatusOK, "waive the rest of the history")
	if got := int(mustFloat(t, later.Body, "settled")); got < 1 {
		t.Errorf("settled = %d, want the remaining history periods", got)
	}
	if got := int(mustFloat(t, later.Body, "created")); got != 0 {
		t.Errorf("created = %d, want 0 without from", got)
	}

	early := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", map[string]any{
		"until": from.AddDate(0, 0, -1).Format(date29), "mode": "paid",
	})
	if early.Code != http.StatusUnprocessableEntity {
		t.Errorf("until before the first period = %d, want 422", early.Code)
	}
}

// TestPhase29BackfillFromValidation: the refusals, each naming its field.
func TestPhase29BackfillFromValidation(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "HistoryBad", "0729000300", "+255729000301")
	c := h.contractHistory29(t, fix.contractID)
	start := c.start
	post := func(body map[string]any) response {
		return fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", body)
	}
	cases := []struct {
		name   string
		body   map[string]any
		status int
		field  string
	}{
		{"until before start without from", map[string]any{
			"until": start.AddDate(0, 0, -10).Format(date29), "mode": "paid"}, 422, "until"},
		{"from on the start date", map[string]any{
			"until": start.Format(date29), "mode": "paid", "from": start.Format(date29)}, 422, "from"},
		{"from over ten years back", map[string]any{
			"until": start.AddDate(-10, 0, -5).Format(date29), "mode": "paid",
			"from": start.AddDate(-10, 0, -10).Format(date29)}, 422, "from"},
		{"until before from", map[string]any{
			"until": start.AddDate(0, 0, -60).Format(date29), "mode": "paid",
			"from": start.AddDate(0, 0, -30).Format(date29)}, 422, "until"},
		{"period_amount without from", map[string]any{
			"until": start.Format(date29), "mode": "paid", "period_amount": 50_000}, 400, "period_amount"},
		{"period_amount zero", map[string]any{
			"until": start.AddDate(0, 0, -1).Format(date29), "mode": "paid",
			"from": start.AddDate(0, 0, -30).Format(date29), "period_amount": 0}, 400, "period_amount"},
	}
	for _, tc := range cases {
		got := post(tc.body)
		if got.Code != tc.status {
			t.Errorf("%s: status = %d, want %d (%v)", tc.name, got.Code, tc.status, got.Body)
			continue
		}
		if errs, _ := got.Body["errors"].(map[string]any); errs == nil || errs[tc.field] == nil {
			t.Errorf("%s: errors = %v, want one on %s", tc.name, got.Body["errors"], tc.field)
		}
	}
	if got := h.scheduleCount29(t, fix.contractID); got != len(fix.scheduleIDs) {
		t.Errorf("schedules after refusals = %d, want %d", got, len(fix.scheduleIDs))
	}
}

// TestPhase29UndoRefusedWhenCreatedPeriodTouched: real money later recorded on
// a created period that the backfill left overdue blocks the undo — removing
// the period would orphan that money.
func TestPhase29UndoRefusedWhenCreatedPeriodTouched(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "HistoryTouched", "0729000400", "+255729000401")
	c := h.contractHistory29(t, fix.contractID)
	from := c.start.AddDate(0, 0, -90)

	done := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", map[string]any{
		"until": from.Format(date29), "mode": "paid", "from": from.Format(date29),
	}).mustStatus(t, http.StatusOK, "backfill the first period")

	var overdue map[string]any
	for _, r := range listOf(t, fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID+"/schedules", nil).
		mustStatus(t, http.StatusOK, "schedules")) {
		if r["status"] == "overdue" {
			overdue = r
			break
		}
	}
	if overdue == nil {
		t.Fatal("no overdue created period to pay")
	}
	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "schedule_id": overdue["id"],
		"amount": int64(mustFloat(t, overdue, "amount")), "method": "cash",
	}).mustStatus(t, http.StatusCreated, "real money on a created period")

	refused := fix.owner.do(http.MethodPost, "/backfills/"+done.str(t, "backfill_id")+"/undo",
		map[string]any{"reason": "try"})
	if refused.Code != http.StatusConflict || refused.Body["type"] != "touched_since" {
		t.Fatalf("undo = %d %v, want 409 touched_since", refused.Code, refused.Body["type"])
	}
}

// TestPhase29BackfillImportFrom: the CSV's `from` and `period_amount` columns
// preview the created periods and commit them.
func TestPhase29BackfillImportFrom(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "HistoryCSV", "0729000500", "+255729000501")
	c := h.contractHistory29(t, fix.contractID)
	from := c.start.AddDate(0, 0, -60)
	until := c.start.AddDate(0, 0, -1)
	want := contract.Generate(40_000, c.cadence, 60, c.cadence, from, c.dueDay)

	preview := fix.owner.importPreview(t, "backfill", "history.csv",
		"renter_phone,unit_code,until,mode,from,period_amount\n"+
			fix.renterPhone+","+fix.unitCodes[0]+","+until.Format(date29)+",paid,"+
			from.Format(date29)+",\"40,000\"\n").
		mustStatus(t, http.StatusCreated, "preview")
	res, _ := importRows(t, preview)[2]["resolved"].(map[string]any)
	if res == nil || int(mustFloat(t, res, "created")) != len(want) ||
		int(mustFloat(t, res, "periods")) != len(want) {
		t.Fatalf("resolved = %v, want %d created and settled", res, len(want))
	}
	var wantTotal int64
	for _, r := range want {
		wantTotal += r.Amount
	}
	if got := int64(mustFloat(t, res, "amount")); got != wantTotal {
		t.Errorf("preview amount = %d, want %d", got, wantTotal)
	}

	fix.owner.do(http.MethodPost, "/imports/"+preview.str(t, "batch", "id")+"/commit", nil).
		mustStatus(t, http.StatusOK, "commit")
	if got := h.scheduleCount29(t, fix.contractID); got != len(fix.scheduleIDs)+len(want) {
		t.Errorf("schedules = %d, want %d", got, len(fix.scheduleIDs)+len(want))
	}

	// `period_amount` alone is a row error.
	bad := fix.owner.importPreview(t, "backfill", "bad.csv",
		"renter_phone,unit_code,until,mode,period_amount\n"+
			fix.renterPhone+","+fix.unitCodes[0]+","+until.Format(date29)+",paid,40000\n").
		mustStatus(t, http.StatusCreated, "preview bad")
	if rowError(t, importRows(t, bad), "2", "period_amount") == "" {
		t.Error("period_amount without from is not a row error")
	}
}

func backfillList29(t *testing.T, fix paymentFixture) []map[string]any {
	t.Helper()
	return listOf(t, fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID+"/backfills", nil).
		mustStatus(t, http.StatusOK, "list backfills"))
}

func withKey(m map[string]any, k string, v any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for key, val := range m {
		out[key] = val
	}
	out[k] = v
	return out
}
