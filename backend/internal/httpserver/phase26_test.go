package httpserver_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"tms/backend/internal/notify"
)

// Phase 26 — a backfill undone as a whole, and a backfill by CSV.
//
// As in Phase 20 the assertions are about the rent book: which rows reopened,
// which payments stand reversed, and what the list says — not merely that the
// endpoint answered 200.

// backfills reads the contract page's "Backfills" list.
func (f backfilled) backfills(t *testing.T) []map[string]any {
	t.Helper()
	return listOf(t, f.owner.do(http.MethodGet, "/contracts/"+f.contractID+"/backfills", nil).
		mustStatus(t, http.StatusOK, "list backfills"))
}

func today26() string { return time.Now().UTC().Format("2006-01-02") }

// TestPhase26UndoPaidBackfill: every payment the batch wrote is reversed
// through the ordinary path, every period is owed again, and nobody is texted.
func TestPhase26UndoPaidBackfill(t *testing.T) {
	h := newHarness(t)
	fix := h.newBackfillFixture(t, "UndoPaid", "0726000100", "+255726000101")

	done := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill",
		map[string]any{"until": today26(), "mode": "paid"}).
		mustStatus(t, http.StatusOK, "backfill paid")
	id := done.str(t, "backfill_id")

	list := fix.backfills(t)
	if len(list) != 1 {
		t.Fatalf("backfills = %d, want 1", len(list))
	}
	var want int64
	for _, a := range fix.amounts {
		want += a
	}
	item := list[0]
	if item["id"] != id || item["mode"] != "paid" || item["can_undo"] != true {
		t.Errorf("list item = %v, want the batch, paid, undoable", item)
	}
	if got := int(mustFloat(t, item, "periods")); got != fix.unpaid {
		t.Errorf("periods = %d, want %d", got, fix.unpaid)
	}
	if got := int64(mustFloat(t, item, "amount")); got != want {
		t.Errorf("amount = %d, want %d", got, want)
	}
	if by, _ := item["created_by"].(map[string]any); by == nil || by["name"] == "" {
		t.Errorf("created_by = %v, want the owner", item["created_by"])
	}

	fix.owner.do(http.MethodPost, "/backfills/"+id+"/undo", map[string]any{}).
		mustStatus(t, http.StatusBadRequest, "undo without a reason")

	undone := fix.owner.do(http.MethodPost, "/backfills/"+id+"/undo",
		map[string]any{"reason": "wrong tenant's book"}).
		mustStatus(t, http.StatusOK, "undo the backfill")
	if got := int(mustFloat(t, undone.Body, "payments_reversed")); got != fix.unpaid {
		t.Errorf("payments_reversed = %d, want %d", got, fix.unpaid)
	}
	for i, status := range fix.statuses(t) {
		if status != "overdue" {
			t.Errorf("schedule %d status = %q, want overdue again", i, status)
		}
	}
	for _, p := range listOf(t, fix.owner.do(http.MethodGet,
		"/payments?contract_id="+fix.contractID+"&source=backfill", nil).
		mustStatus(t, http.StatusOK, "backfill payments")) {
		if p["status"] != "reversed" {
			t.Errorf("backfill payment status = %v, want reversed", p["status"])
		}
	}

	after := fix.backfills(t)[0]
	if after["undone_at"] == nil || after["can_undo"] != false || after["undo_reason"] != "wrong tenant's book" {
		t.Errorf("list item after undo = %v", after)
	}

	again := fix.owner.do(http.MethodPost, "/backfills/"+id+"/undo", map[string]any{"reason": "twice"})
	if again.Code != http.StatusConflict || again.Body["type"] != "already_undone" {
		t.Errorf("second undo = %d %v, want 409 already_undone", again.Code, again.Body["type"])
	}

	if rows := h.auditPayloads(t, "backfill.undo"); len(rows) != 1 {
		t.Errorf("backfill.undo audit rows = %d, want 1", len(rows))
	}
	if rows := h.auditPayloads(t, "payment.reverse"); len(rows) != fix.unpaid {
		t.Errorf("payment.reverse audit rows = %d, want one per payment (%d)", len(rows), fix.unpaid)
	}
	// No SMS for an undo (DECISIONS, Phase 26).
	if sent := ofKind(h.notifications(t), notify.KindPaymentReversed); len(sent) != 0 {
		t.Errorf("payment_reversed messages = %d, want 0", len(sent))
	}

	// The book can be backfilled again, as a new batch.
	fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill",
		map[string]any{"until": today26(), "mode": "paid"}).
		mustStatus(t, http.StatusOK, "backfill again after the undo")
	if got := len(fix.backfills(t)); got != 2 {
		t.Errorf("backfills after a second run = %d, want 2", got)
	}
}

// TestPhase26UndoWaivedBackfill: the case Phase 20 could not undo at all.
func TestPhase26UndoWaivedBackfill(t *testing.T) {
	h := newHarness(t)
	fix := h.newBackfillFixture(t, "UndoWaive", "0726000200", "+255726000201")

	// Half of the first period was really paid; the waiver closes the rest.
	part := fix.amounts[0] / 2
	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "schedule_id": fix.scheduleIDs[0],
		"amount": part, "method": "cash",
	}).mustStatus(t, http.StatusCreated, "half the first period")

	done := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill",
		map[string]any{"until": today26(), "mode": "waived", "note": "forgiven"}).
		mustStatus(t, http.StatusOK, "backfill waived")
	id := done.str(t, "backfill_id")
	for i, status := range fix.statuses(t) {
		if status != "waived" {
			t.Fatalf("schedule %d status = %q, want waived", i, status)
		}
	}

	undone := fix.owner.do(http.MethodPost, "/backfills/"+id+"/undo",
		map[string]any{"reason": "not forgiven after all"}).
		mustStatus(t, http.StatusOK, "undo the waiver")
	if got := int(mustFloat(t, undone.Body, "periods_reopened")); got != fix.unpaid {
		t.Errorf("periods_reopened = %d, want %d", got, fix.unpaid)
	}
	if got := int(mustFloat(t, undone.Body, "payments_reversed")); got != 0 {
		t.Errorf("payments_reversed = %d, want 0 on a waiver", got)
	}
	statuses := fix.statuses(t)
	// The status is recomputed: the half-paid row is past due, so overdue —
	// and the real cash on it is untouched.
	for i, status := range statuses {
		if status != "overdue" {
			t.Errorf("schedule %d status = %q, want overdue", i, status)
		}
	}
	manual := listOf(t, fix.owner.do(http.MethodGet,
		"/payments?contract_id="+fix.contractID+"&source=manual", nil).
		mustStatus(t, http.StatusOK, "manual payments"))
	if len(manual) != 1 || manual[0]["status"] != "recorded" {
		t.Errorf("the real cash payment = %v, want it still recorded", manual)
	}
}

// TestPhase26UndoRefusedWhenTouched: once other money reaches a period the
// batch settled, the undo would pull the floor from under it.
func TestPhase26UndoRefusedWhenTouched(t *testing.T) {
	h := newHarness(t)
	fix := h.newBackfillFixture(t, "UndoTouched", "0726000300", "+255726000301")

	done := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill",
		map[string]any{"until": today26(), "mode": "paid"}).
		mustStatus(t, http.StatusOK, "backfill paid")
	id := done.str(t, "backfill_id")

	// One backfilled payment reversed by hand, and real money recorded on the
	// period it reopened.
	payments := listOf(t, fix.owner.do(http.MethodGet,
		"/payments?contract_id="+fix.contractID+"&source=backfill", nil).
		mustStatus(t, http.StatusOK, "backfill payments"))
	var first string
	for _, p := range payments {
		if p["schedule_id"] == fix.scheduleIDs[0] {
			first, _ = p["id"].(string)
		}
	}
	if first == "" {
		t.Fatalf("no backfill payment on the first period: %v", payments)
	}
	fix.owner.do(http.MethodPost, "/payments/"+first+"/reverse", map[string]any{"reason": "typo"}).
		mustStatus(t, http.StatusOK, "reverse one by hand")
	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "schedule_id": fix.scheduleIDs[0],
		"amount": fix.amounts[0], "method": "bank_transfer",
	}).mustStatus(t, http.StatusCreated, "real money on the reopened period")

	item := fix.backfills(t)[0]
	if item["touched"] != true || item["can_undo"] != false {
		t.Errorf("list item = %v, want touched and not undoable", item)
	}
	refused := fix.owner.do(http.MethodPost, "/backfills/"+id+"/undo", map[string]any{"reason": "try"})
	if refused.Code != http.StatusConflict || refused.Body["type"] != "touched_since" {
		t.Fatalf("undo of a touched batch = %d %v, want 409 touched_since", refused.Code, refused.Body["type"])
	}
	// Nothing moved.
	for i, status := range fix.statuses(t) {
		if status != "paid" {
			t.Errorf("schedule %d status = %q after a refused undo, want paid", i, status)
		}
	}
}

// TestPhase26EmptyBackfillWritesNoBatch: a call that settles nothing is a
// landlord checking, and leaves no line with an Undo button behind.
func TestPhase26EmptyBackfillWritesNoBatch(t *testing.T) {
	h := newHarness(t)
	fix := h.newBackfillFixture(t, "UndoEmpty", "0726000400", "+255726000401")
	fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill",
		map[string]any{"until": today26(), "mode": "paid"}).
		mustStatus(t, http.StatusOK, "backfill")
	again := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill",
		map[string]any{"until": today26(), "mode": "paid"}).
		mustStatus(t, http.StatusOK, "backfill again")
	if again.Body["backfill_id"] != nil {
		t.Errorf("backfill_id = %v on a call that settled nothing, want null", again.Body["backfill_id"])
	}
	if got := len(fix.backfills(t)); got != 1 {
		t.Errorf("backfills = %d, want 1", got)
	}
}

// TestPhase26BackfillImport: the CSV kind previews "N periods · TZS X",
// refuses to commit around a bad line, commits one batch per line, and the
// import undo takes those batches back.
func TestPhase26BackfillImport(t *testing.T) {
	h := newHarness(t)
	fix := h.newBackfillFixture(t, "CSVBackfill", "0726000500", "+255726000501")
	code := fix.unitCodes[0]
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")

	tpl := fix.owner.do(http.MethodGet, "/imports/templates/backfill.csv", nil).
		mustStatus(t, http.StatusOK, "backfill template")
	if !strings.HasPrefix(tpl.Raw, "renter_phone,unit_code,until,mode,paid_at,method,reference,note") {
		t.Errorf("template header = %q", strings.SplitN(tpl.Raw, "\n", 2)[0])
	}

	// A file with bad lines: an unknown code, a waiver with no reason, a date
	// ahead, and the same tenancy twice.
	bad := fix.owner.importPreview(t, "backfill", "bad.csv",
		"renter_phone,unit_code,until,mode,note\n"+
			fix.renterPhone+","+code+","+today26()+",paid,\n"+
			fix.renterPhone+",NOSUCHCODE,"+today26()+",paid,\n"+
			fix.renterPhone+","+code+","+today26()+",waived,\n"+
			fix.renterPhone+","+code+","+tomorrow+",paid,\n"+
			fix.renterPhone+","+code+","+today26()+",paid,again\n").
		mustStatus(t, http.StatusCreated, "preview a bad file")
	rows := importRows(t, bad)
	if rows[2]["errors"] != nil {
		t.Errorf("line 2 errors = %v, want none", rows[2]["errors"])
	}
	if res, _ := rows[2]["resolved"].(map[string]any); res == nil ||
		int(mustFloat(t, res, "periods")) != fix.unpaid {
		t.Errorf("line 2 resolved = %v, want %d periods", rows[2]["resolved"], fix.unpaid)
	}
	if rowError(t, rows, "3", "unit_code") == "" {
		t.Error("an unknown unit code is not a row error")
	}
	if rowError(t, rows, "4", "note") == "" {
		t.Error("a waiver with no reason is not a row error")
	}
	if rowError(t, rows, "5", "until") == "" {
		t.Error("a date ahead is not a row error")
	}
	if rowError(t, rows, "6", "unit_code") == "" {
		t.Error("the same tenancy twice is not a row error")
	}
	// One bad line blocks the commit, skip_errors or not.
	refused := fix.owner.do(http.MethodPost, "/imports/"+bad.str(t, "batch", "id")+"/commit",
		map[string]any{"skip_errors": true})
	if refused.Code != http.StatusConflict || refused.Body["type"] != "batch_has_errors" {
		t.Fatalf("commit around bad lines = %d %v, want 409 batch_has_errors", refused.Code, refused.Body["type"])
	}

	good := fix.owner.importPreview(t, "backfill", "good.csv",
		"renter_phone,unit_code,until,mode,paid_at,method,reference,note\n"+
			fix.renterPhone+","+code+","+today26()+",paid,due_date,bank_transfer,BOOK-1,from the book\n").
		mustStatus(t, http.StatusCreated, "preview a good file")
	var want int64
	for _, a := range fix.amounts {
		want += a
	}
	res, _ := importRows(t, good)[2]["resolved"].(map[string]any)
	if res == nil || int64(mustFloat(t, res, "amount")) != want {
		t.Errorf("preview amount = %v, want %d", res, want)
	}
	batchID := good.str(t, "batch", "id")
	committed := fix.owner.do(http.MethodPost, "/imports/"+batchID+"/commit", nil).
		mustStatus(t, http.StatusOK, "commit the backfill sheet")
	if created, _ := committed.Body["created"].(map[string]any); created == nil ||
		int(mustFloat(t, created, "backfills")) != 1 {
		t.Errorf("created = %v, want one backfill", committed.Body["created"])
	}
	for i, status := range fix.statuses(t) {
		if status != "paid" {
			t.Errorf("schedule %d status = %q after the import, want paid", i, status)
		}
	}
	list := fix.backfills(t)
	if len(list) != 1 || list[0]["import_batch_id"] != batchID {
		t.Fatalf("backfills after the import = %v, want one naming the import", list)
	}
	for _, p := range listOf(t, fix.owner.do(http.MethodGet,
		"/payments?contract_id="+fix.contractID+"&source=backfill", nil).
		mustStatus(t, http.StatusOK, "backfill payments")) {
		if p["method"] != "bank_transfer" || p["reference"] != "BOOK-1" {
			t.Errorf("imported backfill payment = %v, want the sheet's method and reference", p)
		}
	}
	if sent := ofKind(h.notifications(t), notify.KindBackfillDone); len(sent) != 1 {
		t.Errorf("backfill_done messages = %d, want 1 per line", len(sent))
	}

	undone := fix.owner.do(http.MethodPost, "/imports/"+batchID+"/undo", nil).
		mustStatus(t, http.StatusOK, "undo the import")
	if counts, _ := undone.Body["undone"].(map[string]any); counts == nil ||
		int(mustFloat(t, counts, "backfills")) != 1 {
		t.Errorf("undone = %v, want one backfill", undone.Body["undone"])
	}
	for i, status := range fix.statuses(t) {
		if status != "overdue" {
			t.Errorf("schedule %d status = %q after the import undo, want overdue", i, status)
		}
	}
	if after := fix.backfills(t)[0]; after["undone_at"] == nil {
		t.Errorf("the imported batch = %v, want it undone", after)
	}
}

// TestPhase26BackfillsAreOrgScoped: org B can neither list nor undo org A's
// backfills.
func TestPhase26BackfillsAreOrgScoped(t *testing.T) {
	h := newHarness(t)
	fix := h.newBackfillFixture(t, "UndoScope", "0726000600", "+255726000601")
	id := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill",
		map[string]any{"until": today26(), "mode": "paid"}).
		mustStatus(t, http.StatusOK, "backfill").str(t, "backfill_id")

	other, _ := h.createOrg("Undo Scope Beta", "Beta Owner", "undo-scope-beta@jjne.test",
		"0726000610", "supersecret")
	other.do(http.MethodGet, "/contracts/"+fix.contractID+"/backfills", nil).
		mustStatus(t, http.StatusNotFound, "org B lists org A's backfills")
	other.do(http.MethodPost, "/backfills/"+id+"/undo", map[string]any{"reason": "hijack"}).
		mustStatus(t, http.StatusNotFound, "org B undoes org A's backfill")
	if item := fix.backfills(t)[0]; item["undone_at"] != nil {
		t.Errorf("org A's batch = %v, want it untouched", item)
	}
}
