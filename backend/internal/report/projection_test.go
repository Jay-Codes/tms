package report_test

import (
	"testing"
	"time"

	"tms/backend/internal/report"
)

// Phase 28 — the projection engine against hand-computed fixtures.
//
// The base property, worked by hand (start = October 2026, horizon 6):
//
//	trailing: collected 1,080,000 of 1,200,000 expected   → collection 90%
//	          18 of 24 unit-months occupied                → occupancy 75%
//	          running costs 240,000 + 120,000 over 12 mo.  → 30,000 a month
//	units:    one let until month 3 at 100,000 (scheduled 100,000 × 3)
//	          one vacant at 120,000
//
//	months 0–2: (100,000 + 120,000 × 0.75) × 0.90 = 171,000; net 141,000
//	months 3–5: (100,000 + 120,000) × 0.75 × 0.90 = 148,500; net 118,500
//	horizon:    income 958,500, running 180,000, net 778,500
//	annual:     778,500 × 12 / 6 = 1,557,000 projected; 720,000 trailing
//
// Past net since the purchase (15 Jan 2025): 300,000 in June 2025 and 100,000
// in Sept 2026 — the 1,000,000 of December 2024 predates it and is ignored.

func month(y int, m time.Month) time.Time { return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC) }

func i64(v int64) *int64 { return &v }

var projStart = month(2026, time.October)

func baseProperty() report.ProjectionProperty {
	bought := time.Date(2025, time.January, 15, 0, 0, 0, 0, time.UTC)
	return report.ProjectionProperty{
		ID: "p", Name: "Mbezi Block A",
		PurchaseDate:               &bought,
		HistoryMonths:              12,
		TrailingCollected:          1_080_000,
		TrailingExpected:           1_200_000,
		TrailingOccupiedUnitMonths: 18,
		TrailingUnitMonths:         24,
		TrailingRunning: []report.CategoryAmount{
			{ID: "c1", Name: "Repairs", Amount: 240_000},
			{ID: "c2", Name: "Utilities", Amount: 120_000},
		},
		TrailingCapital: 400_000,
		CapitalToDate:   400_000,
		PastNet: []report.MonthAmount{
			{Month: month(2024, time.December), Amount: 1_000_000},
			{Month: month(2025, time.June), Amount: 300_000},
			{Month: month(2026, time.September), Amount: 100_000},
		},
		Scheduled: []int64{100_000, 100_000, 100_000},
		Units: []report.ProjectionUnit{
			{MonthlyRent: 100_000, FreeFrom: 3, Lettable: true},
			{MonthlyRent: 120_000, FreeFrom: 0, Lettable: true},
		},
	}
}

func sixMonths() report.ProjectionScenario { return report.ProjectionScenario{HorizonMonths: 6} }

func wantF(t *testing.T, what string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = null, want %v", what, want)
	}
	if *got != want {
		t.Errorf("%s = %v, want %v", what, *got, want)
	}
}

func wantNilF(t *testing.T, what string, got *float64) {
	t.Helper()
	if got != nil {
		t.Errorf("%s = %v, want null", what, *got)
	}
}

func TestProjectionMonthlyArithmetic(t *testing.T) {
	p := baseProperty()
	p.PurchasePrice = i64(5_000_000)
	got := report.ProjectProperty(projStart, sixMonths(), p)

	if len(got.Months) != 6 {
		t.Fatalf("months = %d, want 6", len(got.Months))
	}
	for i, m := range got.Months {
		wantIncome, wantNet := int64(171_000), int64(141_000)
		if i >= 3 {
			wantIncome, wantNet = 148_500, 118_500
		}
		if m.Income != wantIncome || m.RunningExpenses != 30_000 || m.Net != wantNet {
			t.Errorf("month %d (%s) = income %d, running %d, net %d; want %d, 30000, %d",
				i, m.Month, m.Income, m.RunningExpenses, m.Net, wantIncome, wantNet)
		}
	}
	if got.Months[0].Month != "2026-10" || got.Months[5].Month != "2027-03" {
		t.Errorf("month labels %s…%s, want 2026-10…2027-03", got.Months[0].Month, got.Months[5].Month)
	}
	if got.Totals != (report.ProjectionTotals{Income: 958_500, RunningExpenses: 180_000, Net: 778_500}) {
		t.Errorf("totals = %+v", got.Totals)
	}
	// The cumulative line starts from the cash made since purchase (400,000).
	if c := got.Months[0].Cumulative; c != 541_000 {
		t.Errorf("month 0 cumulative = %d, want 541000", c)
	}
	if c := got.Months[5].Cumulative; c != 1_178_500 {
		t.Errorf("month 5 cumulative = %d, want 1178500", c)
	}

	b := got.Baseline
	wantF(t, "baseline collection", b.CollectionRatePct, 90)
	wantF(t, "baseline occupancy", b.OccupancyPct, 75)
	if b.RunningMonthly != 30_000 || b.RunningExpenses != 360_000 || b.CapitalExpenses != 400_000 {
		t.Errorf("baseline expenses = %+v", b)
	}
	if b.Net != 720_000 || b.UnitsTotal != 2 || b.UnitsLet != 1 || b.UnitsOpen != 1 || b.MarketRentMonthly != 120_000 {
		t.Errorf("baseline = %+v", b)
	}
	if b.HistoryFrom != "2025-10-01" || b.HistoryTo != "2026-10-01" {
		t.Errorf("history window %s → %s", b.HistoryFrom, b.HistoryTo)
	}
	if len(b.Categories) != 2 || b.Categories[0].Monthly != 20_000 || b.Categories[1].Monthly != 10_000 {
		t.Errorf("categories = %+v", b.Categories)
	}
	a := got.Applied
	if a.CollectionRatePct != 90 || a.CollectionRateSource != report.SourceTrailing ||
		a.OccupancyPct != 75 || a.OccupancySource != report.SourceTrailing || a.HorizonMonths != 6 {
		t.Errorf("applied = %+v", a)
	}

	// Investment: 5,000,000 + 400,000 capital. Capital spend is investment, not
	// a running cost: the monthly 30,000 above does not include it.
	inv := got.Investment
	if inv.Total == nil || *inv.Total != 5_400_000 || inv.CashToDate != 400_000 {
		t.Fatalf("investment = %+v", inv)
	}
	if inv.TrailingAnnualNet != 720_000 || inv.ProjectedAnnualNet != 1_557_000 {
		t.Errorf("annual nets = %d trailing, %d projected", inv.TrailingAnnualNet, inv.ProjectedAnnualNet)
	}
	wantF(t, "roi trailing", inv.ROITrailingPct, 13.3)   // 720,000 / 5,400,000
	wantF(t, "roi projected", inv.ROIProjectedPct, 28.8) // 1,557,000 / 5,400,000
	wantF(t, "payback", inv.PaybackYears, 3.5)           // 5,400,000 / 1,557,000 = 3.47
	wantNilF(t, "yield without a current value", inv.YieldPct)
	if inv.BreakEvenStatus != report.BreakEvenBeyondHorizon || inv.BreakEvenMonth != nil {
		t.Errorf("break-even = %v %s, want beyond_horizon", inv.BreakEvenMonth, inv.BreakEvenStatus)
	}
}

func TestProjectionBreakEven(t *testing.T) {
	for _, c := range []struct {
		name       string
		price      int64
		expenseChg float64
		wantMonth  string
		wantStatus string
	}{
		// 400,000 to date; +141,000 ×3, +118,500 → 1,060,000 at month 4.
		{name: "inside the horizon", price: 1_000_000, wantMonth: "2027-02", wantStatus: report.BreakEvenProjected},
		// June 2025 alone pays back 300,000. December 2024 (before purchase)
		// would have paid it back earlier and must not count.
		{name: "already in the past", price: 300_000, wantMonth: "2025-06", wantStatus: report.BreakEvenReached},
		{name: "beyond the horizon", price: 5_000_000, wantStatus: report.BreakEvenBeyondHorizon},
		// Running costs × 11 (330,000 a month) exceed every month's income.
		{name: "never, at a loss", price: 5_000_000, expenseChg: 1000, wantStatus: report.BreakEvenNotProfitable},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := baseProperty()
			p.PurchasePrice = i64(c.price)
			p.CapitalToDate = 0
			sc := sixMonths()
			sc.ExpenseChangePct = c.expenseChg
			inv := report.ProjectProperty(projStart, sc, p).Investment
			if inv.BreakEvenStatus != c.wantStatus {
				t.Fatalf("status = %s, want %s", inv.BreakEvenStatus, c.wantStatus)
			}
			switch {
			case c.wantMonth == "" && inv.BreakEvenMonth != nil:
				t.Errorf("month = %s, want null", *inv.BreakEvenMonth)
			case c.wantMonth != "" && (inv.BreakEvenMonth == nil || *inv.BreakEvenMonth != c.wantMonth):
				t.Errorf("month = %v, want %s", inv.BreakEvenMonth, c.wantMonth)
			}
			if c.wantStatus == report.BreakEvenNotProfitable {
				wantNilF(t, "payback at a loss", inv.PaybackYears)
			}
		})
	}
}

func TestProjectionWithoutPurchasePrice(t *testing.T) {
	p := baseProperty()
	got := report.ProjectProperty(projStart, sixMonths(), p)
	inv := got.Investment
	if inv.Total != nil || inv.BreakEvenMonth != nil || inv.BreakEvenStatus != report.BreakEvenNoPrice {
		t.Errorf("investment without a price = %+v", inv)
	}
	wantNilF(t, "roi trailing", inv.ROITrailingPct)
	wantNilF(t, "roi projected", inv.ROIProjectedPct)
	wantNilF(t, "payback", inv.PaybackYears)
	// Income, expenses and net still work, exactly as with a price.
	if got.Totals.Net != 778_500 || inv.ProjectedAnnualNet != 1_557_000 {
		t.Errorf("totals without a price = %+v, annual %d", got.Totals, inv.ProjectedAnnualNet)
	}
	// Yield needs only the current value.
	p.CurrentValue = i64(7_200_000)
	wantF(t, "yield", report.ProjectProperty(projStart, sixMonths(), p).Investment.YieldPct, 21.6)
}

func TestProjectionScenarioOverrides(t *testing.T) {
	p := baseProperty()
	hundred, half := 100.0, 50.0
	for _, c := range []struct {
		name       string
		sc         report.ProjectionScenario
		wantMonth0 int64 // income
		wantMonth3 int64
		wantExp    int64
	}{
		// Full occupancy, full collection, rents +10% on the open market only:
		// month 0 = 100,000 scheduled + 120,000 × 1.1; month 3 = both × 1.1.
		{
			name:       "occupancy, collection and rent",
			sc:         report.ProjectionScenario{HorizonMonths: 6, OccupancyPct: &hundred, CollectionRatePct: &hundred, RentChangePct: 10},
			wantMonth0: 232_000, wantMonth3: 242_000, wantExp: 30_000,
		},
		// Half collected: (100,000 + 90,000) × 0.5; (220,000 × 0.75) × 0.5.
		{
			name:       "collection only",
			sc:         report.ProjectionScenario{HorizonMonths: 6, CollectionRatePct: &half},
			wantMonth0: 95_000, wantMonth3: 82_500, wantExp: 30_000,
		},
		// Expenses −20%: 24,000 a month; income unchanged.
		{
			name:       "expenses",
			sc:         report.ProjectionScenario{HorizonMonths: 6, ExpenseChangePct: -20},
			wantMonth0: 171_000, wantMonth3: 148_500, wantExp: 24_000,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := report.ProjectProperty(projStart, c.sc, p)
			if got.Months[0].Income != c.wantMonth0 || got.Months[3].Income != c.wantMonth3 {
				t.Errorf("income = %d / %d, want %d / %d",
					got.Months[0].Income, got.Months[3].Income, c.wantMonth0, c.wantMonth3)
			}
			if got.Months[0].RunningExpenses != c.wantExp {
				t.Errorf("running = %d, want %d", got.Months[0].RunningExpenses, c.wantExp)
			}
		})
	}
	got := report.ProjectProperty(projStart, report.ProjectionScenario{HorizonMonths: 6, OccupancyPct: &half}, p)
	if got.Applied.OccupancySource != report.SourceScenario || got.Applied.OccupancyPct != 50 ||
		got.Applied.CollectionRateSource != report.SourceTrailing {
		t.Errorf("applied = %+v", got.Applied)
	}
}

func TestProjectionDefaultsAndHistory(t *testing.T) {
	// A young property: four months in the book, nothing ever due, no units
	// measured. Averages divide by four; both rates default to 100%.
	p := report.ProjectionProperty{
		ID: "young", HistoryMonths: 4,
		TrailingCollected: 200_000,
		TrailingRunning:   []report.CategoryAmount{{ID: "c", Name: "Cleaning", Amount: 120_000}},
		Units:             []report.ProjectionUnit{{MonthlyRent: 80_000, Lettable: true}, {MonthlyRent: 50_000, Lettable: false}},
	}
	got := report.ProjectProperty(projStart, report.ProjectionScenario{}, p)
	if len(got.Months) != report.ProjectionHorizonDefault {
		t.Errorf("default horizon = %d months, want %d", len(got.Months), report.ProjectionHorizonDefault)
	}
	a := got.Applied
	if a.CollectionRatePct != 100 || a.CollectionRateSource != report.SourceDefault ||
		a.OccupancyPct != 100 || a.OccupancySource != report.SourceDefault {
		t.Errorf("applied defaults = %+v", a)
	}
	wantNilF(t, "baseline collection", got.Baseline.CollectionRatePct)
	// 120,000 over 4 months = 30,000; the unlisted unit earns nothing.
	if m := got.Months[0]; m.Income != 80_000 || m.RunningExpenses != 30_000 || m.Net != 50_000 {
		t.Errorf("month 0 = %+v", m)
	}
	// Trailing net (200,000 − 120,000) over 4 months, annualised.
	if got.Investment.TrailingAnnualNet != 240_000 {
		t.Errorf("trailing annual net = %d, want 240000", got.Investment.TrailingAnnualNet)
	}
	// Without a purchase date every recorded month counts.
	p.PastNet = []report.MonthAmount{{Month: month(2020, time.March), Amount: 7}, {Month: month(2026, time.May), Amount: 3}}
	if c := report.ProjectProperty(projStart, report.ProjectionScenario{}, p).Investment.CashToDate; c != 10 {
		t.Errorf("cash to date without a purchase date = %d, want 10", c)
	}
	// Collected above expected is capped at 100%.
	p.TrailingExpected = 100_000
	wantF(t, "capped collection", report.ProjectProperty(projStart, report.ProjectionScenario{}, p).Baseline.CollectionRatePct, 100)
	// The horizon is bounded.
	if n := len(report.ProjectProperty(projStart, report.ProjectionScenario{HorizonMonths: 500}, p).Months); n != 120 {
		t.Errorf("horizon 500 → %d months, want 120", n)
	}
}

func TestProjectionPortfolio(t *testing.T) {
	priced := baseProperty()
	priced.PurchasePrice = i64(1_000_000)
	priced.CapitalToDate = 0
	priced.CurrentValue = i64(7_200_000)
	unpriced := report.ProjectionProperty{
		ID: "q", Name: "Kariakoo shops", HistoryMonths: 12,
		Units: []report.ProjectionUnit{{MonthlyRent: 50_000, Lettable: true}},
	}
	got, parts := report.ProjectPortfolio(projStart, sixMonths(), []report.ProjectionProperty{priced, unpriced})
	if len(parts) != 2 {
		t.Fatalf("parts = %d", len(parts))
	}

	// The monthly lines are the sum of the parts.
	for i, m := range got.Months {
		sum := parts[0].Months[i].Net + parts[1].Months[i].Net
		if m.Net != sum || m.Cumulative != parts[0].Months[i].Cumulative+parts[1].Months[i].Cumulative {
			t.Errorf("month %d net %d cumulative %d do not add up", i, m.Net, m.Cumulative)
		}
	}
	if got.Months[0].Net != 191_000 || got.Totals.Net != 778_500+300_000 {
		t.Errorf("portfolio month 0 net %d, total %d", got.Months[0].Net, got.Totals.Net)
	}

	// Investment covers the priced property only, and says so.
	inv := got.Investment
	if !inv.Incomplete || inv.PricedCount != 1 || len(inv.MissingPriceIDs) != 1 || inv.MissingPriceIDs[0] != "q" {
		t.Errorf("incomplete flag = %+v", inv)
	}
	if inv.Total == nil || *inv.Total != 1_000_000 {
		t.Fatalf("total = %v, want 1,000,000", inv.Total)
	}
	if inv.BreakEvenMonth == nil || *inv.BreakEvenMonth != "2027-02" {
		t.Errorf("portfolio break-even = %v, want 2027-02 (the priced property alone)", inv.BreakEvenMonth)
	}
	wantF(t, "portfolio roi projected", inv.ROIProjectedPct, 155.7) // 1,557,000 / 1,000,000
	wantF(t, "portfolio yield", inv.YieldPct, 21.6)                 // only the valued property
	if inv.ProjectedAnnualNet != 1_557_000+600_000 {
		t.Errorf("portfolio projected annual net = %d", inv.ProjectedAnnualNet)
	}

	// Aggregated baseline: 1,080,000 of 1,200,000; 18 of 24 unit-months.
	wantF(t, "portfolio collection", got.Baseline.CollectionRatePct, 90)
	wantF(t, "portfolio occupancy", got.Baseline.OccupancyPct, 75)
	if got.Baseline.UnitsTotal != 3 || got.Baseline.MarketRentMonthly != 170_000 {
		t.Errorf("portfolio baseline = %+v", got.Baseline)
	}

	// No property priced: no investment figures at all.
	none, _ := report.ProjectPortfolio(projStart, sixMonths(), []report.ProjectionProperty{unpriced})
	if none.Investment.Total != nil || none.Investment.BreakEvenStatus != report.BreakEvenNoPrice {
		t.Errorf("unpriced portfolio investment = %+v", none.Investment)
	}
}
