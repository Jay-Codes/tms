package httpserver_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestCalendarMonthContract: a calendar period bills on the 1st of every
// month — a partial first month prorated over its own days, then full rent
// each month whatever its length — and the document says so.
func TestCalendarMonthContract(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "CalMonth", "0714000290", "+255714000291")

	period := fix.owner.do(http.MethodPost, "/org/payment-periods",
		map[string]any{"label": "Calendar monthly", "months": 1}).
		mustStatus(t, http.StatusCreated, "create calendar period")
	periodID := period.str(t, "period", "id")
	if got := num(t, period, "period", "days"); got != 30 {
		t.Errorf("calendar period days = %v, want the nominal 30", got)
	}
	if got := num(t, period, "period", "months"); got != 1 {
		t.Errorf("months = %v, want 1", got)
	}

	// 15 Oct → 1 Jan: 17 days of October, then November and December.
	created := fix.owner.contractOn(t, map[string]any{
		"unit_id": fix.unitIDs[1], "renter_user_id": fix.renterID,
		"payment_period_id": periodID, "term_days": 78,
		"start_date": "2026-10-15",
	}).mustStatus(t, http.StatusCreated, "create calendar contract")
	id := created.str(t, "contract", "id")
	if got := num(t, created, "contract", "payment_period", "months"); got != 1 {
		t.Errorf("contract payment_period.months = %v, want 1", got)
	}

	doc := fix.owner.do(http.MethodGet, "/contracts/"+id+"/document", nil).
		mustStatus(t, http.StatusOK, "document")
	if terms := doc.str(t, "terms_html"); !strings.Contains(terms, "every calendar month") &&
		!strings.Contains(terms, "kila mwezi wa kalenda") {
		t.Errorf("terms do not describe the calendar period: %s", terms)
	}

	h.signAsRenter(t, fix.renter, id, fix.renterPhone)
	fix.owner.do(http.MethodPost, "/contracts/"+id+"/activate", nil).
		mustStatus(t, http.StatusOK, "activate")

	rows := listOf(t, fix.owner.do(http.MethodGet, "/contracts/"+id+"/schedules", nil).
		mustStatus(t, http.StatusOK, "schedules"))
	want := []struct {
		start, end, due string
		amount          float64
	}{
		{"2026-10-15", "2026-10-31", "2026-10-15", 137097}, // 250,000 × 17/31
		{"2026-11-01", "2026-11-30", "2026-11-01", 250000},
		{"2026-12-01", "2026-12-31", "2026-12-01", 250000},
	}
	if len(rows) != len(want) {
		t.Fatalf("schedules = %d rows, want %d: %v", len(rows), len(want), rows)
	}
	for i, w := range want {
		r := rows[i]
		if r["period_start"] != w.start || r["period_end"] != w.end || r["due_date"] != w.due || r["amount"] != w.amount {
			t.Errorf("row %d = %v, want %+v", i, r, w)
		}
	}

	verify := fix.owner.do(http.MethodGet, "/contracts/"+id+"/verify", nil).
		mustStatus(t, http.StatusOK, "verify")
	if v, _ := verify.Body["valid"].(bool); !v {
		t.Errorf("calendar contract does not verify: %s", verify.Raw)
	}
}

// TestCalendarPeriodRules: a calendar period may sit beside the 30-day one it
// shares a nominal length with, but not beside another of the same months,
// and PATCH switches the kind both ways.
func TestCalendarPeriodRules(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "CalRules", "0714000292", "+255714000293")

	id := fix.owner.do(http.MethodPost, "/org/payment-periods",
		map[string]any{"label": "Calendar", "months": 1}).
		mustStatus(t, http.StatusCreated, "calendar beside 30 days").str(t, "period", "id")
	fix.owner.do(http.MethodPost, "/org/payment-periods",
		map[string]any{"label": "Calendar again", "months": 1}).
		mustStatus(t, http.StatusConflict, "second calendar monthly")
	fix.owner.do(http.MethodPost, "/org/payment-periods",
		map[string]any{"label": "Thirteen", "months": 13}).
		mustStatus(t, http.StatusBadRequest, "13 months")

	q := fix.owner.do(http.MethodPatch, "/org/payment-periods/"+id,
		map[string]any{"months": 3}).mustStatus(t, http.StatusOK, "to quarterly")
	if num(t, q, "period", "days") != 90 || num(t, q, "period", "months") != 3 {
		t.Errorf("quarterly = %s", q.Raw)
	}
	d := fix.owner.do(http.MethodPatch, "/org/payment-periods/"+id,
		map[string]any{"months": 0, "days": 45}).mustStatus(t, http.StatusOK, "back to days")
	if num(t, d, "period", "days") != 45 || d.Body["period"].(map[string]any)["months"] != nil {
		t.Errorf("day period = %s", d.Raw)
	}
}
