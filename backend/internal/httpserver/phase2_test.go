package httpserver_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// ------------------------------------------------------------- fixtures --

// orgFixture is one org with a property and a set of units, the starting point
// of most Phase 2 tests.
type orgFixture struct {
	client     *client
	orgID      string
	propertyID string
	unitIDs    []string
	unitCodes  []string
}

// listOf returns the `items` array of a list response.
func listOf(t *testing.T, r response) []map[string]any {
	t.Helper()
	return arrayOf(t, r, "items")
}

// periodsOf returns the offered periods of the public unit endpoint.
func periodsOf(t *testing.T, r response) []map[string]any {
	t.Helper()
	return arrayOf(t, r, "periods")
}

// arrayOf reads a named array of objects out of a response body.
func arrayOf(t *testing.T, r response, key string) []map[string]any {
	t.Helper()
	raw, ok := r.Body[key].([]any)
	if !ok {
		t.Fatalf("response has no %s array — body: %s", key, r.Raw)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("%s contains a non-object — body: %s", key, r.Raw)
		}
		out = append(out, m)
	}
	return out
}

// num reads a JSON number at a path (all JSON numbers decode as float64).
func num(t *testing.T, r response, path ...string) float64 {
	t.Helper()
	cur := any(r.Body)
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("path %v: %q is not an object — body: %s", path, key, r.Raw)
		}
		cur = m[key]
	}
	f, ok := cur.(float64)
	if !ok {
		t.Fatalf("path %v is not a number — body: %s", path, r.Raw)
	}
	return f
}

func mustFloat(t *testing.T, m map[string]any, key string) float64 {
	t.Helper()
	f, ok := m[key].(float64)
	if !ok {
		t.Fatalf("field %q is not a number in %v", key, m)
	}
	return f
}

// newOrgWithUnits signs an org up, creates one property and bulk-creates the
// named units at the given monthly price.
func (h *harness) newOrgWithUnits(name, email, phone string, unitNames []string, amount int64) orgFixture {
	h.t.Helper()
	c, created := h.createOrg(name, "Owner "+name, email, phone, "supersecret")
	fix := orgFixture{client: c, orgID: created.str(h.t, "org", "id")}

	prop := c.do(http.MethodPost, "/properties", map[string]any{
		"name": name + " Block A", "location_text": "Dar es Salaam",
	}).mustStatus(h.t, http.StatusCreated, "create property")
	fix.propertyID = prop.str(h.t, "property", "id")

	if len(unitNames) == 0 {
		return fix
	}
	body := map[string]any{"names": unitNames}
	if amount > 0 {
		body["price"] = map[string]any{"amount": amount, "period_days": 30}
	}
	units := c.do(http.MethodPost, "/properties/"+fix.propertyID+"/units/bulk", body).
		mustStatus(h.t, http.StatusCreated, "bulk create units")
	for _, u := range listOf(h.t, units) {
		id, _ := u["id"].(string)
		code, _ := u["unit_code"].(string)
		fix.unitIDs = append(fix.unitIDs, id)
		fix.unitCodes = append(fix.unitCodes, code)
	}
	return fix
}

// occupy flips a unit to `occupied` directly in the database. The API refuses
// to set it (it is derived from contracts, which arrive in Phase 4), so tests
// that need an occupied unit set it the way a contract activation will.
func (h *harness) occupy(unitID string) {
	h.t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		"UPDATE units SET status = 'occupied' WHERE id = $1", unitID); err != nil {
		h.t.Fatalf("mark unit occupied: %v", err)
	}
}

// ------------------------------------------------------------- proration --

// TestProration pins the rule from SPEC §4: an offered period's amount is the
// unit's price scaled by days / period_days, rounded to whole TZS.
func TestProration(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Prorate Ltd", "prorate@jjne.test", "0712000200", []string{"Room 1"}, 250_000)

	// One custom period per case, on top of the seeded 30/90/180/365.
	cases := []struct {
		days int
		want int64
	}{
		{7, 58_333},      // 250000 × 7 / 30 = 58 333.33 → 58 333
		{21, 175_000},    // exact
		{45, 375_000},    // exact
		{90, 750_000},    // exact (seeded Quarterly)
		{365, 3_041_667}, // 250000 × 365 / 30 = 3 041 666.67 → 3 041 667
	}

	for _, tc := range cases {
		if tc.days != 90 && tc.days != 365 {
			fix.client.do(http.MethodPost, "/org/payment-periods", map[string]any{
				"label": fmt.Sprintf("%d days", tc.days), "days": tc.days,
			}).mustStatus(t, http.StatusCreated, "create period")
		}
	}

	pub := h.client().do(http.MethodGet, "/public/units/"+fix.unitCodes[0], nil).
		mustStatus(t, http.StatusOK, "public unit")

	byDays := map[int]int64{}
	for _, p := range periodsOf(t, pub) {
		byDays[int(mustFloat(t, p, "days"))] = int64(mustFloat(t, p, "amount"))
	}
	for _, tc := range cases {
		got, ok := byDays[tc.days]
		if !ok {
			t.Fatalf("no offered period of %d days", tc.days)
		}
		if got != tc.want {
			t.Errorf("%d-day period: amount = %d, want %d", tc.days, got, tc.want)
		}
	}
}

// -------------------------------------------------------- payment periods --

func TestPaymentPeriodConstraints(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.createOrg("Periods Ltd", "Owner", "periods@jjne.test", "0712000201", "supersecret")

	seeded := owner.do(http.MethodGet, "/org/payment-periods", nil).
		mustStatus(t, http.StatusOK, "list periods")
	if got := len(listOf(t, seeded)); got != 4 {
		t.Fatalf("seeded periods = %d, want 4 (30/90/180/365)", got)
	}

	// days must be > 0.
	owner.do(http.MethodPost, "/org/payment-periods", map[string]any{"label": "Zero", "days": 0}).
		mustStatus(t, http.StatusBadRequest, "zero days")
	owner.do(http.MethodPost, "/org/payment-periods", map[string]any{"label": "Negative", "days": -5}).
		mustStatus(t, http.StatusBadRequest, "negative days")

	// A custom period is not recommended.
	created := owner.do(http.MethodPost, "/org/payment-periods", map[string]any{"label": "3 weeks", "days": 21}).
		mustStatus(t, http.StatusCreated, "create custom period")
	if rec, _ := created.Body["period"].(map[string]any)["is_recommended"].(bool); rec {
		t.Error("a custom period must not be flagged recommended")
	}
	customID := created.str(t, "period", "id")

	// Duplicate day count and duplicate label both conflict.
	owner.do(http.MethodPost, "/org/payment-periods", map[string]any{"label": "Three weeks", "days": 21}).
		mustStatus(t, http.StatusConflict, "duplicate days")
	owner.do(http.MethodPost, "/org/payment-periods", map[string]any{"label": "3 WEEKS", "days": 22}).
		mustStatus(t, http.StatusConflict, "duplicate label")

	// Deactivate everything but one: the last active period cannot go.
	all := listOf(t, owner.do(http.MethodGet, "/org/payment-periods", nil))
	for i, p := range all {
		id, _ := p["id"].(string)
		if i == len(all)-1 {
			owner.do(http.MethodDelete, "/org/payment-periods/"+id, nil).
				mustStatus(t, http.StatusConflict, "delete last active period")
			continue
		}
		owner.do(http.MethodDelete, "/org/payment-periods/"+id, nil).
			mustStatus(t, http.StatusNoContent, "deactivate period")
	}

	// Deactivated periods are hidden by default and visible on request.
	active := listOf(t, owner.do(http.MethodGet, "/org/payment-periods", nil))
	if len(active) != 1 {
		t.Fatalf("active periods = %d, want 1", len(active))
	}
	withInactive := listOf(t, owner.do(http.MethodGet, "/org/payment-periods?include_inactive=true", nil))
	if len(withInactive) != 5 {
		t.Fatalf("all periods = %d, want 5", len(withInactive))
	}

	// restore-recommended brings the presets back and is idempotent.
	first := owner.do(http.MethodPost, "/org/payment-periods/restore-recommended", nil).
		mustStatus(t, http.StatusOK, "restore recommended")
	second := owner.do(http.MethodPost, "/org/payment-periods/restore-recommended", nil).
		mustStatus(t, http.StatusOK, "restore recommended again")
	firstItems, secondItems := listOf(t, first), listOf(t, second)
	if len(firstItems) != len(secondItems) {
		t.Fatalf("restore is not idempotent: %d then %d periods", len(firstItems), len(secondItems))
	}
	days := map[int]bool{}
	for _, p := range secondItems {
		days[int(mustFloat(t, p, "days"))] = true
	}
	for _, want := range []int{30, 90, 180, 365} {
		if !days[want] {
			t.Errorf("recommended period of %d days was not restored", want)
		}
	}
	// The one period that was never deactivated (the custom 21-day one) is
	// left exactly as it was — restore only touches the presets.
	if !days[21] {
		t.Error("restore-recommended dropped the landlord's custom period")
	}
	if len(secondItems) != 5 {
		t.Fatalf("active periods after restore = %d, want 5 (4 presets + 1 custom)", len(secondItems))
	}
	custom := owner.do(http.MethodPatch, "/org/payment-periods/"+customID,
		map[string]any{"label": "Three weeks"}).mustStatus(t, http.StatusOK, "rename custom period")
	if got := custom.str(t, "period", "label"); got != "Three weeks" {
		t.Errorf("renamed period label = %q", got)
	}
}

// recommendedOf returns the labels of the periods carrying the badge.
func recommendedOf(t *testing.T, items []map[string]any) []string {
	t.Helper()
	var out []string
	for _, p := range items {
		if rec, _ := p["is_recommended"].(bool); rec {
			label, _ := p["label"].(string)
			out = append(out, label)
		}
	}
	return out
}

// TestRecommendedPaymentPeriodIsExclusive covers PLAN2 #8: the "Recommended"
// badge is a landlord saying "choose this one", which is only information while
// exactly one period carries it. Before Part 2 all four bootstrap presets were
// flagged, so the badge appeared on every row and meant nothing.
func TestRecommendedPaymentPeriodIsExclusive(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.createOrg("Badge Ltd", "Owner", "badge@jjne.test", "0712000209", "supersecret")

	items := listOf(t, owner.do(http.MethodGet, "/org/payment-periods", nil).
		mustStatus(t, http.StatusOK, "list periods"))
	if got := recommendedOf(t, items); len(got) != 1 || got[0] != "Monthly" {
		t.Fatalf("bootstrap recommended periods = %v, want exactly [Monthly]", got)
	}

	byLabel := map[string]string{}
	for _, p := range items {
		label, _ := p["label"].(string)
		id, _ := p["id"].(string)
		byLabel[label] = id
	}

	// PATCH cannot move the badge: the field is not part of its body, and the
	// decoder refuses unknown keys rather than dropping them silently.
	owner.do(http.MethodPatch, "/org/payment-periods/"+byLabel["Quarterly"],
		map[string]any{"is_recommended": true}).
		mustStatus(t, http.StatusBadRequest, "PATCH is_recommended")

	// The dedicated endpoint moves it, and the response is the same `{period}`
	// shape PATCH returns.
	moved := owner.do(http.MethodPost, "/org/payment-periods/"+byLabel["Quarterly"]+"/recommend", nil).
		mustStatus(t, http.StatusOK, "recommend Quarterly")
	if rec, _ := moved.Body["period"].(map[string]any)["is_recommended"].(bool); !rec {
		t.Errorf("recommended period is not flagged: %s", moved.Raw)
	}
	if got := recommendedOf(t, listOf(t, owner.do(http.MethodGet, "/org/payment-periods", nil))); len(got) != 1 || got[0] != "Quarterly" {
		t.Fatalf("recommended periods = %v, want exactly [Quarterly]", got)
	}

	// Idempotent: recommending the period that already has the badge is a
	// no-op, not a unique-index violation.
	owner.do(http.MethodPost, "/org/payment-periods/"+byLabel["Quarterly"]+"/recommend", nil).
		mustStatus(t, http.StatusOK, "recommend Quarterly again")
	if got := recommendedOf(t, listOf(t, owner.do(http.MethodGet, "/org/payment-periods", nil))); len(got) != 1 {
		t.Fatalf("recommended periods after a repeat = %v, want one", got)
	}

	// And back.
	owner.do(http.MethodPost, "/org/payment-periods/"+byLabel["Monthly"]+"/recommend", nil).
		mustStatus(t, http.StatusOK, "recommend Monthly")
	if got := recommendedOf(t, listOf(t, owner.do(http.MethodGet, "/org/payment-periods", nil))); len(got) != 1 || got[0] != "Monthly" {
		t.Fatalf("recommended periods = %v, want exactly [Monthly]", got)
	}

	// A period nobody is offered cannot be the one the landlord recommends.
	owner.do(http.MethodDelete, "/org/payment-periods/"+byLabel["Yearly"], nil).
		mustStatus(t, http.StatusNoContent, "deactivate Yearly")
	owner.do(http.MethodPost, "/org/payment-periods/"+byLabel["Yearly"]+"/recommend", nil).
		mustStatus(t, http.StatusConflict, "recommend an inactive period")

	// Unknown and malformed ids are 404, like every other period route.
	owner.do(http.MethodPost, "/org/payment-periods/00000000-0000-0000-0000-000000000000/recommend", nil).
		mustStatus(t, http.StatusNotFound, "recommend an unknown period")
	owner.do(http.MethodPost, "/org/payment-periods/not-a-uuid/recommend", nil).
		mustStatus(t, http.StatusNotFound, "recommend a malformed id")

	// The move is audited with both sides of it, so the trail says which period
	// lost the badge as well as which gained it.
	log := owner.do(http.MethodGet, "/audit-log", nil).mustStatus(t, http.StatusOK, "audit log")
	entries, _ := log.Body["items"].([]any)
	found := false
	for _, raw := range entries {
		m, ok := raw.(map[string]any)
		if !ok || m["action"] != "payment_period.recommend" {
			continue
		}
		found = true
		before, _ := m["before"].(map[string]any)
		after, _ := m["after"].(map[string]any)
		if before == nil || after == nil {
			t.Fatalf("payment_period.recommend carries no before/after: %v", m)
		}
		if before["recommended_period_id"] == after["recommended_period_id"] {
			t.Errorf("audit before and after name the same period: %v", m)
		}
		break
	}
	if !found {
		t.Errorf("no payment_period.recommend audit row: %s", log.Raw)
	}

	// restore-recommended recreates missing presets but must never mint a
	// second badge.
	owner.do(http.MethodPost, "/org/payment-periods/restore-recommended", nil).
		mustStatus(t, http.StatusOK, "restore recommended")
	if got := recommendedOf(t, listOf(t, owner.do(http.MethodGet, "/org/payment-periods", nil))); len(got) != 1 {
		t.Fatalf("recommended periods after restore = %v, want exactly one", got)
	}
}

// ---------------------------------------------- properties, units, status --

func TestPropertyAndUnitCRUD(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.createOrg("CRUD Ltd", "Owner", "crud@jjne.test", "0712000202", "supersecret")

	prop := owner.do(http.MethodPost, "/properties", map[string]any{
		"name": "Mbezi Beach Block A", "location_text": "Mbezi", "lat": -6.7, "lng": 39.2,
	}).mustStatus(t, http.StatusCreated, "create property")
	propertyID := prop.str(t, "property", "id")

	owner.do(http.MethodPost, "/properties", map[string]any{"name": ""}).
		mustStatus(t, http.StatusBadRequest, "empty property name")

	patched := owner.do(http.MethodPatch, "/properties/"+propertyID, map[string]any{
		"name": "Mbezi Beach Block B", "notes": nil,
	}).mustStatus(t, http.StatusOK, "patch property")
	if got := patched.str(t, "property", "name"); got != "Mbezi Beach Block B" {
		t.Fatalf("property name = %q after patch", got)
	}

	units := owner.do(http.MethodPost, "/properties/"+propertyID+"/units/bulk", map[string]any{
		"names": []string{"Room 1", "Room 2", "Room 3"},
		"price": map[string]any{"amount": 250000, "period_days": 30},
	}).mustStatus(t, http.StatusCreated, "bulk units")
	rows := listOf(t, units)
	if len(rows) != 3 {
		t.Fatalf("bulk created %d units, want 3", len(rows))
	}
	codes := map[string]bool{}
	for _, u := range rows {
		code, _ := u["unit_code"].(string)
		if len(code) != 10 {
			t.Errorf("unit_code %q is %d characters, want 10", code, len(code))
		}
		if codes[code] {
			t.Errorf("unit_code %q was issued twice", code)
		}
		codes[code] = true
		if scan, _ := u["scan_url"].(string); scan != "http://localhost:8080/enduser/u/"+code {
			t.Errorf("scan_url = %q for code %q", scan, code)
		}
	}
	// unit_counts follow the units.
	got := owner.do(http.MethodGet, "/properties/"+propertyID, nil).
		mustStatus(t, http.StatusOK, "get property")
	if n := num(t, got, "property", "unit_counts", "total"); n != 3 {
		t.Errorf("unit_counts.total = %v, want 3", n)
	}
	if n := num(t, got, "property", "unit_counts", "vacant"); n != 3 {
		t.Errorf("unit_counts.vacant = %v, want 3", n)
	}
}

func TestUnitStatusOverrideRules(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Status Ltd", "status@jjne.test", "0712000203", []string{"Room 1"}, 200_000)
	unitID := fix.unitIDs[0]

	// `occupied` is derived from contracts and is rejected outright.
	rejected := fix.client.do(http.MethodPatch, "/units/"+unitID, map[string]any{"status": "occupied"}).
		mustStatus(t, http.StatusBadRequest, "set occupied")
	if errs, _ := rejected.Body["errors"].(map[string]any); errs["status"] == nil {
		t.Errorf("400 for status=occupied carries no field error — body: %s", rejected.Raw)
	}

	// A landlord-set status is flagged as an override.
	for _, status := range []string{"unlisted", "maintenance"} {
		resp := fix.client.do(http.MethodPatch, "/units/"+unitID, map[string]any{"status": status}).
			mustStatus(t, http.StatusOK, "set "+status)
		if got := resp.str(t, "unit", "status"); got != status {
			t.Fatalf("status = %q, want %q", got, status)
		}
		if override, _ := resp.Body["unit"].(map[string]any)["status_override"].(bool); !override {
			t.Errorf("status_override is false after setting %q", status)
		}
	}

	// Back to vacant clears the override.
	resp := fix.client.do(http.MethodPatch, "/units/"+unitID, map[string]any{"status": "vacant"}).
		mustStatus(t, http.StatusOK, "set vacant")
	if override, _ := resp.Body["unit"].(map[string]any)["status_override"].(bool); override {
		t.Error("status_override survived a return to vacant")
	}
	if resp.Body["unit"].(map[string]any)["vacant_since"] == nil {
		t.Error("a vacant unit has no vacant_since")
	}

	// Renaming leaves the status alone.
	renamed := fix.client.do(http.MethodPatch, "/units/"+unitID, map[string]any{"name": "Room One"}).
		mustStatus(t, http.StatusOK, "rename unit")
	if got := renamed.str(t, "unit", "name"); got != "Room One" {
		t.Fatalf("name = %q after rename", got)
	}
	if got := renamed.str(t, "unit", "status"); got != "vacant" {
		t.Fatalf("status = %q after a rename-only patch", got)
	}
}

func TestVacancyBoardFilters(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Board Ltd", "board@jjne.test", "0712000204",
		[]string{"Room 1", "Room 2", "Room 3"}, 100_000)

	fix.client.do(http.MethodPatch, "/units/"+fix.unitIDs[1], map[string]any{"status": "maintenance"}).
		mustStatus(t, http.StatusOK, "unit to maintenance")
	h.occupy(fix.unitIDs[2])

	vacant := listOf(t, fix.client.do(http.MethodGet, "/units?status=vacant", nil).
		mustStatus(t, http.StatusOK, "vacancy board"))
	if len(vacant) != 1 {
		t.Fatalf("vacant units = %d, want 1", len(vacant))
	}

	byProperty := listOf(t, fix.client.do(http.MethodGet, "/units?property_id="+fix.propertyID, nil).
		mustStatus(t, http.StatusOK, "by property"))
	if len(byProperty) != 3 {
		t.Fatalf("units in property = %d, want 3", len(byProperty))
	}

	// q matches the unit name…
	byName := listOf(t, fix.client.do(http.MethodGet, "/units?q=room+2", nil).
		mustStatus(t, http.StatusOK, "search by unit name"))
	if len(byName) != 1 {
		t.Fatalf("q=room 2 matched %d units, want 1", len(byName))
	}
	// …and the property name.
	byProp := listOf(t, fix.client.do(http.MethodGet, "/units?q=Board", nil).
		mustStatus(t, http.StatusOK, "search by property name"))
	if len(byProp) != 3 {
		t.Fatalf("q=Board matched %d units, want 3", len(byProp))
	}

	// Cursor pagination walks the whole board without repeats.
	seen := map[string]bool{}
	page := fix.client.do(http.MethodGet, "/units?limit=2", nil).mustStatus(t, http.StatusOK, "page 1")
	for _, u := range listOf(t, page) {
		seen[u["id"].(string)] = true
	}
	cursor, ok := page.Body["next_cursor"].(string)
	if !ok {
		t.Fatalf("first page of 2 has no next_cursor — body: %s", page.Raw)
	}
	page2 := fix.client.do(http.MethodGet, "/units?limit=2&cursor="+cursor, nil).
		mustStatus(t, http.StatusOK, "page 2")
	for _, u := range listOf(t, page2) {
		seen[u["id"].(string)] = true
	}
	if len(seen) != 3 {
		t.Fatalf("pagination yielded %d distinct units, want 3", len(seen))
	}
	if page2.Body["next_cursor"] != nil {
		t.Error("the last page still advertises a next_cursor")
	}
}

func TestDeleteBlockedByLiveContract(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Delete Ltd", "delete@jjne.test", "0712000205", []string{"Room 1"}, 100_000)

	// A renter and a pending_signature contract on the unit: neither the unit
	// nor its property may be deleted (API.md → 409). The contract is written
	// directly — contracts are Phase 4 — exactly as an activation will write it.
	var renterID string
	if err := h.pool.QueryRow(context.Background(),
		`INSERT INTO users (kind, phone, full_name) VALUES ('renter', '+255712000206', 'Renter One')
		 RETURNING id::text`).Scan(&renterID); err != nil {
		t.Fatalf("insert renter: %v", err)
	}
	if _, err := h.pool.Exec(context.Background(), `
		INSERT INTO contracts (org_id, unit_id, renter_user_id, rent_amount, rent_period_days,
		                       payment_period_days, term_days, start_date, end_date, status)
		VALUES ($1, $2, $3, 100000, 30, 30, 180, CURRENT_DATE, CURRENT_DATE + 180, 'pending_signature')`,
		fix.orgID, fix.unitIDs[0], renterID); err != nil {
		t.Fatalf("insert contract: %v", err)
	}

	fix.client.do(http.MethodDelete, "/units/"+fix.unitIDs[0], nil).
		mustStatus(t, http.StatusConflict, "delete unit with pending contract")
	fix.client.do(http.MethodDelete, "/properties/"+fix.propertyID, nil).
		mustStatus(t, http.StatusConflict, "delete property with pending contract")

	// Once the contract ends, both go.
	if _, err := h.pool.Exec(context.Background(),
		"UPDATE contracts SET status = 'ended' WHERE unit_id = $1", fix.unitIDs[0]); err != nil {
		t.Fatalf("end contract: %v", err)
	}
	fix.client.do(http.MethodDelete, "/units/"+fix.unitIDs[0], nil).
		mustStatus(t, http.StatusNoContent, "delete unit")
	fix.client.do(http.MethodGet, "/units/"+fix.unitIDs[0], nil).
		mustStatus(t, http.StatusNotFound, "deleted unit is gone")
	fix.client.do(http.MethodDelete, "/properties/"+fix.propertyID, nil).
		mustStatus(t, http.StatusNoContent, "delete property")
}

// ------------------------------------------------------------------ prices --

func TestPriceHistoryAndCurrentPrice(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Pricing Ltd", "pricing@jjne.test", "0712000207", []string{"Room 1"}, 250_000)
	unitID := fix.unitIDs[0]

	// A price effective in the past, and one dated in the future.
	fix.client.do(http.MethodPost, "/units/"+unitID+"/prices", map[string]any{
		"amount": 300000, "period_days": 30, "effective_from": "2026-01-01",
	}).mustStatus(t, http.StatusCreated, "backdated price")
	fix.client.do(http.MethodPost, "/units/"+unitID+"/prices", map[string]any{
		"amount": 400000, "period_days": 30, "effective_from": "2099-01-01",
	}).mustStatus(t, http.StatusCreated, "future price")
	fix.client.do(http.MethodPost, "/units/"+unitID+"/prices", map[string]any{"amount": 0, "period_days": 30}).
		mustStatus(t, http.StatusBadRequest, "zero amount")

	history := listOf(t, fix.client.do(http.MethodGet, "/units/"+unitID+"/prices", nil).
		mustStatus(t, http.StatusOK, "price history"))
	if len(history) != 3 {
		t.Fatalf("history rows = %d, want 3", len(history))
	}
	// Newest first, by effective_from.
	wantOrder := []string{"2099-01-01", "", "2026-01-01"}
	for i, want := range wantOrder {
		got, _ := history[i]["effective_from"].(string)
		if want != "" && got != want {
			t.Errorf("history[%d].effective_from = %q, want %q", i, got, want)
		}
	}
	if name, _ := history[0]["created_by_name"].(string); name == "" {
		t.Error("history row carries no created_by_name")
	}

	// current_price is the newest row whose effective_from has arrived — the
	// unit's original 250 000, not the future 400 000.
	unit := fix.client.do(http.MethodGet, "/units/"+unitID, nil).mustStatus(t, http.StatusOK, "get unit")
	if got := num(t, unit, "unit", "current_price", "amount"); got != 250_000 {
		t.Errorf("current_price.amount = %v, want 250000 (a future price must not apply)", got)
	}
}

func TestBulkPriceUpdate(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Bulk Ltd", "bulk@jjne.test", "0712000208",
		[]string{"Room 1", "Room 2"}, 250_000)

	// +7 % of 250 000 = 267 500 exactly; +7.5 % = 268 750.
	resp := fix.client.do(http.MethodPost, "/units/bulk-price", map[string]any{
		"unit_ids": fix.unitIDs, "mode": "percent", "value": 7,
	}).mustStatus(t, http.StatusOK, "bulk percent")
	for _, p := range listOf(t, resp) {
		if got := int64(mustFloat(t, p, "amount")); got != 267_500 {
			t.Errorf("percent bulk amount = %d, want 267500", got)
		}
	}

	// A percentage that lands between shillings rounds to a whole one.
	rounded := fix.client.do(http.MethodPost, "/units/bulk-price", map[string]any{
		"unit_ids": fix.unitIDs[:1], "mode": "percent", "value": 0.0001,
	}).mustStatus(t, http.StatusOK, "fractional percent")
	got := mustFloat(t, listOf(t, rounded)[0], "amount")
	if got != float64(int64(got)) {
		t.Errorf("percent bulk produced a fractional amount %v", got)
	}
	if int64(got) != 267_500 { // 267500 × 1.000001 = 267500.2675 → 267500
		t.Errorf("fractional percent amount = %v, want 267500", got)
	}

	// `set` writes the amount as given.
	set := fix.client.do(http.MethodPost, "/units/bulk-price", map[string]any{
		"unit_ids": fix.unitIDs, "mode": "set", "value": 180000, "period_days": 30,
	}).mustStatus(t, http.StatusOK, "bulk set")
	for _, p := range listOf(t, set) {
		if got := int64(mustFloat(t, p, "amount")); got != 180_000 {
			t.Errorf("set bulk amount = %d, want 180000", got)
		}
	}

	fix.client.do(http.MethodPost, "/units/bulk-price", map[string]any{
		"unit_ids": fix.unitIDs, "mode": "sideways", "value": 1,
	}).mustStatus(t, http.StatusBadRequest, "unknown mode")
}

// ---------------------------------------------------------------------- QR --

func TestUnitQRGeneratesPNG(t *testing.T) {
	h := newHarness(t)
	if h.store == nil {
		t.Skip("SKIP: MinIO unreachable — run `make up` to exercise the QR upload")
	}
	fix := h.newOrgWithUnits("QR Ltd", "qr@jjne.test", "0712000209", []string{"Room 1"}, 250_000)

	resp := fix.client.do(http.MethodPost, "/units/"+fix.unitIDs[0]+"/qr", nil).
		mustStatus(t, http.StatusOK, "generate qr")
	if got := resp.str(t, "unit_code"); got != fix.unitCodes[0] {
		t.Errorf("qr unit_code = %q, want %q", got, fix.unitCodes[0])
	}
	if got := resp.str(t, "scan_url"); got != "http://localhost:8080/enduser/u/"+fix.unitCodes[0] {
		t.Errorf("scan_url = %q", got)
	}
	if got := resp.str(t, "png_url"); got == "" {
		t.Error("qr response carries no png_url")
	}
	// The object really landed in the bucket under {org_id}/{unit_id}.png.
	key := fix.orgID + "/" + fix.unitIDs[0] + ".png"
	if !h.store.Exists(context.Background(), "qrcodes", key) {
		t.Errorf("no object at qrcodes/%s", key)
	}

	sheet := listOf(t, fix.client.do(http.MethodGet, "/properties/"+fix.propertyID+"/qr-sheet", nil).
		mustStatus(t, http.StatusOK, "qr sheet"))
	if len(sheet) != 1 {
		t.Fatalf("qr sheet has %d rows, want 1", len(sheet))
	}
	if url, _ := sheet[0]["png_url"].(string); url == "" {
		t.Error("qr sheet row carries no png_url")
	}
}

// -------------------------------------------------------- public endpoints --

func TestPublicUnitEndpoint(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Public Ltd", "public@jjne.test", "0712000210",
		[]string{"Room 1", "Room 2", "Room 3"}, 250_000)
	anon := h.client()

	// Unknown code.
	anon.do(http.MethodGet, "/public/units/ZZZZZZZZZZ", nil).
		mustStatus(t, http.StatusNotFound, "unknown unit code")

	// Unlisted unit: the sticker must read as an unknown code.
	fix.client.do(http.MethodPatch, "/units/"+fix.unitIDs[1], map[string]any{"status": "unlisted"}).
		mustStatus(t, http.StatusOK, "unlist unit")
	anon.do(http.MethodGet, "/public/units/"+fix.unitCodes[1], nil).
		mustStatus(t, http.StatusNotFound, "unlisted unit")

	// Occupied unit still resolves, flagged occupied (FLOWS flow 2).
	h.occupy(fix.unitIDs[2])
	occupied := anon.do(http.MethodGet, "/public/units/"+fix.unitCodes[2], nil).
		mustStatus(t, http.StatusOK, "occupied unit")
	if flag, _ := occupied.Body["occupied"].(bool); !flag {
		t.Error("an occupied unit is not flagged occupied")
	}

	// A vacant unit offers every active org period, with prorated amounts.
	open := anon.do(http.MethodGet, "/public/units/"+fix.unitCodes[0], nil).
		mustStatus(t, http.StatusOK, "vacant unit")
	if flag, _ := open.Body["occupied"].(bool); flag {
		t.Error("a vacant unit is flagged occupied")
	}
	if len(periodsOf(t, open)) != 4 {
		t.Fatalf("offered periods = %d, want the 4 seeded ones", len(periodsOf(t, open)))
	}
	if got := open.str(t, "branding", "display_name"); got != "Public Ltd" {
		t.Errorf("branding display_name = %q", got)
	}

	// allowed_period_ids narrows the offer to a subset.
	periods := listOf(t, fix.client.do(http.MethodGet, "/org/payment-periods", nil))
	monthly, _ := periods[0]["id"].(string)
	fix.client.do(http.MethodPatch, "/units/"+fix.unitIDs[0], map[string]any{
		"allowed_period_ids": []string{monthly},
	}).mustStatus(t, http.StatusOK, "restrict periods")

	narrowed := periodsOf(t, anon.do(http.MethodGet, "/public/units/"+fix.unitCodes[0], nil).
		mustStatus(t, http.StatusOK, "restricted unit"))
	if len(narrowed) != 1 {
		t.Fatalf("restricted unit offers %d periods, want 1", len(narrowed))
	}
	if id, _ := narrowed[0]["id"].(string); id != monthly {
		t.Errorf("restricted unit offers period %q, want %q", id, monthly)
	}

	// A period id from another org is rejected rather than silently stored.
	other := h.newOrgWithUnits("Other Ltd", "other-public@jjne.test", "0712000211", nil, 0)
	otherPeriods := listOf(t, other.client.do(http.MethodGet, "/org/payment-periods", nil))
	otherID, _ := otherPeriods[0]["id"].(string)
	fix.client.do(http.MethodPatch, "/units/"+fix.unitIDs[0], map[string]any{
		"allowed_period_ids": []string{otherID},
	}).mustStatus(t, http.StatusBadRequest, "foreign period id")
}

func TestPublicBrandingBySlug(t *testing.T) {
	h := newHarness(t)
	owner, created := h.createOrg("Branded Ltd", "Owner", "branded@jjne.test", "0712000212", "supersecret")
	_ = owner
	slug := created.str(t, "org", "slug")

	anon := h.client()
	resp := anon.do(http.MethodGet, "/public/orgs/"+slug+"/branding", nil).
		mustStatus(t, http.StatusOK, "public branding")
	if got := resp.str(t, "display_name"); got != "Branded Ltd" {
		t.Errorf("display_name = %q", got)
	}
	if got := resp.str(t, "theme", "font_id"); got == "" {
		t.Error("branding carries no theme font")
	}
	anon.do(http.MethodGet, "/public/orgs/no-such-org/branding", nil).
		mustStatus(t, http.StatusNotFound, "unknown slug")
}

func TestPublicEndpointsAreRateLimited(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Flood Ltd", "flood@jjne.test", "0712000213", []string{"Room 1"}, 100_000)
	anon := h.client()

	path := "/public/units/" + fix.unitCodes[0]
	for i := 0; i < 60; i++ {
		if resp := anon.do(http.MethodGet, path, nil); resp.Code != http.StatusOK {
			t.Fatalf("request %d/60 returned %d, want 200 — body: %s", i+1, resp.Code, resp.Raw)
		}
	}
	limited := anon.do(http.MethodGet, path, nil)
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("request 61 returned %d, want 429 — body: %s", limited.Code, limited.Raw)
	}
}

// ------------------------------------------------------------- isolation --

// TestPhase2CrossOrgIsolation walks every Phase 2 org-scoped route with a
// session from the wrong org: the answer is always 404, never 403 (SPEC §8).
func TestPhase2CrossOrgIsolation(t *testing.T) {
	h := newHarness(t)
	a := h.newOrgWithUnits("Alpha Estates", "alpha-p2@jjne.test", "0712000214", []string{"Room 1"}, 250_000)
	b := h.newOrgWithUnits("Beta Estates", "beta-p2@jjne.test", "0712000215", []string{"Room 1"}, 250_000)

	aPeriods := listOf(t, a.client.do(http.MethodGet, "/org/payment-periods", nil))
	aPeriodID, _ := aPeriods[0]["id"].(string)
	aPrices := listOf(t, a.client.do(http.MethodGet, "/units/"+a.unitIDs[0]+"/prices", nil))
	if len(aPrices) == 0 {
		t.Fatal("org A's unit has no price to probe")
	}

	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"get property", http.MethodGet, "/properties/" + a.propertyID, nil},
		{"patch property", http.MethodPatch, "/properties/" + a.propertyID, map[string]any{"name": "Stolen"}},
		{"delete property", http.MethodDelete, "/properties/" + a.propertyID, nil},
		{"list property units", http.MethodGet, "/properties/" + a.propertyID + "/units", nil},
		{"create unit", http.MethodPost, "/properties/" + a.propertyID + "/units", map[string]any{"name": "Squatter"}},
		{"bulk create units", http.MethodPost, "/properties/" + a.propertyID + "/units/bulk", map[string]any{"names": []string{"Squatter"}}},
		{"qr sheet", http.MethodGet, "/properties/" + a.propertyID + "/qr-sheet", nil},
		{"get unit", http.MethodGet, "/units/" + a.unitIDs[0], nil},
		{"patch unit", http.MethodPatch, "/units/" + a.unitIDs[0], map[string]any{"status": "maintenance"}},
		{"delete unit", http.MethodDelete, "/units/" + a.unitIDs[0], nil},
		{"generate qr", http.MethodPost, "/units/" + a.unitIDs[0] + "/qr", nil},
		{"list prices", http.MethodGet, "/units/" + a.unitIDs[0] + "/prices", nil},
		{"create price", http.MethodPost, "/units/" + a.unitIDs[0] + "/prices", map[string]any{"amount": 1, "period_days": 30}},
		{"bulk price", http.MethodPost, "/units/bulk-price", map[string]any{"unit_ids": []string{a.unitIDs[0]}, "mode": "set", "value": 1}},
		{"patch payment period", http.MethodPatch, "/org/payment-periods/" + aPeriodID, map[string]any{"label": "Stolen"}},
		{"delete payment period", http.MethodDelete, "/org/payment-periods/" + aPeriodID, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b.client.do(tc.method, tc.path, tc.body).
				mustStatus(t, http.StatusNotFound, "org B calling "+tc.path)
		})
	}

	// Listing endpoints show only the caller's own rows.
	props := listOf(t, b.client.do(http.MethodGet, "/properties", nil).mustStatus(t, http.StatusOK, "beta properties"))
	for _, p := range props {
		if id, _ := p["id"].(string); id == a.propertyID {
			t.Fatal("org B's property list contains org A's property")
		}
	}
	units := listOf(t, b.client.do(http.MethodGet, "/units", nil).mustStatus(t, http.StatusOK, "beta units"))
	for _, u := range units {
		if id, _ := u["id"].(string); id == a.unitIDs[0] {
			t.Fatal("org B's vacancy board contains org A's unit")
		}
	}
	beriods := listOf(t, b.client.do(http.MethodGet, "/org/payment-periods", nil).mustStatus(t, http.StatusOK, "beta periods"))
	for _, p := range beriods {
		if id, _ := p["id"].(string); id == aPeriodID {
			t.Fatal("org B's payment periods contain org A's period")
		}
	}

	// Org A is untouched by everything org B just tried.
	a.client.do(http.MethodGet, "/properties/"+a.propertyID, nil).
		mustStatus(t, http.StatusOK, "alpha property survived")
	a.client.do(http.MethodGet, "/units/"+a.unitIDs[0], nil).
		mustStatus(t, http.StatusOK, "alpha unit survived")

	// Malformed ids are 404 as well, so ids are not probeable by shape.
	for _, path := range []string{"/properties/not-a-uuid", "/units/not-a-uuid", "/units/not-a-uuid/prices"} {
		b.client.do(http.MethodGet, path, nil).mustStatus(t, http.StatusNotFound, "malformed "+path)
	}
}

// TestPhase2RoutesRequireAnOrgSession keeps the whole surface behind `tms_o`.
func TestPhase2RoutesRequireAnOrgSession(t *testing.T) {
	h := newHarness(t)
	anon := h.client()
	for _, path := range []string{"/properties", "/units", "/org/payment-periods"} {
		anon.do(http.MethodGet, path, nil).mustStatus(t, http.StatusUnauthorized, "anonymous "+path)
	}
}

// ------------------------------------------------- Phase 2 review regressions --

// TestBulkPriceIsAllOrNothing pins API.md's "one unknown/foreign unit_id → 404
// and no prices are written": the whole batch must be one transaction, not a
// loop that commits as it goes.
func TestBulkPriceIsAllOrNothing(t *testing.T) {
	h := newHarness(t)
	a := h.newOrgWithUnits("Atomic Ltd", "atomic@jjne.test", "0712000220",
		[]string{"Room 1", "Room 2"}, 250_000)
	b := h.newOrgWithUnits("Foreign Ltd", "foreign-bulk@jjne.test", "0712000221",
		[]string{"Room 1"}, 100_000)

	before := len(listOf(t, a.client.do(http.MethodGet, "/units/"+a.unitIDs[0]+"/prices", nil)))

	// The foreign id sits LAST, so a per-row implementation would already have
	// written the first unit's new price before noticing.
	a.client.do(http.MethodPost, "/units/bulk-price", map[string]any{
		"unit_ids": []string{a.unitIDs[0], a.unitIDs[1], b.unitIDs[0]},
		"mode":     "set", "value": 999_000,
	}).mustStatus(t, http.StatusNotFound, "bulk price with a foreign unit id")

	for i, id := range a.unitIDs {
		history := listOf(t, a.client.do(http.MethodGet, "/units/"+id+"/prices", nil))
		if len(history) != before {
			t.Errorf("unit %d has %d price rows after a rejected bulk update, want %d",
				i, len(history), before)
		}
		unit := a.client.do(http.MethodGet, "/units/"+id, nil).mustStatus(t, http.StatusOK, "get unit")
		if got := num(t, unit, "unit", "current_price", "amount"); got != 250_000 {
			t.Errorf("unit %d current_price = %v after a rejected bulk update, want 250000", i, got)
		}
	}

	// Same rule for the 409: a percentage cannot apply to a unit with no price,
	// and the units before it in the list must stay untouched.
	unpriced := a.client.do(http.MethodPost, "/properties/"+a.propertyID+"/units",
		map[string]any{"name": "Room 3 (no price)"}).
		mustStatus(t, http.StatusCreated, "create unpriced unit").str(t, "unit", "id")

	a.client.do(http.MethodPost, "/units/bulk-price", map[string]any{
		"unit_ids": []string{a.unitIDs[0], unpriced}, "mode": "percent", "value": 10,
	}).mustStatus(t, http.StatusConflict, "percent on a unit with no price")

	unit := a.client.do(http.MethodGet, "/units/"+a.unitIDs[0], nil).mustStatus(t, http.StatusOK, "get unit")
	if got := num(t, unit, "unit", "current_price", "amount"); got != 250_000 {
		t.Errorf("current_price = %v after a rejected percent batch, want 250000", got)
	}
}

// TestPublicUnitHiddenAfterSoftDelete: a deleted unit — and every unit of a
// deleted property — must stop resolving publicly, or its sticker outlives it.
func TestPublicUnitHiddenAfterSoftDelete(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Gone Ltd", "gone@jjne.test", "0712000222",
		[]string{"Room 1", "Room 2"}, 250_000)
	anon := h.client()

	anon.do(http.MethodGet, "/public/units/"+fix.unitCodes[0], nil).
		mustStatus(t, http.StatusOK, "unit resolves before deletion")

	fix.client.do(http.MethodDelete, "/units/"+fix.unitIDs[0], nil).
		mustStatus(t, http.StatusNoContent, "delete unit")
	anon.do(http.MethodGet, "/public/units/"+fix.unitCodes[0], nil).
		mustStatus(t, http.StatusNotFound, "deleted unit is gone publicly")
	fix.client.do(http.MethodGet, "/units/"+fix.unitIDs[0], nil).
		mustStatus(t, http.StatusNotFound, "deleted unit is gone for the landlord too")

	// Deleting the property takes its remaining units with it.
	fix.client.do(http.MethodDelete, "/properties/"+fix.propertyID, nil).
		mustStatus(t, http.StatusNoContent, "delete property")
	anon.do(http.MethodGet, "/public/units/"+fix.unitCodes[1], nil).
		mustStatus(t, http.StatusNotFound, "unit of a deleted property is gone publicly")
	units := listOf(t, fix.client.do(http.MethodGet, "/units", nil).
		mustStatus(t, http.StatusOK, "vacancy board"))
	if len(units) != 0 {
		t.Errorf("vacancy board still lists %d units of a deleted property", len(units))
	}
}

// TestPublicUnitIsCaseInsensitiveAndCarriesNoPII pins the two properties the
// sticker endpoint must have: a hand-typed code resolves whatever its case,
// and the payload never grows renter- or contract-shaped fields.
func TestPublicUnitIsCaseInsensitiveAndCarriesNoPII(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Sticker Ltd", "sticker@jjne.test", "0712000223",
		[]string{"Room 1"}, 250_000)
	anon := h.client()

	resp := anon.do(http.MethodGet, "/public/units/"+strings.ToLower(fix.unitCodes[0]), nil).
		mustStatus(t, http.StatusOK, "lower-case unit code")

	for _, key := range []string{
		"renter", "renter_name", "tenant", "contract", "contracts", "phone",
		"email", "owner", "user", "org_user", "notes",
	} {
		if _, present := resp.Body[key]; present {
			t.Errorf("public unit payload exposes %q", key)
		}
	}
	unit, _ := resp.Body["unit"].(map[string]any)
	for _, key := range []string{"property_id", "org_id", "status_override", "created_at"} {
		if _, present := unit[key]; present {
			t.Errorf("public unit block exposes internal field %q", key)
		}
	}
	prop, _ := resp.Body["property"].(map[string]any)
	if _, present := prop["id"]; present {
		t.Error("public property block exposes the internal property id")
	}
}

// TestPublicUnitHiddenForSuspendedOrg: suspending an org takes its stickers
// offline (API.md), rather than leaving them serving that org's data.
func TestPublicUnitHiddenForSuspendedOrg(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Suspended Ltd", "suspended@jjne.test", "0712000224",
		[]string{"Room 1"}, 250_000)
	anon := h.client()
	anon.do(http.MethodGet, "/public/units/"+fix.unitCodes[0], nil).
		mustStatus(t, http.StatusOK, "active org resolves")

	if _, err := h.pool.Exec(context.Background(),
		"UPDATE orgs SET status = 'suspended' WHERE id = $1", fix.orgID); err != nil {
		t.Fatalf("suspend org: %v", err)
	}
	anon.do(http.MethodGet, "/public/units/"+fix.unitCodes[0], nil).
		mustStatus(t, http.StatusNotFound, "suspended org's unit")
	anon.do(http.MethodGet, "/public/orgs/"+slugOf(t, fix.client)+"/branding", nil).
		mustStatus(t, http.StatusNotFound, "suspended org's branding")
}

// slugOf reads the caller's org slug from the session endpoint.
func slugOf(t *testing.T, c *client) string {
	t.Helper()
	return c.do(http.MethodGet, "/auth/me?audience=org", nil).str(t, "org", "slug")
}

// TestListCursorTampering: a hand-edited cursor is a client mistake (400),
// never a 500, and never a way to page across orgs.
func TestListCursorTampering(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Cursor Ltd", "cursor@jjne.test", "0712000225",
		[]string{"Room 1", "Room 2"}, 250_000)

	bad := []string{
		"not-base64!!",
		base64.RawURLEncoding.EncodeToString([]byte("no-comma-here")),
		base64.RawURLEncoding.EncodeToString([]byte("2026-13-45T99:99:99Z,not-a-uuid")),
		base64.RawURLEncoding.EncodeToString([]byte("2026-01-01T00:00:00Z,not-a-uuid")),
		base64.RawURLEncoding.EncodeToString([]byte("' OR 1=1 --,00000000-0000-0000-0000-000000000000")),
	}
	for _, base := range []string{"/properties", "/units"} {
		for _, cursor := range bad {
			fix.client.do(http.MethodGet, base+"?cursor="+url.QueryEscape(cursor), nil).
				mustStatus(t, http.StatusBadRequest, "tampered cursor on "+base)
		}
		fix.client.do(http.MethodGet, base+"?limit=0", nil).
			mustStatus(t, http.StatusBadRequest, "limit 0 on "+base)
		fix.client.do(http.MethodGet, base+"?limit=201", nil).
			mustStatus(t, http.StatusBadRequest, "limit 201 on "+base)
		fix.client.do(http.MethodGet, base+"?limit=abc", nil).
			mustStatus(t, http.StatusBadRequest, "non-numeric limit on "+base)
	}

	// A well-formed cursor still pages normally.
	page := fix.client.do(http.MethodGet, "/units?limit=1", nil).
		mustStatus(t, http.StatusOK, "first page")
	next, _ := page.Body["next_cursor"].(string)
	if next == "" {
		t.Fatal("a full first page returned no next_cursor")
	}
	fix.client.do(http.MethodGet, "/units?limit=1&cursor="+url.QueryEscape(next), nil).
		mustStatus(t, http.StatusOK, "second page")
}

// TestMoneyAndPeriodBounds keeps absurd inputs out of the price tables: the
// public endpoint prorates amount × days in int64.
func TestMoneyAndPeriodBounds(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Bounds Ltd", "bounds@jjne.test", "0712000226",
		[]string{"Room 1"}, 250_000)
	unitID := fix.unitIDs[0]

	cases := []struct {
		name string
		body map[string]any
	}{
		{"zero amount", map[string]any{"amount": 0, "period_days": 30}},
		{"negative amount", map[string]any{"amount": -1, "period_days": 30}},
		{"amount at one trillion", map[string]any{"amount": 1_000_000_000_000, "period_days": 30}},
		{"zero period", map[string]any{"amount": 1000, "period_days": 0}},
		{"period beyond ten years", map[string]any{"amount": 1000, "period_days": 4000}},
		{"unparsable date", map[string]any{"amount": 1000, "period_days": 30, "effective_from": "01/02/2026"}},
		{"unknown field", map[string]any{"amount": 1000, "period_days": 30, "sneaky": 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fix.client.do(http.MethodPost, "/units/"+unitID+"/prices", tc.body).
				mustStatus(t, http.StatusBadRequest, tc.name)
		})
	}

	// The same bounds apply to the bulk endpoint's inputs.
	fix.client.do(http.MethodPost, "/units/bulk-price", map[string]any{
		"unit_ids": []string{unitID}, "mode": "set", "value": 5_000_000_000_000,
	}).mustStatus(t, http.StatusBadRequest, "bulk set beyond the amount ceiling")
	fix.client.do(http.MethodPost, "/units/bulk-price", map[string]any{
		"unit_ids": []string{unitID}, "mode": "percent", "value": 100000,
	}).mustStatus(t, http.StatusBadRequest, "bulk percent beyond the ceiling")
	fix.client.do(http.MethodPost, "/units/bulk-price", map[string]any{
		"unit_ids": []string{}, "mode": "set", "value": 1000,
	}).mustStatus(t, http.StatusBadRequest, "empty unit_ids")

	// And to the unit's inline first price and to payment periods.
	fix.client.do(http.MethodPost, "/properties/"+fix.propertyID+"/units", map[string]any{
		"name": "Room X", "price": map[string]any{"amount": 0, "period_days": 30},
	}).mustStatus(t, http.StatusBadRequest, "inline price of zero")
	fix.client.do(http.MethodPost, "/org/payment-periods", map[string]any{
		"label": "Forever", "days": 100000,
	}).mustStatus(t, http.StatusBadRequest, "payment period beyond ten years")

	// Names are bounded too (API.md: unit 1–60, bulk list 1–200).
	fix.client.do(http.MethodPost, "/properties/"+fix.propertyID+"/units", map[string]any{
		"name": strings.Repeat("x", 61),
	}).mustStatus(t, http.StatusBadRequest, "unit name over 60 characters")
	tooMany := make([]string, 201)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("Room %d", i)
	}
	fix.client.do(http.MethodPost, "/properties/"+fix.propertyID+"/units/bulk",
		map[string]any{"names": tooMany}).
		mustStatus(t, http.StatusBadRequest, "201 unit names")
}
