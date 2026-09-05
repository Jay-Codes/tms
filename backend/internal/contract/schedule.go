package contract

import "time"

// Row is one generated payment period: the span it covers, the day it falls
// due, and the prorated amount for that span.
//
// PeriodStart and PeriodEnd are inclusive dates, so a 30-day row starting on
// the 1st ends on the 30th and the next row starts on the 31st. Days is the
// row's length, which is the cadence for every row except a truncated last one.
type Row struct {
	PeriodStart time.Time
	PeriodEnd   time.Time
	DueDate     time.Time
	Days        int
	Amount      int64
}

// Generate produces the full payment schedule of a contract (SPEC §4).
//
// The contract separates its span (termDays) from its cadence (cadenceDays):
// one row per cadence interval across the span, with the last row truncated to
// the end of the term. A 180-day term at 45-day cadence yields four 45-day
// rows; a 100-day term at 30-day cadence yields 30/30/30/10.
//
// Each row's amount is the rent scaled from its own basis to the row's length
// (rent × days / rentPeriodDays, rounded half away from zero), so a truncated
// last row is charged only for the days it covers.
//
// dueDay is optional. When nil, a row falls due on the day it starts — the
// sane default for arbitrary day-counts, where "the 5th of the month" has no
// meaning. When set (1–31), the due date snaps forward to the next occurrence
// of that day of the month on or after the period start, clamped to the length
// of the month (a due day of 31 falls on the 30th in April).
//
// Generate is pure: same inputs, same rows, no clock and no database. Phase 4
// materialises its output into payment_schedules at contract activation.
func Generate(rent, rentPeriodDays, termDays, cadenceDays int, start time.Time, dueDay *int) []Row {
	if termDays <= 0 || cadenceDays <= 0 {
		return nil
	}
	start = dateOnly(start)

	rows := make([]Row, 0, (termDays+cadenceDays-1)/cadenceDays)
	for offset := 0; offset < termDays; offset += cadenceDays {
		days := cadenceDays
		if remaining := termDays - offset; remaining < days {
			days = remaining
		}
		periodStart := start.AddDate(0, 0, offset)
		rows = append(rows, Row{
			PeriodStart: periodStart,
			// Inclusive: a `days`-long period starting on day 0 ends on
			// day days-1.
			PeriodEnd: periodStart.AddDate(0, 0, days-1),
			DueDate:   dueDateFor(periodStart, dueDay),
			Days:      days,
			Amount:    Prorate(int64(rent), days, rentPeriodDays),
		})
	}
	return rows
}

// EndDate is the exclusive end of a term: SPEC §4's `end_date = start +
// term_days`. It is the day after the last row's PeriodEnd.
func EndDate(start time.Time, termDays int) time.Time {
	return dateOnly(start).AddDate(0, 0, termDays)
}

// RentPerPeriod is the rent for one whole payment period, scaled from the
// unit's own pricing basis: a unit priced at 100,000 per 30 days, rented on a
// 90-day payment period, costs 300,000 each time it falls due.
//
// It is Prorate under a name that says what the number is for, and it is the
// figure the contract document must state. Before Part 2 the document printed
// the unit price beside the payment-period label ("TZS 100,000 per Quarterly
// (90 days)"), which was the wrong amount whenever the two bases differed
// (PLAN2 Phase 9). Because it shares Prorate's rounding, a full-length schedule
// row from Generate always equals this value exactly.
func RentPerPeriod(rentAmount int64, rentPeriodDays, paymentPeriodDays int) int64 {
	return Prorate(rentAmount, paymentPeriodDays, rentPeriodDays)
}

// Prorate scales an amount from one period basis to a number of days, rounding
// half away from zero to whole shillings (SPEC §4). A non-positive basis
// yields 0 rather than dividing by zero.
func Prorate(amount int64, days, basisDays int) int64 {
	if basisDays <= 0 || days <= 0 {
		return 0
	}
	num := amount*int64(days)*2 + int64(basisDays)
	return num / (int64(basisDays) * 2)
}

// dueDateFor snaps a period start to the contract's due day of month.
func dueDateFor(periodStart time.Time, dueDay *int) time.Time {
	if dueDay == nil || *dueDay < 1 || *dueDay > 31 {
		return periodStart
	}
	candidate := clampToMonth(periodStart.Year(), periodStart.Month(), *dueDay, periodStart.Location())
	if candidate.Before(periodStart) {
		next := periodStart.AddDate(0, 0, -(periodStart.Day()-1)).AddDate(0, 1, 0)
		candidate = clampToMonth(next.Year(), next.Month(), *dueDay, periodStart.Location())
	}
	return candidate
}

// clampToMonth builds a date, pulling a day past the month's end back to its
// last day (the 31st of April becomes the 30th).
func clampToMonth(year int, month time.Month, day int, loc *time.Location) time.Time {
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()
	if day > last {
		day = last
	}
	return time.Date(year, month, day, 0, 0, 0, 0, loc)
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
