package httpserver

// GET /reports/revenue and GET /reports/occupancy (PLAN2 Phase 11).
//
// Both are time series over a window resolved by internal/period, and both are
// built the same way: read the facts at day grain in one query per series, then
// fold those days into the window's buckets in Go.
//
// The folding is deliberately not `date_trunc`. internal/period starts a
// custom window's first bucket on the window's own first day — a range opened
// on the 12th reports from the 12th — and `date_trunc` would align the same
// series to the 1st. Two alignments that disagree by eleven days is a wrong
// chart, not a slow one, so there is exactly one alignment and it lives here.

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/period"
	"tms/backend/internal/report"
	"tms/backend/internal/tz"
	"tms/backend/internal/validate"
)

// ------------------------------------------------------------ plumbing --

// seriesQuery is a validated series request: the window, the granularity, the
// bucket starts to zero-fill, and the optional property filter.
type seriesQuery struct {
	window     period.Window
	bucket     string
	starts     []time.Time
	propertyID pgtype.UUID
}

// parseSeriesQuery validates the parameters every series endpoint shares and
// writes the refusal itself, so the handlers below read as arithmetic.
//
// A window that needs more than 400 buckets is a 422 rather than a 400: the
// request is well-formed and the caller has asked a reasonable question, it is
// the *answer* that will not fit. The client's fix is a coarser `bucket` or a
// shorter window, and the error says so.
func (s *Server) parseSeriesQuery(w http.ResponseWriter, r *http.Request) (seriesQuery, bool) {
	qs := r.URL.Query()
	f := validate.Fields{}

	out := seriesQuery{}
	out.propertyID = optQueryUUID(f, "property_id", qs.Get("property_id"))
	win, ok := reportWindowOf(w, f, qs)
	if !ok {
		return seriesQuery{}, false
	}
	out.window = win

	size, err := period.BucketSize(win, qs.Get("bucket"))
	if err != nil {
		f.Add("bucket", "must be day, week or month")
		badRequest(w, f)
		return seriesQuery{}, false
	}
	out.bucket = size

	if period.Overflows(win, size) {
		httpx.WriteProblemCode(w, http.StatusUnprocessableEntity, "too_many_buckets",
			"window is too long for this bucket size",
			"this window needs more than "+strconv.Itoa(period.MaxBuckets)+
				" buckets; ask for a coarser `bucket` or a shorter window")
		return seriesQuery{}, false
	}
	out.starts = period.Buckets(win, size)
	return out, true
}

// reportWindowOf resolves the window every cadence-driven report shares and
// writes the refusal itself — a 400 for a spelling the API does not know, a 422
// for a window that cannot exist (see windowRefusalStatus).
func reportWindowOf(w http.ResponseWriter, f validate.Fields, qs urlValues) (period.Window, bool) {
	win, status, ok := resolveWindowStatus(f, orMonth(qs.Get("cadence")), qs.Get("anchor"),
		optQueryDate(f, "from", qs.Get("from")), optQueryDate(f, "to", qs.Get("to")))
	if !ok || !f.Empty() {
		refuseWindow(w, status, f)
		return period.Window{}, false
	}
	return win, true
}

// refuseWindow writes the refusal a window resolution earned.
func refuseWindow(w http.ResponseWriter, status int, f validate.Fields) {
	if status == http.StatusUnprocessableEntity {
		httpx.WriteProblemFields(w, http.StatusUnprocessableEntity, "window cannot be resolved",
			"the requested reporting window is not one this API can produce", f)
		return
	}
	badRequest(w, f)
}

// dayAmount is one calendar day's worth of one series, as the day-grain
// queries return it.
type dayAmount struct {
	day    time.Time
	amount int64
}

// revenueRows is the three series of one window, unbucketed.
type revenueRows struct {
	collected []dayAmount
	expected  []dayAmount
	expenses  []dayAmount
}

func (r revenueRows) totals() revenueTotals {
	t := revenueTotals{
		Collected: sumDays(r.collected),
		Expected:  sumDays(r.expected),
		Expenses:  sumDays(r.expenses),
	}
	t.Net = t.Collected - t.Expenses
	return t
}

func sumDays(rows []dayAmount) int64 {
	var out int64
	for _, r := range rows {
		out += r.amount
	}
	return out
}

// revenueWindowRows reads the three series of one half-open window.
//
// `collected` is bounded by instants (a payment is stamped with a moment) and
// the other two by dates (a due date and a date of purchase are already
// calendar days); the window's own EAT midnights serve as both.
func (s *Server) revenueWindowRows(
	ctx context.Context, orgID pgtype.UUID, w period.Window, propertyID pgtype.UUID,
) (revenueRows, error) {
	out := revenueRows{}
	fromDate, toDate := dateParam(w.From), dateParam(w.To)

	collected, err := s.q.RevenueCollectedDaily(ctx, sqlc.RevenueCollectedDailyParams{
		OrgID: orgID, FromTs: db.TS(w.From), ToTs: db.TS(w.To), PropertyID: propertyID,
	})
	if err != nil {
		return out, err
	}
	expected, err := s.q.RevenueExpectedDaily(ctx, sqlc.RevenueExpectedDailyParams{
		OrgID: orgID, FromDate: fromDate, ToDate: toDate, PropertyID: propertyID,
	})
	if err != nil {
		return out, err
	}
	expenses, err := s.q.RevenueExpensesDaily(ctx, sqlc.RevenueExpensesDailyParams{
		OrgID: orgID, FromDate: fromDate, ToDate: toDate, PropertyID: propertyID,
	})
	if err != nil {
		return out, err
	}
	for _, row := range collected {
		out.collected = append(out.collected, dayAmount{day: localDay(row.Day), amount: row.Amount})
	}
	for _, row := range expected {
		out.expected = append(out.expected, dayAmount{day: localDay(row.Day), amount: row.Amount})
	}
	for _, row := range expenses {
		out.expenses = append(out.expenses, dayAmount{day: localDay(row.Day), amount: row.Amount})
	}
	return out, nil
}

// localDay reads a calendar date off the wire as EAT midnight, the same instant
// internal/period's bucket starts carry, so the two can be compared directly.
func localDay(d pgtype.Date) time.Time {
	t := d.Time
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, tz.Zone())
}

// foldDays assigns each day to its bucket: the last start not after it. The
// starts are ascending, so a binary search does it without walking the series.
func foldDays(starts []time.Time, rows []dayAmount, into func(i int, amount int64)) {
	if len(starts) == 0 {
		return
	}
	for _, row := range rows {
		i := sort.Search(len(starts), func(i int) bool { return starts[i].After(row.day) }) - 1
		if i < 0 {
			continue
		}
		into(i, row.amount)
	}
}

// changeOf compares two totals figure by figure.
func changeOf(cur, prev revenueTotals) revenueChange {
	return revenueChange{
		Expected:  report.ChangePct(cur.Expected, prev.Expected),
		Collected: report.ChangePct(cur.Collected, prev.Collected),
		Expenses:  report.ChangePct(cur.Expenses, prev.Expenses),
		Net:       report.ChangePct(cur.Net, prev.Net),
	}
}

// windowDTO is a resolved window on the wire.
func windowDTO(w period.Window) reportWindow {
	return reportWindow{
		From: w.From.Format(dateOnly), To: w.To.Format(dateOnly), Cadence: w.Cadence,
	}
}

// previousDTO is the window before it. Every window internal/period resolves
// carries one; the nil guard is for a window built by hand.
func previousDTO(w period.Window) reportWindow {
	if w.Previous == nil {
		return reportWindow{}
	}
	return reportWindow{
		From: w.Previous.From.Format(dateOnly), To: w.Previous.To.Format(dateOnly),
		Cadence: w.Cadence,
	}
}

// ----------------------------------------------------- GET /reports/revenue --

// handleReportRevenue is Flow 9's money chart: what was owed, what came in,
// what went out and what is left, bucketed over any cadence — or, with
// `group_by=property`, the same four figures one row per property.
func (s *Server) handleReportRevenue(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	if strings.TrimSpace(r.URL.Query().Get("group_by")) != "" {
		s.reportRevenueByProperty(w, r)
		return
	}
	p := auth.MustFromContext(r.Context())
	q, ok := s.parseSeriesQuery(w, r)
	if !ok {
		return
	}
	s.sweepOverdue(r, p.OrgID)

	rows, err := s.revenueWindowRows(r.Context(), p.OrgID, q.window, q.propertyID)
	if err != nil {
		s.serverError(w, r, "report.revenue.series", err)
		return
	}
	prevRows, err := s.revenueWindowRows(r.Context(), p.OrgID, *q.window.Previous, q.propertyID)
	if err != nil {
		s.serverError(w, r, "report.revenue.previous", err)
		return
	}

	buckets := make([]revenueBucket, len(q.starts))
	for i, start := range q.starts {
		buckets[i].Start = start.Format(dateOnly)
	}
	foldDays(q.starts, rows.collected, func(i int, v int64) { buckets[i].Collected += v })
	foldDays(q.starts, rows.expected, func(i int, v int64) { buckets[i].Expected += v })
	foldDays(q.starts, rows.expenses, func(i int, v int64) { buckets[i].Expenses += v })

	collectedSeries := make([]int64, len(buckets))
	for i := range buckets {
		buckets[i].Net = buckets[i].Collected - buckets[i].Expenses
		collectedSeries[i] = buckets[i].Collected
	}

	totals, prevTotals := rows.totals(), prevRows.totals()
	WriteJSON(w, http.StatusOK, revenueSeriesResponse{
		Window:         windowDTO(q.window),
		Previous:       previousDTO(q.window),
		Bucket:         q.bucket,
		Buckets:        buckets,
		Totals:         totals,
		PreviousTotals: prevTotals,
		ChangePct:      changeOf(totals, prevTotals),
		Trend:          revenueTrend{SlopeCollectedPerBucket: report.Slope(collectedSeries)},
		CollectionRate: report.Rate(totals.Collected, totals.Expected),
	})
}

// reportRevenueByProperty is `group_by=property`: one row per live property,
// zero-filled from the property list rather than from the money, so a block
// that earned nothing this month is a row of zeroes and keeps its place in the
// legend. Sorted by net descending — the landlord's own ranking.
func (s *Server) reportRevenueByProperty(w http.ResponseWriter, r *http.Request) {
	p := auth.MustFromContext(r.Context())
	f := validate.Fields{}
	if v := strings.TrimSpace(r.URL.Query().Get("group_by")); strings.ToLower(v) != "property" {
		f.Add("group_by", "must be `property`")
		badRequest(w, f)
		return
	}
	q, ok := s.parseSeriesQuery(w, r)
	if !ok {
		return
	}
	s.sweepOverdue(r, p.OrgID)
	ctx := r.Context()
	win := q.window

	props, err := s.q.ReportPropertyNames(ctx, sqlc.ReportPropertyNamesParams{
		OrgID: p.OrgID, PropertyID: q.propertyID,
	})
	if err != nil {
		s.serverError(w, r, "report.revenue.properties", err)
		return
	}
	collected, err := s.q.RevenueCollectedByProperty(ctx, sqlc.RevenueCollectedByPropertyParams{
		OrgID: p.OrgID, FromTs: db.TS(win.From), ToTs: db.TS(win.To), PropertyID: q.propertyID,
	})
	if err != nil {
		s.serverError(w, r, "report.revenue.by_property.collected", err)
		return
	}
	expected, err := s.q.RevenueExpectedByProperty(ctx, sqlc.RevenueExpectedByPropertyParams{
		OrgID: p.OrgID, FromDate: dateParam(win.From), ToDate: dateParam(win.To), PropertyID: q.propertyID,
	})
	if err != nil {
		s.serverError(w, r, "report.revenue.by_property.expected", err)
		return
	}
	expenses, err := s.q.RevenueExpensesByProperty(ctx, sqlc.RevenueExpensesByPropertyParams{
		OrgID: p.OrgID, FromDate: dateParam(win.From), ToDate: dateParam(win.To), PropertyID: q.propertyID,
	})
	if err != nil {
		s.serverError(w, r, "report.revenue.by_property.expenses", err)
		return
	}
	prevRows, err := s.revenueWindowRows(ctx, p.OrgID, *win.Previous, q.propertyID)
	if err != nil {
		s.serverError(w, r, "report.revenue.by_property.previous", err)
		return
	}

	index := make(map[string]int, len(props))
	groups := make([]revenueGroup, 0, len(props))
	for _, pr := range props {
		id := db.UUIDString(pr.ID)
		index[id] = len(groups)
		groups = append(groups, revenueGroup{ID: id, Name: pr.Name})
	}
	add := func(id pgtype.UUID, apply func(*revenueGroup)) {
		if i, ok := index[db.UUIDString(id)]; ok {
			apply(&groups[i])
		}
	}
	for _, row := range collected {
		add(row.PropertyID, func(g *revenueGroup) { g.Collected += row.Amount })
	}
	for _, row := range expected {
		add(row.PropertyID, func(g *revenueGroup) { g.Expected += row.Amount })
	}
	for _, row := range expenses {
		add(row.PropertyID, func(g *revenueGroup) { g.Expenses += row.Amount })
	}

	var totals revenueTotals
	for i := range groups {
		g := &groups[i]
		g.Net = g.Collected - g.Expenses
		g.CollectionRate = report.Rate(g.Collected, g.Expected)
		totals.Collected += g.Collected
		totals.Expected += g.Expected
		totals.Expenses += g.Expenses
	}
	totals.Net = totals.Collected - totals.Expenses
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Net != groups[j].Net {
			return groups[i].Net > groups[j].Net
		}
		return groups[i].Name < groups[j].Name
	})

	prevTotals := prevRows.totals()
	WriteJSON(w, http.StatusOK, revenueGroupsResponse{
		Window:         windowDTO(win),
		Previous:       previousDTO(win),
		Groups:         groups,
		Totals:         totals,
		PreviousTotals: prevTotals,
		ChangePct:      changeOf(totals, prevTotals),
	})
}

// --------------------------------------------------- GET /reports/occupancy --

// handleReportOccupancy answers "how full was the portfolio" at the end of
// every bucket in the window.
//
// A day's occupancy is a question about spans, not about `units.status`: the
// status column says what is true now, and a series over the last twelve months
// asks what was true then. So the tenancies are read as half-open date spans
// and counted per measured day, and the denominator counts the units that
// existed on that day rather than the ones that exist today.
func (s *Server) handleReportOccupancy(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	q, ok := s.parseSeriesQuery(w, r)
	if !ok {
		return
	}

	units, err := s.q.OccupancyUnitDates(r.Context(), sqlc.OccupancyUnitDatesParams{
		OrgID: p.OrgID, PropertyID: q.propertyID,
	})
	if err != nil {
		s.serverError(w, r, "report.occupancy.units", err)
		return
	}
	spans, err := s.q.OccupancyContractSpans(r.Context(), sqlc.OccupancyContractSpansParams{
		OrgID: p.OrgID, ToDate: dateParam(q.window.To), PropertyID: q.propertyID,
	})
	if err != nil {
		s.serverError(w, r, "report.occupancy.spans", err)
		return
	}

	live := make(map[string]time.Time, len(units))
	for _, u := range units {
		live[db.UUIDString(u.ID)] = localDay(u.CreatedOn)
	}
	type span struct {
		unit       string
		start, end time.Time
	}
	covers := make([]span, 0, len(spans))
	for _, sp := range spans {
		id := db.UUIDString(sp.UnitID)
		// A tenancy on a unit that has since been deleted is not counted: the
		// denominator cannot see that unit either, and an occupancy above 100%
		// is worse than a missing point.
		if _, ok := live[id]; !ok {
			continue
		}
		covers = append(covers, span{unit: id, start: localDay(sp.StartDate), end: localDay(sp.EndDate)})
	}

	measure := func(d time.Time) occupancyPoint {
		var total int64
		for _, created := range live {
			if !created.After(d) {
				total++
			}
		}
		occupied := make(map[string]struct{}, len(covers))
		for _, c := range covers {
			if !c.start.After(d) && d.Before(c.end) {
				occupied[c.unit] = struct{}{}
			}
		}
		pt := occupancyPoint{UnitsTotal: total, UnitsOccupied: int64(len(occupied))}
		if total > 0 {
			pt.OccupancyPct = round1(float64(pt.UnitsOccupied) / float64(total) * 100)
		}
		return pt
	}

	buckets := make([]occupancyBucket, 0, len(q.starts))
	for i, start := range q.starts {
		// The day measured is the last one *inside* the bucket: a month's point
		// is how full the portfolio was on the 31st, not on the 1st of the
		// month after.
		end := q.window.To
		if i+1 < len(q.starts) {
			end = q.starts[i+1]
		}
		buckets = append(buckets, occupancyBucket{
			Start: start.Format(dateOnly), occupancyPoint: measure(end.AddDate(0, 0, -1)),
		})
	}

	// `current` is today when today is inside the window, and otherwise the
	// nearest end of it: a window that has not finished must not be measured at
	// a day that has not happened.
	today := time.Now().In(tz.Zone())
	at := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, tz.Zone())
	if last := q.window.To.AddDate(0, 0, -1); at.After(last) {
		at = last
	}
	if at.Before(q.window.From) {
		at = q.window.From
	}

	WriteJSON(w, http.StatusOK, occupancyResponse{
		Window:   windowDTO(q.window),
		Previous: previousDTO(q.window),
		Bucket:   q.bucket,
		Buckets:  buckets,
		Current:  measure(at),
	})
}

// round1 rounds to one decimal place, the precision a percentage is read at.
func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }
