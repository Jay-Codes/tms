package httpserver_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"tms/backend/internal/contract"
)

// Phase 29 introduced `from`; Phase 30 changed what it does: instead of
// inventing periods before the running contract's start, it records an
// OFFLINE CONTRACT for the same renter and unit. These tests keep the Phase 29
// scenarios (they are still the right ones) with the Phase 30 expectations;
// phase30_test.go covers the rest of the offline contract.
//
// The fixture is a fresh contract (starting about today), the shape a landlord
// onboarding a long-standing renter actually has.

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
// history at an older rent become an offline contract, every period paid, the
// running contract untouched, and the undo removes the lot.
func TestPhase29BackfillFromCreatesAndSettlesHistory(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "History", "0729000100", "+255729000101")
	c := h.contractHistory29(t, fix.contractID)
	before := h.scheduleCount29(t, fix.contractID)

	from := c.start.AddDate(0, 0, -90)
	until := c.start.AddDate(0, 0, -1)
	const oldRent = 60_000
	want := contract.Generate(oldRent, c.cadence, 90, c.cadence, from, c.dueDay)
	var wantTotal int64
	for _, r := range want {
		wantTotal += r.Amount
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
	if got := int(mustFloat(t, dry.Body, "settled")); got != len(want) {
		t.Errorf("dry run settled = %d, want %d", got, len(want))
	}
	if got := len(h.offlineContracts30(t, fix.unitIDs[0])); got != 0 {
		t.Fatalf("offline contracts after a dry run = %d, want 0 (nothing written)", got)
	}
	if got := len(backfillList29(t, fix)); got != 0 {
		t.Fatalf("backfills after a dry run = %d, want 0", got)
	}

	done := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", body).
		mustStatus(t, http.StatusOK, "backfill from")
	if got := int(mustFloat(t, done.Body, "created")); got != len(want) {
		t.Errorf("created = %d, want %d", got, len(want))
	}
	if got := int(mustFloat(t, done.Body, "settled")); got != len(want) {
		t.Errorf("settled = %d, want %d", got, len(want))
	}
	if got := int64(mustFloat(t, done.Body, "total")); got != wantTotal {
		t.Errorf("total = %d, want %d at the old rent", got, wantTotal)
	}
	// The running contract's own book is exactly what it was.
	if got := h.scheduleCount29(t, fix.contractID); got != before {
		t.Errorf("running contract schedules = %d, want %d (untouched)", got, before)
	}

	offs := h.offlineContracts30(t, fix.unitIDs[0])
	if len(offs) != 1 {
		t.Fatalf("offline contracts = %d, want 1", len(offs))
	}
	off := offs[0]
	if off.id != done.str(t, "offline_contract_id") {
		t.Errorf("offline_contract_id = %s, want %s", done.str(t, "offline_contract_id"), off.id)
	}

	// Its periods, first to last: from `from`, every one paid.
	rows := listOf(t, fix.owner.do(http.MethodGet, "/contracts/"+off.id+"/schedules", nil).
		mustStatus(t, http.StatusOK, "offline schedules"))
	if len(rows) != len(want) {
		t.Fatalf("offline periods = %d, want %d", len(rows), len(want))
	}
	for i, r := range want {
		got := rows[i]
		if got["period_start"] != r.PeriodStart.Format(date29) {
			t.Errorf("row %d period_start = %v, want %s", i, got["period_start"], r.PeriodStart.Format(date29))
		}
		if got["status"] != "paid" {
			t.Errorf("row %d status = %v, want paid", i, got["status"])
		}
	}

	// The running contract's document lists its own terms only.
	doc := fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID+"/document", nil).
		mustStatus(t, http.StatusOK, "document")
	if sched, _ := doc.Body["schedule"].([]any); len(sched) != before {
		t.Errorf("document schedule rows = %d, want %d", len(sched), before)
	}

	item := backfillList29(t, fix)[0]
	if item["from"] != from.Format(date29) || int(mustFloat(t, item, "created_periods")) != len(want) ||
		item["created_contract_id"] != off.id || item["offline_end"] != until.Format(date29) {
		t.Errorf("list item = %v, want from, created_periods, created_contract_id, offline_end", item)
	}

	// The same stretch again would overlap the contract just recorded.
	again := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", body)
	if again.Code != http.StatusConflict || again.Body["type"] != "offline_overlap" {
		t.Errorf("second from = %d %v, want 409 offline_overlap", again.Code, again.Body["type"])
	}

	undone := fix.owner.do(http.MethodPost, "/backfills/"+done.str(t, "backfill_id")+"/undo",
		map[string]any{"reason": "wrong move-in date"}).mustStatus(t, http.StatusOK, "undo")
	if got := int(mustFloat(t, undone.Body, "periods_removed")); got != len(want) {
		t.Errorf("periods_removed = %d, want %d", got, len(want))
	}
	if undone.Body["contract_removed"] != true {
		t.Errorf("contract_removed = %v, want true", undone.Body["contract_removed"])
	}
	if got := len(h.offlineContracts30(t, fix.unitIDs[0])); got != 0 {
		t.Errorf("offline contracts after the undo = %d, want 0", got)
	}
	if got := h.scheduleCount29(t, fix.contractID); got != before {
		t.Errorf("schedules after the undo = %d, want %d", got, before)
	}
	// With the contract gone, the same `from` is allowed again.
	fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", body).
		mustStatus(t, http.StatusOK, "backfill from again after the undo")
}

// TestPhase29UntilAloneStillSettlesTheRunningContract: a call without `from`
// is the Phase 20 settlement, and stretches that touch each other are fine.
func TestPhase29UntilAloneStillSettlesTheRunningContract(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "HistoryLater", "0729000200", "+255729000201")
	c := h.contractHistory29(t, fix.contractID)
	from := c.start.AddDate(0, 0, -90)

	// Two stretches that meet: the first ends where the second begins.
	fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", map[string]any{
		"until": from.AddDate(0, 0, 29).Format(date29), "mode": "paid", "from": from.Format(date29),
		"period_amount": 50_000,
	}).mustStatus(t, http.StatusOK, "first stretch")
	fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", map[string]any{
		"until": c.start.AddDate(0, 0, -1).Format(date29), "mode": "paid",
		"from": from.AddDate(0, 0, 30).Format(date29), "period_amount": 50_000,
	}).mustStatus(t, http.StatusOK, "second stretch, adjacent")
	if got := len(h.offlineContracts30(t, fix.unitIDs[0])); got != 2 {
		t.Errorf("offline contracts = %d, want 2", got)
	}

	// A stretch that overlaps one of them is a 409 naming it.
	clash := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", map[string]any{
		"until": from.AddDate(0, 0, 40).Format(date29), "mode": "paid",
		"from": from.AddDate(0, 0, 20).Format(date29), "period_amount": 50_000,
	})
	if clash.Code != http.StatusConflict || clash.Body["type"] != "offline_overlap" {
		t.Errorf("overlapping from = %d %v, want 409 offline_overlap", clash.Code, clash.Body["type"])
	}

	// No `from`: the running contract's periods, as ever.
	got := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", map[string]any{
		"until": c.start.Format(date29), "mode": "paid",
	}).mustStatus(t, http.StatusOK, "until alone")
	if int(mustFloat(t, got.Body, "created")) != 0 || got.Body["offline_contract_id"] != nil {
		t.Errorf("until alone created something: %v", got.Body)
	}

	early := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", map[string]any{
		"until": from.AddDate(0, 0, -1).Format(date29), "mode": "paid",
	})
	if early.Code != http.StatusUnprocessableEntity {
		t.Errorf("until before the contract's start, no from = %d, want 422", early.Code)
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
	if got := len(h.offlineContracts30(t, fix.unitIDs[0])); got != 0 {
		t.Errorf("offline contracts after refusals = %d, want 0", got)
	}
}

// TestPhase29UndoRefusedWhenTheOfflineContractIsTouched: an edit to the
// offline contract's periods made after the backfill blocks the undo — taking
// the contract away would discard that edit.
func TestPhase29UndoRefusedWhenTheOfflineContractIsTouched(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "HistoryTouched", "0729000400", "+255729000401")
	c := h.contractHistory29(t, fix.contractID)
	from := c.start.AddDate(0, 0, -90)

	done := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", map[string]any{
		"until": c.start.AddDate(0, 0, -1).Format(date29), "mode": "paid", "from": from.Format(date29),
		"period_amount": 45_000,
	}).mustStatus(t, http.StatusOK, "backfill the stretch")
	off := h.offlineContracts30(t, fix.unitIDs[0])[0]

	if _, err := h.pool.Exec(context.Background(),
		`UPDATE payment_schedules SET amount = amount + 1
		 WHERE id = (SELECT id FROM payment_schedules WHERE contract_id = $1 ORDER BY period_start LIMIT 1)`,
		off.id); err != nil {
		t.Fatalf("edit an offline period: %v", err)
	}

	refused := fix.owner.do(http.MethodPost, "/backfills/"+done.str(t, "backfill_id")+"/undo",
		map[string]any{"reason": "try"})
	if refused.Code != http.StatusConflict || refused.Body["type"] != "touched_since" {
		t.Fatalf("undo = %d %v, want 409 touched_since", refused.Code, refused.Body["type"])
	}
	if item := backfillList29(t, fix)[0]; item["touched"] != true || item["can_undo"] != false {
		t.Errorf("list item = %v, want touched and not undoable", item)
	}
}

// TestPhase29BackfillImportFrom: the CSV's `from` and `period_amount` columns
// preview the offline contract's periods and commit them.
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
	offs := h.offlineContracts30(t, fix.unitIDs[0])
	if len(offs) != 1 {
		t.Fatalf("offline contracts = %d, want 1", len(offs))
	}
	if got := h.scheduleCount29(t, fix.contractID); got != len(fix.scheduleIDs) {
		t.Errorf("running contract schedules = %d, want %d", got, len(fix.scheduleIDs))
	}
	if got := h.scheduleCount29(t, offs[0].id); got != len(want) {
		t.Errorf("offline schedules = %d, want %d", got, len(want))
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
