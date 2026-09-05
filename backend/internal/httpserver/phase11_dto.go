package httpserver

// The Phase 11 wire shapes: the revenue series, its per-property breakdown and
// the occupancy series, plus the window/previous/change_pct trio every Part 2
// report now carries (SPEC §5.9, PLAN2 Phase 11).

// reportWindow is a resolved reporting window as it appears on the wire: `from`
// inclusive, `to` **exclusive**, and the cadence that produced them, so a
// client can prove which period it is looking at.
//
// Phase 10 named this `expenseWindowResponse`; the name below is the one every
// report uses, and the old one is an alias so nothing had to be rewritten.
type reportWindow struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Cadence string `json:"cadence"`
}

// ------------------------------------------------- GET /reports/revenue --

// revenueTotals is the money block, repeated for the window, the previous
// window and each property of the breakdown. `net` is collected − expenses:
// cash in minus cash out, not expected minus expenses, because a landlord's
// bank balance does not move on an invoice.
type revenueTotals struct {
	Expected  int64 `json:"expected"`
	Collected int64 `json:"collected"`
	Expenses  int64 `json:"expenses"`
	Net       int64 `json:"net"`
}

// revenueChange is the movement of each figure against the previous window, as
// a percentage. A member is null when the previous window's figure was zero —
// a rise from nothing is a first period, not "+100%" (Phase 10's rule).
type revenueChange struct {
	Expected  *float64 `json:"expected"`
	Collected *float64 `json:"collected"`
	Expenses  *float64 `json:"expenses"`
	Net       *float64 `json:"net"`
}

// revenueBucket is one point of the series.
type revenueBucket struct {
	Start string `json:"start"`
	revenueTotals
}

// revenueTrend reduces the collected series to the one number a card can show:
// the least-squares gradient per bucket.
type revenueTrend struct {
	SlopeCollectedPerBucket float64 `json:"slope_collected_per_bucket"`
}

type revenueSeriesResponse struct {
	Window   reportWindow `json:"window"`
	Previous reportWindow `json:"previous"`
	// Bucket is the granularity actually used, whether the caller asked for it
	// or the resolver chose it.
	Bucket         string          `json:"bucket"`
	Buckets        []revenueBucket `json:"buckets"`
	Totals         revenueTotals   `json:"totals"`
	PreviousTotals revenueTotals   `json:"previous_totals"`
	ChangePct      revenueChange   `json:"change_pct"`
	Trend          revenueTrend    `json:"trend"`
	// CollectionRate is collected / expected as a fraction, null when nothing
	// was expected: a rate against no expectation is undefined, not 0.
	CollectionRate *float64 `json:"collection_rate"`
}

// revenueGroup is one property's row of `group_by=property`.
type revenueGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	revenueTotals
	CollectionRate *float64 `json:"collection_rate"`
}

type revenueGroupsResponse struct {
	Window         reportWindow   `json:"window"`
	Previous       reportWindow   `json:"previous"`
	Groups         []revenueGroup `json:"groups"`
	Totals         revenueTotals  `json:"totals"`
	PreviousTotals revenueTotals  `json:"previous_totals"`
	ChangePct      revenueChange  `json:"change_pct"`
}

// ----------------------------------------------- GET /reports/occupancy --

// occupancyPoint is how full the portfolio was on one day: the units that
// existed, the ones a tenancy covered, and the percentage (0–100, one decimal)
// between them. It is a percentage rather than the `occupancy_rate` fraction of
// the Phase 7 summary because the field says `pct`.
type occupancyPoint struct {
	UnitsTotal    int64   `json:"units_total"`
	UnitsOccupied int64   `json:"units_occupied"`
	OccupancyPct  float64 `json:"occupancy_pct"`
}

type occupancyBucket struct {
	Start string `json:"start"`
	occupancyPoint
}

type occupancyResponse struct {
	Window   reportWindow      `json:"window"`
	Previous reportWindow      `json:"previous"`
	Bucket   string            `json:"bucket"`
	Buckets  []occupancyBucket `json:"buckets"`
	// Current is the most recent real measurement the window contains: today
	// when today falls inside it, otherwise the nearest end of it. A window
	// that has not finished cannot be measured at its last day without
	// counting tenancies that have not happened yet.
	Current occupancyPoint `json:"current"`
}
