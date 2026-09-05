package httpserver

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/expense"
	"tms/backend/internal/httpx"
	"tms/backend/internal/report"
	"tms/backend/internal/storage"
	"tms/backend/internal/validate"
)

func notFoundExpense(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such expense")
}

func expenseVoidedConflict(w http.ResponseWriter) {
	conflictCode(w, "expense_voided", "expense is voided",
		"this expense has been voided; a correction is recorded, not edited")
}

// unprocessable writes the 422 the expense routes use for a body whose fields
// are each well-formed but which do not agree with the org's data — a unit that
// belongs to another property, a category that is not active. It is not a 400:
// nothing about the request is malformed, and the client cannot fix it by
// reformatting (SPEC §5.11, PLAN2 Phase 10).
func unprocessable(w http.ResponseWriter, fields validate.Fields) {
	httpx.WriteProblemFields(w, http.StatusUnprocessableEntity, "cannot record",
		"one or more fields do not match this organisation's records", fields)
}

// storageUnavailableReceipts is the 503 the receipt routes answer with MinIO
// down: the file cannot be stored or read, and pretending otherwise loses it.
func storageUnavailableReceipts(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusServiceUnavailable, "storage unavailable",
		"receipts cannot be uploaded or read right now")
}

// ------------------------------------------------------------ POST /expenses --

func (s *Server) handleCreateExpense(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		PropertyID string  `json:"property_id"`
		UnitID     *string `json:"unit_id"`
		CategoryID *string `json:"category_id"`
		Amount     int64   `json:"amount"`
		IncurredOn string  `json:"incurred_on"`
		Vendor     *string `json:"vendor"`
		Reference  *string `json:"reference"`
		Note       *string `json:"note"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}

	f := validate.Fields{}
	propertyID := uuidField(f, "property_id", body.PropertyID, true)
	unitID := optUUIDPtr(f, "unit_id", body.UnitID)
	categoryID := optUUIDPtr(f, "category_id", body.CategoryID)
	checkExpenseAmount(f, "amount", body.Amount)
	incurredOn := checkIncurredOn(f, "incurred_on", body.IncurredOn)
	vendor := f.MaxLen("vendor", strings.TrimSpace(db.StrVal(body.Vendor)), expenseVendorMax)
	reference := f.MaxLen("reference", strings.TrimSpace(db.StrVal(body.Reference)), expenseRefMax)
	note := f.MaxLen("note", strings.TrimSpace(db.StrVal(body.Note)), expenseNoteMax)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	if !s.expenseRefsValid(w, r, p.OrgID, propertyID, unitID, categoryID) {
		return
	}

	var created sqlc.Expense
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		created, err = q.CreateExpense(r.Context(), sqlc.CreateExpenseParams{
			OrgID: p.OrgID, PropertyID: propertyID, UnitID: unitID, CategoryID: categoryID,
			Amount: body.Amount, IncurredOn: incurredOn, Vendor: vendor,
			Reference: reference, Note: note, RecordedByUserID: p.UserID,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionExpenseCreate,
			EntityType:  audit.EntityExpense,
			EntityID:    db.UUIDString(created.ID),
			After:       expenseAudit(created),
		})
	}); err != nil {
		s.serverError(w, r, "expense.create.tx", err)
		return
	}

	out, ok := s.reloadExpense(w, r, created.ID, p.OrgID)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"expense": out})
}

// ------------------------------------------------------ PATCH /expenses/{id} --

func (s *Server) handlePatchExpense(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundExpense(w)
		return
	}

	// unit_id and category_id are read as raw JSON so an explicit `null` (clear
	// the field) is distinguishable from an absent member (leave it alone). A
	// pointer cannot tell those apart, and both are things a landlord means.
	var body struct {
		PropertyID *string         `json:"property_id"`
		UnitID     json.RawMessage `json:"unit_id"`
		CategoryID json.RawMessage `json:"category_id"`
		Amount     *int64          `json:"amount"`
		IncurredOn *string         `json:"incurred_on"`
		Vendor     *string         `json:"vendor"`
		Reference  *string         `json:"reference"`
		Note       *string         `json:"note"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}

	current, err := s.q.GetExpense(r.Context(), sqlc.GetExpenseParams{OrgID: p.OrgID, ID: id})
	if isNoRows(err) {
		notFoundExpense(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "expense.patch.get", err)
		return
	}
	if current.Status != expense.StatusRecorded {
		expenseVoidedConflict(w)
		return
	}

	f := validate.Fields{}
	params := sqlc.UpdateExpenseParams{OrgID: p.OrgID, ID: id, Amount: body.Amount}
	if body.PropertyID != nil {
		params.PropertyID = uuidField(f, "property_id", *body.PropertyID, true)
	}
	unitID, clearUnit := rawUUIDField(f, "unit_id", body.UnitID)
	params.UnitID, params.ClearUnit = unitID, clearUnit
	categoryID, clearCategory := rawUUIDField(f, "category_id", body.CategoryID)
	params.CategoryID, params.ClearCategory = categoryID, clearCategory
	if body.Amount != nil {
		checkExpenseAmount(f, "amount", *body.Amount)
	}
	if body.IncurredOn != nil {
		params.IncurredOn = checkIncurredOn(f, "incurred_on", *body.IncurredOn)
	}
	if body.Vendor != nil {
		v := f.MaxLen("vendor", strings.TrimSpace(*body.Vendor), expenseVendorMax)
		params.Vendor = &v
	}
	if body.Reference != nil {
		v := f.MaxLen("reference", strings.TrimSpace(*body.Reference), expenseRefMax)
		params.Reference = &v
	}
	if body.Note != nil {
		v := f.MaxLen("note", strings.TrimSpace(*body.Note), expenseNoteMax)
		params.Note = &v
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	// The references are checked against the row as it will be *after* the
	// patch: moving an expense to another property with its old unit still
	// attached has to be refused, and only the merged state can see that.
	property := current.PropertyID
	if params.PropertyID.Valid {
		property = params.PropertyID
	}
	unit := current.UnitID
	switch {
	case clearUnit:
		unit = pgtype.UUID{}
	case params.UnitID.Valid:
		unit = params.UnitID
	}
	category := current.CategoryID
	switch {
	case clearCategory:
		category = pgtype.UUID{}
	case params.CategoryID.Valid:
		category = params.CategoryID
	}
	if !s.expenseRefsValid(w, r, p.OrgID, property, unit, category) {
		return
	}

	var updated sqlc.Expense
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		updated, err = q.UpdateExpense(r.Context(), params)
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionExpenseUpdate,
			EntityType:  audit.EntityExpense,
			EntityID:    db.UUIDString(id),
			Before:      expenseRowAudit(current),
			After:       expenseAudit(updated),
		})
	}); err != nil {
		if isNoRows(err) {
			// The row was voided between the read and the write.
			expenseVoidedConflict(w)
			return
		}
		s.serverError(w, r, "expense.patch.tx", err)
		return
	}

	out, ok := s.reloadExpense(w, r, id, p.OrgID)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"expense": out})
}

// ------------------------------------------------ POST /expenses/{id}/void --

// handleVoidExpense is the expense ledger's correction, and it is deliberately
// the same shape as reversing a payment (FLOWS 12.3): the row stays, marked
// `voided` with the reason, and drops out of every total. Nothing is deleted,
// because a repair that was recorded and then cancelled is part of the record.
func (s *Server) handleVoidExpense(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundExpense(w)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	reason := f.MaxLen("reason", f.Required("reason", body.Reason), voidReasonMax)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	current, err := s.q.GetExpense(r.Context(), sqlc.GetExpenseParams{OrgID: p.OrgID, ID: id})
	if isNoRows(err) {
		notFoundExpense(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "expense.void.get", err)
		return
	}
	if current.Status != expense.StatusRecorded {
		expenseVoidedConflict(w)
		return
	}

	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		voided, err := q.VoidExpense(r.Context(), sqlc.VoidExpenseParams{
			OrgID: p.OrgID, ID: id, VoidReason: &reason,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionExpenseVoid,
			EntityType:  audit.EntityExpense,
			EntityID:    db.UUIDString(id),
			Before:      map[string]any{"status": current.Status, "amount": current.Amount},
			After:       map[string]any{"status": voided.Status, "reason": reason},
		})
	}); err != nil {
		if isNoRows(err) {
			expenseVoidedConflict(w)
			return
		}
		s.serverError(w, r, "expense.void.tx", err)
		return
	}

	out, ok := s.reloadExpense(w, r, id, p.OrgID)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"expense": out})
}

// ------------------------------------------------- GET /expenses, /{id} --

func (s *Server) handleListExpenses(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	qs := r.URL.Query()
	f := validate.Fields{}

	format := "json"
	if v := strings.TrimSpace(qs.Get("format")); v != "" {
		format = f.OneOf("format", strings.ToLower(v), "json", "csv")
	}
	filters := parseExpenseFilters(f, qs)
	limit, cursor := parseExpensePage(r, f)
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	if format == "csv" {
		// The export is the whole filtered set, not a page: a spreadsheet of
		// the first fifty rows would be a worse answer than none. The cap is
		// what keeps "the whole set" from meaning "the whole database".
		limit = expenseCSVMaxRows
		cursor = expenseCursor{}
	}

	rows, err := s.q.ListExpenses(r.Context(), sqlc.ListExpensesParams{
		OrgID: p.OrgID, Status: filters.Status,
		PropertyID: filters.PropertyID, UnitID: filters.UnitID, CategoryID: filters.CategoryID,
		FromDate: filters.From, ToDate: filters.To, Q: filters.Q,
		CursorIncurred: cursor.Incurred, CursorCreated: cursor.Created, CursorID: cursor.ID,
		RowLimit: limit,
	})
	if err != nil {
		s.serverError(w, r, "expense.list", err)
		return
	}

	items := make([]expenseResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, toExpense(expenseRowOfList(row)))
	}
	if format == "csv" {
		writeExpensesCSV(w, items, filters)
		return
	}

	totals, err := s.q.ListExpenseTotals(r.Context(), sqlc.ListExpenseTotalsParams{
		OrgID: p.OrgID, Status: filters.Status,
		PropertyID: filters.PropertyID, UnitID: filters.UnitID, CategoryID: filters.CategoryID,
		FromDate: filters.From, ToDate: filters.To, Q: filters.Q,
	})
	if err != nil {
		s.serverError(w, r, "expense.list.totals", err)
		return
	}

	var next *string
	if len(rows) > 0 && len(rows) >= int(limit) {
		last := rows[len(rows)-1]
		c := encodeExpenseCursor(last.IncurredOn.Time, last.CreatedAt.Time, db.UUIDString(last.ID))
		next = &c
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items": items, "next_cursor": next,
		"totals": expense.Total{Amount: totals.Amount, Count: totals.RowCount},
	})
}

func (s *Server) handleGetExpense(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundExpense(w)
		return
	}
	out, ok := s.reloadExpense(w, r, id, p.OrgID)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"expense": out})
}

// expenseCSVHeader is the export's column order. It is part of the contract: a
// landlord's spreadsheet formulas point at column letters.
//
//nolint:gochecknoglobals // fixed vocabulary, read-only.
var expenseCSVHeader = []string{
	"date", "property", "unit", "category", "vendor", "reference",
	"amount", "status", "note", "recorded_by",
}

func writeExpensesCSV(w http.ResponseWriter, items []expenseResponse, filters expenseFilters) {
	from, to := filters.FromLabel, filters.ToLabel
	if from == "" {
		from = "all"
	}
	if to == "" {
		to = time.Now().In(report.Zone()).Format(dateLayout)
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="expenses-`+from+`-`+to+`.csv"`)
	w.WriteHeader(http.StatusOK)

	cw := csv.NewWriter(w)
	_ = cw.Write(expenseCSVHeader)
	rec := make([]string, len(expenseCSVHeader))
	for _, it := range items {
		// Every free-text cell is defused before it is written: a vendor named
		// `=HYPERLINK(...)` is a formula the landlord's spreadsheet would run
		// on open. Commas, quotes and newlines are `encoding/csv`'s job.
		rec[0] = it.IncurredOn
		rec[1] = report.CSVCell(it.Property.Name)
		rec[2] = ""
		if it.Unit != nil {
			rec[2] = report.CSVCell(it.Unit.Name)
		}
		rec[3] = ""
		if it.Category != nil {
			rec[3] = report.CSVCell(it.Category.Name)
		}
		rec[4] = report.CSVCell(it.Vendor)
		rec[5] = report.CSVCell(it.Reference)
		rec[6] = strconv.FormatInt(it.Amount, 10)
		rec[7] = it.Status
		rec[8] = report.CSVCell(it.Note)
		rec[9] = ""
		if it.RecordedBy != nil {
			rec[9] = report.CSVCell(it.RecordedBy.Name)
		}
		_ = cw.Write(rec)
	}
	cw.Flush()
}

// ------------------------------------------------- GET /expenses/summary --

// handleExpenseSummary is the all-properties view: what was spent in the
// window, grouped by property or by category, beside the same figure for the
// window before it.
//
// Groups are zero-filled from the org's properties (or its active categories)
// rather than from the expenses themselves, so a chart keeps its bars — and its
// colours — when a month happens to have no spend under one of them.
func (s *Server) handleExpenseSummary(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	qs := r.URL.Query()
	f := validate.Fields{}

	groupBy := "property"
	if v := strings.TrimSpace(qs.Get("group_by")); v != "" {
		groupBy = f.OneOf("group_by", strings.ToLower(v), "property", "category")
	}
	propertyID := optQueryUUID(f, "property_id", qs.Get("property_id"))
	window, ok := reportWindowOf(w, f, qs)
	if !ok {
		return
	}

	from, to := dateParam(window.From), dateParam(window.To)
	prevFrom, prevTo := dateParam(window.Previous.From), dateParam(window.Previous.To)

	groups := make([]expense.Group, 0, len(expense.DefaultCategories))
	switch groupBy {
	case "category":
		rows, err := s.q.ExpenseSummaryByCategory(r.Context(), sqlc.ExpenseSummaryByCategoryParams{
			OrgID: p.OrgID, FromDate: from, ToDate: to, PropertyID: propertyID,
		})
		if err != nil {
			s.serverError(w, r, "expense.summary.category", err)
			return
		}
		for _, row := range rows {
			id := db.UUIDString(row.ID)
			groups = append(groups, expense.Group{
				ID: &id, Name: row.Name, Amount: row.Amount, Count: row.RowCount,
			})
		}
		// Rows filed under no category are not a category, so they cannot come
		// out of that query — but they are money spent, and a summary that
		// silently dropped them would not add up to the ledger's own total.
		unfiled, err := s.q.ExpenseUncategorisedTotal(r.Context(), sqlc.ExpenseUncategorisedTotalParams{
			OrgID: p.OrgID, FromDate: from, ToDate: to, PropertyID: propertyID,
		})
		if err != nil {
			s.serverError(w, r, "expense.summary.uncategorised", err)
			return
		}
		if unfiled.RowCount > 0 {
			groups = append(groups, expense.Group{
				Name: "Uncategorised", Amount: unfiled.Amount, Count: unfiled.RowCount,
			})
		}
	default:
		rows, err := s.q.ExpenseSummaryByProperty(r.Context(), sqlc.ExpenseSummaryByPropertyParams{
			OrgID: p.OrgID, FromDate: from, ToDate: to, PropertyID: propertyID,
		})
		if err != nil {
			s.serverError(w, r, "expense.summary.property", err)
			return
		}
		for _, row := range rows {
			id := db.UUIDString(row.ID)
			groups = append(groups, expense.Group{
				ID: &id, Name: row.Name, Amount: row.Amount, Count: row.RowCount,
			})
		}
	}
	expense.SortGroups(groups)

	total, err := s.q.ExpenseWindowTotal(r.Context(), sqlc.ExpenseWindowTotalParams{
		OrgID: p.OrgID, FromDate: from, ToDate: to, PropertyID: propertyID,
	})
	if err != nil {
		s.serverError(w, r, "expense.summary.total", err)
		return
	}
	previous, err := s.q.ExpenseWindowTotal(r.Context(), sqlc.ExpenseWindowTotalParams{
		OrgID: p.OrgID, FromDate: prevFrom, ToDate: prevTo, PropertyID: propertyID,
	})
	if err != nil {
		s.serverError(w, r, "expense.summary.previous", err)
		return
	}

	WriteJSON(w, http.StatusOK, expenseSummaryResponse{
		Window: expenseWindowResponse{
			From: window.From.Format(dateLayout), To: window.To.Format(dateLayout),
			Cadence: window.Cadence,
		},
		Previous: expenseWindowResponse{
			From: window.Previous.From.Format(dateLayout), To: window.Previous.To.Format(dateLayout),
			Cadence: window.Cadence,
		},
		GroupBy:       groupBy,
		Groups:        groups,
		Total:         expense.Total{Amount: total.Amount, Count: total.RowCount},
		PreviousTotal: expense.Total{Amount: previous.Amount, Count: previous.RowCount},
		ChangePct:     expense.ChangePct(total.Amount, previous.Amount),
	})
}

// ------------------------------------------------------------- receipts --

// handleExpenseReceiptUpload mints the presigned PUT. The key is derived from
// the org and the expense — never from anything the client sends — so a caller
// cannot address another org's prefix whatever they ask for.
func (s *Server) handleExpenseReceiptUpload(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	// Counted before object storage is consulted: the limit is on how often an
	// org may ask, and it must hold whether or not MinIO is reachable.
	if res := s.limiter.Allow(r.Context(), "expense:receipt:"+p.OrgIDString(),
		receiptUploadLimit, receiptUploadWindow); !res.Allowed {
		tooMany(w, res, "too many receipt uploads; try again shortly")
		return
	}

	var body struct {
		ContentType string `json:"content_type"`
		Size        int64  `json:"size"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	contentType := strings.ToLower(strings.TrimSpace(body.ContentType))
	ext, ok := receiptContentTypes[contentType]
	if !ok {
		f.Add("content_type", "must be image/jpeg, image/png or application/pdf")
	}
	if body.Size <= 0 || body.Size > receiptMaxBytes {
		f.Add("size", "must be between 1 byte and 5 MiB")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	row, ok := s.liveExpense(w, r, p.OrgID)
	if !ok {
		return
	}
	if s.deps.Storage == nil {
		storageUnavailableReceipts(w)
		return
	}

	objectKey := receiptKey(p.OrgIDString(), db.UUIDString(row.ID), ext)
	url, err := s.deps.Storage.PresignPut(r.Context(), storage.BucketReceipts, objectKey, receiptUploadTTL)
	if err != nil {
		s.logger.Error("receipt presign put failed", "error", err)
		storageUnavailableReceipts(w)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"upload_url": url,
		"object_key": objectKey,
		"expires_in": int(receiptUploadTTL.Seconds()),
		// The completion callback checks the type MinIO recorded, so the PUT
		// has to carry it: a presigned URL does not set it for the client.
		"headers": map[string]string{"Content-Type": contentType},
	})
}

// handleExpenseReceiptComplete records an upload that actually landed. A
// presigned PUT can enforce neither type nor size, so both are checked here
// against what MinIO holds, and a rejected object is removed rather than left
// unreferenced in the bucket (SPEC §7).
func (s *Server) handleExpenseReceiptComplete(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		ObjectKey string `json:"object_key"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	row, ok := s.liveExpense(w, r, p.OrgID)
	if !ok {
		return
	}
	if s.deps.Storage == nil {
		storageUnavailableReceipts(w)
		return
	}

	// The key is rebuilt from the org and the expense rather than trusted. A
	// client may name which of the three it uploaded; it may not name where.
	candidates := receiptKeys(p.OrgIDString(), db.UUIDString(row.ID))
	if sent := strings.TrimSpace(body.ObjectKey); sent != "" {
		if !containsString(candidates, sent) {
			httpx.WriteProblemFields(w, http.StatusBadRequest, "invalid object key",
				"that key was not issued for this expense",
				map[string]string{"object_key": "must be an upload issued for this expense"})
			return
		}
		candidates = []string{sent}
	}

	var (
		key  string
		info storage.ObjectInfo
	)
	for _, candidate := range candidates {
		got, err := s.deps.Storage.Stat(r.Context(), storage.BucketReceipts, candidate)
		if err == nil {
			key, info = candidate, got
			break
		}
	}
	if key == "" {
		httpx.WriteProblemFields(w, http.StatusBadRequest, "upload not found",
			"no uploaded receipt was found for this expense",
			map[string]string{"object_key": "no object has been uploaded under this key"})
		return
	}

	reject := func(field, msg string) {
		if err := s.deps.Storage.Remove(r.Context(), storage.BucketReceipts, key); err != nil {
			s.logger.Warn("could not remove rejected receipt", "object_key", key, "error", err)
		}
		httpx.WriteProblemFields(w, http.StatusBadRequest, "invalid receipt",
			"the uploaded receipt was rejected", map[string]string{field: msg})
	}
	if info.Size > receiptMaxBytes {
		reject("size", "must be at most 5 MiB")
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(info.ContentType))
	ext, allowed := receiptContentTypes[contentType]
	if !allowed || receiptKey(p.OrgIDString(), db.UUIDString(row.ID), ext) != key {
		// Either the object is not one of the three accepted types, or it is a
		// type that disagrees with the extension the key was issued under — a
		// PDF uploaded to the `.png` URL, which would later be served as an
		// image and fail to open.
		reject("content_type", "must be image/jpeg, image/png or application/pdf")
		return
	}

	size := info.Size
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.SetExpenseReceipt(r.Context(), sqlc.SetExpenseReceiptParams{
			OrgID: p.OrgID, ID: row.ID,
			ReceiptObjectKey: &key, ReceiptContentType: &contentType, ReceiptSize: &size,
		}); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionExpenseReceiptAttach,
			EntityType:  audit.EntityExpense,
			EntityID:    db.UUIDString(row.ID),
			After: map[string]any{
				"object_key": key, "content_type": contentType, "size": size,
			},
		})
	}); err != nil {
		s.serverError(w, r, "expense.receipt.complete.tx", err)
		return
	}

	out, ok := s.reloadExpense(w, r, row.ID, p.OrgID)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"expense": out})
}

// handleExpenseReceiptView issues the short-lived read link for the viewer.
func (s *Server) handleExpenseReceiptView(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundExpense(w)
		return
	}
	row, err := s.q.GetExpense(r.Context(), sqlc.GetExpenseParams{OrgID: p.OrgID, ID: id})
	if isNoRows(err) {
		notFoundExpense(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "expense.receipt.get", err)
		return
	}
	if row.ReceiptObjectKey == nil || *row.ReceiptObjectKey == "" {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no receipt has been uploaded")
		return
	}
	if s.deps.Storage == nil {
		storageUnavailableReceipts(w)
		return
	}
	url, err := s.deps.Storage.PresignGet(r.Context(), storage.BucketReceipts, *row.ReceiptObjectKey, receiptReadTTL)
	if err != nil {
		s.logger.Error("receipt presign get failed", "error", err)
		storageUnavailableReceipts(w)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"url": url, "expires_in": int(receiptReadTTL.Seconds()),
	})
}

// handleExpenseReceiptDelete detaches a receipt and removes the object.
//
// This is the one place in the ledger where something is really deleted, and it
// is deliberate: the receipt is an attachment, not the record. The expense row
// — the accounting fact — is untouched, and the removal is audited.
func (s *Server) handleExpenseReceiptDelete(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundExpense(w)
		return
	}
	row, err := s.q.GetExpense(r.Context(), sqlc.GetExpenseParams{OrgID: p.OrgID, ID: id})
	if isNoRows(err) {
		notFoundExpense(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "expense.receipt.delete.get", err)
		return
	}
	if row.ReceiptObjectKey == nil || *row.ReceiptObjectKey == "" {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no receipt has been uploaded")
		return
	}
	key := *row.ReceiptObjectKey

	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.SetExpenseReceipt(r.Context(), sqlc.SetExpenseReceiptParams{
			OrgID: p.OrgID, ID: id,
		}); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionExpenseReceiptRemove,
			EntityType:  audit.EntityExpense,
			EntityID:    db.UUIDString(id),
			Before:      map[string]any{"object_key": key},
		})
	}); err != nil {
		s.serverError(w, r, "expense.receipt.delete.tx", err)
		return
	}
	// The row no longer references the object, so a failure here leaves a file
	// nothing points at — untidy, not incorrect, and not worth failing the
	// request the landlord asked for.
	if s.deps.Storage != nil {
		if err := s.deps.Storage.Remove(r.Context(), storage.BucketReceipts, key); err != nil {
			s.logger.Warn("could not remove detached receipt", "object_key", key, "error", err)
		}
	}

	out, ok := s.reloadExpense(w, r, id, p.OrgID)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"expense": out})
}

// --------------------------------------------------------------- helpers --

// receiptKey is the object key for one expense's receipt: `{org}/{expense}.ext`
// (PLAN2 Phase 10). Both segments are ids the server holds, so the key cannot
// be steered by a request.
func receiptKey(orgID, expenseID, ext string) string {
	return orgID + "/" + expenseID + "." + ext
}

// receiptKeys is every key an expense's receipt could have been uploaded under
// — one per accepted content type. The completion callback stats them because
// the extension depends on what the client presigned for.
func receiptKeys(orgID, expenseID string) []string {
	return []string{
		receiptKey(orgID, expenseID, "jpg"),
		receiptKey(orgID, expenseID, "png"),
		receiptKey(orgID, expenseID, "pdf"),
	}
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// liveExpense loads the expense named by the route and refuses a voided one:
// the three receipt writes all need the same two checks.
func (s *Server) liveExpense(w http.ResponseWriter, r *http.Request, orgID pgtype.UUID) (sqlc.GetExpenseRow, bool) {
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundExpense(w)
		return sqlc.GetExpenseRow{}, false
	}
	row, err := s.q.GetExpense(r.Context(), sqlc.GetExpenseParams{OrgID: orgID, ID: id})
	if isNoRows(err) {
		notFoundExpense(w)
		return sqlc.GetExpenseRow{}, false
	}
	if err != nil {
		s.serverError(w, r, "expense.get", err)
		return sqlc.GetExpenseRow{}, false
	}
	if row.Status != expense.StatusRecorded {
		expenseVoidedConflict(w)
		return sqlc.GetExpenseRow{}, false
	}
	return row, true
}

// reloadExpense re-reads an expense with its joined names for the response.
func (s *Server) reloadExpense(
	w http.ResponseWriter, r *http.Request, id, orgID pgtype.UUID,
) (expenseResponse, bool) {
	row, err := s.q.GetExpense(r.Context(), sqlc.GetExpenseParams{OrgID: orgID, ID: id})
	if isNoRows(err) {
		notFoundExpense(w)
		return expenseResponse{}, false
	}
	if err != nil {
		s.serverError(w, r, "expense.reload", err)
		return expenseResponse{}, false
	}
	return toExpense(row), true
}

// expenseRefsValid checks the three ids an expense points at against the org's
// own data: the property must be the caller's (404, like every other id that is
// not theirs), and the unit and category must fit it (422 — the request is
// well-formed, it just does not describe this org's world).
func (s *Server) expenseRefsValid(
	w http.ResponseWriter, r *http.Request, orgID, propertyID, unitID, categoryID pgtype.UUID,
) bool {
	if _, err := s.q.GetProperty(r.Context(), sqlc.GetPropertyParams{OrgID: orgID, ID: propertyID}); err != nil {
		if isNoRows(err) {
			notFoundProperty(w)
			return false
		}
		s.serverError(w, r, "expense.refs.property", err)
		return false
	}

	f := validate.Fields{}
	if unitID.Valid {
		unit, err := s.q.GetUnit(r.Context(), sqlc.GetUnitParams{OrgID: orgID, ID: unitID})
		switch {
		case isNoRows(err):
			f.Add("unit_id", "must be a unit of the chosen property")
		case err != nil:
			s.serverError(w, r, "expense.refs.unit", err)
			return false
		case db.UUIDString(unit.PropertyID) != db.UUIDString(propertyID):
			f.Add("unit_id", "must be a unit of the chosen property")
		}
	}
	if categoryID.Valid {
		category, err := s.q.GetExpenseCategory(r.Context(), sqlc.GetExpenseCategoryParams{
			OrgID: orgID, ID: categoryID,
		})
		switch {
		case isNoRows(err):
			f.Add("category_id", "must be an active expense category of this organisation")
		case err != nil:
			s.serverError(w, r, "expense.refs.category", err)
			return false
		case !category.Active:
			f.Add("category_id", "must be an active expense category of this organisation")
		}
	}
	if !f.Empty() {
		unprocessable(w, f)
		return false
	}
	return true
}

// optUUIDPtr parses an optional id field sent as a nullable string.
func optUUIDPtr(f validate.Fields, name string, in *string) pgtype.UUID {
	if in == nil {
		return pgtype.UUID{}
	}
	return uuidField(f, name, *in, false)
}

// rawUUIDField reads a PATCH member that may be absent, null (clear it) or an
// id. It returns the id and whether the field is being cleared.
func rawUUIDField(f validate.Fields, name string, raw json.RawMessage) (pgtype.UUID, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) == 0 {
		return pgtype.UUID{}, false
	}
	if trimmed == "null" {
		return pgtype.UUID{}, true
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		f.Add(name, "must be an id (UUID) or null")
		return pgtype.UUID{}, false
	}
	if strings.TrimSpace(v) == "" {
		return pgtype.UUID{}, true
	}
	return uuidField(f, name, v, false), false
}

// orMonth defaults a cadence the summary always needs one of.
func orMonth(cadence string) string {
	if v := strings.TrimSpace(cadence); v != "" {
		return v
	}
	return "month"
}

// expenseAudit is the audit payload for a stored expense row. It carries the
// ledger's facts, not its joined display names: a trail is read for what
// changed, and a property's name is not part of the expense.
func expenseAudit(e sqlc.Expense) map[string]any {
	return map[string]any{
		"property_id": db.UUIDString(e.PropertyID),
		"unit_id":     nullableID(e.UnitID),
		"category_id": nullableID(e.CategoryID),
		"amount":      e.Amount,
		"incurred_on": e.IncurredOn.Time.Format(dateLayout),
		"vendor":      e.Vendor,
		"reference":   e.Reference,
		"note":        e.Note,
		"status":      e.Status,
	}
}

func expenseRowAudit(e sqlc.GetExpenseRow) map[string]any {
	return expenseAudit(sqlc.Expense{
		PropertyID: e.PropertyID, UnitID: e.UnitID, CategoryID: e.CategoryID,
		Amount: e.Amount, IncurredOn: e.IncurredOn, Vendor: e.Vendor,
		Reference: e.Reference, Note: e.Note, Status: e.Status,
	})
}

func nullableID(id pgtype.UUID) any {
	if !id.Valid {
		return nil
	}
	return db.UUIDString(id)
}
