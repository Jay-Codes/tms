package contract

import (
	"testing"
	"time"
)

func date(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func intPtr(n int) *int { return &n }

// TestGenerateSpans pins the worked examples from SPEC §4: one row per cadence
// interval across the span, last row truncated to the end of the term.
func TestGenerateSpans(t *testing.T) {
	cases := []struct {
		name      string
		termDays  int
		cadence   int
		wantDays  []int
		wantStart []string
	}{
		{
			name:     "180-day term at 45-day cadence is four full rows",
			termDays: 180, cadence: 45,
			wantDays:  []int{45, 45, 45, 45},
			wantStart: []string{"2026-01-01", "2026-02-15", "2026-04-01", "2026-05-16"},
		},
		{
			name:     "100-day term at 30-day cadence truncates the last row",
			termDays: 100, cadence: 30,
			wantDays:  []int{30, 30, 30, 10},
			wantStart: []string{"2026-01-01", "2026-01-31", "2026-03-02", "2026-04-01"},
		},
		{
			name:     "a 7-day cadence over 30 days leaves a 2-day tail",
			termDays: 30, cadence: 7,
			wantDays:  []int{7, 7, 7, 7, 2},
			wantStart: []string{"2026-01-01", "2026-01-08", "2026-01-15", "2026-01-22", "2026-01-29"},
		},
		{
			name:     "a term shorter than one cadence is a single truncated row",
			termDays: 10, cadence: 30,
			wantDays:  []int{10},
			wantStart: []string{"2026-01-01"},
		},
		{
			name:     "term equal to cadence is exactly one row",
			termDays: 30, cadence: 30,
			wantDays:  []int{30},
			wantStart: []string{"2026-01-01"},
		},
		{
			name:     "a 365-day term at 30-day cadence ends with a 5-day row",
			termDays: 365, cadence: 30,
			wantDays: []int{30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 5},
			wantStart: []string{
				"2026-01-01", "2026-01-31", "2026-03-02", "2026-04-01", "2026-05-01",
				"2026-05-31", "2026-06-30", "2026-07-30", "2026-08-29", "2026-09-28",
				"2026-10-28", "2026-11-27", "2026-12-27",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := Generate(300_000, 30, tc.termDays, tc.cadence, date("2026-01-01"), nil)
			if len(rows) != len(tc.wantDays) {
				t.Fatalf("rows = %d, want %d", len(rows), len(tc.wantDays))
			}
			total := 0
			for i, row := range rows {
				if row.Days != tc.wantDays[i] {
					t.Errorf("row %d: days = %d, want %d", i, row.Days, tc.wantDays[i])
				}
				if got := row.PeriodStart.Format("2006-01-02"); got != tc.wantStart[i] {
					t.Errorf("row %d: period_start = %s, want %s", i, got, tc.wantStart[i])
				}
				// PeriodEnd is inclusive, so it is days-1 after the start.
				wantEnd := row.PeriodStart.AddDate(0, 0, row.Days-1)
				if !row.PeriodEnd.Equal(wantEnd) {
					t.Errorf("row %d: period_end = %s, want %s", i, row.PeriodEnd, wantEnd)
				}
				total += row.Days
			}
			if total != tc.termDays {
				t.Errorf("rows cover %d days, want the full term of %d", total, tc.termDays)
			}
			// The rows tile the term without gap or overlap.
			for i := 1; i < len(rows); i++ {
				if !rows[i].PeriodStart.Equal(rows[i-1].PeriodEnd.AddDate(0, 0, 1)) {
					t.Errorf("row %d starts %s but row %d ends %s", i, rows[i].PeriodStart, i-1, rows[i-1].PeriodEnd)
				}
			}
			last := rows[len(rows)-1]
			if want := EndDate(date("2026-01-01"), tc.termDays); !last.PeriodEnd.AddDate(0, 0, 1).Equal(want) {
				t.Errorf("last row ends %s, term end_date is %s", last.PeriodEnd, want)
			}
		})
	}
}

// TestGenerateAmounts pins the proration rule: rent × days / rent_period_days,
// rounded to whole shillings, with the truncated last row charged pro rata.
func TestGenerateAmounts(t *testing.T) {
	cases := []struct {
		name     string
		rent     int
		basis    int
		termDays int
		cadence  int
		want     []int64
	}{
		{
			name: "a full-cadence schedule charges the basis price each row",
			rent: 250_000, basis: 30, termDays: 90, cadence: 30,
			want: []int64{250_000, 250_000, 250_000},
		},
		{
			name: "a truncated last row is charged for its own days only",
			rent: 300_000, basis: 30, termDays: 100, cadence: 30,
			want: []int64{300_000, 300_000, 300_000, 100_000},
		},
		{
			name: "a 45-day cadence on a 30-day basis is one and a half rents",
			rent: 250_000, basis: 30, termDays: 180, cadence: 45,
			want: []int64{375_000, 375_000, 375_000, 375_000},
		},
		{
			name: "a 7-day cadence rounds to whole shillings",
			rent: 250_000, basis: 30, termDays: 14, cadence: 7,
			want: []int64{58_333, 58_333},
		},
		{
			name: "a 7-day basis prorates up to a 30-day cadence",
			rent: 70_000, basis: 7, termDays: 60, cadence: 30,
			want: []int64{300_000, 300_000},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := Generate(tc.rent, tc.basis, tc.termDays, tc.cadence, date("2026-03-01"), nil)
			if len(rows) != len(tc.want) {
				t.Fatalf("rows = %d, want %d", len(rows), len(tc.want))
			}
			for i, row := range rows {
				if row.Amount != tc.want[i] {
					t.Errorf("row %d: amount = %d, want %d", i, row.Amount, tc.want[i])
				}
			}
		})
	}
}

// TestGenerateDueDates covers both due-date modes: the default (due on the day
// the period starts) and due_day snapping for monthly-style cadences.
func TestGenerateDueDates(t *testing.T) {
	t.Run("without a due day, each row falls due when it starts", func(t *testing.T) {
		rows := Generate(300_000, 30, 90, 30, date("2026-01-15"), nil)
		for i, row := range rows {
			if !row.DueDate.Equal(row.PeriodStart) {
				t.Errorf("row %d: due %s, want the period start %s", i, row.DueDate, row.PeriodStart)
			}
		}
	})

	t.Run("a due day later in the month snaps forward inside the same month", func(t *testing.T) {
		// Periods start on the 1st; due day 5 is still ahead of each start.
		rows := Generate(300_000, 30, 90, 30, date("2026-01-01"), intPtr(5))
		want := []string{"2026-01-05", "2026-02-05", "2026-03-05"}
		for i, row := range rows {
			if got := row.DueDate.Format("2006-01-02"); got != want[i] {
				t.Errorf("row %d: due = %s, want %s", i, got, want[i])
			}
		}
	})

	t.Run("a due day already past rolls into the next month", func(t *testing.T) {
		// Periods start on the 15th; due day 5 has gone by, so it rolls.
		rows := Generate(300_000, 30, 60, 30, date("2026-01-15"), intPtr(5))
		want := []string{"2026-02-05", "2026-03-05"}
		for i, row := range rows {
			if got := row.DueDate.Format("2006-01-02"); got != want[i] {
				t.Errorf("row %d: due = %s, want %s", i, got, want[i])
			}
		}
	})

	t.Run("a due day snaps to the 31st of each period's month", func(t *testing.T) {
		// Periods start 31 Jan, 2 Mar, 1 Apr; the 31st is still ahead of each
		// start, so it stays inside that month — and April has only 30 days.
		rows := Generate(300_000, 30, 90, 30, date("2026-01-31"), intPtr(31))
		want := []string{"2026-01-31", "2026-03-31", "2026-04-30"}
		got := []string{
			rows[0].DueDate.Format("2006-01-02"),
			rows[1].DueDate.Format("2006-01-02"),
			rows[2].DueDate.Format("2006-01-02"),
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("row %d: due = %s, want %s (period starts %s)",
					i, got[i], want[i], rows[i].PeriodStart.Format("2006-01-02"))
			}
		}
	})

	t.Run("a February period start with a 31st due day clamps to the 28th", func(t *testing.T) {
		rows := Generate(300_000, 30, 30, 30, date("2026-02-01"), intPtr(31))
		if got := rows[0].DueDate.Format("2006-01-02"); got != "2026-02-28" {
			t.Errorf("due = %s, want 2026-02-28", got)
		}
	})

	t.Run("an out-of-range due day is ignored", func(t *testing.T) {
		rows := Generate(300_000, 30, 30, 30, date("2026-02-01"), intPtr(45))
		if !rows[0].DueDate.Equal(rows[0].PeriodStart) {
			t.Errorf("due = %s, want the period start", rows[0].DueDate)
		}
	})
}

// TestGenerateRejectsNonsense keeps the generator total: bad spans yield no
// rows rather than a panic or an unbounded loop.
func TestGenerateRejectsNonsense(t *testing.T) {
	cases := []struct {
		name          string
		term, cadence int
	}{
		{"zero term", 0, 30},
		{"negative term", -10, 30},
		{"zero cadence", 90, 0},
		{"negative cadence", 90, -5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rows := Generate(300_000, 30, tc.term, tc.cadence, date("2026-01-01"), nil); rows != nil {
				t.Errorf("rows = %v, want nil", rows)
			}
		})
	}
}

func TestProrate(t *testing.T) {
	cases := []struct {
		name        string
		amount      int64
		days, basis int
		want        int64
	}{
		{"the basis period returns the price itself", 250_000, 30, 30, 250_000},
		{"a third of a shilling rounds down", 1, 1, 3, 0},
		{"a half shilling rounds away from zero", 1, 1, 2, 1},
		{"365 days of a 30-day price rounds up", 250_000, 365, 30, 3_041_667},
		{"a zero basis is not divided by", 250_000, 30, 0, 0},
		{"zero days is zero", 250_000, 0, 30, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Prorate(tc.amount, tc.days, tc.basis); got != tc.want {
				t.Errorf("Prorate(%d, %d, %d) = %d, want %d", tc.amount, tc.days, tc.basis, got, tc.want)
			}
		})
	}
}

// TestGenerateTotalTracksTheTermPrice: rounding happens per row, so the sum of
// a schedule can differ from the price of the whole term — but only by the
// rounding of each row, never more. A schedule that drifts further would bill
// a renter for days nobody agreed to.
func TestGenerateTotalTracksTheTermPrice(t *testing.T) {
	cases := []struct {
		rent, basis, term, cadence int
	}{
		{250_000, 30, 180, 30},
		{300_000, 30, 100, 30},
		{250_000, 30, 14, 7},
		{70_000, 7, 365, 30},
		{123_457, 30, 365, 45},
		{999_999, 31, 400, 7},
		{1, 30, 90, 30},
		{250_000, 30, 10, 30}, // term shorter than the cadence: one row
	}
	for _, tc := range cases {
		rows := Generate(tc.rent, tc.basis, tc.term, tc.cadence, date("2026-01-01"), nil)
		if len(rows) == 0 {
			t.Fatalf("rent %d/%dd over %dd at %dd cadence produced no rows",
				tc.rent, tc.basis, tc.term, tc.cadence)
		}
		var sum int64
		for _, row := range rows {
			sum += row.Amount
		}
		whole := Prorate(int64(tc.rent), tc.term, tc.basis)
		drift := sum - whole
		if drift < 0 {
			drift = -drift
		}
		if drift > int64(len(rows)) {
			t.Errorf("rent %d/%dd over %dd at %dd cadence: rows sum to %d, the whole term prices at %d (drift %d over %d rows)",
				tc.rent, tc.basis, tc.term, tc.cadence, sum, whole, drift, len(rows))
		}
	}
}

// TestRentPerPeriodMatchesAFullScheduleRow is the whole point of the function:
// the figure the contract document states must be the figure the renter is
// actually billed. A schedule row covering a full payment period is that bill,
// so the two are checked against each other rather than against a constant
// worked out by hand.
//
// The bug it closes (PLAN2 Phase 9): the document used to print the unit price
// beside the payment-period label — "TZS 100,000 per Quarterly (90 days)" for a
// unit priced per 30 days — while the first schedule row said 300,000.
func TestRentPerPeriodMatchesAFullScheduleRow(t *testing.T) {
	cases := []struct {
		name              string
		rent              int64
		rentPeriodDays    int
		paymentPeriodDays int
		want              int64
	}{
		{"quarterly on a monthly price", 100_000, 30, 90, 300_000},
		{"same basis", 300_000, 30, 30, 300_000},
		{"yearly on a monthly price", 100_000, 30, 365, 1_216_667},
		{"monthly on a quarterly price", 300_000, 90, 30, 100_000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RentPerPeriod(tc.rent, tc.rentPeriodDays, tc.paymentPeriodDays)
			if got != tc.want {
				t.Errorf("RentPerPeriod(%d, %d, %d) = %d, want %d",
					tc.rent, tc.rentPeriodDays, tc.paymentPeriodDays, got, tc.want)
			}

			// A term of exactly one payment period yields one full row, whose
			// amount must be the same number.
			rows := Generate(int(tc.rent), tc.rentPeriodDays,
				tc.paymentPeriodDays, tc.paymentPeriodDays,
				time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC), nil)
			if len(rows) != 1 {
				t.Fatalf("Generate produced %d rows, want 1", len(rows))
			}
			if rows[0].Amount != got {
				t.Errorf("schedule row = %d, RentPerPeriod = %d — the document would state the wrong figure",
					rows[0].Amount, got)
			}

			// And over a longer term every full row agrees too.
			long := Generate(int(tc.rent), tc.rentPeriodDays,
				tc.paymentPeriodDays*3, tc.paymentPeriodDays,
				time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC), nil)
			for i, row := range long {
				if row.Days == tc.paymentPeriodDays && row.Amount != got {
					t.Errorf("row %d (%d days) = %d, want %d", i, row.Days, row.Amount, got)
				}
			}
		})
	}
}

// TestRentBasisPhrase: the unit price and the span it covers, as the document
// prints it beside the per-period figure.
func TestRentBasisPhrase(t *testing.T) {
	if got := RentBasisPhrase("TZS 100,000", 30); got != "TZS 100,000 / 30 days" {
		t.Errorf("RentBasisPhrase = %q, want \"TZS 100,000 / 30 days\"", got)
	}
}
