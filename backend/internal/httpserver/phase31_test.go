package httpserver_test

import (
	"net/http"
	"strings"
	"testing"

	"tms/backend/internal/notify"
)

// Phase 31: a contract change is drafted by a manager or owner, approved by an
// owner, and only then reaches the renter, who signs or declines it.

// addManager invites a manager to the fixture's org and returns their session.
func (h *harness) addManager(t *testing.T, owner *client, email string) *client {
	t.Helper()
	owner.do(http.MethodPost, "/org/members", map[string]any{
		"email": email, "full_name": "Maker Manager", "role": "org_manager",
	}).mustStatus(t, http.StatusCreated, "invite manager")
	link := h.email.LastLink(email)
	if link == "" {
		t.Fatal("no invite email captured")
	}
	m := h.client()
	m.do(http.MethodPost, "/auth/invite/accept", map[string]any{
		"token": tokenFromLink(t, link), "password": "managerpass1",
	}).mustStatus(t, http.StatusOK, "accept invite")
	return m
}

// secondPeriod is the start of the contract's second period: a valid
// effective date for an amendment.
func secondPeriod(t *testing.T, c *client, contractID string) string {
	t.Helper()
	rows := scheduleRows(t, c, contractID)
	if len(rows) < 2 {
		t.Fatalf("contract has %d periods", len(rows))
	}
	d, _ := rows[1]["period_start"].(string)
	return d
}

func amendBodyWith(effective string, extra map[string]any) map[string]any {
	b := map[string]any{
		"effective_date": effective,
		"note_sw":        "Kodi inapanda kuanzia mwezi ujao.",
		"note_en":        "Rent goes up from next month.",
	}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func TestPhase31ManagerDraftsOwnerApprovesRenterSigns(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "MakerChecker", "0724000100", "+255724000101")
	owner := fix.owner
	manager := h.addManager(t, owner, "maker@mc.test")
	effective := secondPeriod(t, owner, fix.contractID)
	readyBefore := len(ofKind(h.notifications(t), notify.KindContractReady))

	// Both notes are required.
	manager.do(http.MethodPost, "/contracts/"+fix.contractID+"/amend", map[string]any{
		"effective_date": effective, "note_en": "only english",
	}).mustStatus(t, http.StatusBadRequest, "missing note_sw")

	draft := manager.do(http.MethodPost, "/contracts/"+fix.contractID+"/amend",
		amendBodyWith(effective, map[string]any{"rent_amount": 300_000})).
		mustStatus(t, http.StatusCreated, "draft")
	id := draft.str(t, "contract", "id")
	if got := draft.str(t, "contract", "status"); got != "draft" {
		t.Errorf("status = %s, want draft", got)
	}
	if got := draft.str(t, "contract", "amendment", "stage"); got != "draft" {
		t.Errorf("stage = %s, want draft", got)
	}
	if n := len(ofKind(h.notifications(t), notify.KindContractReady)); n != readyBefore {
		t.Errorf("contract_ready on a draft = %d", n)
	}
	if n := len(ofKind(h.notifications(t), notify.KindContractAmendment)); n != 0 {
		t.Errorf("contract_amendment on a draft = %d", n)
	}

	// The renter cannot see it yet.
	fix.renter.do(http.MethodGet, "/contracts/"+id, nil).mustStatus(t, http.StatusNotFound, "renter reads draft")
	for _, c := range listOf(t, fix.renter.do(http.MethodGet, "/me/contracts", nil).mustStatus(t, http.StatusOK, "mine")) {
		if c["id"] == id {
			t.Error("draft listed for the renter")
		}
	}

	// A manager may not approve, not even their own draft.
	if r := manager.do(http.MethodPost, "/contracts/"+id+"/amendment/approve", nil); r.Code != http.StatusForbidden {
		t.Errorf("manager approve = %d, want 403", r.Code)
	}
	// An owner may not approve a manager's draft before it is submitted.
	if r := owner.do(http.MethodPost, "/contracts/"+id+"/amendment/approve", nil); r.Code != http.StatusConflict {
		t.Errorf("approve unsubmitted = %d, want 409", r.Code)
	}

	manager.do(http.MethodPost, "/contracts/"+id+"/amendment/submit", nil).mustStatus(t, http.StatusOK, "submit")
	queue := listOf(t, owner.do(http.MethodGet, "/contracts?amendment_stage=submitted", nil).mustStatus(t, http.StatusOK, "queue"))
	if len(queue) != 1 || queue[0]["id"] != id {
		t.Errorf("approval queue = %v", queue)
	}
	// Submitted: the manager can no longer edit it.
	if r := manager.do(http.MethodPatch, "/contracts/"+id+"/amendment", map[string]any{"note_en": "late edit"}); r.Code != http.StatusConflict {
		t.Errorf("manager edit after submit = %d, want 409", r.Code)
	}

	approved := owner.do(http.MethodPost, "/contracts/"+id+"/amendment/approve", nil).mustStatus(t, http.StatusOK, "approve")
	if got := approved.str(t, "contract", "status"); got != "pending_signature" {
		t.Errorf("approved status = %s", got)
	}
	if got := approved.str(t, "contract", "amendment", "drafted_by_name"); got != "Maker Manager" {
		t.Errorf("drafted_by_name = %q", got)
	}
	sms := ofKind(h.notifications(t), notify.KindContractAmendment)
	if len(sms) != 1 {
		t.Fatalf("contract_amendment SMS = %d, want 1", len(sms))
	}
	// Default renter locale is Swahili: the Swahili note travels.
	if !strings.Contains(sms[0].Body, "Kodi inapanda") || !strings.Contains(sms[0].Body, effective) {
		t.Errorf("SMS body = %q", sms[0].Body)
	}

	// The renter sees the note but not the org's internals.
	mine := fix.renter.do(http.MethodGet, "/contracts/"+id, nil).mustStatus(t, http.StatusOK, "renter reads approved")
	if got := mine.str(t, "contract", "amendment", "note_sw"); got == "" {
		t.Error("renter lacks the note")
	}
	if strings.Contains(string(mine.Raw), "drafted_by") {
		t.Errorf("renter sees drafter: %s", mine.Raw)
	}

	h.signAsRenter(t, fix.renter, id, fix.renterPhone)
	owner.do(http.MethodPost, "/contracts/"+id+"/activate", nil).mustStatus(t, http.StatusOK, "activate")
	if got := owner.do(http.MethodGet, "/contracts/"+fix.contractID, nil).str(t, "contract", "superseded_by_contract_id"); got != id {
		t.Errorf("superseded_by = %s", got)
	}
	for _, a := range []string{"contract.amend", "contract.amend_submit", "contract.amend_approve"} {
		if n := len(h.auditPayloads(t, a)); n != 1 {
			t.Errorf("%s rows = %d, want 1", a, n)
		}
	}
}

func TestPhase31OwnerApprovesOwnDraftInEnglish(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "SelfApprove", "0724000200", "+255724000201")
	fix.renter.do(http.MethodPatch, "/me", map[string]any{"locale": "en"}).mustStatus(t, http.StatusOK, "renter english")
	owner := fix.owner
	effective := secondPeriod(t, owner, fix.contractID)

	id := owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/amend", amendBodyWith(effective, map[string]any{
		"body_html": "<p>Tenant {{renter_name}} pays {{rent}} for {{unit}}. Pets allowed.</p>",
	})).mustStatus(t, http.StatusCreated, "draft").str(t, "contract", "id")

	doc := owner.do(http.MethodGet, "/contracts/"+id+"/document", nil).mustStatus(t, http.StatusOK, "document")
	if !strings.Contains(string(doc.Raw), "Pets allowed") || strings.Contains(string(doc.Raw), "{{rent}}") {
		t.Errorf("custom wording not rendered: %s", doc.Raw)
	}

	owner.do(http.MethodPost, "/contracts/"+id+"/amendment/approve", nil).mustStatus(t, http.StatusOK, "self approve")
	sms := ofKind(h.notifications(t), notify.KindContractAmendment)
	if len(sms) != 1 || !strings.Contains(sms[0].Body, "Rent goes up") {
		t.Errorf("english SMS = %v", sms)
	}
	if rows := h.auditPayloads(t, "contract.amend_approve"); len(rows) != 1 || !strings.Contains(rows[0], `"self_approved": true`) {
		t.Errorf("approve audit = %v", rows)
	}
}

func TestPhase31ReturnEditResubmitAndReject(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "ReturnReject", "0724000300", "+255724000301")
	owner := fix.owner
	manager := h.addManager(t, owner, "maker2@mc.test")
	effective := secondPeriod(t, owner, fix.contractID)
	id := manager.do(http.MethodPost, "/contracts/"+fix.contractID+"/amend", amendBodyWith(effective, nil)).
		mustStatus(t, http.StatusCreated, "draft").str(t, "contract", "id")
	manager.do(http.MethodPost, "/contracts/"+id+"/amendment/submit", nil).mustStatus(t, http.StatusOK, "submit")

	owner.do(http.MethodPost, "/contracts/"+id+"/amendment/return", map[string]any{}).
		mustStatus(t, http.StatusBadRequest, "return without reason")
	ret := owner.do(http.MethodPost, "/contracts/"+id+"/amendment/return", map[string]any{"reason": "Say the new amount"}).
		mustStatus(t, http.StatusOK, "return")
	if got := ret.str(t, "contract", "amendment", "stage"); got != "draft" {
		t.Errorf("returned stage = %s", got)
	}
	if got := ret.str(t, "contract", "amendment", "review_note"); got != "Say the new amount" {
		t.Errorf("review note = %q", got)
	}

	edited := manager.do(http.MethodPatch, "/contracts/"+id+"/amendment", map[string]any{
		"note_en": "Rent becomes TZS 300,000.", "rent_amount": 300_000,
	}).mustStatus(t, http.StatusOK, "edit")
	if got := edited.str(t, "contract", "amendment", "note_en"); got != "Rent becomes TZS 300,000." {
		t.Errorf("note_en = %q", got)
	}
	if got := mustFloat(t, edited.Body["contract"].(map[string]any), "rent_amount"); got != 300_000 {
		t.Errorf("rent = %v", got)
	}
	manager.do(http.MethodPost, "/contracts/"+id+"/amendment/submit", nil).mustStatus(t, http.StatusOK, "resubmit")

	rej := owner.do(http.MethodPost, "/contracts/"+id+"/amendment/reject", map[string]any{"reason": "Not this year"}).
		mustStatus(t, http.StatusOK, "reject")
	if got := rej.str(t, "contract", "status"); got != "terminated" {
		t.Errorf("rejected status = %s", got)
	}
	if n := len(ofKind(h.notifications(t), notify.KindContractAmendment)); n != 0 {
		t.Errorf("SMS after reject = %d", n)
	}
	if n := len(ofKind(h.notifications(t), notify.KindContractTerminated)); n != 0 {
		t.Errorf("termination SMS for a draft = %d", n)
	}
	// A new draft is possible again.
	manager.do(http.MethodPost, "/contracts/"+fix.contractID+"/amend", amendBodyWith(effective, nil)).
		mustStatus(t, http.StatusCreated, "draft again")
	inbox := listOf(t, owner.do(http.MethodGet, "/inbox", nil).mustStatus(t, http.StatusOK, "inbox"))
	kinds := map[string]int{}
	for _, it := range inbox {
		k, _ := it["kind"].(string)
		kinds[k]++
	}
	if kinds["amendment_submitted"] != 2 || kinds["amendment_returned"] != 1 || kinds["amendment_rejected"] != 1 {
		t.Errorf("inbox kinds = %v", kinds)
	}
}

func TestPhase31WithdrawDraft(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Withdraw", "0724000400", "+255724000401")
	effective := secondPeriod(t, fix.owner, fix.contractID)
	id := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/amend", amendBodyWith(effective, nil)).
		mustStatus(t, http.StatusCreated, "draft").str(t, "contract", "id")
	w := fix.owner.do(http.MethodPost, "/contracts/"+id+"/amendment/withdraw", nil).mustStatus(t, http.StatusOK, "withdraw")
	if got := w.str(t, "contract", "amendment", "stage"); got != "withdrawn" {
		t.Errorf("stage = %s", got)
	}
	fix.owner.do(http.MethodPost, "/contracts/"+id+"/amendment/approve", nil).mustStatus(t, http.StatusConflict, "approve withdrawn")
}

func TestPhase31RenterDeclines(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Decline", "0724000500", "+255724000501")
	owner := fix.owner
	effective := secondPeriod(t, owner, fix.contractID)
	id := owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/amend", amendBodyWith(effective, map[string]any{"rent_amount": 400_000})).
		mustStatus(t, http.StatusCreated, "draft").str(t, "contract", "id")

	// Not approved yet: the renter cannot reach it.
	fix.renter.do(http.MethodPost, "/me/contracts/"+id+"/decline", map[string]any{"reason": "no"}).
		mustStatus(t, http.StatusNotFound, "decline draft")
	owner.do(http.MethodPost, "/contracts/"+id+"/amendment/approve", nil).mustStatus(t, http.StatusOK, "approve")

	// A first contract is not an amendment and cannot be declined.
	fix.renter.do(http.MethodPost, "/me/contracts/"+fix.contractID+"/decline", map[string]any{"reason": "no"}).
		mustStatus(t, http.StatusConflict, "decline original")
	fix.renter.do(http.MethodPost, "/me/contracts/"+id+"/decline", map[string]any{}).
		mustStatus(t, http.StatusBadRequest, "decline without reason")
	d := fix.renter.do(http.MethodPost, "/me/contracts/"+id+"/decline", map[string]any{"reason": "Too expensive"}).
		mustStatus(t, http.StatusOK, "decline")
	if got := d.str(t, "contract", "amendment", "stage"); got != "declined" {
		t.Errorf("stage = %s", got)
	}
	if got := d.str(t, "contract", "amendment", "decline_reason"); got != "Too expensive" {
		t.Errorf("decline reason = %q", got)
	}
	if got := owner.do(http.MethodGet, "/contracts/"+fix.contractID, nil).str(t, "contract", "status"); got != "active" {
		t.Errorf("running contract = %s, want active", got)
	}
	for _, r := range scheduleRows(t, owner, fix.contractID) {
		if r["status"] == "waived" {
			t.Error("a decline waived a period of the running contract")
		}
	}
	fix.renter.do(http.MethodPost, "/me/contracts/"+id+"/decline", map[string]any{"reason": "again"}).
		mustStatus(t, http.StatusConflict, "decline twice")
	inbox := listOf(t, owner.do(http.MethodGet, "/inbox", nil).mustStatus(t, http.StatusOK, "inbox"))
	found := false
	for _, it := range inbox {
		if it["kind"] == "amendment_declined" && strings.Contains(it["body"].(string), "Too expensive") {
			found = true
		}
	}
	if !found {
		t.Errorf("no amendment_declined inbox item: %v", inbox)
	}
	if n := len(h.auditPayloads(t, "contract.amend_decline")); n != 1 {
		t.Errorf("decline audit rows = %d", n)
	}
}
