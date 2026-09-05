package report

import (
	"fmt"
	"strings"
	"time"
)

// LocalZone is the wall clock a report's calendar boundaries are read against.
// A landlord asking for "this month" means the month on the wall in Dar es
// Salaam, not the one in UTC — which at 22:00 on the last day of a month is a
// different month (SPEC §6 uses the same zone for send hours).
const LocalZone = "Africa/Dar_es_Salaam"

const eatOffsetHours = 3

// Zone resolves the org wall clock, falling back to a fixed UTC+3 when the host
// image ships without tzdata.
func Zone() *time.Location {
	if loc, err := time.LoadLocation(LocalZone); err == nil {
		return loc
	}
	return time.FixedZone("EAT", eatOffsetHours*60*60)
}

// Period is a closed range of calendar days, inclusive at both ends. `From` and
// `To` are the dates the SQL `due_date BETWEEN` uses; FromTime/ToTime are the
// half-open instants the `paid_at` window uses.
type Period struct {
	From time.Time
	To   time.Time
}

// FromTime is the first instant of the period in the local zone.
func (p Period) FromTime() time.Time { return startOfDay(p.From) }

// ToTime is the first instant *after* the period: the `paid_at` window is
// half-open so a payment recorded at 23:59:59.9 on the last day still counts.
func (p Period) ToTime() time.Time { return startOfDay(p.To).AddDate(0, 0, 1) }

func startOfDay(d time.Time) time.Time {
	loc := Zone()
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
}

// ParsePeriod resolves the `period` query parameter of GET /reports/summary.
// Empty or "month" means the calendar month `now` falls in; otherwise the value
// must be `YYYY-MM` (API.md).
func ParsePeriod(raw string, now time.Time) (Period, error) {
	switch raw {
	case "", "month", "current":
		local := now.In(Zone())
		return monthOf(local.Year(), local.Month()), nil
	}
	t, err := time.Parse("2006-01", raw)
	if err != nil {
		return Period{}, fmt.Errorf("period must be `month` or `YYYY-MM`")
	}
	return monthOf(t.Year(), t.Month()), nil
}

func monthOf(year int, month time.Month) Period {
	first := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	return Period{From: first, To: first.AddDate(0, 1, -1)}
}

// Statuses a renter's tenancy can report, worst first (API.md).
const (
	StatusOverdue = "overdue"
	StatusPartial = "partial"
	StatusPending = "pending"
	StatusPaid    = "paid"
)

// Statuses is the accepted `status=` filter vocabulary of
// GET /reports/payment-status.
//
//nolint:gochecknoglobals // fixed vocabulary, read-only.
var Statuses = []string{StatusPaid, StatusPending, StatusOverdue, StatusPartial}

// WorstStatus is the renter's headline status: the worst state among their
// unsettled schedules, `paid` when nothing is unsettled (API.md). "Worst" is
// ordered overdue > partial > pending, because that is the order a landlord
// chases them in.
func WorstStatus(overdue, partial, pending int64) string {
	switch {
	case overdue > 0:
		return StatusOverdue
	case partial > 0:
		return StatusPartial
	case pending > 0:
		return StatusPending
	default:
		return StatusPaid
	}
}

// Groupings accepted by GET /reports/collections.
const (
	GroupDay   = "day"
	GroupWeek  = "week"
	GroupMonth = "month"
)

// Groups is the accepted `group=` vocabulary.
//
//nolint:gochecknoglobals // fixed vocabulary, read-only.
var Groups = []string{GroupDay, GroupWeek, GroupMonth}

// TruncBucket snaps a date down to the start of its bucket, matching Postgres
// `date_trunc` exactly: weeks start on Monday, months on the 1st.
func TruncBucket(d time.Time, group string) time.Time {
	d = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	switch group {
	case GroupMonth:
		return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
	case GroupWeek:
		// Go's Weekday has Sunday at 0; Postgres' ISO week starts on Monday.
		offset := (int(d.Weekday()) + 6) % 7
		return d.AddDate(0, 0, -offset)
	default:
		return d
	}
}

// Buckets enumerates every bucket start from `from` to `to` inclusive, so a
// month in which nothing happened is reported as a zero rather than as a gap in
// the series.
//
// It stops one past MaxBuckets: the caller rejects an over-long range anyway,
// and `from=0001-01-01&to=9999-12-31&group=day` must not build a three-million
// element slice before that rejection.
func Buckets(from, to time.Time, group string) []time.Time {
	if to.Before(from) {
		return nil
	}
	out := make([]time.Time, 0, MaxBuckets+1)
	cur := TruncBucket(from, group)
	last := TruncBucket(to, group)
	for !cur.After(last) {
		if len(out) > MaxBuckets {
			return out
		}
		out = append(out, cur)
		switch group {
		case GroupMonth:
			cur = cur.AddDate(0, 1, 0)
		case GroupWeek:
			cur = cur.AddDate(0, 0, 7)
		default:
			cur = cur.AddDate(0, 0, 1)
		}
	}
	return out
}

// MaxBuckets caps a collections series. A day-grouped decade would be four
// thousand points nobody reads and one response nobody wants to parse.
const MaxBuckets = 400

// ------------------------------------------------------------------- CSV --

// csvInjectionPrefixes are the characters a spreadsheet reads as the start of a
// formula. A renter called `=cmd|'/c calc'!A1` is a live payload in Excel,
// LibreOffice and Sheets the moment a landlord opens the export.
const csvInjectionPrefixes = "=+-@\t\r"

// CSVCell neutralises formula injection in one exported cell.
//
// The value is prefixed with an apostrophe when it opens with a character a
// spreadsheet treats as a formula, which every spreadsheet then shows as the
// literal text. Quoting, commas, quotes and newlines are `encoding/csv`'s job
// and are left to it — this only defuses the cell's *first* character.
func CSVCell(v string) string {
	if v == "" {
		return v
	}
	if strings.ContainsRune(csvInjectionPrefixes, rune(v[0])) {
		return "'" + v
	}
	return v
}
