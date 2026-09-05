package period_test

import (
	"testing"
	"time"

	"tms/backend/internal/period"
	"tms/backend/internal/tz"
)

// Overflows is what stands between a five-year custom range and a two-thousand
// point response: Buckets stops at the cap silently, so the endpoint has to ask
// first.
func TestOverflows(t *testing.T) {
	day := func(s string) time.Time {
		d, err := time.ParseInLocation("2006-01-02", s, tz.Zone())
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	win := func(from, to string) period.Window {
		return period.Window{From: day(from), To: day(to), Cadence: period.CadenceCustom}
	}
	for _, c := range []struct {
		name string
		w    period.Window
		size string
		want bool
	}{
		{name: "a month of days", w: win("2026-03-01", "2026-04-01"), size: period.BucketDay},
		{name: "a year of days fits", w: win("2026-01-01", "2027-01-01"), size: period.BucketDay},
		{name: "two years of days does not", w: win("2026-01-01", "2028-01-01"), size: period.BucketDay, want: true},
		{name: "two years of weeks fits", w: win("2026-01-01", "2028-01-01"), size: period.BucketWeek},
		{name: "five years of months fits", w: win("2026-01-01", "2031-01-01"), size: period.BucketMonth},
		{name: "an empty window", w: win("2026-01-01", "2026-01-01"), size: period.BucketDay},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := period.Overflows(c.w, c.size); got != c.want {
				t.Fatalf("Overflows = %v, want %v (buckets: %d)",
					got, c.want, len(period.Buckets(c.w, c.size)))
			}
		})
	}
}
