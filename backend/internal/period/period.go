// Package period turns a report's cadence into the half-open window it covers,
// and that window into the buckets a series is drawn on.
//
// Every Part 2 report takes the same `cadence` + `from`/`to` parameters (PLAN2
// Phase 11), and two endpoints that resolve "this quarter" differently would
// report different revenue for the same quarter. So the resolution lives here,
// once, and every handler calls it.
//
// Two rules hold throughout:
//
//   - Windows are half-open, `[From, To)`. A month is "the 1st up to but not
//     including the 1st of the next month", which is the only convention under
//     which consecutive windows neither overlap nor leave a day in the gap.
//   - Boundaries are cut on the platform wall clock (internal/tz — Africa/
//     Dar_es_Salaam), because a landlord's month is the month it is where they
//     stand. Every time in a Window is midnight EAT.
package period

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"tms/backend/internal/tz"
)

// Cadences a report may ask for.
const (
	CadenceMonth    = "month"
	CadenceQuarter  = "quarter"
	CadenceHalfYear = "half_year"
	CadenceYear     = "year"
	CadenceCustom   = "custom"
)

// Bucket sizes a series may be drawn on.
const (
	BucketDay   = "day"
	BucketWeek  = "week"
	BucketMonth = "month"
)

// Thresholds for automatic bucket sizing (PLAN2 Phase 9): daily points stay
// readable to about two months, weekly ones to about half a year.
const (
	maxDaysForDayBuckets   = 62
	maxWeeksForWeekBuckets = 26
	// MaxBuckets caps a series so a five-year custom range cannot ask the
	// database for two thousand points or the browser to draw them.
	MaxBuckets = 400
	// maxCustomYears bounds a custom range (PLAN2 Phase 11).
	maxCustomYears = 5
)

// Errors callers translate into 400s.
var (
	ErrCadence      = errors.New("unknown cadence")
	ErrRangeMissing = errors.New("a custom cadence needs both from and to")
	ErrRangeOrder   = errors.New("from must be before to")
	ErrRangeTooLong = errors.New("a custom range may not exceed five years")
	ErrBucketSize   = errors.New("unknown bucket size")
)

// Window is one half-open span `[From, To)` at EAT midnight, and the span of
// equal shape immediately before it.
//
// Previous is what a "% change vs the previous period" figure is computed
// against. It is a pointer so the type is not infinitely recursive: the
// previous window's own Previous is nil, because nothing asks for the quarter
// before the quarter before.
type Window struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Cadence  string    `json:"cadence"`
	Previous *Window   `json:"previous,omitempty"`
}

// Days is the window's length in whole days. Every window this package
// produces starts and ends at EAT midnight, so the division is exact.
func (w Window) Days() int {
	return int(w.To.Sub(w.From).Hours() / 24)
}

// Resolve turns a cadence and an anchor instant into the window to report on.
//
// The anchor is "the moment the caller means" — usually now, or the date a
// PeriodPicker has navigated to. For the calendar cadences the anchor names the
// unit: an anchor anywhere in May yields the whole of May. `from`/`to` are
// ignored for those, and required for `custom`.
//
// The previous window is the same span immediately before: the previous
// calendar unit for a calendar cadence (so the month before a 31-day May is a
// 30-day April, not "31 days back"), and an equal-length span for a custom
// range.
func Resolve(cadence string, anchor time.Time, from, to *time.Time) (Window, error) {
	cadence = strings.ToLower(strings.TrimSpace(cadence))
	if cadence == "" {
		cadence = CadenceMonth
	}
	loc := tz.Zone()
	a := anchor.In(loc)

	var start, end, prevStart time.Time
	switch cadence {
	case CadenceMonth:
		start = day(a.Year(), a.Month(), 1, loc)
		end = start.AddDate(0, 1, 0)
		prevStart = start.AddDate(0, -1, 0)
	case CadenceQuarter:
		// Calendar quarters: Jan–Mar, Apr–Jun, Jul–Sep, Oct–Dec.
		first := time.Month(((int(a.Month())-1)/3)*3 + 1)
		start = day(a.Year(), first, 1, loc)
		end = start.AddDate(0, 3, 0)
		prevStart = start.AddDate(0, -3, 0)
	case CadenceHalfYear:
		// Jan–Jun and Jul–Dec.
		first := time.January
		if a.Month() > time.June {
			first = time.July
		}
		start = day(a.Year(), first, 1, loc)
		end = start.AddDate(0, 6, 0)
		prevStart = start.AddDate(0, -6, 0)
	case CadenceYear:
		start = day(a.Year(), time.January, 1, loc)
		end = start.AddDate(1, 0, 0)
		prevStart = start.AddDate(-1, 0, 0)
	case CadenceCustom:
		if from == nil || to == nil {
			return Window{}, ErrRangeMissing
		}
		start = midnight(*from, loc)
		end = midnight(*to, loc)
		if !start.Before(end) {
			return Window{}, ErrRangeOrder
		}
		if end.After(start.AddDate(maxCustomYears, 0, 0)) {
			return Window{}, ErrRangeTooLong
		}
		// An equal-length span immediately before: the only meaning "previous"
		// can carry for a range the caller invented.
		prevStart = start.Add(-end.Sub(start))
	default:
		return Window{}, fmt.Errorf("%w: %q", ErrCadence, cadence)
	}

	return Window{
		From:    start,
		To:      end,
		Cadence: cadence,
		Previous: &Window{
			From:    prevStart,
			To:      start,
			Cadence: cadence,
		},
	}, nil
}

// BucketSize picks the granularity a window's series is drawn on: days up to
// about two months, weeks up to about half a year, months beyond that.
//
// A non-empty override wins, provided it is one of the three sizes. It is
// validated here rather than at the edge so every endpoint rejects the same
// spellings.
func BucketSize(w Window, override string) (string, error) {
	if v := strings.ToLower(strings.TrimSpace(override)); v != "" {
		switch v {
		case BucketDay, BucketWeek, BucketMonth:
			return v, nil
		default:
			return "", fmt.Errorf("%w: %q", ErrBucketSize, override)
		}
	}
	switch days := w.Days(); {
	case days <= maxDaysForDayBuckets:
		return BucketDay, nil
	case days <= maxWeeksForWeekBuckets*7:
		return BucketWeek, nil
	default:
		return BucketMonth, nil
	}
}

// Buckets are the starts of every bucket in the window, in order: the zero-fill
// skeleton a series is built on, so a bucket with no payments in it is a point
// at zero rather than a gap in the line.
//
// The first bucket starts at the window's own From, even when that is mid-week
// or mid-month — a custom range starting on the 12th should report from the
// 12th, not silently widen itself back to the 1st. Later buckets are aligned to
// the step from there. The list is capped at MaxBuckets.
func Buckets(w Window, size string) []time.Time {
	if !w.From.Before(w.To) {
		return []time.Time{}
	}
	out := make([]time.Time, 0, 32)
	for cur := w.From; cur.Before(w.To) && len(out) < MaxBuckets; cur = advance(cur, size) {
		out = append(out, cur)
	}
	return out
}

// Overflows reports whether a window needs more buckets than MaxBuckets, which
// is the question an endpoint must answer *before* it draws a series: Buckets
// silently stops at the cap, and a series that quietly ends two years early is
// a wrong chart rather than a refused one.
func Overflows(w Window, size string) bool {
	n := 0
	for cur := w.From; cur.Before(w.To); cur = advance(cur, size) {
		n++
		if n > MaxBuckets {
			return true
		}
	}
	return false
}

// advance steps one bucket forward. AddDate on a zoned midnight keeps the
// result at midnight, so a month step lands on the 1st of the next month rather
// than drifting by the length of the one it left.
func advance(t time.Time, size string) time.Time {
	switch size {
	case BucketWeek:
		return t.AddDate(0, 0, 7)
	case BucketMonth:
		return t.AddDate(0, 1, 0)
	default:
		return t.AddDate(0, 0, 1)
	}
}

// day is midnight EAT on a calendar date.
func day(year int, month time.Month, d int, loc *time.Location) time.Time {
	return time.Date(year, month, d, 0, 0, 0, 0, loc)
}

// midnight is the EAT midnight starting the calendar day t names, read on the
// EAT wall clock. A caller that parsed "2026-03-01" as a UTC date and one that
// passed an instant therefore agree on the day.
func midnight(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return day(l.Year(), l.Month(), l.Day(), loc)
}
