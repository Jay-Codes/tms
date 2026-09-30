package report_test

import (
	"testing"
	"time"

	"tms/backend/internal/report"
)

// Phase 28 — the projection engine against hand-computed fixtures (reworked
// 27 Sep 2026: units instead of an occupancy guess, returns against spend).
//
// The base property, worked by hand (start = October 2026, horizon 6):
//
//	trailing: collected 1,080,000 of 1,200,000 expected   → collection 90%
//	          running costs 240,000 + 120,000 over 12 mo.  → 30,000 a month
//	          capital 400,000
//	units:    a — let until month 3 at 100,000 (scheduled 100,000 × 3)
//	          b — vacant at 120,000
//	          c — under maintenance at 50,000
//
//	signed contracts only:
//	  months 0–2: 100,000 × 0.90 = 90,000; net 60,000
//	  months 3–5: nothing let;           net −30,000
//	  horizon:    income 270,000, running 180,000, net 90,000
//	  annual:     net 180,000 on 430,000 spent so far → ROI 41.9%
//	best case (a after its contract, b from now; c is not lettable):
//	  every month: (100,000 + 120,000) × 0.90 = 198,000; net 168,000
//
// Recorded months since the purchase (15 Jan 2025): June 2025 income 300,000;
// November 2025 income 500,000 and capital 400,000; September 2026 income
// 130,000 and running 30,000 — 930,000 in, 430,000 out, 500,000 ahead. The
// 1,000,000 of December 2024 predates the purchase and is ignored.

func month(y int, m time.Month) time.Time { return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC) }

func i64(v int64) *int64 { return &v }

var projStart = month(2026, time.October)

func baseProperty() report.ProjectionProperty {
	bought := time.Date(2025, time.January, 15, 0, 0, 0, 0, time.UTC)
	until := "2026-12-31"
	return report.ProjectionProperty{
		ID: "p", Name: "Mbezi Block A",
		PurchaseDate:      &bought,
		HistoryMonths:     12,
		TrailingCollected: 1_080_000,
		TrailingExpected:  1_200_000,
		TrailingRunning: []report.CategoryAmount{
			{ID: "c1", Name: "Repairs", Amount: 240_000},
			{ID: "c2", Name: "Utilities", Amount: 120_000},
		},
		TrailingCapital: 400_000,
		Past: []report.PastMonth{
			{Month: month(2024, time.December), Income: 1_000_000},
			{Month: month(2025, time.June), Income: 300_000},
			{Month: month(2025, time.November), Income: 500_000, Capital: 400_000},
			{Month: month(2026, time.September), Income: 130_000, Running: 30_000},
		},
		Scheduled: []int64{100_000, 100_000, 100_000},
		Units: []report.ProjectionUnit{
			{ID: "a", Name: "A1", Status: "occupied", MonthlyRent: 100_000, FreeFrom: 3, LetUntil: &until, Lettable: true},
			{ID: "b", Name: "A2", Status: "vacant", MonthlyRent: 120_000, Lettable: true},
			{ID: "c", Name: "A3", Status: "maintenance", MonthlyRent: 50_000},
		},
	}
}

// sixMonths counts from the purchase date, as the hand-worked figures above
// do; TestProjectionTotalExpenditure covers the default of counting everything.
func sixMonths() report.ProjectionScenario {
	return report.ProjectionScenario{HorizonMonths: 6, FromPurchase: true}
}

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

func withBasis(basis string, units ...string) report.ProjectionScenario {
	return report.ProjectionScenario{HorizonMonths: 6, Basis: basis, UnitIDs: units, FromPurchase: true}
}

func TestProjectionSignedContractsOnly(t *testing.T) {
	got := report.ProjectProperty(projStart, sixMonths(), baseProperty())

	if len(got.Months) != 6 || got.Months[0].Month != "2026-10" || got.Months[5].Month != "2027-03" {
		t.Fatalf("months = %+v", got.Months)
	}
	for i, m := range got.Months {
		wantIncome, wantNet := int64(90_000), int64(60_000)
		if i >= 3 {
			wantIncome, wantNet = 0, -30_000
		}
		if m.Income != wantIncome || m.RunningExpenses != 30_000 || m.Net != wantNet {
			t.Errorf("month %d = income %d, running %d, net %d; want %d, 30000, %d",
				i, m.Income, m.RunningExpenses, m.Net, wantIncome, wantNet)
		}
	}
	if got.Totals != (report.ProjectionTotals{Income: 270_000, RunningExpenses: 180_000, Net: 90_000}) {
		t.Errorf("totals = %+v", got.Totals)
	}
	// The cumulative lines start from what was made and spent since purchase.
	if m := got.Months[0]; m.CumulativeIncome != 1_020_000 || m.CumulativeSpent != 460_000 || m.Cumulative != 560_000 {
		t.Errorf("month 0 cumulative = %+v", m)
	}

	a := got.Applied
	if a.Basis != report.BasisContracts || a.CollectionRatePct != 90 || a.CollectionRateSource != report.SourceTrailing {
		t.Errorf("applied = %+v", a)
	}
	b := got.Baseline
	wantF(t, "baseline collection", b.CollectionRatePct, 90)
	if b.RunningMonthly != 30_000 || b.CapitalExpenses != 400_000 || b.Net != 720_000 {
		t.Errorf("baseline = %+v", b)
	}
	if b.UnitsTotal != 3 || b.UnitsLet != 1 || b.UnitsOpen != 1 || b.UnitsAssumed != 0 || b.AssumedRentMonthly != 0 {
		t.Errorf("baseline units = %+v", b)
	}
	if len(got.Units) != 3 || got.Units[0].Assumed || got.Units[0].LetUntil == nil || got.Units[0].PropertyName != "Mbezi Block A" {
		t.Errorf("unit rows = %+v", got.Units)
	}

	// Returns are measured against spend, with no purchase price needed.
	inv := got.Investment
	if inv.IncomeToDate != 930_000 || inv.RunningSpend != 30_000 || inv.CapitalSpend != 400_000 ||
		inv.SpentToDate != 430_000 || inv.CashToDate != 500_000 {
		t.Errorf("to date = %+v", inv)
	}
	if inv.ProjectedAnnualNet != 180_000 || inv.ProjectedAnnualExpenses != 360_000 || inv.TrailingAnnualNet != 720_000 {
		t.Errorf("annual = %+v", inv)
	}
	wantF(t, "roi projected", inv.ROIProjectedPct, 41.9) // 180,000 / 430,000 spent so far
	wantF(t, "roi trailing", inv.ROITrailingPct, 167.4)  // 720,000 / 430,000
	wantF(t, "payback once ahead", inv.PaybackYears, 0)  // already 500,000 ahead
	wantNilF(t, "yield without a current value", inv.YieldPct)
	if inv.BreakEvenStatus != report.BreakEvenReached || inv.BreakEvenMonth == nil || *inv.BreakEvenMonth != "2025-06" {
		t.Errorf("break-even = %v %s, want reached 2025-06", inv.BreakEvenMonth, inv.BreakEvenStatus)
	}
}

func TestProjectionBases(t *testing.T) {
	hundred := 100.0
	for _, c := range []struct {
		name         string
		sc           report.ProjectionScenario
		month0       int64 // income
		month3       int64
		assumed      int
		assumedRent  int64
		wantAssumeds []bool
	}{
		{
			name: "best case lets every lettable unit", sc: withBasis(report.BasisBestCase),
			month0: 198_000, month3: 198_000, assumed: 2, assumedRent: 220_000, wantAssumeds: []bool{true, true, false},
		},
		{
			// b from now; a's contract ends and nothing replaces it.
			name: "picked vacant unit", sc: withBasis(report.BasisSelected, "b"),
			month0: 198_000, month3: 108_000, assumed: 1, assumedRent: 120_000, wantAssumeds: []bool{false, true, false},
		},
		{
			// A landlord may pick a unit best case leaves out.
			name: "picked unit under maintenance", sc: withBasis(report.BasisSelected, "c", "not-ours"),
			month0: 135_000, month3: 45_000, assumed: 1, assumedRent: 50_000, wantAssumeds: []bool{false, false, true},
		},
		{
			// Picks are ignored outside "selected".
			name: "picks without the selected basis", sc: withBasis(report.BasisContracts, "b"),
			month0: 90_000, month3: 0, wantAssumeds: []bool{false, false, false},
		},
		{
			// Signed rent is kept; assumed lettings take the change.
			name: "rent change, full collection",
			sc: report.ProjectionScenario{HorizonMonths: 6, Basis: report.BasisBestCase,
				RentChangePct: 10, CollectionRatePct: &hundred},
			month0: 232_000, month3: 242_000, assumed: 2, assumedRent: 220_000, wantAssumeds: []bool{true, true, false},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := report.ProjectProperty(projStart, c.sc, baseProperty())
			if got.Months[0].Income != c.month0 || got.Months[3].Income != c.month3 {
				t.Errorf("income = %d / %d, want %d / %d", got.Months[0].Income, got.Months[3].Income, c.month0, c.month3)
			}
			if got.Baseline.UnitsAssumed != c.assumed || got.Baseline.AssumedRentMonthly != c.assumedRent {
				t.Errorf("assumed = %d at %d, want %d at %d",
					got.Baseline.UnitsAssumed, got.Baseline.AssumedRentMonthly, c.assumed, c.assumedRent)
			}
			for i, u := range got.Units {
				if u.Assumed != c.wantAssumeds[i] {
					t.Errorf("unit %s assumed = %v, want %v", u.ID, u.Assumed, c.wantAssumeds[i])
				}
			}
		})
	}
	// An unknown basis is signed contracts.
	if got := report.ProjectProperty(projStart, withBasis("wishful"), baseProperty()); got.Applied.Basis != report.BasisContracts {
		t.Errorf("unknown basis applied as %s", got.Applied.Basis)
	}
	// Expenses −20%: 24,000 a month.
	sc := sixMonths()
	sc.ExpenseChangePct = -20
	if got := report.ProjectProperty(projStart, sc, baseProperty()); got.Months[0].RunningExpenses != 24_000 {
		t.Errorf("running = %d, want 24000", got.Months[0].RunningExpenses)
	}
}

func TestProjectionBreakEven(t *testing.T) {
	for _, c := range []struct {
		name        string
		price       int64
		sc          report.ProjectionScenario
		wantMonth   string
		wantStatus  string
		wantPayback *float64
	}{
		// −1,000,000; +300,000, +100,000, +100,000 → −500,000; then +168,000 a
		// month: −332,000, −164,000, +4,000 in December 2026.
		{name: "inside the horizon", price: 1_000_000, sc: withBasis(report.BasisBestCase),
			wantMonth: "2026-12", wantStatus: report.BreakEvenProjected, wantPayback: f64(0.2)}, // 500,000 / 2,016,000
		// June 2025 alone covers 300,000. December 2024 (before purchase) must not count.
		{name: "already in the past", price: 300_000, sc: sixMonths(), wantMonth: "2025-06", wantStatus: report.BreakEvenReached, wantPayback: f64(0)},
		// −1,500,000 behind at +180,000 a year.
		{name: "beyond the horizon", price: 2_000_000, sc: sixMonths(), wantStatus: report.BreakEvenBeyondHorizon, wantPayback: f64(8.3)},
		// Running costs × 11 (330,000 a month) exceed every month's income.
		{name: "never, at a loss", price: 5_000_000, sc: report.ProjectionScenario{HorizonMonths: 6, ExpenseChangePct: 1000, FromPurchase: true},
			wantStatus: report.BreakEvenNotProfitable},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := baseProperty()
			p.PurchasePrice = i64(c.price)
			inv := report.ProjectProperty(projStart, c.sc, p).Investment
			if inv.BreakEvenStatus != c.wantStatus {
				t.Fatalf("status = %s, want %s", inv.BreakEvenStatus, c.wantStatus)
			}
			switch {
			case c.wantMonth == "" && inv.BreakEvenMonth != nil:
				t.Errorf("month = %s, want null", *inv.BreakEvenMonth)
			case c.wantMonth != "" && (inv.BreakEvenMonth == nil || *inv.BreakEvenMonth != c.wantMonth):
				t.Errorf("month = %v, want %s", inv.BreakEvenMonth, c.wantMonth)
			}
			if c.wantPayback == nil {
				wantNilF(t, "payback", inv.PaybackYears)
			} else {
				wantF(t, "payback", inv.PaybackYears, *c.wantPayback)
			}
			if inv.SpentToDate != 430_000+c.price {
				t.Errorf("spent to date = %d, want the purchase price added", inv.SpentToDate)
			}
		})
	}
}

func f64(v float64) *float64 { return &v }

func TestProjectionNothingSpent(t *testing.T) {
	// Income and no costs at all: nothing to break even on, no ROI.
	p := report.ProjectionProperty{
		ID: "free", HistoryMonths: 12, TrailingCollected: 600_000,
		Past:      []report.PastMonth{{Month: month(2026, time.May), Income: 600_000}},
		Scheduled: []int64{50_000, 50_000},
	}
	inv := report.ProjectProperty(projStart, sixMonths(), p).Investment
	if inv.BreakEvenStatus != report.BreakEvenNoCosts || inv.BreakEvenMonth != nil {
		t.Errorf("break-even = %v %s, want no_costs", inv.BreakEvenMonth, inv.BreakEvenStatus)
	}
	wantNilF(t, "roi projected", inv.ROIProjectedPct)
	wantNilF(t, "roi trailing", inv.ROITrailingPct)
	wantNilF(t, "payback", inv.PaybackYears)

	// Nothing spent yet, but the forecast dips (60,000 a month of costs) and
	// comes back: month 0 −60,000, month 1 +140,000.
	q := report.ProjectionProperty{
		ID: "new", HistoryMonths: 1,
		TrailingRunning: []report.CategoryAmount{{ID: "c", Name: "Cleaning", Amount: 60_000}},
		Scheduled:       []int64{0, 200_000},
	}
	inv = report.ProjectProperty(projStart, report.ProjectionScenario{HorizonMonths: 3}, q).Investment
	if inv.BreakEvenStatus != report.BreakEvenProjected || inv.BreakEvenMonth == nil || *inv.BreakEvenMonth != "2026-11" {
		t.Errorf("break-even = %v %s, want projected 2026-11", inv.BreakEvenMonth, inv.BreakEvenStatus)
	}
}

func TestProjectionDefaultsAndHistory(t *testing.T) {
	// A young property: four months in the book, nothing ever due. Averages
	// divide by four; the collection rate defaults to 100%.
	p := report.ProjectionProperty{
		ID: "young", HistoryMonths: 4,
		TrailingCollected: 200_000,
		TrailingRunning:   []report.CategoryAmount{{ID: "c", Name: "Cleaning", Amount: 120_000}},
		Units: []report.ProjectionUnit{
			{ID: "u1", MonthlyRent: 80_000, Lettable: true},
			{ID: "u2", MonthlyRent: 50_000, Lettable: false},
		},
	}
	got := report.ProjectProperty(projStart, report.ProjectionScenario{Basis: report.BasisBestCase}, p)
	if len(got.Months) != report.ProjectionHorizonDefault {
		t.Errorf("default horizon = %d months, want %d", len(got.Months), report.ProjectionHorizonDefault)
	}
	if a := got.Applied; a.CollectionRatePct != 100 || a.CollectionRateSource != report.SourceDefault {
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
	p.Past = []report.PastMonth{{Month: month(2020, time.March), Income: 7}, {Month: month(2026, time.May), Income: 5, Running: 2}}
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
	// Yield needs only the current value: 180,000 a year on 7,200,000.
	base := baseProperty()
	base.CurrentValue = i64(7_200_000)
	wantF(t, "yield", report.ProjectProperty(projStart, sixMonths(), base).Investment.YieldPct, 2.5)
}

func TestProjectionPortfolio(t *testing.T) {
	priced := baseProperty()
	priced.PurchasePrice = i64(1_000_000)
	priced.CurrentValue = i64(7_200_000)
	unpriced := report.ProjectionProperty{
		ID: "q", Name: "Kariakoo shops", HistoryMonths: 12,
		Units: []report.ProjectionUnit{{ID: "q1", MonthlyRent: 50_000, Lettable: true}},
	}
	sc := withBasis(report.BasisBestCase)
	got, parts := report.ProjectPortfolio(projStart, sc, []report.ProjectionProperty{priced, unpriced})
	if len(parts) != 2 {
		t.Fatalf("parts = %d", len(parts))
	}

	// The monthly lines are the sum of the parts.
	for i, m := range got.Months {
		a, b := parts[0].Months[i], parts[1].Months[i]
		if m.Net != a.Net+b.Net || m.Cumulative != a.Cumulative+b.Cumulative ||
			m.CumulativeIncome != a.CumulativeIncome+b.CumulativeIncome || m.CumulativeSpent != a.CumulativeSpent+b.CumulativeSpent {
			t.Errorf("month %d does not add up: %+v", i, m)
		}
	}
	// 168,000 + 50,000 (the unpriced shop has no history: 100% collection).
	if got.Months[0].Net != 218_000 || got.Totals.Net != 6*218_000 {
		t.Errorf("portfolio month 0 net %d, total %d", got.Months[0].Net, got.Totals.Net)
	}
	if len(got.Units) != 4 || got.Units[3].PropertyName != "Kariakoo shops" || !got.Units[3].Assumed {
		t.Errorf("portfolio units = %+v", got.Units)
	}

	// Every property counts toward the returns; the purchase price adds to spend.
	inv := got.Investment
	if inv.PurchasePrice == nil || *inv.PurchasePrice != 1_000_000 || inv.SpentToDate != 1_430_000 || inv.CashToDate != -500_000 {
		t.Fatalf("investment = %+v", inv)
	}
	// −500,000 + 218,000 a month: −282,000, −64,000, +154,000 in December.
	if inv.BreakEvenStatus != report.BreakEvenProjected || inv.BreakEvenMonth == nil || *inv.BreakEvenMonth != "2026-12" {
		t.Errorf("portfolio break-even = %v %s", inv.BreakEvenMonth, inv.BreakEvenStatus)
	}
	if inv.ProjectedAnnualNet != 2_616_000 || inv.ProjectedAnnualExpenses != 360_000 {
		t.Errorf("portfolio annual = %+v", inv)
	}
	wantF(t, "portfolio roi projected", inv.ROIProjectedPct, 182.9) // 2,616,000 / 1,430,000 spent
	wantF(t, "portfolio yield", inv.YieldPct, 28)                   // only the valued property: 2,016,000 / 7,200,000
	wantF(t, "portfolio collection", got.Baseline.CollectionRatePct, 90)
	if got.Baseline.UnitsTotal != 4 || got.Baseline.UnitsAssumed != 3 || got.Baseline.AssumedRentMonthly != 270_000 {
		t.Errorf("portfolio baseline = %+v", got.Baseline)
	}
}

func TestProjectionMonthlyExpenseEstimate(t *testing.T) {
	est := int64(50_000)
	sc := sixMonths()
	sc.MonthlyExpenses = &est
	sc.ExpenseChangePct = 100 // ignored once an estimate is given
	got := report.ProjectProperty(projStart, sc, baseProperty())
	for i, m := range got.Months {
		if m.RunningExpenses != 50_000 {
			t.Errorf("month %d running = %d, want the 50,000 estimate", i, m.RunningExpenses)
		}
	}
	if a := got.Applied; a.MonthlyExpenses != 50_000 || a.MonthlyExpensesSource != report.SourceScenario {
		t.Errorf("applied = %+v", a)
	}
	// Months 0–2: 90,000 − 50,000; months 3–5: −50,000 → −30,000 over six
	// months, −60,000 a year, on 430,000 spent.
	if inv := got.Investment; inv.ProjectedAnnualNet != -60_000 {
		t.Errorf("annual net = %d, want -60000", inv.ProjectedAnnualNet)
	}
	wantF(t, "roi on the estimate", got.Investment.ROIProjectedPct, -14)

	// Without an estimate: the trailing average, labelled as such.
	if a := report.ProjectProperty(projStart, sixMonths(), baseProperty()).Applied; a.MonthlyExpenses != 30_000 || a.MonthlyExpensesSource != report.SourceTrailing {
		t.Errorf("default applied = %+v", a)
	}

	// Across a portfolio the estimate is shared by trailing running costs
	// (the shop has none, so the block carries it all) and adds back up.
	shop := report.ProjectionProperty{ID: "q", HistoryMonths: 12, Units: []report.ProjectionUnit{{ID: "q1", MonthlyRent: 50_000, Lettable: true}}}
	total := int64(100_000)
	psc := sixMonths()
	psc.MonthlyExpenses = &total
	all, parts := report.ProjectPortfolio(projStart, psc, []report.ProjectionProperty{baseProperty(), shop})
	if all.Months[0].RunningExpenses != 100_000 || parts[0].Months[0].RunningExpenses != 100_000 || parts[1].Months[0].RunningExpenses != 0 {
		t.Errorf("shares = %d + %d, portfolio %d",
			parts[0].Months[0].RunningExpenses, parts[1].Months[0].RunningExpenses, all.Months[0].RunningExpenses)
	}
	if all.Applied.MonthlyExpenses != 100_000 || all.Applied.MonthlyExpensesSource != report.SourceScenario {
		t.Errorf("portfolio applied = %+v", all.Applied)
	}
	// No running history anywhere: shared by units (1 : 3 here), exactly.
	young := baseProperty()
	young.TrailingRunning = nil
	odd := int64(100_001)
	psc.MonthlyExpenses = &odd
	_, parts = report.ProjectPortfolio(projStart, psc, []report.ProjectionProperty{shop, young})
	if a, b := parts[0].Months[0].RunningExpenses, parts[1].Months[0].RunningExpenses; a != 25_000 || a+b != 100_001 {
		t.Errorf("unit shares = %d + %d, want 25,000 + 75,001", a, b)
	}
}

func TestProjectionTotalExpenditure(t *testing.T) {
	// The default counts every recorded month, the December 2024 one before
	// the purchase included: 1,930,000 in, 430,000 out.
	all := report.ProjectionScenario{HorizonMonths: 6}
	inv := report.ProjectProperty(projStart, all, baseProperty()).Investment
	if inv.IncomeToDate != 1_930_000 || inv.SpentToDate != 430_000 || inv.ROISpend != 430_000 {
		t.Errorf("everything counted = %+v", inv)
	}
	// Old expenses are never dropped by default: a 50,000 repair before the
	// purchase is still spend.
	p := baseProperty()
	p.Past[0].Running = 50_000
	if got := report.ProjectProperty(projStart, all, p).Investment.SpentToDate; got != 480_000 {
		t.Errorf("spent with a pre-purchase repair = %d, want 480000", got)
	}
	// …unless the landlord asks to count from the purchase.
	from := all
	from.FromPurchase = true
	if got := report.ProjectProperty(projStart, from, p).Investment; got.SpentToDate != 430_000 || got.IncomeToDate != 930_000 {
		t.Errorf("from purchase = %+v", got)
	}
	if a := report.ProjectProperty(projStart, from, p).Applied; !a.FromPurchase || a.IncludeFutureExpenses {
		t.Errorf("applied flags = %+v", a)
	}

	// Including the coming year's expenses: 430,000 + 360,000 = 790,000.
	fut := all
	fut.IncludeFutureExpenses = true
	got := report.ProjectProperty(projStart, fut, baseProperty()).Investment
	if got.ROISpend != 790_000 {
		t.Errorf("roi spend = %d, want 790000", got.ROISpend)
	}
	wantF(t, "roi with future expenses", got.ROIProjectedPct, 22.8) // 180,000 / 790,000
	if got.SpentToDate != 430_000 {
		t.Errorf("spent to date moved to %d; only ROI's total should", got.SpentToDate)
	}
	port, _ := report.ProjectPortfolio(projStart, fut, []report.ProjectionProperty{baseProperty()})
	if port.Investment.ROISpend != 790_000 {
		t.Errorf("portfolio roi spend = %d", port.Investment.ROISpend)
	}
}
