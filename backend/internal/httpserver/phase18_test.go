package httpserver_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
)

// Phase 18 — landlord-assisted onboarding (PLAN2 Phase 18, SPEC §5.15,
// FLOWS 2b).
//
// The feature is defined by what it does *not* change: the code goes into the
// slot the SMS path already writes, so POST /auth/otp/verify,
// POST /auth/register/renter and POST /contracts/{id}/sign are untouched. Most
// of what follows is therefore a test of those endpoints being driven by a
// code that never left the building.

// assistFixture is an org with a priced, vacant unit and a renter phone that
// has no account yet — the situation the landlord is standing in.
type assistFixture struct {
	h        *harness
	owner    *client
	orgID    string
	unitID   string
	unitCode string
	periodID string
	phone    string
}

func newAssistFixture(t *testing.T, h *harness, tag, ownerPhone, renterPhone string) assistFixture {
	t.Helper()
	fix := h.newOrgWithUnits(tag, strings.ToLower(tag)+"@jjne.test", ownerPhone,
		[]string{"Room 1", "Room 2"}, 250_000)
	return assistFixture{
		h: h, owner: fix.client, orgID: fix.orgID,
		unitID: fix.unitIDs[0], unitCode: fix.unitCodes[0],
		periodID: fix.client.periodIDByDays(t, 30),
		phone:    renterPhone,
	}
}

// open starts an assist session for the fixture's phone and unit.
func (f assistFixture) open(t *testing.T) response {
	t.Helper()
	return f.owner.do(http.MethodPost, "/assist", map[string]any{
		"phone": f.phone, "unit_id": f.unitID,
	})
}

// expire backdates a session's window, which is how a thirty-minute encounter
// is watched to lapse inside a one-second test.
func (h *harness) expireAssist(t *testing.T, sessionID string) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		"UPDATE assist_sessions SET expires_at = now() - interval '1 minute' WHERE id = $1",
		sessionID); err != nil {
		t.Fatalf("expire assist session: %v", err)
	}
}

// shownRow reads one in-person delivery-log row by its dedupe key.
type shownRow struct {
	ID      string
	Kind    string
	Channel string
	Status  string
	Body    string
	Phone   string
}

func (h *harness) shownNotification(t *testing.T, dedupeKey string) shownRow {
	t.Helper()
	var out shownRow
	if err := h.pool.QueryRow(context.Background(),
		`SELECT id::text, kind, channel, status, body, to_phone
		   FROM notification_log WHERE dedupe_key = $1`, dedupeKey).
		Scan(&out.ID, &out.Kind, &out.Channel, &out.Status, &out.Body, &out.Phone); err != nil {
		t.Fatalf("read notification_log row %q: %v", dedupeKey, err)
	}
	return out
}

// ------------------------------------------------- the code that is shown --

// TestAssistedCodeVerifiesThroughTheOrdinaryEndpoint is the whole point of the
// design: the landlord's screen is a different *channel*, not a different
// verification path.
func TestAssistedCodeVerifiesThroughTheOrdinaryEndpoint(t *testing.T) {
	h := newHarness(t)
	f := newAssistFixture(t, h, "AsstVerify", "0716180100", "+255716180101")

	opened := f.open(t).mustStatus(t, http.StatusCreated, "open assist session")
	code := opened.str(t, "code")
	if len(code) != 6 {
		t.Fatalf("assist code = %q, want six digits", code)
	}
	if got := opened.str(t, "session", "purpose"); got != "register" {
		t.Errorf("purpose = %q, want register for a number with no account", got)
	}
	if got := opened.str(t, "link"); !strings.Contains(got, f.unitCode) ||
		!strings.Contains(got, opened.str(t, "session", "id")) {
		t.Errorf("link = %q, want the unit code and the session id in it", got)
	}
	// Nothing was sent. The SMS capture is the provider; if it saw the code,
	// the feature has not been built.
	if h.sms.LastOTP(f.phone) != "" {
		t.Error("an assisted code reached the SMS provider; it must never be sent")
	}

	// The renter, on their own device, uses the unchanged endpoint.
	renter := h.client()
	verified := renter.do(http.MethodPost, "/auth/otp/verify", map[string]any{
		"phone": f.phone, "code": code, "purpose": "register",
	}).mustStatus(t, http.StatusOK, "verify the assisted code")
	if verified.str(t, "otp_token") == "" {
		t.Error("verifying an assisted code produced no otp_token")
	}
}

// TestAssistedIssueOverwritesAnInFlightSMSCode is the documented edge case
// (FLOWS 2b): the two paths share one slot per (purpose, phone), so a text
// still in flight carries a dead code once the landlord shows one.
func TestAssistedIssueOverwritesAnInFlightSMSCode(t *testing.T) {
	h := newHarness(t)
	f := newAssistFixture(t, h, "AsstOverwrite", "0716180110", "+255716180111")

	h.client().do(http.MethodPost, "/auth/otp/send",
		map[string]any{"phone": f.phone, "purpose": "register"}).
		mustStatus(t, http.StatusAccepted, "sms otp send")
	smsCode := h.sms.LastOTP(f.phone)
	if smsCode == "" {
		t.Fatal("no SMS OTP captured")
	}

	shown := f.open(t).mustStatus(t, http.StatusCreated, "open assist session").str(t, "code")
	if shown == smsCode {
		t.Fatal("the assisted code equals the texted one; the slot was not rewritten")
	}

	renter := h.client()
	renter.do(http.MethodPost, "/auth/otp/verify", map[string]any{
		"phone": f.phone, "code": smsCode, "purpose": "register",
	}).mustStatus(t, http.StatusBadRequest, "the superseded SMS code")
	renter.do(http.MethodPost, "/auth/otp/verify", map[string]any{
		"phone": f.phone, "code": shown, "purpose": "register",
	}).mustStatus(t, http.StatusOK, "the shown code")
}

// TestAssistedIssueBypassesTheResendCooldown checks that the bypass is
// assisted-only: the public send path still enforces its sixty seconds, which
// is what stops a stranger walking a stranger's inbox.
func TestAssistedIssueBypassesTheResendCooldown(t *testing.T) {
	h := newHarness(t)
	f := newAssistFixture(t, h, "AsstCooldown", "0716180120", "+255716180121")

	anon := h.client()
	anon.do(http.MethodPost, "/auth/otp/send",
		map[string]any{"phone": f.phone, "purpose": "register"}).
		mustStatus(t, http.StatusAccepted, "first sms otp send")
	anon.do(http.MethodPost, "/auth/otp/send",
		map[string]any{"phone": f.phone, "purpose": "register"}).
		mustStatus(t, http.StatusTooManyRequests, "second sms otp send inside the cooldown")

	// The landlord, standing next to the renter, is not stopped by it.
	opened := f.open(t).mustStatus(t, http.StatusCreated, "open assist session")
	sessionID := opened.str(t, "session", "id")
	first := opened.str(t, "code")

	refreshed := f.owner.do(http.MethodPost, "/assist/"+sessionID+"/code", nil).
		mustStatus(t, http.StatusOK, "new code inside the cooldown")
	if refreshed.str(t, "code") == first {
		t.Error("a refresh returned the same code")
	}
	if got := num(t, refreshed, "code_issued_count"); got != 2 {
		t.Errorf("code_issued_count = %v, want 2", got)
	}
}

// TestAssistPerOrgBudget is the limiter that replaces the per-phone one: the
// landlord vouches for the number, so the cap is on the organisation.
func TestAssistPerOrgBudget(t *testing.T) {
	h := newHarness(t)
	f := newAssistFixture(t, h, "AsstBudget", "0716180130", "+255716180131")

	// Three sessions of ten reveals each is the thirty the org may show in an
	// hour; the thirty-first call is refused.
	spent := 0
	for i := range 3 {
		phone := fmt.Sprintf("+2557161802%02d", i)
		opened := f.owner.do(http.MethodPost, "/assist",
			map[string]any{"phone": phone, "unit_id": f.unitID}).
			mustStatus(t, http.StatusCreated, "open assist session")
		spent++
		id := opened.str(t, "session", "id")
		for range 9 {
			f.owner.do(http.MethodPost, "/assist/"+id+"/code", nil).
				mustStatus(t, http.StatusOK, "new code")
			spent++
		}
		f.owner.do(http.MethodPost, "/assist/"+id+"/close", nil).
			mustStatus(t, http.StatusOK, "close session")
	}
	if spent != 30 {
		t.Fatalf("spent %d reveals, want 30", spent)
	}

	refused := f.owner.do(http.MethodPost, "/assist",
		map[string]any{"phone": "+255716180299", "unit_id": f.unitID})
	if refused.Code != http.StatusTooManyRequests {
		t.Fatalf("the thirty-first reveal: status = %d, want 429 — body: %s",
			refused.Code, refused.Raw)
	}
}

// TestAssistSessionCapsItsOwnReveals: ten codes in one session is not a typing
// problem, it is a session that should be closed and reopened.
func TestAssistSessionCapsItsOwnReveals(t *testing.T) {
	h := newHarness(t)
	f := newAssistFixture(t, h, "AsstCap", "0716180140", "+255716180141")

	id := f.open(t).mustStatus(t, http.StatusCreated, "open assist session").str(t, "session", "id")
	for i := range 9 {
		f.owner.do(http.MethodPost, "/assist/"+id+"/code", nil).
			mustStatus(t, http.StatusOK, fmt.Sprintf("code %d", i+2))
	}
	f.owner.do(http.MethodPost, "/assist/"+id+"/code", nil).
		mustStatus(t, http.StatusTooManyRequests, "the eleventh code")
}

// ---------------------------------------------------------- what is refused --

// TestAssistRefusesAStaffNumber is the rule that keeps the cooldown bypass
// from becoming a privilege escalation: a manager must not be able to mint a
// login code for the owner's own account.
func TestAssistRefusesAStaffNumber(t *testing.T) {
	h := newHarness(t)
	f := newAssistFixture(t, h, "AsstStaff", "0716180150", "+255716180151")

	// The owner's own number, in the E.164 form the account stores.
	refused := f.owner.do(http.MethodPost, "/assist",
		map[string]any{"phone": "+255716180150", "unit_id": f.unitID}).
		mustStatus(t, http.StatusConflict, "assisting a staff number")
	if got := refused.str(t, "type"); got != "not_a_renter_phone" {
		t.Errorf("problem type = %q, want not_a_renter_phone", got)
	}
}

// TestAssistRefusesASecondOpenSession: two landlords onboarding one number
// would write into one slot and cancel each other's code. The refusal names
// the session already running, which is the one the caller wants.
func TestAssistRefusesASecondOpenSession(t *testing.T) {
	h := newHarness(t)
	f := newAssistFixture(t, h, "AsstDup", "0716180160", "+255716180161")

	first := f.open(t).mustStatus(t, http.StatusCreated, "first session").str(t, "session", "id")

	second := f.open(t).mustStatus(t, http.StatusConflict, "second session")
	if got := second.str(t, "type"); got != "assist_open" {
		t.Errorf("problem type = %q, want assist_open", got)
	}
	if got := second.str(t, "session_id"); got != first {
		t.Errorf("session_id = %q, want the open session %q", got, first)
	}

	// Closing it frees the number again.
	f.owner.do(http.MethodPost, "/assist/"+first+"/close", nil).
		mustStatus(t, http.StatusOK, "close the first session")
	f.open(t).mustStatus(t, http.StatusCreated, "a session after the first was closed")
}

// TestAssistExpiredSessionRefusesANewCode: expiry and closure are one answer.
func TestAssistExpiredSessionRefusesANewCode(t *testing.T) {
	h := newHarness(t)
	f := newAssistFixture(t, h, "AsstExpiry", "0716180170", "+255716180171")

	id := f.open(t).mustStatus(t, http.StatusCreated, "open assist session").str(t, "session", "id")
	h.expireAssist(t, id)

	refused := f.owner.do(http.MethodPost, "/assist/"+id+"/code", nil).
		mustStatus(t, http.StatusConflict, "refresh an expired session")
	if got := refused.str(t, "type"); got != "assist_closed" {
		t.Errorf("problem type = %q, want assist_closed", got)
	}
	read := f.owner.do(http.MethodGet, "/assist/"+id, nil).
		mustStatus(t, http.StatusOK, "read an expired session")
	if got := read.str(t, "status_detail"); got != "closed" {
		t.Errorf("status_detail = %q, want closed for an expired session", got)
	}
	if got := read.str(t, "session", "status"); got != "closed" {
		t.Errorf("session.status = %q, want closed for an expired session", got)
	}
}

// TestAssistSessionIsOrgScoped: the session id travels in a QR link, so it
// must not be a handle another organisation can use.
func TestAssistSessionIsOrgScoped(t *testing.T) {
	h := newHarness(t)
	a := newAssistFixture(t, h, "AsstScopeA", "0716180180", "+255716180181")
	b := newAssistFixture(t, h, "AsstScopeB", "0716180190", "+255716180191")

	id := a.open(t).mustStatus(t, http.StatusCreated, "org A session").str(t, "session", "id")

	b.owner.do(http.MethodGet, "/assist/"+id, nil).
		mustStatus(t, http.StatusNotFound, "org B reading org A's session")
	b.owner.do(http.MethodPost, "/assist/"+id+"/code", nil).
		mustStatus(t, http.StatusNotFound, "org B refreshing org A's session")
	b.owner.do(http.MethodPost, "/assist/"+id+"/close", nil).
		mustStatus(t, http.StatusNotFound, "org B closing org A's session")

	listed := b.owner.do(http.MethodGet, "/assist", nil).
		mustStatus(t, http.StatusOK, "org B's own sessions")
	if strings.Contains(listed.Raw, id) {
		t.Errorf("org B's listing names org A's session: %s", listed.Raw)
	}
}

// TestPublicAssistLookupCarriesNoPhone: the renter's device resolves the link
// before it has an account, so the projection is the unit code and the purpose
// and nothing else.
func TestPublicAssistLookupCarriesNoPhone(t *testing.T) {
	h := newHarness(t)
	f := newAssistFixture(t, h, "AsstPublic", "0716180200", "+255716180201")

	id := f.open(t).mustStatus(t, http.StatusCreated, "open assist session").str(t, "session", "id")

	got := h.client().do(http.MethodGet, "/public/assist/"+id, nil).
		mustStatus(t, http.StatusOK, "public assist lookup")
	if got.str(t, "unit_code") != f.unitCode {
		t.Errorf("unit_code = %q, want %q", got.str(t, "unit_code"), f.unitCode)
	}
	if got.str(t, "purpose") != "register" {
		t.Errorf("purpose = %q, want register", got.str(t, "purpose"))
	}
	if got.str(t, "status") != "open" {
		t.Errorf("status = %q, want open", got.str(t, "status"))
	}
	if _, ok := got.Body["phone"]; ok {
		t.Errorf("the public lookup carries a phone field: %s", got.Raw)
	}
	if strings.Contains(got.Raw, strings.TrimPrefix(f.phone, "+")) {
		t.Errorf("the public lookup leaks the renter's number: %s", got.Raw)
	}
	if strings.Contains(got.Raw, f.orgID) {
		t.Errorf("the public lookup leaks the org id: %s", got.Raw)
	}

	h.client().do(http.MethodGet, "/public/assist/"+f.orgID, nil).
		mustStatus(t, http.StatusNotFound, "an unknown session id")
}

// ------------------------------------------------------------ the progress --

// TestAssistSessionFollowsTheRenter walks FLOWS 2b end to end and watches the
// landlord's status line move: waiting → registered → requested → approved.
func TestAssistSessionFollowsTheRenter(t *testing.T) {
	h := newHarness(t)
	f := newAssistFixture(t, h, "AsstFlow", "0716180210", "+255716180211")

	opened := f.open(t).mustStatus(t, http.StatusCreated, "open assist session")
	id := opened.str(t, "session", "id")
	code := opened.str(t, "code")

	if got := f.detail(t, id).str(t, "status_detail"); got != "waiting" {
		t.Fatalf("status_detail = %q, want waiting", got)
	}

	// The renter registers on their own device with the shown code.
	renter := h.client()
	token := renter.do(http.MethodPost, "/auth/otp/verify", map[string]any{
		"phone": f.phone, "code": code, "purpose": "register",
	}).mustStatus(t, http.StatusOK, "verify").str(t, "otp_token")
	renter.do(http.MethodPost, "/auth/register/renter", map[string]any{
		"phone": f.phone, "otp_token": token, "pin": defaultPIN, "full_name": "Asha Assisted",
	}).mustStatus(t, http.StatusCreated, "register")

	registered := f.detail(t, id)
	if got := registered.str(t, "status_detail"); got != "registered" {
		t.Fatalf("status_detail = %q, want registered — body: %s", got, registered.Raw)
	}
	if got := registered.str(t, "renter", "full_name"); got != "Asha Assisted" {
		t.Errorf("renter.full_name = %q, want the account just created", got)
	}

	// …and applies for the unit the session was opened for.
	renter.completeProfile(t, "Asha Assisted", validNIDA)
	applied := renter.do(http.MethodPost, "/units/"+f.unitCode+"/link",
		linkBody(f.periodID, testTermDays)).
		mustStatus(t, http.StatusCreated, "apply").str(t, "request", "id")

	requested := f.detail(t, id)
	if got := requested.str(t, "status_detail"); got != "requested" {
		t.Fatalf("status_detail = %q, want requested — body: %s", got, requested.Raw)
	}
	if got := requested.str(t, "link_request", "id"); got != applied {
		t.Errorf("link_request.id = %q, want %q", got, applied)
	}

	f.owner.do(http.MethodPost, "/link-requests/"+applied+"/approve", nil).
		mustStatus(t, http.StatusOK, "approve")
	if got := f.detail(t, id).str(t, "status_detail"); got != "approved" {
		t.Errorf("status_detail = %q, want approved", got)
	}
}

func (f assistFixture) detail(t *testing.T, id string) response {
	t.Helper()
	return f.owner.do(http.MethodGet, "/assist/"+id, nil).
		mustStatus(t, http.StatusOK, "read assist session")
}

// TestAssistedLoginStampsTheSession covers the other half of the derivation:
// a number that already has an account gets the `login` slot, and verifying
// against it stamps the same session.
func TestAssistedLoginStampsTheSession(t *testing.T) {
	h := newHarness(t)
	f := newAssistFixture(t, h, "AsstLogin", "0716180220", "+255716180221")
	h.registerRenter(f.phone, "Returning Renter", defaultPIN)

	opened := f.open(t).mustStatus(t, http.StatusCreated, "open assist session")
	if got := opened.str(t, "session", "purpose"); got != "login" {
		t.Fatalf("purpose = %q, want login for a number that already has an account", got)
	}
	id := opened.str(t, "session", "id")

	renter := h.client()
	renter.do(http.MethodPost, "/auth/otp/verify", map[string]any{
		"phone": f.phone, "code": opened.str(t, "code"), "purpose": "login",
	}).mustStatus(t, http.StatusOK, "assisted login")
	if !renter.hasCookie("tms_r") {
		t.Fatal("the assisted login did not set tms_r")
	}
	if got := f.detail(t, id).str(t, "status_detail"); got != "registered" {
		t.Errorf("status_detail = %q, want registered after an assisted login", got)
	}
}

// ---------------------------------------------------------- the delivery log --

// TestAssistedRevealIsLoggedButNeverSent: the reveal is recorded, the code is
// not, no credit moves, and the SMS worker can never pick the row up.
func TestAssistedRevealIsLoggedButNeverSent(t *testing.T) {
	h := newHarness(t)
	f := newAssistFixture(t, h, "AsstLog", "0716180230", "+255716180231")

	before := h.creditBalance(t, f.orgID)
	id := f.open(t).mustStatus(t, http.StatusCreated, "open assist session").str(t, "session", "id")

	row := h.shownNotification(t, "assist:"+id+":1")
	if row.Kind != "otp" {
		t.Errorf("kind = %q, want otp", row.Kind)
	}
	if row.Channel != "in_person" {
		t.Errorf("channel = %q, want in_person", row.Channel)
	}
	if row.Status != "shown" {
		t.Errorf("status = %q, want shown", row.Status)
	}
	if row.Body != "" {
		t.Errorf("body = %q, want empty — the code must never be persisted", row.Body)
	}
	if row.Phone != f.phone {
		t.Errorf("to_phone = %q, want %q", row.Phone, f.phone)
	}

	// The worker's claim requires `queued`, so an in-person row is unreachable
	// from the send path — nothing can turn a shown code into a text.
	q := sqlc.New(h.pool)
	rowID, err := db.ParseUUID(row.ID)
	if err != nil {
		t.Fatalf("parse notification id: %v", err)
	}
	if _, err := q.ClaimNotification(context.Background(), rowID); err == nil {
		t.Error("the SMS worker claimed an in-person row; a shown code must never be sent")
	}

	if after := h.creditBalance(t, f.orgID); after != before {
		t.Errorf("sms credits moved from %d to %d; an in-person code costs nothing", before, after)
	}

	// A refresh writes its own row, keyed by the session's running count.
	f.owner.do(http.MethodPost, "/assist/"+id+"/code", nil).
		mustStatus(t, http.StatusOK, "new code")
	if second := h.shownNotification(t, "assist:"+id+":2"); second.Status != "shown" {
		t.Errorf("the second reveal's status = %q, want shown", second.Status)
	}
}

// creditBalance reads an org's prepaid SMS balance straight out of the table.
func (h *harness) creditBalance(t *testing.T, orgID string) int {
	t.Helper()
	var balance int
	if err := h.pool.QueryRow(context.Background(),
		"SELECT COALESCE(balance, 0) FROM org_sms_credits WHERE org_id = $1", orgID).
		Scan(&balance); err != nil {
		t.Fatalf("read sms credits: %v", err)
	}
	return balance
}

// ---------------------------------------------------------- witnessed signing --

// TestWitnessedSignatureRecordsTheWitness is FLOWS 2b.6: the renter signs, on
// their own device, with a code the landlord showed — and the document says
// who was standing there.
func TestWitnessedSignatureRecordsTheWitness(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "Witness", "0716180240", "+255716180241")

	ownerID := fix.owner.do(http.MethodGet, "/auth/me?audience=org", nil).
		mustStatus(t, http.StatusOK, "owner me").str(t, "user", "id")

	shown := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/witness-otp", nil).
		mustStatus(t, http.StatusOK, "witness otp")
	code := shown.str(t, "code")
	if len(code) != 6 {
		t.Fatalf("witness code = %q, want six digits", code)
	}
	// Nothing was sent: the code exists on the landlord's screen and in Redis.
	for _, m := range h.sms.Messages() {
		if strings.Contains(m.Body, code) {
			t.Fatalf("a witnessed signing code reached the SMS provider: %q", m.Body)
		}
	}

	fix.renter.do(http.MethodPost, "/contracts/"+fix.contractID+"/sign",
		map[string]any{"otp_code": code}).mustStatus(t, http.StatusOK, "sign with the shown code")

	got := fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID, nil).
		mustStatus(t, http.StatusOK, "read contract")
	sig := signatureOfParty(t, got, "renter")
	witness, ok := sig["witnessed_by"].(map[string]any)
	if !ok {
		t.Fatalf("the renter's signature carries no witness: %s", got.Raw)
	}
	if witness["id"] != ownerID {
		t.Errorf("witnessed_by.id = %v, want the owner %q", witness["id"], ownerID)
	}
	if witness["full_name"] == "" {
		t.Error("witnessed_by carries no name")
	}
	if sig["method"] != "otp_accept" {
		t.Errorf("method = %v, want otp_accept — the renter still signed", sig["method"])
	}

	// The document says it too, which is where a reader of the contract looks.
	doc := fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID+"/document", nil).
		mustStatus(t, http.StatusOK, "contract document")
	if !strings.Contains(doc.Raw, ownerID) {
		t.Errorf("the document's signature block names no witness: %s", doc.Raw)
	}
}

// TestSMSSignatureRecordsNoWitness is the control: the ordinary path must not
// acquire a witness it never had.
func TestSMSSignatureRecordsNoWitness(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "NoWitness", "0716180250", "+255716180251")

	h.signAsRenter(t, fix.renter, fix.contractID, fix.renterPhone)

	got := fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID, nil).
		mustStatus(t, http.StatusOK, "read contract")
	if w := signatureOfParty(t, got, "renter")["witnessed_by"]; w != nil {
		t.Errorf("an SMS-signed contract carries a witness: %v", w)
	}
}

// TestWitnessOTPRefusesASignedContract keeps the witness route in step with
// the sign-otp route it shadows.
func TestWitnessOTPRefusesASignedContract(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "WitnessTwice", "0716180260", "+255716180261")
	h.signAsRenter(t, fix.renter, fix.contractID, fix.renterPhone)

	refused := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/witness-otp", nil).
		mustStatus(t, http.StatusConflict, "witness a signed contract")
	if got := refused.str(t, "type"); got != "already_signed" {
		t.Errorf("problem type = %q, want already_signed", got)
	}
}

// signatureOfParty pulls one party's signature block out of a contract read.
func signatureOfParty(t *testing.T, r response, party string) map[string]any {
	t.Helper()
	contract, ok := r.Body["contract"].(map[string]any)
	if !ok {
		t.Fatalf("response carries no contract: %s", r.Raw)
	}
	sigs, _ := contract["signatures"].([]any)
	for _, raw := range sigs {
		if m, ok := raw.(map[string]any); ok && m["party"] == party {
			return m
		}
	}
	t.Fatalf("no %s signature in: %s", party, r.Raw)
	return nil
}
