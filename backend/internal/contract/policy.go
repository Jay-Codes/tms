package contract

import (
	"encoding/json"
	"strconv"
)

// Policy is the part of a tenancy agreement the system itself acts on
// (Phase 22 §22.2): what happens to the last month on moving out, to prepaid
// rent on an early exit, what deposit is held, and how much notice each side
// gives. The client's rule (27 Sep 2026) is that the landlord configures these
// and the contract stipulates them, so they live on the template, are copied
// onto each contract when it is written, and are covered by its hash — a
// signed tenancy keeps the rules it was signed under.
//
// On a template, DepositAmount is the fixed amount (mode `fixed`) and
// DepositMonths the multiple (mode `months`). On a contract the deposit is
// always resolved to an amount, whatever the mode was.
type Policy struct {
	MoveOutProration           string `json:"move_out_proration"`
	EarlyExitPrepaid           string `json:"early_exit_prepaid"`
	DepositMode                string `json:"deposit_mode"`
	DepositAmount              int64  `json:"deposit_amount"`
	DepositMonths              int    `json:"deposit_months"`
	DeductionsMayExceedDeposit bool   `json:"deductions_may_exceed_deposit"`
	TenantNoticeDays           int    `json:"tenant_notice_days"`
	EvictionNoticeDays         int    `json:"eviction_notice_days"`
}

// Allowed values. Each set is small on purpose: a value the settlement code
// does not know how to act on is a value it must never meet.
const (
	ProrationFullMonth = "full_month"
	ProrationProRata   = "pro_rata"

	PrepaidRefund          = "refund"
	PrepaidForfeit         = "forfeit"
	PrepaidLandlordDecides = "landlord_decides"

	DepositNone   = "none"
	DepositFixed  = "fixed"
	DepositMonths = "months"

	maxNoticeDays    = 365
	maxDepositMonths = 24
	maxDepositAmount = 1_000_000_000_000
	depositMonthDays = 30
)

// Problems returns field → message for every rule the policy breaks, keyed by
// the JSON name under `policy.` so a handler can hand them straight back.
func (p Policy) Problems() map[string]string {
	out := map[string]string{}
	oneOf := func(field, v string, allowed ...string) {
		for _, a := range allowed {
			if v == a {
				return
			}
		}
		out["policy."+field] = "must be one of the allowed values"
	}
	oneOf("move_out_proration", p.MoveOutProration, ProrationFullMonth, ProrationProRata)
	oneOf("early_exit_prepaid", p.EarlyExitPrepaid, PrepaidRefund, PrepaidForfeit, PrepaidLandlordDecides)
	oneOf("deposit_mode", p.DepositMode, DepositNone, DepositFixed, DepositMonths)
	switch p.DepositMode {
	case DepositFixed:
		if p.DepositAmount < 1 || p.DepositAmount > maxDepositAmount {
			out["policy.deposit_amount"] = "a fixed deposit needs an amount above zero"
		}
	case DepositMonths:
		if p.DepositMonths < 1 || p.DepositMonths > maxDepositMonths {
			out["policy.deposit_months"] = "between 1 and 24 months"
		}
	}
	if p.TenantNoticeDays < 0 || p.TenantNoticeDays > maxNoticeDays {
		out["policy.tenant_notice_days"] = "between 0 and 365 days"
	}
	if p.EvictionNoticeDays < 0 || p.EvictionNoticeDays > maxNoticeDays {
		out["policy.eviction_notice_days"] = "between 0 and 365 days"
	}
	return out
}

// Normalized drops the fields the mode does not use, so two templates that
// mean the same thing store the same thing.
func (p Policy) Normalized() Policy {
	switch p.DepositMode {
	case DepositFixed:
		p.DepositMonths = 0
	case DepositMonths:
		p.DepositAmount = 0
	default:
		p.DepositAmount, p.DepositMonths = 0, 0
		p.DeductionsMayExceedDeposit = false
	}
	return p
}

// ForContract resolves a template policy onto one tenancy: a deposit given in
// months becomes an amount, from the unit's price scaled to 30 days with the
// same rounding the rent book uses.
func (p Policy) ForContract(unitPrice int64, unitPeriodDays int) Policy {
	p = p.Normalized()
	if p.DepositMode == DepositMonths {
		p.DepositAmount = RentPerPeriod(unitPrice, unitPeriodDays, depositMonthDays) * int64(p.DepositMonths)
	}
	return p
}

// Canonical is the byte-stable form the hash covers. encoding/json writes
// struct fields in declaration order, so the same policy always hashes the
// same, whatever order JSONB handed the keys back in.
func (p Policy) Canonical() string {
	b, _ := json.Marshal(p)
	return string(b)
}

// ParsePolicy reads a stored policy column. Empty or `null` is "no policy"
// (every contract written before Phase 22, and templates that set none).
func ParsePolicy(raw []byte) (*Policy, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var p Policy
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// PolicyVars are the `{{variables}}` a policy adds to the document, in the
// contract's language. Without a policy they are blank, which the reader sees.
func PolicyVars(p *Policy, lang, formattedDeposit string) map[string]string {
	if p == nil {
		return map[string]string{}
	}
	deposit := formattedDeposit
	if p.DepositMode == DepositNone {
		deposit = "none"
		if lang == LangSwahili {
			deposit = "hakuna"
		}
	}
	return map[string]string{
		"deposit":              deposit,
		"tenant_notice_days":   strconv.Itoa(p.TenantNoticeDays),
		"eviction_notice_days": strconv.Itoa(p.EvictionNoticeDays),
	}
}
