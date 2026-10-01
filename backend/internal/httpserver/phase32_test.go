package httpserver_test

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Phase 32 — one sheet line, one paper payment, one offline contract.
//
// Landlords' paper records are receipts: "460,000 on 2 June for three
// months". A line with `amount` records exactly that — an offline contract
// that is one period for the whole sum, paid once — and a renter may have
// several such lines. Each stretch is floored: a contract already in TMS (or a
// later line) that starts inside it keeps its start date, and the stretch ends
// the day before.

const csvHead32 = "renter_phone,unit_code,until,mode,paid_at,method,reference,note,from,amount\n"

func d32(t time.Time) string { return t.Format(date29) }

// TestPhase32TwoPaymentsTwoContracts is the field case: two receipts for the
// same renter and unit, written back to back so the second's first day is the
// first's last. Both are accepted; the first is floored to end the day before.
func TestPhase32TwoPaymentsTwoContracts(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Off32Two", "0732000100", "+255732000101")
	unit, code := fix.unitIDs[1], fix.unitCodes[1]
	end := yesterday30()
	mid := end.AddDate(0, 0, -30)
	start := mid.AddDate(0, 0, -90)

	csv := csvHead32 +
		// Listed newest first on purpose: the commit must not depend on order.
		fix.renterPhone + "," + code + "," + d32(end) + ",paid," + d32(mid) + ",cash,,Mwezi moja," + d32(mid) + ",140000\n" +
		fix.renterPhone + "," + code + "," + d32(mid) + ",paid," + d32(start) + ",cash,,Miezi mitatu," + d32(start) + ",\"460,000\"\n"
	preview := fix.owner.importPreview(t, "backfill", "two.csv", csv).
		mustStatus(t, http.StatusCreated, "preview")
	rows := importRows(t, preview)
	for _, line := range []int{2, 3} {
		if rows[line]["errors"] != nil {
			t.Fatalf("line %d errors = %v, want none", line, rows[line]["errors"])
		}
	}
	older, _ := rows[3]["resolved"].(map[string]any)
	if older["offline_end"] != d32(mid.AddDate(0, 0, -1)) {
		t.Errorf("older line ends %v, want the day before the newer line (%s)", older["offline_end"], d32(mid.AddDate(0, 0, -1)))
	}
	if int(mustFloat(t, older, "periods")) != 1 || int64(mustFloat(t, older, "amount")) != 460_000 {
		t.Errorf("older line resolved = %v, want one period of 460000", older)
	}

	fix.owner.do(http.MethodPost, "/imports/"+preview.str(t, "batch", "id")+"/commit", nil).
		mustStatus(t, http.StatusOK, "commit")

	offs := h.offlineContracts30(t, unit)
	if len(offs) != 2 {
		t.Fatalf("offline contracts = %d, want 2", len(offs))
	}
	want := []struct {
		start, last time.Time
		amount      int64
	}{
		{start, mid.AddDate(0, 0, -1), 460_000},
		{mid, end, 140_000},
	}
	for i, o := range offs {
		w := want[i]
		if !o.start.Equal(w.start) || !o.end.Equal(w.last.AddDate(0, 0, 1)) || o.rent != w.amount {
			t.Errorf("contract %d = %s..%s rent %d, want %s..%s rent %d", i,
				d32(o.start), d32(o.end.AddDate(0, 0, -1)), o.rent, d32(w.start), d32(w.last), w.amount)
		}
		sch := listOf(t, fix.owner.do(http.MethodGet, "/contracts/"+o.id+"/schedules", nil).
			mustStatus(t, http.StatusOK, "schedules"))
		if len(sch) != 1 || sch[0]["status"] != "paid" || int64(mustFloat(t, sch[0], "amount")) != w.amount {
			t.Errorf("contract %d periods = %v, want one paid period of %d", i, sch, w.amount)
		}
		pays := listOf(t, fix.owner.do(http.MethodGet, "/payments?contract_id="+o.id, nil).
			mustStatus(t, http.StatusOK, "payments"))
		if len(pays) != 1 || int64(mustFloat(t, pays[0], "amount")) != w.amount ||
			!strings.HasPrefix(pays[0]["paid_at"].(string), d32(w.start)) {
			t.Errorf("contract %d payments = %v, want one of %d paid %s", i, pays, w.amount, d32(w.start))
		}
	}
}

// TestPhase32FloorAtATMSContract: a stretch that runs into a contract already
// in TMS — even past today — ends the day before it starts, and that
// contract's own periods are left alone.
func TestPhase32FloorAtATMSContract(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Off32Floor", "0732000200", "+255732000201")
	c := h.contractHistory29(t, fix.contractID)
	before := fix.statuses(t)
	from := c.start.AddDate(0, 0, -60)
	until := c.start.AddDate(0, 0, 40) // past the TMS start, and in the future

	preview := fix.owner.importPreview(t, "backfill", "floor.csv", csvHead32+
		fix.renterPhone+","+fix.unitCodes[0]+","+d32(until)+",paid,,cash,,,"+d32(from)+",120000\n").
		mustStatus(t, http.StatusCreated, "preview")
	row := importRows(t, preview)[2]
	if row["errors"] != nil {
		t.Fatalf("errors = %v, want the stretch floored, not refused", row["errors"])
	}
	res, _ := row["resolved"].(map[string]any)
	if res["offline_end"] != d32(c.start.AddDate(0, 0, -1)) {
		t.Errorf("offline_end = %v, want the day before the TMS contract (%s)", res["offline_end"], d32(c.start.AddDate(0, 0, -1)))
	}
	fix.owner.do(http.MethodPost, "/imports/"+preview.str(t, "batch", "id")+"/commit", nil).
		mustStatus(t, http.StatusOK, "commit")
	after := fix.statuses(t)
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("TMS contract period %d = %q, was %q — an amount line must not settle it", i, after[i], before[i])
		}
	}

	// A later sheet whose stretch runs into that offline contract is floored
	// against it too.
	earlier := from.AddDate(0, 0, -45)
	second := fix.owner.importPreview(t, "backfill", "earlier.csv", csvHead32+
		fix.renterPhone+","+fix.unitCodes[0]+","+d32(from.AddDate(0, 0, 10))+",paid,,cash,,,"+d32(earlier)+",90000\n").
		mustStatus(t, http.StatusCreated, "preview earlier")
	res2, _ := importRows(t, second)[2]["resolved"].(map[string]any)
	if res2 == nil || res2["offline_end"] != d32(from.AddDate(0, 0, -1)) {
		t.Errorf("earlier line = %v, want it to end the day before the offline contract", importRows(t, second)[2])
	}
}

// TestPhase32Refusals: what an `amount` line cannot be.
func TestPhase32Refusals(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Off32Bad", "0732000300", "+255732000301")
	code := fix.unitCodes[1]
	until := yesterday30()
	from := until.AddDate(0, 0, -30)
	future := time.Now().UTC().AddDate(0, 0, 20)

	bad := fix.owner.importPreview(t, "backfill", "bad.csv",
		"renter_phone,unit_code,until,mode,from,amount,period_amount\n"+
			fix.renterPhone+","+code+","+d32(until)+",paid,,50000,\n"+
			fix.renterPhone+","+code+","+d32(until)+",paid,"+d32(from)+",50000,50000\n"+
			fix.renterPhone+","+fix.unitCodes[2]+","+d32(future)+",paid,"+d32(from)+",50000,\n"+
			fix.renterPhone+","+fix.unitCodes[3]+","+d32(until)+",paid,"+d32(from)+",50000,\n"+
			fix.renterPhone+","+fix.unitCodes[3]+","+d32(until)+",paid,"+d32(from)+",60000,\n").
		mustStatus(t, http.StatusCreated, "preview")
	rows := importRows(t, bad)
	if rowError(t, rows, "2", "amount") == "" {
		t.Error("amount without from is not a row error")
	}
	if rowError(t, rows, "3", "amount") == "" {
		t.Error("amount with period_amount is not a row error")
	}
	if rowError(t, rows, "4", "until") == "" {
		t.Error("a future until with nothing to floor against is not a row error")
	}
	if rows[5]["errors"] != nil {
		t.Errorf("line 5 errors = %v, want none", rows[5]["errors"])
	}
	if rowError(t, rows, "6", "unit_code") == "" {
		t.Error("two lines with the same renter, unit and from are not a row error")
	}

	got := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill",
		map[string]any{"until": d32(until), "mode": "paid", "amount": 50000})
	if got.Code != http.StatusBadRequest {
		t.Errorf("endpoint amount without from = %d, want 400", got.Code)
	}
}
