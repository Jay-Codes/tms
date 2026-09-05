package notify_test

import (
	"testing"
	"time"

	"tms/backend/internal/notify"
)

// TestLocalDateAtTheEATDayBoundary: the day a sweep runs for is the org's own
// calendar day. At 23:30 UTC it is already 02:30 tomorrow in Dar es Salaam, and
// a dedupe key (and a `due_date = today`) written against the UTC day would be
// a day behind the renter.
func TestLocalDateAtTheEATDayBoundary(t *testing.T) {
	loc, err := time.LoadLocation(notify.LocalZone)
	if err != nil {
		loc = time.FixedZone("EAT", 3*60*60)
	}
	cases := []struct {
		name string
		utc  time.Time
		want string
	}{
		{"23:30 UTC is already tomorrow in EAT",
			time.Date(2026, 9, 5, 23, 30, 0, 0, time.UTC), "2026-09-06"},
		{"00:30 UTC is still the same EAT day",
			time.Date(2026, 9, 6, 0, 30, 0, 0, time.UTC), "2026-09-06"},
		{"21:30 UTC is the EAT day just ending",
			time.Date(2026, 9, 5, 21, 30, 0, 0, time.UTC), "2026-09-06"},
		{"noon UTC is unambiguous",
			time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC), "2026-09-05"},
		{"the last instant of an EAT day",
			time.Date(2026, 9, 5, 20, 59, 59, 0, time.UTC), "2026-09-05"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := notify.LocalDate(tc.utc.In(loc)).Format("2006-01-02")
			if got != tc.want {
				t.Errorf("LocalDate(%s in EAT) = %s, want %s", tc.utc.Format(time.RFC3339), got, tc.want)
			}
		})
	}
}
