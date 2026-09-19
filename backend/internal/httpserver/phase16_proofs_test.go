package httpserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"tms/backend/internal/storage"
)

// Phase 16 §16.1 — proof of payment.
//
// The rule the whole feature rests on is "a proof is a claim, a payment is a
// fact": nothing here may move a schedule except through the accept path, and
// that path runs the same allocator POST /payments runs. The tests below check
// both halves — that a claim on its own changes no money, and that accepting
// one behaves exactly like recording the payment by hand.

// proofFixture is an activated contract plus the renter who rents it.
type proofFixture struct {
	paymentFixture
	h *harness
}

func (h *harness) newProofFixture(t *testing.T, tag, ownerPhone, renterPhone string) proofFixture {
	t.Helper()
	return proofFixture{paymentFixture: h.newPaymentFixture(t, tag, ownerPhone, renterPhone), h: h}
}

// requireStorage skips a test that needs a real object in the bucket. The
// validation tests above it do not, deliberately: a presigned URL must never be
// minted for a type the completion callback will refuse, and that has to hold
// on a machine with no MinIO.
func (f proofFixture) requireStorage(t *testing.T) {
	t.Helper()
	if f.h.store == nil {
		t.Skip("MinIO is not reachable; the presigned round trip is skipped")
	}
}

// pngBytes is a payload of a given size that MinIO will record as image/png.
func pngBytes(n int) []byte {
	out := make([]byte, n)
	copy(out, []byte("\x89PNG\r\n\x1a\n"))
	return out
}

// submitProof walks the real renter path: presign, upload, file the claim.
// `overrides` is merged into the POST /me/proofs body, so a test can change
// the amount, the schedule or the method without repeating the rest.
func (f proofFixture) submitProof(t *testing.T, size int, overrides map[string]any) response {
	t.Helper()
	ticket := f.renter.do(http.MethodPost, "/me/proofs/upload", map[string]any{
		"contract_id": f.contractID, "content_type": "image/png", "size_bytes": size,
	}).mustStatus(t, http.StatusOK, "presign a proof upload")
	key := ticket.str(t, "object_key")

	if err := f.h.store.PutBytes(context.Background(), storage.BucketProofs,
		key, pngBytes(size), "image/png"); err != nil {
		t.Fatalf("upload the proof object: %v", err)
	}

	body := map[string]any{
		"contract_id": f.contractID, "amount": f.amounts[0],
		"paid_at": time.Now().UTC().Format(time.RFC3339),
		"method":  "bank_transfer", "reference": "TRF-99", "object_key": key,
	}
	for k, v := range overrides {
		body[k] = v
	}
	return f.renter.do(http.MethodPost, "/me/proofs", body)
}

// ------------------------------------------------------------- the ticket --

// TestProofPresignEnforcesTypeAndSize: the ceiling is checked before object
// storage is consulted, so an unacceptable upload never gets a URL at all.
func TestProofPresignEnforcesTypeAndSize(t *testing.T) {
	h := newHarness(t)
	fix := h.newProofFixture(t, "ProofPresign", "0716120100", "+255716120101")

	for _, probe := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"a GIF", map[string]any{"content_type": "image/gif", "size_bytes": 1024}, "content_type"},
		{"no type", map[string]any{"size_bytes": 1024}, "content_type"},
		{"six MiB", map[string]any{"content_type": "application/pdf", "size_bytes": 6 << 20}, "size_bytes"},
		{"nothing at all", map[string]any{"content_type": "image/png", "size_bytes": 0}, "size_bytes"},
	} {
		body := map[string]any{"contract_id": fix.contractID}
		for k, v := range probe.body {
			body[k] = v
		}
		resp := fix.renter.do(http.MethodPost, "/me/proofs/upload", body)
		resp.mustStatus(t, http.StatusBadRequest, probe.name)
		if _, ok := errorsOf(t, resp)[probe.field]; !ok {
			t.Errorf("%s did not name %q: %s", probe.name, probe.field, resp.Raw)
		}
	}

	fix.requireStorage(t)

	// A key that was never issued cannot be redeemed, however well-formed it
	// looks: the ticket, not the key's shape, is the authorisation.
	forged := fix.renter.do(http.MethodPost, "/me/proofs", map[string]any{
		"contract_id": fix.contractID, "amount": fix.amounts[0],
		"method": "bank_transfer", "paid_at": time.Now().UTC().Format(time.RFC3339),
		"object_key": fix.orgID + "/00000000-0000-4000-8000-000000000000.png",
	})
	forged.mustStatus(t, http.StatusBadRequest, "submit against a key nobody issued")
	if _, ok := errorsOf(t, forged)["object_key"]; !ok {
		t.Errorf("a forged key did not name object_key: %s", forged.Raw)
	}

	// The completion checks what MinIO holds against what the ticket promised:
	// presigning for 2 KB and uploading 4 KB is refused and the object removed.
	ticket := fix.renter.do(http.MethodPost, "/me/proofs/upload", map[string]any{
		"contract_id": fix.contractID, "content_type": "image/png", "size_bytes": 2048,
	}).mustStatus(t, http.StatusOK, "presign")
	key := ticket.str(t, "object_key")
	if want := fix.orgID + "/" + ticket.str(t, "proof_id") + ".png"; key != want {
		t.Errorf("object_key = %q, want %q", key, want)
	}
	if err := h.store.PutBytes(context.Background(), storage.BucketProofs,
		key, pngBytes(4096), "image/png"); err != nil {
		t.Fatalf("upload: %v", err)
	}
	mismatch := fix.renter.do(http.MethodPost, "/me/proofs", map[string]any{
		"contract_id": fix.contractID, "amount": fix.amounts[0],
		"method": "bank_transfer", "paid_at": time.Now().UTC().Format(time.RFC3339),
		"object_key": key,
	})
	mismatch.mustStatus(t, http.StatusBadRequest, "upload a different size than was presigned")
	if _, ok := errorsOf(t, mismatch)["size_bytes"]; !ok {
		t.Errorf("a size mismatch did not name size_bytes: %s", mismatch.Raw)
	}
}

// TestProofForAnotherRentersContractIs404: "not yours" and "not there" are the
// same answer (API.md), so a renter probing contract ids learns nothing.
func TestProofForAnotherRentersContractIs404(t *testing.T) {
	h := newHarness(t)
	mine := h.newProofFixture(t, "ProofMine", "0716120110", "+255716120111")
	other := h.newProofFixture(t, "ProofOther", "0716120120", "+255716120121")

	resp := other.renter.do(http.MethodPost, "/me/proofs/upload", map[string]any{
		"contract_id": mine.contractID, "content_type": "image/png", "size_bytes": 1024,
	})
	resp.mustStatus(t, http.StatusNotFound, "presign against another renter's contract")

	filed := other.renter.do(http.MethodPost, "/me/proofs", map[string]any{
		"contract_id": mine.contractID, "amount": 10_000, "method": "bank_transfer",
		"paid_at":    time.Now().UTC().Format(time.RFC3339),
		"object_key": mine.orgID + "/00000000-0000-4000-8000-000000000000.png",
	})
	filed.mustStatus(t, http.StatusNotFound, "file against another renter's contract")
}

// ------------------------------------------------------------ the ruling --

// TestAcceptProofAllocatesPayment is the heart of §16.1: accepting runs the
// allocator, links the payment, settles the schedule and thanks the renter —
// the same four things recording the payment by hand does.
func TestAcceptProofAllocatesPayment(t *testing.T) {
	h := newHarness(t)
	fix := h.newProofFixture(t, "ProofAccept", "0716120130", "+255716120131")
	fix.requireStorage(t)

	filed := fix.submitProof(t, 2048, nil).
		mustStatus(t, http.StatusCreated, "file a proof")
	proofID := filed.str(t, "proof", "id")
	if status := filed.str(t, "proof", "status"); status != "submitted" {
		t.Errorf("a fresh proof is %q, want submitted", status)
	}
	// A claim moves no money on its own.
	if got := fix.statuses(t)[0]; got == "paid" {
		t.Fatal("filing a proof settled a schedule; a claim is not a payment")
	}

	// It reaches the landlord's queue, and the badge counts it.
	queue := listOf(t, fix.owner.do(http.MethodGet, "/proofs", nil).
		mustStatus(t, http.StatusOK, "the review queue"))
	if len(queue) != 1 || queue[0]["id"] != proofID {
		t.Fatalf("review queue = %+v, want the one filed proof", queue)
	}
	if got := num(t, fix.owner.do(http.MethodGet, "/proofs/summary", nil).
		mustStatus(t, http.StatusOK, "the badge"), "submitted_count"); got != 1 {
		t.Errorf("submitted_count = %v, want 1", got)
	}

	// The detail view carries a link to the evidence, and issuing it is audited.
	detail := fix.owner.do(http.MethodGet, "/proofs/"+proofID, nil).
		mustStatus(t, http.StatusOK, "read the proof")
	if detail.str(t, "proof", "view_url") == "" {
		t.Error("no view_url was issued")
	}
	if len(h.auditPayloads(t, "proof.view")) == 0 {
		t.Error("issuing a view link wrote no proof.view audit row")
	}

	accepted := fix.owner.do(http.MethodPost, "/proofs/"+proofID+"/accept", map[string]any{}).
		mustStatus(t, http.StatusOK, "accept the proof")
	if status := accepted.str(t, "proof", "status"); status != "accepted" {
		t.Errorf("proof status = %q, want accepted", status)
	}
	paymentID := accepted.str(t, "payment", "id")
	if linked := accepted.str(t, "proof", "payment_id"); linked != paymentID {
		t.Errorf("proof.payment_id = %q, want the payment it created (%q)", linked, paymentID)
	}
	if accepted.str(t, "proof", "reviewed_by_name") == "" {
		t.Error("an accepted proof does not name its reviewer")
	}
	if got := fix.statuses(t)[0]; got != "paid" {
		t.Errorf("first schedule status = %q, want paid", got)
	}
	if len(ofKind(h.notifications(t), "thank_you")) != 1 {
		t.Error("accepting a proof queued no thank-you")
	}
	if len(h.auditPayloads(t, "proof.accept")) != 1 {
		t.Error("accepting wrote no proof.accept audit row")
	}
	if len(h.auditPayloads(t, "payment.record")) != 1 {
		t.Error("accepting did not record the payment through the allocator's own path")
	}

	// A second ruling on a decided claim is a 409, not a second payment.
	fix.owner.do(http.MethodPost, "/proofs/"+proofID+"/accept", map[string]any{}).
		mustStatus(t, http.StatusConflict, "accept twice")
	fix.owner.do(http.MethodPost, "/proofs/"+proofID+"/reject", map[string]any{"reason": "changed my mind"}).
		mustStatus(t, http.StatusConflict, "reject an accepted proof")
	// And it has left the default queue.
	if got := num(t, fix.owner.do(http.MethodGet, "/proofs/summary", nil).
		mustStatus(t, http.StatusOK, "the badge"), "submitted_count"); got != 0 {
		t.Errorf("submitted_count after the ruling = %v, want 0", got)
	}
}

// TestAcceptProofOverpayPassesThroughUnchanged: the allocator's confirm
// prompt reaches the review sheet verbatim, which is what lets the UI reuse
// the record-payment confirm (PLAN2 §16.1).
func TestAcceptProofOverpayPassesThroughUnchanged(t *testing.T) {
	h := newHarness(t)
	fix := h.newProofFixture(t, "ProofOverpay", "0716120140", "+255716120141")
	fix.requireStorage(t)

	over := fix.amounts[0] + 50_000
	filed := fix.submitProof(t, 2048, map[string]any{
		"amount": over, "schedule_id": fix.scheduleIDs[0],
	}).mustStatus(t, http.StatusCreated, "file an overpaying proof")
	proofID := filed.str(t, "proof", "id")

	refused := fix.owner.do(http.MethodPost, "/proofs/"+proofID+"/accept", map[string]any{})
	refused.mustStatus(t, http.StatusConflict, "accept an overpayment without confirming")
	if refused.Body["type"] != "overpay_confirm_required" {
		t.Fatalf("type = %v, want overpay_confirm_required — body: %s", refused.Body["type"], refused.Raw)
	}
	if got := num(t, refused, "excess"); got != 50_000 {
		t.Errorf("excess = %v, want 50000", got)
	}
	if refused.Body["next_schedule"] == nil {
		t.Errorf("the refusal names no next_schedule: %s", refused.Raw)
	}
	// Refused means nothing happened: the claim is still waiting.
	if status := fix.owner.do(http.MethodGet, "/proofs/"+proofID, nil).
		mustStatus(t, http.StatusOK, "read the proof").str(t, "proof", "status"); status != "submitted" {
		t.Errorf("status after a refused acceptance = %q, want submitted", status)
	}

	fix.owner.do(http.MethodPost, "/proofs/"+proofID+"/accept",
		map[string]any{"allow_overpay_rollover": true}).
		mustStatus(t, http.StatusOK, "accept with the rollover confirmed")
	statuses := fix.statuses(t)
	if statuses[0] != "paid" || statuses[1] != "partial" {
		t.Errorf("schedule statuses = %v, want the first paid and the excess rolled onto the second", statuses)
	}
}

// TestRejectProofTellsTheRenterWhy: the reason is mandatory and it travels.
func TestRejectProofTellsTheRenterWhy(t *testing.T) {
	h := newHarness(t)
	fix := h.newProofFixture(t, "ProofReject", "0716120150", "+255716120151")
	fix.requireStorage(t)

	proofID := fix.submitProof(t, 2048, nil).
		mustStatus(t, http.StatusCreated, "file a proof").str(t, "proof", "id")

	fix.owner.do(http.MethodPost, "/proofs/"+proofID+"/reject", map[string]any{}).
		mustStatus(t, http.StatusBadRequest, "reject with no reason")

	const reason = "the reference does not appear on our statement"
	rejected := fix.owner.do(http.MethodPost, "/proofs/"+proofID+"/reject",
		map[string]any{"reason": reason}).
		mustStatus(t, http.StatusOK, "reject the proof")
	if status := rejected.str(t, "proof", "status"); status != "rejected" {
		t.Errorf("status = %q, want rejected", status)
	}
	if got := rejected.str(t, "proof", "rejection_reason"); got != reason {
		t.Errorf("rejection_reason = %q, want %q", got, reason)
	}

	notes := ofKind(h.notifications(t), "proof_rejected")
	if len(notes) != 1 {
		t.Fatalf("proof_rejected notifications = %+v, want 1", notes)
	}
	if !strings.Contains(notes[0].Body, reason) {
		t.Errorf("the SMS %q does not carry the reason", notes[0].Body)
	}
	if len(h.auditPayloads(t, "proof.reject")) != 1 {
		t.Error("rejecting wrote no proof.reject audit row")
	}

	// The renter sees the ruling on their own history, and may file again.
	mine := listOf(t, fix.renter.do(http.MethodGet, "/me/proofs", nil).
		mustStatus(t, http.StatusOK, "the renter's proofs"))
	if len(mine) != 1 || mine[0]["status"] != "rejected" {
		t.Fatalf("the renter's history = %+v, want one rejected claim", mine)
	}
	fix.submitProof(t, 2048, nil).mustStatus(t, http.StatusCreated, "file again after a rejection")
}

// TestWithdrawProofOnlyWhileSubmitted: a renter may take back a claim nobody
// has ruled on, and only that.
func TestWithdrawProofOnlyWhileSubmitted(t *testing.T) {
	h := newHarness(t)
	fix := h.newProofFixture(t, "ProofWithdraw", "0716120160", "+255716120161")
	fix.requireStorage(t)

	first := fix.submitProof(t, 2048, nil).
		mustStatus(t, http.StatusCreated, "file a proof").str(t, "proof", "id")
	fix.renter.do(http.MethodDelete, "/me/proofs/"+first, nil).
		mustStatus(t, http.StatusNoContent, "withdraw a pending proof")
	if rows := listOf(t, fix.renter.do(http.MethodGet, "/me/proofs", nil).
		mustStatus(t, http.StatusOK, "the renter's proofs")); len(rows) != 0 {
		t.Errorf("a withdrawn proof is still listed: %+v", rows)
	}
	fix.renter.do(http.MethodDelete, "/me/proofs/"+first, nil).
		mustStatus(t, http.StatusNotFound, "withdraw the same proof twice")
	if len(h.auditPayloads(t, "proof.withdraw")) != 1 {
		t.Error("withdrawing wrote no proof.withdraw audit row")
	}

	second := fix.submitProof(t, 2048, nil).
		mustStatus(t, http.StatusCreated, "file another proof").str(t, "proof", "id")
	fix.owner.do(http.MethodPost, "/proofs/"+second+"/accept", map[string]any{}).
		mustStatus(t, http.StatusOK, "accept it")
	fix.renter.do(http.MethodDelete, "/me/proofs/"+second, nil).
		mustStatus(t, http.StatusConflict, "withdraw a proof that has been accepted")
}

// TestProofTicketIsSingleUse: one upload link buys one claim. The ticket is
// redeemed with a GETDEL, so a replay of the same body finds nothing and is
// refused on `object_key` — which is what stops one screenshot of a bank app
// becoming two claims on the landlord's queue.
func TestProofTicketIsSingleUse(t *testing.T) {
	h := newHarness(t)
	fix := h.newProofFixture(t, "ProofTicket", "0716120180", "+255716120181")
	fix.requireStorage(t)

	key := fix.renter.do(http.MethodPost, "/me/proofs/upload", map[string]any{
		"contract_id": fix.contractID, "content_type": "image/png", "size_bytes": 2048,
	}).mustStatus(t, http.StatusOK, "presign").str(t, "object_key")
	if err := h.store.PutBytes(context.Background(), storage.BucketProofs,
		key, pngBytes(2048), "image/png"); err != nil {
		t.Fatalf("upload the proof object: %v", err)
	}

	body := map[string]any{
		"contract_id": fix.contractID, "amount": fix.amounts[0],
		"paid_at": time.Now().UTC().Format(time.RFC3339),
		"method":  "bank_transfer", "reference": "TRF-1", "object_key": key,
	}
	fix.renter.do(http.MethodPost, "/me/proofs", body).
		mustStatus(t, http.StatusCreated, "file the claim")

	second := fix.renter.do(http.MethodPost, "/me/proofs", body)
	if second.Code != http.StatusBadRequest && second.Code != http.StatusConflict {
		t.Fatalf("redeeming the same upload twice: status = %d, want 400 or 409 — body: %s",
			second.Code, second.Raw)
	}
	if second.Code == http.StatusBadRequest {
		if _, ok := errorsOf(t, second)["object_key"]; !ok {
			t.Errorf("the second submission did not name object_key: %s", second.Raw)
		}
	}
	if rows := listOf(t, fix.renter.do(http.MethodGet, "/me/proofs", nil).
		mustStatus(t, http.StatusOK, "the renter's proofs")); len(rows) != 1 {
		t.Errorf("one upload became %d claims: %+v", len(rows), rows)
	}
}

// TestAcceptedProofSurvivesAReversal: a decided claim is a record. A second
// ruling is `proof_not_pending`, and reversing the payment it created does not
// un-decide it — the claim was made and answered, and the reversal is a fact
// about the payment, not about the proof (API.md §16.1, SPEC §4).
func TestAcceptedProofSurvivesAReversal(t *testing.T) {
	h := newHarness(t)
	fix := h.newProofFixture(t, "ProofReverse", "0716120190", "+255716120191")
	fix.requireStorage(t)

	proofID := fix.submitProof(t, 2048, nil).
		mustStatus(t, http.StatusCreated, "file a proof").str(t, "proof", "id")
	accepted := fix.owner.do(http.MethodPost, "/proofs/"+proofID+"/accept", map[string]any{}).
		mustStatus(t, http.StatusOK, "accept the proof")
	paymentID := accepted.str(t, "payment", "id")

	again := fix.owner.do(http.MethodPost, "/proofs/"+proofID+"/accept", map[string]any{})
	again.mustStatus(t, http.StatusConflict, "accept the same proof twice")
	if again.Body["type"] != "proof_not_pending" {
		t.Errorf("type = %v, want proof_not_pending — body: %s", again.Body["type"], again.Raw)
	}
	if len(h.auditPayloads(t, "payment.record")) != 1 {
		t.Error("a second acceptance recorded a second payment")
	}

	fix.owner.do(http.MethodPost, "/payments/"+paymentID+"/reverse",
		map[string]any{"reason": "the transfer bounced"}).
		mustStatus(t, http.StatusOK, "reverse the payment the proof created")

	after := fix.owner.do(http.MethodGet, "/proofs/"+proofID, nil).
		mustStatus(t, http.StatusOK, "read the proof after the reversal")
	if got := after.str(t, "proof", "status"); got != "accepted" {
		t.Errorf("proof status after the reversal = %q, want accepted", got)
	}
	if got := after.str(t, "proof", "payment_id"); got != paymentID {
		t.Errorf("proof.payment_id = %q, want %q — the link to the reversed payment stays", got, paymentID)
	}
	if got := fix.statuses(t)[0]; got == "paid" {
		t.Error("reversing the payment left the schedule settled")
	}
}

// TestProofSubmissionRateLimit: ten claims a day is the ceiling PLAN2 names.
func TestProofSubmissionRateLimit(t *testing.T) {
	h := newHarness(t)
	fix := h.newProofFixture(t, "ProofLimit", "0716120170", "+255716120171")
	fix.requireStorage(t)

	for i := 0; i < 10; i++ {
		fix.submitProof(t, 1024, map[string]any{"amount": 1_000}).
			mustStatus(t, http.StatusCreated, "file proof within the daily ceiling")
	}
	fix.submitProof(t, 1024, map[string]any{"amount": 1_000}).
		mustStatus(t, http.StatusTooManyRequests, "the eleventh proof of the day")
}
