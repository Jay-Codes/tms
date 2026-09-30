package report

// Phase 28 — projections, break-even and ROI (PLAN2 Phase 28, reworked
// 27 Sep 2026 after the client walk-through).
//
// This file is the whole of the arithmetic. It reads no database and knows no
// HTTP: the handler gathers the facts (the trailing twelve months of cash and
// expenses, the tenancies' future schedules, each unit's price, the costs a
// landlord has logged) and this turns them into a monthly forecast and the
// return figures. Keeping it pure is what lets every figure be checked against
// a hand-computed fixture, and CLAUDE.md keeps it out of the frontend: the
// screen sends scenario parameters and draws what comes back.
//
// The model, in one paragraph. A month's projected income is the rent already
// scheduled on signed, running contracts plus, for every unit the scenario
// assumes let, its own price × (1 + rent change) from the month it is free of
// its contract; the sum is then multiplied by the collection rate. Which units
// are assumed let is the basis: none (signed contracts only), the ones the
// landlord picked, or every lettable unit (best case) — there is no occupancy
// percentage to guess. A month's running expenses are the landlord's own
// monthly estimate, else the trailing monthly average of the non-capital
// categories × (1 + expense change). The return is measured against the sum
// of everything spent, because that is what the rent book records: every
// expense logged, running and capital, plus the purchase price when a landlord
// entered one. Break-even is the month cumulative income first covers
// cumulative spend; ROI is annual net ÷ that sum.

import (
	"math"
	"sort"
	"time"
)

// Projection bounds (API.md Phase 28).
const (
	ProjectionHorizonDefault = 24
	ProjectionHorizonMax     = 120
	// ProjectionHistoryMonths is the trailing window every default is read
	// from: twelve whole calendar months before the projection starts.
	ProjectionHistoryMonths = 12
)

// Bases: which units the forecast assumes let beyond the signed contracts.
const (
	BasisContracts = "contracts" // signed contracts only; nothing else is let
	BasisSelected  = "selected"  // signed contracts plus the units picked
	BasisBestCase  = "best_case" // every lettable unit let whenever no contract covers it
)

// ValidBasis reports whether b is one of the three bases.
func ValidBasis(b string) bool {
	return b == BasisContracts || b == BasisSelected || b == BasisBestCase
}

// Break-even outcomes. Exactly one applies to a scope.
const (
	BreakEvenReached       = "reached"        // income has already covered the spend
	BreakEvenProjected     = "projected"      // happens inside the horizon
	BreakEvenBeyondHorizon = "beyond_horizon" // profitable, but not within the horizon
	BreakEvenNotProfitable = "not_profitable" // the projected net never pays it back
	BreakEvenNoCosts       = "no_costs"       // nothing spent yet, nothing to break even on
)

// Where an applied rate came from.
const (
	SourceScenario = "scenario" // the caller set it
	SourceTrailing = "trailing" // read from the trailing twelve months
	SourceDefault  = "default"  // no history to read: 100%
)

// ProjectionScenario is what the landlord can move. A nil rate means "what the
// last twelve months say".
type ProjectionScenario struct {
	HorizonMonths int
	// Basis is one of the Basis* constants; empty means BasisContracts.
	Basis string
	// UnitIDs are the units assumed let under BasisSelected.
	UnitIDs           []string
	RentChangePct     float64
	CollectionRatePct *float64
	ExpenseChangePct  float64
	// MonthlyExpenses is the landlord's own estimate of running costs a
	// month. Nil means the trailing monthly average × (1 + ExpenseChangePct);
	// set, it is used as it is and ExpenseChangePct does not apply.
	MonthlyExpenses *int64
	// FromPurchase drops every recorded month before a property's purchase
	// date — income and expenses alike. Off (the default), everything ever
	// logged counts: the return is measured against total expenditure.
	FromPurchase bool
	// IncludeFutureExpenses adds the coming year's projected running costs to
	// the spend ROI is measured against.
	IncludeFutureExpenses bool
}

// CategoryAmount is one expense category's total over the trailing window.
type CategoryAmount struct {
	ID     string
	Name   string
	Amount int64
}

// PastMonth is one recorded calendar month: the cash collected (net of
// refunds) and what was spent, running and capital. Month is the 1st.
type PastMonth struct {
	Month   time.Time
	Income  int64
	Running int64
	Capital int64
}

// Spent is everything paid out that month.
func (m PastMonth) Spent() int64 { return m.Running + m.Capital }

// ProjectionUnit is one unit as the forecast sees it.
type ProjectionUnit struct {
	ID     string
	Name   string
	Status string
	// MonthlyRent is the unit's own price per month (its current price plan,
	// else the last tenancy's rent), normalised to 30 days.
	MonthlyRent int64
	// FreeFrom is the first projection month the unit is not under a running
	// contract: 0 for a vacant unit, the month after its tenancy ends
	// otherwise. Before it, the unit's income is its scheduled rent.
	FreeFrom int
	// LetUntil is the running contract's end date (YYYY-MM-DD), if any.
	LetUntil *string
	// Lettable is false for a unit under maintenance or unlisted: best case
	// leaves it out, though a landlord may still pick it by hand.
	Lettable bool
}

// ProjectionProperty is everything the forecast knows about one property.
type ProjectionProperty struct {
	ID            string
	Name          string
	PurchasePrice *int64
	PurchaseDate  *time.Time
	CurrentValue  *int64

	// HistoryMonths is how many months of the trailing window the property
	// has been in the book (1–12); averages divide by it, so a block added in
	// June is not averaged as if it had earned nothing since last October.
	HistoryMonths     int
	TrailingCollected int64            // cash collected, net of refunds
	TrailingExpected  int64            // what fell due
	TrailingRunning   []CategoryAmount // non-capital expenses per category
	TrailingCapital   int64            // capital spend inside the window

	// Past is every recorded month before the projection starts, ascending.
	// Months before the purchase month are ignored.
	Past []PastMonth
	// Scheduled is the rent already scheduled on running tenancies, per
	// projection month.
	Scheduled []int64
	Units     []ProjectionUnit
}

// ------------------------------------------------------------- output --

// ProjectionMonth is one row of the forecast.
type ProjectionMonth struct {
	Month           string `json:"month"`
	Income          int64  `json:"income"`
	RunningExpenses int64  `json:"running_expenses"`
	Net             int64  `json:"net"`
	// CumulativeIncome and CumulativeSpent run from the purchase (or the
	// start of the book) to the end of this month; Cumulative is their
	// difference, and break-even is where it turns positive.
	CumulativeIncome int64 `json:"cumulative_income"`
	CumulativeSpent  int64 `json:"cumulative_spent"`
	Cumulative       int64 `json:"cumulative"`
}

// ProjectionTotals adds the forecast up over the horizon.
type ProjectionTotals struct {
	Income          int64 `json:"income"`
	RunningExpenses int64 `json:"running_expenses"`
	Net             int64 `json:"net"`
}

// CategoryMonthly is one running-cost category's trailing monthly average.
type CategoryMonthly struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Monthly int64  `json:"monthly"`
}

// ProjectionBaseline is what the last twelve months said, before the scenario.
type ProjectionBaseline struct {
	HistoryFrom       string   `json:"history_from"` // inclusive
	HistoryTo         string   `json:"history_to"`   // exclusive
	HistoryMonths     int      `json:"history_months"`
	Collected         int64    `json:"collected"`
	Expected          int64    `json:"expected"`
	CollectionRatePct *float64 `json:"collection_rate_pct"`
	RunningExpenses   int64    `json:"running_expenses"`
	CapitalExpenses   int64    `json:"capital_expenses"`
	// RunningMonthly is the trailing monthly average of running costs.
	RunningMonthly int64             `json:"running_expenses_monthly"`
	Categories     []CategoryMonthly `json:"categories"`
	Net            int64             `json:"net"`
	UnitsTotal     int               `json:"units_total"`
	UnitsLet       int               `json:"units_let"`
	UnitsOpen      int               `json:"units_open"`
	// UnitsAssumed are the units the basis assumes let beyond their contracts,
	// and AssumedRentMonthly their prices a month before the rent change.
	UnitsAssumed       int   `json:"units_assumed"`
	AssumedRentMonthly int64 `json:"assumed_rent_monthly"`
}

// ProjectionApplied is the scenario as it was actually applied.
type ProjectionApplied struct {
	HorizonMonths        int      `json:"horizon_months"`
	Basis                string   `json:"basis"`
	UnitIDs              []string `json:"unit_ids"`
	RentChangePct        float64  `json:"rent_change_pct"`
	CollectionRatePct    float64  `json:"collection_rate_pct"`
	CollectionRateSource string   `json:"collection_rate_source"`
	ExpenseChangePct     float64  `json:"expense_change_pct"`
	// MonthlyExpenses is the running cost a month the forecast used, and
	// MonthlyExpensesSource where it came from: scenario | trailing.
	MonthlyExpenses       int64  `json:"monthly_expenses"`
	MonthlyExpensesSource string `json:"monthly_expenses_source"`
	FromPurchase          bool   `json:"from_purchase"`
	IncludeFutureExpenses bool   `json:"include_future_expenses"`
}

// ProjectionUnitRow is one unit in the picker: what it rents for, whether a
// contract holds it and until when, and whether this forecast assumes it let.
type ProjectionUnitRow struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	PropertyID   string  `json:"property_id"`
	PropertyName string  `json:"property_name"`
	Status       string  `json:"status"`
	MonthlyRent  int64   `json:"monthly_rent"`
	LetUntil     *string `json:"let_until"`
	Lettable     bool    `json:"lettable"`
	Assumed      bool    `json:"assumed"`
}

// ProjectionInvestment is the break-even and return block, measured against
// what has been spent.
type ProjectionInvestment struct {
	// PurchasePrice counts as spend when entered; it is optional.
	PurchasePrice *int64 `json:"purchase_price"`
	CapitalSpend  int64  `json:"capital_spend"`
	RunningSpend  int64  `json:"running_spend"`
	// SpentToDate is running + capital + purchase price up to the start of
	// the projection: every expense ever logged, or only those from the
	// purchase month on when the scenario sets FromPurchase.
	SpentToDate  int64  `json:"spent_to_date"`
	IncomeToDate int64  `json:"income_to_date"`
	CashToDate   int64  `json:"cash_to_date"` // income − spent
	CurrentValue *int64 `json:"current_value"`

	BreakEvenMonth  *string `json:"break_even_month"`
	BreakEvenStatus string  `json:"break_even_status"`

	TrailingAnnualNet int64 `json:"trailing_annual_net"`
	// TrailingAnnualSpend is running + capital over the window, annualised.
	TrailingAnnualSpend     int64 `json:"trailing_annual_spend"`
	ProjectedAnnualNet      int64 `json:"projected_annual_net"`
	ProjectedAnnualExpenses int64 `json:"projected_annual_expenses"`
	// ROI is annual net ÷ SpentToDate — the sum of every expense logged (and
	// the purchase price when entered): what a year brings back on what the
	// property has cost so far.
	// ROISpend is the total ROI divides by: SpentToDate, plus the coming
	// year's projected expenses when the scenario includes them.
	ROISpend        int64    `json:"roi_spend"`
	ROITrailingPct  *float64 `json:"roi_trailing_pct"`
	ROIProjectedPct *float64 `json:"roi_projected_pct"`
	YieldPct        *float64 `json:"yield_pct"`
	// PaybackYears is how long, at the projected annual net, until income
	// covers the spend to date: 0 once it has, null if it never will.
	PaybackYears *float64 `json:"payback_years"`
}

// ProjectionResult is one scope's forecast: a property, or the portfolio.
type ProjectionResult struct {
	ID         string               `json:"id,omitempty"`
	Name       string               `json:"name,omitempty"`
	Baseline   ProjectionBaseline   `json:"baseline"`
	Applied    ProjectionApplied    `json:"applied"`
	Months     []ProjectionMonth    `json:"months"`
	Totals     ProjectionTotals     `json:"totals"`
	Investment ProjectionInvestment `json:"investment"`
	Units      []ProjectionUnitRow  `json:"units"`
}

// ------------------------------------------------------------- engine --

// ProjectProperty forecasts one property from `start` (the 1st of the first
// projected month) for the scenario's horizon.
func ProjectProperty(start time.Time, sc ProjectionScenario, p ProjectionProperty) ProjectionResult {
	sc = sc.normalised()
	coll, collSrc := appliedRate(sc.CollectionRatePct, trailingCollectionPct(p.TrailingCollected, p.TrailingExpected))

	months := historyMonths(p.HistoryMonths)
	var runningTotal int64
	for _, c := range p.TrailingRunning {
		runningTotal += c.Amount
	}
	runningAvg := float64(runningTotal) / float64(months)
	monthlyExpenses, expSrc := roundInt(runningAvg*(1+sc.ExpenseChangePct/100)), SourceTrailing
	if sc.MonthlyExpenses != nil {
		monthlyExpenses, expSrc = *sc.MonthlyExpenses, SourceScenario
	}

	assumed := assumedUnits(sc, p.Units)
	rentFactor := 1 + sc.RentChangePct/100
	out := ProjectionResult{
		ID:       p.ID,
		Name:     p.Name,
		Baseline: propertyBaseline(start, p, months, runningTotal, assumed),
		Applied:  applied(sc, coll, collSrc, monthlyExpenses, expSrc),
		Months:   make([]ProjectionMonth, sc.HorizonMonths),
		Units:    make([]ProjectionUnitRow, 0, len(p.Units)),
	}
	for i, u := range p.Units {
		out.Units = append(out.Units, ProjectionUnitRow{
			ID: u.ID, Name: u.Name, PropertyID: p.ID, PropertyName: p.Name, Status: u.Status,
			MonthlyRent: u.MonthlyRent, LetUntil: u.LetUntil, Lettable: u.Lettable, Assumed: assumed[i],
		})
	}

	past := sc.recorded(p)
	inv := ProjectionInvestment{PurchasePrice: p.PurchasePrice, CurrentValue: p.CurrentValue}
	for _, m := range past {
		inv.IncomeToDate += m.Income
		inv.RunningSpend += m.Running
		inv.CapitalSpend += m.Capital
	}
	inv.SpentToDate = inv.RunningSpend + inv.CapitalSpend
	if p.PurchasePrice != nil {
		inv.SpentToDate += *p.PurchasePrice
	}
	inv.CashToDate = inv.IncomeToDate - inv.SpentToDate

	cumIn, cumOut := inv.IncomeToDate, inv.SpentToDate
	nets := make([]int64, sc.HorizonMonths)
	expenses := make([]int64, sc.HorizonMonths)
	for m := 0; m < sc.HorizonMonths; m++ {
		gross := 0.0
		if m < len(p.Scheduled) {
			gross += float64(p.Scheduled[m])
		}
		for i, u := range p.Units {
			if assumed[i] && m >= u.FreeFrom {
				gross += float64(u.MonthlyRent) * rentFactor
			}
		}
		income := roundInt(gross * coll / 100)
		net := income - monthlyExpenses
		cumIn += income
		cumOut += monthlyExpenses
		nets[m], expenses[m] = net, monthlyExpenses
		out.Months[m] = ProjectionMonth{
			Month: start.AddDate(0, m, 0).Format(monthLayout), Income: income,
			RunningExpenses: monthlyExpenses, Net: net,
			CumulativeIncome: cumIn, CumulativeSpent: cumOut, Cumulative: cumIn - cumOut,
		}
		out.Totals.Income += income
		out.Totals.RunningExpenses += monthlyExpenses
		out.Totals.Net += net
	}

	trailingSpend := runningTotal + p.TrailingCapital
	inv.TrailingAnnualNet = annualise(out.Baseline.Net, months)
	inv.TrailingAnnualSpend = annualise(trailingSpend, months)
	inv.ProjectedAnnualNet = projectedAnnual(nets)
	inv.ProjectedAnnualExpenses = projectedAnnual(expenses)
	inv.ROISpend = roiSpend(sc, inv)
	inv.ROITrailingPct = pctRatio(inv.TrailingAnnualNet, inv.ROISpend)
	inv.ROIProjectedPct = pctRatio(inv.ProjectedAnnualNet, inv.ROISpend)
	inv.BreakEvenMonth, inv.BreakEvenStatus = breakEven(start, purchaseOf(p), past, nets, inv.ProjectedAnnualNet)
	inv.PaybackYears = payback(inv.CashToDate, inv.SpentToDate, inv.ProjectedAnnualNet)
	if p.CurrentValue != nil {
		inv.YieldPct = pctRatio(inv.ProjectedAnnualNet, *p.CurrentValue)
	}
	out.Investment = inv
	return out
}

// ProjectPortfolio forecasts every property and adds them up. The monthly
// lines are the sum of the properties' own lines, so the portfolio always
// reconciles with its parts; the return figures are computed on the sums.
func ProjectPortfolio(
	start time.Time, sc ProjectionScenario, props []ProjectionProperty,
) (ProjectionResult, []ProjectionResult) {
	sc = sc.normalised()
	parts := make([]ProjectionResult, len(props))
	shares := monthlyShares(sc.MonthlyExpenses, props)
	for i, p := range props {
		psc := sc
		if shares != nil {
			v := shares[i]
			psc.MonthlyExpenses = &v
		}
		parts[i] = ProjectProperty(start, psc, p)
	}

	out := ProjectionResult{Months: make([]ProjectionMonth, sc.HorizonMonths), Units: []ProjectionUnitRow{}}
	for m := range out.Months {
		out.Months[m].Month = start.AddDate(0, m, 0).Format(monthLayout)
	}
	var collected, expected, trailingSpend, purchase int64
	cats := map[string]*CategoryMonthly{}
	var catOrder []string
	b := &out.Baseline
	b.HistoryFrom = start.AddDate(0, -ProjectionHistoryMonths, 0).Format(dateLayout)
	b.HistoryTo = start.Format(dateLayout)
	b.Categories = []CategoryMonthly{}

	inv := ProjectionInvestment{}
	var valueTotal, valuedProjected int64
	var priced bool
	nets := make([]int64, sc.HorizonMonths)
	expenses := make([]int64, sc.HorizonMonths)
	pastByMonth := map[time.Time]*PastMonth{}
	for i, part := range parts {
		p := props[i]
		for m := range out.Months {
			pm := part.Months[m]
			o := &out.Months[m]
			o.Income += pm.Income
			o.RunningExpenses += pm.RunningExpenses
			o.Net += pm.Net
			o.CumulativeIncome += pm.CumulativeIncome
			o.CumulativeSpent += pm.CumulativeSpent
			o.Cumulative += pm.Cumulative
			nets[m] += pm.Net
			expenses[m] += pm.RunningExpenses
		}
		out.Totals.Income += part.Totals.Income
		out.Totals.RunningExpenses += part.Totals.RunningExpenses
		out.Totals.Net += part.Totals.Net
		out.Units = append(out.Units, part.Units...)

		pb := part.Baseline
		collected += p.TrailingCollected
		expected += p.TrailingExpected
		if pb.HistoryMonths > b.HistoryMonths {
			b.HistoryMonths = pb.HistoryMonths
		}
		b.RunningExpenses += pb.RunningExpenses
		b.CapitalExpenses += pb.CapitalExpenses
		b.RunningMonthly += pb.RunningMonthly
		b.Net += pb.Net
		b.UnitsTotal += pb.UnitsTotal
		b.UnitsLet += pb.UnitsLet
		b.UnitsOpen += pb.UnitsOpen
		b.UnitsAssumed += pb.UnitsAssumed
		b.AssumedRentMonthly += pb.AssumedRentMonthly
		for _, c := range pb.Categories {
			if cur, ok := cats[c.ID]; ok {
				cur.Monthly += c.Monthly
				continue
			}
			cp := c
			cats[c.ID] = &cp
			catOrder = append(catOrder, c.ID)
		}

		pi := part.Investment
		inv.CapitalSpend += pi.CapitalSpend
		inv.RunningSpend += pi.RunningSpend
		inv.SpentToDate += pi.SpentToDate
		inv.IncomeToDate += pi.IncomeToDate
		inv.CashToDate += pi.CashToDate
		inv.TrailingAnnualNet += pi.TrailingAnnualNet
		inv.TrailingAnnualSpend += pi.TrailingAnnualSpend
		trailingSpend += pb.RunningExpenses + pb.CapitalExpenses
		if p.PurchasePrice != nil {
			priced = true
			purchase += *p.PurchasePrice
		}
		for _, pm := range sc.recorded(p) {
			cur := pastByMonth[pm.Month]
			if cur == nil {
				cur = &PastMonth{Month: pm.Month}
				pastByMonth[pm.Month] = cur
			}
			cur.Income += pm.Income
			cur.Running += pm.Running
			cur.Capital += pm.Capital
		}
		if pi.CurrentValue != nil {
			valueTotal += *pi.CurrentValue
			valuedProjected += pi.ProjectedAnnualNet
		}
	}

	b.Collected, b.Expected = collected, expected
	b.CollectionRatePct = trailingCollectionPct(collected, expected)
	for _, id := range catOrder {
		b.Categories = append(b.Categories, *cats[id])
	}
	if b.HistoryMonths == 0 {
		b.HistoryMonths = 1
	}
	coll, collSrc := appliedRate(sc.CollectionRatePct, b.CollectionRatePct)
	var monthly int64
	for _, part := range parts {
		monthly += part.Applied.MonthlyExpenses
	}
	expSrc := SourceTrailing
	if sc.MonthlyExpenses != nil {
		expSrc = SourceScenario
	}
	out.Applied = applied(sc, coll, collSrc, monthly, expSrc)

	if priced {
		pp := purchase
		inv.PurchasePrice = &pp
	}
	past := make([]PastMonth, 0, len(pastByMonth))
	for _, pm := range pastByMonth {
		past = append(past, *pm)
	}
	sort.Slice(past, func(i, j int) bool { return past[i].Month.Before(past[j].Month) })
	inv.ProjectedAnnualNet = projectedAnnual(nets)
	inv.ProjectedAnnualExpenses = projectedAnnual(expenses)
	inv.ROISpend = roiSpend(sc, inv)
	inv.ROITrailingPct = pctRatio(inv.TrailingAnnualNet, inv.ROISpend)
	inv.ROIProjectedPct = pctRatio(inv.ProjectedAnnualNet, inv.ROISpend)
	inv.BreakEvenMonth, inv.BreakEvenStatus = breakEven(start, purchase, past, nets, inv.ProjectedAnnualNet)
	inv.PaybackYears = payback(inv.CashToDate, inv.SpentToDate, inv.ProjectedAnnualNet)
	if valueTotal > 0 {
		v := valueTotal
		inv.CurrentValue = &v
		inv.YieldPct = pctRatio(valuedProjected, valueTotal)
	}
	out.Investment = inv
	return out, parts
}

// ------------------------------------------------------------ helpers --

const monthLayout = "2006-01"

const dateLayout = "2006-01-02"

// normalised applies the defaults and the horizon bounds.
func (sc ProjectionScenario) normalised() ProjectionScenario {
	if sc.HorizonMonths <= 0 {
		sc.HorizonMonths = ProjectionHorizonDefault
	}
	if sc.HorizonMonths > ProjectionHorizonMax {
		sc.HorizonMonths = ProjectionHorizonMax
	}
	if !ValidBasis(sc.Basis) {
		sc.Basis = BasisContracts
	}
	if sc.Basis != BasisSelected || sc.UnitIDs == nil {
		sc.UnitIDs = []string{}
	}
	return sc
}

func applied(sc ProjectionScenario, coll float64, collSrc string, monthly int64, expSrc string) ProjectionApplied {
	return ProjectionApplied{
		HorizonMonths: sc.HorizonMonths, Basis: sc.Basis, UnitIDs: sc.UnitIDs,
		RentChangePct:     sc.RentChangePct,
		CollectionRatePct: coll, CollectionRateSource: collSrc,
		ExpenseChangePct: sc.ExpenseChangePct,
		MonthlyExpenses:  monthly, MonthlyExpensesSource: expSrc,
		FromPurchase: sc.FromPurchase, IncludeFutureExpenses: sc.IncludeFutureExpenses,
	}
}

// monthlyShares splits a portfolio-wide monthly estimate across the
// properties so each property's line still adds up to the portfolio's: in
// proportion to their trailing running costs, else to their unit counts, else
// equally. The last property takes the rounding, so the shares sum exactly.
// Nil when there is no estimate.
func monthlyShares(total *int64, props []ProjectionProperty) []int64 {
	if total == nil || len(props) == 0 {
		return nil
	}
	weights := make([]float64, len(props))
	var sum float64
	for i, p := range props {
		for _, c := range p.TrailingRunning {
			weights[i] += float64(c.Amount) / float64(historyMonths(p.HistoryMonths))
		}
		sum += weights[i]
	}
	if sum == 0 {
		for i, p := range props {
			weights[i] = float64(len(p.Units))
			sum += weights[i]
		}
	}
	if sum == 0 {
		for i := range weights {
			weights[i] = 1
		}
		sum = float64(len(weights))
	}
	out := make([]int64, len(props))
	var given int64
	for i := range props {
		if i == len(props)-1 {
			out[i] = *total - given
			break
		}
		out[i] = roundInt(float64(*total) * weights[i] / sum)
		given += out[i]
	}
	return out
}

// assumedUnits says, per unit, whether the basis assumes it let once it is
// free of its contract. Signed contracts are always counted through their
// schedules; this is only about the time no contract covers a unit.
func assumedUnits(sc ProjectionScenario, units []ProjectionUnit) []bool {
	out := make([]bool, len(units))
	picked := make(map[string]struct{}, len(sc.UnitIDs))
	for _, id := range sc.UnitIDs {
		picked[id] = struct{}{}
	}
	for i, u := range units {
		switch sc.Basis {
		case BasisBestCase:
			out[i] = u.Lettable
		case BasisSelected:
			_, out[i] = picked[u.ID]
		}
	}
	return out
}

// propertyBaseline describes one property's trailing window.
func propertyBaseline(start time.Time, p ProjectionProperty, months int, runningTotal int64, assumed []bool) ProjectionBaseline {
	b := ProjectionBaseline{
		HistoryFrom:       start.AddDate(0, -ProjectionHistoryMonths, 0).Format(dateLayout),
		HistoryTo:         start.Format(dateLayout),
		HistoryMonths:     months,
		Collected:         p.TrailingCollected,
		Expected:          p.TrailingExpected,
		CollectionRatePct: trailingCollectionPct(p.TrailingCollected, p.TrailingExpected),
		RunningExpenses:   runningTotal,
		CapitalExpenses:   p.TrailingCapital,
		RunningMonthly:    roundInt(float64(runningTotal) / float64(months)),
		Categories:        make([]CategoryMonthly, 0, len(p.TrailingRunning)),
		Net:               p.TrailingCollected - runningTotal,
		UnitsTotal:        len(p.Units),
	}
	for _, c := range p.TrailingRunning {
		b.Categories = append(b.Categories, CategoryMonthly{
			ID: c.ID, Name: c.Name, Monthly: roundInt(float64(c.Amount) / float64(months)),
		})
	}
	for i, u := range p.Units {
		switch {
		case u.FreeFrom > 0:
			b.UnitsLet++
		case u.Lettable:
			b.UnitsOpen++
		}
		if assumed[i] {
			b.UnitsAssumed++
			b.AssumedRentMonthly += u.MonthlyRent
		}
	}
	return b
}

func historyMonths(n int) int {
	if n < 1 {
		return 1
	}
	if n > ProjectionHistoryMonths {
		return ProjectionHistoryMonths
	}
	return n
}

// trailingCollectionPct is collected ÷ expected as a percentage, capped at
// 100: arrears paid late can push a window's collections above what fell due
// in it, and projecting more than every shilling owed would be fiction. Nil
// when nothing fell due.
func trailingCollectionPct(collected, expected int64) *float64 {
	if expected <= 0 {
		return nil
	}
	v := round1(clampPct(float64(collected) / float64(expected) * 100))
	return &v
}

// appliedRate picks the scenario's rate, else the trailing one, else 100%.
func appliedRate(scenario, trailing *float64) (float64, string) {
	switch {
	case scenario != nil:
		return *scenario, SourceScenario
	case trailing != nil:
		return *trailing, SourceTrailing
	default:
		return 100, SourceDefault
	}
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// monthStart is the 1st of d's month, in UTC like every other month key here.
func monthStart(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// recorded is the property's recorded months the scenario counts: all of
// them, or only those from the purchase month on when FromPurchase is set.
func (sc ProjectionScenario) recorded(p ProjectionProperty) []PastMonth {
	if !sc.FromPurchase {
		return p.Past
	}
	return sincePurchase(p.Past, p.PurchaseDate)
}

// roiSpend is what ROI is measured against: everything spent to date, plus
// the coming year's projected running costs when the scenario asks for them.
func roiSpend(sc ProjectionScenario, inv ProjectionInvestment) int64 {
	if sc.IncludeFutureExpenses {
		return inv.SpentToDate + inv.ProjectedAnnualExpenses
	}
	return inv.SpentToDate
}

// sincePurchase keeps the months from the purchase month on; every month
// without a purchase date.
func sincePurchase(past []PastMonth, purchase *time.Time) []PastMonth {
	if purchase == nil {
		return past
	}
	from := monthStart(*purchase)
	out := make([]PastMonth, 0, len(past))
	for _, m := range past {
		if !monthStart(m.Month).Before(from) {
			out = append(out, m)
		}
	}
	return out
}

func purchaseOf(p ProjectionProperty) int64 {
	if p.PurchasePrice == nil {
		return 0
	}
	return *p.PurchasePrice
}

// breakEven walks the actual months and then the projected ones, with the
// purchase price (if any) spent up front, and names the month cumulative
// income covers cumulative spend. A book that dipped below and came back is
// "reached" at the month it last came back; one still below is projected
// forward. A book whose income covers every cost, past and projected, from
// the first month has nothing to break even on: `no_costs`.
func breakEven(start time.Time, purchase int64, past []PastMonth, nets []int64, annual int64) (*string, string) {
	cum := -purchase
	spent := purchase
	var back *time.Time
	if cum >= 0 && len(past) > 0 {
		m := monthStart(past[0].Month)
		back = &m
	}
	for _, m := range past {
		cum += m.Income - m.Spent()
		spent += m.Spent()
		switch {
		case cum < 0:
			back = nil
		case back == nil:
			ms := monthStart(m.Month)
			back = &ms
		}
	}
	if cum >= 0 && spent > 0 {
		if back != nil {
			s := back.Format(monthLayout)
			return &s, BreakEvenReached
		}
		return nil, BreakEvenReached
	}
	// Still below, or nothing spent yet: walk the forecast. A book that has
	// spent nothing only has something to break even on once a projected
	// month takes it below zero.
	dipped := cum < 0
	for i, n := range nets {
		cum += n
		switch {
		case cum < 0:
			dipped = true
		case dipped:
			s := start.AddDate(0, i, 0).Format(monthLayout)
			return &s, BreakEvenProjected
		}
	}
	if !dipped {
		return nil, BreakEvenNoCosts
	}
	if annual <= 0 {
		return nil, BreakEvenNotProfitable
	}
	return nil, BreakEvenBeyondHorizon
}

// annualise scales a window's figure to twelve months.
func annualise(v int64, months int) int64 {
	return roundInt(float64(v) * ProjectionHistoryMonths / float64(months))
}

// projectedAnnual is the first twelve projected months' figure, scaled to a
// year when the horizon is shorter.
func projectedAnnual(vals []int64) int64 {
	n := len(vals)
	if n > ProjectionHistoryMonths {
		n = ProjectionHistoryMonths
	}
	if n == 0 {
		return 0
	}
	var sum int64
	for _, v := range vals[:n] {
		sum += v
	}
	return annualise(sum, n)
}

// pctRatio is num ÷ den as a percentage to one decimal; nil for no den.
func pctRatio(num, den int64) *float64 {
	if den <= 0 {
		return nil
	}
	v := round1(float64(num) / float64(den) * 100)
	return &v
}

// payback is how many years, at the projected annual net, until income
// covers the spend to date: 0 when it already has, nil when nothing was spent
// or the property does not make money.
func payback(cashToDate, spent, annual int64) *float64 {
	if spent <= 0 {
		return nil
	}
	if cashToDate >= 0 {
		v := 0.0
		return &v
	}
	if annual <= 0 {
		return nil
	}
	v := round1(float64(-cashToDate) / float64(annual))
	return &v
}

// roundInt rounds half away from zero to a whole shilling.
func roundInt(v float64) int64 { return int64(math.Round(v)) }
