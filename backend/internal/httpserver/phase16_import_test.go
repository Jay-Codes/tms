package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"tms/backend/internal/httpserver"
)

// Phase 16 §16.2 — the CSV import of previous records.
//
// The tests below walk the three sheets end to end against the real database:
// what the parser tolerates (a BOM, a `;`, mixed-case headers), where each
// error lands, that a commit is all-or-nothing, that payments are allocated in
// the order the money arrived, that a renters import stops at
// `pending_signature`, and that an undo takes back its own batch and nothing
// else.

// ------------------------------------------------------------- helpers --

// upload posts one multipart request carrying this client's cookies. The JSON
// `do` helper cannot express a file part, and the import is the only endpoint
// that takes one.
func (c *client) upload(method, path string, fields map[string]string,
	fileField, filename string, content []byte,
) response {
	c.h.t.Helper()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			c.h.t.Fatalf("write field %s: %v", k, err)
		}
	}
	if fileField != "" {
		fw, err := w.CreateFormFile(fileField, filename)
		if err != nil {
			c.h.t.Fatalf("create file part: %v", err)
		}
		if _, err := fw.Write(content); err != nil {
			c.h.t.Fatalf("write file part: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		c.h.t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(method, httpserver.APIPrefix+path, bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", w.FormDataContentType())
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	c.h.srv.Handler().ServeHTTP(rec, req)

	out := response{Code: rec.Code, Raw: rec.Body.String()}
	if len(out.Raw) > 0 {
		_ = json.Unmarshal([]byte(out.Raw), &out.Body)
	}
	return out
}

// importPreview uploads one sheet for preview.
func (c *client) importPreview(t *testing.T, kind, filename, body string) response {
	t.Helper()
	return c.upload(http.MethodPost, "/imports/preview",
		map[string]string{"kind": kind}, "file", filename, []byte(body))
}

// importRows reads the preview table keyed by line number.
func importRows(t *testing.T, r response) map[int]map[string]any {
	t.Helper()
	out := map[int]map[string]any{}
	for _, row := range arrayOf(t, r, "rows") {
		out[int(mustFloat(t, row, "line"))] = row
	}
	return out
}

// rowError reads the error recorded against one column of one line.
func rowError(t *testing.T, rows map[int]map[string]any, line, column string) string {
	t.Helper()
	n := 0
	if _, err := fmt.Sscanf(line, "%d", &n); err != nil {
		t.Fatalf("bad line %q", line)
	}
	row, ok := rows[n]
	if !ok {
		t.Fatalf("no row on line %s", line)
	}
	errs, ok := row["errors"].(map[string]any)
	if !ok {
		t.Fatalf("line %s carries no errors: %v", line, row)
	}
	msg, _ := errs[column].(string)
	return msg
}

func resolvedOfRow(t *testing.T, rows map[int]map[string]any, line int) map[string]any {
	t.Helper()
	row, ok := rows[line]
	if !ok {
		t.Fatalf("no row on line %d", line)
	}
	res, _ := row["resolved"].(map[string]any)
	return res
}

// withBOM prefixes the UTF-8 byte-order mark Excel writes.
func withBOM(s string) string { return "\ufeff" + s }

// ------------------------------------------------------------- the units --

// TestImportUnitsPreviewCommitAndUndo is the whole §16.2 units story: a file
// Excel wrote (BOM, semicolons, shouty headers), a preview that flags what is
// wrong without writing anything, a refusal to commit while errors stand, a
// commit that skips them, and an undo that takes it all back.
func TestImportUnitsPreviewCommitAndUndo(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Imp", "imp@jjne.test", "0716000100", []string{"Room 1"}, 250_000)
	owner := fix.client

	sheet := withBOM(strings.Join([]string{
		"Property;UNIT; rent_amount ;status",
		"Imp Block A;Room 1;150000;vacant", // line 2: the property already has it
		"Imp Block A;Room 9;TZS 150,000;",  // line 3: ok, money with a prefix
		"New Block;B1;200000;unlisted",     // line 4: ok, creates the property
		"New Block;B1;200000;vacant",       // line 5: this file already claims it
		"New Block;B2;not-a-number;vacant", // line 6: bad money
		"",
	}, "\n"))

	preview := owner.importPreview(t, "units", "previous-units.csv", sheet).
		mustStatus(t, http.StatusCreated, "units preview")
	batchID := preview.str(t, "batch", "id")
	if got := num(t, preview, "batch", "row_count"); got != 5 {
		t.Fatalf("row_count = %v, want 5 — body: %s", got, preview.Raw)
	}
	if got := num(t, preview, "batch", "ok_count"); got != 2 {
		t.Errorf("ok_count = %v, want 2 — body: %s", got, preview.Raw)
	}
	if got := num(t, preview, "batch", "error_count"); got != 3 {
		t.Errorf("error_count = %v, want 3 — body: %s", got, preview.Raw)
	}

	rows := importRows(t, preview)
	if msg := rowError(t, rows, "2", "unit"); !strings.Contains(msg, "already has a unit") {
		t.Errorf("line 2 unit error = %q", msg)
	}
	if msg := rowError(t, rows, "5", "unit"); !strings.Contains(msg, "already creates") {
		t.Errorf("line 5 unit error = %q", msg)
	}
	if msg := rowError(t, rows, "6", "rent_amount"); msg == "" {
		t.Errorf("line 6 has no rent_amount error: %v", rows[6])
	}
	if res := resolvedOfRow(t, rows, 4); res["property_create"] != true {
		t.Errorf("line 4 should flag property_create: %v", res)
	}
	if res := resolvedOfRow(t, rows, 3); res["property_create"] != false {
		t.Errorf("line 3 names an existing property: %v", res)
	}
	// The preview wrote nothing but the batch.
	if names := unitNames(t, owner); len(names) != 1 {
		t.Fatalf("preview created units: %v", names)
	}

	// Errors stand: the commit is refused with the count.
	refused := owner.do(http.MethodPost, "/imports/"+batchID+"/commit", nil).
		mustStatus(t, http.StatusConflict, "commit with errors")
	if refused.Body["type"] != "batch_has_errors" {
		t.Errorf("refusal type = %v, want batch_has_errors — body: %s", refused.Body["type"], refused.Raw)
	}
	if got := num(t, refused, "error_count"); got != 3 {
		t.Errorf("refusal error_count = %v, want 3", got)
	}

	committed := owner.do(http.MethodPost, "/imports/"+batchID+"/commit",
		map[string]any{"skip_errors": true}).mustStatus(t, http.StatusOK, "commit skipping errors")
	if got := num(t, committed, "created", "units"); got != 2 {
		t.Errorf("created.units = %v, want 2 — body: %s", got, committed.Raw)
	}
	if got := num(t, committed, "created", "properties"); got != 1 {
		t.Errorf("created.properties = %v, want 1 — body: %s", got, committed.Raw)
	}
	if got := committed.str(t, "batch", "status"); got != "committed" {
		t.Errorf("batch status = %q, want committed", got)
	}

	names := unitNames(t, owner)
	for _, want := range []string{"Room 9", "B1"} {
		if !names[want] {
			t.Errorf("unit %q was not created — have %v", want, names)
		}
	}

	// A second commit is refused rather than doubling the sheet.
	owner.do(http.MethodPost, "/imports/"+batchID+"/commit", map[string]any{"skip_errors": true}).
		mustStatus(t, http.StatusConflict, "second commit")

	undone := owner.do(http.MethodPost, "/imports/"+batchID+"/undo", nil).
		mustStatus(t, http.StatusOK, "undo")
	if got := num(t, undone, "undone", "units"); got != 2 {
		t.Errorf("undone.units = %v, want 2 — body: %s", got, undone.Raw)
	}
	if got := num(t, undone, "undone", "properties"); got != 1 {
		t.Errorf("undone.properties = %v, want 1 — body: %s", got, undone.Raw)
	}
	if names := unitNames(t, owner); len(names) != 1 || !names["Room 1"] {
		t.Errorf("undo did not take the imported units back: %v", names)
	}
	if got := undone.str(t, "batch", "status"); got != "undone" {
		t.Errorf("batch status after undo = %q", got)
	}
	// The trail records all three movements.
	assertAudited(t, owner, "import.preview", "import.commit", "import.undo")
}

func unitNames(t *testing.T, c *client) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, row := range listOf(t, c.do(http.MethodGet, "/units?limit=200", nil).
		mustStatus(t, http.StatusOK, "list units")) {
		name, _ := row["name"].(string)
		out[name] = true
	}
	return out
}

func assertAudited(t *testing.T, c *client, actions ...string) {
	t.Helper()
	seen := map[string]bool{}
	for _, row := range listOf(t, c.do(http.MethodGet, "/audit-log?limit=200", nil).
		mustStatus(t, http.StatusOK, "audit log")) {
		action, _ := row["action"].(string)
		seen[action] = true
	}
	for _, want := range actions {
		if !seen[want] {
			t.Errorf("no %s audit row — saw %v", want, seen)
		}
	}
}

// TestImportHeaderValidationNamesEveryColumn: a header that does not match is
// refused once, naming everything that is wrong with it.
func TestImportHeaderValidationNamesEveryColumn(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Head", "head@jjne.test", "0716000110", nil, 0)

	resp := fix.client.importPreview(t, "units", "wrong.csv",
		"property,unit,rent,notes\nA,B,1,2\n").
		mustStatus(t, http.StatusBadRequest, "bad header")
	if resp.Body["type"] != "csv_header_mismatch" {
		t.Fatalf("type = %v, want csv_header_mismatch — body: %s", resp.Body["type"], resp.Raw)
	}
	missing := stringSet(t, resp, "missing")
	if !missing["rent_amount"] {
		t.Errorf("missing does not name rent_amount: %s", resp.Raw)
	}
	unknown := stringSet(t, resp, "unknown")
	if !unknown["rent"] || !unknown["notes"] {
		t.Errorf("unknown does not name both stray columns: %s", resp.Raw)
	}

	// An empty file is its own refusal, not a header one.
	empty := fix.client.importPreview(t, "units", "empty.csv", "").
		mustStatus(t, http.StatusBadRequest, "empty file")
	if empty.Body["type"] != "empty_file" {
		t.Errorf("empty file type = %v, want empty_file — body: %s", empty.Body["type"], empty.Raw)
	}

	// A header with no data rows is refused too.
	fix.client.importPreview(t, "units", "headers.csv", "property,unit,rent_amount\n").
		mustStatus(t, http.StatusBadRequest, "header only")
}

func stringSet(t *testing.T, r response, key string) map[string]bool {
	t.Helper()
	raw, ok := r.Body[key].([]any)
	if !ok {
		t.Fatalf("response has no %s array — body: %s", key, r.Raw)
	}
	out := map[string]bool{}
	for _, v := range raw {
		s, _ := v.(string)
		out[strings.ToLower(s)] = true
	}
	return out
}

// TestImportCommitIsAtomic: a row that passed the preview and no longer does
// takes the whole commit with it. Nothing of the file is written.
func TestImportCommitIsAtomic(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Atom", "atom@jjne.test", "0716000120", []string{"Room 1"}, 250_000)
	owner := fix.client

	preview := owner.importPreview(t, "units", "atomic.csv",
		"property,unit,rent_amount\nAtom Block A,Z1,150000\nAtom Block A,Z2,150000\n").
		mustStatus(t, http.StatusCreated, "preview")
	batchID := preview.str(t, "batch", "id")
	if got := num(t, preview, "batch", "ok_count"); got != 2 {
		t.Fatalf("ok_count = %v, want 2 — body: %s", got, preview.Raw)
	}

	// Somebody creates Z2 by hand between the preview and the commit.
	owner.do(http.MethodPost, "/properties/"+fix.propertyID+"/units",
		map[string]any{"name": "Z2"}).mustStatus(t, http.StatusCreated, "manual unit")

	failed := owner.do(http.MethodPost, "/imports/"+batchID+"/commit", nil).
		mustStatus(t, http.StatusConflict, "commit after the world moved")
	if failed.Body["type"] != "row_failed" {
		t.Fatalf("type = %v, want row_failed — body: %s", failed.Body["type"], failed.Raw)
	}
	if got := num(t, failed, "line"); got != 3 {
		t.Errorf("failing line = %v, want 3 — body: %s", got, failed.Raw)
	}

	names := unitNames(t, owner)
	if names["Z1"] {
		t.Error("the first row was written although the commit failed — the transaction is not atomic")
	}
	// The batch is still previewable, not half-committed.
	again := owner.do(http.MethodGet, "/imports/"+batchID, nil).
		mustStatus(t, http.StatusOK, "read batch")
	if got := again.str(t, "batch", "status"); got != "previewed" {
		t.Errorf("batch status after a failed commit = %q, want previewed", got)
	}
}

// ----------------------------------------------------------- the renters --

// TestImportRentersStopAtPendingSignature: a renters sheet pre-registers the
// person and draws up a contract for them to sign. It never activates one, and
// the `contract_ready` SMS is queued by the commit, not by the preview.
func TestImportRentersStopAtPendingSignature(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Rent", "rent@jjne.test", "0716000130",
		[]string{"Room 1", "Room 2"}, 250_000)
	owner := fix.client

	sheet := "full_name,phone,locale,property,unit\n" +
		"Asha Mollel,0716000131,sw,Rent Block A,Room 1\n" +
		"Juma Ally,0716000132,en,,\n"

	preview := owner.importPreview(t, "renters", "renters.csv", sheet).
		mustStatus(t, http.StatusCreated, "renters preview")
	batchID := preview.str(t, "batch", "id")
	if got := num(t, preview, "batch", "ok_count"); got != 2 {
		t.Fatalf("ok_count = %v, want 2 — body: %s", got, preview.Raw)
	}
	rows := importRows(t, preview)
	if res := resolvedOfRow(t, rows, 2); res["contract"] != "pending_signature" {
		t.Errorf("line 2 resolved.contract = %v, want pending_signature", res)
	}
	if res := resolvedOfRow(t, rows, 2); res["renter_create"] != true {
		t.Errorf("line 2 should create the renter: %v", res)
	}
	if before := notificationKinds(t, owner)["contract_ready"]; before != 0 {
		t.Fatalf("the preview queued %d contract_ready messages; it must queue none", before)
	}

	committed := owner.do(http.MethodPost, "/imports/"+batchID+"/commit", nil).
		mustStatus(t, http.StatusOK, "commit renters")
	if got := num(t, committed, "created", "renters"); got != 2 {
		t.Errorf("created.renters = %v, want 2 — body: %s", got, committed.Raw)
	}
	if got := num(t, committed, "created", "contracts"); got != 1 {
		t.Errorf("created.contracts = %v, want 1 — body: %s", got, committed.Raw)
	}

	contracts := listOf(t, owner.do(http.MethodGet, "/contracts", nil).
		mustStatus(t, http.StatusOK, "contracts"))
	if len(contracts) != 1 {
		t.Fatalf("contracts = %d, want 1", len(contracts))
	}
	if status, _ := contracts[0]["status"].(string); status != "pending_signature" {
		t.Errorf("imported contract status = %q, want pending_signature", status)
	}
	if after := notificationKinds(t, owner)["contract_ready"]; after != 1 {
		t.Errorf("contract_ready messages after the commit = %d, want 1", after)
	}

	// The unit stays vacant: what makes it occupied is activation.
	if got := h.unitStatus(t, owner, fix.unitIDs[0]); got != "vacant" {
		t.Errorf("unit status after a renters import = %q, want vacant", got)
	}

	// A second sheet naming the same number attaches the existing person.
	second := owner.importPreview(t, "renters", "again.csv",
		"full_name,phone,property,unit\nAsha Mollel,+255716000131,Rent Block A,Room 2\n").
		mustStatus(t, http.StatusCreated, "second renters preview")
	if res := resolvedOfRow(t, importRows(t, second), 2); res["renter_create"] != false {
		t.Errorf("a known number must not create a second account: %v", res)
	}
}

func notificationKinds(t *testing.T, c *client) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, row := range listOf(t, c.do(http.MethodGet, "/notifications/log?limit=200", nil).
		mustStatus(t, http.StatusOK, "notification log")) {
		kind, _ := row["kind"].(string)
		out[kind]++
	}
	return out
}

// ---------------------------------------------------------- the payments --

// TestImportPaymentsAllocateInDateOrder: the sheet's order is not the money's
// order. Rows are allocated by `paid_at`, so the earlier payment settles the
// earlier schedule however the lines were typed — and a row the contract cannot
// absorb is an error in the preview, not a surprise at commit time.
func TestImportPaymentsAllocateInDateOrder(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "PayImp", "0716000140", "+255716000141")
	owner := fix.owner

	unitName := "Room 1"
	early := time.Now().UTC().AddDate(0, 0, -20).Format("2006-01-02")
	late := time.Now().UTC().AddDate(0, 0, -10).Format("2006-01-02")

	sheet := "unit,renter_phone,amount,paid_at,method,reference\n" +
		fmt.Sprintf("%s,%s,%d,%s,bank_transfer,LATE\n", unitName, fix.renterPhone, fix.amounts[1], late) +
		fmt.Sprintf("%s,%s,%d,%s,cash,EARLY\n", unitName, fix.renterPhone, fix.amounts[0], early)

	preview := owner.importPreview(t, "payments", "payments.csv", sheet).
		mustStatus(t, http.StatusCreated, "payments preview")
	if got := num(t, preview, "batch", "ok_count"); got != 2 {
		t.Fatalf("ok_count = %v, want 2 — body: %s", got, preview.Raw)
	}
	batchID := preview.str(t, "batch", "id")

	committed := owner.do(http.MethodPost, "/imports/"+batchID+"/commit", nil).
		mustStatus(t, http.StatusOK, "commit payments")
	if got := num(t, committed, "created", "payments"); got != 2 {
		t.Fatalf("created.payments = %v, want 2 — body: %s", got, committed.Raw)
	}

	// The earlier payment is on the first schedule, the later one on the second.
	byReference := map[string]map[string]any{}
	for _, row := range listOf(t, owner.do(http.MethodGet, "/payments?limit=50", nil).
		mustStatus(t, http.StatusOK, "payments")) {
		ref, _ := row["reference"].(string)
		byReference[ref] = row
	}
	for ref, wantSchedule := range map[string]string{
		"EARLY": fix.scheduleIDs[0], "LATE": fix.scheduleIDs[1],
	} {
		row, ok := byReference[ref]
		if !ok {
			t.Fatalf("payment %s was not recorded — have %v", ref, byReference)
		}
		applied, _ := row["applied"].([]any)
		if len(applied) != 1 {
			t.Fatalf("%s applied = %v, want one schedule", ref, applied)
		}
		first, _ := applied[0].(map[string]any)
		if got, _ := first["schedule_id"].(string); got != wantSchedule {
			t.Errorf("%s landed on schedule %s, want %s", ref, got, wantSchedule)
		}
		if got, _ := row["import_batch_id"].(string); got != batchID {
			t.Errorf("%s carries import_batch_id %q, want %q", ref, got, batchID)
		}
	}

	// More than the contract still owes is a row error, not a 500.
	over := owner.importPreview(t, "payments", "over.csv",
		"unit,renter_phone,amount,paid_at,method\n"+
			fmt.Sprintf("%s,%s,900000000,%s,cash\n", unitName, fix.renterPhone, late)).
		mustStatus(t, http.StatusCreated, "overpay preview")
	if got := num(t, over, "batch", "error_count"); got != 1 {
		t.Fatalf("error_count = %v, want 1 — body: %s", got, over.Raw)
	}
	if msg := rowError(t, importRows(t, over), "2", "amount"); msg != "exceeds_contract_balance" {
		t.Errorf("row error = %q, want exceeds_contract_balance", msg)
	}

	// A phone nobody in the org has is refused on its own column.
	unknown := owner.importPreview(t, "payments", "unknown.csv",
		"unit,renter_phone,amount,paid_at,method\n"+
			fmt.Sprintf("%s,0716999999,1000,%s,cash\n", unitName, late)).
		mustStatus(t, http.StatusCreated, "unknown renter preview")
	if msg := rowError(t, importRows(t, unknown), "2", "renter_phone"); msg == "" {
		t.Errorf("an unknown number must fail on renter_phone: %s", unknown.Raw)
	}
}

// TestImportUndoReversesOnlyItsOwnPayments: the undo is a scalpel. Money the
// landlord keyed in by hand is untouched, and after 24 h the button is gone.
func TestImportUndoReversesOnlyItsOwnPayments(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "UndoImp", "0716000150", "+255716000151")
	owner := fix.owner

	manual := owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "schedule_id": fix.scheduleIDs[0],
		"amount": fix.amounts[0], "method": "cash", "reference": "BY-HAND",
	}).mustStatus(t, http.StatusCreated, "manual payment").str(t, "payment", "id")

	paidAt := time.Now().UTC().AddDate(0, 0, -5).Format("2006-01-02")
	preview := owner.importPreview(t, "payments", "undo.csv",
		"unit,renter_phone,amount,paid_at,method,reference\n"+
			fmt.Sprintf("Room 1,%s,%d,%s,cash,IMPORTED\n", fix.renterPhone, fix.amounts[1], paidAt)).
		mustStatus(t, http.StatusCreated, "preview")
	batchID := preview.str(t, "batch", "id")
	owner.do(http.MethodPost, "/imports/"+batchID+"/commit", nil).
		mustStatus(t, http.StatusOK, "commit")

	undone := owner.do(http.MethodPost, "/imports/"+batchID+"/undo", nil).
		mustStatus(t, http.StatusOK, "undo")
	if got := num(t, undone, "undone", "payments"); got != 1 {
		t.Fatalf("undone.payments = %v, want 1 — body: %s", got, undone.Raw)
	}

	statuses := map[string]string{}
	for _, row := range listOf(t, owner.do(http.MethodGet, "/payments?limit=50", nil).
		mustStatus(t, http.StatusOK, "payments")) {
		ref, _ := row["reference"].(string)
		status, _ := row["status"].(string)
		statuses[ref] = status
	}
	if statuses["IMPORTED"] != "reversed" {
		t.Errorf("the imported payment is %q, want reversed", statuses["IMPORTED"])
	}
	if statuses["BY-HAND"] != "recorded" {
		t.Errorf("the manual payment is %q; the undo must not touch it", statuses["BY-HAND"])
	}
	reason := owner.do(http.MethodGet, "/payments/"+manual, nil).
		mustStatus(t, http.StatusOK, "manual payment")
	if got := reason.Body["payment"].(map[string]any)["reversal_reason"]; got != nil {
		t.Errorf("the manual payment carries a reversal reason %v", got)
	}

	// A second batch, committed a day ago, is past the window.
	stale := owner.importPreview(t, "payments", "stale.csv",
		"unit,renter_phone,amount,paid_at,method,reference\n"+
			fmt.Sprintf("Room 1,%s,%d,%s,cash,STALE\n", fix.renterPhone, fix.amounts[1], paidAt)).
		mustStatus(t, http.StatusCreated, "stale preview")
	staleID := stale.str(t, "batch", "id")
	owner.do(http.MethodPost, "/imports/"+staleID+"/commit", nil).
		mustStatus(t, http.StatusOK, "commit stale")
	h.backdateCommit(t, staleID, 25*time.Hour)

	closed := owner.do(http.MethodPost, "/imports/"+staleID+"/undo", nil).
		mustStatus(t, http.StatusConflict, "undo past the window")
	if closed.Body["type"] != "undo_window_closed" {
		t.Errorf("type = %v, want undo_window_closed — body: %s", closed.Body["type"], closed.Raw)
	}
}

// backdateCommit moves a batch's commit into the past, the way the payment
// tests move a due date, so the 24 h window can be tested in one second.
func (h *harness) backdateCommit(t *testing.T, batchID string, ago time.Duration) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		"UPDATE import_batches SET committed_at = now() - $2::interval WHERE id = $1",
		batchID, fmt.Sprintf("%d seconds", int(ago.Seconds()))); err != nil {
		t.Fatalf("backdate commit: %v", err)
	}
}

// ------------------------------------------------------------ the guards --

// TestImportBatchesAreOrgScoped: org B cannot read, commit or undo org A's
// spreadsheet, and its own history is empty.
func TestImportBatchesAreOrgScoped(t *testing.T) {
	h := newHarness(t)
	a := h.newOrgWithUnits("IsoImpA", "isoimpa@jjne.test", "0716000160", []string{"Room 1"}, 250_000)
	b := h.newOrgWithUnits("IsoImpB", "isoimpb@jjne.test", "0716000161", []string{"Room 1"}, 250_000)

	batchID := a.client.importPreview(t, "units", "a.csv",
		"property,unit,rent_amount\nIsoImpA Block A,Z1,150000\n").
		mustStatus(t, http.StatusCreated, "org A preview").str(t, "batch", "id")

	for _, probe := range []struct{ method, path string }{
		{http.MethodGet, "/imports/" + batchID},
		{http.MethodPost, "/imports/" + batchID + "/commit"},
		{http.MethodPost, "/imports/" + batchID + "/undo"},
	} {
		b.client.do(probe.method, probe.path, map[string]any{}).
			mustStatus(t, http.StatusNotFound, "org B "+probe.path)
	}
	if rows := listOf(t, b.client.do(http.MethodGet, "/imports", nil).
		mustStatus(t, http.StatusOK, "org B history")); len(rows) != 0 {
		t.Errorf("org B sees %d batches, want none", len(rows))
	}
	if strings.Contains(b.client.do(http.MethodGet, "/imports", nil).Raw, batchID) {
		t.Error("org A's batch id leaked into org B's history")
	}
}

// TestImportPaymentsPreviewNamesNoRenterFromAnotherOrg: the preview resolves a
// phone number against a platform-global table, and must give nothing away for
// it. A number belonging to another landlord's tenant and a number belonging to
// nobody answer with the *same* row error, and neither puts a name in
// `resolved` — otherwise a payments sheet of guessed numbers would be a
// directory of every renter on the platform (SPEC §8).
func TestImportPaymentsPreviewNamesNoRenterFromAnotherOrg(t *testing.T) {
	h := newHarness(t)
	a := h.newPaymentFixture(t, "LeakA", "0716000200", "+255716000201")
	b := h.newOrgWithUnits("LeakB", "leakb@jjne.test", "0716000202", []string{"Room 1"}, 250_000)

	paidAt := time.Now().UTC().AddDate(0, 0, -3).Format("2006-01-02")
	preview := b.client.importPreview(t, "payments", "probe.csv",
		"unit,renter_phone,amount,paid_at,method\n"+
			fmt.Sprintf("Room 1,%s,1000,%s,cash\n", a.renterPhone, paidAt)+
			fmt.Sprintf("Room 1,0716999998,1000,%s,cash\n", paidAt)).
		mustStatus(t, http.StatusCreated, "org B previews org A's renter")

	if got := num(t, preview, "batch", "error_count"); got != 2 {
		t.Fatalf("error_count = %v, want 2 — body: %s", got, preview.Raw)
	}
	rows := importRows(t, preview)
	theirs := rowError(t, rows, "2", "renter_phone")
	nobody := rowError(t, rows, "3", "renter_phone")
	if theirs == "" {
		t.Fatalf("another org's renter was accepted: %s", preview.Raw)
	}
	if theirs != nobody {
		t.Errorf("another org's renter answers %q while an unknown number answers %q; "+
			"the two must be indistinguishable", theirs, nobody)
	}
	if res := resolvedOfRow(t, rows, 2); res["renter_name"] != nil || res["user_id"] != nil {
		t.Errorf("the preview resolved a renter org B does not know: %v", res)
	}
	if strings.Contains(preview.Raw, "LeakA Renter") {
		t.Errorf("org A's renter name leaked into org B's preview: %s", preview.Raw)
	}
}

// TestImportRentersPreviewHidesAStrangersAccount: the renters sheet attaches an
// existing account rather than duplicating it, but it may only *say* so for a
// renter this org already knows. For anybody else the row reads "will be
// created or attached", under the name the sheet itself supplies.
func TestImportRentersPreviewHidesAStrangersAccount(t *testing.T) {
	h := newHarness(t)
	a := h.newPaymentFixture(t, "HideA", "0716000205", "+255716000206")
	b := h.newOrgWithUnits("HideB", "hideb@jjne.test", "0716000207", []string{"Room 1"}, 250_000)

	preview := b.client.importPreview(t, "renters", "stranger.csv",
		"full_name,phone,property,unit\nNot Their Name,"+a.renterPhone+",HideB Block A,Room 1\n").
		mustStatus(t, http.StatusCreated, "org B previews org A's renter")
	res := resolvedOfRow(t, importRows(t, preview), 2)
	if res["renter_create"] != true {
		t.Errorf("org B was told the account already exists: %v", res)
	}
	if res["user_id"] != nil {
		t.Errorf("org B was handed a stranger's user id: %v", res)
	}
	if res["full_name"] != "Not Their Name" || strings.Contains(preview.Raw, "HideA Renter") {
		t.Errorf("the stored name leaked instead of the sheet's own: %s", preview.Raw)
	}

	// The account is still reused, not duplicated: the commit attaches it.
	batchID := preview.str(t, "batch", "id")
	committed := b.client.do(http.MethodPost, "/imports/"+batchID+"/commit", nil).
		mustStatus(t, http.StatusOK, "commit")
	if got := num(t, committed, "created", "renters"); got != 0 {
		t.Errorf("created.renters = %v, want 0 — the existing account must be attached, not copied", got)
	}
	if got := num(t, committed, "created", "contracts"); got != 1 {
		t.Errorf("created.contracts = %v, want 1 — body: %s", got, committed.Raw)
	}
}

// TestImportUndoKeepsARenterAnotherOrgKnows: `users` is platform-global, so the
// undo's delete is too. An account another landlord has since taken on is not
// this org's to take back — it is detached here and left standing.
func TestImportUndoKeepsARenterAnotherOrgKnows(t *testing.T) {
	h := newHarness(t)
	a := h.newOrgWithUnits("KeepA", "keepa@jjne.test", "0716000210", []string{"Room 1"}, 250_000)
	b := h.newOrgWithUnits("KeepB", "keepb@jjne.test", "0716000211", []string{"Room 1"}, 250_000)

	const phone = "0716000212"
	batchID := a.client.importPreview(t, "renters", "a.csv",
		"full_name,phone,property,unit\nSalma Kileo,"+phone+",KeepA Block A,Room 1\n").
		mustStatus(t, http.StatusCreated, "org A preview").str(t, "batch", "id")
	if got := num(t, a.client.do(http.MethodPost, "/imports/"+batchID+"/commit", nil).
		mustStatus(t, http.StatusOK, "org A commit"), "created", "renters"); got != 1 {
		t.Fatalf("org A created %v renters, want 1", got)
	}

	// Org B takes the same person on.
	bBatch := b.client.importPreview(t, "renters", "b.csv",
		"full_name,phone,property,unit\nSalma Kileo,"+phone+",KeepB Block A,Room 1\n").
		mustStatus(t, http.StatusCreated, "org B preview").str(t, "batch", "id")
	b.client.do(http.MethodPost, "/imports/"+bBatch+"/commit", nil).
		mustStatus(t, http.StatusOK, "org B commit")

	undone := a.client.do(http.MethodPost, "/imports/"+batchID+"/undo", nil).
		mustStatus(t, http.StatusOK, "org A undo")
	if got := num(t, undone, "undone", "renters"); got != 0 {
		t.Errorf("undone.renters = %v, want 0 — org B still knows this person", got)
	}
	if !h.userLives(t, phone) {
		t.Fatal("org A's undo deleted an account org B holds a tenancy for")
	}
	if rows := listOf(t, b.client.do(http.MethodGet, "/renters", nil).
		mustStatus(t, http.StatusOK, "org B's directory")); len(rows) != 1 {
		t.Errorf("org B's renter directory = %+v, want the renter it took on", rows)
	}
}

// userLives answers whether an account with this number is still there, by the
// number's last nine digits: registration normalises `0716…` to `+255716…`.
func (h *harness) userLives(t *testing.T, phone string) bool {
	t.Helper()
	tail := phone
	if len(tail) > 9 {
		tail = tail[len(tail)-9:]
	}
	var n int
	if err := h.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM users WHERE phone LIKE '%' || $1 AND deleted_at IS NULL", tail).
		Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	return n > 0
}

// TestImportPaymentsUndoLeavesTheContractAlone: a payments row's `contract_id`
// names a tenancy that was already there. Undoing the batch reverses its money
// and stops — it may not reach for the contract, whatever status that contract
// happens to be in.
func TestImportPaymentsUndoLeavesTheContractAlone(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "PayUndo", "0716000220", "+255716000221")
	owner := fix.owner

	paidAt := time.Now().UTC().AddDate(0, 0, -4).Format("2006-01-02")
	batchID := owner.importPreview(t, "payments", "pay.csv",
		"unit,renter_phone,amount,paid_at,method,reference\n"+
			fmt.Sprintf("Room 1,%s,%d,%s,cash,HIST\n", fix.renterPhone, fix.amounts[0], paidAt)).
		mustStatus(t, http.StatusCreated, "preview").str(t, "batch", "id")
	owner.do(http.MethodPost, "/imports/"+batchID+"/commit", nil).
		mustStatus(t, http.StatusOK, "commit")

	// The one status the undo's contract statement can move. Reaching it here is
	// artificial — a payments sheet never resolves to an unsigned contract — and
	// that is the point: the gate must be the batch's kind, not the row's shape.
	h.setContractStatus(t, fix.contractID, "pending_signature")

	undone := owner.do(http.MethodPost, "/imports/"+batchID+"/undo", nil).
		mustStatus(t, http.StatusOK, "undo the payments batch")
	if got := num(t, undone, "undone", "payments"); got != 1 {
		t.Errorf("undone.payments = %v, want 1 — body: %s", got, undone.Raw)
	}
	if got := num(t, undone, "undone", "contracts"); got != 0 {
		t.Errorf("undone.contracts = %v, want 0 — a payments undo must not touch a contract", got)
	}
	if !h.contractLives(t, fix.contractID) {
		t.Fatal("undoing a payments import withdrew the renter's contract")
	}

	// Again, with the batch's own money taken out of the picture, so the
	// contract is protected by nothing except the gate itself.
	h.setContractStatus(t, fix.contractID, "active") // a payments sheet only resolves a running tenancy
	second := owner.importPreview(t, "payments", "pay2.csv",
		"unit,renter_phone,amount,paid_at,method,reference\n"+
			fmt.Sprintf("Room 1,%s,%d,%s,cash,HIST2\n", fix.renterPhone, fix.amounts[0], paidAt)).
		mustStatus(t, http.StatusCreated, "second preview").str(t, "batch", "id")
	owner.do(http.MethodPost, "/imports/"+second+"/commit", nil).
		mustStatus(t, http.StatusOK, "second commit")
	h.forgetPaymentsOfBatch(t, second)
	h.setContractStatus(t, fix.contractID, "pending_signature")

	bare := owner.do(http.MethodPost, "/imports/"+second+"/undo", nil).
		mustStatus(t, http.StatusOK, "undo a payments batch with no money left to reverse")
	if got := num(t, bare, "undone", "contracts"); got != 0 {
		t.Errorf("undone.contracts = %v, want 0 — only a renters batch owns a contract", got)
	}
	if !h.contractLives(t, fix.contractID) {
		t.Fatal("the payments undo followed resolved.contract_id into a contract it never created")
	}
}

// forgetPaymentsOfBatch removes a batch's payments outright, which the API
// never does. It is here to strip away the incidental protection the payment
// count gives a contract, so the undo's kind gate is the only thing left
// standing between a payments rollback and somebody's tenancy.
func (h *harness) forgetPaymentsOfBatch(t *testing.T, batchID string) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		"UPDATE payments SET deleted_at = now() WHERE import_batch_id = $1", batchID); err != nil {
		t.Fatalf("remove the batch's payments: %v", err)
	}
}

func (h *harness) setContractStatus(t *testing.T, contractID, status string) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		"UPDATE contracts SET status = $2 WHERE id = $1", contractID, status); err != nil {
		t.Fatalf("set contract status: %v", err)
	}
}

func (h *harness) contractLives(t *testing.T, contractID string) bool {
	t.Helper()
	var live bool
	if err := h.pool.QueryRow(context.Background(),
		"SELECT deleted_at IS NULL FROM contracts WHERE id = $1", contractID).Scan(&live); err != nil {
		t.Fatalf("read contract: %v", err)
	}
	return live
}

// TestImportRentersUndoKeepsASignedTenancy: signing does not move a contract
// off `pending_signature` — activation does — so status alone would let a
// rollback tear up a document the renter has put their name to. A signature is
// a reference, the contract stays, and the approval that created it stays with
// it.
func TestImportRentersUndoKeepsASignedTenancy(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("SignU", "signu@jjne.test", "0716000230", []string{"Room 1"}, 250_000)
	owner := fix.client

	const phone = "+255716000231"
	renter := h.registerRenter(phone, "SignU Renter", defaultPIN)
	renter.completeProfile(t, "SignU Renter", validNIDA)

	batchID := owner.importPreview(t, "renters", "signed.csv",
		"full_name,phone,property,unit\nSignU Renter,"+phone+",SignU Block A,Room 1\n").
		mustStatus(t, http.StatusCreated, "preview").str(t, "batch", "id")
	owner.do(http.MethodPost, "/imports/"+batchID+"/commit", nil).
		mustStatus(t, http.StatusOK, "commit")

	contracts := listOf(t, owner.do(http.MethodGet, "/contracts", nil).
		mustStatus(t, http.StatusOK, "contracts"))
	if len(contracts) != 1 {
		t.Fatalf("contracts = %d, want the one the import drew up", len(contracts))
	}
	contractID, _ := contracts[0]["id"].(string)
	h.signAsRenter(t, renter, contractID, phone)

	undone := owner.do(http.MethodPost, "/imports/"+batchID+"/undo", nil).
		mustStatus(t, http.StatusOK, "undo after the renter signed")
	if got := num(t, undone, "undone", "contracts"); got != 0 {
		t.Errorf("undone.contracts = %v, want 0 — the renter signed this one", got)
	}
	if got := num(t, undone, "undone", "renters"); got != 0 {
		t.Errorf("undone.renters = %v, want 0 — the account was not created by this import", got)
	}
	owner.do(http.MethodGet, "/contracts/"+contractID, nil).
		mustStatus(t, http.StatusOK, "the signed contract is still there")
	links := listOf(t, owner.do(http.MethodGet, "/link-requests?status=approved", nil).
		mustStatus(t, http.StatusOK, "link requests"))
	if len(links) != 1 {
		t.Errorf("approved link requests = %+v, want the one behind the signed contract", links)
	}
	if !h.userLives(t, phone) {
		t.Error("the undo deleted the renter's account")
	}
}

// TestImportedPaymentMatchesTheManualOne is the parity `applyImportedPayment`
// owes the ledger. The import writes its own payment rather than going through
// allocatePayment (a finished tenancy cannot: DECISIONS), so the two paths are
// held to the same result — the same schedule statuses, the same paid amounts
// and the same payment_allocations rows for the same money.
func TestImportedPaymentMatchesTheManualOne(t *testing.T) {
	h := newHarness(t)
	imported := h.newPaymentFixture(t, "ParityI", "0716000240", "+255716000241")
	manual := h.newPaymentFixture(t, "ParityM", "0716000242", "+255716000243")

	// Enough to settle the first instalment and spill onto the second, so the
	// comparison covers more than one allocation row.
	amount := imported.amounts[0] + 5_000
	paidAt := time.Now().UTC().AddDate(0, 0, -2)

	batchID := imported.owner.importPreview(t, "payments", "parity.csv",
		"unit,renter_phone,amount,paid_at,method,reference,note\n"+
			fmt.Sprintf("Room 1,%s,%d,%s,cash,PARITY,at the office\n",
				imported.renterPhone, amount, paidAt.Format("2006-01-02"))).
		mustStatus(t, http.StatusCreated, "preview").str(t, "batch", "id")
	imported.owner.do(http.MethodPost, "/imports/"+batchID+"/commit", nil).
		mustStatus(t, http.StatusOK, "commit the imported payment")

	manual.owner.recordPayment(map[string]any{
		"contract_id": manual.contractID, "amount": amount, "method": "cash",
		"reference": "PARITY", "note": "at the office",
		"paid_at": paidAt.Format(time.RFC3339), "allow_overpay_rollover": true,
	}).mustStatus(t, http.StatusCreated, "record the same payment by hand")

	gotStatuses, gotPaid, gotAllocs := paymentLedger(t, imported)
	wantStatuses, wantPaid, wantAllocs := paymentLedger(t, manual)
	if !reflect.DeepEqual(gotStatuses, wantStatuses) {
		t.Errorf("imported statuses = %v, hand-keyed = %v", gotStatuses, wantStatuses)
	}
	if !reflect.DeepEqual(gotPaid, wantPaid) {
		t.Errorf("imported paid_amount = %v, hand-keyed = %v", gotPaid, wantPaid)
	}
	if !reflect.DeepEqual(gotAllocs, wantAllocs) {
		t.Errorf("imported allocations = %v, hand-keyed = %v", gotAllocs, wantAllocs)
	}
	if len(gotAllocs) < 2 {
		t.Fatalf("the payment did not spill onto a second schedule: %v", gotAllocs)
	}
}

// paymentLedger reads one contract's schedules and the allocations written
// against them, keyed by the schedule's *position* rather than its id, so two
// different contracts can be compared row for row.
func paymentLedger(t *testing.T, fix paymentFixture) (statuses []string, paid []int64, allocs []string) {
	t.Helper()
	index := map[string]int{}
	for _, row := range listOf(t, fix.owner.do(http.MethodGet,
		"/contracts/"+fix.contractID+"/schedules", nil).
		mustStatus(t, http.StatusOK, "schedules")) {
		id, _ := row["id"].(string)
		index[id] = len(statuses)
		status, _ := row["status"].(string)
		statuses = append(statuses, status)
		paid = append(paid, int64(mustFloat(t, row, "paid_amount")))
	}
	allocs = []string{}
	for _, row := range listOf(t, fix.owner.do(http.MethodGet, "/payments?limit=50", nil).
		mustStatus(t, http.StatusOK, "payments")) {
		applied, _ := row["applied"].([]any)
		for _, a := range applied {
			m, _ := a.(map[string]any)
			id, _ := m["schedule_id"].(string)
			amount, _ := m["amount"].(float64)
			allocs = append(allocs, fmt.Sprintf("%d:%d", index[id], int64(amount)))
		}
	}
	sort.Strings(allocs)
	return statuses, paid, allocs
}

// TestImportTemplateServesHeadersAndColumns: the template download and the
// column reference come off the same route, so they cannot drift apart.
func TestImportTemplateServesHeadersAndColumns(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Tpl", "tpl@jjne.test", "0716000170", nil, 0)

	csv := fix.client.do(http.MethodGet, "/imports/templates/payments.csv", nil).
		mustStatus(t, http.StatusOK, "template csv")
	if !strings.HasPrefix(csv.Raw, "unit,renter_phone,amount,paid_at,method,reference,note") {
		t.Errorf("template header = %q", strings.SplitN(csv.Raw, "\n", 2)[0])
	}
	cols := fix.client.do(http.MethodGet, "/imports/templates/payments", nil).
		mustStatus(t, http.StatusOK, "template columns")
	if len(arrayOf(t, cols, "columns")) != 7 {
		t.Errorf("payments has %d columns, want 7 — body: %s", len(arrayOf(t, cols, "columns")), cols.Raw)
	}
	fix.client.do(http.MethodGet, "/imports/templates/nonsense.csv", nil).
		mustStatus(t, http.StatusNotFound, "unknown template")
}

// TestImportPreviewIsRateLimited: twenty uploads an hour, then a 429.
func TestImportPreviewIsRateLimited(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Rate", "rate@jjne.test", "0716000180", nil, 0)

	sheet := "property,unit,rent_amount\nRate Block A,R1,150000\n"
	for i := 0; i < 20; i++ {
		fix.client.importPreview(t, "units", "rate.csv", sheet).
			mustStatus(t, http.StatusCreated, fmt.Sprintf("preview %d", i+1))
	}
	fix.client.importPreview(t, "units", "rate.csv", sheet).
		mustStatus(t, http.StatusTooManyRequests, "the twenty-first preview")

	// Redis is ephemeral and the limiter fails open, so the ceiling is counted
	// again in Postgres. With the counters gone the answer must not change: a
	// restarted cache cannot hand an org twenty more uploads.
	h.forgetRateLimits(t, "rl:import:preview:*")
	fix.client.importPreview(t, "units", "rate.csv", sheet).
		mustStatus(t, http.StatusTooManyRequests, "the twenty-first preview against a cold cache")
}

// forgetRateLimits drops the limiter's counters, the way losing Redis does.
func (h *harness) forgetRateLimits(t *testing.T, pattern string) {
	t.Helper()
	ctx := context.Background()
	keys, err := h.redis.Keys(ctx, pattern).Result()
	if err != nil {
		t.Fatalf("list rate-limit keys: %v", err)
	}
	if len(keys) == 0 {
		t.Fatalf("no rate-limit keys matched %q; the counter is not where the test thinks", pattern)
	}
	if err := h.redis.Del(ctx, keys...).Err(); err != nil {
		t.Fatalf("clear rate-limit keys: %v", err)
	}
}

// TestImportLargeUnitsPreviewIsFast is the PLAN2 budget: 5 000 rows previewed
// in under five seconds, which is what stops the resolver from going back to
// the database once per line.
func TestImportLargeUnitsPreviewIsFast(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the 5 000-row preview in -short")
	}
	h := newHarness(t)
	fix := h.newOrgWithUnits("Bulk", "bulk@jjne.test", "0716000190", []string{"Room 1"}, 250_000)

	var sheet strings.Builder
	sheet.WriteString("property,unit,rent_amount\n")
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&sheet, "Bulk Block A,U%d,150000\n", i)
	}

	start := time.Now()
	preview := fix.client.importPreview(t, "units", "bulk.csv", sheet.String()).
		mustStatus(t, http.StatusCreated, "5000-row preview")
	elapsed := time.Since(start)

	if got := num(t, preview, "batch", "ok_count"); got != 5000 {
		t.Fatalf("ok_count = %v, want 5000", got)
	}
	if elapsed > 5*time.Second {
		t.Errorf("previewing 5 000 rows took %s, want under 5s", elapsed)
	}
	t.Logf("5 000-row units preview in %s", elapsed)

	// One row past the ceiling is refused whole.
	sheet.WriteString("Bulk Block A,U5000,150000\n")
	over := fix.client.importPreview(t, "units", "over.csv", sheet.String()).
		mustStatus(t, http.StatusBadRequest, "5001 rows")
	if over.Body["type"] != "too_many_rows" {
		t.Errorf("type = %v, want too_many_rows — body: %s", over.Body["type"], over.Raw)
	}
}
