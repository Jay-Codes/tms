package contract_test

import (
	"testing"
	"time"

	"tms/backend/internal/contract"
)

func d(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }

// Three 30-day periods of 300,000: Sep 1–30, Oct 1–30, Oct 31–Nov 29.
func rows(paid ...int64) []contract.SettlementRow {
	starts := []string{"2026-09-01", "2026-10-01", "2026-10-31"}
	out := []contract.SettlementRow{}
	for i, s := range starts {
		st := d(s)
		out = append(out, contract.SettlementRow{
			ID: s, PeriodStart: st, PeriodEnd: st.AddDate(0, 0, 29), Amount: 300_000, Paid: paid[i], Status: "pending",
		})
	}
	return out
}

func TestSettlementProRataWithPrepaidRefund(t *testing.T) {
	p := &contract.Policy{MoveOutProration: contract.ProrationProRata, EarlyExitPrepaid: contract.PrepaidRefund,
		DepositMode: contract.DepositFixed, DepositAmount: 500_000}
	// Leaves on Oct 10: 10 of 30 days of October; all three periods paid.
	s := contract.ComputeSettlement(p, d("2026-10-10"), rows(300_000, 300_000, 300_000), 500_000, "")
	if s.Straddle == nil || s.Straddle.Charged != 100_000 {
		t.Fatalf("straddle = %+v, want 100000 charged", s.Straddle)
	}
	if s.Prepaid != 200_000+300_000 || s.Refund != s.Prepaid || s.Arrears != 0 {
		t.Errorf("prepaid %d refund %d arrears %d", s.Prepaid, s.Refund, s.Arrears)
	}
	if s.Net != -(500_000 + 500_000) {
		t.Errorf("net = %d, want landlord owes refund + deposit", s.Net)
	}
}

func TestSettlementFullMonthWithArrears(t *testing.T) {
	p := &contract.Policy{MoveOutProration: contract.ProrationFullMonth, EarlyExitPrepaid: contract.PrepaidForfeit,
		DepositMode: contract.DepositNone}
	s := contract.ComputeSettlement(p, d("2026-10-10"), rows(300_000, 100_000, 0), 0, "")
	if s.Arrears != 200_000 || s.Prepaid != 0 || s.Refund != 0 || s.Net != 200_000 {
		t.Errorf("settlement = %+v", s)
	}
}

func TestSettlementLandlordDecides(t *testing.T) {
	p := &contract.Policy{MoveOutProration: contract.ProrationFullMonth, EarlyExitPrepaid: contract.PrepaidLandlordDecides,
		DepositMode: contract.DepositNone}
	if s := contract.ComputeSettlement(p, d("2026-10-30"), rows(300_000, 300_000, 300_000), 0, ""); s.PrepaidAction != "" {
		t.Errorf("no choice yet, action = %q", s.PrepaidAction)
	}
	s := contract.ComputeSettlement(p, d("2026-10-30"), rows(300_000, 300_000, 300_000), 0, contract.PrepaidRefund)
	if s.Refund != 300_000 || s.Straddle != nil {
		t.Errorf("refund = %d straddle = %+v, want the whole third period back", s.Refund, s.Straddle)
	}
}

func TestSettlementWithoutPolicyKeepsTheOldBehaviour(t *testing.T) {
	s := contract.ComputeSettlement(nil, d("2026-10-10"), rows(300_000, 300_000, 300_000), 0, "")
	if s.Proration != contract.ProrationFullMonth || s.PrepaidAction != contract.PrepaidForfeit || s.Refund != 0 {
		t.Errorf("settlement = %+v", s)
	}
}
