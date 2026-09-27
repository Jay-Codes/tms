package httpserver

// Phase 28 — projections, break-even and ROI (PLAN2 Phase 28).
//
//	POST   /reports/projection                  the forecast, per property or portfolio
//	GET    /reports/projection/scenarios        the org's saved "what if" sets
//	POST   /reports/projection/scenarios
//	DELETE /reports/projection/scenarios/{id}
//
// The handler only gathers facts; every figure is computed by
// internal/report/projection.go. The facts use the cash definitions of
// GET /reports/revenue — collected is non-reversed payments by `paid_at` less
// rent refunds by `refunded_at` (phase22_cash.go), expected is non-waived
// schedules by `due_date`, expenses are recorded ones by `incurred_on` — so a
// projection's baseline agrees with the revenue chart over the same months.

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/report"
	"tms/backend/internal/tz"
	"tms/backend/internal/validate"
)

// Scenario bounds (API.md Phase 28).
const (
	projectionChangeMin    = -100.0 // a rent or cost change cannot go below nothing
	projectionChangeMax    = 500.0
	projectionScenarioMax  = 50
	projectionScenarioName = 60
	earliestPurchaseYear   = 1900
)

// ------------------------------------------------ patch-field helpers --

// jsonMoneyPtr decodes an optional-and-nullable whole-shilling patch field.
// An empty raw value is an absent field; `null` clears it.
func jsonMoneyPtr(f validate.Fields, field string, raw json.RawMessage) (set bool, val *int64) {
	if len(raw) == 0 {
		return false, nil
	}
	var v *int64
	if err := json.Unmarshal(raw, &v); err != nil {
		f.Add(field, "must be a whole number of shillings or null")
		return false, nil
	}
	if v != nil {
		checkAmount(f, field, *v)
	}
	return true, v
}

// jsonPastDatePtr decodes an optional-and-nullable YYYY-MM-DD patch field that
// may not lie in the future: a purchase that has not happened yet has no cash
// history to break even against.
func jsonPastDatePtr(f validate.Fields, field string, raw json.RawMessage) (set bool, val pgtype.Date) {
	if len(raw) == 0 {
		return false, pgtype.Date{}
	}
	var v *string
	if err := json.Unmarshal(raw, &v); err != nil {
		f.Add(field, "must be a date (YYYY-MM-DD) or null")
		return false, pgtype.Date{}
	}
	if v == nil || strings.TrimSpace(*v) == "" {
		return true, pgtype.Date{}
	}
	t, err := time.Parse(dateLayout, strings.TrimSpace(*v))
	if err != nil {
		f.Add(field, "must be a date (YYYY-MM-DD) or null")
		return false, pgtype.Date{}
	}
	if t.Year() < earliestPurchaseYear || t.After(todayDate().Time) {
		f.Add(field, "must be a date between 1900 and today")
		return false, pgtype.Date{}
	}
	return true, pgtype.Date{Time: t, Valid: true}
}

// --------------------------------------------------- scenario params --

// projectionParams is the scenario part of a request, shared by the forecast
// and by a saved scenario.
type projectionParams struct {
	HorizonMonths     *int     `json:"horizon_months"`
	RentChangePct     *float64 `json:"rent_change_pct"`
	OccupancyPct      *float64 `json:"occupancy_pct"`
	CollectionRatePct *float64 `json:"collection_rate_pct"`
	ExpenseChangePct  *float64 `json:"expense_change_pct"`
}

// scenario validates the parameters and applies the defaults.
func (in projectionParams) scenario(f validate.Fields) report.ProjectionScenario {
	out := report.ProjectionScenario{HorizonMonths: report.ProjectionHorizonDefault}
	if in.HorizonMonths != nil {
		if *in.HorizonMonths < 1 || *in.HorizonMonths > report.ProjectionHorizonMax {
			f.Add("horizon_months", "must be between 1 and 120")
		}
		out.HorizonMonths = *in.HorizonMonths
	}
	change := func(field string, v *float64) float64 {
		if v == nil {
			return 0
		}
		if *v < projectionChangeMin || *v > projectionChangeMax {
			f.Add(field, "must be between -100 and 500")
		}
		return *v
	}
	rate := func(field string, v *float64) *float64 {
		if v != nil && (*v < 0 || *v > 100) {
			f.Add(field, "must be between 0 and 100, or null for the trailing figure")
		}
		return v
	}
	out.RentChangePct = change("rent_change_pct", in.RentChangePct)
	out.ExpenseChangePct = change("expense_change_pct", in.ExpenseChangePct)
	out.OccupancyPct = rate("occupancy_pct", in.OccupancyPct)
	out.CollectionRatePct = rate("collection_rate_pct", in.CollectionRatePct)
	return out
}

// ------------------------------------------ POST /reports/projection --

// projectionPropertyRow is one property's headline in the response.
type projectionPropertyRow struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	HasPurchasePrice   bool     `json:"has_purchase_price"`
	ProjectedAnnualNet int64    `json:"projected_annual_net"`
	ROIProjectedPct    *float64 `json:"roi_projected_pct"`
	YieldPct           *float64 `json:"yield_pct"`
	PaybackYears       *float64 `json:"payback_years"`
	BreakEvenMonth     *string  `json:"break_even_month"`
	BreakEvenStatus    string   `json:"break_even_status"`
}

type projectionResponse struct {
	Scope      string    `json:"scope"` // property | portfolio
	Property   *namedRef `json:"property"`
	StartMonth string    `json:"start_month"`
	report.ProjectionResult
	Properties []projectionPropertyRow `json:"properties"`
}

func (s *Server) handleReportProjection(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	var body struct {
		PropertyID *string `json:"property_id"`
		projectionParams
	}
	if !DecodeJSONOptional(w, r, &body) {
		return
	}
	f := validate.Fields{}
	sc := body.scenario(f)
	var propertyID pgtype.UUID
	if body.PropertyID != nil && strings.TrimSpace(*body.PropertyID) != "" {
		id, err := db.ParseUUID(strings.TrimSpace(*body.PropertyID))
		if err != nil {
			f.Add("property_id", "must be a property id")
		}
		propertyID = id
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	start := projectionStart(time.Now())
	props, err := s.projectionInputs(r, p.OrgID, propertyID, start, sc.HorizonMonths)
	if err != nil {
		s.serverError(w, r, "report.projection", err)
		return
	}
	if propertyID.Valid && len(props) == 0 {
		notFoundProperty(w)
		return
	}

	resp := projectionResponse{Scope: "portfolio", StartMonth: start.Format("2006-01")}
	var parts []report.ProjectionResult
	if propertyID.Valid {
		one := report.ProjectProperty(start, sc, props[0])
		resp.Scope = "property"
		resp.Property = &namedRef{ID: one.ID, Name: one.Name}
		resp.ProjectionResult = one
		parts = []report.ProjectionResult{one}
	} else {
		resp.ProjectionResult, parts = report.ProjectPortfolio(start, sc, props)
	}
	resp.Properties = make([]projectionPropertyRow, 0, len(parts))
	for _, part := range parts {
		inv := part.Investment
		resp.Properties = append(resp.Properties, projectionPropertyRow{
			ID: part.ID, Name: part.Name, HasPurchasePrice: inv.PurchasePrice != nil,
			ProjectedAnnualNet: inv.ProjectedAnnualNet, ROIProjectedPct: inv.ROIProjectedPct,
			YieldPct: inv.YieldPct, PaybackYears: inv.PaybackYears,
			BreakEvenMonth: inv.BreakEvenMonth, BreakEvenStatus: inv.BreakEvenStatus,
		})
	}
	WriteJSON(w, http.StatusOK, resp)
}

// projectionStart is the 1st of the current month on the Dar es Salaam wall
// clock, as a UTC date like the engine's other month keys. The trailing window
// is the twelve whole months before it; the forecast starts with it.
func projectionStart(now time.Time) time.Time {
	local := now.In(tz.Zone())
	return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// monthKey snaps a date to the 1st of its month (UTC).
func monthKey(d time.Time) time.Time { return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC) }

// monthIndex is how many months `d` lies after `start`.
func monthIndex(start, d time.Time) int {
	return (d.Year()-start.Year())*12 + int(d.Month()) - int(start.Month())
}

// projectionFacts is one property's facts while they are being gathered.
type projectionFacts struct {
	in         report.ProjectionProperty
	created    time.Time
	collected  map[time.Time]int64 // net of refunds, whole history
	running    map[time.Time]int64 // whole history
	activity   time.Time           // earliest month anything happened
	categories map[string]*report.CategoryAmount
	unitIDs    map[string]time.Time // unit → created on
}

// projectionInputs reads every fact the engine needs, per property.
func (s *Server) projectionInputs(
	r *http.Request, orgID, propertyID pgtype.UUID, start time.Time, horizon int,
) ([]report.ProjectionProperty, error) {
	ctx := r.Context()
	historyFrom := start.AddDate(0, -report.ProjectionHistoryMonths, 0)
	end := start.AddDate(0, horizon, 0)
	startInstant := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, tz.Zone())

	props, err := s.q.ProjectionProperties(ctx, sqlc.ProjectionPropertiesParams{OrgID: orgID, PropertyID: propertyID})
	if err != nil || len(props) == 0 {
		return nil, err
	}
	facts := make(map[string]*projectionFacts, len(props))
	order := make([]string, 0, len(props))
	for _, pr := range props {
		id := db.UUIDString(pr.ID)
		created := monthKey(pr.CreatedOn.Time)
		pf := &projectionFacts{
			in: report.ProjectionProperty{
				ID: id, Name: pr.Name, PurchasePrice: pr.PurchasePrice, CurrentValue: pr.CurrentValue,
				Scheduled: make([]int64, horizon),
			},
			created: created, activity: created,
			collected: map[time.Time]int64{}, running: map[time.Time]int64{},
			categories: map[string]*report.CategoryAmount{}, unitIDs: map[string]time.Time{},
		}
		if pr.PurchaseDate.Valid {
			d := pr.PurchaseDate.Time
			pf.in.PurchaseDate = &d
		}
		facts[id] = pf
		order = append(order, id)
	}
	touch := func(pf *projectionFacts, m time.Time) {
		if m.Before(pf.activity) {
			pf.activity = m
		}
	}

	collected, err := s.q.ProjectionCollectedMonthly(ctx, sqlc.ProjectionCollectedMonthlyParams{
		OrgID: orgID, ToTs: db.TS(startInstant), PropertyID: propertyID,
	})
	if err != nil {
		return nil, err
	}
	for _, row := range collected {
		if pf := facts[db.UUIDString(row.PropertyID)]; pf != nil {
			m := monthKey(row.Month.Time)
			pf.collected[m] += row.Amount
			touch(pf, m)
		}
	}
	refunded, err := s.q.ProjectionRefundedMonthly(ctx, sqlc.ProjectionRefundedMonthlyParams{
		OrgID: orgID, ToTs: db.TS(startInstant), PropertyID: propertyID,
	})
	if err != nil {
		return nil, err
	}
	for _, row := range refunded {
		if pf := facts[db.UUIDString(row.PropertyID)]; pf != nil {
			pf.collected[monthKey(row.Month.Time)] -= row.Amount
		}
	}
	expected, err := s.q.ProjectionExpectedMonthly(ctx, sqlc.ProjectionExpectedMonthlyParams{
		OrgID: orgID, FromDate: dateParam(historyFrom), ToDate: dateParam(start), PropertyID: propertyID,
	})
	if err != nil {
		return nil, err
	}
	for _, row := range expected {
		if pf := facts[db.UUIDString(row.PropertyID)]; pf != nil {
			pf.in.TrailingExpected += row.Amount
			touch(pf, monthKey(row.Month.Time))
		}
	}
	expenses, err := s.q.ProjectionExpensesMonthly(ctx, sqlc.ProjectionExpensesMonthlyParams{
		OrgID: orgID, ToDate: dateParam(start), PropertyID: propertyID,
	})
	if err != nil {
		return nil, err
	}
	for _, row := range expenses {
		pf := facts[db.UUIDString(row.PropertyID)]
		if pf == nil {
			continue
		}
		m := monthKey(row.Month.Time)
		touch(pf, m)
		inWindow := !m.Before(historyFrom)
		if row.IsCapital {
			pf.in.CapitalToDate += row.Amount
			if inWindow {
				pf.in.TrailingCapital += row.Amount
			}
			continue
		}
		pf.running[m] += row.Amount
		if inWindow {
			key := db.UUIDString(row.CategoryID)
			c := pf.categories[key]
			if c == nil {
				c = &report.CategoryAmount{ID: key, Name: row.CategoryName}
				pf.categories[key] = c
			}
			c.Amount += row.Amount
		}
	}
	scheduled, err := s.q.ProjectionScheduledDaily(ctx, sqlc.ProjectionScheduledDailyParams{
		OrgID: orgID, FromDate: dateParam(start), ToDate: dateParam(end), PropertyID: propertyID,
	})
	if err != nil {
		return nil, err
	}
	for _, row := range scheduled {
		pf := facts[db.UUIDString(row.PropertyID)]
		if pf == nil {
			continue
		}
		if i := monthIndex(start, row.Day.Time); i >= 0 && i < horizon {
			pf.in.Scheduled[i] += row.Amount
		}
	}
	units, err := s.q.ProjectionUnits(ctx, sqlc.ProjectionUnitsParams{OrgID: orgID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	for _, u := range units {
		pf := facts[db.UUIDString(u.PropertyID)]
		if pf == nil {
			continue
		}
		uid := db.UUIDString(u.ID)
		pf.unitIDs[uid] = u.CreatedOn.Time
		pf.in.Units = append(pf.in.Units, projectionUnit(start, u))
	}
	spans, err := s.q.OccupancyContractSpans(ctx, sqlc.OccupancyContractSpansParams{
		OrgID: orgID, ToDate: dateParam(start), PropertyID: propertyID,
	})
	if err != nil {
		return nil, err
	}

	out := make([]report.ProjectionProperty, 0, len(order))
	for _, id := range order {
		pf := facts[id]
		first := pf.activity
		if first.Before(historyFrom) {
			first = historyFrom
		}
		pf.in.HistoryMonths = monthIndex(first, start)
		if pf.in.HistoryMonths < 1 {
			pf.in.HistoryMonths = 1
		}
		// Past net, month by month, over the whole history.
		months := make(map[time.Time]struct{}, len(pf.collected)+len(pf.running))
		for m := range pf.collected {
			months[m] = struct{}{}
		}
		for m := range pf.running {
			months[m] = struct{}{}
		}
		for m := range months {
			pf.in.PastNet = append(pf.in.PastNet, report.MonthAmount{Month: m, Amount: pf.collected[m] - pf.running[m]})
			if !m.Before(historyFrom) {
				pf.in.TrailingCollected += pf.collected[m]
			}
		}
		sort.Slice(pf.in.PastNet, func(i, j int) bool { return pf.in.PastNet[i].Month.Before(pf.in.PastNet[j].Month) })
		for _, c := range pf.categories {
			pf.in.TrailingRunning = append(pf.in.TrailingRunning, *c)
		}
		sort.Slice(pf.in.TrailingRunning, func(i, j int) bool {
			a, b := pf.in.TrailingRunning[i], pf.in.TrailingRunning[j]
			if a.Name != b.Name {
				return a.Name < b.Name
			}
			return a.ID < b.ID
		})
		pf.in.TrailingOccupiedUnitMonths, pf.in.TrailingUnitMonths =
			trailingOccupancy(start, pf.in.HistoryMonths, pf.unitIDs, spans)
		out = append(out, pf.in)
	}
	return out, nil
}

// projectionUnit is one unit as the engine sees it: its market rent per month
// (the current price, else the last tenancy's rent, normalised to 30 days)
// and the first projection month it is on the open market.
func projectionUnit(start time.Time, u sqlc.ProjectionUnitsRow) report.ProjectionUnit {
	out := report.ProjectionUnit{Lettable: u.Status == "vacant" || u.Status == "occupied"}
	switch {
	case u.PriceAmount > 0 && u.PricePeriodDays > 0:
		out.MonthlyRent = int64(math.Round(float64(u.PriceAmount) * 30 / float64(u.PricePeriodDays)))
	case u.LastRentAmount > 0 && u.LastRentPeriodDays > 0:
		out.MonthlyRent = int64(math.Round(float64(u.LastRentAmount) * 30 / float64(u.LastRentPeriodDays)))
	}
	if u.RunningEnd.Valid {
		e := u.RunningEnd.Time
		i := monthIndex(start, e)
		if e.Day() > 1 {
			i++ // the month the tenancy ends in is still its own
		}
		if i > 0 {
			out.FreeFrom = i
		}
	}
	return out
}

// trailingOccupancy measures the property's units at the last day of each of
// its history months, the way GET /reports/occupancy measures a month: units
// that existed that day, and how many of them a tenancy covered.
func trailingOccupancy(
	start time.Time, months int, units map[string]time.Time, spans []sqlc.OccupancyContractSpansRow,
) (occupied, total int64) {
	for k := months; k >= 1; k-- {
		day := start.AddDate(0, -k+1, -1) // last day of the month k months back
		covered := map[string]struct{}{}
		for _, sp := range spans {
			uid := db.UUIDString(sp.UnitID)
			if _, ours := units[uid]; !ours {
				continue
			}
			if !sp.StartDate.Time.After(day) && day.Before(sp.EndDate.Time) {
				covered[uid] = struct{}{}
			}
		}
		for uid, created := range units {
			if created.After(day) {
				continue
			}
			total++
			if _, ok := covered[uid]; ok {
				occupied++
			}
		}
	}
	return occupied, total
}

// ------------------------------------------------ saved scenarios --

type projectionScenarioResponse struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	HorizonMonths     int       `json:"horizon_months"`
	RentChangePct     float64   `json:"rent_change_pct"`
	OccupancyPct      *float64  `json:"occupancy_pct"`
	CollectionRatePct *float64  `json:"collection_rate_pct"`
	ExpenseChangePct  float64   `json:"expense_change_pct"`
	CreatedAt         time.Time `json:"created_at"`
}

func toProjectionScenario(r sqlc.ProjectionScenario) projectionScenarioResponse {
	return projectionScenarioResponse{
		ID: db.UUIDString(r.ID), Name: r.Name, HorizonMonths: int(r.HorizonMonths),
		RentChangePct: r.RentChangePct, OccupancyPct: r.OccupancyPct,
		CollectionRatePct: r.CollectionRatePct, ExpenseChangePct: r.ExpenseChangePct,
		CreatedAt: r.CreatedAt.Time,
	}
}

func (s *Server) handleListProjectionScenarios(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	rows, err := s.q.ListProjectionScenarios(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "projection_scenarios.list", err)
		return
	}
	items := make([]projectionScenarioResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, toProjectionScenario(row))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCreateProjectionScenario(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	var body struct {
		Name string `json:"name"`
		projectionParams
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	name := f.MaxLen("name", f.Required("name", body.Name), projectionScenarioName)
	sc := body.scenario(f)
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	n, err := s.q.CountProjectionScenarios(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "projection_scenarios.count", err)
		return
	}
	if n >= projectionScenarioMax {
		httpx.WriteProblemCode(w, http.StatusUnprocessableEntity, "too_many_scenarios",
			"too many saved scenarios", "an organisation keeps at most 50 saved scenarios; delete one first")
		return
	}

	var created sqlc.ProjectionScenario
	err = s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		created, err = q.CreateProjectionScenario(r.Context(), sqlc.CreateProjectionScenarioParams{
			OrgID: p.OrgID, Name: name, HorizonMonths: int32(sc.HorizonMonths), //nolint:gosec // bounded 1–120
			RentChangePct: sc.RentChangePct, OccupancyPct: sc.OccupancyPct,
			CollectionRatePct: sc.CollectionRatePct, ExpenseChangePct: sc.ExpenseChangePct,
			CreatedByUserID: p.UserID,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionProjectionScenarioCreate,
			EntityType:  audit.EntityProjectionScenario,
			EntityID:    db.UUIDString(created.ID),
			After:       toProjectionScenario(created),
		})
	})
	if isUnique(err) {
		conflictCode(w, "scenario_exists", "duplicate scenario",
			"this organisation already has a saved scenario with that name")
		return
	}
	if err != nil {
		s.serverError(w, r, "projection_scenarios.create.tx", err)
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"scenario": toProjectionScenario(created)})
}

func (s *Server) handleDeleteProjectionScenario(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	notFound := func() { httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such scenario") }
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFound()
		return
	}
	err = s.inTx(r.Context(), func(q *sqlc.Queries) error {
		gone, err := q.DeleteProjectionScenario(r.Context(), sqlc.DeleteProjectionScenarioParams{OrgID: p.OrgID, ID: id})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionProjectionScenarioDelete,
			EntityType:  audit.EntityProjectionScenario,
			EntityID:    db.UUIDString(id),
			Before:      toProjectionScenario(gone),
		})
	})
	if isNoRows(err) {
		notFound()
		return
	}
	if err != nil {
		s.serverError(w, r, "projection_scenarios.delete.tx", err)
		return
	}
	NoContent(w)
}
