package contract

import "time"

// Settlement is what ending a tenancy on a given day means in money (Phase 22
// §22.5), worked out from the contract's own policy (§22.2). It is pure: the
// handler previews it, and applies exactly what it previews.
//
// The effective date is the last day the renter lives there, as it has always
// been for termination (FLOWS 6.5): periods starting after it are not owed.
type Settlement struct {
	EffectiveDate string `json:"effective_date"`
	// Proration is the rule applied to the period the tenancy ends inside.
	Proration string `json:"proration"`
	// Straddle is that period, when the end falls inside one.
	Straddle *StraddleCharge `json:"straddle"`
	// Arrears is rent owed for periods lived through (after proration).
	Arrears int64 `json:"arrears"`
	// Prepaid is rent already paid for time after the end.
	Prepaid int64 `json:"prepaid"`
	// PrepaidAction is refund | forfeit, or "" when the landlord must choose.
	PrepaidAction string `json:"prepaid_action"`
	// Refund is what is paid back now (Prepaid when refunding, else 0).
	Refund int64 `json:"refund"`
	// DepositRequired / DepositHeld describe the deposit ledger; the deposit
	// is settled from its own ledger, and appears here for the net figure.
	DepositRequired int64 `json:"deposit_required"`
	DepositHeld     int64 `json:"deposit_held"`
	// Net is from the landlord's side: positive, the renter still owes it;
	// negative, the landlord owes the renter (refund still due + deposit held).
	Net int64 `json:"net"`
}

// StraddleCharge is the period the tenancy ends inside.
type StraddleCharge struct {
	ScheduleID string `json:"schedule_id"`
	Amount     int64  `json:"amount"`
	Charged    int64  `json:"charged"`
	DaysLived  int    `json:"days_lived"`
	PeriodDays int    `json:"period_days"`
}

// SettlementRow is one schedule row as the settlement reads it.
type SettlementRow struct {
	ID          string
	PeriodStart time.Time
	PeriodEnd   time.Time // inclusive
	Amount      int64
	Paid        int64
	Status      string
}

// ComputeSettlement works out ending on `effective` under `policy` (nil: the
// behaviour before policies — full month, prepaid money kept). `choice` is the
// landlord's refund|forfeit answer, used when the policy leaves it to them.
func ComputeSettlement(policy *Policy, effective time.Time, rows []SettlementRow, depositHeld int64, choice string) Settlement {
	eff := dateOnly(effective)
	out := Settlement{EffectiveDate: eff.Format("2006-01-02"), Proration: ProrationFullMonth, DepositHeld: depositHeld}
	action := PrepaidForfeit
	if policy != nil {
		out.Proration = policy.MoveOutProration
		out.DepositRequired = policy.DepositAmount
		action = policy.EarlyExitPrepaid
	}
	if action == PrepaidLandlordDecides {
		action = ""
		if choice == PrepaidRefund || choice == PrepaidForfeit {
			action = choice
		}
	}
	out.PrepaidAction = action

	for _, r := range rows {
		if r.Status == StatusWaivedRow || r.Status == StatusWrittenOffRow {
			continue
		}
		start, end := dateOnly(r.PeriodStart), dateOnly(r.PeriodEnd)
		switch {
		case start.After(eff):
			out.Prepaid += r.Paid
		case end.After(eff):
			charged := r.Amount
			lived := int(eff.Sub(start).Hours()/24) + 1
			days := int(end.Sub(start).Hours()/24) + 1
			if out.Proration == ProrationProRata && days > 0 {
				charged = (r.Amount*int64(lived) + int64(days)/2) / int64(days)
			}
			out.Straddle = &StraddleCharge{
				ScheduleID: r.ID, Amount: r.Amount, Charged: charged, DaysLived: lived, PeriodDays: days,
			}
			if r.Paid > charged {
				out.Prepaid += r.Paid - charged
			} else {
				out.Arrears += charged - r.Paid
			}
		default:
			if r.Paid < r.Amount {
				out.Arrears += r.Amount - r.Paid
			}
		}
	}
	if action == PrepaidRefund {
		out.Refund = out.Prepaid
	}
	out.Net = out.Arrears - out.Refund - depositHeld
	return out
}

// Row statuses the settlement skips (they owe and hold nothing to settle).
const (
	StatusWaivedRow     = "waived"
	StatusWrittenOffRow = "written_off"
)
