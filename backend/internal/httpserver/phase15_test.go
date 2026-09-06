package httpserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Phase 15 — the hardening pass (PLAN2 Phase 15).
//
// The route census in isolation_suite_test.go proves that *every* registered
// route has been thought about. The probes here are the ones the census cannot
// express: routes that answer 200 to both tenants and whose isolation is
// therefore a question about the *content* of the answer rather than its
// status code, plus the CSV export, whose failure mode is a spreadsheet
// executing a cell rather than a leak.

// ------------------------------------------------------- CSV neutralisation --

// TestExpenseCSVNeutralisesEveryFormulaPrefix drives all four characters a
// spreadsheet reads as the start of a formula through the vendor column.
//
// The existing Phase 10 test covers `=` and `+` on one row; this one is the
// table, because the cheap mistake is to defuse the character someone
// remembered and leave the other three live. `-` matters most: a vendor
// recorded as a negative credit note is ordinary data, and `-2+3+cmd|…` is the
// same cell with a payload on the end.
func TestExpenseCSVNeutralisesEveryFormulaPrefix(t *testing.T) {
	h := newHarness(t)
	fix := h.newExpenseFixture(t, "CSVPrefix", "0716004000")

	// One expense per dangerous prefix, plus a benign vendor that must come
	// back exactly as it was typed.
	vendors := []struct {
		vendor string
		// want is the cell as it must appear in the row, before `encoding/csv`
		// adds its own quoting.
		want string
	}{
		{"=1+1", "'=1+1"},
		{"+255744000000", "'+255744000000"},
		{"-Umeme credit note", "'-Umeme credit note"},
		{"@SUM(A1:A9)", "'@SUM(A1:A9)"},
		{"Umeme Tanzania", "Umeme Tanzania"},
	}
	for i, v := range vendors {
		fix.record(t, map[string]any{
			"property_id": fix.propertyID, "amount": int64(10_000 + i*1_000),
			"incurred_on": today(), "vendor": v.vendor,
		})
	}

	resp := fix.client.do(http.MethodGet,
		"/expenses?format=csv&from="+today()+"&to="+today(), nil).
		mustStatus(t, http.StatusOK, "csv export")

	for _, v := range vendors {
		if !strings.Contains(resp.Raw, v.want) {
			t.Errorf("vendor %q does not appear as %q in the export — a spreadsheet "+
				"would evaluate it on open. Body:\n%s", v.vendor, v.want, resp.Raw)
		}
	}
	// The neutralisation is a prefix, not a rewrite: the original text is still
	// readable, which is the point of an apostrophe rather than a strip.
	for _, v := range vendors {
		if !strings.Contains(resp.Raw, v.vendor) {
			t.Errorf("vendor %q was altered rather than prefixed: %s", v.vendor, resp.Raw)
		}
	}
}

// ------------------------------------------------- cross-org content probes --

// TestPart2CrossOrgProbes covers the Part 2 routes that answer 200 to every
// org. A 404 is not available as evidence here — org B is *supposed* to get an
// answer — so what is asserted is that the answer is org B's own.
func TestPart2CrossOrgProbes(t *testing.T) {
	h := newHarness(t)

	a := h.newExpenseFixture(t, "ProbeAlpha", "0716004100")
	b := h.newExpenseFixture(t, "ProbeBeta", "0716004200")

	// -------------------------------------------- expenses/{id}/receipt --
	//
	// The receipt routes name an expense id, so a foreign one is a 404 —
	// including the `complete` call, which is refused on the shape of the key
	// alone, before object storage is ever asked whether the object is there.
	// That ordering is the load-bearing part: a 404 that depends on MinIO is a
	// 404 that becomes a 200 the day MinIO is slow.
	alphaExpense := a.record(t, map[string]any{
		"property_id": a.propertyID, "amount": 55_000, "incurred_on": today(),
		"vendor": "Alpha Hardware",
	})
	receipt := "/expenses/" + alphaExpense + "/receipt"
	for _, probe := range []struct {
		method string
		path   string
		body   map[string]any
	}{
		{http.MethodGet, receipt, nil},
		{http.MethodDelete, receipt, nil},
		{http.MethodPost, receipt, map[string]any{"content_type": "image/png", "size": 1024}},
		{http.MethodPost, receipt + "/complete",
			map[string]any{"object_key": a.orgID + "/" + alphaExpense + ".png"}},
	} {
		got := b.client.do(probe.method, probe.path, probe.body)
		if got.Code != http.StatusNotFound {
			t.Errorf("%s %s as org B: status = %d, want 404 — body: %s",
				probe.method, probe.path, got.Code, got.Raw)
		}
	}

	// -------------------------------------------------- org/sms-credits --
	//
	// The route names no id: what it answers with is decided by the session,
	// so the probe is that two orgs with deliberately different balances each
	// read their own.
	h.grantCredits(a.orgID, 500)
	h.grantCredits(b.orgID, 25)

	alphaCredits := a.client.do(http.MethodGet, "/org/sms-credits", nil).
		mustStatus(t, http.StatusOK, "org A credits")
	betaCredits := b.client.do(http.MethodGet, "/org/sms-credits", nil).
		mustStatus(t, http.StatusOK, "org B credits")

	if got := balanceOf(t, alphaCredits); got != 500 {
		t.Errorf("org A balance = %d, want 500", got)
	}
	if got := balanceOf(t, betaCredits); got != 25 {
		t.Errorf("org B balance = %d, want 25 — org B is reading someone else's meter", got)
	}
	if strings.Contains(betaCredits.Raw, a.orgID) {
		t.Errorf("org B's credit view names org A: %s", betaCredits.Raw)
	}

	// --------------------------------------------------------- themes --
	//
	// The theme is public by design — a login page is branded before anyone
	// has signed in — but it is resolved *per org*, and the public route takes
	// the slug from the URL rather than from a session. Two orgs on different
	// presets is the cheapest way to prove the resolution is not shared state.
	a.client.do(http.MethodPut, "/org/branding",
		map[string]any{"theme": map[string]any{"preset_id": "forest"}}).
		mustStatus(t, http.StatusOK, "org A picks forest")
	b.client.do(http.MethodPut, "/org/branding",
		map[string]any{"theme": map[string]any{"preset_id": "night_ledger"}}).
		mustStatus(t, http.StatusOK, "org B picks night ledger")

	alphaSlug := a.client.do(http.MethodGet, "/org", nil).
		mustStatus(t, http.StatusOK, "org A").str(t, "slug")
	betaSlug := b.client.do(http.MethodGet, "/org", nil).
		mustStatus(t, http.StatusOK, "org B").str(t, "slug")

	// An anonymous caller — the state a QR scanner is in — reads each slug.
	anon := h.client()
	alphaPublic := anon.do(http.MethodGet, "/public/orgs/"+alphaSlug+"/branding", nil).
		mustStatus(t, http.StatusOK, "public branding A")
	betaPublic := anon.do(http.MethodGet, "/public/orgs/"+betaSlug+"/branding", nil).
		mustStatus(t, http.StatusOK, "public branding B")

	if got := presetOf(t, alphaPublic); got != "forest" {
		t.Errorf("public branding for %s resolved preset %q, want forest", alphaSlug, got)
	}
	if got := presetOf(t, betaPublic); got != "night_ledger" {
		t.Errorf("public branding for %s resolved preset %q, want night_ledger — "+
			"the public theme is leaking across orgs", betaSlug, got)
	}
	// Nothing org-scoped rides along with a theme.
	for _, id := range []string{a.orgID, alphaExpense} {
		if strings.Contains(betaPublic.Raw, id) {
			t.Errorf("public branding for org B carries org A's %s: %s", id, betaPublic.Raw)
		}
	}

	// The preset catalogue is platform data: identical for both, and for
	// nobody in particular.
	presetsAnon := anon.do(http.MethodGet, "/themes/presets", nil).
		mustStatus(t, http.StatusOK, "presets anonymous")
	presetsOrg := b.client.do(http.MethodGet, "/themes/presets", nil).
		mustStatus(t, http.StatusOK, "presets as org B")
	if presetsAnon.Raw != presetsOrg.Raw {
		t.Error("GET /themes/presets answers differently to a session than to an " +
			"anonymous caller; the catalogue is meant to be platform data")
	}
}

// balanceOf reads the credit balance out of a GET /org/sms-credits response.
func balanceOf(t *testing.T, r response) int {
	t.Helper()
	v, ok := r.Body["balance"].(float64)
	if !ok {
		t.Fatalf("no `balance` number in %s", r.Raw)
	}
	return int(v)
}

// presetOf reads the resolved theme preset out of a branding response.
func presetOf(t *testing.T, r response) string {
	t.Helper()
	theme, ok := r.Body["theme"].(map[string]any)
	if !ok {
		t.Fatalf("no `theme` object in %s", r.Raw)
	}
	id, _ := theme["preset_id"].(string)
	return id
}

// ------------------------------------------------------- cross-renter probe --

// TestRenterCannotReadAnotherRentersLanguage is the Phase 13 half of the
// isolation story. A renter's locale is a fact about the person, and the
// contract it drives — the language the terms were rendered in — is one of the
// few places it surfaces in an API response. Renter 2 must reach neither.
func TestRenterCannotReadAnotherRentersLanguage(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "LangIso", "0716004300", "+255716004301")

	// Renter 1 reads Swahili; renter 2 reads English. Both switches are the
	// caller's own, which is the only switch the API offers.
	fix.renter.do(http.MethodPatch, "/me", map[string]any{"locale": "sw"}).
		mustStatus(t, http.StatusOK, "renter 1 picks Swahili")

	renter2 := h.registerRenter("+255716004302", "Lang Iso Two", defaultPIN)
	renter2.completeProfile(t, "Lang Iso Two", "19900101123456789014")
	renter2.do(http.MethodPatch, "/me", map[string]any{"locale": "en"}).
		mustStatus(t, http.StatusOK, "renter 2 picks English")

	// Renter 1's own contract answers, and says which language it is in.
	own := fix.renter.do(http.MethodGet, "/contracts/"+fix.contractID, nil).
		mustStatus(t, http.StatusOK, "renter 1 reads their own contract")
	if lang := own.str(t, "contract", "language"); lang == "" {
		t.Error("the contract carries no `language`; the probe below would prove nothing")
	}

	// Renter 2 reaches none of it — not the contract, not the document it was
	// rendered into, not the schedules hanging off it.
	for _, path := range []string{
		"/contracts/" + fix.contractID,
		"/contracts/" + fix.contractID + "/document",
		"/contracts/" + fix.contractID + "/verify",
		"/contracts/" + fix.contractID + "/schedules",
	} {
		got := renter2.do(http.MethodGet, path, nil)
		if got.Code != http.StatusNotFound {
			t.Errorf("GET %s as renter 2: status = %d, want 404 — body: %s",
				path, got.Code, got.Raw)
		}
	}

	// And their own lists stay their own: renter 2 has no tenancy, so the
	// collection routes are empty rather than helpful.
	for _, path := range []string{"/me/contracts", "/me/schedules", "/me/payments"} {
		got := renter2.do(http.MethodGet, path, nil).
			mustStatus(t, http.StatusOK, "renter 2 "+path)
		if strings.Contains(got.Raw, fix.contractID) {
			t.Errorf("GET %s as renter 2 names renter 1's contract: %s", path, got.Raw)
		}
	}

	// The locales themselves did not cross: renter 1 is still `sw` after
	// renter 2 chose `en`, which is what the per-user column is for.
	if locale := renterLocale(t, h, fix.renterID); locale != "sw" {
		t.Errorf("renter 1's locale = %q after renter 2 switched to en, want sw", locale)
	}
}

// renterLocale reads one user's stored locale straight from the column, so the
// assertion does not depend on an endpoint that might itself be the bug.
func renterLocale(t *testing.T, h *harness, userID string) string {
	t.Helper()
	var locale string
	if err := h.pool.QueryRow(context.Background(),
		"SELECT locale FROM users WHERE id = $1", userID).Scan(&locale); err != nil {
		t.Fatalf("read locale for %s: %v", userID, err)
	}
	return locale
}
