package httpserver_test

import (
	"context"
	"encoding/csv"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/httpserver"
	"tms/backend/internal/notify"
)

// ------------------------------------------------------------- fixtures --

// reportFixture is one org with two properties, four units and two tenancies:
// one settled, one in arrears. Every schedule is parked far in the future
// except the two placed in `period`, so the numbers a report quotes are exact
// rather than "whatever the generator happened to write".
type reportFixture struct {
	owner       *client
	orgID       string
	propertyID  string // property A, for the cross-org `property_id` filter
	unitIDs     []string
	unitCodes   []string
	paidRenter  *client
	lateRenter  *client
	paidLink    string // contract id of the settled tenancy
	lateLink    string // contract id of the tenancy in arrears
	period      string // YYYY-MM, the month the money sits in
	anchor      time.Time
	rent        int64
	paidRenterN string
	lateRenterN string
}

func (h *harness) newReportFixture(t *testing.T, tag, ownerPhone, phoneA, phoneB string) reportFixture {
	t.Helper()

	base := h.newOrgWithUnits(tag, strings.ToLower(tag)+"@jjne.test", ownerPhone,
		[]string{"A1", "A2"}, testUnitAmount)
	out := reportFixture{
		owner: base.client, orgID: base.orgID, propertyID: base.propertyID,
		unitIDs: base.unitIDs, unitCodes: base.unitCodes,
		paidRenterN: tag + " Settled", lateRenterN: tag + " Late",
	}
	// A second property, so the summary counts two of them and occupancy is
	// measured across the whole portfolio.
	propB := base.client.do(http.MethodPost, "/properties", map[string]any{
		"name": tag + " Block B", "location_text": "Dar es Salaam",
	}).mustStatus(t, http.StatusCreated, "create property B").str(t, "property", "id")
	unitsB := base.client.do(http.MethodPost, "/properties/"+propB+"/units/bulk", map[string]any{
		"names": []string{"B1", "B2"},
		"price": map[string]any{"amount": testUnitAmount, "period_days": 30},
	}).mustStatus(t, http.StatusCreated, "bulk create B units")
	for _, u := range listOf(t, unitsB) {
		id, _ := u["id"].(string)
		code, _ := u["unit_code"].(string)
		out.unitIDs = append(out.unitIDs, id)
		out.unitCodes = append(out.unitCodes, code)
	}

	periodID := base.client.periodIDByDays(t, 30)
	out.paidRenter, out.paidLink = h.tenancyOn(t, base.client, out.unitCodes[0], periodID, phoneA, out.paidRenterN)
	out.lateRenter, out.lateLink = h.tenancyOn(t, base.client, out.unitCodes[2], periodID, phoneB, out.lateRenterN)

	// Park every generated row, then place exactly one due row per contract in
	// a month that is safely in the past — no matter which day the suite runs.
	out.anchor = time.Now().UTC().AddDate(0, 0, -45).Truncate(24 * time.Hour)
	out.period = out.anchor.Format("2006-01")

	paidSchedules := h.scheduleIDs(t, base.client, out.paidLink)
	lateSchedules := h.scheduleIDs(t, base.client, out.lateLink)
	out.rent = h.scheduleAmount(t, base.client, out.paidLink)

	h.parkSchedules(t, out.paidLink)
	h.parkSchedules(t, out.lateLink)
	h.setDueDate(t, paidSchedules[0], out.anchor, "pending")
	h.setDueDate(t, lateSchedules[0], out.anchor, "pending")

	// The settled tenancy's remaining periods are waived, so its headline
	// status is `paid` rather than "paid this month, pending next".
	h.waiveParked(t, out.paidLink)

	base.client.recordPayment(map[string]any{
		"contract_id": out.paidLink, "schedule_id": paidSchedules[0],
		"amount": out.rent, "method": "cash", "reference": "RCT-REP",
	}).mustStatus(t, http.StatusCreated, "record settling payment")
	// Money moves on the day it is received; the recording API stamps `now`, so
	// the test moves it back into the period it is reporting on.
	h.backdatePayment(t, out.paidLink, out.anchor)

	return out
}

// tenancyOn walks one renter from registration to an active contract.
func (h *harness) tenancyOn(t *testing.T, owner *client, unitCode, periodID, phone, name string) (*client, string) {
	t.Helper()
	renter := h.registerRenter(phone, name, defaultPIN)
	renter.completeProfile(t, name, validNIDA)

	applied := renter.do(http.MethodPost, "/units/"+unitCode+"/link", linkBody(periodID, testTermDays)).
		mustStatus(t, http.StatusCreated, "apply for "+unitCode)
	contractID := owner.do(http.MethodPost,
		"/link-requests/"+applied.str(t, "request", "id")+"/approve", nil).
		mustStatus(t, http.StatusOK, "approve "+unitCode).str(t, "contract", "id")

	h.signAsRenter(t, renter, contractID, phone)
	owner.do(http.MethodPost, "/contracts/"+contractID+"/activate", nil).
		mustStatus(t, http.StatusOK, "activate "+unitCode)
	return renter, contractID
}

func (h *harness) scheduleIDs(t *testing.T, c *client, contractID string) []string {
	t.Helper()
	var out []string
	for _, row := range listOf(t, c.do(http.MethodGet, "/contracts/"+contractID+"/schedules", nil).
		mustStatus(t, http.StatusOK, "schedules")) {
		id, _ := row["id"].(string)
		out = append(out, id)
	}
	if len(out) == 0 {
		t.Fatal("contract has no schedules")
	}
	return out
}

func (h *harness) scheduleAmount(t *testing.T, c *client, contractID string) int64 {
	t.Helper()
	rows := listOf(t, c.do(http.MethodGet, "/contracts/"+contractID+"/schedules", nil).
		mustStatus(t, http.StatusOK, "schedules"))
	return int64(mustFloat(t, rows[0], "amount"))
}

// waiveParked marks the far-future rows of a contract as waived — the state a
// terminated or fully-settled tenancy leaves behind.
func (h *harness) waiveParked(t *testing.T, contractID string) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE payment_schedules SET status = 'waived'
		 WHERE contract_id = $1 AND due_date > CURRENT_DATE`, contractID); err != nil {
		t.Fatalf("waive parked schedules: %v", err)
	}
}

func (h *harness) backdatePayment(t *testing.T, contractID string, at time.Time) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE payments SET paid_at = $2 WHERE contract_id = $1`,
		contractID, at.Add(12*time.Hour)); err != nil {
		t.Fatalf("backdate payment: %v", err)
	}
}

// raw issues a request and hands back the whole recorder, for the endpoints
// whose answer is not JSON (the CSV export).
func (c *client) raw(method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, httpserver.APIPrefix+path, strings.NewReader(""))
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	c.h.srv.Handler().ServeHTTP(rec, req)
	return rec
}

// ------------------------------------------------------ GET /reports/summary --

// TestReportSummaryNumbers is Flow 9's headline panel: four units of which two
// are let, two tenancies, one period's rent collected and one still owed.
func TestReportSummaryNumbers(t *testing.T) {
	h := newHarness(t)
	fix := h.newReportFixture(t, "Rep", "0716000100", "+255716000101", "+255716000102")

	got := fix.owner.do(http.MethodGet, "/reports/summary?period="+fix.period, nil).
		mustStatus(t, http.StatusOK, "summary")

	checks := []struct {
		path []string
		want float64
	}{
		{[]string{"assets", "properties"}, 2},
		{[]string{"assets", "units"}, 4},
		{[]string{"assets", "occupied"}, 2},
		{[]string{"assets", "vacant"}, 2},
		{[]string{"assets", "occupancy_rate"}, 0.5},
		{[]string{"renters", "active"}, 2},
		{[]string{"contracts", "active"}, 2},
		{[]string{"period", "expected"}, float64(2 * fix.rent)},
		{[]string{"period", "collected"}, float64(fix.rent)},
		{[]string{"period", "outstanding"}, float64(fix.rent)},
		{[]string{"period", "overdue_count"}, 1},
		{[]string{"period", "overdue_amount"}, float64(fix.rent)},
	}
	for _, c := range checks {
		if v := num(t, got, c.path...); v != c.want {
			t.Errorf("summary %v = %v, want %v — body: %s", c.path, v, c.want, got.Raw)
		}
	}
	if from := got.str(t, "period", "from"); from != fix.period+"-01" {
		t.Errorf("period.from = %s, want %s-01", from, fix.period)
	}
	// The two empty units are the ones the landlord is asked to fill.
	empties := arrayOf(t, got, "vacant_units")
	if len(empties) != 2 {
		t.Fatalf("vacant_units = %d rows, want 2 — body: %s", len(empties), got.Raw)
	}
	if empties[0]["days_vacant"] == nil {
		t.Errorf("vacant unit carries no days_vacant: %v", empties[0])
	}

	// The default period is the current month, which holds none of this money.
	current := fix.owner.do(http.MethodGet, "/reports/summary", nil).
		mustStatus(t, http.StatusOK, "summary, default period")
	if v := num(t, current, "period", "collected"); v != 0 {
		t.Errorf("this month collected %v, want 0 — the money is 45 days old", v)
	}
	if v := num(t, current, "assets", "units"); v != 4 {
		t.Errorf("asset counts must not depend on the period: units = %v", v)
	}
}

func TestReportSummaryRejectsABadPeriod(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Badper", "badper@jjne.test", "0716000110", nil, 0)
	fix.client.do(http.MethodGet, "/reports/summary?period=september", nil).
		mustStatus(t, http.StatusBadRequest, "bad period")
}

// ----------------------------------------------- GET /reports/payment-status --

// TestReportPaymentStatusWorstStatus checks the per-renter roll-up: a renter
// with nothing unsettled reads `paid`, one with a lapsed schedule `overdue`.
func TestReportPaymentStatusWorstStatus(t *testing.T) {
	h := newHarness(t)
	fix := h.newReportFixture(t, "Status", "0716000200", "+255716000201", "+255716000202")

	rows := arrayOf(t, fix.owner.do(http.MethodGet, "/reports/payment-status", nil).
		mustStatus(t, http.StatusOK, "payment status"), "items")
	if len(rows) != 2 {
		t.Fatalf("payment-status returned %d rows, want 2", len(rows))
	}
	byName := map[string]map[string]any{}
	for _, row := range rows {
		name, _ := row["renter_name"].(string)
		byName[name] = row
	}

	settled, ok := byName[fix.paidRenterN]
	if !ok {
		t.Fatalf("the settled renter is missing from %v", byName)
	}
	if settled["status"] != "paid" {
		t.Errorf("settled renter status = %v, want paid", settled["status"])
	}
	if got := int64(mustFloat(t, settled, "outstanding")); got != 0 {
		t.Errorf("settled renter owes %d, want 0", got)
	}
	if settled["last_payment_at"] == nil {
		t.Error("settled renter has no last_payment_at")
	}

	late, ok := byName[fix.lateRenterN]
	if !ok {
		t.Fatalf("the late renter is missing from %v", byName)
	}
	if late["status"] != "overdue" {
		t.Errorf("late renter status = %v, want overdue", late["status"])
	}
	if got := int64(mustFloat(t, late, "overdue_amount")); got != fix.rent {
		t.Errorf("late renter overdue_amount = %d, want %d", got, fix.rent)
	}
	if got, _ := late["next_due_date"].(string); got != fix.anchor.Format("2006-01-02") {
		t.Errorf("late renter next_due_date = %q, want %s", got, fix.anchor.Format("2006-01-02"))
	}

	// The status filter narrows the same roll-up.
	only := arrayOf(t, fix.owner.do(http.MethodGet, "/reports/payment-status?status=overdue", nil).
		mustStatus(t, http.StatusOK, "filtered payment status"), "items")
	if len(only) != 1 || only[0]["renter_name"] != fix.lateRenterN {
		t.Errorf("status=overdue returned %v, want just the late renter", only)
	}
	fix.owner.do(http.MethodGet, "/reports/payment-status?status=sideways", nil).
		mustStatus(t, http.StatusBadRequest, "unknown status filter")
}

// TestReportPaymentStatusCSV pins the export: a real attachment, the fixed
// header, and one line per renter.
func TestReportPaymentStatusCSV(t *testing.T) {
	h := newHarness(t)
	fix := h.newReportFixture(t, "Csv", "0716000300", "+255716000301", "+255716000302")

	rec := fix.owner.raw(http.MethodGet, "/reports/payment-status?format=csv")
	if rec.Code != http.StatusOK {
		t.Fatalf("csv export: status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
	disposition := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disposition, "attachment; filename=\"payment-status-") ||
		!strings.HasSuffix(disposition, ".csv\"") {
		t.Errorf("Content-Disposition = %q, want a payment-status-{date}.csv attachment", disposition)
	}

	records, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v — body: %s", err, rec.Body.String())
	}
	if len(records) != 3 {
		t.Fatalf("csv has %d lines, want a header and two renters: %v", len(records), records)
	}
	wantHeader := []string{
		"renter_name", "phone", "property", "unit", "status",
		"next_due_date", "next_due_amount", "outstanding", "overdue_amount", "last_payment_at",
	}
	for i, col := range wantHeader {
		if records[0][i] != col {
			t.Errorf("csv column %d = %q, want %q", i, records[0][i], col)
		}
	}
	var sawOverdue bool
	for _, row := range records[1:] {
		if len(row) != len(wantHeader) {
			t.Errorf("csv row has %d fields, want %d: %v", len(row), len(wantHeader), row)
		}
		if row[4] == "overdue" {
			sawOverdue = true
			if row[8] == "" || row[8] == "0" {
				t.Errorf("an overdue row exports overdue_amount %q", row[8])
			}
		}
	}
	if !sawOverdue {
		t.Errorf("no overdue line in the export: %v", records)
	}
}

// ------------------------------------------- GET /reports/collections --

// TestReportCollectionsByMonth buckets the same two schedules by calendar
// month: the anchor month carries all of it, the months around it carry zero.
func TestReportCollectionsByMonth(t *testing.T) {
	h := newHarness(t)
	fix := h.newReportFixture(t, "Coll", "0716000400", "+255716000401", "+255716000402")

	from := fix.anchor.AddDate(0, -1, 0).Format("2006-01-02")
	to := time.Now().UTC().Format("2006-01-02")
	got := fix.owner.do(http.MethodGet,
		"/reports/collections?group=month&from="+from+"&to="+to, nil).
		mustStatus(t, http.StatusOK, "collections")

	buckets := arrayOf(t, got, "buckets")
	if len(buckets) < 2 {
		t.Fatalf("collections returned %d buckets, want the whole span — body: %s", len(buckets), got.Raw)
	}
	var found bool
	for _, b := range buckets {
		start, _ := b["start"].(string)
		expected := int64(mustFloat(t, b, "expected"))
		collected := int64(mustFloat(t, b, "collected"))
		if strings.HasPrefix(start, fix.period) {
			found = true
			if expected != 2*fix.rent {
				t.Errorf("bucket %s expected = %d, want %d", start, expected, 2*fix.rent)
			}
			if collected != fix.rent {
				t.Errorf("bucket %s collected = %d, want %d", start, collected, fix.rent)
			}
			continue
		}
		if expected != 0 || collected != 0 {
			t.Errorf("bucket %s = (%d, %d), want an empty month", start, expected, collected)
		}
	}
	if !found {
		t.Fatalf("no bucket for %s in %v", fix.period, buckets)
	}
	if got := int64(num(t, got, "totals", "expected")); got != 2*fix.rent {
		t.Errorf("totals.expected = %d, want %d", got, 2*fix.rent)
	}
	if got := int64(num(t, got, "totals", "collected")); got != fix.rent {
		t.Errorf("totals.collected = %d, want %d", got, fix.rent)
	}
	// Every bucket start is the first of a month.
	for _, b := range buckets {
		if start, _ := b["start"].(string); !strings.HasSuffix(start, "-01") {
			t.Errorf("month bucket starts at %q, want the first of a month", start)
		}
	}

	fix.owner.do(http.MethodGet, "/reports/collections?group=fortnight", nil).
		mustStatus(t, http.StatusBadRequest, "unknown grouping")
	fix.owner.do(http.MethodGet, "/reports/collections?from=2026-09-10&to=2026-09-01", nil).
		mustStatus(t, http.StatusBadRequest, "backwards range")
	fix.owner.do(http.MethodGet, "/reports/collections?group=day&from=2000-01-01&to=2030-01-01", nil).
		mustStatus(t, http.StatusBadRequest, "too many buckets")
}

// ------------------------------------------------------------ isolation --

// TestReportsAreOwnOrgOnly: reports are org-scoped by construction, so the
// proof is that two orgs holding different money report different numbers and
// neither can name the other's renters.
func TestReportsAreOwnOrgOnly(t *testing.T) {
	h := newHarness(t)
	a := h.newReportFixture(t, "IsoA", "0716000500", "+255716000501", "+255716000502")
	b := h.newOrgWithUnits("IsoB", "isob@jjne.test", "0716000510", []string{"Z1"}, testUnitAmount)

	mine := a.owner.do(http.MethodGet, "/reports/summary?period="+a.period, nil).
		mustStatus(t, http.StatusOK, "org A summary")
	theirs := b.client.do(http.MethodGet, "/reports/summary?period="+a.period, nil).
		mustStatus(t, http.StatusOK, "org B summary")

	if num(t, mine, "period", "collected") == num(t, theirs, "period", "collected") {
		t.Errorf("both orgs report the same collections — the report is not org-scoped")
	}
	if got := num(t, theirs, "period", "collected"); got != 0 {
		t.Errorf("org B collected %v of org A's money", got)
	}
	if got := num(t, theirs, "assets", "units"); got != 1 {
		t.Errorf("org B counts %v units, want its own 1", got)
	}
	if got := num(t, theirs, "renters", "active"); got != 0 {
		t.Errorf("org B counts %v active renters, want 0", got)
	}

	rows := arrayOf(t, b.client.do(http.MethodGet, "/reports/payment-status", nil).
		mustStatus(t, http.StatusOK, "org B payment status"), "items")
	if len(rows) != 0 {
		t.Errorf("org B sees %d of org A's renters", len(rows))
	}
	// A property id belonging to another org filters to nothing, never to a
	// leak or a 500.
	filtered := arrayOf(t, b.client.do(http.MethodGet,
		"/reports/payment-status?property_id="+a.propertyID, nil).
		mustStatus(t, http.StatusOK, "cross-org property filter"), "items")
	if len(filtered) != 0 {
		t.Errorf("a cross-org property_id returned %d rows", len(filtered))
	}
}

// ------------------------------------------------------ dashboard prefs --

func TestDashboardPrefsValidation(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Prefs", "prefs@jjne.test", "0716000600", nil, 0)

	ok := fix.client.do(http.MethodPut, "/org/branding", map[string]any{
		"dashboard_prefs": map[string]any{
			"cards":  []string{"overdue", "collections", "assets"},
			"layout": "list",
		},
	}).mustStatus(t, http.StatusOK, "put valid prefs")
	prefs, isMap := ok.Body["branding"].(map[string]any)["dashboard_prefs"].(map[string]any)
	if !isMap {
		t.Fatalf("dashboard_prefs did not round-trip: %s", ok.Raw)
	}
	if prefs["layout"] != "list" {
		t.Errorf("layout = %v, want list", prefs["layout"])
	}
	cards, _ := prefs["cards"].([]any)
	if len(cards) != 3 || cards[0] != "overdue" {
		t.Errorf("cards = %v, want the order it was sent in", cards)
	}

	bad := []struct {
		what  string
		prefs map[string]any
		field string
	}{
		{"unknown card", map[string]any{"cards": []string{"assets", "profits"}}, "dashboard_prefs.cards"},
		{"duplicate card", map[string]any{"cards": []string{"assets", "assets"}}, "dashboard_prefs.cards"},
		{"cards not an array", map[string]any{"cards": "assets"}, "dashboard_prefs.cards"},
		{"unknown layout", map[string]any{"layout": "carousel"}, "dashboard_prefs.layout"},
		{"unknown setting", map[string]any{"refresh_seconds": 5}, "dashboard_prefs.refresh_seconds"},
	}
	for _, c := range bad {
		resp := fix.client.do(http.MethodPut, "/org/branding",
			map[string]any{"dashboard_prefs": c.prefs}).
			mustStatus(t, http.StatusBadRequest, c.what)
		errs, _ := resp.Body["errors"].(map[string]any)
		if _, named := errs[c.field]; !named {
			t.Errorf("%s: errors = %v, want a message on %s", c.what, errs, c.field)
		}
	}

	// The rejected writes left the good value in place.
	after := fix.client.do(http.MethodGet, "/org/branding", nil).
		mustStatus(t, http.StatusOK, "read branding back")
	if got := after.Body["dashboard_prefs"].(map[string]any)["layout"]; got != "list" {
		t.Errorf("layout after rejected writes = %v, want list", got)
	}
}

// -------------------------------------------------------- platform admin --

// TestAdminOrgListAndSuspension is Flow 11 end to end: the admin sees every
// tenant with its counts, suspends one, that org's staff are locked out and its
// stickers stop resolving, and activation puts everything back.
func TestAdminOrgListAndSuspension(t *testing.T) {
	h := newHarness(t)
	fix := h.newReportFixture(t, "Adm", "0716000700", "+255716000701", "+255716000702")
	other := h.newOrgWithUnits("AdmOther", "admother@jjne.test", "0716000710", []string{"Q1"}, testUnitAmount)
	admin := h.adminClient(t)

	list := arrayOf(t, admin.do(http.MethodGet, "/admin/orgs", nil).
		mustStatus(t, http.StatusOK, "admin org list"), "items")
	if len(list) < 2 {
		t.Fatalf("admin sees %d orgs, want at least 2", len(list))
	}
	var target map[string]any
	for _, row := range list {
		if row["id"] == fix.orgID {
			target = row
		}
	}
	if target == nil {
		t.Fatalf("the seeded org is missing from the admin list: %v", list)
	}
	counts, _ := target["counts"].(map[string]any)
	if int(mustFloat(t, counts, "units")) != 4 || int(mustFloat(t, counts, "properties")) != 2 {
		t.Errorf("admin counts = %v, want 4 units across 2 properties", counts)
	}
	if int(mustFloat(t, counts, "active_contracts")) != 2 {
		t.Errorf("admin active_contracts = %v, want 2", counts["active_contracts"])
	}
	owner, _ := target["owner"].(map[string]any)
	if owner["email"] != "adm@jjne.test" {
		t.Errorf("admin list owner = %v, want the org's owner", owner)
	}
	if _, ok := target["sms"].(map[string]any); !ok {
		t.Errorf("admin list row carries no sms block: %v", target)
	}

	// The search narrows by name, and the status filter by state.
	found := arrayOf(t, admin.do(http.MethodGet, "/admin/orgs?q=AdmOther", nil).
		mustStatus(t, http.StatusOK, "admin org search"), "items")
	if len(found) != 1 || found[0]["id"] != other.orgID {
		t.Errorf("q=AdmOther returned %v, want just that org", found)
	}

	detail := admin.do(http.MethodGet, "/admin/orgs/"+fix.orgID, nil).
		mustStatus(t, http.StatusOK, "admin org detail")
	if members := arrayOf(t, response{Body: detail.Body["org"].(map[string]any), Raw: detail.Raw},
		"members"); len(members) != 1 {
		t.Errorf("org detail lists %d members, want the owner", len(members))
	}
	admin.do(http.MethodGet, "/admin/orgs/"+strings.Repeat("0", 8)+"-0000-0000-0000-000000000000", nil).
		mustStatus(t, http.StatusNotFound, "unknown org detail")

	// --- suspend ---
	suspended := admin.do(http.MethodPost, "/admin/orgs/"+fix.orgID+"/suspend",
		map[string]any{"reason": "unpaid platform invoice"}).
		mustStatus(t, http.StatusOK, "suspend")
	if got := suspended.str(t, "org", "status"); got != "suspended" {
		t.Errorf("suspended org status = %q", got)
	}
	admin.do(http.MethodPost, "/admin/orgs/"+fix.orgID+"/suspend", map[string]any{"reason": ""}).
		mustStatus(t, http.StatusBadRequest, "suspend without a reason")

	locked := fix.owner.do(http.MethodGet, "/org", nil).
		mustStatus(t, http.StatusForbidden, "suspended org user")
	if got := locked.Body["type"]; got != "org_suspended" {
		t.Errorf("lockout problem type = %v, want org_suspended", got)
	}
	fix.owner.do(http.MethodGet, "/reports/summary", nil).
		mustStatus(t, http.StatusForbidden, "suspended org reports")
	fix.owner.do(http.MethodGet, "/contracts/"+fix.paidLink, nil).
		mustStatus(t, http.StatusForbidden, "suspended org contract read")

	// The public sticker goes quiet, and other orgs are untouched.
	h.client().do(http.MethodGet, "/public/units/"+fix.unitCodes[1], nil).
		mustStatus(t, http.StatusNotFound, "suspended org's public unit")
	other.client.do(http.MethodGet, "/org", nil).
		mustStatus(t, http.StatusOK, "an unrelated org keeps working")
	// A renter of the suspended org is not themselves suspended.
	fix.paidRenter.do(http.MethodGet, "/me/profile", nil).
		mustStatus(t, http.StatusOK, "renter of a suspended org")

	if got := int(num(t, admin.do(http.MethodGet, "/admin/metrics", nil).
		mustStatus(t, http.StatusOK, "metrics"), "orgs", "suspended")); got != 1 {
		t.Errorf("metrics report %d suspended orgs, want 1", got)
	}

	// --- activate ---
	restored := admin.do(http.MethodPost, "/admin/orgs/"+fix.orgID+"/activate", nil).
		mustStatus(t, http.StatusOK, "activate")
	if got := restored.str(t, "org", "status"); got != "active" {
		t.Errorf("activated org status = %q", got)
	}
	fix.owner.do(http.MethodGet, "/org", nil).
		mustStatus(t, http.StatusOK, "org user after activation")
	h.client().do(http.MethodGet, "/public/units/"+fix.unitCodes[1], nil).
		mustStatus(t, http.StatusOK, "public unit after activation")

	// Both movements are on the trail, against the target org.
	trail := arrayOf(t, admin.do(http.MethodGet, "/admin/audit-log?q=org.", nil).
		mustStatus(t, http.StatusOK, "admin audit search"), "items")
	seen := map[string]bool{}
	for _, row := range trail {
		action, _ := row["action"].(string)
		if action == "org.suspend" || action == "org.activate" {
			seen[action] = true
			if row["org_id"] != fix.orgID {
				t.Errorf("%s was audited against org %v, want the target %s", action, row["org_id"], fix.orgID)
			}
			if row["actor_user_id"] == "" {
				t.Errorf("%s was audited with no actor", action)
			}
		}
	}
	if !seen["org.suspend"] || !seen["org.activate"] {
		t.Errorf("the audit trail is missing suspend/activate: %v", seen)
	}
}

func TestAdminMetricsShape(t *testing.T) {
	h := newHarness(t)
	h.newReportFixture(t, "Met", "0716000800", "+255716000801", "+255716000802")
	admin := h.adminClient(t)

	got := admin.do(http.MethodGet, "/admin/metrics", nil).
		mustStatus(t, http.StatusOK, "metrics")

	for _, path := range [][]string{
		{"orgs", "total"}, {"orgs", "active"}, {"orgs", "suspended"},
		{"renters", "total"}, {"units", "total"}, {"units", "occupied"},
		{"contracts", "active"},
		{"sms", "sent_24h"}, {"sms", "failed_24h"}, {"sms", "queued"},
		{"payments", "recorded_30d"}, {"payments", "amount_30d"},
	} {
		num(t, got, path...)
	}
	for _, dep := range []string{"db", "redis", "minio"} {
		block, ok := got.Body[dep].(map[string]any)
		if !ok {
			t.Fatalf("metrics has no %s block: %s", dep, got.Raw)
		}
		if _, isBool := block["ok"].(bool); !isBool {
			t.Errorf("metrics.%s.ok = %v, want a boolean", dep, block["ok"])
		}
	}
	if got.Body["db"].(map[string]any)["ok"] != true {
		t.Error("metrics report the database as down while serving the request")
	}
	if v := num(t, got, "units", "total"); v != 4 {
		t.Errorf("units.total = %v, want the fixture's 4", v)
	}
	if v := num(t, got, "contracts", "active"); v != 2 {
		t.Errorf("contracts.active = %v, want 2", v)
	}
}

// TestAdminAuditSearchCrossesOrgs: the platform trail spans tenants, and
// filtering by org narrows it back down to one.
func TestAdminAuditSearchCrossesOrgs(t *testing.T) {
	h := newHarness(t)
	a := h.newOrgWithUnits("AudA", "auda@jjne.test", "0716000900", []string{"R1"}, testUnitAmount)
	b := h.newOrgWithUnits("AudB", "audb@jjne.test", "0716000910", []string{"R2"}, testUnitAmount)
	admin := h.adminClient(t)

	all := arrayOf(t, admin.do(http.MethodGet, "/admin/audit-log?q=unit.create&limit=200", nil).
		mustStatus(t, http.StatusOK, "cross-org audit search"), "items")
	orgs := map[string]bool{}
	for _, row := range all {
		if action, _ := row["action"].(string); action != "unit.create" {
			t.Errorf("q=unit.create matched %q", action)
		}
		id, _ := row["org_id"].(string)
		orgs[id] = true
	}
	if !orgs[a.orgID] || !orgs[b.orgID] {
		t.Errorf("the cross-org search saw orgs %v, want both %s and %s", orgs, a.orgID, b.orgID)
	}

	one := arrayOf(t, admin.do(http.MethodGet, "/admin/audit-log?org_id="+a.orgID+"&limit=200", nil).
		mustStatus(t, http.StatusOK, "org-filtered audit search"), "items")
	if len(one) == 0 {
		t.Fatal("filtering by org returned nothing")
	}
	for _, row := range one {
		if row["org_id"] != a.orgID {
			t.Errorf("org_id filter leaked a row from %v", row["org_id"])
		}
		if row["org_name"] == "" {
			t.Errorf("audit row carries no org_name: %v", row)
		}
	}

	// entity_type is the other axis operators reach for.
	byEntity := arrayOf(t, admin.do(http.MethodGet, "/admin/audit-log?entity_type=property&limit=200", nil).
		mustStatus(t, http.StatusOK, "entity_type filter"), "items")
	if len(byEntity) == 0 {
		t.Fatal("entity_type=property returned nothing")
	}
	for _, row := range byEntity {
		if row["entity_type"] != "property" {
			t.Errorf("entity_type filter leaked %v", row["entity_type"])
		}
	}
	admin.do(http.MethodGet, "/admin/audit-log?org_id=not-a-uuid", nil).
		mustStatus(t, http.StatusBadRequest, "malformed org_id")
}

func TestAdminJobsReportsLastRun(t *testing.T) {
	h := newHarness(t)
	admin := h.adminClient(t)

	before := arrayOf(t, admin.do(http.MethodGet, "/admin/jobs", nil).
		mustStatus(t, http.StatusOK, "jobs before"), "items")
	if len(before) != 3 {
		t.Fatalf("admin jobs = %d, want the three schedulers", len(before))
	}
	for _, job := range before {
		if job["last_run_at"] != nil {
			t.Errorf("job %v claims a run before anything ran", job["name"])
		}
	}

	admin.do(http.MethodPost, "/admin/jobs/overdue", nil).
		mustStatus(t, http.StatusOK, "run overdue job")

	after := arrayOf(t, admin.do(http.MethodGet, "/admin/jobs", nil).
		mustStatus(t, http.StatusOK, "jobs after"), "items")
	var ran map[string]any
	for _, job := range after {
		if job["name"] == "overdue" {
			ran = job
		}
	}
	if ran == nil {
		t.Fatalf("the overdue job is missing from %v", after)
	}
	if ran["last_run_at"] == nil {
		t.Errorf("the overdue job reports no last run after being run: %v", ran)
	}
	if ran["last_result"] == nil {
		t.Errorf("the overdue job reports no result: %v", ran)
	}
}

// TestAdminRoutesRefuseEveryoneElse: /admin/* is the platform audience only.
func TestAdminRoutesRefuseEveryoneElse(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("NoAdm", "noadm@jjne.test", "0716001000", []string{"S1"}, testUnitAmount)
	renter := h.registerRenter("+255716001001", "Curious Renter", defaultPIN)
	anonymous := h.client()

	routes := []struct{ method, path string }{
		{http.MethodGet, "/admin/orgs"},
		{http.MethodGet, "/admin/orgs/" + fix.orgID},
		{http.MethodGet, "/admin/metrics"},
		{http.MethodGet, "/admin/audit-log"},
		{http.MethodGet, "/admin/jobs"},
		{http.MethodPost, "/admin/orgs/" + fix.orgID + "/suspend"},
		{http.MethodPost, "/admin/orgs/" + fix.orgID + "/activate"},
	}
	for _, who := range []struct {
		name string
		c    *client
	}{
		{"an org owner", fix.client},
		{"a renter", renter},
		{"an anonymous caller", anonymous},
	} {
		for _, route := range routes {
			var body any
			if route.method == http.MethodPost {
				body = map[string]any{"reason": "because"}
			}
			resp := who.c.do(route.method, route.path, body)
			if resp.Code != http.StatusUnauthorized {
				t.Errorf("%s on %s %s: status = %d, want 401",
					who.name, route.method, route.path, resp.Code)
			}
		}
	}
	// And the org stayed active throughout.
	fix.client.do(http.MethodGet, "/org", nil).
		mustStatus(t, http.StatusOK, "org after the failed suspend attempts")
}

// TestSuspendedOrgMessagesAreNotClaimed: a suspended tenant's queued SMS stays
// queued. The claim finds nothing, the worker returns as it does for any
// already-taken row, and nothing is burned as `failed` — so reactivating the
// org lets the backlog go out rather than leaving a trail of errors.
func TestSuspendedOrgMessagesAreNotClaimed(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Quiet", "0716001100", "+255716001101")
	h.parkSchedules(t, fix.contractID)
	h.setDueDate(t, fix.scheduleIDs[0], time.Now().UTC(), "pending")
	h.runScheduler(t, notify.Options{ForceHour: true})

	var id, status string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT id::text, status FROM notification_log WHERE kind = $1 LIMIT 1`,
		notify.KindReminderDue).Scan(&id, &status); err != nil {
		t.Fatalf("read the queued reminder: %v", err)
	}
	if status != "queued" {
		t.Fatalf("the reminder is %q, want queued", status)
	}
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE orgs SET status = 'suspended' WHERE id = $1`, fix.orgID); err != nil {
		t.Fatalf("suspend org: %v", err)
	}

	if _, err := h.queries().ClaimNotification(context.Background(), db.MustUUID(id)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("claim on a suspended org returned %v, want no rows", err)
	}
	if err := h.pool.QueryRow(context.Background(),
		`SELECT status FROM notification_log WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("re-read the reminder: %v", err)
	}
	if status != "queued" {
		t.Errorf("the unclaimed reminder is now %q, want it left queued", status)
	}

	// Reactivating lets the same row be claimed.
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE orgs SET status = 'active' WHERE id = $1`, fix.orgID); err != nil {
		t.Fatalf("reactivate org: %v", err)
	}
	if _, err := h.queries().ClaimNotification(context.Background(), db.MustUUID(id)); err != nil {
		t.Errorf("claim after reactivation: %v", err)
	}
}

// ------------------------------------------------- Phase 7 review additions --

// TestReportCSVNeutralisesFormulaInjection: a renter or a property can be named
// anything, and a CSV cell opening with `=`, `+`, `-` or `@` is a live formula
// the moment the landlord double-clicks the download. Every free-text cell is
// prefixed with an apostrophe; commas, quotes and newlines stay `encoding/csv`'s
// problem and must survive a round trip through a CSV reader.
func TestReportCSVNeutralisesFormulaInjection(t *testing.T) {
	h := newHarness(t)
	fix := h.newReportFixture(t, "Inj", "0716000900", "+255716000901", "+255716000902")

	const evilName = `=cmd|' /c calc'!A1`
	const evilProperty = "Blo,ck \"B\"\nrow two"
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE users SET full_name = $2 WHERE id =
		   (SELECT renter_user_id FROM contracts WHERE id = $1)`,
		fix.lateLink, evilName); err != nil {
		t.Fatalf("rename renter: %v", err)
	}
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE properties SET name = $2 WHERE org_id = $1 AND name LIKE '%Block B'`,
		fix.orgID, evilProperty); err != nil {
		t.Fatalf("rename property: %v", err)
	}

	rec := fix.owner.raw(http.MethodGet, "/reports/payment-status?format=csv")
	if rec.Code != http.StatusOK {
		t.Fatalf("csv export: status = %d, body: %s", rec.Code, rec.Body.String())
	}
	records, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v — body: %s", err, rec.Body.String())
	}

	var sawName, sawProperty bool
	for _, row := range records[1:] {
		if row[0] == "'"+evilName {
			sawName = true
		}
		if row[0] == evilName {
			t.Errorf("a formula reached the export unescaped: %q", row[0])
		}
		// The comma/quote/newline property name round-trips exactly: the CSV
		// writer quoted it, and it does not open with a formula character.
		if row[2] == evilProperty {
			sawProperty = true
		}
		// A `+`-prefixed phone is a formula to a spreadsheet too.
		if strings.HasPrefix(row[1], "+") {
			t.Errorf("a +phone reached the export unescaped: %q", row[1])
		}
	}
	if !sawName {
		t.Errorf("no apostrophe-prefixed renter name in the export: %v", records)
	}
	if !sawProperty {
		t.Errorf("the comma/quote/newline property name did not round-trip: %v", records)
	}
}

// TestReportPaymentStatusRejectsAnAbsurdRange: `from`/`to` are free-form dates,
// so the bucket cap has to be reached without first enumerating a few million
// days into a slice.
func TestReportCollectionsRejectsAnAbsurdRange(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Span", "span@jjne.test", "0716000910", nil, 0)

	bad := fix.client.do(http.MethodGet,
		"/reports/collections?from=0001-01-01&to=9999-12-31&group=day", nil).
		mustStatus(t, http.StatusBadRequest, "absurd day range")
	if errs, _ := bad.Body["errors"].(map[string]any); errs["group"] == nil {
		t.Errorf("the range was refused without naming `group`: %v", bad.Body)
	}
	fix.client.do(http.MethodGet, "/reports/collections?from=2026-01-01&to=2026-03-31&group=day", nil).
		mustStatus(t, http.StatusOK, "a sane day range")
}

// TestSuspensionSurvivesAColdCache: the Redis flag is a cache, not the truth.
// With the flag gone — an evicted key, a restarted Redis — the org route still
// has to answer 403 from the `orgs.status` fallback.
func TestSuspensionSurvivesAColdCache(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Cold", "cold@jjne.test", "0716000920", nil, 0)
	admin := h.adminClient(t)

	admin.do(http.MethodPost, "/admin/orgs/"+fix.orgID+"/suspend",
		map[string]any{"reason": "cache test"}).mustStatus(t, http.StatusOK, "suspend")

	ctx := context.Background()
	if err := h.redis.Del(ctx, auth.SuspensionKey(fix.orgID)).Err(); err != nil {
		t.Fatalf("drop the suspension flag: %v", err)
	}
	locked := fix.client.do(http.MethodGet, "/org", nil).
		mustStatus(t, http.StatusForbidden, "suspended org with a cold cache")
	if got := locked.Body["type"]; got != "org_suspended" {
		t.Errorf("cold-cache lockout type = %v, want org_suspended", got)
	}

	// Activation clears the flag as well as the column: the very next request
	// is served, without waiting out the cache TTL.
	admin.do(http.MethodPost, "/admin/orgs/"+fix.orgID+"/activate", nil).
		mustStatus(t, http.StatusOK, "activate")
	fix.client.do(http.MethodGet, "/org", nil).
		mustStatus(t, http.StatusOK, "activated org, warm flag")
	if v, err := h.redis.Get(ctx, auth.SuspensionKey(fix.orgID)).Result(); err != nil || v != "0" {
		t.Errorf("activation left the cached flag at %q (err %v), want \"0\"", v, err)
	}
}

// TestAdminSearchTreatsWildcardsAsText: `q` is wrapped as `%q%`, so a `%` or a
// `_` an operator types has to match itself rather than silently widening the
// search to everything.
func TestAdminSearchTreatsWildcardsAsText(t *testing.T) {
	h := newHarness(t)
	h.newOrgWithUnits("WildOne", "wild1@jjne.test", "0716000930", nil, 0)
	h.newOrgWithUnits("WildTwo", "wild2@jjne.test", "0716000931", nil, 0)
	admin := h.adminClient(t)

	for _, q := range []string{"%", "_", `\`, "Wild%One"} {
		got := arrayOf(t, admin.do(http.MethodGet, "/admin/orgs?q="+url.QueryEscape(q), nil).
			mustStatus(t, http.StatusOK, "org search for "+q), "items")
		if len(got) != 0 {
			t.Errorf("q=%q matched %d orgs, want none — the wildcard was not escaped", q, len(got))
		}
	}
	if got := arrayOf(t, admin.do(http.MethodGet, "/admin/audit-log?q="+url.QueryEscape("%"), nil).
		mustStatus(t, http.StatusOK, "audit search for %"), "items"); len(got) != 0 {
		t.Errorf("audit q=%% matched %d rows, want none", len(got))
	}
	if got := arrayOf(t, admin.do(http.MethodGet, "/admin/orgs?q=WildOne", nil).
		mustStatus(t, http.StatusOK, "plain org search"), "items"); len(got) != 1 {
		t.Errorf("a plain search matched %d orgs, want 1", len(got))
	}
}
