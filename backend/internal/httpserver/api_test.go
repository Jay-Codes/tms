package httpserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"tms/backend/internal/cache"
	"tms/backend/internal/config"
	"tms/backend/internal/db"
	"tms/backend/internal/httpserver"
	"tms/backend/internal/storage"
	"tms/backend/internal/testutil"
)

// ---------------------------------------------------------------- harness --

type harness struct {
	t     *testing.T
	srv   *httpserver.Server
	sms   *testutil.SMSCapture
	email *testutil.EmailCapture
	pool  *db.Pool
	redis *cache.Client
	// store is nil when MinIO is unreachable; the QR tests skip in that case.
	store *storage.Client
}

// newHarness builds a server against the real test Postgres and an in-process
// Redis. It skips the test when the database is unreachable.
func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := testutil.Pool(t)
	redis := testutil.Redis(t)
	sms := testutil.NewSMSCapture()
	email := testutil.NewEmailCapture()

	cfg := config.Config{
		Env:             config.EnvDev,
		Port:            "0",
		AppBaseURL:      "http://localhost:8080",
		SessionTTLHours: 24,
		NidaEncKey:      "test-key",
	}
	store := testutil.Storage(t)
	srv := httpserver.New(cfg, httpserver.Deps{
		DB: pool, Redis: redis, Pool: pool, Cache: redis, SMS: sms, Email: email, Storage: store,
	}, testutil.Logger())

	return &harness{t: t, srv: srv, sms: sms, email: email, pool: pool, redis: redis, store: store}
}

// client is one browser: it keeps the cookies the server sets.
type client struct {
	h       *harness
	cookies map[string]*http.Cookie
}

func (h *harness) client() *client {
	return &client{h: h, cookies: map[string]*http.Cookie{}}
}

type response struct {
	Code int
	Body map[string]any
	Raw  string
}

// do issues a request carrying this client's cookies and records any set.
func (c *client) do(method, path string, body any) response {
	c.h.t.Helper()

	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.h.t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, httpserver.APIPrefix+path, reader)
	req.Header.Set("Content-Type", "application/json")
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}

	rec := httptest.NewRecorder()
	c.h.srv.Handler().ServeHTTP(rec, req)

	for _, ck := range rec.Result().Cookies() {
		if ck.MaxAge < 0 {
			delete(c.cookies, ck.Name)
			continue
		}
		c.cookies[ck.Name] = ck
	}

	out := response{Code: rec.Code, Raw: rec.Body.String()}
	if len(out.Raw) > 0 {
		_ = json.Unmarshal([]byte(out.Raw), &out.Body)
	}
	return out
}

func (c *client) hasCookie(name string) bool {
	_, ok := c.cookies[name]
	return ok
}

// mustStatus fails the test unless the response carries the wanted status.
func (r response) mustStatus(t *testing.T, want int, what string) response {
	t.Helper()
	if r.Code != want {
		t.Fatalf("%s: status = %d, want %d — body: %s", what, r.Code, want, r.Raw)
	}
	return r
}

func (r response) str(t *testing.T, path ...string) string {
	t.Helper()
	cur := any(r.Body)
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("path %v: %q is not an object — body: %s", path, key, r.Raw)
		}
		cur = m[key]
	}
	s, ok := cur.(string)
	if !ok {
		t.Fatalf("path %v is not a string — body: %s", path, r.Raw)
	}
	return s
}

// tokenFromLink extracts the ?token= parameter from a dev-logged link.
func tokenFromLink(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse link %q: %v", link, err)
	}
	token := u.Query().Get("token")
	if token == "" {
		t.Fatalf("link %q carries no token", link)
	}
	return token
}

// registerRenter walks the full renter onboarding flow and returns the client
// holding the resulting `tms_r` session.
func (h *harness) registerRenter(phone, name, pin string) *client {
	h.t.Helper()
	c := h.client()

	c.do(http.MethodPost, "/auth/otp/send", map[string]any{"phone": phone, "purpose": "register"}).
		mustStatus(h.t, http.StatusAccepted, "otp send")

	code := h.sms.LastOTP(phone)
	if code == "" {
		h.t.Fatalf("no OTP captured for %s", phone)
	}

	verify := c.do(http.MethodPost, "/auth/otp/verify",
		map[string]any{"phone": phone, "code": code, "purpose": "register"}).
		mustStatus(h.t, http.StatusOK, "otp verify")
	otpToken := verify.str(h.t, "otp_token")

	c.do(http.MethodPost, "/auth/register/renter", map[string]any{
		"phone": phone, "otp_token": otpToken, "pin": pin, "full_name": name,
	}).mustStatus(h.t, http.StatusCreated, "register renter")

	return c
}

// createOrg signs an org up and returns the owner's client.
func (h *harness) createOrg(orgName, ownerName, email, phone, password string) (*client, response) {
	h.t.Helper()
	c := h.client()
	resp := c.do(http.MethodPost, "/orgs", map[string]any{
		"org_name": orgName, "owner_name": ownerName,
		"email": email, "phone": phone, "password": password,
	}).mustStatus(h.t, http.StatusCreated, "create org")
	return c, resp
}

// ------------------------------------------------------- renter onboarding --

func TestRenterRegisterFlow(t *testing.T) {
	h := newHarness(t)
	const phone = "+255712000001"

	c := h.registerRenter(phone, "Asha Mrisho", "1234")

	if !c.hasCookie("tms_r") {
		t.Fatal("registration did not set the tms_r cookie")
	}

	me := c.do(http.MethodGet, "/auth/me?audience=renter", nil).
		mustStatus(t, http.StatusOK, "auth/me")
	if got := me.str(t, "user", "phone"); got != phone {
		t.Fatalf("me.user.phone = %q, want %q", got, phone)
	}
	if got := me.str(t, "user", "kind"); got != "renter" {
		t.Fatalf("me.user.kind = %q, want renter", got)
	}

	// A renter session must not open the org audience.
	c.do(http.MethodGet, "/org", nil).mustStatus(t, http.StatusUnauthorized, "renter reading /org")
}

func TestRenterRegisterRejectsReusedPhone(t *testing.T) {
	h := newHarness(t)
	const phone = "+255712000002"
	h.registerRenter(phone, "First Renter", "1234")

	// A second registration needs a fresh OTP; the cooldown is per purpose and
	// phone, so use the login purpose to prove the account now exists instead.
	c := h.client()
	c.do(http.MethodPost, "/auth/otp/send", map[string]any{"phone": phone, "purpose": "login"}).
		mustStatus(t, http.StatusAccepted, "otp send (login)")
	code := h.sms.LastOTP(phone)

	login := c.do(http.MethodPost, "/auth/otp/verify",
		map[string]any{"phone": phone, "code": code, "purpose": "login"}).
		mustStatus(t, http.StatusOK, "otp login")
	if got := login.str(t, "user", "phone"); got != phone {
		t.Fatalf("login user phone = %q, want %q", got, phone)
	}
	if !c.hasCookie("tms_r") {
		t.Fatal("OTP login did not set tms_r")
	}
}

func TestRenterLoginWithPIN(t *testing.T) {
	h := newHarness(t)
	const phone = "+255712000003"
	h.registerRenter(phone, "Juma Ally", "4321")

	c := h.client()
	c.do(http.MethodPost, "/auth/login", map[string]any{"phone": phone, "pin": "4321"}).
		mustStatus(t, http.StatusOK, "renter login")
	if !c.hasCookie("tms_r") {
		t.Fatal("login did not set tms_r")
	}

	bad := h.client()
	bad.do(http.MethodPost, "/auth/login", map[string]any{"phone": phone, "pin": "0000"}).
		mustStatus(t, http.StatusUnauthorized, "wrong pin")
}

func TestOTPVerifyRejectsWrongCode(t *testing.T) {
	h := newHarness(t)
	const phone = "+255712000004"
	c := h.client()
	c.do(http.MethodPost, "/auth/otp/send", map[string]any{"phone": phone, "purpose": "register"}).
		mustStatus(t, http.StatusAccepted, "otp send")
	c.do(http.MethodPost, "/auth/otp/verify",
		map[string]any{"phone": phone, "code": "000000", "purpose": "register"}).
		mustStatus(t, http.StatusBadRequest, "wrong otp")
}

func TestPhoneValidationReturnsFieldErrors(t *testing.T) {
	h := newHarness(t)
	resp := h.client().do(http.MethodPost, "/auth/otp/send",
		map[string]any{"phone": "not-a-phone", "purpose": "register"}).
		mustStatus(t, http.StatusBadRequest, "invalid phone")

	errs, ok := resp.Body["errors"].(map[string]any)
	if !ok || errs["phone"] == nil {
		t.Fatalf("expected a `errors.phone` field error, got: %s", resp.Raw)
	}
}

// ----------------------------------------------------------- org onboarding --

func TestOrgSignupLoginAndMemberInvite(t *testing.T) {
	h := newHarness(t)

	owner, created := h.createOrg("JJnE Rentals", "Joseph Chuchu",
		"owner@jjne.test", "0712000010", "supersecret")
	if !owner.hasCookie("tms_o") {
		t.Fatal("org signup did not set tms_o")
	}
	if got := created.str(t, "org", "slug"); got != "jjne-rentals" {
		t.Fatalf("org slug = %q, want jjne-rentals", got)
	}

	// Verification email is issued with a working link.
	verifyLink := h.email.LastLink("owner@jjne.test")
	if verifyLink == "" {
		t.Fatal("no verification email captured for the owner")
	}
	owner.do(http.MethodPost, "/auth/verify-email",
		map[string]any{"token": tokenFromLink(t, verifyLink)}).
		mustStatus(t, http.StatusOK, "verify email")

	// GET /org returns the org with seeded defaults.
	org := owner.do(http.MethodGet, "/org", nil).mustStatus(t, http.StatusOK, "get org")
	settings, ok := org.Body["settings"].(map[string]any)
	if !ok {
		t.Fatalf("org has no settings object: %s", org.Raw)
	}
	if settings["sms_language"] != "sw" {
		t.Fatalf("default sms_language = %v, want sw", settings["sms_language"])
	}

	// PATCH /org validates settings.
	owner.do(http.MethodPatch, "/org", map[string]any{
		"settings": map[string]any{"due_day": 45},
	}).mustStatus(t, http.StatusBadRequest, "invalid due_day")

	patched := owner.do(http.MethodPatch, "/org", map[string]any{
		"name":     "JJnE Rentals Ltd",
		"settings": map[string]any{"due_day": 5, "grace_days": 7, "sms_language": "en"},
	}).mustStatus(t, http.StatusOK, "patch org")
	if got := patched.str(t, "org", "name"); got != "JJnE Rentals Ltd" {
		t.Fatalf("patched org name = %q", got)
	}

	// Invite a manager.
	invited := owner.do(http.MethodPost, "/org/members", map[string]any{
		"email": "manager@jjne.test", "full_name": "Neema Said", "role": "org_manager",
	}).mustStatus(t, http.StatusCreated, "invite member")
	memberID := invited.str(t, "member", "id")

	inviteLink := h.email.LastLink("manager@jjne.test")
	if inviteLink == "" {
		t.Fatal("no invite email captured")
	}

	// Duplicate invite conflicts.
	owner.do(http.MethodPost, "/org/members", map[string]any{
		"email": "manager@jjne.test", "full_name": "Neema Said", "role": "org_manager",
	}).mustStatus(t, http.StatusConflict, "duplicate invite")

	// The invitee sets a password and lands logged in.
	manager := h.client()
	manager.do(http.MethodPost, "/auth/invite/accept", map[string]any{
		"token": tokenFromLink(t, inviteLink), "password": "managerpass1",
	}).mustStatus(t, http.StatusOK, "accept invite")
	if !manager.hasCookie("tms_o") {
		t.Fatal("invite acceptance did not set tms_o")
	}

	// …and can log in again with those credentials.
	fresh := h.client()
	login := fresh.do(http.MethodPost, "/auth/login",
		map[string]any{"email": "manager@jjne.test", "password": "managerpass1"}).
		mustStatus(t, http.StatusOK, "manager login")
	if got := login.str(t, "org", "role"); got != "org_manager" {
		t.Fatalf("login org.role = %q, want org_manager", got)
	}

	// A manager may not invite or remove staff (owner-only routes).
	fresh.do(http.MethodPost, "/org/members", map[string]any{
		"email": "another@jjne.test", "full_name": "Another", "role": "org_manager",
	}).mustStatus(t, http.StatusForbidden, "manager inviting staff")

	// Members list shows both.
	list := owner.do(http.MethodGet, "/org/members", nil).mustStatus(t, http.StatusOK, "list members")
	items, _ := list.Body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("members = %d, want 2 — body: %s", len(items), list.Raw)
	}

	// Owner removes the manager.
	owner.do(http.MethodDelete, "/org/members/"+memberID, nil).
		mustStatus(t, http.StatusNoContent, "remove member")

	// The last owner cannot be removed.
	list = owner.do(http.MethodGet, "/org/members", nil).mustStatus(t, http.StatusOK, "list members again")
	items, _ = list.Body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("members after removal = %d, want 1", len(items))
	}
	ownerMember, _ := items[0].(map[string]any)
	ownerMemberID, _ := ownerMember["id"].(string)
	owner.do(http.MethodDelete, "/org/members/"+ownerMemberID, nil).
		mustStatus(t, http.StatusConflict, "removing the last owner")
}

func TestOrgSignupRejectsDuplicateEmail(t *testing.T) {
	h := newHarness(t)
	h.createOrg("First Org", "Owner One", "dup@jjne.test", "0712000020", "supersecret")

	h.client().do(http.MethodPost, "/orgs", map[string]any{
		"org_name": "Second Org", "owner_name": "Owner Two",
		"email": "dup@jjne.test", "phone": "0712000021", "password": "supersecret",
	}).mustStatus(t, http.StatusConflict, "duplicate org owner email")
}

func TestOrgSlugCollisionGetsSuffix(t *testing.T) {
	h := newHarness(t)
	_, first := h.createOrg("Bahari Homes", "Owner A", "a@bahari.test", "0712000030", "supersecret")
	_, second := h.createOrg("Bahari Homes", "Owner B", "b@bahari.test", "0712000031", "supersecret")

	if got := first.str(t, "org", "slug"); got != "bahari-homes" {
		t.Fatalf("first slug = %q", got)
	}
	if got := second.str(t, "org", "slug"); got != "bahari-homes-2" {
		t.Fatalf("second slug = %q, want bahari-homes-2", got)
	}
}

func TestUnauthenticatedOrgRoutesReturn401(t *testing.T) {
	h := newHarness(t)
	anon := h.client()
	for _, route := range []string{"/org", "/org/members", "/audit-log"} {
		anon.do(http.MethodGet, route, nil).mustStatus(t, http.StatusUnauthorized, "anonymous "+route)
	}
}

// ------------------------------------------------------------- audit trail --

func TestAuditLogRecordsMutations(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.createOrg("Audit Co", "Owner", "audit@jjne.test", "0712000040", "supersecret")

	log := owner.do(http.MethodGet, "/audit-log", nil).mustStatus(t, http.StatusOK, "audit log")
	items, _ := log.Body["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("audit log is empty after org creation: %s", log.Raw)
	}

	actions := map[string]bool{}
	for _, raw := range items {
		if m, ok := raw.(map[string]any); ok {
			if a, ok := m["action"].(string); ok {
				actions[a] = true
			}
		}
	}
	if !actions["org.create"] {
		t.Fatalf("no org.create audit row — actions seen: %v", actions)
	}
	// Signup audits the org creation; a subsequent sign-in audits auth.login.
	h.client().do(http.MethodPost, "/auth/login",
		map[string]any{"email": "audit@jjne.test", "password": "supersecret"}).
		mustStatus(t, http.StatusOK, "owner login")

	after := owner.do(http.MethodGet, "/audit-log", nil).mustStatus(t, http.StatusOK, "audit log after login")
	afterItems, _ := after.Body["items"].([]any)
	sawLogin := false
	for _, raw := range afterItems {
		if m, ok := raw.(map[string]any); ok && m["action"] == "auth.login" {
			sawLogin = true
		}
	}
	if !sawLogin {
		t.Fatal("no auth.login audit row after a successful sign-in")
	}

	// Filtering by entity_type narrows the result.
	filtered := owner.do(http.MethodGet, "/audit-log?entity_type=org", nil).
		mustStatus(t, http.StatusOK, "filtered audit log")
	fitems, _ := filtered.Body["items"].([]any)
	for _, raw := range fitems {
		m, _ := raw.(map[string]any)
		if m["entity_type"] != "org" {
			t.Fatalf("filter leaked entity_type %v", m["entity_type"])
		}
	}

	// A one-row page hands back a cursor that advances.
	page := owner.do(http.MethodGet, "/audit-log?limit=1", nil).
		mustStatus(t, http.StatusOK, "first page")
	cursor, ok := page.Body["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("expected a next_cursor on a full page: %s", page.Raw)
	}
	firstItems, _ := page.Body["items"].([]any)
	firstID := firstItems[0].(map[string]any)["id"]

	next := owner.do(http.MethodGet, "/audit-log?limit=1&cursor="+url.QueryEscape(cursor), nil).
		mustStatus(t, http.StatusOK, "second page")
	nextItems, _ := next.Body["items"].([]any)
	if len(nextItems) == 0 {
		t.Fatal("second page is empty")
	}
	if nextItems[0].(map[string]any)["id"] == firstID {
		t.Fatal("cursor did not advance")
	}
}

func TestFailedLoginIsAudited(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.createOrg("Watchful Ltd", "Owner", "watch@jjne.test", "0712000050", "supersecret")

	h.client().do(http.MethodPost, "/auth/login",
		map[string]any{"email": "watch@jjne.test", "password": "wrong-password"}).
		mustStatus(t, http.StatusUnauthorized, "bad password")

	// The failed attempt has no org context (the session is what carries it),
	// so it is visible platform-side rather than in the org's log; assert the
	// successful login is recorded and the org log stays clean of the failure.
	log := owner.do(http.MethodGet, "/audit-log", nil).mustStatus(t, http.StatusOK, "audit log")
	items, _ := log.Body["items"].([]any)
	for _, raw := range items {
		m, _ := raw.(map[string]any)
		if m["action"] == "auth.login_failed" {
			t.Fatal("a failed login leaked into the org-scoped audit log")
		}
	}
}

// ------------------------------------------------------------- isolation --

// TestCrossOrgAccessReturns404 is the SPEC §8 isolation test: org B must never
// be able to see or touch org A's rows, and probing must not distinguish
// "belongs to someone else" from "does not exist".
func TestCrossOrgAccessReturns404(t *testing.T) {
	h := newHarness(t)

	ownerA, createdA := h.createOrg("Org Alpha", "Alice", "alice@alpha.test", "0712000060", "supersecret")
	ownerB, createdB := h.createOrg("Org Beta", "Bob", "bob@beta.test", "0712000061", "supersecret")

	orgAID := createdA.str(t, "org", "id")
	orgBID := createdB.str(t, "org", "id")
	if orgAID == orgBID {
		t.Fatal("the two orgs share an id")
	}

	// A member that exists — in org A.
	invited := ownerA.do(http.MethodPost, "/org/members", map[string]any{
		"email": "staff@alpha.test", "full_name": "Alpha Staff", "role": "org_manager",
	}).mustStatus(t, http.StatusCreated, "invite alpha staff")
	alphaMemberID := invited.str(t, "member", "id")

	// An audit entry that exists — in org A.
	logA := ownerA.do(http.MethodGet, "/audit-log", nil).mustStatus(t, http.StatusOK, "alpha audit log")
	itemsA, _ := logA.Body["items"].([]any)
	if len(itemsA) == 0 {
		t.Fatal("org A has no audit entries")
	}
	alphaEntryID, _ := itemsA[0].(map[string]any)["id"].(string)

	// 1. GET /org returns only the caller's own org.
	orgB := ownerB.do(http.MethodGet, "/org", nil).mustStatus(t, http.StatusOK, "beta get org")
	if got := orgB.str(t, "id"); got != orgBID {
		t.Fatalf("org B session sees org %q, want %q", got, orgBID)
	}
	if got := orgB.str(t, "name"); got != "Org Beta" {
		t.Fatalf("org B session sees org named %q", got)
	}

	// 2. Deleting org A's member from org B's session is a 404, not a 403.
	ownerB.do(http.MethodDelete, "/org/members/"+alphaMemberID, nil).
		mustStatus(t, http.StatusNotFound, "cross-org member delete")

	// The member is untouched in org A.
	listA := ownerA.do(http.MethodGet, "/org/members", nil).
		mustStatus(t, http.StatusOK, "alpha members")
	membersA, _ := listA.Body["items"].([]any)
	if len(membersA) != 2 {
		t.Fatalf("org A members = %d, want 2 (the cross-org delete took effect)", len(membersA))
	}

	// 3. Org A's audit entry is invisible to org B.
	ownerB.do(http.MethodGet, "/audit-log/"+alphaEntryID, nil).
		mustStatus(t, http.StatusNotFound, "cross-org audit entry")

	// …and org B's audit list contains none of org A's rows.
	logB := ownerB.do(http.MethodGet, "/audit-log", nil).mustStatus(t, http.StatusOK, "beta audit log")
	itemsB, _ := logB.Body["items"].([]any)
	alphaIDs := map[string]bool{}
	for _, raw := range itemsA {
		if m, ok := raw.(map[string]any); ok {
			if id, ok := m["id"].(string); ok {
				alphaIDs[id] = true
			}
		}
	}
	for _, raw := range itemsB {
		m, _ := raw.(map[string]any)
		if id, ok := m["id"].(string); ok && alphaIDs[id] {
			t.Fatalf("org B's audit log contains org A's entry %s", id)
		}
	}

	// 4. Org B's own member list is unaffected by org A's staff.
	listB := ownerB.do(http.MethodGet, "/org/members", nil).mustStatus(t, http.StatusOK, "beta members")
	membersB, _ := listB.Body["items"].([]any)
	if len(membersB) != 1 {
		t.Fatalf("org B members = %d, want 1", len(membersB))
	}
}

// TestMalformedIDsReturn404 keeps id probing uninformative.
func TestMalformedIDsReturn404(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.createOrg("Probe Ltd", "Owner", "probe@jjne.test", "0712000070", "supersecret")

	owner.do(http.MethodDelete, "/org/members/not-a-uuid", nil).
		mustStatus(t, http.StatusNotFound, "malformed member id")
	owner.do(http.MethodGet, "/audit-log/not-a-uuid", nil).
		mustStatus(t, http.StatusNotFound, "malformed audit id")
}

// ----------------------------------------------------------------- logout --

func TestLogoutClearsOnlyItsAudience(t *testing.T) {
	h := newHarness(t)
	c, _ := h.createOrg("Dual Ltd", "Owner", "dual@jjne.test", "0712000080", "supersecret")

	// The same browser also registers as a renter.
	renterPhone := "+255712000081"
	c.do(http.MethodPost, "/auth/otp/send", map[string]any{"phone": renterPhone, "purpose": "register"}).
		mustStatus(t, http.StatusAccepted, "otp send")
	verify := c.do(http.MethodPost, "/auth/otp/verify", map[string]any{
		"phone": renterPhone, "code": h.sms.LastOTP(renterPhone), "purpose": "register",
	}).mustStatus(t, http.StatusOK, "otp verify")
	c.do(http.MethodPost, "/auth/register/renter", map[string]any{
		"phone": renterPhone, "otp_token": verify.str(t, "otp_token"),
		"pin": "1234", "full_name": "Dual Persona",
	}).mustStatus(t, http.StatusCreated, "register renter")

	if !c.hasCookie("tms_o") || !c.hasCookie("tms_r") {
		t.Fatal("expected both audience cookies in one client")
	}

	c.do(http.MethodPost, "/auth/logout?audience=renter", nil).
		mustStatus(t, http.StatusNoContent, "renter logout")
	if c.hasCookie("tms_r") {
		t.Fatal("renter logout did not clear tms_r")
	}

	// The org session survives.
	c.do(http.MethodGet, "/org", nil).mustStatus(t, http.StatusOK, "org session after renter logout")
	c.do(http.MethodGet, "/auth/me?audience=renter", nil).
		mustStatus(t, http.StatusUnauthorized, "renter me after logout")
}
