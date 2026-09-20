package httpserver_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"tms/backend/internal/notify"
)

// Phase 19 — identity visibility (PLAN2 Phase 19, SPEC §5.10, §8).
//
// The tests here are mostly tests of things that must *not* happen: the number
// not appearing on a GET, the reveal not working across orgs, the rename not
// touching a signed document. The masked value is the product's normal state,
// and each of these is a way it could quietly stop being.

// ------------------------------------------------------------ 19.1 reveal --

// TestPhase19RevealGivesTheNumberAndRecordsIt is the happy path and the audit
// row in one: the landlord gets the digits, and the trail says they did.
func TestPhase19RevealGivesTheNumberAndRecordsIt(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "Reveal", "0719000100", "+255719000101")

	revealed := fix.owner.do(http.MethodPost,
		"/renters/"+fix.renterID+"/nida/reveal", map[string]any{"reason": "police report"}).
		mustStatus(t, http.StatusOK, "reveal nida")
	if got := revealed.str(t, "nida_number"); got != validNIDA {
		t.Errorf("nida_number = %q, want the stored number %q", got, validNIDA)
	}
	if revealed.str(t, "revealed_at") == "" {
		t.Error("reveal carries no revealed_at")
	}
	// A response holding a national ID must not be cacheable by anything.
	if cc := revealed.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store on a reveal", cc)
	}

	// The audit row carries the reason and the actor kind — and never the
	// number, which is the whole point of auditing the act rather than the data.
	payloads := h.auditPayloads(t, "renter.nida_reveal")
	if len(payloads) != 1 {
		t.Fatalf("renter.nida_reveal rows = %d, want 1", len(payloads))
	}
	if !strings.Contains(payloads[0], "police report") ||
		!strings.Contains(payloads[0], "org_user") {
		t.Errorf("audit payload = %s, want the reason and actor_kind", payloads[0])
	}
	if strings.Contains(payloads[0], validNIDA) {
		t.Fatalf("the audit trail carries the NIDA number: %s", payloads[0])
	}
}

// TestPhase19RevealIsRefusedWithoutARelationship: the directory's 404 is the
// answer, so the endpoint cannot be walked to learn whether an account exists.
func TestPhase19RevealIsRefusedWithoutARelationship(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "RevealA", "0719000200", "+255719000201")
	other := h.newOrgWithUnits("RevealB", "revealb@jjne.test", "0719000300",
		[]string{"Room 1"}, 250_000)

	other.client.do(http.MethodPost, "/renters/"+fix.renterID+"/nida/reveal", nil).
		mustStatus(t, http.StatusNotFound, "reveal a renter of another org")

	// A renter with no NIDA on file is also a 404, not an empty string: "no
	// number" and "not your renter" are the same non-answer.
	stranger := h.registerRenter("+255719000401", "No Nida", defaultPIN)
	strangerID := stranger.do(http.MethodGet, "/auth/me?audience=renter", nil).
		mustStatus(t, http.StatusOK, "me").str(t, "user", "id")
	fix.owner.do(http.MethodPost, "/renters/"+strangerID+"/nida/reveal", nil).
		mustStatus(t, http.StatusNotFound, "reveal a renter who never applied")

	if rows := h.auditPayloads(t, "renter.nida_reveal"); len(rows) != 0 {
		t.Errorf("refused reveals wrote %d audit rows, want 0", len(rows))
	}
}

// TestPhase19RevealIsRateLimitedPerOrg: the budget belongs to the org, so a
// manager and an owner scraping in turn do not get two of them.
func TestPhase19RevealIsRateLimitedPerOrg(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "RevealLim", "0719000500", "+255719000501")
	path := "/renters/" + fix.renterID + "/nida/reveal"

	for i := range 60 {
		fix.owner.do(http.MethodPost, path, nil).
			mustStatus(t, http.StatusOK, "reveal "+string(rune('a'+i%26)))
	}
	limited := fix.owner.do(http.MethodPost, path, nil)
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("reveal 61 status = %d, want 429 — body: %s", limited.Code, limited.Raw)
	}
	if limited.Header.Get("Retry-After") == "" {
		t.Error("429 carries no Retry-After")
	}
	h.forgetRateLimits(t, "rl:nida:reveal:*")
}

// TestPhase19RenterSeesWhoLooked: the deterrent half. The renter's own profile
// lists the reveals, without the reason the landlord typed.
func TestPhase19RenterSeesWhoLooked(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "RevealSeen", "0719000600", "+255719000601")

	before := fix.renter.do(http.MethodGet, "/me/profile", nil).
		mustStatus(t, http.StatusOK, "profile before")
	if rows := arrayOf(t, before, "nida_reveals"); len(rows) != 0 {
		t.Errorf("nida_reveals before any reveal = %d rows, want 0", len(rows))
	}

	fix.owner.do(http.MethodPost, "/renters/"+fix.renterID+"/nida/reveal",
		map[string]any{"reason": "guarantor check"}).
		mustStatus(t, http.StatusOK, "reveal")

	after := fix.renter.do(http.MethodGet, "/me/profile", nil).
		mustStatus(t, http.StatusOK, "profile after")
	rows := arrayOf(t, after, "nida_reveals")
	if len(rows) != 1 {
		t.Fatalf("nida_reveals = %d rows, want 1 — body: %s", len(rows), after.Raw)
	}
	if got, _ := rows[0]["by_kind"].(string); got != "landlord" {
		t.Errorf("by_kind = %q, want landlord", got)
	}
	if got, _ := rows[0]["org_name"].(string); got == "" {
		t.Error("a landlord reveal carries no org_name")
	}
	if strings.Contains(after.Raw, "guarantor check") {
		t.Error("the renter's profile leaks the landlord's stated reason")
	}
	// And the masked value on the same screen is untouched.
	if got := after.str(t, "profile", "nida_masked"); !strings.HasSuffix(got, validNIDA[len(validNIDA)-4:]) {
		t.Errorf("nida_masked = %q, want the masked form", got)
	}
	if strings.Contains(after.str(t, "profile", "nida_masked"), validNIDA) {
		t.Error("nida_masked returned the whole number")
	}
}

// TestPhase19NoGETEverCarriesTheNumber sweeps the renter-facing and
// landlord-facing reads that touch identity, and fails if any of them carries
// the digits.
func TestPhase19NoGETEverCarriesTheNumber(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "RevealSweep", "0719000700", "+255719000701")

	for _, probe := range []struct {
		c    *client
		path string
	}{
		{fix.owner, "/renters"},
		{fix.owner, "/renters/" + fix.renterID},
		{fix.renter, "/me/profile"},
		{fix.owner, "/contracts/" + fix.contractID},
	} {
		got := probe.c.do(http.MethodGet, probe.path, nil).
			mustStatus(t, http.StatusOK, "GET "+probe.path)
		if strings.Contains(got.Raw, validNIDA) {
			t.Errorf("GET %s carries the full NIDA number — body: %s", probe.path, got.Raw)
		}
	}
}

// --------------------------------------------------------- 19.3 renames --

// TestPhase19MemberRenamesThemselves also pins the Phase 13 body: a
// locale-only PATCH still works and still writes the locale action.
func TestPhase19MemberRenamesThemselves(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("RenameSelf", "renameself@jjne.test", "0719001000",
		[]string{"Room 1"}, 250_000)

	renamed := fix.client.do(http.MethodPatch, "/org/members/me",
		map[string]any{"full_name": "  Joseph   Chuchu  "}).
		mustStatus(t, http.StatusOK, "rename self")
	// Trimmed and inner whitespace collapsed, in one place for all four routes.
	if got := renamed.str(t, "member", "full_name"); got != "Joseph Chuchu" {
		t.Errorf("full_name = %q, want %q", got, "Joseph Chuchu")
	}
	if rows := h.auditPayloads(t, "member.update"); len(rows) != 1 ||
		!strings.Contains(rows[0], "Joseph Chuchu") {
		t.Errorf("member.update rows = %v, want one carrying before and after", rows)
	}

	// The Phase 13 call, unchanged.
	fix.client.do(http.MethodPatch, "/org/members/me", map[string]any{"locale": "en"}).
		mustStatus(t, http.StatusOK, "locale only")
	if rows := h.auditPayloads(t, "user.locale_update"); len(rows) == 0 {
		t.Error("a locale-only PATCH stopped writing user.locale_update")
	}

	for _, bad := range []any{"", "A", strings.Repeat("x", 81)} {
		fix.client.do(http.MethodPatch, "/org/members/me",
			map[string]any{"full_name": bad}).
			mustStatus(t, http.StatusBadRequest, "reject a name out of bounds")
	}
}

// TestPhase19OwnerRenamesAManagerAndCannotEmptyTheOrg covers the two refusals
// that keep an org governable.
func TestPhase19OwnerRenamesAManagerAndCannotEmptyTheOrg(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("RenameStaff", "renamestaff@jjne.test", "0719001100",
		[]string{"Room 1"}, 250_000)

	created := fix.client.do(http.MethodPost, "/org/members", map[string]any{
		"email": "manager@renamestaff.test", "full_name": "Mangr Typo", "role": "org_manager",
	}).mustStatus(t, http.StatusCreated, "invite manager")
	managerID := created.str(t, "member", "id")

	fixed := fix.client.do(http.MethodPatch, "/org/members/"+managerID,
		map[string]any{"full_name": "Manager Fixed"}).
		mustStatus(t, http.StatusOK, "rename manager")
	if got := fixed.str(t, "member", "full_name"); got != "Manager Fixed" {
		t.Errorf("full_name = %q, want Manager Fixed", got)
	}

	// Promotion is allowed; the caller demoting themselves is not, because a
	// single request must never be able to leave an org without an owner.
	fix.client.do(http.MethodPatch, "/org/members/"+managerID,
		map[string]any{"role": "org_owner"}).
		mustStatus(t, http.StatusOK, "promote manager to owner")

	members := listOf(t, fix.client.do(http.MethodGet, "/org/members", nil).
		mustStatus(t, http.StatusOK, "members"))
	var ownID string
	for _, m := range members {
		if email, _ := m["email"].(string); email == "renamestaff@jjne.test" {
			ownID, _ = m["id"].(string)
		}
	}
	own := fix.client.do(http.MethodPatch, "/org/members/"+ownID,
		map[string]any{"role": "org_manager"})
	if own.Code != http.StatusConflict {
		t.Fatalf("self-demotion status = %d, want 409 — body: %s", own.Code, own.Raw)
	}
	if got := own.str(t, "type"); got != "cannot_change_own_role" {
		t.Errorf("type = %q, want cannot_change_own_role", got)
	}
}

// TestPhase19LandlordRenamesRenterOnlyBeforeSigning is the rule the phase turns
// on: a typo is the landlord's to fix, a signature is not.
func TestPhase19LandlordRenamesRenterOnlyBeforeSigning(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "RenameRenter", "0719001200", "+255719001201")

	renamed := fix.owner.do(http.MethodPatch, "/renters/"+fix.renterID,
		map[string]any{"full_name": "Asha Mrisho"}).
		mustStatus(t, http.StatusOK, "rename renter")
	if got := renamed.str(t, "renter", "full_name"); got != "Asha Mrisho" {
		t.Errorf("renter.full_name = %q, want Asha Mrisho", got)
	}
	// Both halves, or the directory shows one person under two names.
	if got := renamed.str(t, "profile", "full_name"); got != "Asha Mrisho" {
		t.Errorf("profile.full_name = %q, want Asha Mrisho", got)
	}
	if got := fix.renter.do(http.MethodGet, "/me/profile", nil).
		mustStatus(t, http.StatusOK, "renter profile").str(t, "user", "full_name"); got != "Asha Mrisho" {
		t.Errorf("users.full_name = %q, want Asha Mrisho", got)
	}

	// The renter is told, so a wrong edit is noticed.
	sent := ofKind(h.notifications(t), notify.KindNameCorrected)
	if len(sent) != 1 {
		t.Fatalf("name_corrected messages = %d, want 1", len(sent))
	}
	if !strings.Contains(sent[0].Body, "Asha Mrisho") {
		t.Errorf("name_corrected body = %q, want the new name in it", sent[0].Body)
	}
	if rows := h.auditPayloads(t, "renter.update"); len(rows) != 1 ||
		!strings.Contains(rows[0], "Asha Mrisho") {
		t.Errorf("renter.update rows = %v, want one carrying before and after", rows)
	}

	// Once signed, it is the renter's own name on their own document.
	h.signAsRenter(t, fix.renter, fix.contractID, fix.renterPhone)
	refused := fix.owner.do(http.MethodPatch, "/renters/"+fix.renterID,
		map[string]any{"full_name": "Wrong Again"})
	if refused.Code != http.StatusConflict {
		t.Fatalf("rename after signing status = %d, want 409 — body: %s", refused.Code, refused.Raw)
	}
	if got := refused.str(t, "type"); got != "renter_signed" {
		t.Errorf("type = %q, want renter_signed", got)
	}
}

// TestPhase19RenameLeavesTheSignedDocumentByteIdentical is the snapshot rule.
// The rendered terms and the hash are frozen at creation; only the live parties
// header follows the account.
func TestPhase19RenameLeavesTheSignedDocumentByteIdentical(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "RenameSnap", "0719001300", "+255719001301")
	h.signAsRenter(t, fix.renter, fix.contractID, fix.renterPhone)
	fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/activate", nil).
		mustStatus(t, http.StatusOK, "activate")

	docBefore := fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID+"/document", nil).
		mustStatus(t, http.StatusOK, "document before")
	verifyBefore := fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID+"/verify", nil).
		mustStatus(t, http.StatusOK, "verify before")
	hashBefore := verifyBefore.str(t, "stored_hash")

	// The renter fixes their own name — the one rename that is always allowed.
	fix.renter.completeProfile(t, "Renamed After Signing", validNIDA)

	docAfter := fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID+"/document", nil).
		mustStatus(t, http.StatusOK, "document after")
	verifyAfter := fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID+"/verify", nil).
		mustStatus(t, http.StatusOK, "verify after")

	if docAfter.str(t, "terms_html") != docBefore.str(t, "terms_html") {
		t.Error("the rendered terms changed after a rename; the snapshot is not frozen")
	}
	if got := verifyAfter.str(t, "stored_hash"); got != hashBefore {
		t.Errorf("stored_hash = %q, want the unchanged %q", got, hashBefore)
	}
	if got := verifyAfter.str(t, "computed_hash"); got != hashBefore {
		t.Errorf("computed_hash = %q, want the unchanged %q", got, hashBefore)
	}
	if got := docAfter.str(t, "snapshot_hash"); got != hashBefore {
		t.Errorf("document snapshot_hash = %q, want the unchanged %q", got, hashBefore)
	}
	if verifyAfter.Body["valid"] != true {
		t.Errorf("verify says the contract is no longer valid — body: %s", verifyAfter.Raw)
	}
}

// ------------------------------------------------- 19.2 admin directory --

// TestPhase19AdminDirectorySearchesEveryWay covers the three things an operator
// pastes into the box, and asserts the one field that must never be in the
// answer.
func TestPhase19AdminDirectorySearchesEveryWay(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "Directory", "0719002000", "+255719002001")
	admin := h.adminClient(t)

	for _, probe := range []struct{ name, query string }{
		{"phone as typed", "0719002001"},
		{"phone normalised", "+255719002001"},
		{"name prefix", "Directory Ren"},
	} {
		got := admin.do(http.MethodGet, "/admin/users?q="+url.QueryEscape(probe.query), nil).
			mustStatus(t, http.StatusOK, "search by "+probe.name)
		if !containsUserID(t, got, fix.renterID) {
			t.Errorf("search by %s did not find the renter — body: %s", probe.name, got.Raw)
		}
		if strings.Contains(got.Raw, "nida") {
			t.Fatalf("the user directory list carries a nida field — body: %s", got.Raw)
		}
	}

	// The org owner is an org_user with a role, not a renter with a tenancy.
	byKind := admin.do(http.MethodGet, "/admin/users?kind=org_user", nil).
		mustStatus(t, http.StatusOK, "filter by kind")
	for _, row := range listOf(t, byKind) {
		if got, _ := row["kind"].(string); got != "org_user" {
			t.Errorf("kind filter returned a %q row", got)
		}
	}

	// Scoped to one org, and only through a relationship with it.
	byOrg := admin.do(http.MethodGet, "/admin/users?org_id="+fix.orgID, nil).
		mustStatus(t, http.StatusOK, "filter by org")
	if !containsUserID(t, byOrg, fix.renterID) {
		t.Errorf("org filter lost the org's own renter — body: %s", byOrg.Raw)
	}

	// An org owner cannot reach it at all: the admin audience is a cookie, not
	// a stronger role.
	fix.owner.do(http.MethodGet, "/admin/users", nil).
		mustStatus(t, http.StatusUnauthorized, "org owner on the admin directory")
}

// TestPhase19AdminDetailAggregatesAndIsAudited: the page is a read, and the
// read is recorded, because it assembles cross-org PII.
func TestPhase19AdminDetailAggregatesAndIsAudited(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "DirDetail", "0719002100", "+255719002101")
	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": fix.amounts[0], "method": "cash",
	}).mustStatus(t, http.StatusCreated, "record payment")
	admin := h.adminClient(t)

	detail := admin.do(http.MethodGet, "/admin/users/"+fix.renterID, nil).
		mustStatus(t, http.StatusOK, "user detail")
	if got := detail.str(t, "user", "nida_masked"); strings.Contains(got, validNIDA) {
		t.Fatalf("the detail page carries the full NIDA number: %q", got)
	}
	if strings.Contains(detail.Raw, validNIDA) {
		t.Fatalf("the detail page carries the full NIDA number — body: %s", detail.Raw)
	}
	if rows := arrayOf(t, detail, "contracts"); len(rows) != 1 {
		t.Errorf("contracts = %d, want 1", len(rows))
	}
	payments, _ := detail.Body["payments"].(map[string]any)
	if payments == nil || int(mustFloat(t, payments, "count")) != 1 {
		t.Errorf("payments summary = %v, want one payment", payments)
	}
	if total := int64(mustFloat(t, payments, "total")); total != fix.amounts[0] {
		t.Errorf("payments total = %d, want %d", total, fix.amounts[0])
	}
	auditBlock, _ := detail.Body["audit"].(map[string]any)
	if auditBlock == nil {
		t.Fatalf("detail carries no audit block — body: %s", detail.Raw)
	}

	if rows := h.auditPayloads(t, "admin.user_view"); len(rows) != 1 {
		t.Errorf("admin.user_view rows = %d, want 1", len(rows))
	}
	admin.do(http.MethodGet, "/admin/users/"+fix.orgID, nil).
		mustStatus(t, http.StatusNotFound, "an org id is not a user id")
}

// TestPhase19AdminSuspendRevokesSessions: a suspended account is out of the
// product, not merely unable to sign in again.
func TestPhase19AdminSuspendRevokesSessions(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "DirSuspend", "0719002200", "+255719002201")
	admin := h.adminClient(t)

	fix.renter.do(http.MethodGet, "/me/profile", nil).
		mustStatus(t, http.StatusOK, "renter is signed in")

	suspended := admin.do(http.MethodPost, "/admin/users/"+fix.renterID+"/suspend",
		map[string]any{"reason": "fraud investigation"}).
		mustStatus(t, http.StatusOK, "suspend")
	if got := suspended.str(t, "user", "status"); got != "suspended" {
		t.Errorf("status = %q, want suspended", got)
	}
	// The live session is gone.
	fix.renter.do(http.MethodGet, "/me/profile", nil).
		mustStatus(t, http.StatusUnauthorized, "the suspended renter's session")

	admin.do(http.MethodPost, "/admin/users/"+fix.renterID+"/suspend",
		map[string]any{"reason": "again"}).
		mustStatus(t, http.StatusConflict, "suspend twice")

	admin.do(http.MethodPost, "/admin/users/"+fix.renterID+"/activate",
		map[string]any{"reason": "cleared"}).
		mustStatus(t, http.StatusOK, "activate")
	if rows := h.auditPayloads(t, "admin.user_suspend"); len(rows) != 1 {
		t.Errorf("admin.user_suspend rows = %d, want 1", len(rows))
	}
	if rows := h.auditPayloads(t, "admin.user_activate"); len(rows) != 1 {
		t.Errorf("admin.user_activate rows = %d, want 1", len(rows))
	}
	// A revoked session must stay revoked: reactivation restores the account,
	// not the cookie the suspended person was holding.
	fix.renter.do(http.MethodGet, "/me/profile", nil).
		mustStatus(t, http.StatusUnauthorized, "the old session after reactivation")
}

// TestPhase19AdminRenamesAnyoneWithAReason: no signed-contract restriction, but
// a reason is required and recorded.
func TestPhase19AdminRenamesAnyoneWithAReason(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "DirRename", "0719002300", "+255719002301")
	h.signAsRenter(t, fix.renter, fix.contractID, fix.renterPhone)
	admin := h.adminClient(t)

	admin.do(http.MethodPatch, "/admin/users/"+fix.renterID,
		map[string]any{"full_name": "Support Fixed"}).
		mustStatus(t, http.StatusBadRequest, "rename without a reason")

	renamed := admin.do(http.MethodPatch, "/admin/users/"+fix.renterID,
		map[string]any{"full_name": "Support Fixed", "reason": "ticket 4412"}).
		mustStatus(t, http.StatusOK, "rename a signed renter as platform admin")
	if got := renamed.str(t, "user", "full_name"); got != "Support Fixed" {
		t.Errorf("full_name = %q, want Support Fixed", got)
	}
	rows := h.auditPayloads(t, "admin.user_update")
	if len(rows) != 1 || !strings.Contains(rows[0], "ticket 4412") {
		t.Errorf("admin.user_update rows = %v, want one carrying the reason", rows)
	}
	if sent := ofKind(h.notifications(t), notify.KindNameCorrected); len(sent) != 1 {
		t.Errorf("name_corrected messages = %d, want 1", len(sent))
	}

	// The platform reveal needs a reason too, and is recorded as a platform act
	// — with no org_id, so it does not appear in a landlord's own audit page as
	// if they had made it.
	admin.do(http.MethodPost, "/admin/users/"+fix.renterID+"/nida/reveal", nil).
		mustStatus(t, http.StatusBadRequest, "platform reveal without a reason")
	admin.do(http.MethodPost, "/admin/users/"+fix.renterID+"/nida/reveal",
		map[string]any{"reason": "ticket 4412"}).
		mustStatus(t, http.StatusOK, "platform reveal")

	var orgless int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log
		   WHERE action = 'renter.nida_reveal' AND org_id IS NULL
		     AND after->>'actor_kind' = 'platform_admin'`).Scan(&orgless); err != nil {
		t.Fatalf("count platform reveals: %v", err)
	}
	if orgless != 1 {
		t.Errorf("org-less platform reveal rows = %d, want 1", orgless)
	}
	// And the renter sees it as a platform reveal.
	fix.renter.do(http.MethodPost, "/auth/login",
		map[string]any{"phone": fix.renterPhone, "pin": defaultPIN}).
		mustStatus(t, http.StatusOK, "renter signs back in")
	profile := fix.renter.do(http.MethodGet, "/me/profile", nil).
		mustStatus(t, http.StatusOK, "renter profile")
	seen := arrayOf(t, profile, "nida_reveals")
	if len(seen) != 1 {
		t.Fatalf("nida_reveals = %d, want 1 — body: %s", len(seen), profile.Raw)
	}
	if got, _ := seen[0]["by_kind"].(string); got != "platform_admin" {
		t.Errorf("by_kind = %q, want platform_admin", got)
	}
}

// ----------------------------------------------------------------- helpers --

// containsUserID reports whether a directory page holds one user id.
func containsUserID(t *testing.T, r response, id string) bool {
	t.Helper()
	for _, row := range listOf(t, r) {
		if got, _ := row["id"].(string); got == id {
			return true
		}
	}
	return false
}
