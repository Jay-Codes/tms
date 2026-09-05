package httpserver_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Phase 10 — the expense ledger (SPEC §5.11, FLOWS 12, PLAN2 Phase 10).
//
// The tests below are grouped by the thing that can go wrong rather than by
// endpoint: the seeded vocabulary, the validation an expense has to pass, the
// void semantics that make a correction a record rather than a deletion, the
// arithmetic behind the summary, and the CSV a landlord opens in a spreadsheet.

// expenseFixture is one org with two properties, its seeded categories and a
// client to record against.
type expenseFixture struct {
	client      *client
	orgID       string
	propertyID  string
	property2ID string
	unitIDs     []string
	// otherUnitID is a unit of the *second* property — the one that must not
	// be accepted against the first.
	otherUnitID string
	categories  []map[string]any
}

func (h *harness) newExpenseFixture(t *testing.T, tag, phone string) expenseFixture {
	t.Helper()
	base := h.newOrgWithUnits(tag, strings.ToLower(tag)+"@jjne.test", phone,
		[]string{"Room 1", "Room 2"}, testUnitAmount)

	fix := expenseFixture{
		client: base.client, orgID: base.orgID,
		propertyID: base.propertyID, unitIDs: base.unitIDs,
	}

	second := base.client.do(http.MethodPost, "/properties", map[string]any{
		"name": tag + " Block B", "location_text": "Dar es Salaam",
	}).mustStatus(t, http.StatusCreated, "second property")
	fix.property2ID = second.str(t, "property", "id")

	other := base.client.do(http.MethodPost, "/properties/"+fix.property2ID+"/units", map[string]any{
		"name": "Room B1",
	}).mustStatus(t, http.StatusCreated, "unit on second property")
	fix.otherUnitID = other.str(t, "unit", "id")

	fix.categories = listOf(t, base.client.do(http.MethodGet, "/org/expense-categories", nil).
		mustStatus(t, http.StatusOK, "expense categories"))
	return fix
}

// categoryID returns the id of the seeded category with the given name.
func (f expenseFixture) categoryID(t *testing.T, name string) string {
	t.Helper()
	for _, c := range f.categories {
		if c["name"] == name {
			id, _ := c["id"].(string)
			return id
		}
	}
	t.Fatalf("no seeded category named %q — got %v", name, f.categories)
	return ""
}

// record posts one expense and returns its id.
func (f expenseFixture) record(t *testing.T, body map[string]any) string {
	t.Helper()
	return f.client.do(http.MethodPost, "/expenses", body).
		mustStatus(t, http.StatusCreated, "record expense").str(t, "expense", "id")
}

// today is the wire date the ledger's bounds are read against.
func today() string { return time.Now().UTC().Format("2006-01-02") }

// ---------------------------------------------------------- categories --

func TestExpenseCategoriesSeededLazilyAndIdempotently(t *testing.T) {
	h := newHarness(t)
	fix := h.newExpenseFixture(t, "Cats", "0716010100")

	// The eight defaults arrive in their seeded order, all marked as defaults.
	want := []string{
		"Repairs & maintenance", "Utilities", "Security", "Cleaning",
		"Taxes & levies", "Insurance", "Management fees", "Other",
	}
	if len(fix.categories) != len(want) {
		t.Fatalf("seeded %d categories, want %d: %v", len(fix.categories), len(want), fix.categories)
	}
	for i, name := range want {
		if fix.categories[i]["name"] != name {
			t.Errorf("category %d = %v, want %q", i, fix.categories[i]["name"], name)
		}
		if fix.categories[i]["is_default"] != true {
			t.Errorf("category %q is not marked is_default", name)
		}
		if fix.categories[i]["active"] != true {
			t.Errorf("category %q is not active", name)
		}
	}

	// Seeding is lazy and must therefore be idempotent: a second read of the
	// same org must not seed a second set.
	again := listOf(t, fix.client.do(http.MethodGet, "/org/expense-categories", nil).
		mustStatus(t, http.StatusOK, "second read"))
	if len(again) != len(want) {
		t.Fatalf("second read returned %d categories, want %d — seeding is not idempotent", len(again), len(want))
	}
	firstIDs := fix.categoryID(t, "Utilities")
	if again[1]["id"] != firstIDs {
		t.Errorf("the seeded ids changed between reads: %v vs %v", again[1]["id"], firstIDs)
	}
}

func TestExpenseCategoryCRUD(t *testing.T) {
	h := newHarness(t)
	fix := h.newExpenseFixture(t, "CatCRUD", "0716010200")

	created := fix.client.do(http.MethodPost, "/org/expense-categories",
		map[string]any{"name": "Landscaping"}).
		mustStatus(t, http.StatusCreated, "create category")
	id := created.str(t, "category", "id")
	if created.Body["category"].(map[string]any)["is_default"] != false {
		t.Error("a category created by the landlord must not be marked is_default")
	}

	// Duplicate names are refused case-insensitively: two categories differing
	// only in capitalisation would be two words for the same thing.
	dup := fix.client.do(http.MethodPost, "/org/expense-categories",
		map[string]any{"name": "landscaping"})
	dup.mustStatus(t, http.StatusConflict, "duplicate category")
	if dup.Body["type"] != "category_exists" {
		t.Errorf("duplicate category type = %v, want category_exists", dup.Body["type"])
	}

	// Rename and deactivate. A deactivated category still appears in the list
	// — the settings screen has to be able to switch it back on.
	patched := fix.client.do(http.MethodPatch, "/org/expense-categories/"+id,
		map[string]any{"name": "Grounds", "active": false}).
		mustStatus(t, http.StatusOK, "patch category")
	cat := patched.Body["category"].(map[string]any)
	if cat["name"] != "Grounds" || cat["active"] != false {
		t.Errorf("patched category = %v, want name Grounds and active false", cat)
	}
	rows := listOf(t, fix.client.do(http.MethodGet, "/org/expense-categories", nil).
		mustStatus(t, http.StatusOK, "list after deactivate"))
	if !containsCategory(rows, id) {
		t.Error("a deactivated category disappeared from the list; it cannot be reactivated")
	}

	// An inactive category may not be filed against.
	refused := fix.client.do(http.MethodPost, "/expenses", map[string]any{
		"property_id": fix.propertyID, "category_id": id,
		"amount": 5000, "incurred_on": today(),
	})
	refused.mustStatus(t, http.StatusUnprocessableEntity, "inactive category")
	if _, ok := errorsOf(t, refused)["category_id"]; !ok {
		t.Errorf("inactive category did not name category_id: %s", refused.Raw)
	}

	// Unused: it may go. In use: it may not.
	fix.client.do(http.MethodDelete, "/org/expense-categories/"+id, nil).
		mustStatus(t, http.StatusNoContent, "delete unused category")
	after := listOf(t, fix.client.do(http.MethodGet, "/org/expense-categories", nil).
		mustStatus(t, http.StatusOK, "list after delete"))
	if containsCategory(after, id) {
		t.Error("a soft-deleted category is still listed")
	}

	utilities := fix.categoryID(t, "Utilities")
	fix.record(t, map[string]any{
		"property_id": fix.propertyID, "category_id": utilities,
		"amount": 12000, "incurred_on": today(),
	})
	inUse := fix.client.do(http.MethodDelete, "/org/expense-categories/"+utilities, nil)
	inUse.mustStatus(t, http.StatusConflict, "delete a category in use")
	if inUse.Body["type"] != "category_in_use" {
		t.Errorf("in-use delete type = %v, want category_in_use", inUse.Body["type"])
	}
}

func containsCategory(rows []map[string]any, id string) bool {
	for _, row := range rows {
		if row["id"] == id {
			return true
		}
	}
	return false
}

// errorsOf reads the per-field `errors` object of a problem document.
func errorsOf(t *testing.T, r response) map[string]any {
	t.Helper()
	out, ok := r.Body["errors"].(map[string]any)
	if !ok {
		t.Fatalf("response carries no `errors` object — body: %s", r.Raw)
	}
	return out
}

// ---------------------------------------------------------- validation --

func TestExpenseValidation(t *testing.T) {
	h := newHarness(t)
	fix := h.newExpenseFixture(t, "Valid", "0716010300")
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	overmorrow := time.Now().UTC().AddDate(0, 0, 3).Format("2006-01-02")

	cases := []struct {
		name  string
		body  map[string]any
		want  int
		field string
	}{
		{
			name:  "amount of zero",
			body:  map[string]any{"property_id": fix.propertyID, "amount": 0, "incurred_on": today()},
			want:  http.StatusBadRequest,
			field: "amount",
		},
		{
			name:  "amount over a billion",
			body:  map[string]any{"property_id": fix.propertyID, "amount": 1_000_000_001, "incurred_on": today()},
			want:  http.StatusBadRequest,
			field: "amount",
		},
		{
			name:  "a date three days out",
			body:  map[string]any{"property_id": fix.propertyID, "amount": 1000, "incurred_on": overmorrow},
			want:  http.StatusBadRequest,
			field: "incurred_on",
		},
		{
			name:  "a date before the platform's world",
			body:  map[string]any{"property_id": fix.propertyID, "amount": 1000, "incurred_on": "1999-12-31"},
			want:  http.StatusBadRequest,
			field: "incurred_on",
		},
		{
			name:  "an unparseable date",
			body:  map[string]any{"property_id": fix.propertyID, "amount": 1000, "incurred_on": "31/12/2026"},
			want:  http.StatusBadRequest,
			field: "incurred_on",
		},
		{
			name:  "a missing date",
			body:  map[string]any{"property_id": fix.propertyID, "amount": 1000},
			want:  http.StatusBadRequest,
			field: "incurred_on",
		},
		{
			name:  "a vendor over 120 characters",
			body:  map[string]any{"property_id": fix.propertyID, "amount": 1000, "incurred_on": today(), "vendor": strings.Repeat("x", 121)},
			want:  http.StatusBadRequest,
			field: "vendor",
		},
		{
			name:  "a note over 1000 characters",
			body:  map[string]any{"property_id": fix.propertyID, "amount": 1000, "incurred_on": today(), "note": strings.Repeat("x", 1001)},
			want:  http.StatusBadRequest,
			field: "note",
		},
		{
			name:  "a property id that is not a UUID",
			body:  map[string]any{"property_id": "not-a-uuid", "amount": 1000, "incurred_on": today()},
			want:  http.StatusBadRequest,
			field: "property_id",
		},
		{
			name: "a unit of another property",
			body: map[string]any{
				"property_id": fix.propertyID, "unit_id": fix.otherUnitID,
				"amount": 1000, "incurred_on": today(),
			},
			want:  http.StatusUnprocessableEntity,
			field: "unit_id",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := fix.client.do(http.MethodPost, "/expenses", tc.body)
			resp.mustStatus(t, tc.want, tc.name)
			if _, ok := errorsOf(t, resp)[tc.field]; !ok {
				t.Errorf("the problem document does not name %q: %s", tc.field, resp.Raw)
			}
		})
	}

	// Tomorrow is allowed: a bill paid across midnight is real (FLOWS 12).
	fix.record(t, map[string]any{
		"property_id": fix.propertyID, "amount": 1000, "incurred_on": tomorrow,
	})
}

func TestExpenseRecordAndRead(t *testing.T) {
	h := newHarness(t)
	fix := h.newExpenseFixture(t, "Record", "0716010400")
	repairs := fix.categoryID(t, "Repairs & maintenance")

	id := fix.record(t, map[string]any{
		"property_id": fix.propertyID, "unit_id": fix.unitIDs[0], "category_id": repairs,
		"amount": 75_000, "incurred_on": today(),
		"vendor": "Juma Plumbing", "reference": "INV-19", "note": "Burst pipe",
	})

	got := fix.client.do(http.MethodGet, "/expenses/"+id, nil).
		mustStatus(t, http.StatusOK, "read expense")
	e := got.Body["expense"].(map[string]any)

	if e["amount"].(float64) != 75_000 {
		t.Errorf("amount = %v, want 75000", e["amount"])
	}
	if e["status"] != "recorded" {
		t.Errorf("status = %v, want recorded", e["status"])
	}
	if e["property"].(map[string]any)["id"] != fix.propertyID {
		t.Errorf("property block = %v", e["property"])
	}
	if e["unit"].(map[string]any)["id"] != fix.unitIDs[0] {
		t.Errorf("unit block = %v", e["unit"])
	}
	if e["category"].(map[string]any)["name"] != "Repairs & maintenance" {
		t.Errorf("category block = %v", e["category"])
	}
	if e["recorded_by"].(map[string]any)["name"] == "" {
		t.Error("recorded_by carries no name")
	}
	if receipt := e["receipt"].(map[string]any); receipt["present"] != false {
		t.Errorf("a fresh expense claims a receipt: %v", receipt)
	}
	if e["voided_at"] != nil || e["void_reason"] != nil {
		t.Errorf("a fresh expense carries void fields: %v", e)
	}

	// The edit is audited before/after, and it takes.
	fix.client.do(http.MethodPatch, "/expenses/"+id, map[string]any{
		"amount": 80_000, "note": "Burst pipe and tap",
	}).mustStatus(t, http.StatusOK, "patch expense")
	reread := fix.client.do(http.MethodGet, "/expenses/"+id, nil).
		mustStatus(t, http.StatusOK, "read after patch")
	if v := reread.Body["expense"].(map[string]any)["amount"].(float64); v != 80_000 {
		t.Errorf("amount after patch = %v, want 80000", v)
	}

	// An explicit null clears an optional reference; an absent member does not.
	fix.client.do(http.MethodPatch, "/expenses/"+id, map[string]any{"unit_id": nil}).
		mustStatus(t, http.StatusOK, "clear the unit")
	cleared := fix.client.do(http.MethodGet, "/expenses/"+id, nil).
		mustStatus(t, http.StatusOK, "read after clearing the unit")
	if u := cleared.Body["expense"].(map[string]any)["unit"]; u != nil {
		t.Errorf("unit = %v after an explicit null, want null", u)
	}
	if c := cleared.Body["expense"].(map[string]any)["category"]; c == nil {
		t.Error("clearing the unit also cleared the category")
	}
}

// -------------------------------------------------------------- voiding --

func TestExpenseVoidSemantics(t *testing.T) {
	h := newHarness(t)
	fix := h.newExpenseFixture(t, "Void", "0716010500")

	id := fix.record(t, map[string]any{
		"property_id": fix.propertyID, "amount": 40_000, "incurred_on": today(), "vendor": "Umeme",
	})
	live := fix.record(t, map[string]any{
		"property_id": fix.propertyID, "amount": 10_000, "incurred_on": today(),
	})

	voided := fix.client.do(http.MethodPost, "/expenses/"+id+"/void",
		map[string]any{"reason": "Recorded twice"}).
		mustStatus(t, http.StatusOK, "void")
	e := voided.Body["expense"].(map[string]any)
	if e["status"] != "voided" || e["void_reason"] != "Recorded twice" || e["voided_at"] == nil {
		t.Fatalf("voided expense = %v", e)
	}

	// Twice voided, edited after voiding, or given a receipt after voiding:
	// all the same 409. A correction is a record, not a state to bounce out of.
	for _, probe := range []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{"a second void", http.MethodPost, "/expenses/" + id + "/void", map[string]any{"reason": "again"}},
		{"an edit", http.MethodPatch, "/expenses/" + id, map[string]any{"amount": 1}},
		{"a receipt", http.MethodPost, "/expenses/" + id + "/receipt",
			map[string]any{"content_type": "image/png", "size": 1024}},
	} {
		resp := fix.client.do(probe.method, probe.path, probe.body)
		resp.mustStatus(t, http.StatusConflict, probe.name+" on a voided expense")
		if resp.Body["type"] != "expense_voided" {
			t.Errorf("%s: type = %v, want expense_voided", probe.name, resp.Body["type"])
		}
	}

	// The ledger defaults to the live rows, and its totals follow the filter.
	def := fix.client.do(http.MethodGet, "/expenses", nil).
		mustStatus(t, http.StatusOK, "default listing")
	if rows := listOf(t, def); len(rows) != 1 || rows[0]["id"] != live {
		t.Errorf("the default listing showed %d rows, want only the live one", len(rows))
	}
	if got := num(t, def, "totals", "amount"); got != 10_000 {
		t.Errorf("default totals.amount = %v, want 10000 — a voided row is still counted", got)
	}

	onlyVoided := fix.client.do(http.MethodGet, "/expenses?status=voided", nil).
		mustStatus(t, http.StatusOK, "voided listing")
	if rows := listOf(t, onlyVoided); len(rows) != 1 || rows[0]["id"] != id {
		t.Errorf("status=voided returned %v", rows)
	}
	all := fix.client.do(http.MethodGet, "/expenses?status=all", nil).
		mustStatus(t, http.StatusOK, "all listing")
	if rows := listOf(t, all); len(rows) != 2 {
		t.Errorf("status=all returned %d rows, want 2", len(rows))
	}

	// And the summary — which is always the recorded rows — agrees.
	sum := fix.client.do(http.MethodGet, "/expenses/summary?anchor="+today(), nil).
		mustStatus(t, http.StatusOK, "summary")
	if got := num(t, sum, "total", "amount"); got != 10_000 {
		t.Errorf("summary total = %v, want 10000 — the voided row is in the total", got)
	}
}

// -------------------------------------------------------------- summary --

func TestExpenseSummaryMatchesTheLedger(t *testing.T) {
	h := newHarness(t)
	fix := h.newExpenseFixture(t, "Summary", "0716010600")
	repairs := fix.categoryID(t, "Repairs & maintenance")
	utilities := fix.categoryID(t, "Utilities")

	now := time.Now().UTC()
	anchor := now.Format("2006-01-02")
	// A day inside this month that is not in the future: the 1st, which every
	// month has and which is never ahead of today.
	thisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	lastMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).
		AddDate(0, -1, 0).Format("2006-01-02")

	fix.record(t, map[string]any{
		"property_id": fix.propertyID, "category_id": repairs,
		"amount": 100_000, "incurred_on": thisMonth,
	})
	fix.record(t, map[string]any{
		"property_id": fix.propertyID, "category_id": utilities,
		"amount": 50_000, "incurred_on": thisMonth,
	})
	fix.record(t, map[string]any{
		"property_id": fix.property2ID, "category_id": utilities,
		"amount": 25_000, "incurred_on": thisMonth,
	})
	// Filed under no category: the "Uncategorised" bucket.
	fix.record(t, map[string]any{
		"property_id": fix.property2ID, "amount": 5_000, "incurred_on": thisMonth,
	})
	// The previous window, for the comparison.
	fix.record(t, map[string]any{
		"property_id": fix.propertyID, "category_id": repairs,
		"amount": 90_000, "incurred_on": lastMonth,
	})

	const wantTotal = 180_000

	byProperty := fix.client.do(http.MethodGet,
		"/expenses/summary?cadence=month&group_by=property&anchor="+anchor, nil).
		mustStatus(t, http.StatusOK, "summary by property")
	if got := num(t, byProperty, "total", "amount"); got != wantTotal {
		t.Errorf("total by property = %v, want %d", got, wantTotal)
	}
	groups := arrayOf(t, byProperty, "groups")
	if len(groups) != 2 {
		t.Fatalf("expected a group per property, got %v", groups)
	}
	// Sorted by amount, biggest first.
	if mustFloat(t, groups[0], "amount") != 150_000 || mustFloat(t, groups[1], "amount") != 30_000 {
		t.Errorf("property groups are not sorted by amount: %v", groups)
	}
	if sumGroups(t, groups) != wantTotal {
		t.Errorf("the property groups (%v) do not sum to the total %d", groups, wantTotal)
	}

	byCategory := fix.client.do(http.MethodGet,
		"/expenses/summary?cadence=month&group_by=category&anchor="+anchor, nil).
		mustStatus(t, http.StatusOK, "summary by category")
	catGroups := arrayOf(t, byCategory, "groups")
	if sumGroups(t, catGroups) != wantTotal {
		t.Errorf("the category groups (%v) do not sum to the total %d", catGroups, wantTotal)
	}
	// Every active category is present, spend or no spend, so a chart keeps
	// its bars — plus the uncategorised bucket, which is not a category.
	var zeroes, uncategorised int
	for _, g := range catGroups {
		if mustFloat(t, g, "amount") == 0 {
			zeroes++
		}
		if g["id"] == nil {
			uncategorised++
			if g["name"] != "Uncategorised" || mustFloat(t, g, "amount") != 5_000 {
				t.Errorf("uncategorised bucket = %v", g)
			}
		}
	}
	if zeroes == 0 {
		t.Error("no zero-spend category came back; the chart would lose its bars")
	}
	if uncategorised != 1 {
		t.Errorf("expected exactly one uncategorised bucket, got %d", uncategorised)
	}

	// The previous window is the previous calendar month, and the comparison
	// is measured against it.
	if got := num(t, byProperty, "previous_total", "amount"); got != 90_000 {
		t.Errorf("previous_total = %v, want 90000", got)
	}
	if got := num(t, byProperty, "change_pct"); got != 100 {
		t.Errorf("change_pct = %v, want 100 (180k against 90k)", got)
	}
	window := byProperty.Body["window"].(map[string]any)
	if window["cadence"] != "month" || window["from"] != thisMonth {
		t.Errorf("window = %v, want the calendar month starting %s", window, thisMonth)
	}
	prev := byProperty.Body["previous"].(map[string]any)
	if prev["from"] != lastMonth || prev["to"] != thisMonth {
		t.Errorf("previous window = %v, want %s → %s", prev, lastMonth, thisMonth)
	}

	// The same window read through the ledger has to agree with the summary:
	// two answers to "what did this month cost" is one answer too many.
	ledger := fix.client.do(http.MethodGet,
		"/expenses?from="+thisMonth+"&to="+anchor, nil).
		mustStatus(t, http.StatusOK, "ledger for the window")
	if got := num(t, ledger, "totals", "amount"); got != wantTotal {
		t.Errorf("ledger totals.amount = %v, but the summary says %d", got, wantTotal)
	}
	if got := num(t, ledger, "totals", "count"); got != 4 {
		t.Errorf("ledger totals.count = %v, want 4", got)
	}

	// A property filter narrows both the groups and the totals.
	scoped := fix.client.do(http.MethodGet,
		"/expenses/summary?cadence=month&group_by=category&anchor="+anchor+"&property_id="+fix.property2ID, nil).
		mustStatus(t, http.StatusOK, "summary for one property")
	if got := num(t, scoped, "total", "amount"); got != 30_000 {
		t.Errorf("scoped total = %v, want 30000", got)
	}

	// A first month has no previous window to compare against, and "+100%"
	// there would be an invention.
	fresh := h.newExpenseFixture(t, "Fresh", "0716010700")
	empty := fresh.client.do(http.MethodGet, "/expenses/summary?anchor="+anchor, nil).
		mustStatus(t, http.StatusOK, "summary with nothing in it")
	if empty.Body["change_pct"] != nil {
		t.Errorf("change_pct = %v against an empty previous window, want null", empty.Body["change_pct"])
	}
}

func sumGroups(t *testing.T, groups []map[string]any) float64 {
	t.Helper()
	var total float64
	for _, g := range groups {
		total += mustFloat(t, g, "amount")
	}
	return total
}

// ------------------------------------------------------------ filtering --

func TestExpenseFiltersAndPaging(t *testing.T) {
	h := newHarness(t)
	fix := h.newExpenseFixture(t, "Filter", "0716010800")
	utilities := fix.categoryID(t, "Utilities")

	for i := 0; i < 5; i++ {
		fix.record(t, map[string]any{
			"property_id": fix.propertyID, "unit_id": fix.unitIDs[0], "category_id": utilities,
			"amount": 1000 + i, "incurred_on": today(), "vendor": "TANESCO",
		})
	}
	fix.record(t, map[string]any{
		"property_id": fix.property2ID, "amount": 7000, "incurred_on": today(),
		"vendor": "100% Hardware", "note": "Nails",
	})

	// The page carries a cursor; the totals do not follow the page.
	first := fix.client.do(http.MethodGet, "/expenses?limit=2", nil).
		mustStatus(t, http.StatusOK, "first page")
	if rows := listOf(t, first); len(rows) != 2 {
		t.Fatalf("first page returned %d rows, want 2", len(rows))
	}
	if got := num(t, first, "totals", "count"); got != 6 {
		t.Errorf("totals.count = %v on a page of 2, want 6 — totals must cover the filter", got)
	}
	cursor, _ := first.Body["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("a full page carried no next_cursor")
	}
	seen := map[string]bool{}
	for _, row := range listOf(t, first) {
		seen[row["id"].(string)] = true
	}
	for cursor != "" {
		page := fix.client.do(http.MethodGet, "/expenses?limit=2&cursor="+url.QueryEscape(cursor), nil).
			mustStatus(t, http.StatusOK, "next page")
		for _, row := range listOf(t, page) {
			id := row["id"].(string)
			if seen[id] {
				t.Fatalf("row %s came back on two pages", id)
			}
			seen[id] = true
		}
		cursor, _ = page.Body["next_cursor"].(string)
	}
	if len(seen) != 6 {
		t.Errorf("paging saw %d of 6 rows", len(seen))
	}

	// Filters.
	byProperty := fix.client.do(http.MethodGet, "/expenses?property_id="+fix.property2ID, nil).
		mustStatus(t, http.StatusOK, "by property")
	if got := num(t, byProperty, "totals", "count"); got != 1 {
		t.Errorf("property filter matched %v rows, want 1", got)
	}
	byCategory := fix.client.do(http.MethodGet, "/expenses?category_id="+utilities, nil).
		mustStatus(t, http.StatusOK, "by category")
	if got := num(t, byCategory, "totals", "count"); got != 5 {
		t.Errorf("category filter matched %v rows, want 5", got)
	}
	byUnit := fix.client.do(http.MethodGet, "/expenses?unit_id="+fix.unitIDs[0], nil).
		mustStatus(t, http.StatusOK, "by unit")
	if got := num(t, byUnit, "totals", "count"); got != 5 {
		t.Errorf("unit filter matched %v rows, want 5", got)
	}

	// `q` searches vendor, reference and note — with its wildcards escaped, so
	// a bare `%` finds the vendor actually named with one (the Phase 8 rule).
	term := fix.client.do(http.MethodGet, "/expenses?q="+url.QueryEscape("%"), nil).
		mustStatus(t, http.StatusOK, "wildcard search")
	rows := listOf(t, term)
	if len(rows) != 1 || rows[0]["vendor"] != "100% Hardware" {
		t.Errorf("a bare %% matched %d rows, want only the vendor named with one: %v", len(rows), rows)
	}
	note := fix.client.do(http.MethodGet, "/expenses?q=nail", nil).
		mustStatus(t, http.StatusOK, "note search")
	if got := num(t, note, "totals", "count"); got != 1 {
		t.Errorf("a note search matched %v rows, want 1", got)
	}

	// A date window that excludes everything.
	past := fix.client.do(http.MethodGet, "/expenses?from=2020-01-01&to=2020-12-31", nil).
		mustStatus(t, http.StatusOK, "past window")
	if got := num(t, past, "totals", "count"); got != 0 {
		t.Errorf("a 2020 window matched %v rows", got)
	}

	// Bad parameters are refused by field.
	for _, probe := range []struct{ query, field string }{
		{"limit=0", "limit"},
		{"limit=500", "limit"},
		{"cursor=not-a-cursor", "cursor"},
		{"status=maybe", "status"},
		{"format=pdf", "format"},
		{"from=yesterday", "from"},
		{"cadence=fortnight", "cadence"},
		{"property_id=nope", "property_id"},
	} {
		resp := fix.client.do(http.MethodGet, "/expenses?"+probe.query, nil)
		resp.mustStatus(t, http.StatusBadRequest, probe.query)
		if _, ok := errorsOf(t, resp)[probe.field]; !ok {
			t.Errorf("%s did not name %q: %s", probe.query, probe.field, resp.Raw)
		}
	}
}

// ------------------------------------------------------------------ CSV --

func TestExpenseCSVExport(t *testing.T) {
	h := newHarness(t)
	fix := h.newExpenseFixture(t, "CSV", "0716010900")
	cleaning := fix.categoryID(t, "Cleaning")

	fix.record(t, map[string]any{
		"property_id": fix.propertyID, "category_id": cleaning,
		"amount": 30_000, "incurred_on": today(),
		// The cell a landlord's spreadsheet would otherwise execute on open.
		"vendor": "=HYPERLINK(\"http://evil.example\",\"click\")",
		"note":   "+1234",
	})

	resp := fix.client.do(http.MethodGet, "/expenses?format=csv&from="+today()+"&to="+today(), nil).
		mustStatus(t, http.StatusOK, "csv export")
	lines := strings.Split(strings.TrimSpace(resp.Raw), "\n")
	if len(lines) != 2 {
		t.Fatalf("csv has %d lines, want a header and one row: %q", len(lines), resp.Raw)
	}
	wantHeader := "date,property,unit,category,vendor,reference,amount,status,note,recorded_by"
	if strings.TrimSpace(lines[0]) != wantHeader {
		t.Errorf("csv header = %q, want %q", lines[0], wantHeader)
	}
	// Formula neutralisation: both dangerous cells arrive quoted with a
	// leading apostrophe, so the spreadsheet reads them as text.
	if !strings.Contains(lines[1], `"'=HYPERLINK`) {
		t.Errorf("the vendor formula was not neutralised: %q", lines[1])
	}
	if !strings.Contains(lines[1], `'+1234`) {
		t.Errorf("the note formula was not neutralised: %q", lines[1])
	}
	if !strings.Contains(lines[1], ",30000,recorded,") {
		t.Errorf("the row does not carry the amount and status: %q", lines[1])
	}
}

// -------------------------------------------------------------- receipts --

func TestExpenseReceiptRules(t *testing.T) {
	h := newHarness(t)
	fix := h.newExpenseFixture(t, "Receipt", "0716011000")
	id := fix.record(t, map[string]any{
		"property_id": fix.propertyID, "amount": 15_000, "incurred_on": today(),
	})

	// The content-type and size checks run before object storage is consulted,
	// so they hold on a machine with no MinIO — which is the point: a presigned
	// URL must never be minted for a type the completion callback will refuse.
	for _, probe := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"a GIF", map[string]any{"content_type": "image/gif", "size": 1024}, "content_type"},
		{"no type", map[string]any{"size": 1024}, "content_type"},
		{"six MiB", map[string]any{"content_type": "application/pdf", "size": 6 << 20}, "size"},
		{"nothing at all", map[string]any{"content_type": "image/png", "size": 0}, "size"},
	} {
		resp := fix.client.do(http.MethodPost, "/expenses/"+id+"/receipt", probe.body)
		resp.mustStatus(t, http.StatusBadRequest, probe.name)
		if _, ok := errorsOf(t, resp)[probe.field]; !ok {
			t.Errorf("%s did not name %q: %s", probe.name, probe.field, resp.Raw)
		}
	}

	// An expense with no receipt has nothing to show or remove.
	fix.client.do(http.MethodGet, "/expenses/"+id+"/receipt", nil).
		mustStatus(t, http.StatusNotFound, "view a receipt that does not exist")
	fix.client.do(http.MethodDelete, "/expenses/"+id+"/receipt", nil).
		mustStatus(t, http.StatusNotFound, "remove a receipt that does not exist")

	if h.store == nil {
		t.Skip("MinIO is not reachable; the presigned round trip is skipped")
	}

	issued := fix.client.do(http.MethodPost, "/expenses/"+id+"/receipt",
		map[string]any{"content_type": "image/png", "size": 2048}).
		mustStatus(t, http.StatusOK, "presign a receipt upload")
	key := issued.str(t, "object_key")
	if want := fix.orgID + "/" + id + ".png"; key != want {
		t.Errorf("object_key = %q, want %q", key, want)
	}
	if issued.str(t, "upload_url") == "" {
		t.Error("no upload_url was issued")
	}

	// Completing an upload that never happened is a 400, not a 500.
	fix.client.do(http.MethodPost, "/expenses/"+id+"/receipt/complete", map[string]any{}).
		mustStatus(t, http.StatusBadRequest, "complete without uploading")

	// A key from another org's prefix is refused on its shape alone.
	foreign := fix.client.do(http.MethodPost, "/expenses/"+id+"/receipt/complete",
		map[string]any{"object_key": "00000000-0000-4000-8000-000000000000/" + id + ".png"})
	foreign.mustStatus(t, http.StatusBadRequest, "complete with a foreign key")
	if _, ok := errorsOf(t, foreign)["object_key"]; !ok {
		t.Errorf("a foreign key did not name object_key: %s", foreign.Raw)
	}
}
