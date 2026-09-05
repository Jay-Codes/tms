package report_test

import (
	"testing"
	"time"

	"tms/backend/internal/report"
)

func TestParsePeriod(t *testing.T) {
	now := time.Date(2026, 9, 5, 22, 30, 0, 0, time.UTC) // 01:30 on the 6th, EAT

	cases := []struct {
		raw, from, to string
		wantErr       bool
	}{
		{raw: "", from: "2026-09-01", to: "2026-09-30"},
		{raw: "month", from: "2026-09-01", to: "2026-09-30"},
		{raw: "2026-02", from: "2026-02-01", to: "2026-02-28"},
		{raw: "2024-02", from: "2024-02-01", to: "2024-02-29"}, // leap year
		{raw: "2026-12", from: "2026-12-01", to: "2026-12-31"},
		{raw: "nonsense", wantErr: true},
		{raw: "2026-13", wantErr: true},
	}
	for _, c := range cases {
		got, err := report.ParsePeriod(c.raw, now)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParsePeriod(%q) = %v, want an error", c.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParsePeriod(%q): %v", c.raw, err)
			continue
		}
		if from := got.From.Format("2006-01-02"); from != c.from {
			t.Errorf("ParsePeriod(%q).From = %s, want %s", c.raw, from, c.from)
		}
		if to := got.To.Format("2006-01-02"); to != c.to {
			t.Errorf("ParsePeriod(%q).To = %s, want %s", c.raw, to, c.to)
		}
	}
}

// TestPeriodWindowIsHalfOpen pins the `paid_at` window: it starts at local
// midnight on the first day and ends at local midnight the day after the last,
// so a payment taken at 23:59 on the final day still counts.
func TestPeriodWindowIsHalfOpen(t *testing.T) {
	p, err := report.ParsePeriod("2026-09", time.Now())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	lastMoment := time.Date(2026, 9, 30, 23, 59, 59, 0, report.Zone())
	if !p.FromTime().Before(lastMoment) || !p.ToTime().After(lastMoment) {
		t.Errorf("window [%s, %s) does not contain %s", p.FromTime(), p.ToTime(), lastMoment)
	}
	if got := p.ToTime().Sub(p.FromTime()); got != 30*24*time.Hour {
		t.Errorf("September spans %v, want 720h", got)
	}
}

func TestWorstStatus(t *testing.T) {
	cases := []struct {
		overdue, partial, pending int64
		want                      string
	}{
		{0, 0, 0, "paid"},
		{0, 0, 3, "pending"},
		{0, 1, 3, "partial"},
		{2, 1, 3, "overdue"},
		{1, 0, 0, "overdue"},
	}
	for _, c := range cases {
		if got := report.WorstStatus(c.overdue, c.partial, c.pending); got != c.want {
			t.Errorf("WorstStatus(%d,%d,%d) = %q, want %q", c.overdue, c.partial, c.pending, got, c.want)
		}
	}
}

func TestBuckets(t *testing.T) {
	day := func(s string) time.Time {
		d, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return d
	}

	months := report.Buckets(day("2026-01-15"), day("2026-03-02"), report.GroupMonth)
	want := []string{"2026-01-01", "2026-02-01", "2026-03-01"}
	assertDates(t, "month", months, want)

	// 2026-09-02 is a Wednesday; its ISO week starts Monday the 31st of August.
	weeks := report.Buckets(day("2026-09-02"), day("2026-09-15"), report.GroupWeek)
	assertDates(t, "week", weeks, []string{"2026-08-31", "2026-09-07", "2026-09-14"})

	days := report.Buckets(day("2026-09-02"), day("2026-09-04"), report.GroupDay)
	assertDates(t, "day", days, []string{"2026-09-02", "2026-09-03", "2026-09-04"})

	if got := report.Buckets(day("2026-09-04"), day("2026-09-02"), report.GroupDay); got != nil {
		t.Errorf("a backwards range yields %v, want nothing", got)
	}
}

func assertDates(t *testing.T, what string, got []time.Time, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s buckets = %v, want %v", what, got, want)
	}
	for i := range got {
		if s := got[i].Format("2006-01-02"); s != want[i] {
			t.Errorf("%s bucket %d = %s, want %s", what, i, s, want[i])
		}
	}
}

// TestCSVCell: a cell opening with a formula character is defused, everything
// else is passed through byte for byte.
func TestCSVCell(t *testing.T) {
	cases := map[string]string{
		"":                    "",
		"Asha Mollel":         "Asha Mollel",
		"=1+1":                "'=1+1",
		"+255716000001":       "'+255716000001",
		"-2":                  "'-2",
		"@SUM(A1)":            "'@SUM(A1)",
		"\tstart":             "'\tstart",
		"\rstart":             "'\rstart",
		"Block B, \"annexe\"": "Block B, \"annexe\"",
		"a=b":                 "a=b",
	}
	for in, want := range cases {
		if got := report.CSVCell(in); got != want {
			t.Errorf("CSVCell(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBucketsStopJustPastTheCap: the handler rejects an over-long range, but it
// must not have to materialise a million-element slice to find that out.
func TestBucketsStopJustPastTheCap(t *testing.T) {
	from := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
	if got := len(report.Buckets(from, to, report.GroupDay)); got != report.MaxBuckets+1 {
		t.Errorf("Buckets over an absurd range returned %d, want %d", got, report.MaxBuckets+1)
	}
}
