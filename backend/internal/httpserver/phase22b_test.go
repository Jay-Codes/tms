package httpserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"tms/backend/internal/notify"
)

// Phase 22 §22.5 (rest) — period relief, notice to leave, holdover, eviction.

func TestPhase22PeriodReliefWaiveDiscountUndo(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Relief", "0723000100", "+255723000101")
	c := fix.owner
	c.do(http.MethodPost, "/schedules/"+fix.scheduleIDs[0]+"/adjust", map[string]any{"kind": "waive"}).
		mustStatus(t, http.StatusBadRequest, "waive without reason")
	w := c.do(http.MethodPost, "/schedules/"+fix.scheduleIDs[0]+"/adjust", map[string]any{
		"kind": "waive", "reason": "roof repair month",
	}).mustStatus(t, http.StatusOK, "waive")
	if got := w.str(t, "schedule", "status"); got != "waived" {
		t.Errorf("waived status = %s", got)
	}
	d := c.do(http.MethodPost, "/schedules/"+fix.scheduleIDs[1]+"/adjust", map[string]any{
		"kind": "discount", "discount": 50_000, "reason": "loyalty",
	}).mustStatus(t, http.StatusOK, "discount")
	if got := int64(mustFloat(t, d.Body["schedule"].(map[string]any), "amount")); got != fix.amounts[1]-50_000 {
		t.Errorf("discounted amount = %d", got)
	}
	again := c.do(http.MethodPost, "/schedules/"+fix.scheduleIDs[1]+"/adjust", map[string]any{
		"kind": "discount", "discount": 1_000, "reason": "twice",
	})
	if again.Code != http.StatusConflict {
		t.Errorf("second adjustment = %d, want 409", again.Code)
	}
	u := c.do(http.MethodPost, "/schedules/"+fix.scheduleIDs[1]+"/adjust/undo", nil).mustStatus(t, http.StatusOK, "undo")
	if got := int64(mustFloat(t, u.Body["schedule"].(map[string]any), "amount")); got != fix.amounts[1] {
		t.Errorf("amount after undo = %d, want %d", got, fix.amounts[1])
	}
	c.do(http.MethodPost, "/schedules/"+fix.scheduleIDs[0]+"/adjust/undo", nil).mustStatus(t, http.StatusOK, "undo waive")
	if got := fix.statuses(t)[0]; got == "waived" {
		t.Errorf("status after undoing the waiver = %s", got)
	}
}

func TestPhase22NoticeToLeave(t *testing.T) {
	h := newHarness(t)
	tn := h.policyTenancy(t, "Notice", "+255723000201", map[string]any{
		"move_out_proration": "pro_rata", "early_exit_prepaid": "refund",
		"deposit_mode": "none", "tenant_notice_days": 30, "eviction_notice_days": 14,
	})
	path := "/me/contracts/" + tn.contractID + "/notice"
	short := tn.renter.do(http.MethodPost, path, map[string]any{
		"leave_on": time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02"),
	})
	if short.Code != http.StatusUnprocessableEntity || short.str(t, "type") != "notice_too_short" {
		t.Fatalf("short notice = %d %s", short.Code, short.Raw)
	}
	leave := time.Now().UTC().AddDate(0, 0, 45).Format("2006-01-02")
	given := tn.renter.do(http.MethodPost, path, map[string]any{"leave_on": leave, "reason": "new job"}).
		mustStatus(t, http.StatusOK, "notice")
	if got := given.str(t, "contract", "notice_leave_on"); got != leave {
		t.Errorf("notice_leave_on = %s, want %s", got, leave)
	}
	if n := len(ofKind(h.notifications(t), notify.KindNoticeReceived)); n != 1 {
		t.Errorf("notice_received messages = %d, want 1", n)
	}
	if got := tn.owner.do(http.MethodGet, "/contracts/"+tn.contractID, nil).str(t, "contract", "notice_leave_on"); got != leave {
		t.Errorf("landlord sees notice_leave_on = %s", got)
	}
	tn.owner.do(http.MethodDelete, "/contracts/"+tn.contractID+"/notice", nil).mustStatus(t, http.StatusOK, "withdraw")
	tn.owner.do(http.MethodDelete, "/contracts/"+tn.contractID+"/notice", nil).
		mustStatus(t, http.StatusConflict, "withdraw twice")
}

// endContract makes a running contract run out yesterday.
func (h *harness) endContract(t *testing.T, id string) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE contracts SET status = 'ended', end_date = CURRENT_DATE - 1 WHERE id = $1`, id); err != nil {
		t.Fatalf("end contract: %v", err)
	}
}

func TestPhase22HoldoverConfirmOrRenew(t *testing.T) {
	h := newHarness(t)
	gone := h.newPaymentFixture(t, "HoldGone", "0723000300", "+255723000301")
	h.endContract(t, gone.contractID)
	list := listOf(t, gone.owner.do(http.MethodGet, "/holdovers", nil).mustStatus(t, http.StatusOK, "holdovers"))
	if len(list) != 1 {
		t.Fatalf("holdovers = %d, want 1", len(list))
	}
	gone.owner.do(http.MethodPost, "/contracts/"+gone.contractID+"/moved-out", nil).mustStatus(t, http.StatusOK, "moved out")
	if list := listOf(t, gone.owner.do(http.MethodGet, "/holdovers", nil)); len(list) != 0 {
		t.Errorf("holdovers after confirming = %d", len(list))
	}

	stay := h.newPaymentFixture(t, "HoldStay", "0723000400", "+255723000401")
	h.endContract(t, stay.contractID)
	end := stay.owner.do(http.MethodGet, "/contracts/"+stay.contractID, nil).str(t, "contract", "end_date")
	renewal := stay.owner.do(http.MethodPost, "/contracts/"+stay.contractID+"/amend", map[string]any{
		"effective_date": end, "reason": "still living there",
	}).mustStatus(t, http.StatusCreated, "renew ended").str(t, "contract", "id")
	h.signAsRenter(t, stay.renter, renewal, stay.renterPhone)
	stay.owner.do(http.MethodPost, "/contracts/"+renewal+"/activate", nil).mustStatus(t, http.StatusOK, "activate renewal")
	if got := stay.owner.do(http.MethodGet, "/contracts/"+stay.contractID, nil).str(t, "contract", "superseded_by_contract_id"); got != renewal {
		t.Errorf("superseded_by = %s, want %s", got, renewal)
	}
	if list := listOf(t, stay.owner.do(http.MethodGet, "/holdovers", nil)); len(list) != 0 {
		t.Errorf("holdovers after renewal = %d", len(list))
	}
}

func TestPhase22EvictionStages(t *testing.T) {
	h := newHarness(t)
	fix := h.newBackfillFixture(t, "Evict", "0723000500", "+255723000501")
	c := fix.owner
	open := c.do(http.MethodPost, "/contracts/"+fix.contractID+"/eviction", nil).mustStatus(t, http.StatusCreated, "demand")
	caseID := open.str(t, "eviction", "id")
	if got := open.str(t, "eviction", "stage"); got != "demand" {
		t.Errorf("stage = %s", got)
	}
	if n := len(ofKind(h.notifications(t), notify.KindEvictionDemand)); n != 1 {
		t.Errorf("eviction_demand messages = %d", n)
	}
	dup := c.do(http.MethodPost, "/contracts/"+fix.contractID+"/eviction", nil)
	if dup.Code != http.StatusConflict || dup.str(t, "type") != "eviction_open" {
		t.Errorf("second case = %d %s", dup.Code, dup.Raw)
	}
	letter := c.do(http.MethodGet, "/evictions/"+caseID+"/letter?kind=demand&lang=sw", nil).mustStatus(t, http.StatusOK, "letter")
	if html := letter.str(t, "html"); !strings.Contains(html, "Madai") || !strings.Contains(html, "Evict Renter") {
		t.Errorf("demand letter = %s", html)
	}
	c.do(http.MethodGet, "/evictions/"+caseID+"/letter?kind=notice", nil).mustStatus(t, http.StatusConflict, "notice letter before notice")

	notice := c.do(http.MethodPost, "/evictions/"+caseID+"/notice", nil).mustStatus(t, http.StatusOK, "notice")
	if notice.str(t, "eviction", "vacate_by") == "" {
		t.Error("no vacate_by on the notice")
	}
	en := c.do(http.MethodGet, "/evictions/"+caseID+"/letter?kind=notice&lang=en", nil).mustStatus(t, http.StatusOK, "notice letter")
	if !strings.Contains(en.str(t, "html"), "Notice to vacate") {
		t.Error("notice letter lacks its title")
	}
	if list := listOf(t, c.do(http.MethodGet, "/evictions", nil).mustStatus(t, http.StatusOK, "open cases")); len(list) != 1 {
		t.Errorf("open cases = %d", len(list))
	}

	c.do(http.MethodPost, "/evictions/"+caseID+"/withdraw", map[string]any{"reason": "paid in full"}).
		mustStatus(t, http.StatusOK, "withdraw")
	second := c.do(http.MethodPost, "/contracts/"+fix.contractID+"/eviction", nil).mustStatus(t, http.StatusCreated, "reopen").
		str(t, "eviction", "id")
	c.do(http.MethodPost, "/contracts/"+fix.contractID+"/terminate", map[string]any{"reason": "evicted"}).
		mustStatus(t, http.StatusOK, "terminate")
	ev := c.do(http.MethodGet, "/contracts/"+fix.contractID+"/eviction", nil).mustStatus(t, http.StatusOK, "cases")
	if ev.Body["open"] != nil {
		t.Error("a case is still open after termination")
	}
	var closed string
	for _, e := range ev.Body["history"].([]any) {
		m := e.(map[string]any)
		if m["id"] == second {
			closed, _ = m["stage"].(string)
		}
	}
	if closed != "vacated" {
		t.Errorf("case after termination = %q, want vacated", closed)
	}

	clean := h.newPaymentFixture(t, "EvictNone", "0723000600", "+255723000601")
	r := clean.owner.do(http.MethodPost, "/contracts/"+clean.contractID+"/eviction", nil)
	if r.Code != http.StatusConflict || r.str(t, "type") != "no_arrears" {
		t.Errorf("eviction with nothing owed = %d %s", r.Code, r.Raw)
	}
}
