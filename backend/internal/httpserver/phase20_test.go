package httpserver_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"tms/backend/internal/notify"
)

// Phase 20 — proof amount lock (§20.1) and the backfill of history that
// predates TMS (§20.3).
//
// The backfill's whole justification is that a rent book must be able to say
// whether a period was ever paid. So the assertions are about the book: which
// rows closed, what the money's `paid_at` is, and which period the reports count
// it in — not merely that the endpoint answered 200.

// proofSize is a payload comfortably inside the 5 MiB ceiling.
const proofSize = 2048

// ------------------------------------------------- 20.1 proof amount lock --

// TestPhase20ProofAmountMustMatchTheInstalment: the renter's sheet renders the
// figure rather than offering a box, so a mismatch means the row moved.
func TestPhase20ProofAmountMustMatchTheInstalment(t *testing.T) {
	h := newHarness(t)
	fix := h.newProofFixture(t, "ProofLock", "0720000100", "+255720000101")

	// The right amount, aimed at the row: accepted.
	fix.requireStorage(t)
	ok := fix.submitProof(t, proofSize, map[string]any{
		"schedule_id": fix.scheduleIDs[0], "amount": fix.amounts[0],
	})
	ok.mustStatus(t, http.StatusCreated, "proof for the row's own balance")

	// A typo: refused, with the figure the client should have sent.
	bad := fix.submitProof(t, proofSize, map[string]any{
		"schedule_id": fix.scheduleIDs[0], "amount": fix.amounts[0] - 1,
	})
	if bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("mismatched amount status = %d, want 422 — body: %s", bad.Code, bad.Raw)
	}
	if got := bad.str(t, "type"); got != "amount_mismatch" {
		t.Errorf("type = %q, want amount_mismatch", got)
	}
	if got := int64(mustFloat(t, bad.Body, "expected")); got != fix.amounts[0] {
		t.Errorf("expected = %d, want %d", got, fix.amounts[0])
	}

	// Without a schedule_id there is nothing to lock the amount to, and the
	// Phase 16 rule stands: any amount within the contract balance.
	fix.submitProof(t, proofSize, map[string]any{"amount": fix.amounts[0] - 1}).
		mustStatus(t, http.StatusCreated, "proof with no schedule named")
}

// TestPhase20ProofAmountFollowsTheRowAsItMoves: once the landlord has taken part
// of the period in cash, the locked figure is the remainder, not the original.
func TestPhase20ProofAmountFollowsTheRowAsItMoves(t *testing.T) {
	h := newHarness(t)
	fix := h.newProofFixture(t, "ProofMoves", "0720000200", "+255720000201")
	fix.requireStorage(t)

	part := fix.amounts[0] / 4
	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "schedule_id": fix.scheduleIDs[0],
		"amount": part, "method": "cash",
	}).mustStatus(t, http.StatusCreated, "landlord takes part in cash")

	stale := fix.submitProof(t, proofSize, map[string]any{
		"schedule_id": fix.scheduleIDs[0], "amount": fix.amounts[0],
	})
	if stale.Code != http.StatusUnprocessableEntity {
		t.Fatalf("stale amount status = %d, want 422 — body: %s", stale.Code, stale.Raw)
	}
	if got := int64(mustFloat(t, stale.Body, "expected")); got != fix.amounts[0]-part {
		t.Errorf("expected = %d, want the remainder %d", got, fix.amounts[0]-part)
	}
	fix.submitProof(t, proofSize, map[string]any{
		"schedule_id": fix.scheduleIDs[0], "amount": fix.amounts[0] - part,
	}).mustStatus(t, http.StatusCreated, "proof for the remainder")
}

// --------------------------------------------------- 20.3 past start dates --

// TestPhase20LandlordContractMayStartInThePastButAnApplicationMayNot is the
// asymmetry the phase turns on: the landlord states the truth about a tenancy
// they already have; a renter may not draft a back-dated one alone.
func TestPhase20LandlordContractMayStartInThePastButAnApplicationMayNot(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "PastStart", "0720001000", "+255720001001")

	twoYearsAgo := time.Now().UTC().AddDate(-2, 0, 0).Format("2006-01-02")
	created := fix.owner.contractOn(t, map[string]any{
		"unit_id": fix.unitIDs[1], "renter_user_id": fix.renterID,
		"payment_period_id": fix.periodID, "term_days": 1095, "start_date": twoYearsAgo,
	})
	created.mustStatus(t, http.StatusCreated, "contract starting two years back")
	if got := created.str(t, "contract", "start_date"); got != twoYearsAgo {
		t.Errorf("start_date = %q, want %q", got, twoYearsAgo)
	}

	// Eleven years is past the ten-year bound.
	elevenYears := time.Now().UTC().AddDate(-11, 0, 0).Format("2006-01-02")
	fix.owner.contractOn(t, map[string]any{
		"unit_id": fix.unitIDs[2], "renter_user_id": fix.renterID,
		"payment_period_id": fix.periodID, "term_days": 365, "start_date": elevenYears,
	}).mustStatus(t, http.StatusBadRequest, "contract starting eleven years back")

	// The renter's own application keeps the 7-day backstop.
	fix.renter.do(http.MethodPost, "/units/"+fix.unitCodes[3]+"/link", map[string]any{
		"payment_period_id": fix.periodID, "term_days": 365,
		"start_date": twoYearsAgo, "accepted_terms": true,
	}).mustStatus(t, http.StatusBadRequest, "renter applying with a back-dated start")
}

// ----------------------------------------------------------- 20.3 backfill --

// backfilled is a contract that started a year ago and has never been paid: the
// state a landlord joining TMS mid-tenancy actually arrives in.
type backfilled struct {
	paymentFixture
	unpaid int
}

func (h *harness) newBackfillFixture(t *testing.T, tag, ownerPhone, renterPhone string) backfilled {
	t.Helper()
	fix := h.newPaymentFixture(t, tag, ownerPhone, renterPhone)
	// Walking the whole tenancy back a year is how one test watches a year of
	// rent fall due; the generator's own rows are left alone otherwise.
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE payment_schedules
		    SET period_start = period_start - interval '1 year',
		        period_end   = period_end   - interval '1 year',
		        due_date     = due_date     - interval '1 year'
		  WHERE contract_id = $1`, fix.contractID); err != nil {
		t.Fatalf("backdate the rent book: %v", err)
	}
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE contracts SET start_date = start_date - interval '1 year',
		                      end_date   = end_date   - interval '1 year'
		  WHERE id = $1`, fix.contractID); err != nil {
		t.Fatalf("backdate the contract: %v", err)
	}
	return backfilled{paymentFixture: fix, unpaid: len(fix.scheduleIDs)}
}

// TestPhase20BackfillSettlesEveryPastPeriodOnce is the main path: one call, one
// payment per period, one text.
func TestPhase20BackfillSettlesEveryPastPeriodOnce(t *testing.T) {
	h := newHarness(t)
	fix := h.newBackfillFixture(t, "Backfill", "0720002000", "+255720002001")
	today := time.Now().UTC().Format("2006-01-02")

	done := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill",
		map[string]any{"until": today, "mode": "paid", "method": "cash",
			"reference": "OLD-BOOK", "note": "reconstructed from the paper book"}).
		mustStatus(t, http.StatusOK, "backfill paid")

	settled := int(mustFloat(t, done.Body, "settled"))
	if settled != fix.unpaid {
		t.Fatalf("settled = %d, want every one of the %d past periods", settled, fix.unpaid)
	}
	if got := int(mustFloat(t, done.Body, "skipped")); got != 0 {
		t.Errorf("skipped = %d, want 0 on a book where nothing was paid", got)
	}
	var want int64
	for _, a := range fix.amounts {
		want += a
	}
	if got := int64(mustFloat(t, done.Body, "total")); got != want {
		t.Errorf("total = %d, want %d", got, want)
	}

	// Every row is paid, and each carries the chip.
	for i, status := range fix.statuses(t) {
		if status != "paid" {
			t.Errorf("schedule %d status = %q, want paid", i, status)
		}
	}
	for _, row := range listOf(t, fix.owner.do(http.MethodGet,
		"/contracts/"+fix.contractID+"/schedules", nil).
		mustStatus(t, http.StatusOK, "schedules")) {
		if got, _ := row["last_payment_source"].(string); got != "backfill" {
			t.Errorf("last_payment_source = %q, want backfill", got)
		}
	}

	// One payment per period, each dated to its own due date — which is the
	// point: the money is counted in the month it was owed, not today.
	payments := listOf(t, fix.owner.do(http.MethodGet,
		"/payments?contract_id="+fix.contractID+"&source=backfill", nil).
		mustStatus(t, http.StatusOK, "payments filtered by source"))
	if len(payments) != settled {
		t.Fatalf("backfill payments = %d, want %d", len(payments), settled)
	}
	for _, p := range payments {
		if got, _ := p["source"].(string); got != "backfill" {
			t.Errorf("payment source = %q, want backfill", got)
		}
		paidAt, _ := p["paid_at"].(string)
		if strings.HasPrefix(paidAt, time.Now().UTC().Format("2006-01-02")) {
			t.Errorf("paid_at = %q, want the period's own due date, not today", paidAt)
		}
	}
	// And the filter is a filter: manual money is still there beside it.
	if rows := listOf(t, fix.owner.do(http.MethodGet,
		"/payments?contract_id="+fix.contractID+"&source=manual", nil).
		mustStatus(t, http.StatusOK, "manual filter")); len(rows) != 0 {
		t.Errorf("manual payments = %d, want 0 — nothing was keyed in by hand", len(rows))
	}
	fix.owner.do(http.MethodGet, "/payments?source=nonsense", nil).
		mustStatus(t, http.StatusBadRequest, "an unknown source")

	// Exactly one SMS for the lot, and no thank-you per period.
	if sent := ofKind(h.notifications(t), notify.KindBackfillDone); len(sent) != 1 {
		t.Fatalf("backfill_done messages = %d, want exactly 1", len(sent))
	}
	if sent := ofKind(h.notifications(t), notify.KindThankYou); len(sent) != 0 {
		t.Errorf("thank_you messages = %d, want 0 — a backfill is not news", len(sent))
	}

	// One audit row for the decision, one per payment for the ledger.
	if rows := h.auditPayloads(t, "contract.backfill"); len(rows) != 1 {
		t.Fatalf("contract.backfill rows = %d, want 1", len(rows))
	} else if !strings.Contains(rows[0], `"paid"`) {
		t.Errorf("audit payload = %s, want the mode in it", rows[0])
	}
	if rows := h.auditPayloads(t, "payment.record"); len(rows) != settled {
		t.Errorf("payment.record rows = %d, want %d", len(rows), settled)
	}

	// A second run finds nothing left and says so, without a second message.
	again := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill",
		map[string]any{"until": today, "mode": "paid"}).
		mustStatus(t, http.StatusOK, "backfill again")
	if got := int(mustFloat(t, again.Body, "settled")); got != 0 {
		t.Errorf("second run settled = %d, want 0", got)
	}
	if got := int(mustFloat(t, again.Body, "skipped")); got != settled {
		t.Errorf("second run skipped = %d, want %d", got, settled)
	}
	if sent := ofKind(h.notifications(t), notify.KindBackfillDone); len(sent) != 1 {
		t.Errorf("backfill_done messages after a second run = %d, want 1", len(sent))
	}
}

// TestPhase20BackfillWaivesAndSkips: the other mode, and the rows it must leave
// alone.
func TestPhase20BackfillWaivesAndSkips(t *testing.T) {
	h := newHarness(t)
	fix := h.newBackfillFixture(t, "BackfillWaive", "0720002100", "+255720002101")
	today := time.Now().UTC().Format("2006-01-02")

	// One period was genuinely paid, and one partly.
	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "schedule_id": fix.scheduleIDs[0],
		"amount": fix.amounts[0], "method": "cash",
	}).mustStatus(t, http.StatusCreated, "settle the first period for real")
	part := fix.amounts[1] / 2
	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "schedule_id": fix.scheduleIDs[1],
		"amount": part, "method": "cash",
	}).mustStatus(t, http.StatusCreated, "half-settle the second")

	fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill",
		map[string]any{"until": today, "mode": "waived"}).
		mustStatus(t, http.StatusBadRequest, "waive with no reason")

	done := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill",
		map[string]any{"until": today, "mode": "waived", "note": "tenant left; written off"}).
		mustStatus(t, http.StatusOK, "backfill waived")

	if got := int(mustFloat(t, done.Body, "skipped")); got != 1 {
		t.Errorf("skipped = %d, want 1 — the period that was really paid", got)
	}
	if got := int64(mustFloat(t, done.Body, "total")); got != 0 {
		t.Errorf("total = %d, want 0 on a waiver", got)
	}
	statuses := fix.statuses(t)
	if statuses[0] != "paid" {
		t.Errorf("the genuinely paid period is now %q, want paid", statuses[0])
	}
	// A partial row is not "answered": it is settled, which here means waived
	// for the remainder.
	if statuses[1] != "waived" {
		t.Errorf("the half-paid period is now %q, want waived", statuses[1])
	}
	// The chip on the half-paid row still follows the money that reached it.
	for _, row := range listOf(t, fix.owner.do(http.MethodGet,
		"/contracts/"+fix.contractID+"/schedules", nil).
		mustStatus(t, http.StatusOK, "schedules")) {
		id, _ := row["id"].(string)
		src, _ := row["last_payment_source"].(string)
		switch id {
		case fix.scheduleIDs[0], fix.scheduleIDs[1]:
			if src != "manual" {
				t.Errorf("schedule %s last_payment_source = %q, want manual", id, src)
			}
		default:
			if src != "" {
				t.Errorf("a waived period with no money carries source %q, want null", src)
			}
		}
	}
}

// TestPhase20BackfillRefusals covers the three ways the call is refused.
func TestPhase20BackfillRefusals(t *testing.T) {
	h := newHarness(t)
	fix := h.newBackfillFixture(t, "BackfillNo", "0720002200", "+255720002201")

	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	future := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill",
		map[string]any{"until": tomorrow, "mode": "paid"})
	if future.Code != http.StatusUnprocessableEntity {
		t.Errorf("until in the future status = %d, want 422 — body: %s", future.Code, future.Raw)
	}

	longAgo := time.Now().UTC().AddDate(-5, 0, 0).Format("2006-01-02")
	early := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill",
		map[string]any{"until": longAgo, "mode": "paid"})
	if early.Code != http.StatusUnprocessableEntity {
		t.Errorf("until before the start date status = %d, want 422 — body: %s", early.Code, early.Raw)
	}

	h.setContractStatus(t, fix.contractID, "ended")
	ended := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill",
		map[string]any{"until": time.Now().UTC().Format("2006-01-02"), "mode": "paid"})
	if ended.Code != http.StatusConflict {
		t.Fatalf("ended contract status = %d, want 409 — body: %s", ended.Code, ended.Raw)
	}
	if got := ended.str(t, "type"); got != "contract_not_active" {
		t.Errorf("type = %q, want contract_not_active", got)
	}
}

// TestPhase20BackfillIsCountedInThePeriodItWasPaid is the reports rule: `paid_at`
// is what a collection figure buckets by, so a backfilled year lands across that
// year rather than all in today.
func TestPhase20BackfillIsCountedInThePeriodItWasPaid(t *testing.T) {
	h := newHarness(t)
	fix := h.newBackfillFixture(t, "BackfillReport", "0720002300", "+255720002301")
	today := time.Now().UTC()

	fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill",
		map[string]any{"until": today.Format("2006-01-02"), "mode": "paid"}).
		mustStatus(t, http.StatusOK, "backfill a year")

	// This month collected nothing: every payment is dated to an old due date,
	// which is the whole reason `paid_at` exists.
	thisMonth := fix.owner.do(http.MethodGet, "/reports/summary", nil).
		mustStatus(t, http.StatusOK, "summary for this month")
	if got := int64(num(t, thisMonth, "period", "collected")); got != 0 {
		t.Errorf("collected this month = %d, want 0 — the money is dated to the periods it settled", got)
	}

	// The whole span does see it.
	wide := fix.owner.do(http.MethodGet, "/reports/summary?cadence=custom&from="+
		today.AddDate(-2, 0, 0).Format("2006-01-02")+"&to="+
		today.Format("2006-01-02"), nil).
		mustStatus(t, http.StatusOK, "summary across the backfilled year")
	var want int64
	for _, a := range fix.amounts {
		want += a
	}
	if got := int64(num(t, wide, "period", "collected")); got != want {
		t.Errorf("collected across the span = %d, want %d", got, want)
	}
}

// TestPhase20RenterSeesTheBackfillChip: money recorded on the renter's behalf is
// money whose provenance they can see.
func TestPhase20RenterSeesTheBackfillChip(t *testing.T) {
	h := newHarness(t)
	fix := h.newBackfillFixture(t, "BackfillRenter", "0720002400", "+255720002401")

	fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill",
		map[string]any{"until": time.Now().UTC().Format("2006-01-02"), "mode": "paid"}).
		mustStatus(t, http.StatusOK, "backfill")

	mine := fix.renter.do(http.MethodGet, "/me/schedules", nil).
		mustStatus(t, http.StatusOK, "renter schedules")
	rows := listOf(t, mine)
	if len(rows) == 0 {
		t.Fatal("renter sees no schedules")
	}
	for _, row := range rows {
		if got, _ := row["last_payment_source"].(string); got != "backfill" {
			t.Errorf("renter row last_payment_source = %q, want backfill", got)
		}
	}
}

// TestPhase20PaymentsCSVCarriesTheSource pins the export column.
func TestPhase20PaymentsCSVCarriesTheSource(t *testing.T) {
	h := newHarness(t)
	fix := h.newBackfillFixture(t, "BackfillCSV", "0720002500", "+255720002501")
	fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill",
		map[string]any{"until": time.Now().UTC().Format("2006-01-02"), "mode": "paid"}).
		mustStatus(t, http.StatusOK, "backfill")

	csv := fix.owner.do(http.MethodGet, "/payments?format=csv", nil).
		mustStatus(t, http.StatusOK, "payments CSV")
	if ct := csv.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
	lines := strings.Split(strings.TrimSpace(csv.Raw), "\n")
	if !strings.Contains(lines[0], "source") {
		t.Fatalf("CSV header = %q, want a source column", lines[0])
	}
	if len(lines) < 2 || !strings.Contains(lines[1], "backfill") {
		t.Errorf("CSV rows = %v, want a backfill row", lines)
	}
}

// TestPhase20ImportPointsAtBackfillForMoneyBeforeTheRentBook: the hint that
// makes the failure fixable instead of mysterious.
func TestPhase20ImportPointsAtBackfillForMoneyBeforeTheRentBook(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "ImportHint", "0720003000", "+255720003001")

	// The book is settled in full, so the sheet's row has nothing left to land
	// on — the state a landlord importing history actually hits, because the
	// allocator walks forward from the earliest unpaid period and does not care
	// what date the money carries.
	var whole int64
	for _, a := range fix.amounts {
		whole += a
	}
	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": whole, "method": "cash",
		"allow_overpay_rollover": true,
	}).mustStatus(t, http.StatusCreated, "settle the whole book")

	// The sheet then claims rent paid before the book begins.
	lastYear := time.Now().UTC().AddDate(-1, 0, 0).Format("2006-01-02")
	body := "unit,renter_phone,amount,paid_at,method\n" +
		"Room 1," + fix.renterPhone + "," + strconv.FormatInt(fix.amounts[0], 10) + "," + lastYear + ",cash\n"
	preview := fix.owner.importPreview(t, "payments", "old.csv", body).
		mustStatus(t, http.StatusCreated, "preview payments")

	rows := arrayOf(t, preview, "rows")
	if len(rows) != 1 {
		t.Fatalf("preview rows = %d, want 1 — body: %s", len(rows), preview.Raw)
	}
	errs, _ := rows[0]["errors"].(map[string]any)
	if errs == nil {
		t.Fatalf("the row was accepted; a payment before the rent book cannot be — body: %s", preview.Raw)
	}
	hint, _ := errs["paid_at"].(string)
	if !strings.Contains(hint, "Backfill") {
		t.Errorf("paid_at error = %q, want the Backfill hint", hint)
	}
}

// TestPhase20RentersImportAcceptsARealMoveInDate: a landlord loading an existing
// book gets contracts whose past periods exist.
func TestPhase20RentersImportAcceptsARealMoveInDate(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("ImportStart", "importstart@jjne.test", "0720003100",
		[]string{"Room 1"}, 250_000)

	lastYear := time.Now().UTC().AddDate(-1, 0, 0).Format("2006-01-02")
	body := "full_name,phone,unit,start_date\nAsha Old,0720003101,Room 1," + lastYear + "\n"
	preview := fix.client.importPreview(t, "renters", "renters.csv", body).
		mustStatus(t, http.StatusCreated, "preview renters")
	rows := arrayOf(t, preview, "rows")
	if errs, _ := rows[0]["errors"].(map[string]any); errs != nil {
		t.Fatalf("a past move-in date was refused: %v", errs)
	}
	resolved, _ := rows[0]["resolved"].(map[string]any)
	if got, _ := resolved["start_date"].(string); got != lastYear {
		t.Errorf("resolved start_date = %q, want %q", got, lastYear)
	}

	committed := fix.client.do(http.MethodPost,
		"/imports/"+preview.str(t, "batch", "id")+"/commit", nil).
		mustStatus(t, http.StatusOK, "commit renters")
	_ = committed

	contracts := listOf(t, fix.client.do(http.MethodGet, "/contracts", nil).
		mustStatus(t, http.StatusOK, "contracts"))
	if len(contracts) != 1 {
		t.Fatalf("contracts = %d, want 1", len(contracts))
	}
	if got, _ := contracts[0]["start_date"].(string); got != lastYear {
		t.Errorf("contract start_date = %q, want the imported %q", got, lastYear)
	}

	// Eleven years back is refused by the sheet, as it is by the endpoint.
	tooOld := time.Now().UTC().AddDate(-11, 0, 0).Format("2006-01-02")
	badPreview := fix.client.importPreview(t, "renters", "renters.csv",
		"full_name,phone,unit,start_date\nToo Old,0720003102,Room 1,"+tooOld+"\n").
		mustStatus(t, http.StatusCreated, "preview an eleven-year-old start")
	badRows := arrayOf(t, badPreview, "rows")
	if errs, _ := badRows[0]["errors"].(map[string]any); errs == nil || errs["start_date"] == nil {
		t.Errorf("an eleven-year-old start date was accepted: %v", badRows[0])
	}
}
