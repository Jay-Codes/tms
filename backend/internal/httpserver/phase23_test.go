package httpserver_test

import (
	"net/http"
	"testing"
)

// Phase 23 — the whole-list reports now page.
func TestPhase23ReportsPage(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Paging", "0724000100", "+255724000101")
	r := fix.owner.do(http.MethodGet, "/reports/payment-status?limit=1", nil).mustStatus(t, http.StatusOK, "payment status")
	if got := mustFloat(t, r.Body, "total"); got != 1 {
		t.Errorf("total = %v, want 1", got)
	}
	if r.Body["next_cursor"] != nil {
		t.Errorf("next_cursor = %v on the last page", r.Body["next_cursor"])
	}
	if _, ok := r.Body["counts"].(map[string]any); !ok {
		t.Errorf("counts missing: %s", r.Raw)
	}
	fix.owner.do(http.MethodGet, "/reports/payment-status?cursor=abc", nil).
		mustStatus(t, http.StatusBadRequest, "bad cursor")
	up := fix.owner.do(http.MethodGet, "/reports/upcoming?days=30&limit=1", nil).mustStatus(t, http.StatusOK, "upcoming")
	if items := listOf(t, up); len(items) > 1 {
		t.Errorf("upcoming page = %d rows, want at most 1", len(items))
	}
}
