package payment_test

import (
	"errors"
	"testing"

	"tms/backend/internal/payment"
)

// sched is a shorthand for one unpaid schedule of the standard 250,000 rent.
func sched(id string, amount, paid int64, status string) payment.Schedule {
	return payment.Schedule{ID: id, Amount: amount, PaidAmount: paid, Status: status, DueDate: "2026-10-01"}
}

// TestAllocate is the table from API.md Phase 5: what a payment does to the
// schedule it is aimed at, and where the excess goes.
func TestAllocate(t *testing.T) {
	const rent = 250_000

	tests := []struct {
		name          string
		amount        int64
		target        payment.Schedule
		following     []payment.Schedule
		allowRollover bool
		want          []payment.Alloc
	}{
		{
			name:   "exact payment settles the schedule",
			amount: rent,
			target: sched("s1", rent, 0, payment.StatusPending),
			want: []payment.Alloc{
				{ScheduleID: "s1", Amount: rent, NewPaid: rent, NewStatus: payment.StatusPaid},
			},
		},
		{
			name:   "underpayment leaves the schedule partial",
			amount: 100_000,
			target: sched("s1", rent, 0, payment.StatusPending),
			want: []payment.Alloc{
				{ScheduleID: "s1", Amount: 100_000, NewPaid: 100_000, NewStatus: payment.StatusPartial},
			},
		},
		{
			name:   "a second instalment tops a partial schedule up to paid",
			amount: 150_000,
			target: sched("s1", rent, 100_000, payment.StatusPartial),
			want: []payment.Alloc{
				{ScheduleID: "s1", Amount: 150_000, NewPaid: rent, NewStatus: payment.StatusPaid},
			},
		},
		{
			name:   "an overdue schedule paid in full becomes paid",
			amount: rent,
			target: sched("s1", rent, 0, payment.StatusOverdue),
			want: []payment.Alloc{
				{ScheduleID: "s1", Amount: rent, NewPaid: rent, NewStatus: payment.StatusPaid},
			},
		},
		{
			name:          "with rollover the excess spans two schedules",
			amount:        400_000,
			target:        sched("s1", rent, 0, payment.StatusPending),
			following:     []payment.Schedule{sched("s2", rent, 0, payment.StatusPending), sched("s3", rent, 0, payment.StatusPending)},
			allowRollover: true,
			want: []payment.Alloc{
				{ScheduleID: "s1", Amount: rent, NewPaid: rent, NewStatus: payment.StatusPaid},
				{ScheduleID: "s2", Amount: 150_000, NewPaid: 150_000, NewStatus: payment.StatusPartial},
			},
		},
		{
			name:          "rollover skips schedules that are already settled",
			amount:        400_000,
			target:        sched("s1", rent, 0, payment.StatusPending),
			following:     []payment.Schedule{sched("s2", rent, rent, payment.StatusPaid), sched("s3", rent, 0, payment.StatusPending)},
			allowRollover: true,
			want: []payment.Alloc{
				{ScheduleID: "s1", Amount: rent, NewPaid: rent, NewStatus: payment.StatusPaid},
				{ScheduleID: "s3", Amount: 150_000, NewPaid: 150_000, NewStatus: payment.StatusPartial},
			},
		},
		{
			name:          "rollover fills three schedules exactly",
			amount:        750_000,
			target:        sched("s1", rent, 0, payment.StatusPending),
			following:     []payment.Schedule{sched("s2", rent, 0, payment.StatusPending), sched("s3", rent, 0, payment.StatusOverdue)},
			allowRollover: true,
			want: []payment.Alloc{
				{ScheduleID: "s1", Amount: rent, NewPaid: rent, NewStatus: payment.StatusPaid},
				{ScheduleID: "s2", Amount: rent, NewPaid: rent, NewStatus: payment.StatusPaid},
				{ScheduleID: "s3", Amount: rent, NewPaid: rent, NewStatus: payment.StatusPaid},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := payment.Allocate(tc.amount, tc.target, tc.following, tc.allowRollover)
			if err != nil {
				t.Fatalf("Allocate: unexpected error %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("applied %d schedules, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("applied[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
			// Every shilling of the payment must land somewhere.
			var total int64
			for _, a := range got {
				total += a.Amount
			}
			if total != tc.amount {
				t.Errorf("allocated %d, want the whole payment %d", total, tc.amount)
			}
		})
	}
}

// TestAllocateRefusals pins the three 409s: an overpayment awaiting
// confirmation, one that no schedule can absorb, and a target already settled.
func TestAllocateRefusals(t *testing.T) {
	const rent = 250_000

	t.Run("overpayment without rollover asks for confirmation", func(t *testing.T) {
		next := sched("s2", rent, 0, payment.StatusPending)
		_, err := payment.Allocate(400_000, sched("s1", rent, 0, payment.StatusPending),
			[]payment.Schedule{next}, false)

		var overpay *payment.OverpayError
		if !errors.As(err, &overpay) {
			t.Fatalf("error = %v, want *OverpayError", err)
		}
		if overpay.Excess != 150_000 {
			t.Errorf("excess = %d, want 150000", overpay.Excess)
		}
		if overpay.Next.ID != "s2" {
			t.Errorf("next schedule = %q, want s2", overpay.Next.ID)
		}
	})

	t.Run("more than the contract owes is refused outright", func(t *testing.T) {
		_, err := payment.Allocate(600_000, sched("s1", rent, 0, payment.StatusPending),
			[]payment.Schedule{sched("s2", rent, 0, payment.StatusPending)}, true)
		if !errors.Is(err, payment.ErrExceedsContractBalance) {
			t.Fatalf("error = %v, want ErrExceedsContractBalance", err)
		}
	})

	t.Run("an overpayment on the last schedule is refused even unconfirmed", func(t *testing.T) {
		// There is no next schedule to prompt about, so this is not a confirm
		// case: the contract simply cannot take the money.
		_, err := payment.Allocate(300_000, sched("s1", rent, 0, payment.StatusPending), nil, false)
		if !errors.Is(err, payment.ErrExceedsContractBalance) {
			t.Fatalf("error = %v, want ErrExceedsContractBalance", err)
		}
	})

	t.Run("a target that is already paid is refused", func(t *testing.T) {
		_, err := payment.Allocate(rent, sched("s1", rent, rent, payment.StatusPaid),
			[]payment.Schedule{sched("s2", rent, 0, payment.StatusPending)}, true)
		if !errors.Is(err, payment.ErrSchedulePaid) {
			t.Fatalf("error = %v, want ErrSchedulePaid", err)
		}
	})

	t.Run("a waived target is refused too", func(t *testing.T) {
		_, err := payment.Allocate(rent, sched("s1", rent, 0, payment.StatusWaived), nil, true)
		if !errors.Is(err, payment.ErrSchedulePaid) {
			t.Fatalf("error = %v, want ErrSchedulePaid", err)
		}
	})

	t.Run("a non-positive amount is refused", func(t *testing.T) {
		_, err := payment.Allocate(0, sched("s1", rent, 0, payment.StatusPending), nil, true)
		if !errors.Is(err, payment.ErrInvalidAmount) {
			t.Fatalf("error = %v, want ErrInvalidAmount", err)
		}
	})
}

// TestEarliestUnpaid is the target `POST /payments` picks when the body names
// no schedule_id: the first row that still owes something.
func TestEarliestUnpaid(t *testing.T) {
	const rent = 250_000
	rows := []payment.Schedule{
		sched("s1", rent, rent, payment.StatusPaid),
		sched("s2", rent, 0, payment.StatusWaived),
		sched("s3", rent, 50_000, payment.StatusPartial),
		sched("s4", rent, 0, payment.StatusPending),
	}
	if got := payment.EarliestUnpaid(rows); got != 2 {
		t.Errorf("EarliestUnpaid = %d, want 2 (the partial row)", got)
	}
	if got := payment.EarliestUnpaid(rows[:2]); got != -1 {
		t.Errorf("EarliestUnpaid on a settled contract = %d, want -1", got)
	}
}
