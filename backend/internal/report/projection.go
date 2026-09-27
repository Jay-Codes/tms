package report

// Phase 28 — projections, break-even and ROI (PLAN2 Phase 28).
//
// This file is the whole of the arithmetic. It reads no database and knows no
// HTTP: the handler gathers the facts (the trailing twelve months of cash and
// expenses, the tenancies' future schedules, the units' market rents, the
// investment a landlord entered) and this turns them into a monthly forecast
// and the four investment figures. Keeping it pure is what lets every figure be
// checked against a hand-computed fixture, and CLAUDE.md keeps it out of the
// frontend: the screen sends scenario parameters and draws what comes back.
//
// The model, in one paragraph. A month's projected income is the rent already
// scheduled on running tenancies plus, for every lettable unit not covered by
// one, its market rent × (1 + rent change) × occupancy; the sum is then
// multiplied by the collection rate. A month's running expenses are the
// trailing monthly average of each non-capital category × (1 + expense
// change). Capital spend is not a running cost: it is added to the purchase
// price to make the investment. Net is income less running expenses; the
// cumulative line starts from the cash the property has actually made since it
// was bought, and break-even is the first month it reaches the investment.

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

// Break-even outcomes. Exactly one applies to a scope.
const (
	BreakEvenReached       = "reached"           // already happened, in the past
	BreakEvenProjected     = "projected"         // happens inside the horizon
	BreakEvenBeyondHorizon = "beyond_horizon"    // profitable, but not within the horizon
	BreakEvenNotProfitable = "not_profitable"    // the projected net never pays it back
	BreakEvenNoPrice       = "no_purchase_price" // no investment to break even on
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
	HorizonMonths     int
	RentChangePct     float64
	OccupancyPct      *float64
	CollectionRatePct *float64
	ExpenseChangePct  float64
}

// CategoryAmount is one expense category's total over the trailing window.
type CategoryAmount struct {
	ID     string
	Name   string
	Amount int64
}

// MonthAmount is one calendar month's figure; Month is the 1st of the month.
type MonthAmount struct {
	Month  time.Time
	Amount int64
}

// ProjectionUnit is one unit as the forecast sees it.
type ProjectionUnit struct {
	// MonthlyRent is what the market pays for it per month: the current price
	// (or the last tenancy's rent) normalised to 30 days.
	MonthlyRent int64
	// FreeFrom is the first projection month the unit is on the open market:
	// 0 for a vacant unit, the month after its running tenancy ends otherwise.
	// Before it, the unit's income is its scheduled rent, counted elsewhere.
	FreeFrom int
	// Lettable is false for a unit under maintenance or unlisted: it earns
	// its schedule, if it has one, and nothing on the open market.
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
	TrailingCollected int64 // cash collected, net of refunds
	TrailingExpected  int64 // what fell due
	// Occupancy over the window, as unit-months: each unit measured at the end
	// of each month.
	TrailingOccupiedUnitMonths int64
	TrailingUnitMonths         int64
	TrailingRunning            []CategoryAmount // non-capital expenses per category
	TrailingCapital            int64            // capital spend inside the window
	CapitalToDate              int64            // all capital spend ever recorded

	// PastNet is the actual monthly net (collected less running expenses)
	// over the property's whole recorded history before the projection
	// starts, ascending. Months before the purchase month are ignored.
	PastNet []MonthAmount
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
	// Cumulative is the cash made since purchase (or since the book began,
	// without a purchase date), at the end of this month.
	Cumulative int64 `json:"cumulative"`
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
	OccupancyPct      *float64 `json:"occupancy_pct"`
	RunningExpenses   int64    `json:"running_expenses"`
	CapitalExpenses   int64    `json:"capital_expenses"`
	// RunningMonthly is the trailing monthly average of running costs.
	RunningMonthly    int64             `json:"running_expenses_monthly"`
	Categories        []CategoryMonthly `json:"categories"`
	Net               int64             `json:"net"`
	UnitsTotal        int               `json:"units_total"`
	UnitsLet          int               `json:"units_let"`
	UnitsOpen         int               `json:"units_open"`
	MarketRentMonthly int64             `json:"market_rent_monthly"`
}

// ProjectionApplied is the scenario as it was actually applied.
type ProjectionApplied struct {
	HorizonMonths        int     `json:"horizon_months"`
	RentChangePct        float64 `json:"rent_change_pct"`
	OccupancyPct         float64 `json:"occupancy_pct"`
	OccupancySource      string  `json:"occupancy_source"`
	CollectionRatePct    float64 `json:"collection_rate_pct"`
	CollectionRateSource string  `json:"collection_rate_source"`
	ExpenseChangePct     float64 `json:"expense_change_pct"`
}

// ProjectionInvestment is the break-even and return block.
type ProjectionInvestment struct {
	PurchasePrice *int64 `json:"purchase_price"`
	CapitalSpend  int64  `json:"capital_spend"`
	// Total is purchase price + capital spend; null without a purchase price.
	Total        *int64 `json:"total"`
	CurrentValue *int64 `json:"current_value"`
	// CashToDate is the actual net cash made since purchase, up to the start
	// of the projection — where the cumulative line starts.
	CashToDate         int64    `json:"cash_to_date"`
	BreakEvenMonth     *string  `json:"break_even_month"`
	BreakEvenStatus    string   `json:"break_even_status"`
	TrailingAnnualNet  int64    `json:"trailing_annual_net"`
	ProjectedAnnualNet int64    `json:"projected_annual_net"`
	ROITrailingPct     *float64 `json:"roi_trailing_pct"`
	ROIProjectedPct    *float64 `json:"roi_projected_pct"`
	YieldPct           *float64 `json:"yield_pct"`
	PaybackYears       *float64 `json:"payback_years"`
	// Incomplete is the portfolio's warning: some properties have no purchase
	// price, so the investment figures cover only the ones that do.
	Incomplete      bool     `json:"incomplete"`
	PricedCount     int      `json:"priced_properties"`
	MissingPriceIDs []string `json:"missing_price_property_ids"`
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
}

// ------------------------------------------------------------- engine --

// ProjectProperty forecasts one property from `start` (the 1st of the first
// projected month) for the scenario's horizon.
func ProjectProperty(start time.Time, sc ProjectionScenario, p ProjectionProperty) ProjectionResult {
	sc = sc.normalised()
	coll, collSrc := appliedRate(sc.CollectionRatePct, trailingCollectionPct(p.TrailingCollected, p.TrailingExpected))
	occ, occSrc := appliedRate(sc.OccupancyPct, pctOf(p.TrailingOccupiedUnitMonths, p.TrailingUnitMonths))

	months := historyMonths(p.HistoryMonths)
	var runningTotal int64
	for _, c := range p.TrailingRunning {
		runningTotal += c.Amount
	}
	runningAvg := float64(runningTotal) / float64(months)
	monthlyExpenses := roundInt(runningAvg * (1 + sc.ExpenseChangePct/100))

	rentFactor := 1 + sc.RentChangePct/100
	out := ProjectionResult{
		ID:       p.ID,
		Name:     p.Name,
		Baseline: propertyBaseline(start, p, months, runningTotal),
		Applied: ProjectionApplied{
			HorizonMonths: sc.HorizonMonths, RentChangePct: sc.RentChangePct,
			OccupancyPct: occ, OccupancySource: occSrc,
			CollectionRatePct: coll, CollectionRateSource: collSrc,
			ExpenseChangePct: sc.ExpenseChangePct,
		},
		Months: make([]ProjectionMonth, sc.HorizonMonths),
	}

	cash := cashSince(p.PastNet, p.PurchaseDate)
	cum := cash
	nets := make([]int64, sc.HorizonMonths)
	for m := 0; m < sc.HorizonMonths; m++ {
		gross := 0.0
		if m < len(p.Scheduled) {
			gross += float64(p.Scheduled[m])
		}
		for _, u := range p.Units {
			if u.Lettable && m >= u.FreeFrom {
				gross += float64(u.MonthlyRent) * rentFactor * occ / 100
			}
		}
		income := roundInt(gross * coll / 100)
		net := income - monthlyExpenses
		cum += net
		nets[m] = net
		out.Months[m] = ProjectionMonth{
			Month: start.AddDate(0, m, 0).Format(monthLayout), Income: income,
			RunningExpenses: monthlyExpenses, Net: net, Cumulative: cum,
		}
		out.Totals.Income += income
		out.Totals.RunningExpenses += monthlyExpenses
		out.Totals.Net += net
	}

	inv := ProjectionInvestment{
		PurchasePrice: p.PurchasePrice, CapitalSpend: p.CapitalToDate, CurrentValue: p.CurrentValue,
		CashToDate:         cash,
		TrailingAnnualNet:  annualise(out.Baseline.Net, months),
		ProjectedAnnualNet: projectedAnnual(nets),
		MissingPriceIDs:    []string{},
	}
	if p.PurchasePrice != nil {
		total := *p.PurchasePrice + p.CapitalToDate
		inv.Total = &total
		inv.PricedCount = 1
		past := sincePurchase(p.PastNet, p.PurchaseDate)
		inv.BreakEvenMonth, inv.BreakEvenStatus = breakEven(start, total, past, nets, inv.ProjectedAnnualNet)
		inv.ROITrailingPct = pctRatio(inv.TrailingAnnualNet, total)
		inv.ROIProjectedPct = pctRatio(inv.ProjectedAnnualNet, total)
		inv.PaybackYears = payback(total, inv.ProjectedAnnualNet)
	} else {
		inv.BreakEvenStatus = BreakEvenNoPrice
		inv.MissingPriceIDs = []string{p.ID}
	}
	if p.CurrentValue != nil {
		inv.YieldPct = pctRatio(inv.ProjectedAnnualNet, *p.CurrentValue)
	}
	out.Investment = inv
	return out
}

// ProjectPortfolio forecasts every property and adds them up. The monthly
// lines are the sum of the properties' own lines, so the portfolio always
// reconciles with its parts. The investment figures cover only the properties
// that have a purchase price — adding the income of an unpriced block to the
// return on a priced one would overstate it — and say so with `incomplete`.
func ProjectPortfolio(
	start time.Time, sc ProjectionScenario, props []ProjectionProperty,
) (ProjectionResult, []ProjectionResult) {
	sc = sc.normalised()
	parts := make([]ProjectionResult, len(props))
	for i, p := range props {
		parts[i] = ProjectProperty(start, sc, p)
	}

	out := ProjectionResult{Months: make([]ProjectionMonth, sc.HorizonMonths)}
	for m := range out.Months {
		out.Months[m].Month = start.AddDate(0, m, 0).Format(monthLayout)
	}
	var collected, expected, occUnits, unitMonths int64
	cats := map[string]*CategoryMonthly{}
	var catOrder []string
	b := &out.Baseline
	b.HistoryFrom = start.AddDate(0, -ProjectionHistoryMonths, 0).Format(dateLayout)
	b.HistoryTo = start.Format(dateLayout)
	b.Categories = []CategoryMonthly{}

	inv := ProjectionInvestment{MissingPriceIDs: []string{}}
	var pricedTotal, pricedTrailing, pricedProjected, valueTotal, valuedProjected int64
	pricedNets := make([]int64, sc.HorizonMonths)
	pastByMonth := map[time.Time]int64{}
	for i, part := range parts {
		p := props[i]
		for m := range out.Months {
			pm := part.Months[m]
			out.Months[m].Income += pm.Income
			out.Months[m].RunningExpenses += pm.RunningExpenses
			out.Months[m].Net += pm.Net
			out.Months[m].Cumulative += pm.Cumulative
		}
		out.Totals.Income += part.Totals.Income
		out.Totals.RunningExpenses += part.Totals.RunningExpenses
		out.Totals.Net += part.Totals.Net

		pb := part.Baseline
		collected += p.TrailingCollected
		expected += p.TrailingExpected
		occUnits += p.TrailingOccupiedUnitMonths
		unitMonths += p.TrailingUnitMonths
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
		b.MarketRentMonthly += pb.MarketRentMonthly
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
		inv.CashToDate += pi.CashToDate
		inv.TrailingAnnualNet += pi.TrailingAnnualNet
		inv.ProjectedAnnualNet += pi.ProjectedAnnualNet
		if pi.Total != nil {
			inv.PricedCount++
			pricedTotal += *pi.Total
			pricedTrailing += pi.TrailingAnnualNet
			pricedProjected += pi.ProjectedAnnualNet
			for m := range pricedNets {
				pricedNets[m] += part.Months[m].Net
			}
			for _, pm := range sincePurchase(p.PastNet, p.PurchaseDate) {
				pastByMonth[pm.Month] += pm.Amount
			}
		} else {
			inv.MissingPriceIDs = append(inv.MissingPriceIDs, p.ID)
		}
		if pi.CurrentValue != nil {
			valueTotal += *pi.CurrentValue
			valuedProjected += pi.ProjectedAnnualNet
		}
	}

	b.Collected, b.Expected = collected, expected
	b.CollectionRatePct = trailingCollectionPct(collected, expected)
	b.OccupancyPct = pctOf(occUnits, unitMonths)
	for _, id := range catOrder {
		b.Categories = append(b.Categories, *cats[id])
	}
	if b.HistoryMonths == 0 {
		b.HistoryMonths = 1
	}

	coll, collSrc := appliedRate(sc.CollectionRatePct, b.CollectionRatePct)
	occ, occSrc := appliedRate(sc.OccupancyPct, b.OccupancyPct)
	out.Applied = ProjectionApplied{
		HorizonMonths: sc.HorizonMonths, RentChangePct: sc.RentChangePct,
		OccupancyPct: occ, OccupancySource: occSrc,
		CollectionRatePct: coll, CollectionRateSource: collSrc,
		ExpenseChangePct: sc.ExpenseChangePct,
	}

	inv.Incomplete = inv.PricedCount < len(props)
	if inv.PricedCount > 0 {
		purchase := pricedTotal
		inv.Total = &purchase
		past := make([]MonthAmount, 0, len(pastByMonth))
		for month, amount := range pastByMonth {
			past = append(past, MonthAmount{Month: month, Amount: amount})
		}
		sort.Slice(past, func(i, j int) bool { return past[i].Month.Before(past[j].Month) })
		inv.BreakEvenMonth, inv.BreakEvenStatus = breakEven(start, pricedTotal, past, pricedNets, pricedProjected)
		inv.ROITrailingPct = pctRatio(pricedTrailing, pricedTotal)
		inv.ROIProjectedPct = pctRatio(pricedProjected, pricedTotal)
		inv.PaybackYears = payback(pricedTotal, pricedProjected)
		var pp int64
		for _, part := range parts {
			if part.Investment.PurchasePrice != nil {
				pp += *part.Investment.PurchasePrice
			}
		}
		inv.PurchasePrice = &pp
	} else {
		inv.BreakEvenStatus = BreakEvenNoPrice
	}
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
	return sc
}

// propertyBaseline describes one property's trailing window.
func propertyBaseline(start time.Time, p ProjectionProperty, months int, runningTotal int64) ProjectionBaseline {
	b := ProjectionBaseline{
		HistoryFrom:       start.AddDate(0, -ProjectionHistoryMonths, 0).Format(dateLayout),
		HistoryTo:         start.Format(dateLayout),
		HistoryMonths:     months,
		Collected:         p.TrailingCollected,
		Expected:          p.TrailingExpected,
		CollectionRatePct: trailingCollectionPct(p.TrailingCollected, p.TrailingExpected),
		OccupancyPct:      pctOf(p.TrailingOccupiedUnitMonths, p.TrailingUnitMonths),
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
	for _, u := range p.Units {
		switch {
		case u.FreeFrom > 0:
			b.UnitsLet++
		case u.Lettable:
			b.UnitsOpen++
			b.MarketRentMonthly += u.MonthlyRent
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

// pctOf is part ÷ whole as a percentage to one decimal; nil for no whole.
func pctOf(part, whole int64) *float64 {
	if whole <= 0 {
		return nil
	}
	v := round1(clampPct(float64(part) / float64(whole) * 100))
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

// sincePurchase keeps the months from the purchase month on; every month
// without a purchase date.
func sincePurchase(past []MonthAmount, purchase *time.Time) []MonthAmount {
	if purchase == nil {
		return past
	}
	from := monthStart(*purchase)
	out := make([]MonthAmount, 0, len(past))
	for _, m := range past {
		if !monthStart(m.Month).Before(from) {
			out = append(out, m)
		}
	}
	return out
}

func cashSince(past []MonthAmount, purchase *time.Time) int64 {
	var out int64
	for _, m := range sincePurchase(past, purchase) {
		out += m.Amount
	}
	return out
}

// breakEven walks the actual months and then the projected ones, and names
// the first month the cumulative net reaches the investment.
func breakEven(start time.Time, investment int64, past []MonthAmount, nets []int64, annual int64) (*string, string) {
	var cum int64
	for _, m := range past {
		cum += m.Amount
		if cum >= investment {
			s := monthStart(m.Month).Format(monthLayout)
			return &s, BreakEvenReached
		}
	}
	for i, n := range nets {
		cum += n
		if cum >= investment {
			s := start.AddDate(0, i, 0).Format(monthLayout)
			return &s, BreakEvenProjected
		}
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

// projectedAnnual is the first twelve projected months' net, scaled to a year
// when the horizon is shorter.
func projectedAnnual(nets []int64) int64 {
	n := len(nets)
	if n > ProjectionHistoryMonths {
		n = ProjectionHistoryMonths
	}
	if n == 0 {
		return 0
	}
	var sum int64
	for _, v := range nets[:n] {
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

// payback is the simple payback period in years: investment ÷ annual net.
// Nil when the property does not make money — it never pays back.
func payback(investment, annual int64) *float64 {
	if annual <= 0 {
		return nil
	}
	v := round1(float64(investment) / float64(annual))
	return &v
}

// roundInt rounds half away from zero to a whole shilling.
func roundInt(v float64) int64 { return int64(math.Round(v)) }
