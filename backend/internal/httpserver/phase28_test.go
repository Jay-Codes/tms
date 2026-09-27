package httpserver_test

import (
	"context"
	"math"
	"net/http"
	"testing"
)

// Phase 28 — projections, break-even and ROI (PLAN2 Phase 28).
//
// The arithmetic is table-tested in internal/report/projection_test.go against
// hand-computed fixtures. These tests are about what the endpoint feeds it:
// the investment fields, the capital flag, the same cash definitions as the
// revenue report (refunds come off), the scenario bounds, and org scoping.

// projection posts one forecast request.
func (c *client) projection(body map[string]any) response {
	return c.do(http.MethodPost, "/reports/projection", body)
}

func TestPhase28PropertyInvestmentFields(t *testing.T) {
	h := newHarness(t)
	base := h.newOrgWithUnits("Inv28", "inv28@jjne.test", "0716280100", []string{"I1"}, testUnitAmount)
	path := "/properties/" + base.propertyID

	got := base.client.do(http.MethodGet, path, nil).mustStatus(t, http.StatusOK, "get property")
	for _, k := range []string{"purchase_price", "purchase_date", "current_value"} {
		if v, ok := got.Body["property"].(map[string]any)[k]; !ok || v != nil {
			t.Errorf("fresh property %s = %v (present %v), want null", k, v, ok)
		}
	}

	patched := base.client.do(http.MethodPatch, path, map[string]any{
		"purchase_price": 85_000_000, "purchase_date": "2021-03-15", "current_value": 120_000_000,
	}).mustStatus(t, http.StatusOK, "set investment")
	if v := num(t, patched, "property", "purchase_price"); v != 85_000_000 {
		t.Errorf("purchase_price = %v", v)
	}
	if d := patched.str(t, "property", "purchase_date"); d != "2021-03-15" {
		t.Errorf("purchase_date = %q", d)
	}
	got = base.client.do(http.MethodGet, path, nil).mustStatus(t, http.StatusOK, "re-read property")
	if v := num(t, got, "property", "current_value"); v != 120_000_000 {
		t.Errorf("current_value = %v", v)
	}
	// Leaving a field out keeps it; null clears it.
	cleared := base.client.do(http.MethodPatch, path, map[string]any{"current_value": nil}).
		mustStatus(t, http.StatusOK, "clear value")
	prop, _ := cleared.Body["property"].(map[string]any)
	if prop["current_value"] != nil || prop["purchase_price"] == nil {
		t.Errorf("after clearing current_value: %v", prop)
	}

	for _, c := range []struct {
		name string
		body map[string]any
		key  string
	}{
		{"zero price", map[string]any{"purchase_price": 0}, "purchase_price"},
		{"fractional price", map[string]any{"purchase_price": 10.5}, "purchase_price"},
		{"future purchase", map[string]any{"purchase_date": "2999-01-01"}, "purchase_date"},
		{"not a date", map[string]any{"purchase_date": "15/03/2021"}, "purchase_date"},
		{"negative value", map[string]any{"current_value": -5}, "current_value"},
	} {
		r := base.client.do(http.MethodPatch, path, c.body)
		if r.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 — %s", c.name, r.Code, r.Raw)
			continue
		}
		fieldErrorFor(t, r, c.key)
	}

	// Audited like every other property edit.
	var n int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = 'property.update'
		   AND entity_id = $1 AND after->>'purchase_price' = '85000000'`, base.propertyID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Error("no property.update audit row carries the purchase price")
	}
}

func TestPhase28CapitalCategory(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.createOrg("Cap28", "Cap Owner", "cap28@jjne.test", "0716280200", "supersecret")

	created := owner.do(http.MethodPost, "/org/expense-categories", map[string]any{
		"name": "Renovations", "is_capital": true,
	}).mustStatus(t, http.StatusCreated, "create capital category")
	if created.Body["category"].(map[string]any)["is_capital"] != true {
		t.Fatalf("created category = %s", created.Raw)
	}
	id := created.str(t, "category", "id")
	patched := owner.do(http.MethodPatch, "/org/expense-categories/"+id, map[string]any{"is_capital": false}).
		mustStatus(t, http.StatusOK, "unset capital")
	if patched.Body["category"].(map[string]any)["is_capital"] != false {
		t.Errorf("patched category = %s", patched.Raw)
	}
	// The seeded categories are running costs.
	for _, c := range listOf(t, owner.do(http.MethodGet, "/org/expense-categories", nil).
		mustStatus(t, http.StatusOK, "list categories")) {
		if c["is_capital"] != false {
			t.Errorf("category %v is_capital = %v, want false", c["name"], c["is_capital"])
		}
	}
}

func TestPhase28ProjectionEndpoint(t *testing.T) {
	h := newHarness(t)
	fix := h.newRevenueFixture(t, "Proj28", "0716280300", "+255716280301", "+255716280302")

	// A rent refund in the reported month comes off the cash, exactly as it
	// does on GET /reports/revenue (Phase 22 §22.5).
	const refund = 10_000
	if _, err := h.pool.Exec(context.Background(),
		`INSERT INTO rent_refunds (org_id, contract_id, amount, method, reason, refunded_at)
		 VALUES ($1, $2, $3, 'cash', 'test refund', $4)`,
		fix.orgID, fix.paidLink, refund, fix.anchor.Add(14*3600e9)); err != nil {
		t.Fatalf("insert refund: %v", err)
	}
	revenue := fix.owner.do(http.MethodGet, "/reports/revenue?"+fix.monthQuery(), nil).
		mustStatus(t, http.StatusOK, "revenue")

	portfolio := fix.owner.projection(nil).mustStatus(t, http.StatusOK, "portfolio projection")
	if s := portfolio.str(t, "scope"); s != "portfolio" {
		t.Errorf("scope = %q", s)
	}
	if got, want := num(t, portfolio, "baseline", "collected"), num(t, revenue, "totals", "collected"); got != want {
		t.Errorf("baseline collected %v, revenue report %v — the two must agree", got, want)
	}
	if got := num(t, portfolio, "baseline", "collected"); got != float64(fix.rent-refund) {
		t.Errorf("baseline collected = %v, want rent less refund %v", got, fix.rent-refund)
	}
	if got := num(t, portfolio, "baseline", "expected"); got != float64(2*fix.rent) {
		t.Errorf("baseline expected = %v, want %v", got, 2*fix.rent)
	}
	if got := num(t, portfolio, "baseline", "running_expenses"); got != float64(fix.expenseAmount) {
		t.Errorf("baseline running expenses = %v, want %v", got, fix.expenseAmount)
	}
	if n := len(arrayOf(t, portfolio, "months")); n != 24 {
		t.Errorf("default horizon = %d months, want 24", n)
	}
	if n := len(arrayOf(t, portfolio, "properties")); n != 2 {
		t.Errorf("properties = %d, want 2", n)
	}
	// Without any purchase price the returns are still measured, against spend.
	if s := portfolio.str(t, "investment", "break_even_status"); s == "" {
		t.Errorf("break-even status without prices is empty")
	}
	if v := num(t, portfolio, "investment", "spent_to_date"); v != float64(fix.expenseAmount) {
		t.Errorf("spent to date without prices = %v, want the running expense %v", v, fix.expenseAmount)
	}
	if s := portfolio.str(t, "applied", "basis"); s != "contracts" {
		t.Errorf("default basis = %q, want contracts", s)
	}

	// The unit picker: every live unit with its price, and which the basis assumes let.
	units := arrayOf(t, portfolio, "units")
	if len(units) == 0 {
		t.Fatalf("no units in the projection: %s", portfolio.Raw)
	}
	for _, u := range units {
		if u["assumed"] != false || u["name"] == "" || u["property_name"] == "" {
			t.Errorf("unit under signed contracts only = %v", u)
		}
	}
	best := fix.owner.projection(map[string]any{"basis": "best_case"}).mustStatus(t, http.StatusOK, "best case")
	var picked string
	for _, u := range arrayOf(t, best, "units") {
		if u["lettable"] == true && u["assumed"] != true {
			t.Errorf("best case leaves out lettable unit %v", u)
		}
		if u["let_until"] == nil && u["lettable"] == true && picked == "" {
			picked, _ = u["id"].(string)
		}
	}
	if num(t, best, "totals", "income") < num(t, portfolio, "totals", "income") {
		t.Errorf("best case income %v below signed contracts %v",
			num(t, best, "totals", "income"), num(t, portfolio, "totals", "income"))
	}
	if picked != "" {
		sel := fix.owner.projection(map[string]any{"basis": "selected", "unit_ids": []string{picked}}).
			mustStatus(t, http.StatusOK, "selected unit")
		if v := num(t, sel, "baseline", "units_assumed"); v != 1 {
			t.Errorf("selected units assumed = %v, want 1", v)
		}
		if ids, _ := sel.Body["applied"].(map[string]any)["unit_ids"].([]any); len(ids) != 1 || ids[0] != picked {
			t.Errorf("applied unit ids = %v", ids)
		}
	}

	// One property, with a price and some capital spend.
	fix.owner.do(http.MethodPatch, "/properties/"+fix.propertyID, map[string]any{
		"purchase_price": 1_000_000,
	}).mustStatus(t, http.StatusOK, "price property A")
	capital := fix.owner.do(http.MethodPost, "/org/expense-categories", map[string]any{
		"name": "Renovations", "is_capital": true,
	}).mustStatus(t, http.StatusCreated, "capital category").str(t, "category", "id")
	fix.owner.do(http.MethodPost, "/expenses", map[string]any{
		"property_id": fix.propertyID, "category_id": capital, "amount": 200_000,
		"incurred_on": fix.anchorDay(), "vendor": "Fundi Ali",
	}).mustStatus(t, http.StatusCreated, "capital expense")

	one := fix.owner.projection(map[string]any{
		"property_id": fix.propertyID, "horizon_months": 12, "collection_rate_pct": 100,
	}).mustStatus(t, http.StatusOK, "property projection")
	if s := one.str(t, "scope"); s != "property" {
		t.Errorf("scope = %q", s)
	}
	if id := one.str(t, "property", "id"); id != fix.propertyID {
		t.Errorf("property = %q", id)
	}
	if n := len(arrayOf(t, one, "months")); n != 12 {
		t.Errorf("months = %d, want 12", n)
	}
	// Capital spend is spent money, not a running cost; the price adds to it.
	if v := num(t, one, "investment", "spent_to_date"); v != float64(1_200_000+fix.expenseAmount) {
		t.Errorf("spent to date = %v, want price + capital + running", v)
	}
	if v := num(t, one, "investment", "capital_spend"); v != 200_000 {
		t.Errorf("capital spend = %v", v)
	}
	if v := num(t, one, "baseline", "running_expenses"); v != float64(fix.expenseAmount) {
		t.Errorf("running expenses = %v, want only the running %v", v, fix.expenseAmount)
	}
	if v := num(t, one, "baseline", "capital_expenses"); v != 200_000 {
		t.Errorf("capital expenses in the window = %v", v)
	}
	hm := num(t, one, "baseline", "history_months")
	if want := math.Round(float64(fix.expenseAmount) / hm); num(t, one, "baseline", "running_expenses_monthly") != want {
		t.Errorf("running monthly = %v, want %v over %v months",
			num(t, one, "baseline", "running_expenses_monthly"), want, hm)
	}
	if s := one.str(t, "applied", "collection_rate_source"); s != "scenario" {
		t.Errorf("collection source = %q", s)
	}
	if s := one.str(t, "investment", "break_even_status"); s == "no_costs" || s == "reached" {
		t.Errorf("a property 1,200,000 behind reports %q", s)
	}
	// The cumulative line starts from the cash made to date.
	months := arrayOf(t, one, "months")
	cash := num(t, one, "investment", "cash_to_date")
	if first := months[0]; mustFloat(t, first, "cumulative") != cash+mustFloat(t, first, "net") {
		t.Errorf("month 0 cumulative %v != cash to date %v + net %v",
			first["cumulative"], cash, first["net"])
	}
	if want := float64(fix.rent - refund - fix.expenseAmount - 200_000 - 1_000_000); cash != want {
		t.Errorf("cash to date = %v, want %v", cash, want)
	}
	if first := months[0]; mustFloat(t, first, "cumulative") != mustFloat(t, first, "cumulative_income")-mustFloat(t, first, "cumulative_spent") {
		t.Errorf("month 0 cumulative is not income less spend: %v", first)
	}

	// The portfolio counts every property; the one price adds to its spend.
	portfolio = fix.owner.projection(map[string]any{}).mustStatus(t, http.StatusOK, "portfolio again")
	if v := num(t, portfolio, "investment", "purchase_price"); v != 1_000_000 {
		t.Errorf("portfolio purchase price = %v", v)
	}
}

func TestPhase28ProjectionValidation(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.createOrg("Val28", "Val Owner", "val28@jjne.test", "0716280400", "supersecret")

	for _, c := range []struct {
		name string
		body map[string]any
		key  string
	}{
		{"horizon zero", map[string]any{"horizon_months": 0}, "horizon_months"},
		{"horizon too long", map[string]any{"horizon_months": 121}, "horizon_months"},
		{"unknown basis", map[string]any{"basis": "wishful"}, "basis"},
		{"malformed unit", map[string]any{"basis": "selected", "unit_ids": []string{"nope"}}, "unit_ids"},
		{"collection below 0", map[string]any{"collection_rate_pct": -1}, "collection_rate_pct"},
		{"rent below -100", map[string]any{"rent_change_pct": -150}, "rent_change_pct"},
		{"expenses above 500", map[string]any{"expense_change_pct": 900}, "expense_change_pct"},
		{"malformed property", map[string]any{"property_id": "nope"}, "property_id"},
	} {
		r := owner.projection(c.body)
		if r.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 — %s", c.name, r.Code, r.Raw)
			continue
		}
		fieldErrorFor(t, r, c.key)
	}
	if r := owner.projection(map[string]any{"horizon": 12}); r.Code != http.StatusBadRequest {
		t.Errorf("unknown field: status %d, want 400", r.Code)
	}
	if r := owner.projection(map[string]any{"property_id": "7f0c1c2e-1111-4a4a-9b9b-000000000000"}); r.Code != http.StatusNotFound {
		t.Errorf("unknown property: status %d, want 404", r.Code)
	}
	// An org with no properties still gets an (empty) forecast.
	empty := owner.projection(map[string]any{"horizon_months": 3}).mustStatus(t, http.StatusOK, "empty org")
	if n := len(arrayOf(t, empty, "months")); n != 3 {
		t.Errorf("months = %d, want 3", n)
	}
}

func TestPhase28ProjectionScenarios(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.createOrg("Scn28", "Scn Owner", "scn28@jjne.test", "0716280500", "supersecret")
	const path = "/reports/projection/scenarios"

	created := owner.do(http.MethodPost, path, map[string]any{
		"name": "Rent up 10%", "horizon_months": 36, "rent_change_pct": 10,
		"basis": "selected", "unit_ids": []string{"7f0c1c2e-1111-4a4a-9b9b-000000000001"},
	}).mustStatus(t, http.StatusCreated, "save scenario")
	id := created.str(t, "scenario", "id")
	if s := created.str(t, "scenario", "basis"); s != "selected" {
		t.Errorf("basis = %q", s)
	}
	if ids, _ := created.Body["scenario"].(map[string]any)["unit_ids"].([]any); len(ids) != 1 {
		t.Errorf("unit ids = %v", ids)
	}
	if v := num(t, created, "scenario", "horizon_months"); v != 36 {
		t.Errorf("horizon = %v", v)
	}
	if created.Body["scenario"].(map[string]any)["collection_rate_pct"] != nil {
		t.Errorf("unset collection rate should stay null: %s", created.Raw)
	}
	// Defaults: a 24-month horizon, no changes.
	plain := owner.do(http.MethodPost, path, map[string]any{"name": "Baseline"}).
		mustStatus(t, http.StatusCreated, "save baseline scenario")
	if v := num(t, plain, "scenario", "horizon_months"); v != 24 {
		t.Errorf("default horizon = %v", v)
	}
	if s := plain.str(t, "scenario", "basis"); s != "contracts" {
		t.Errorf("default basis = %q", s)
	}

	if r := owner.do(http.MethodPost, path, map[string]any{"name": "rent UP 10%"}); r.Code != http.StatusConflict {
		t.Errorf("duplicate name (any case): status %d, want 409", r.Code)
	}
	if r := owner.do(http.MethodPost, path, map[string]any{"name": " "}); r.Code != http.StatusBadRequest {
		t.Errorf("blank name: status %d, want 400", r.Code)
	}
	if r := owner.do(http.MethodPost, path, map[string]any{"name": "x", "basis": "hopeful"}); r.Code != http.StatusBadRequest {
		t.Errorf("bad basis: status %d, want 400", r.Code)
	}
	if n := len(listOf(t, owner.do(http.MethodGet, path, nil).mustStatus(t, http.StatusOK, "list"))); n != 2 {
		t.Errorf("scenarios = %d, want 2", n)
	}

	// Another org cannot see or delete it.
	other, _ := h.createOrg("Scn28b", "Other Owner", "scn28b@jjne.test", "0716280501", "supersecret")
	if n := len(listOf(t, other.do(http.MethodGet, path, nil).mustStatus(t, http.StatusOK, "other list"))); n != 0 {
		t.Errorf("other org sees %d scenarios", n)
	}
	other.do(http.MethodDelete, path+"/"+id, nil).mustStatus(t, http.StatusNotFound, "other org delete")

	owner.do(http.MethodDelete, path+"/"+id, nil).mustStatus(t, http.StatusNoContent, "delete")
	owner.do(http.MethodDelete, path+"/"+id, nil).mustStatus(t, http.StatusNotFound, "delete again")
}

// fieldErrorFor fails unless the problem document names `key` among its field
// errors.
func fieldErrorFor(t *testing.T, r response, key string) {
	t.Helper()
	if _, ok := errorsOf(t, r)[key]; !ok {
		t.Errorf("no field error for %q — body: %s", key, r.Raw)
	}
}
