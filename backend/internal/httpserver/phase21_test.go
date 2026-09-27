package httpserver_test

import (
	"net/http"
	"testing"
	"time"
)

// Phase 21 §21.2 — arrears after a tenancy has closed. The assertions are about
// the book: money still lands on the rows the renter lived through, the board
// lists who owes, and a write-off moves the debt out of `outstanding` without
// pretending it was never owed.

// closedWithArrears is a tenancy a year behind, terminated today: every row it
// lived through still owes.
func (h *harness) closedWithArrears(t *testing.T, tag, ownerPhone, renterPhone string) backfilled {
	t.Helper()
	fix := h.newBackfillFixture(t, tag, ownerPhone, renterPhone)
	fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/terminate", map[string]any{
		"reason": "moved out owing rent", "effective_date": time.Now().UTC().Format("2006-01-02"),
	}).mustStatus(t, http.StatusOK, "terminate")
	return fix
}

func arrearsOutstanding(t *testing.T, c *client) (int64, []map[string]any) {
	t.Helper()
	r := c.do(http.MethodGet, "/arrears", nil).mustStatus(t, http.StatusOK, "arrears")
	total, _ := r.Body["total"].(map[string]any)
	out, _ := total["outstanding"].(float64)
	return int64(out), listOf(t, r)
}

func TestPhase21PaymentLandsOnAClosedTenancy(t *testing.T) {
	h := newHarness(t)
	fix := h.closedWithArrears(t, "Arrears", "0721000100", "+255721000101")

	before, items := arrearsOutstanding(t, fix.owner)
	if len(items) != 1 || before <= 0 {
		t.Fatalf("arrears board = %d items, outstanding %d; want the terminated tenancy", len(items), before)
	}
	if got, _ := items[0]["contract_status"].(string); got != "terminated" {
		t.Errorf("contract_status = %q, want terminated", got)
	}

	// Three weeks later the former tenant pays part of it.
	part := fix.amounts[0] / 2
	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": part, "method": "cash",
	}).mustStatus(t, http.StatusCreated, "payment on a terminated contract")

	after, _ := arrearsOutstanding(t, fix.owner)
	if after != before-part {
		t.Errorf("outstanding after payment = %d, want %d", after, before-part)
	}

	// More than is owed is still refused: there is no future to roll into.
	over := fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": after + 1, "method": "cash",
		"allow_overpay_rollover": true,
	})
	if over.Code/100 != 4 {
		t.Errorf("overpayment on a closed tenancy = %d, want a refusal — body: %s", over.Code, over.Raw)
	}
}

func TestPhase21WriteOffAndUndo(t *testing.T) {
	h := newHarness(t)
	fix := h.closedWithArrears(t, "WriteOff", "0721000200", "+255721000201")
	before, _ := arrearsOutstanding(t, fix.owner)

	path := "/contracts/" + fix.contractID + "/write-off"
	fix.owner.do(http.MethodPost, path, map[string]any{}).
		mustStatus(t, http.StatusBadRequest, "write-off without a reason")

	done := fix.owner.do(http.MethodPost, path, map[string]any{"reason": "tenant untraceable"}).
		mustStatus(t, http.StatusOK, "write-off")
	if got := int64(mustFloat(t, done.Body, "amount")); got != before {
		t.Errorf("written off = %d, want the whole %d", got, before)
	}
	for i, s := range fix.statuses(t) {
		if s != "written_off" && s != "waived" && s != "paid" {
			t.Errorf("schedule %d status = %q after write-off", i, s)
		}
	}
	if out, items := arrearsOutstanding(t, fix.owner); out != 0 || len(items) != 0 {
		t.Errorf("arrears after write-off = %d over %d items, want none", out, len(items))
	}
	if rows := h.auditPayloads(t, "contract.write_off"); len(rows) != 1 {
		t.Errorf("contract.write_off audit rows = %d, want 1", len(rows))
	}

	// Nothing is left to pay on, so money is refused until the write-off is undone.
	if r := fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": 1000, "method": "cash",
	}); r.Code/100 != 4 {
		t.Errorf("payment on a written-off book = %d, want a refusal", r.Code)
	}
	fix.owner.do(http.MethodPost, path, map[string]any{"reason": "again"}).
		mustStatus(t, http.StatusConflict, "second write-off")

	fix.owner.do(http.MethodPost, path+"/undo", nil).mustStatus(t, http.StatusOK, "undo")
	if out, _ := arrearsOutstanding(t, fix.owner); out != before {
		t.Errorf("arrears after undo = %d, want %d back", out, before)
	}
	fix.owner.do(http.MethodPost, path+"/undo", nil).
		mustStatus(t, http.StatusConflict, "second undo")
}

func TestPhase21WriteOffRefusedOnARunningTenancy(t *testing.T) {
	h := newHarness(t)
	fix := h.newBackfillFixture(t, "WriteOffLive", "0721000300", "+255721000301")
	r := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/write-off",
		map[string]any{"reason": "too early"})
	if r.Code != http.StatusConflict || r.str(t, "type") != "contract_not_closed" {
		t.Errorf("write-off on a running contract = %d %s, want 409 contract_not_closed", r.Code, r.Raw)
	}
}
