package contract

import "testing"

func monthly(months int) Cadence { return Cadence{Days: 30 * months, Months: months} }

// TestCalendarOnTheFirst: a year from 1 Jan at 100,000 a month is twelve rows,
// each on the 1st, each the full rent — February's 28 days and July's 31
// cost the same.
func TestCalendarOnTheFirst(t *testing.T) {
	rows := GenerateCadence(100000, 30, 365, monthly(1), date("2026-01-01"), nil)
	if len(rows) != 12 {
		t.Fatalf("rows = %d, want 12", len(rows))
	}
	for i, r := range rows {
		if r.PeriodStart.Day() != 1 || !r.DueDate.Equal(r.PeriodStart) {
			t.Errorf("row %d starts %s due %s, want the 1st", i, r.PeriodStart, r.DueDate)
		}
		if r.Amount != 100000 {
			t.Errorf("row %d amount = %d, want 100000", i, r.Amount)
		}
		if next := r.PeriodEnd.AddDate(0, 0, 1); i+1 < len(rows) && !next.Equal(rows[i+1].PeriodStart) {
			t.Errorf("row %d ends %s, next starts %s", i, r.PeriodEnd, rows[i+1].PeriodStart)
		}
	}
	if got := rows[1].PeriodEnd.Format("2006-01-02"); got != "2026-02-28" {
		t.Errorf("February ends %s", got)
	}
	if got := rows[11].PeriodEnd.Format("2006-01-02"); got != "2026-12-31" {
		t.Errorf("last row ends %s", got)
	}
}

// TestCalendarMidMonthStart: moving in on the 15th opens with a prorated
// partial month up to the 1st, then full months; ending mid-month closes
// with a prorated tail.
func TestCalendarMidMonthStart(t *testing.T) {
	// 15 Jan → 10 Apr (exclusive): 17 of 31 Jan days, Feb, Mar, 9 of 30 Apr days.
	start := date("2026-01-15")
	term := int(date("2026-04-10").Sub(start).Hours() / 24)
	rows := GenerateCadence(100000, 30, term, monthly(1), start, nil)
	want := []struct {
		start, end string
		amount     int64
	}{
		{"2026-01-15", "2026-01-31", 54839}, // 100000 × 17/31
		{"2026-02-01", "2026-02-28", 100000},
		{"2026-03-01", "2026-03-31", 100000},
		{"2026-04-01", "2026-04-09", 30000}, // 100000 × 9/30
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %d, want %d", len(rows), len(want))
	}
	for i, w := range want {
		r := rows[i]
		if r.PeriodStart.Format("2006-01-02") != w.start || r.PeriodEnd.Format("2006-01-02") != w.end || r.Amount != w.amount {
			t.Errorf("row %d = %s..%s %d, want %s..%s %d", i,
				r.PeriodStart.Format("2006-01-02"), r.PeriodEnd.Format("2006-01-02"), r.Amount, w.start, w.end, w.amount)
		}
	}
}

// TestCalendarDueDay: the due day is the billing day, and one past a month's
// end clamps without dragging later months with it.
func TestCalendarDueDay(t *testing.T) {
	rows := GenerateCadence(100000, 30, 120, monthly(1), date("2026-01-05"), intPtr(5))
	for _, r := range rows[:3] {
		if r.PeriodStart.Day() != 5 || r.Amount != 100000 {
			t.Errorf("row %s amount %d, want the 5th at full rent", r.PeriodStart, r.Amount)
		}
	}

	rows = GenerateCadence(100000, 30, 120, monthly(1), date("2026-01-31"), intPtr(31))
	got := []string{}
	for _, r := range rows {
		got = append(got, r.PeriodStart.Format("2006-01-02"))
	}
	wantStarts := []string{"2026-01-31", "2026-02-28", "2026-03-31", "2026-04-30"}
	for i, w := range wantStarts {
		if i >= len(got) || got[i] != w {
			t.Fatalf("starts = %v, want prefix %v", got, wantStarts)
		}
	}
}

// TestCalendarQuarterly: a 3-month calendar cadence bills rent × 3 per row
// (a 30-day basis scaled to 90 nominal days).
func TestCalendarQuarterly(t *testing.T) {
	rows := GenerateCadence(100000, 30, 365, monthly(3), date("2026-01-01"), nil)
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(rows))
	}
	for _, r := range rows {
		if r.Amount != 300000 {
			t.Errorf("row %s amount %d, want 300000", r.PeriodStart, r.Amount)
		}
	}
	if rows[1].PeriodStart.Format("2006-01-02") != "2026-04-01" {
		t.Errorf("second quarter starts %s", rows[1].PeriodStart)
	}
}

// TestCadenceDaysUnchanged: a day cadence is Generate exactly.
func TestCadenceDaysUnchanged(t *testing.T) {
	a := GenerateCadence(100000, 30, 100, Cadence{Days: 30}, date("2026-01-01"), nil)
	b := Generate(100000, 30, 100, 30, date("2026-01-01"), nil)
	if len(a) != len(b) {
		t.Fatalf("len %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("row %d differs", i)
		}
	}
}
