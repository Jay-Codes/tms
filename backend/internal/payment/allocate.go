package payment

import (
	"errors"
	"fmt"
)

// Schedule statuses a payment can land on (SPEC §4).
const (
	StatusPending = "pending"
	StatusPartial = "partial"
	StatusPaid    = "paid"
	StatusOverdue = "overdue"
	StatusWaived  = "waived"
)

// Schedule is the part of a payment_schedules row allocation cares about. It
// is a plain value so the rules can be table-tested without a database.
type Schedule struct {
	ID         string
	Amount     int64
	PaidAmount int64
	Status     string
	DueDate    string // YYYY-MM-DD, carried only so a 409 can name the next row
}

// Outstanding is what is still owed on a schedule (never negative).
func (s Schedule) Outstanding() int64 {
	if s.Status == StatusWaived {
		return 0
	}
	if s.PaidAmount >= s.Amount {
		return 0
	}
	return s.Amount - s.PaidAmount
}

// Settled reports whether a schedule can absorb no more money.
func (s Schedule) Settled() bool { return s.Outstanding() == 0 }

// Alloc is one schedule's share of a payment, and the state that share leaves
// the schedule in. The handler writes exactly these rows and updates.
type Alloc struct {
	ScheduleID string
	Amount     int64
	NewPaid    int64
	NewStatus  string
}

// Allocation refusals. Each maps to one answer in API.md.
var (
	// ErrInvalidAmount guards the pure function against a caller that skipped
	// validation; the handler answers 400 before it ever gets here.
	ErrInvalidAmount = errors.New("payment: amount must be positive")
	// ErrSchedulePaid is a payment aimed at a schedule that owes nothing.
	ErrSchedulePaid = errors.New("payment: schedule already settled")
	// ErrExceedsContractBalance is money left over once every schedule of the
	// contract is paid.
	ErrExceedsContractBalance = errors.New("payment: amount exceeds the contract balance")
)

// OverpayError reports that the payment is bigger than the target schedule and
// the caller did not agree to roll the excess forward. It carries what the UI
// needs to raise the confirm prompt (FLOWS 7): how much is left over and which
// schedule it would go to.
type OverpayError struct {
	Excess int64
	Next   Schedule
}

func (e *OverpayError) Error() string {
	return fmt.Sprintf("payment: %d over the target schedule, confirmation required", e.Excess)
}

// Allocate spreads a payment across a contract's schedules.
//
// The rules are API.md's, in order:
//
//   - The target must owe something; a settled (or waived) target is
//     ErrSchedulePaid — the caller picked the wrong row.
//   - The target absorbs up to what it still owes. Paid in full → `paid`,
//     part-paid → `partial`.
//   - Anything left over needs a following unpaid schedule. With
//     allowRollover it walks forward through `following` in order, recording
//     each share; without it, it stops at *OverpayError so the UI can prompt.
//   - Money still left once every following schedule is paid is
//     ErrExceedsContractBalance: the contract cannot absorb it, and a payment
//     is never recorded for more than is owed.
//
// following must be the schedules after the target in due order; settled and
// waived rows in it are skipped rather than rejected, so the caller can pass
// the tail of the contract's list unfiltered.
func Allocate(amount int64, target Schedule, following []Schedule, allowRollover bool) ([]Alloc, error) {
	if amount <= 0 {
		return nil, ErrInvalidAmount
	}
	if target.Settled() {
		return nil, ErrSchedulePaid
	}

	applied := make([]Alloc, 0, 1+len(following))
	remaining := amount

	take := func(s Schedule) Alloc {
		share := s.Outstanding()
		if share > remaining {
			share = remaining
		}
		remaining -= share
		newPaid := s.PaidAmount + share
		status := StatusPartial
		if newPaid >= s.Amount {
			status = StatusPaid
		}
		return Alloc{ScheduleID: s.ID, Amount: share, NewPaid: newPaid, NewStatus: status}
	}

	applied = append(applied, take(target))
	if remaining == 0 {
		return applied, nil
	}

	// The excess needs somewhere to go. Find the next row that owes something;
	// with nothing left to pay, the contract cannot take the money at all.
	next := -1
	for i, s := range following {
		if !s.Settled() {
			next = i
			break
		}
	}
	if next < 0 {
		return nil, ErrExceedsContractBalance
	}
	if !allowRollover {
		return nil, &OverpayError{Excess: remaining, Next: following[next]}
	}

	for _, s := range following[next:] {
		if s.Settled() {
			continue
		}
		applied = append(applied, take(s))
		if remaining == 0 {
			return applied, nil
		}
	}
	return nil, ErrExceedsContractBalance
}

// EarliestUnpaid returns the index of the first schedule that still owes
// something — the target `POST /payments` picks when the body names none.
// It returns -1 when the contract is fully settled.
func EarliestUnpaid(schedules []Schedule) int {
	for i, s := range schedules {
		if !s.Settled() {
			return i
		}
	}
	return -1
}
