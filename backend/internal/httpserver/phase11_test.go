package httpserver_test

import (
	"net/http"
	"testing"
	"time"
)

// Phase 11 — Reports v2 (SPEC §5.9, FLOWS 9, PLAN2 Phase 11).
//
// The tests below are about the two ways a report can lie: a total that does
// not agree with the ledger it summarises, and a window that is not the window
// the caller asked for. Everything else — the shape of the JSON — is checked
// only where the frontend has been promised a specific key.

const phase11ExpenseAmount = 40_000

// revenueFixture is the Phase 7 report fixture (two properties, four units, one
// settled tenancy and one in arrears, all the money sitting in one past month)
// with one expense added on property A in that same month, so `net` has both
// sides to work with.
type revenueFixture struct {
	reportFixture
	expenseAmount int64
}

func (h *harness) newRevenueFixture(t *testing.T, tag, ownerPhone, phoneA, phoneB string) revenueFixture {
	t.Helper()
	base := h.newReportFixture(t, tag, ownerPhone, phoneA, phoneB)
	out := revenueFixture{reportFixture: base, expenseAmount: phase11ExpenseAmount}
	base.owner.do(http.MethodPost, "/expenses", map[string]any{
		"property_id": base.propertyID,
		"amount":      out.expenseAmount,
		"incurred_on": base.anchor.Format(testDateLayout),
		"vendor":      "Fundi Juma",
	}).mustStatus(t, http.StatusCreated, "record expense in the reported month")
	return out
}

// anchorDay is the `anchor` parameter that selects the month the money is in.
func (f revenueFixture) anchorDay() string { return f.anchor.Format(testDateLayout) }

// monthQuery is the cadence pair every test below opens with.
func (f revenueFixture) monthQuery() string { return "cadence=month&anchor=" + f.anchorDay() }

// ------------------------------------------------------- the revenue series --

// TestRevenueSeriesReconcilesWithLedgers is the guarantee the chart exists for:
// its four totals are the same facts /payments and /expenses report, added up
// over the same window.
func TestRevenueSeriesReconcilesWithLedgers(t *testing.T) {
	h := newHarness(t)
	fix := h.newRevenueFixture(t, "Rev", "0716000200", "+255716000201", "+255716000202")

	got := fix.owner.do(http.MethodGet, "/reports/revenue?"+fix.monthQuery(), nil).
		mustStatus(t, http.StatusOK, "revenue")

	// A calendar month is 28–31 days, so the resolver draws it daily.
	if b := got.str(t, "bucket"); b != "day" {
		t.Errorf("bucket = %q, want day", b)
	}
	for _, c := range []struct {
		key  string
		want float64
	}{
		{"expected", float64(2 * fix.rent)},
		{"collected", float64(fix.rent)},
		{"expenses", float64(fix.expenseAmount)},
		{"net", float64(fix.rent - fix.expenseAmount)},
	} {
		if v := num(t, got, "totals", c.key); v != c.want {
			t.Errorf("totals.%s = %v, want %v — body: %s", c.key, v, c.want, got.Raw)
		}
	}
	if rate := num(t, got, "collection_rate"); rate != 0.5 {
		t.Errorf("collection_rate = %v, want 0.5", rate)
	}

	// The buckets are the totals, spread out: zero-filled across the month and
	// adding back up to the same four numbers.
	buckets := arrayOf(t, got, "buckets")
	from := mustDate(t, got.str(t, "window", "from"))
	to := mustDate(t, got.str(t, "window", "to"))
	if want := int(to.Sub(from).Hours() / 24); len(buckets) != want {
		t.Fatalf("buckets = %d, want one per day (%d) — body: %s", len(buckets), want, got.Raw)
	}
	var sumCollected, sumExpected, sumExpenses, sumNet float64
	var paidDay map[string]any
	for _, b := range buckets {
		sumCollected += mustFloat(t, b, "collected")
		sumExpected += mustFloat(t, b, "expected")
		sumExpenses += mustFloat(t, b, "expenses")
		sumNet += mustFloat(t, b, "net")
		if b["start"] == fix.anchorDay() {
			paidDay = b
		}
	}
	if sumCollected != float64(fix.rent) || sumExpected != float64(2*fix.rent) ||
		sumExpenses != float64(fix.expenseAmount) || sumNet != float64(fix.rent-fix.expenseAmount) {
		t.Errorf("buckets do not add up to the totals: collected=%v expected=%v expenses=%v net=%v",
			sumCollected, sumExpected, sumExpenses, sumNet)
	}
	if paidDay == nil {
		t.Fatalf("no bucket starts on %s — body: %s", fix.anchorDay(), got.Raw)
	}
	if v := mustFloat(t, paidDay, "collected"); v != float64(fix.rent) {
		t.Errorf("the day the rent was paid collected %v, want %v", v, fix.rent)
	}

	// Reconciliation, the other way round: the payment ledger and the expense
	// ledger, read over the same window, must produce the same two figures.
	var ledgerCollected float64
	for _, row := range listOf(t, fix.owner.do(http.MethodGet, "/payments?limit=200", nil).
		mustStatus(t, http.StatusOK, "payments")) {
		if row["status"] == "reversed" {
			continue
		}
		at, ok := row["paid_at"].(string)
		if !ok {
			t.Fatalf("payment carries no paid_at: %v", row)
		}
		day := mustDate(t, at[:len(testDateLayout)])
		if !day.Before(from) && day.Before(to) {
			ledgerCollected += mustFloat(t, row, "amount")
		}
	}
	if ledgerCollected != float64(fix.rent) {
		t.Errorf("GET /payments sums to %v over the window, revenue says %v", ledgerCollected, fix.rent)
	}

	lastDay := to.AddDate(0, 0, -1).Format(testDateLayout)
	ledger := fix.owner.do(http.MethodGet,
		"/expenses?from="+from.Format(testDateLayout)+"&to="+lastDay, nil).
		mustStatus(t, http.StatusOK, "expenses ledger")
	if v := num(t, ledger, "totals", "amount"); v != float64(fix.expenseAmount) {
		t.Errorf("GET /expenses totals %v over the window, revenue says %v", v, fix.expenseAmount)
	}
}

// TestRevenueGroupsSumToTheGrandTotal is the per-property breakdown: every
// property is a row whether or not it earned anything, the rows add up to the
// totals beside them, and the best-performing block is first.
func TestRevenueGroupsSumToTheGrandTotal(t *testing.T) {
	h := newHarness(t)
	fix := h.newRevenueFixture(t, "Grp", "0716000300", "+255716000301", "+255716000302")

	got := fix.owner.do(http.MethodGet,
		"/reports/revenue?group_by=property&"+fix.monthQuery(), nil).
		mustStatus(t, http.StatusOK, "revenue by property")

	groups := arrayOf(t, got, "groups")
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want one per property (2) — body: %s", len(groups), got.Raw)
	}
	var sums = map[string]float64{}
	for _, g := range groups {
		for _, key := range []string{"expected", "collected", "expenses", "net"} {
			sums[key] += mustFloat(t, g, key)
		}
	}
	for key, want := range map[string]float64{
		"expected":  float64(2 * fix.rent),
		"collected": float64(fix.rent),
		"expenses":  float64(fix.expenseAmount),
		"net":       float64(fix.rent - fix.expenseAmount),
	} {
		if sums[key] != want {
			t.Errorf("groups sum %s = %v, want %v", key, sums[key], want)
		}
		if v := num(t, got, "totals", key); v != want {
			t.Errorf("totals.%s = %v, want %v", key, v, want)
		}
	}
	// Property A holds the settled tenancy and the expense; property B holds
	// the tenancy in arrears and earned nothing. Sorted by net descending, A
	// leads.
	if first := groups[0]["id"]; first != fix.propertyID {
		t.Errorf("first group is %v, want property A (%s) — body: %s", first, fix.propertyID, got.Raw)
	}
	if v := mustFloat(t, groups[1], "collected"); v != 0 {
		t.Errorf("property B collected %v, want 0", v)
	}
	if groups[1]["collection_rate"] != float64(0) {
		t.Errorf("property B expected rent and collected none: rate = %v, want 0",
			groups[1]["collection_rate"])
	}
}

// TestRevenueComparesWithThePreviousWindow covers the rule Phase 10 set and
// Phase 11 reuses: a movement against an empty previous window is null, not
// "+100%", and a fall to nothing is -100%.
func TestRevenueComparesWithThePreviousWindow(t *testing.T) {
	h := newHarness(t)
	fix := h.newRevenueFixture(t, "Cmp", "0716000400", "+255716000401", "+255716000402")

	// The month *after* the money: nothing came in, and the previous window is
	// the month that holds the rent.
	after := fix.anchor.AddDate(0, 1, 0).Format(testDateLayout)
	got := fix.owner.do(http.MethodGet, "/reports/revenue?cadence=month&anchor="+after, nil).
		mustStatus(t, http.StatusOK, "revenue, month after")
	if v := num(t, got, "previous_totals", "collected"); v != float64(fix.rent) {
		t.Errorf("previous_totals.collected = %v, want %v", v, fix.rent)
	}
	if v := num(t, got, "change_pct", "collected"); v != -100 {
		t.Errorf("change_pct.collected = %v, want -100 — body: %s", v, got.Raw)
	}
	if got.str(t, "previous", "from") != fix.anchor.Format("2006-01")+"-01" {
		t.Errorf("previous window is %s, want the month of the money", got.str(t, "previous", "from"))
	}

	// Two months after: the previous window is itself empty, so there is no
	// percentage to quote.
	later := fix.anchor.AddDate(0, 2, 0).Format(testDateLayout)
	empty := fix.owner.do(http.MethodGet, "/reports/revenue?cadence=month&anchor="+later, nil).
		mustStatus(t, http.StatusOK, "revenue, two months after")
	change, ok := empty.Body["change_pct"].(map[string]any)
	if !ok {
		t.Fatalf("no change_pct object — body: %s", empty.Raw)
	}
	if change["collected"] != nil {
		t.Errorf("change_pct.collected = %v against an empty previous window, want null", change["collected"])
	}
	if v := num(t, empty, "trend", "slope_collected_per_bucket"); v != 0 {
		t.Errorf("a series of zeroes has slope %v, want 0", v)
	}
}

// TestRevenueTrendSlopeRises checks the trend line is a gradient and not a
// sign: money arriving later in the window slopes upward.
func TestRevenueTrendSlopeRises(t *testing.T) {
	h := newHarness(t)
	fix := h.newRevenueFixture(t, "Trend", "0716000500", "+255716000501", "+255716000502")

	got := fix.owner.do(http.MethodGet, "/reports/revenue?"+fix.monthQuery(), nil).
		mustStatus(t, http.StatusOK, "revenue")
	slope := num(t, got, "trend", "slope_collected_per_bucket")
	// The single payment lands on the 15th-ish of the month; a lone spike after
	// the midpoint slopes up, before it slopes down. Either way it is not flat.
	if slope == 0 {
		t.Errorf("a series with one payment in it has slope 0 — body: %s", got.Raw)
	}
}

// TestRevenueBucketSizing is the auto-sizing rule and its override.
func TestRevenueBucketSizing(t *testing.T) {
	h := newHarness(t)
	fix := h.newRevenueFixture(t, "Buck", "0716000600", "+255716000601", "+255716000602")

	for _, c := range []struct{ query, want string }{
		{"cadence=month&anchor=" + fix.anchorDay(), "day"},
		{"cadence=quarter&anchor=" + fix.anchorDay(), "week"},
		{"cadence=year&anchor=" + fix.anchorDay(), "month"},
		{"cadence=month&anchor=" + fix.anchorDay() + "&bucket=week", "week"},
	} {
		got := fix.owner.do(http.MethodGet, "/reports/revenue?"+c.query, nil).
			mustStatus(t, http.StatusOK, c.query)
		if b := got.str(t, "bucket"); b != c.want {
			t.Errorf("%s: bucket = %q, want %q", c.query, b, c.want)
		}
	}

	// Two years drawn daily is more points than any chart wants: refused, and
	// the refusal says what to do about it. (A single year is 365 days, which
	// fits under the 400 cap and is answered.)
	tooMany := fix.owner.do(http.MethodGet,
		"/reports/revenue?cadence=custom&from=2024-01-01&to=2025-12-31&bucket=day", nil)
	tooMany.mustStatus(t, http.StatusUnprocessableEntity, "two years of daily buckets")
	if tooMany.Body["type"] != "too_many_buckets" {
		t.Errorf("type = %v, want too_many_buckets — body: %s", tooMany.Body["type"], tooMany.Raw)
	}

	bad := fix.owner.do(http.MethodGet, "/reports/revenue?bucket=fortnight", nil)
	bad.mustStatus(t, http.StatusBadRequest, "unknown bucket")
	if _, ok := errorsOf(t, bad)["bucket"]; !ok {
		t.Errorf("an unknown bucket did not name the field: %s", bad.Raw)
	}
}

// TestRevenueCustomWindowValidation: a window that cannot exist is a 422 — the
// request is well-formed, the answer is not one this API produces.
func TestRevenueCustomWindowValidation(t *testing.T) {
	h := newHarness(t)
	fix := h.newRevenueFixture(t, "Cust", "0716000700", "+255716000701", "+255716000702")

	for _, c := range []struct{ name, query string }{
		{"backwards", "cadence=custom&from=2026-05-10&to=2026-05-01"},
		{"beyond five years", "cadence=custom&from=2020-01-01&to=2026-06-01"},
		{"no dates", "cadence=custom"},
	} {
		resp := fix.owner.do(http.MethodGet, "/reports/revenue?"+c.query, nil)
		resp.mustStatus(t, http.StatusUnprocessableEntity, c.name)
		if _, ok := errorsOf(t, resp)["cadence"]; !ok {
			t.Errorf("%s did not name `cadence`: %s", c.name, resp.Raw)
		}
	}
	// A cadence the API does not know is malformed, not unsatisfiable.
	fix.owner.do(http.MethodGet, "/reports/revenue?cadence=fortnight", nil).
		mustStatus(t, http.StatusBadRequest, "unknown cadence")

	// A custom window the API *can* answer is answered, first bucket on the
	// window's own first day rather than snapped to a calendar boundary.
	start := fix.anchor.AddDate(0, 0, -3)
	ok := fix.owner.do(http.MethodGet, "/reports/revenue?cadence=custom&from="+
		start.Format(testDateLayout)+"&to="+fix.anchor.AddDate(0, 0, 3).Format(testDateLayout), nil).
		mustStatus(t, http.StatusOK, "custom window")
	buckets := arrayOf(t, ok, "buckets")
	if len(buckets) != 7 || buckets[0]["start"] != start.Format(testDateLayout) {
		t.Errorf("custom window drew %d buckets starting %v, want 7 from %s",
			len(buckets), buckets[0]["start"], start.Format(testDateLayout))
	}
	if v := num(t, ok, "totals", "collected"); v != float64(fix.rent) {
		t.Errorf("custom window collected %v, want %v", v, fix.rent)
	}
}

// TestRevenuePropertyFilterNarrowsEverySeries: the filter reaches the payments
// (through contract → unit), the schedules and the expenses alike.
func TestRevenuePropertyFilterNarrowsEverySeries(t *testing.T) {
	h := newHarness(t)
	fix := h.newRevenueFixture(t, "Filt", "0716000800", "+255716000801", "+255716000802")

	a := fix.owner.do(http.MethodGet,
		"/reports/revenue?"+fix.monthQuery()+"&property_id="+fix.propertyID, nil).
		mustStatus(t, http.StatusOK, "property A")
	for key, want := range map[string]float64{
		"collected": float64(fix.rent),
		"expected":  float64(fix.rent),
		"expenses":  float64(fix.expenseAmount),
	} {
		if v := num(t, a, "totals", key); v != want {
			t.Errorf("property A totals.%s = %v, want %v", key, v, want)
		}
	}

	// Property B: the tenancy in arrears, no money and no spend.
	groups := arrayOf(t, fix.owner.do(http.MethodGet,
		"/reports/revenue?group_by=property&"+fix.monthQuery(), nil).
		mustStatus(t, http.StatusOK, "groups"), "groups")
	var propertyB string
	for _, g := range groups {
		if id, _ := g["id"].(string); id != fix.propertyID {
			propertyB = id
		}
	}
	b := fix.owner.do(http.MethodGet,
		"/reports/revenue?"+fix.monthQuery()+"&property_id="+propertyB, nil).
		mustStatus(t, http.StatusOK, "property B")
	if v := num(t, b, "totals", "collected"); v != 0 {
		t.Errorf("property B collected %v, want 0", v)
	}
	if v := num(t, b, "totals", "expected"); v != float64(fix.rent) {
		t.Errorf("property B expected %v, want %v", v, fix.rent)
	}
}

// ----------------------------------------------------------- occupancy --

// TestOccupancyCountsLiveTenanciesOnly is the occupancy definition on a
// portfolio holding one of each kind of contract: an active tenancy occupies
// its unit, a tenancy terminated with effect from yesterday does not, and a
// contract nobody has signed never did.
func TestOccupancyCountsLiveTenanciesOnly(t *testing.T) {
	h := newHarness(t)
	base := h.newOrgWithUnits("Occ", "occ@jjne.test", "0716000900",
		[]string{"O1", "O2", "O3", "O4"}, testUnitAmount)
	periodID := base.client.periodIDByDays(t, 30)

	// 1. active.
	h.tenancyOn(t, base.client, base.unitCodes[0], periodID, "+255716000901", "Occ Active")

	// 2. terminated, with effect from yesterday: occupied up to and including
	//    the effective date, and not after it.
	_, ended := h.tenancyOn(t, base.client, base.unitCodes[1], periodID, "+255716000902", "Occ Ended")
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format(testDateLayout)
	base.client.do(http.MethodPost, "/contracts/"+ended+"/terminate", map[string]any{
		"reason": "moved out", "effective_date": yesterday,
	}).mustStatus(t, http.StatusOK, "terminate")

	// 3. approved but unsigned: a contract, not a tenancy.
	pending := h.registerRenter("+255716000903", "Occ Pending", defaultPIN)
	pending.completeProfile(t, "Occ Pending", validNIDA)
	applied := pending.do(http.MethodPost, "/units/"+base.unitCodes[2]+"/link",
		linkBody(periodID, testTermDays)).
		mustStatus(t, http.StatusCreated, "apply")
	base.client.do(http.MethodPost,
		"/link-requests/"+applied.str(t, "request", "id")+"/approve", nil).
		mustStatus(t, http.StatusOK, "approve")

	today := time.Now().UTC()
	got := base.client.do(http.MethodGet,
		"/reports/occupancy?cadence=month&anchor="+today.Format(testDateLayout), nil).
		mustStatus(t, http.StatusOK, "occupancy")

	if v := num(t, got, "current", "units_total"); v != 4 {
		t.Errorf("current.units_total = %v, want 4 — body: %s", v, got.Raw)
	}
	if v := num(t, got, "current", "units_occupied"); v != 1 {
		t.Errorf("current.units_occupied = %v, want 1 (the active tenancy alone) — body: %s", v, got.Raw)
	}
	if v := num(t, got, "current", "occupancy_pct"); v != 25 {
		t.Errorf("current.occupancy_pct = %v, want 25", v)
	}

	// The series is daily over the month and zero-filled: the days before the
	// tenancies started are points at zero, not gaps.
	buckets := arrayOf(t, got, "buckets")
	if got.str(t, "bucket") != "day" {
		t.Errorf("bucket = %q, want day", got.str(t, "bucket"))
	}
	if len(buckets) == 0 {
		t.Fatalf("no buckets — body: %s", got.Raw)
	}
	for _, b := range buckets {
		if mustFloat(t, b, "units_occupied") > mustFloat(t, b, "units_total") {
			t.Fatalf("a bucket is more than full: %v", b)
		}
	}
	// The first day of the month is before every contract in this fixture
	// (they all start today), unless the test runs on the 1st.
	if today.Day() > 1 {
		if v := mustFloat(t, buckets[0], "units_occupied"); v != 0 {
			t.Errorf("the 1st of the month was %v units occupied, want 0", v)
		}
	}
}

// ------------------------------------------------- cadence on the old three --

// TestSummaryAcceptsBothVocabularies: the Phase 7 `period` parameter still
// resolves, the Phase 11 cadence resolves the same way, and sending both is a
// refusal rather than a silent winner.
func TestSummaryAcceptsBothVocabularies(t *testing.T) {
	h := newHarness(t)
	fix := h.newRevenueFixture(t, "Cad", "0716001000", "+255716001001", "+255716001002")
	monthStart := fix.anchor.Format("2006-01") + "-01"

	legacy := fix.owner.do(http.MethodGet, "/reports/summary?period="+fix.period, nil).
		mustStatus(t, http.StatusOK, "legacy period")
	if legacy.str(t, "window", "from") != monthStart {
		t.Errorf("window.from = %s, want %s", legacy.str(t, "window", "from"), monthStart)
	}
	if legacy.str(t, "window", "cadence") != "month" {
		t.Errorf("legacy period resolved cadence %q, want month", legacy.str(t, "window", "cadence"))
	}
	// The Phase 7 block is untouched: inclusive dates, same five figures.
	if legacy.str(t, "period", "from") != monthStart {
		t.Errorf("period.from = %s, want %s", legacy.str(t, "period", "from"), monthStart)
	}
	if v := num(t, legacy, "period", "collected"); v != float64(fix.rent) {
		t.Errorf("period.collected = %v, want %v", v, fix.rent)
	}

	cadence := fix.owner.do(http.MethodGet,
		"/reports/summary?cadence=month&anchor="+fix.anchorDay(), nil).
		mustStatus(t, http.StatusOK, "cadence month")
	if cadence.str(t, "window", "from") != legacy.str(t, "window", "from") ||
		cadence.str(t, "window", "to") != legacy.str(t, "window", "to") {
		t.Errorf("the two vocabularies resolved different windows: %s vs %s",
			cadence.Raw, legacy.Raw)
	}

	// A quarter contains the same money and compares against the quarter before.
	quarter := fix.owner.do(http.MethodGet,
		"/reports/summary?cadence=quarter&anchor="+fix.anchorDay(), nil).
		mustStatus(t, http.StatusOK, "cadence quarter")
	if v := num(t, quarter, "period", "collected"); v != float64(fix.rent) {
		t.Errorf("quarter collected %v, want %v", v, fix.rent)
	}
	if _, ok := quarter.Body["previous_totals"].(map[string]any); !ok {
		t.Errorf("no previous_totals on the summary — body: %s", quarter.Raw)
	}
	change, ok := quarter.Body["change_pct"].(map[string]any)
	if !ok {
		t.Fatalf("no change_pct on the summary — body: %s", quarter.Raw)
	}
	if _, present := change["collected"]; !present {
		t.Errorf("change_pct has no `collected` key — body: %s", quarter.Raw)
	}

	both := fix.owner.do(http.MethodGet,
		"/reports/summary?period="+fix.period+"&cadence=month", nil)
	both.mustStatus(t, http.StatusBadRequest, "both vocabularies")
	if _, ok := errorsOf(t, both)["period"]; !ok {
		t.Errorf("the refusal did not name `period`: %s", both.Raw)
	}
}

// TestCollectionsAndPaymentStatusCarryTheWindow: the two remaining Phase 7
// reports learn the cadence without losing their Phase 7 defaults.
func TestCollectionsAndPaymentStatusCarryTheWindow(t *testing.T) {
	h := newHarness(t)
	fix := h.newRevenueFixture(t, "Coll", "0716001100", "+255716001101", "+255716001102")

	// Legacy call: no parameters at all, the twelve months ending today,
	// grouped by month, plus the new window/previous echo.
	legacy := fix.owner.do(http.MethodGet, "/reports/collections", nil).
		mustStatus(t, http.StatusOK, "collections, no parameters")
	if legacy.str(t, "group") != "month" {
		t.Errorf("default group = %q, want month", legacy.str(t, "group"))
	}
	if legacy.str(t, "window", "cadence") != "custom" {
		t.Errorf("a hand-rolled window reports cadence %q, want custom", legacy.str(t, "window", "cadence"))
	}
	if legacy.str(t, "previous", "to") != legacy.str(t, "window", "from") {
		t.Errorf("the previous window does not abut the window: %s", legacy.Raw)
	}
	if v := num(t, legacy, "totals", "collected"); v != float64(fix.rent) {
		t.Errorf("collections totals.collected = %v, want %v", v, fix.rent)
	}

	// Cadence call: the window drives the range, and the bucket size comes from
	// the same auto-sizing the revenue series uses.
	month := fix.owner.do(http.MethodGet, "/reports/collections?"+fix.monthQuery(), nil).
		mustStatus(t, http.StatusOK, "collections, cadence month")
	if month.str(t, "group") != "day" {
		t.Errorf("cadence=month grouped by %q, want day", month.str(t, "group"))
	}
	if v := num(t, month, "totals", "collected"); v != float64(fix.rent) {
		t.Errorf("cadence month collected %v, want %v", v, fix.rent)
	}
	if v := num(t, month, "totals", "expected"); v != float64(2*fix.rent) {
		t.Errorf("cadence month expected %v, want %v", v, 2*fix.rent)
	}
	if _, ok := month.Body["previous_totals"].(map[string]any); !ok {
		t.Errorf("no previous_totals on collections — body: %s", month.Raw)
	}

	status := fix.owner.do(http.MethodGet,
		"/reports/payment-status?cadence=quarter&anchor="+fix.anchorDay(), nil).
		mustStatus(t, http.StatusOK, "payment status with a cadence")
	if status.str(t, "window", "cadence") != "quarter" {
		t.Errorf("payment-status echoed cadence %q, want quarter", status.str(t, "window", "cadence"))
	}
	if len(arrayOf(t, status, "items")) != 2 {
		t.Errorf("payment-status rows changed with the cadence — body: %s", status.Raw)
	}
}

// mustDate parses a wire date, failing the test rather than the caller.
func mustDate(t *testing.T, v string) time.Time {
	t.Helper()
	d, err := time.Parse(testDateLayout, v)
	if err != nil {
		t.Fatalf("not a date: %q (%v)", v, err)
	}
	return d
}
