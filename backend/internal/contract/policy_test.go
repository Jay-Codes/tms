package contract_test

import (
	"testing"

	"tms/backend/internal/contract"
)

func TestPolicyDepositInMonthsBecomesAnAmount(t *testing.T) {
	p := contract.Policy{
		MoveOutProration: contract.ProrationProRata, EarlyExitPrepaid: contract.PrepaidRefund,
		DepositMode: contract.DepositMonths, DepositMonths: 2, TenantNoticeDays: 30, EvictionNoticeDays: 14,
	}
	if probs := p.Problems(); len(probs) != 0 {
		t.Fatalf("valid policy reported %v", probs)
	}
	// 300,000 per 30 days → 2 months is 600,000; per 60 days → 150,000 a month.
	if got := p.ForContract(300_000, 30).DepositAmount; got != 600_000 {
		t.Errorf("deposit = %d, want 600000", got)
	}
	if got := p.ForContract(300_000, 60).DepositAmount; got != 300_000 {
		t.Errorf("deposit on a 60-day price = %d, want 300000", got)
	}
}

func TestPolicyProblemsAndNormalisation(t *testing.T) {
	bad := contract.Policy{MoveOutProration: "weekly", EarlyExitPrepaid: contract.PrepaidForfeit,
		DepositMode: contract.DepositFixed, TenantNoticeDays: 400}
	probs := bad.Problems()
	for _, k := range []string{"policy.move_out_proration", "policy.deposit_amount", "policy.tenant_notice_days"} {
		if _, ok := probs[k]; !ok {
			t.Errorf("missing problem %s in %v", k, probs)
		}
	}
	n := contract.Policy{DepositMode: contract.DepositNone, DepositAmount: 5, DepositMonths: 3,
		DeductionsMayExceedDeposit: true}.Normalized()
	if n.DepositAmount != 0 || n.DepositMonths != 0 || n.DeductionsMayExceedDeposit {
		t.Errorf("none deposit kept stray fields: %+v", n)
	}
}

func TestHashIgnoresAnAbsentPolicyButCoversAPresentOne(t *testing.T) {
	base := contract.Snapshot{TermsHTML: "<p>x</p>", UnitID: "u", RenterUserID: "r", RentAmount: 1,
		RentPeriodDays: 30, PaymentPeriodDays: 30, TermDays: 365, StartDate: "2026-01-01", EndDate: "2027-01-01"}
	withPolicy := base
	withPolicy.Policy = contract.Policy{DepositMode: contract.DepositNone}.Canonical()
	if base.Hash() == withPolicy.Hash() {
		t.Error("a policy did not change the hash")
	}
	other := withPolicy
	other.Policy = contract.Policy{DepositMode: contract.DepositNone, TenantNoticeDays: 1}.Canonical()
	if other.Hash() == withPolicy.Hash() {
		t.Error("a different policy hashed the same")
	}
}
