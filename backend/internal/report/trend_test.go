package report_test

import (
	"testing"

	"tms/backend/internal/report"
)

// The Phase 11 arithmetic, away from the database: the two numbers a report can
// get wrong rather than merely miss.

func TestChangePct(t *testing.T) {
	for _, c := range []struct {
		name              string
		current, previous int64
		want              *float64
	}{
		{name: "a rise from nothing has no percentage", current: 500, previous: 0},
		{name: "nothing from nothing likewise"},
		{name: "a fall to nothing is -100", current: 0, previous: 400, want: ptr(-100)},
		{name: "doubling is +100", current: 800, previous: 400, want: ptr(100)},
		{name: "rounded to one decimal", current: 1234, previous: 1000, want: ptr(23.4)},
		{name: "negative rounds away from zero", current: 900, previous: 1000, want: ptr(-10)},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := report.ChangePct(c.current, c.previous)
			switch {
			case c.want == nil && got != nil:
				t.Fatalf("got %v, want null", *got)
			case c.want != nil && got == nil:
				t.Fatalf("got null, want %v", *c.want)
			case c.want != nil && *got != *c.want:
				t.Fatalf("got %v, want %v", *got, *c.want)
			}
		})
	}
}

func TestSlope(t *testing.T) {
	for _, c := range []struct {
		name   string
		series []int64
		want   float64
	}{
		{name: "no points", want: 0},
		{name: "one point is a level, not a trend", series: []int64{500}, want: 0},
		{name: "flat", series: []int64{100, 100, 100}, want: 0},
		{name: "rising by a hundred a bucket", series: []int64{0, 100, 200, 300}, want: 100},
		{name: "falling", series: []int64{300, 200, 100}, want: -100},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := report.Slope(c.series); got != c.want {
				t.Fatalf("slope = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRate(t *testing.T) {
	if r := report.Rate(5, 0); r != nil {
		t.Errorf("a rate against nothing = %v, want null", *r)
	}
	if r := report.Rate(1, 4); r == nil || *r != 0.25 {
		t.Errorf("rate = %v, want 0.25", r)
	}
}

func ptr(v float64) *float64 { return &v }
