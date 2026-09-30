package httpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"tms/backend/internal/contract"
	"tms/backend/internal/notify"
)

// Phase 30 — offline contracts for backfill. A backfill given `from` records an
// ended, never-signed contract for the real renter and unit; nothing else in
// the product may treat it as a tenancy, but its money is real.
//
// The fixture is the Phase 29 one: a running contract on Room 1, the renter
// known to the org, and Rooms 2-4 with no contract at all — the "no running
// contract" case.

// offline30 is one offline contract row, read straight from the database.
type offline30 struct {
	id         string
	renterID   string
	status     string
	isOffline  bool
	start, end time.Time // end is the stored, exclusive end_date
	termDays   int
	rent       int64
	cadence    int
	periodID   *string
	hash       *string
	templateID *string
	terms      string
}

func (h *harness) offlineContracts30(t *testing.T, unitID string) []offline30 {
	t.Helper()
	rows, err := h.pool.Query(context.Background(),
		`SELECT id::text, renter_user_id::text, status, is_offline, start_date, end_date, term_days,
		        rent_amount, payment_period_days, payment_period_id::text, snapshot_hash,
		        template_id::text, terms_snapshot_html
		 FROM contracts WHERE unit_id = $1 AND is_offline AND deleted_at IS NULL
		 ORDER BY start_date`, unitID)
	if err != nil {
		t.Fatalf("read offline contracts: %v", err)
	}
	defer rows.Close()
	var out []offline30
	for rows.Next() {
		var o offline30
		if err := rows.Scan(&o.id, &o.renterID, &o.status, &o.isOffline, &o.start, &o.end, &o.termDays,
			&o.rent, &o.cadence, &o.periodID, &o.hash, &o.templateID, &o.terms); err != nil {
			t.Fatalf("scan offline contract: %v", err)
		}
		out = append(out, o)
	}
	return out
}

func (h *harness) count30(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := h.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

const csvHead30 = "renter_phone,unit_code,until,mode,paid_at,method,reference,note,from,period_amount\n"

// yesterday30 is a date safely inside "not in the future" in every zone.
func yesterday30() time.Time {
	d := time.Now().UTC().AddDate(0, 0, -1)
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
}

// TestPhase30OfflineContractFields: what the record looks like, and that it
// is a real contract of the real renter, not a signable one.
func TestPhase30OfflineContractFields(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Off30", "0730000100", "+255730000101")
	c := h.contractHistory29(t, fix.contractID)
	from := c.start.AddDate(0, 0, -90)
	until := c.start.AddDate(0, 0, -1)

	done := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", map[string]any{
		"until": until.Format(date29), "mode": "paid", "from": from.Format(date29),
		"period_amount": 70_000, "reference": "PAPER",
	}).mustStatus(t, http.StatusOK, "record the offline contract")

	offs := h.offlineContracts30(t, fix.unitIDs[0])
	if len(offs) != 1 {
		t.Fatalf("offline contracts = %d, want 1", len(offs))
	}
	o := offs[0]
	if !o.isOffline || o.status != "ended" || o.renterID != fix.renterID {
		t.Errorf("contract = %+v, want ended, offline, the real renter", o)
	}
	if !o.start.Equal(from) || !o.end.Equal(until.AddDate(0, 0, 1)) || o.termDays != 90 {
		t.Errorf("span = %s..%s (%d days), want %s..%s (90)", o.start.Format(date29), o.end.Format(date29),
			o.termDays, from.Format(date29), until.AddDate(0, 0, 1).Format(date29))
	}
	if o.rent != 70_000 || o.cadence != c.cadence {
		t.Errorf("rent/cadence = %d/%d, want 70000/%d (inherited)", o.rent, o.cadence, c.cadence)
	}
	if o.hash != nil || o.templateID != nil {
		t.Errorf("hash %v template %v, want neither", o.hash, o.templateID)
	}
	if !strings.Contains(o.terms, "nje ya mfumo") && !strings.Contains(o.terms, "Offline contract") {
		t.Errorf("terms = %q, want the offline notice", o.terms)
	}
	if n := h.count30(t, `SELECT count(*) FROM contract_signatures WHERE contract_id = $1`, o.id); n != 0 {
		t.Errorf("signatures = %d, want none", n)
	}
	// Never sent for signature: no OTP, no signing text.
	if n := h.count30(t, `SELECT count(*) FROM notification_log WHERE kind NOT IN ('backfill_done')
		AND body LIKE '%' || $1 || '%'`, o.id); n != 0 {
		t.Errorf("messages naming the offline contract = %d, want 0", n)
	}

	// The API says so, on the single read, the list and the document.
	got := fix.owner.do(http.MethodGet, "/contracts/"+o.id, nil).mustStatus(t, http.StatusOK, "get")
	if got.Body["contract"].(map[string]any)["is_offline"] != true {
		t.Errorf("is_offline = %v, want true", got.Body["contract"].(map[string]any)["is_offline"])
	}
	var listed bool
	for _, item := range listOf(t, fix.owner.do(http.MethodGet, "/contracts?status=ended", nil).
		mustStatus(t, http.StatusOK, "list")) {
		if item["id"] == o.id {
			listed = item["is_offline"] == true
		}
	}
	if !listed {
		t.Error("the list does not show the offline contract flagged")
	}
	if running := fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID, nil).
		mustStatus(t, http.StatusOK, "get running"); running.Body["contract"].(map[string]any)["is_offline"] != false {
		t.Error("the running contract is flagged offline")
	}
	doc := fix.owner.do(http.MethodGet, "/contracts/"+o.id+"/document", nil).
		mustStatus(t, http.StatusOK, "offline document")
	if doc.Body["offline"] != true {
		t.Errorf("document offline = %v, want true", doc.Body["offline"])
	}
	if sched, _ := doc.Body["schedule"].([]any); len(sched) != 0 {
		t.Errorf("document schedule = %d rows, want none", len(sched))
	}
	if sigs, _ := doc.Body["signatures"].([]any); len(sigs) != 0 {
		t.Errorf("document signatures = %d, want none", len(sigs))
	}

	// Periods paid, each dated its own due day (never after `until`).
	rows := listOf(t, fix.owner.do(http.MethodGet, "/contracts/"+o.id+"/schedules", nil).
		mustStatus(t, http.StatusOK, "schedules"))
	want := contract.Generate(70_000, c.cadence, 90, c.cadence, from, c.dueDay)
	if len(rows) != len(want) {
		t.Fatalf("periods = %d, want %d", len(rows), len(want))
	}
	for i, r := range rows {
		if r["status"] != "paid" || int64(mustFloat(t, r, "paid_amount")) != want[i].Amount {
			t.Errorf("period %d = %v, want paid in full (%d)", i, r, want[i].Amount)
		}
	}
	if n := h.count30(t, `SELECT count(*) FROM payments WHERE contract_id = $1 AND source = 'backfill'
		AND paid_at::date <= $2::date AND backfill_batch_id = $3::uuid`,
		o.id, until.Format(date29), done.str(t, "backfill_id")); n != len(want) {
		t.Errorf("backfill payments on the offline contract = %d, want %d", n, len(want))
	}
	if n := h.count30(t, `SELECT count(*) FROM audit_log WHERE action = 'contract.create'
		AND entity_id = $1::uuid`, o.id); n != 1 {
		t.Errorf("contract.create audit rows = %d, want 1", n)
	}
}

// TestPhase30ActionsRefusedOnAnOfflineContract: nothing that changes a
// contract's state applies to one.
func TestPhase30ActionsRefusedOnAnOfflineContract(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Off30Act", "0730000200", "+255730000201")
	c := h.contractHistory29(t, fix.contractID)
	fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", map[string]any{
		"until": c.start.AddDate(0, 0, -1).Format(date29), "mode": "paid",
		"from": c.start.AddDate(0, 0, -30).Format(date29), "period_amount": 50_000,
	}).mustStatus(t, http.StatusOK, "record")
	id := h.offlineContracts30(t, fix.unitIDs[0])[0].id

	for _, tc := range []struct {
		name   string
		client *client
		method string
		path   string
		body   map[string]any
	}{
		{"sign otp", fix.renter, http.MethodPost, "/contracts/" + id + "/sign/otp", nil},
		{"sign", fix.renter, http.MethodPost, "/contracts/" + id + "/sign", map[string]any{"otp_code": "123456"}},
		{"activate", fix.owner, http.MethodPost, "/contracts/" + id + "/activate", nil},
		{"terminate", fix.owner, http.MethodPost, "/contracts/" + id + "/terminate",
			map[string]any{"reason": "x", "effective_date": c.start.Format(date29)}},
		{"reissue", fix.owner, http.MethodPost, "/contracts/" + id + "/reissue", map[string]any{}},
		{"amend", fix.owner, http.MethodPost, "/contracts/" + id + "/amend",
			map[string]any{"effective_date": c.start.Format(date29), "reason": "x"}},
		{"notice", fix.owner, http.MethodPost, "/contracts/" + id + "/notice", map[string]any{}},
		{"pay", fix.owner, http.MethodPost, "/payments",
			map[string]any{"contract_id": id, "amount": 1000, "method": "cash"}},
	} {
		got := tc.client.do(tc.method, tc.path, tc.body)
		if got.Code != http.StatusConflict || got.Body["type"] != "offline_contract" {
			t.Errorf("%s = %d %v, want 409 offline_contract", tc.name, got.Code, got.Body["type"])
		}
	}
}

// TestPhase30CSVWithoutARunningContract is the field case: the renter is in
// TMS, the unit has no contract at all, the sheet carries the paper record.
func TestPhase30CSVWithoutARunningContract(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Off30CSV", "0730000300", "+255730000301")
	unit := fix.unitIDs[1]
	code := fix.unitCodes[1]
	until := yesterday30()
	from := until.AddDate(0, 0, -100)

	// Missing from and missing period_amount are row errors saying what to add.
	bad := fix.owner.importPreview(t, "backfill", "bad.csv", csvHead30+
		fix.renterPhone+","+code+","+until.Format(date29)+",paid,due_date,cash,,no from,,\n"+
		fix.renterPhone+","+fix.unitCodes[2]+","+until.Format(date29)+",paid,due_date,cash,,no amount,"+
		from.Format(date29)+",\n").
		mustStatus(t, http.StatusCreated, "preview bad")
	rows := importRows(t, bad)
	if msg := rowError(t, rows, "2", "from"); !strings.Contains(msg, "offline contract") {
		t.Errorf("line 2 from error = %q, want it to say to add from", msg)
	}
	if msg := rowError(t, rows, "3", "period_amount"); !strings.Contains(msg, "no running contract") {
		t.Errorf("line 3 period_amount error = %q", msg)
	}

	// The same renter and unit twice in one file.
	dup := fix.owner.importPreview(t, "backfill", "dup.csv", csvHead30+
		fix.renterPhone+","+code+","+until.Format(date29)+",paid,,,,,"+from.Format(date29)+",50000\n"+
		fix.renterPhone+","+code+","+until.Format(date29)+",paid,,,,,"+from.Format(date29)+",50000\n").
		mustStatus(t, http.StatusCreated, "preview dup")
	if rowError(t, importRows(t, dup), "3", "unit_code") == "" {
		t.Error("the same unit and renter twice is not a row error")
	}

	preview := fix.owner.importPreview(t, "backfill", "offline.csv", csvHead30+
		fix.renterPhone+","+code+","+until.Format(date29)+",paid,due_date,cash,,Mwezi moa; Mwezi wa kwanza,"+
		from.Format(date29)+",460000\n").
		mustStatus(t, http.StatusCreated, "preview")
	res, _ := importRows(t, preview)[2]["resolved"].(map[string]any)
	if res == nil || int(mustFloat(t, res, "created")) != 4 || int(mustFloat(t, res, "periods")) != 4 {
		t.Fatalf("resolved = %v, want 4 periods", res)
	}
	if h.count30(t, `SELECT count(*) FROM contracts WHERE unit_id = $1`, unit) != 0 {
		t.Fatal("a preview wrote a contract")
	}
	batchID := preview.str(t, "batch", "id")
	fix.owner.do(http.MethodPost, "/imports/"+batchID+"/commit", nil).mustStatus(t, http.StatusOK, "commit")

	offs := h.offlineContracts30(t, unit)
	if len(offs) != 1 {
		t.Fatalf("offline contracts = %d, want 1", len(offs))
	}
	o := offs[0]
	if o.renterID != fix.renterID || o.status != "ended" || o.termDays != 101 || o.rent != 460_000 ||
		o.cadence != 30 || o.periodID == nil || *o.periodID != fix.periodID {
		t.Errorf("contract = %+v, want the renter, ended, 101 days at 460000 per 30 days, the org's 30-day period", o)
	}
	sch := listOf(t, fix.owner.do(http.MethodGet, "/contracts/"+o.id+"/schedules", nil).
		mustStatus(t, http.StatusOK, "schedules"))
	if len(sch) != 4 {
		t.Fatalf("periods = %d, want 4", len(sch))
	}
	if last := int64(mustFloat(t, sch[3], "amount")); last != contract.Prorate(460_000, 11, 30) {
		t.Errorf("last period = %d, want the 11-day proration", last)
	}
	for i, r := range sch {
		if r["status"] != "paid" {
			t.Errorf("period %d = %v, want paid", i, r["status"])
		}
	}
	// The unit is still vacant: an offline contract occupies nothing.
	if got := h.unitStatus(t, fix.owner, unit); got != "vacant" {
		t.Errorf("unit status = %q, want vacant", got)
	}
	if n := len(ofKind(h.notifications(t), notify.KindBackfillDone)); n != 1 {
		t.Errorf("backfill_done messages = %d, want 1", n)
	}
	// Listed on the offline contract's own page.
	if l := listOf(t, fix.owner.do(http.MethodGet, "/contracts/"+o.id+"/backfills", nil).
		mustStatus(t, http.StatusOK, "backfills")); len(l) != 1 || l[0]["import_batch_id"] != batchID {
		t.Errorf("backfills on the offline contract = %v, want the import's batch", l)
	}

	// Undo the import: the contract, its periods and its money go.
	undone := fix.owner.do(http.MethodPost, "/imports/"+batchID+"/undo", nil).
		mustStatus(t, http.StatusOK, "undo import")
	if counts, _ := undone.Body["undone"].(map[string]any); counts == nil ||
		int(mustFloat(t, counts, "backfills")) != 1 {
		t.Errorf("undone = %v, want one backfill", undone.Body["undone"])
	}
	if len(h.offlineContracts30(t, unit)) != 0 {
		t.Error("the offline contract survived the import undo")
	}
	if n := h.count30(t, `SELECT count(*) FROM payments WHERE contract_id = $1 AND status = 'recorded'`, o.id); n != 0 {
		t.Errorf("live payments on the removed contract = %d, want 0", n)
	}

	// A second sheet for the same stretch now goes through again.
	again := fix.owner.importPreview(t, "backfill", "again.csv", csvHead30+
		fix.renterPhone+","+code+","+until.Format(date29)+",paid,,,,,"+from.Format(date29)+",460000\n").
		mustStatus(t, http.StatusCreated, "preview again")
	if importRows(t, again)[2]["errors"] != nil {
		t.Errorf("row after the undo = %v, want none", importRows(t, again)[2]["errors"])
	}
}

// TestPhase30RunningContractSettledToo: `until` past the running contract's
// start settles both, in one batch, with one audit row and one text.
func TestPhase30RunningContractSettledToo(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Off30Both", "0730000400", "+255730000401")
	c := h.contractHistory29(t, fix.contractID)
	from := c.start.AddDate(0, 0, -60)

	done := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/backfill", map[string]any{
		"until": c.start.Format(date29), "mode": "paid", "from": from.Format(date29),
		"period_amount": 80_000,
	}).mustStatus(t, http.StatusOK, "both")

	// The offline contract stops the day before the running one starts.
	o := h.offlineContracts30(t, fix.unitIDs[0])[0]
	if !o.end.Equal(c.start) || o.termDays != 60 {
		t.Errorf("offline span ends %s (%d days), want the running start %s (60)",
			o.end.Format(date29), o.termDays, c.start.Format(date29))
	}
	if got, want := int(mustFloat(t, done.Body, "created")), 2; got != want {
		t.Errorf("created = %d, want %d", got, want)
	}
	if got := int(mustFloat(t, done.Body, "settled")); got < 3 {
		t.Errorf("settled = %d, want the 2 offline periods and the running contract's first", got)
	}
	if fix.statuses(t)[0] != "paid" {
		t.Errorf("running contract's first period = %q, want paid", fix.statuses(t)[0])
	}
	if l := backfillList29(t, fix); len(l) != 1 {
		t.Errorf("backfills = %d, want one shared batch", len(l))
	}
	if n := len(h.auditPayloads(t, "contract.backfill")); n != 1 {
		t.Errorf("contract.backfill audit rows = %d, want 1", n)
	}
	if n := len(ofKind(h.notifications(t), notify.KindBackfillDone)); n != 1 {
		t.Errorf("backfill_done messages = %d, want 1", n)
	}

	// Undoing it reopens the running contract's period and removes the rest.
	fix.owner.do(http.MethodPost, "/backfills/"+done.str(t, "backfill_id")+"/undo",
		map[string]any{"reason": "test"}).mustStatus(t, http.StatusOK, "undo")
	if fix.statuses(t)[0] == "paid" {
		t.Error("the running contract's period is still paid after the undo")
	}
	if len(h.offlineContracts30(t, fix.unitIDs[0])) != 0 {
		t.Error("the offline contract survived the undo")
	}
}

// TestPhase30OfflineStaysOutOfTheLiveBook: it is never a tenancy — not vacancy,
// occupancy, holdover or projection — but its money is revenue.
func TestPhase30OfflineStaysOutOfTheLiveBook(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Off30Rep", "0730000500", "+255730000501")
	c := h.contractHistory29(t, fix.contractID)
	unit := fix.unitIDs[1]
	until := yesterday30()
	from := until.AddDate(0, 0, -29) // one 30-day period

	projection := func() string {
		got := fix.owner.do(http.MethodPost, "/reports/projection",
			map[string]any{"horizon_months": 6, "basis": "best_case"}).mustStatus(t, http.StatusOK, "projection")
		delete(got.Body, "generated_at")
		raw, _ := json.Marshal(got.Body)
		return string(raw)
	}
	vacantSince := func() any {
		return fix.owner.do(http.MethodGet, "/units/"+unit, nil).
			mustStatus(t, http.StatusOK, "unit").Body["unit"].(map[string]any)["vacant_since"]
	}
	beforeProjection, beforeVacant := projection(), vacantSince()
	revenue := func() float64 {
		return num(t, fix.owner.do(http.MethodGet, "/reports/revenue?cadence=month&anchor="+
			c.start.Format(date29), nil).mustStatus(t, http.StatusOK, "revenue"), "totals", "collected")
	}
	revenueBefore := revenue()

	prev := fix.owner.importPreview(t, "backfill", "y.csv", csvHead30+
		fix.renterPhone+","+fix.unitCodes[1]+","+until.Format(date29)+",paid,"+c.start.Format(date29)+
		",cash,,,"+from.Format(date29)+",120000\n").mustStatus(t, http.StatusCreated, "preview 2")
	fix.owner.do(http.MethodPost, "/imports/"+prev.str(t, "batch", "id")+"/commit", nil).
		mustStatus(t, http.StatusOK, "commit")
	if len(h.offlineContracts30(t, unit)) != 1 {
		t.Fatal("no offline contract to test with")
	}

	// Revenue counts the money.
	if got := revenue(); got != revenueBefore+120_000 {
		t.Errorf("collected = %v, want %v + 120000", got, revenueBefore)
	}
	// Vacancy, projection baseline, holdovers, occupancy do not see it.
	if got := vacantSince(); got != beforeVacant {
		t.Errorf("vacant_since = %v, want %v (offline contract ignored)", got, beforeVacant)
	}
	if got := projection(); got != beforeProjection {
		t.Errorf("projection changed after an offline contract:\n before %s\n after  %s", beforeProjection, got)
	}
	for _, item := range listOf(t, fix.owner.do(http.MethodGet, "/holdovers", nil).
		mustStatus(t, http.StatusOK, "holdovers")) {
		if item["unit_id"] == unit {
			t.Errorf("holdover for the offline contract: %v", item)
		}
	}
	occ := fix.owner.do(http.MethodGet, "/reports/occupancy?cadence=month&anchor="+
		c.start.Format(date29), nil).mustStatus(t, http.StatusOK, "occupancy")
	if c.start.Day() > 1 && until.Month() == c.start.Month() {
		if v := mustFloat(t, arrayOf(t, occ, "buckets")[0], "units_occupied"); v != 0 {
			t.Errorf("the 1st of the month = %v units occupied, want 0", v)
		}
	}
	if v := num(t, occ, "current", "units_occupied"); v != 1 {
		t.Errorf("current occupied = %v, want 1 (the running contract only)", v)
	}
	// Not in the expiring / active lists, nor the tenants-to-chase report.
	for _, status := range []string{"active", "expiring"} {
		for _, item := range listOf(t, fix.owner.do(http.MethodGet, "/contracts?status="+status, nil).
			mustStatus(t, http.StatusOK, status)) {
			if item["is_offline"] == true {
				t.Errorf("an offline contract is in the %s list", status)
			}
		}
	}
	// The renter is still known to the org.
	if h.count30(t, `SELECT count(*) FROM contracts WHERE unit_id = $1 AND is_offline`, unit) != 1 {
		t.Error("unexpected offline count")
	}
}

// TestPhase30LegacyHistoryBatchStillUndoes: a Phase 29 batch (periods on the
// running contract, no offline contract) is still read and undone.
func TestPhase30LegacyHistoryBatchStillUndoes(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Off30Legacy", "0730000600", "+255730000601")
	c := h.contractHistory29(t, fix.contractID)
	ctx := context.Background()

	var batchID string
	if err := h.pool.QueryRow(ctx, `INSERT INTO backfill_batches
		(org_id, contract_id, mode, until, from_date, created_periods)
		SELECT org_id, id, 'paid', start_date - 1, start_date - 30, 1 FROM contracts WHERE id = $1
		RETURNING id::text`, fix.contractID).Scan(&batchID); err != nil {
		t.Fatalf("legacy batch: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `INSERT INTO payment_schedules
		(org_id, contract_id, period_start, period_end, due_date, amount, status, created_by_backfill_id)
		SELECT org_id, id, start_date - 30, start_date - 1, start_date - 30, 1000, 'overdue', $2::uuid
		FROM contracts WHERE id = $1`, fix.contractID, batchID); err != nil {
		t.Fatalf("legacy period: %v", err)
	}
	item := backfillList29(t, fix)[0]
	if item["created_contract_id"] != nil || int(mustFloat(t, item, "created_periods")) != 1 ||
		item["from"] != c.start.AddDate(0, 0, -30).Format(date29) {
		t.Errorf("legacy list item = %v", item)
	}
	undone := fix.owner.do(http.MethodPost, "/backfills/"+batchID+"/undo",
		map[string]any{"reason": "legacy"}).mustStatus(t, http.StatusOK, "undo legacy")
	if int(mustFloat(t, undone.Body, "periods_removed")) != 1 || undone.Body["contract_removed"] != false {
		t.Errorf("undo = %v, want the created period removed and no contract", undone.Body)
	}
}

// TestPhase30DuplicateHeaderIsAFileError: a column named twice is refused with
// its name, not silently read from the first.
func TestPhase30DuplicateHeaderIsAFileError(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Off30Hdr", "0730000700", "+255730000701")
	got := fix.owner.importPreview(t, "backfill", "dup.csv",
		"renter_phone,unit_code,until,mode,note,note\n"+
			fix.renterPhone+","+fix.unitCodes[0]+","+yesterday30().Format(date29)+",paid,a,b\n")
	if got.Code != http.StatusBadRequest {
		t.Fatalf("duplicate header = %d, want 400", got.Code)
	}
	dup, _ := got.Body["duplicate"].([]any)
	if len(dup) != 1 || dup[0] != "note" {
		t.Errorf("duplicate = %v, want [note] — body: %s", got.Body["duplicate"], got.Raw)
	}
}
