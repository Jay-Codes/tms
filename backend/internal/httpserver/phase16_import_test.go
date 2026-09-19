package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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
