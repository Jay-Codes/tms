package httpserver_test

import (
	"net/http"
	"testing"
	"time"

	"tms/backend/internal/tz"
)

// A named window sent the way the landlord app sends it — `cadence` with
// `from`/`to` and no `anchor` — must answer for the window holding `from`, not
// for the current one. Before the fix every report and the expense summary
// answered any earlier month with this month's figures.
func TestReportWindowFromWithoutAnchor(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.createOrg("Win30", "Win Owner", "win30@jjne.test", "0716300100", "supersecret")

	for _, c := range []struct {
		path, from, to string
	}{
		{"/reports/revenue?cadence=month&from=2026-01-01&to=2026-01-31", "2026-01-01", "2026-02-01"},
		{"/reports/summary?cadence=quarter&from=2025-04-01&to=2025-06-30", "2025-04-01", "2025-07-01"},
		{"/expenses/summary?from=2026-01-01&to=2026-01-31&group_by=category", "2026-01-01", "2026-02-01"},
		{"/expenses/summary?cadence=custom&from=2026-01-01&to=2026-01-31", "2026-01-01", "2026-02-01"},
		// An explicit anchor still wins.
		{"/reports/revenue?cadence=month&anchor=2026-03-15&from=2026-01-01", "2026-03-01", "2026-04-01"},
	} {
		r := owner.do(http.MethodGet, c.path, nil).mustStatus(t, http.StatusOK, c.path)
		if got := r.str(t, "window", "from"); got != c.from {
			t.Errorf("%s: window from %s, want %s", c.path, got, c.from)
		}
		if got := r.str(t, "window", "to"); got != c.to {
			t.Errorf("%s: window to %s, want %s", c.path, got, c.to)
		}
	}
}

// Expenses logged this month are history the projection must count: they are
// in the spend to date, and the forecast starts next month.
func TestProjectionCountsCurrentMonth(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.createOrg("Cur30", "Cur Owner", "cur30@jjne.test", "0716300200", "supersecret")
	prop := owner.do(http.MethodPost, "/properties", map[string]any{"name": "Kijitonyama Flats"}).
		mustStatus(t, http.StatusCreated, "property")
	propertyID := prop.str(t, "property", "id")
	today := time.Now().In(tz.Zone()).Format(testDateLayout)
	owner.do(http.MethodPost, "/expenses", map[string]any{
		"property_id": propertyID, "amount": 60_000_000, "incurred_on": today, "vendor": "Fundi",
	}).mustStatus(t, http.StatusCreated, "expense today")

	r := owner.projection(map[string]any{"property_id": propertyID, "horizon_months": 3}).
		mustStatus(t, http.StatusOK, "projection")
	if v := num(t, r, "investment", "spent_to_date"); v != 60_000_000 {
		t.Errorf("spent to date = %v, want this month's 60,000,000", v)
	}
	if s := r.str(t, "investment", "break_even_status"); s == "no_costs" {
		t.Errorf("break-even status = %q with 60,000,000 spent", s)
	}
	// Logged as a running cost with one month of history, the 60,000,000
	// would be projected every month; the landlord's own estimate replaces it.
	if v := num(t, r, "applied", "monthly_expenses"); v != 60_000_000 {
		t.Errorf("trailing monthly = %v, want 60,000,000 over one month", v)
	}
	est := owner.projection(map[string]any{"property_id": propertyID, "horizon_months": 3, "monthly_expenses": 150_000}).
		mustStatus(t, http.StatusOK, "projection with an estimate")
	if s := est.str(t, "applied", "monthly_expenses_source"); s != "scenario" {
		t.Errorf("monthly source = %q", s)
	}
	for _, m := range arrayOf(t, est, "months") {
		if m["running_expenses"] != float64(150_000) {
			t.Errorf("month %v running = %v, want the estimate", m["month"], m["running_expenses"])
		}
	}
	if v := num(t, est, "investment", "spent_to_date"); v != 60_000_000 {
		t.Errorf("spent to date with an estimate = %v", v)
	}
	// No income: −1,800,000 a year on 60,000,000 spent = −3%.
	if v := num(t, est, "investment", "roi_projected_pct"); v != -3 {
		t.Errorf("roi = %v, want -3 (annual net ÷ everything spent)", v)
	}
	if r := owner.projection(map[string]any{"monthly_expenses": -1}); r.Code != http.StatusBadRequest {
		t.Errorf("negative estimate: status %d, want 400", r.Code)
	}
	saved := owner.do(http.MethodPost, "/reports/projection/scenarios", map[string]any{"name": "Lean", "monthly_expenses": 150_000}).
		mustStatus(t, http.StatusCreated, "save with an estimate")
	if v := num(t, saved, "scenario", "monthly_expenses"); v != 150_000 {
		t.Errorf("saved estimate = %v", v)
	}

	next := time.Now().In(tz.Zone()).AddDate(0, 0, -time.Now().In(tz.Zone()).Day()+1).AddDate(0, 1, 0).Format("2006-01")
	if s := r.str(t, "start_month"); s != next {
		t.Errorf("start month = %s, want next month %s", s, next)
	}
}
