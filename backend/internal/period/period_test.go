package period_test

import (
	"errors"
	"testing"
	"time"

	"tms/backend/internal/period"
	"tms/backend/internal/tz"
)

// eat parses a "2006-01-02 15:04" wall-clock reading in Africa/Dar_es_Salaam.
func eat(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation("2006-01-02 15:04", s, tz.Zone())
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return parsed
}

func eatDay(t *testing.T, s string) time.Time {
	t.Helper()
	return eat(t, s+" 00:00")
}

// TestResolveCalendarCadences: an anchor anywhere inside a calendar unit
// resolves to the whole unit, and "previous" is the previous unit — not a
// fixed number of days back, which would put the month before a 31-day May
// partly inside March.
func TestResolveCalendarCadences(t *testing.T) {
	cases := []struct {
		name             string
		cadence          string
		anchor           string
		from, to         string
		prevFrom         string
		wantDays         int
		wantPreviousDays int
	}{
		{"month mid", period.CadenceMonth, "2026-05-17 13:20", "2026-05-01", "2026-06-01", "2026-04-01", 31, 30},
		{"month first instant", period.CadenceMonth, "2026-05-01 00:00", "2026-05-01", "2026-06-01", "2026-04-01", 31, 30},
		{"month last instant", period.CadenceMonth, "2026-05-31 23:59", "2026-05-01", "2026-06-01", "2026-04-01", 31, 30},
		{"quarter Q1", period.CadenceQuarter, "2026-02-14 09:00", "2026-01-01", "2026-04-01", "2025-10-01", 90, 92},
		{"quarter Q2", period.CadenceQuarter, "2026-04-01 00:00", "2026-04-01", "2026-07-01", "2026-01-01", 91, 90},
		{"quarter Q3", period.CadenceQuarter, "2026-09-30 23:59", "2026-07-01", "2026-10-01", "2026-04-01", 92, 91},
		{"quarter Q4", period.CadenceQuarter, "2026-12-31 23:59", "2026-10-01", "2027-01-01", "2026-07-01", 92, 92},
		{"half year first", period.CadenceHalfYear, "2026-06-30 23:59", "2026-01-01", "2026-07-01", "2025-07-01", 181, 184},
		{"half year second", period.CadenceHalfYear, "2026-07-01 00:00", "2026-07-01", "2027-01-01", "2026-01-01", 184, 181},
		{"year", period.CadenceYear, "2026-08-08 12:00", "2026-01-01", "2027-01-01", "2025-01-01", 365, 365},
		// Leap years: 2024 is one, so February and the year itself are a day
		// longer, and 2025's "previous year" is 366 days.
		{"leap february", period.CadenceMonth, "2024-02-10 06:00", "2024-02-01", "2024-03-01", "2024-01-01", 29, 31},
		{"leap year", period.CadenceYear, "2024-03-01 00:00", "2024-01-01", "2025-01-01", "2023-01-01", 366, 365},
		{"year after leap", period.CadenceYear, "2025-06-01 00:00", "2025-01-01", "2026-01-01", "2024-01-01", 365, 366},
		{"leap Q1", period.CadenceQuarter, "2024-01-31 23:00", "2024-01-01", "2024-04-01", "2023-10-01", 91, 92},
		{"leap half year", period.CadenceHalfYear, "2024-05-05 00:00", "2024-01-01", "2024-07-01", "2023-07-01", 182, 184},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, err := period.Resolve(tc.cadence, eat(t, tc.anchor), nil, nil)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if !w.From.Equal(eatDay(t, tc.from)) || !w.To.Equal(eatDay(t, tc.to)) {
				t.Errorf("window = [%s, %s), want [%s, %s)", w.From, w.To, tc.from, tc.to)
			}
			if w.Cadence != tc.cadence {
				t.Errorf("cadence = %q, want %q", w.Cadence, tc.cadence)
			}
			if w.Days() != tc.wantDays {
				t.Errorf("Days() = %d, want %d", w.Days(), tc.wantDays)
			}
			if w.Previous == nil {
				t.Fatal("Previous is nil")
			}
			if !w.Previous.From.Equal(eatDay(t, tc.prevFrom)) {
				t.Errorf("previous from = %s, want %s", w.Previous.From, tc.prevFrom)
			}
			// The previous window must end exactly where this one starts: no
			// overlap, no gap.
			if !w.Previous.To.Equal(w.From) {
				t.Errorf("previous to = %s, want %s", w.Previous.To, w.From)
			}
			if w.Previous.Days() != tc.wantPreviousDays {
				t.Errorf("previous Days() = %d, want %d", w.Previous.Days(), tc.wantPreviousDays)
			}
		})
	}
}

// TestResolveReadsTheEATWallClock: the boundary case that motivates internal/tz.
// 21:30 UTC on 30 April is already 00:30 EAT on 1 May, so a landlord in Dar es
// Salaam asking for "this month" at that instant means May, not April.
func TestResolveReadsTheEATWallClock(t *testing.T) {
	anchor := time.Date(2026, time.April, 30, 21, 30, 0, 0, time.UTC)
	w, err := period.Resolve(period.CadenceMonth, anchor, nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !w.From.Equal(eatDay(t, "2026-05-01")) {
		t.Errorf("from = %s, want 2026-05-01 EAT", w.From)
	}

	// And every boundary is EAT midnight, which is 21:00 the previous day UTC.
	if h, m, s := w.From.UTC().Clock(); h != 21 || m != 0 || s != 0 {
		t.Errorf("from in UTC = %s, want 21:00:00 (EAT midnight)", w.From.UTC())
	}

	// The other side of the same boundary: 20:30 UTC is still 23:30 EAT on the
	// 30th, so the window is April's.
	early, err := period.Resolve(period.CadenceMonth,
		time.Date(2026, time.April, 30, 20, 30, 0, 0, time.UTC), nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !early.From.Equal(eatDay(t, "2026-04-01")) {
		t.Errorf("from = %s, want 2026-04-01 EAT", early.From)
	}
}

// TestResolveDefaultsToMonth: an empty cadence is the dashboard's default.
func TestResolveDefaultsToMonth(t *testing.T) {
	w, err := period.Resolve("  ", eat(t, "2026-05-17 13:20"), nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if w.Cadence != period.CadenceMonth || !w.From.Equal(eatDay(t, "2026-05-01")) {
		t.Errorf("empty cadence resolved to %s [%s, %s)", w.Cadence, w.From, w.To)
	}
}

func TestResolveCustom(t *testing.T) {
	from := eatDay(t, "2026-03-12")
	to := eatDay(t, "2026-04-12")

	w, err := period.Resolve(period.CadenceCustom, time.Now(), &from, &to)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !w.From.Equal(from) || !w.To.Equal(to) {
		t.Errorf("window = [%s, %s), want [%s, %s)", w.From, w.To, from, to)
	}
	// A custom range's previous window is an equal-length span ending where it
	// starts — there is no calendar unit to fall back on.
	if w.Previous.Days() != w.Days() || !w.Previous.To.Equal(w.From) {
		t.Errorf("previous = [%s, %s) (%d days), want %d days ending at %s",
			w.Previous.From, w.Previous.To, w.Previous.Days(), w.Days(), w.From)
	}

	// A custom window keeps the caller's day even mid-month, and an instant
	// mid-day is read down to the EAT day it falls in.
	noon := eat(t, "2026-03-12 12:00")
	sameDay, err := period.Resolve(period.CadenceCustom, time.Now(), &noon, &to)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !sameDay.From.Equal(from) {
		t.Errorf("from = %s, want %s", sameDay.From, from)
	}
}

func TestResolveRejects(t *testing.T) {
	from := eatDay(t, "2026-03-12")
	to := eatDay(t, "2026-04-12")
	tooFar := eatDay(t, "2031-03-13")
	exactlyFive := eatDay(t, "2031-03-12")

	cases := []struct {
		name     string
		cadence  string
		from, to *time.Time
		want     error
	}{
		{"unknown cadence", "fortnight", nil, nil, period.ErrCadence},
		{"custom without range", period.CadenceCustom, nil, nil, period.ErrRangeMissing},
		{"custom without to", period.CadenceCustom, &from, nil, period.ErrRangeMissing},
		{"custom reversed", period.CadenceCustom, &to, &from, period.ErrRangeOrder},
		{"custom empty", period.CadenceCustom, &from, &from, period.ErrRangeOrder},
		{"custom over five years", period.CadenceCustom, &from, &tooFar, period.ErrRangeTooLong},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := period.Resolve(tc.cadence, time.Now(), tc.from, tc.to); !errors.Is(err, tc.want) {
				t.Errorf("Resolve error = %v, want %v", err, tc.want)
			}
		})
	}

	// Exactly five years is allowed; the day after it is not.
	if _, err := period.Resolve(period.CadenceCustom, time.Now(), &from, &exactlyFive); err != nil {
		t.Errorf("exactly five years: %v", err)
	}
}

func TestBucketSize(t *testing.T) {
	from := eatDay(t, "2026-01-01")
	win := func(days int) period.Window {
		return period.Window{From: from, To: from.AddDate(0, 0, days), Cadence: period.CadenceCustom}
	}

	cases := []struct {
		name     string
		w        period.Window
		override string
		want     string
		wantErr  error
	}{
		{"one day", win(1), "", period.BucketDay, nil},
		{"62 days", win(62), "", period.BucketDay, nil},
		{"63 days", win(63), "", period.BucketWeek, nil},
		{"26 weeks", win(26 * 7), "", period.BucketWeek, nil},
		{"26 weeks + a day", win(26*7 + 1), "", period.BucketMonth, nil},
		{"a year", win(365), "", period.BucketMonth, nil},
		{"override wins", win(365), "day", period.BucketDay, nil},
		{"override cased", win(5), "  WEEK ", period.BucketWeek, nil},
		{"override rejected", win(5), "fortnight", "", period.ErrBucketSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := period.BucketSize(tc.w, tc.override)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("BucketSize = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuckets(t *testing.T) {
	may, err := period.Resolve(period.CadenceMonth, eat(t, "2026-05-17 13:20"), nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	daily := period.Buckets(may, period.BucketDay)
	if len(daily) != 31 {
		t.Errorf("daily buckets = %d, want 31", len(daily))
	}
	if !daily[0].Equal(eatDay(t, "2026-05-01")) || !daily[30].Equal(eatDay(t, "2026-05-31")) {
		t.Errorf("daily buckets run %s … %s", daily[0], daily[len(daily)-1])
	}

	// Every bucket start is EAT midnight, including across a month step.
	year, err := period.Resolve(period.CadenceYear, eat(t, "2024-06-01 00:00"), nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	monthly := period.Buckets(year, period.BucketMonth)
	if len(monthly) != 12 {
		t.Fatalf("monthly buckets = %d, want 12", len(monthly))
	}
	for i, b := range monthly {
		if b.Day() != 1 || b.Hour() != 0 || b.Minute() != 0 {
			t.Errorf("bucket %d = %s, want the 1st at EAT midnight", i, b)
		}
	}
	// 2024 is a leap year: the step from February must land on 1 March, not
	// drift by 28 days.
	if !monthly[2].Equal(eatDay(t, "2024-03-01")) {
		t.Errorf("third bucket = %s, want 2024-03-01", monthly[2])
	}

	// Weekly buckets start at the window's own From rather than widening back
	// to a Monday, and the last one may be short.
	from := eatDay(t, "2026-03-12")
	to := eatDay(t, "2026-04-12")
	custom, err := period.Resolve(period.CadenceCustom, time.Now(), &from, &to)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	weekly := period.Buckets(custom, period.BucketWeek)
	if len(weekly) != 5 || !weekly[0].Equal(from) {
		t.Errorf("weekly buckets = %d starting %s, want 5 starting %s", len(weekly), weekly[0], from)
	}

	// The cap holds even for the longest range the resolver allows.
	long := eatDay(t, "2031-01-01")
	five, err := period.Resolve(period.CadenceCustom, time.Now(), &from, &long)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := len(period.Buckets(five, period.BucketDay)); got != period.MaxBuckets {
		t.Errorf("capped buckets = %d, want %d", got, period.MaxBuckets)
	}

	// An empty window has no buckets, and the slice is never nil (it is
	// marshalled straight into a JSON array).
	empty := period.Buckets(period.Window{From: from, To: from}, period.BucketDay)
	if empty == nil || len(empty) != 0 {
		t.Errorf("empty window buckets = %#v, want an empty non-nil slice", empty)
	}
}
