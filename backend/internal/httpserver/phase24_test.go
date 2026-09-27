package httpserver_test

import (
	"net/http"
	"testing"

	"tms/backend/internal/notify"
)

// Phase 24 — duplicates, replay, correction, and who is told.

func TestPhase24DuplicateGuardAndReplay(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Dup", "0725000100", "+255725000101")
	c := fix.owner
	body := map[string]any{"contract_id": fix.contractID, "amount": 100_000, "method": "cash", "reference": "RCPT-9"}

	first := c.withHeader("Idempotency-Key", "tap-1", func() response { return c.recordPayment(body) }).
		mustStatus(t, http.StatusCreated, "first")
	replay := c.withHeader("Idempotency-Key", "tap-1", func() response { return c.recordPayment(body) }).
		mustStatus(t, http.StatusOK, "replay")
	if replay.str(t, "payment", "id") != first.str(t, "payment", "id") {
		t.Error("replay wrote a second payment")
	}

	dup := c.recordPayment(body)
	if dup.Code != http.StatusConflict || dup.str(t, "type") != "possible_duplicate" {
		t.Fatalf("duplicate = %d %s", dup.Code, dup.Raw)
	}
	if m, _ := dup.Body["matches"].([]any); len(m) != 1 {
		t.Errorf("matches = %v", dup.Body["matches"])
	}
	body["confirm_duplicate"] = true
	c.recordPayment(body).mustStatus(t, http.StatusCreated, "confirmed duplicate")
	inbox := listOf(t, c.do(http.MethodGet, "/inbox", nil).mustStatus(t, http.StatusOK, "inbox"))
	if len(inbox) != 1 || inbox[0]["kind"] != "payment_duplicate_confirmed" {
		t.Errorf("inbox = %v", inbox)
	}
	if got := mustFloat(t, c.do(http.MethodGet, "/inbox/unread", nil).Body, "unread"); got != 1 {
		t.Errorf("unread = %v", got)
	}
	c.do(http.MethodPost, "/inbox/read", map[string]any{"all": true}).mustStatus(t, http.StatusOK, "read all")
	if got := mustFloat(t, c.do(http.MethodGet, "/inbox/unread", nil).Body, "unread"); got != 0 {
		t.Errorf("unread after read = %v", got)
	}
}

func TestPhase24CorrectAPayment(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Correct", "0725000200", "+255725000201")
	c := fix.owner
	typo := c.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": 25_000, "method": "cash",
	}).mustStatus(t, http.StatusCreated, "typo").str(t, "payment", "id")

	c.do(http.MethodPost, "/payments/"+typo+"/correct", map[string]any{"amount": 250_000}).
		mustStatus(t, http.StatusBadRequest, "no reason")
	fixed := c.do(http.MethodPost, "/payments/"+typo+"/correct", map[string]any{
		"amount": 250_000, "reason": "missed a zero",
	}).mustStatus(t, http.StatusCreated, "correct")
	if got := fixed.str(t, "reversed", "status"); got != "reversed" {
		t.Errorf("original status = %s", got)
	}
	if got := mustFloat(t, fixed.Body["payment"].(map[string]any), "amount"); got != 250_000 {
		t.Errorf("new amount = %v", got)
	}
	rows := scheduleRows(t, c, fix.contractID)
	if got := mustFloat(t, rows[0], "paid_amount"); got != 250_000 {
		t.Errorf("period paid = %v, want 250000 (not 275000)", got)
	}
	if n := len(ofKind(h.notifications(t), notify.KindPaymentCorrected)); n != 1 {
		t.Errorf("payment_corrected SMS = %d", n)
	}
	c.do(http.MethodPost, "/payments/"+typo+"/correct", map[string]any{"reason": "again"}).
		mustStatus(t, http.StatusConflict, "correct a reversed payment")

	// A plain reversal texts the renter and rings the bell.
	newID := fixed.str(t, "payment", "id")
	c.do(http.MethodPost, "/payments/"+newID+"/reverse", map[string]any{"reason": "bounced"}).
		mustStatus(t, http.StatusOK, "reverse")
	if n := len(ofKind(h.notifications(t), notify.KindPaymentReversed)); n != 1 {
		t.Errorf("payment_reversed SMS = %d", n)
	}
	kinds := map[string]int{}
	for _, it := range listOf(t, c.do(http.MethodGet, "/inbox", nil)) {
		kinds[it["kind"].(string)]++
	}
	if kinds["payment_corrected"] != 1 || kinds["payment_reversed"] != 1 {
		t.Errorf("inbox kinds = %v", kinds)
	}
}
