package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/importer"
	"tms/backend/internal/payment"
	"tms/backend/internal/validate"
)

// Phase 16 §16.2 — bringing previous records in from a spreadsheet.
//
// The shape of the feature is "look before you leap": a preview writes nothing
// but a record of the file and what each line would do, and a commit runs the
// ok rows through the ordinary service paths inside ONE transaction, because a
// landlord must never end up with half a spreadsheet. An undo within 24 h takes
// the batch back off, through the same reversal path a mistyped payment uses.
//
// Resolution (which property is "Block A"? whose contract is unit A1's?) lives
// here beside those service calls; the file and cell rules live in
// internal/importer, which never sees a database.

// errImportRowFailed is a row that passed the preview and no longer passes at
// commit time — the unit was let in the meantime, the property was renamed. It
// fails the whole transaction: the landlord previews again and sees why.
type importRowFailedError struct {
	Line   int
	Column string
	Reason string
}

func (e *importRowFailedError) Error() string {
	return fmt.Sprintf("import: line %d: %s: %s", e.Line, e.Column, e.Reason)
}

// errImportNoLongerPreviewed is a batch somebody else already committed.
var errImportNoLongerPreviewed = errors.New("import: batch is no longer previewed")

func notFoundImportBatch(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such import batch")
}

// ------------------------------------ GET /imports/templates/{kind}.csv --

// handleImportTemplate serves the blank sheet: the fixed English machine
// headers and one example line. The headers are identical in both app
// languages — they are what the parser matches, not what the screen shows
// (PLAN2 §16.2) — so the page prints the column reference beside the download.
func (s *Server) handleImportTemplate(w http.ResponseWriter, r *http.Request) {
	raw := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "kind")))
	kind := strings.TrimSuffix(raw, ".csv")
	body := importer.TemplateCSV(kind)
	if body == nil {
		httpx.WriteProblem(w, http.StatusNotFound, "not found",
			"no such import template; the kinds are units, renters and payments")
		return
	}
	// Without the .csv suffix the same path answers the column reference the
	// screen prints beside the download button, so the two never drift apart.
	if raw == kind {
		WriteJSON(w, http.StatusOK, map[string]any{
			"kind": kind, "columns": importColumns(kind),
		})
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="tms-import-`+kind+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// ------------------------------------------------- POST /imports/preview --

func (s *Server) handleImportPreview(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	if res := s.limiter.Allow(r.Context(), "import:preview:"+p.OrgIDString(),
		importPreviewLimit, importPreviewWindow); !res.Allowed {
		tooMany(w, res, "too many import previews this hour; try again later")
		return
	}

	// The body is read under a hard ceiling before anything else looks at it.
	r.Body = http.MaxBytesReader(w, r.Body, importer.MaxBytes+importMultipartMemory)
	if err := r.ParseMultipartForm(importMultipartMemory); err != nil {
		httpx.WriteProblemCode(w, http.StatusBadRequest, "bad_upload", "upload could not be read",
			"send the file as multipart/form-data with a `file` part and a `kind` field, at most 2 MiB")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	kind := strings.ToLower(strings.TrimSpace(r.FormValue("kind")))
	if !importer.Valid(kind) {
		f := validate.Fields{}
		f.Add("kind", "must be units, renters or payments")
		badRequest(w, f)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		f := validate.Fields{}
		f.Add("file", "a CSV file is required")
		badRequest(w, f)
		return
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, importer.MaxBytes+1))
	if err != nil {
		httpx.WriteProblemCode(w, http.StatusBadRequest, "bad_upload", "upload could not be read",
			"the file could not be read")
		return
	}
	if len(data) > importer.MaxBytes {
		httpx.WriteProblemCode(w, http.StatusBadRequest, importer.CodeFileTooLarge,
			"file too large", "the file is larger than 2 MiB; split it")
		return
	}

	rows, err := importer.Parse(kind, data)
	if !writeImportParseError(w, err) {
		return
	}

	resolved, err := s.resolveImportRows(r.Context(), s.q, p, kind, rows)
	if err != nil {
		s.serverError(w, r, "import.preview.resolve", err)
		return
	}
	okCount, errorCount := countImportRows(resolved)

	filename := ""
	if header != nil {
		filename = trimName(header.Filename)
	}

	var batch sqlc.ImportBatch
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		batch, err = q.CreateImportBatch(r.Context(), sqlc.CreateImportBatchParams{
			OrgID: p.OrgID, Kind: kind, Filename: filename,
			RowCount: int32(len(resolved)), OkCount: int32(okCount), ErrorCount: int32(errorCount),
			CreatedByUserID: p.UserID,
		})
		if err != nil {
			return err
		}
		for _, row := range resolved {
			rawJSON, err := json.Marshal(row.raw)
			if err != nil {
				return err
			}
			created, err := q.CreateImportRow(r.Context(), sqlc.CreateImportRowParams{
				BatchID: batch.ID, OrgID: p.OrgID, Line: int32(row.line),
				Raw: rawJSON, Errors: jsonOrNil(row.errs), Resolved: jsonOrNil(row.resolved),
			})
			if err != nil {
				return err
			}
			row.rowID = created.ID
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionImportPreview,
			EntityType:  audit.EntityImportBatch,
			EntityID:    db.UUIDString(batch.ID),
			After: map[string]any{
				"kind": kind, "filename": filename, "row_count": len(resolved),
				"ok_count": okCount, "error_count": errorCount,
			},
		})
	}); err != nil {
		s.serverError(w, r, "import.preview.tx", err)
		return
	}

	WriteJSON(w, http.StatusCreated, map[string]any{
		"batch": s.reloadImportBatch(r.Context(), p, batch),
		"rows":  previewRows(resolved),
	})
}

// writeImportParseError turns a file-level refusal into its 400. It returns
// false when it wrote one.
func writeImportParseError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	var he *importer.HeaderError
	if errors.As(err, &he) {
		w.Header().Set("Content-Type", httpx.ProblemContentType)
		w.WriteHeader(http.StatusBadRequest)
		WriteRawJSON(w, map[string]any{
			"type":    "csv_header_mismatch",
			"title":   "the header row does not match the template",
			"status":  http.StatusBadRequest,
			"detail":  he.Error(),
			"missing": stringsOrEmpty(he.Missing),
			"unknown": stringsOrEmpty(he.Unknown),
		})
		return false
	}
	var fe *importer.FileError
	if errors.As(err, &fe) {
		httpx.WriteProblemCode(w, http.StatusBadRequest, fe.Code, "the file could not be imported", fe.Message)
		return false
	}
	httpx.WriteProblemCode(w, http.StatusBadRequest, importer.CodeUnreadable,
		"the file could not be imported", err.Error())
	return false
}

// ------------------------------------------------------------ GET /imports --

func (s *Server) handleListImports(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	f := validate.Fields{}
	page := parseListPage(r, f)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	rows, err := s.q.ListImportBatches(r.Context(), sqlc.ListImportBatchesParams{
		OrgID: p.OrgID, CursorAt: page.CursorAt, CursorID: page.CursorID, RowLimit: page.Limit,
	})
	if err != nil {
		s.serverError(w, r, "import.list", err)
		return
	}
	now := time.Now().UTC()
	items := make([]importBatchResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, toImportBatch(batchOfList(row), row.CreatedByName, now))
	}
	var next *string
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		next = nextCursor(len(rows), page.Limit, last.CreatedAt.Time, db.UUIDString(last.ID))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// ------------------------------------------------------- GET /imports/{id} --

func (s *Server) handleGetImport(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	batch, ok := s.orgImportBatch(w, r, p)
	if !ok {
		return
	}
	rows, err := s.q.ListImportRows(r.Context(), sqlc.ListImportRowsParams{
		OrgID: p.OrgID, BatchID: batch.ID,
	})
	if err != nil {
		s.serverError(w, r, "import.get.rows", err)
		return
	}
	items := make([]importRowResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, toImportRow(row))
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"batch": toImportBatch(batchOfGet(batch), batch.CreatedByName, time.Now().UTC()),
		"rows":  items,
	})
}

// orgImportBatch loads a batch inside the caller's org (404 otherwise).
func (s *Server) orgImportBatch(w http.ResponseWriter, r *http.Request, p auth.Principal,
) (sqlc.GetImportBatchRow, bool) {
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundImportBatch(w)
		return sqlc.GetImportBatchRow{}, false
	}
	row, err := s.q.GetImportBatch(r.Context(), sqlc.GetImportBatchParams{OrgID: p.OrgID, ID: id})
	if isNoRows(err) {
		notFoundImportBatch(w)
		return sqlc.GetImportBatchRow{}, false
	}
	if err != nil {
		s.serverError(w, r, "import.get", err)
		return sqlc.GetImportBatchRow{}, false
	}
	return row, true
}

// ----------------------------------------------- POST /imports/{id}/commit --

func (s *Server) handleCommitImport(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	// The body is optional: "Commit N rows" sends nothing, and only the
	// "skip M rows with errors" button has anything to say.
	var body struct {
		SkipErrors bool `json:"skip_errors"`
	}
	if r.ContentLength > 0 {
		if !DecodeJSON(w, r, &body) {
			return
		}
	}
	existing, ok := s.orgImportBatch(w, r, p)
	if !ok {
		return
	}
	switch existing.Status {
	case importCommitted:
		conflictCode(w, "already_committed", "batch already committed",
			"this file has already been imported")
		return
	case importUndone:
		conflictCode(w, "already_undone", "batch already undone",
			"this import was undone; upload the file again to retry")
		return
	}
	if existing.ErrorCount > 0 && !body.SkipErrors {
		writeBatchHasErrors(w, int(existing.ErrorCount))
		return
	}

	var (
		batch    sqlc.ImportBatch
		created  importCreated
		notifyID []string
		rowErr   *importRowFailedError
	)
	txErr := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		locked, err := q.LockImportBatch(r.Context(), sqlc.LockImportBatchParams{
			OrgID: p.OrgID, ID: existing.ID,
		})
		if err != nil {
			return err
		}
		if locked.Status != importPreviewed {
			return errImportNoLongerPreviewed
		}
		stored, err := q.ListImportRows(r.Context(), sqlc.ListImportRowsParams{
			OrgID: p.OrgID, BatchID: locked.ID,
		})
		if err != nil {
			return err
		}
		// The file is re-validated and re-resolved against the database as it
		// is now: a preview is a promise about a moment, and the commit has to
		// keep it or refuse whole.
		parsed := make([]importer.Row, 0, len(stored))
		rowIDs := make([]pgtype.UUID, 0, len(stored))
		for _, row := range stored {
			raw := map[string]string{}
			if err := json.Unmarshal(row.Raw, &raw); err != nil {
				return err
			}
			parsed = append(parsed, importer.Row{Line: int(row.Line), Raw: raw})
			rowIDs = append(rowIDs, row.ID)
		}
		resolved, err := s.resolveImportRows(r.Context(), q, p, locked.Kind, parsed)
		if err != nil {
			return err
		}
		for i := range resolved {
			resolved[i].rowID = rowIDs[i]
		}
		created, notifyID, err = s.applyImportRows(r.Context(), q, p, locked, resolved, body.SkipErrors)
		if err != nil {
			return err
		}
		batch, err = q.CommitImportBatch(r.Context(), sqlc.CommitImportBatchParams{
			OrgID: p.OrgID, ID: locked.ID,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionImportCommit,
			EntityType:  audit.EntityImportBatch,
			EntityID:    db.UUIDString(batch.ID),
			Before:      map[string]any{"status": importPreviewed},
			After: map[string]any{
				"status": importCommitted, "kind": batch.Kind,
				"row_count": batch.RowCount, "ok_count": batch.OkCount,
				"error_count": batch.ErrorCount, "skip_errors": body.SkipErrors,
				"created": created,
			},
		})
	})
	switch {
	case txErr == nil:
	case errors.Is(txErr, errImportNoLongerPreviewed):
		conflictCode(w, "already_committed", "batch already committed",
			"this file has already been imported")
		return
	case errors.As(txErr, &rowErr):
		writeImportRowFailed(w, rowErr)
		return
	default:
		s.serverError(w, r, "import.commit.tx", txErr)
		return
	}
	s.enqueueNotifications(r.Context(), notifyID...)

	WriteJSON(w, http.StatusOK, map[string]any{
		"batch":   s.reloadImportBatch(r.Context(), p, batch),
		"created": created,
	})
}

func writeBatchHasErrors(w http.ResponseWriter, errorCount int) {
	w.Header().Set("Content-Type", httpx.ProblemContentType)
	w.WriteHeader(http.StatusConflict)
	WriteRawJSON(w, map[string]any{
		"type":        "batch_has_errors",
		"title":       "the file still has rows with errors",
		"status":      http.StatusConflict,
		"detail":      "fix the sheet and preview again, or resend with skip_errors to import the rest",
		"error_count": errorCount,
	})
}

func writeImportRowFailed(w http.ResponseWriter, e *importRowFailedError) {
	w.Header().Set("Content-Type", httpx.ProblemContentType)
	w.WriteHeader(http.StatusConflict)
	WriteRawJSON(w, map[string]any{
		"type":   "row_failed",
		"title":  "a row that passed the preview no longer does",
		"status": http.StatusConflict,
		"detail": "nothing was imported; preview the file again to see the current state",
		"line":   e.Line,
		"column": e.Column,
		"reason": e.Reason,
	})
}

// ------------------------------------------------- POST /imports/{id}/undo --

func (s *Server) handleUndoImport(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	existing, ok := s.orgImportBatch(w, r, p)
	if !ok {
		return
	}
	switch {
	case existing.Status == importPreviewed:
		conflictCode(w, "not_committed", "batch not committed",
			"there is nothing to undo: this file was never imported")
		return
	case existing.Status == importUndone:
		conflictCode(w, "already_undone", "batch already undone",
			"this import has already been undone")
		return
	case !existing.CommittedAt.Valid ||
		time.Since(existing.CommittedAt.Time) > importUndoWindow:
		conflictCode(w, "undo_window_closed", "too late to undo",
			"an import can only be undone within 24 hours of the commit")
		return
	}

	org, err := s.q.GetOrg(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "import.undo.org", err)
		return
	}
	grace := int32(parseSettings(org.Settings).GraceDays)

	var batch sqlc.ImportBatch
	var undone importUndoCounts
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		locked, err := q.LockImportBatch(r.Context(), sqlc.LockImportBatchParams{
			OrgID: p.OrgID, ID: existing.ID,
		})
		if err != nil {
			return err
		}
		if locked.Status != importCommitted {
			return errImportNoLongerPreviewed
		}
		undone, err = s.undoImportBatch(r.Context(), q, p, locked, grace)
		if err != nil {
			return err
		}
		batch, err = q.UndoImportBatch(r.Context(), sqlc.UndoImportBatchParams{
			OrgID: p.OrgID, ID: locked.ID,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionImportUndo,
			EntityType:  audit.EntityImportBatch,
			EntityID:    db.UUIDString(batch.ID),
			Before:      map[string]any{"status": importCommitted},
			After: map[string]any{
				"status": importUndone, "kind": batch.Kind, "undone": undone,
			},
		})
	}); err != nil {
		if errors.Is(err, errImportNoLongerPreviewed) {
			conflictCode(w, "already_undone", "batch already undone",
				"this import has already been undone")
			return
		}
		s.serverError(w, r, "import.undo.tx", err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"batch":  s.reloadImportBatch(r.Context(), p, batch),
		"undone": undone,
	})
}

// --------------------------------------------------------- shared helpers --

// importRow is one line on its way through resolution and commit: what the file
// said, what is wrong with it, what it resolves to, and — after the commit —
// what it became.
type importRow struct {
	line     int
	raw      map[string]string
	errs     importer.RowErrors
	resolved map[string]any
	rowID    pgtype.UUID

	unit    *importer.UnitRow
	renter  *importer.RenterRow
	payment *importer.PaymentRow

	propertyID     pgtype.UUID
	propertyName   string
	createProperty bool
	unitID         pgtype.UUID
	unitName       string
	renterUserID   pgtype.UUID
	createRenter   bool
	locale         string
	contractID     pgtype.UUID
	periodID       pgtype.UUID
}

func (row *importRow) ok() bool { return len(row.errs) == 0 }

func (row *importRow) fail(column, reason string) *importRowFailedError {
	return &importRowFailedError{Line: row.line, Column: column, Reason: reason}
}

func countImportRows(rows []*importRow) (ok, bad int) {
	for _, row := range rows {
		if row.ok() {
			ok++
			continue
		}
		bad++
	}
	return ok, bad
}

func previewRows(rows []*importRow) []importRowResponse {
	out := make([]importRowResponse, 0, len(rows))
	for _, row := range rows {
		item := importRowResponse{Line: row.line, Raw: row.raw, Resolved: row.resolved}
		if len(row.errs) > 0 {
			item.Errors = row.errs
		}
		out = append(out, item)
	}
	return out
}

func jsonOrNil(v any) []byte {
	switch t := v.(type) {
	case importer.RowErrors:
		if len(t) == 0 {
			return nil
		}
	case map[string]any:
		if len(t) == 0 {
			return nil
		}
	case nil:
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}

func stringsOrEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// trimName keeps the base name of an uploaded file, bounded: a filename is
// display text, and the browser chose it, not us.
func trimName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(name)
	if len([]rune(name)) > importer.NameMax {
		name = string([]rune(name)[:importer.NameMax])
	}
	return name
}

// reloadImportBatch re-reads a batch after a write so the response carries the
// uploader's name, exactly as the history list does. A failed re-read is not
// worth losing the answer over: the batch itself is already durable.
func (s *Server) reloadImportBatch(
	ctx context.Context, p auth.Principal, batch sqlc.ImportBatch,
) importBatchResponse {
	now := time.Now().UTC()
	row, err := s.q.GetImportBatch(ctx, sqlc.GetImportBatchParams{OrgID: p.OrgID, ID: batch.ID})
	if err != nil {
		return toImportBatch(batch, nil, now)
	}
	return toImportBatch(batchOfGet(row), row.CreatedByName, now)
}

// --------------------------------------------------------- resolution --

// resolveImportRows runs the cell rules and then the database lookups for one
// file. It is read-only, so the preview and the commit can share it: the same
// function decides what a row means, and the commit simply refuses when the
// answer has changed.
func (s *Server) resolveImportRows(
	ctx context.Context, q *sqlc.Queries, p auth.Principal, kind string, rows []importer.Row,
) ([]*importRow, error) {
	out := make([]*importRow, 0, len(rows))
	for _, r := range rows {
		row := &importRow{line: r.Line, raw: r.Raw, errs: importer.RowErrors{}, resolved: map[string]any{}}
		for k, v := range r.Errors {
			row.errs[k] = v
		}
		out = append(out, row)
	}
	switch kind {
	case importer.KindUnits:
		return out, s.resolveUnitRows(ctx, q, p, out)
	case importer.KindRenters:
		return out, s.resolveRenterRows(ctx, q, p, out)
	case importer.KindPayments:
		return out, s.resolvePaymentRows(ctx, q, p, out)
	default:
		return out, nil
	}
}

func lowerKey(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.ToLower(strings.TrimSpace(p)))
	}
	return strings.Join(out, "\x00")
}

// orgDefaultRentDays is the `rent_period_days` a units sheet gets when the
// column is blank: the org's recommended payment period when it has one, else
// 30 days.
func (s *Server) orgDefaultRentDays(ctx context.Context, q *sqlc.Queries, orgID pgtype.UUID) int32 {
	period, err := q.GetRecommendedPaymentPeriod(ctx, orgID)
	if err == nil && period.Days > 0 {
		return period.Days
	}
	return importer.DefaultRentDays
}

// resolveUnitRows matches each row's property by name (creating it when it is
// missing is the point of the sheet) and refuses a unit name the property
// already carries — including one an earlier line of the same file claims.
func (s *Server) resolveUnitRows(
	ctx context.Context, q *sqlc.Queries, p auth.Principal, rows []*importRow,
) error {
	defaultDays := s.orgDefaultRentDays(ctx, q, p.OrgID)
	known := map[string]*sqlc.Property{}          // lower(name) → existing property
	planned := map[string]bool{}                  // lower(name) → this file creates it
	takenUnits := map[string]bool{}               // property|unit → claimed
	existingUnits := map[string]map[string]bool{} // property id → its unit names
	for _, row := range rows {
		parsed, errs := importer.ParseUnitRow(row.raw, defaultDays)
		mergeErrors(row, errs)
		row.unit = &parsed
		if !row.ok() {
			continue
		}
		row.propertyName = parsed.Property
		row.unitName = parsed.Unit

		key := lowerKey(parsed.Property)
		prop, cached := known[key]
		if !cached {
			found, err := q.FindPropertyByName(ctx, sqlc.FindPropertyByNameParams{
				OrgID: p.OrgID, Name: parsed.Property,
			})
			if err != nil {
				return err
			}
			if len(found) > 0 {
				prop = &found[0]
			}
			known[key] = prop
		}
		switch {
		case prop != nil:
			row.propertyID = prop.ID
			row.resolved["property_id"] = db.UUIDString(prop.ID)
			row.resolved["property_create"] = false
		default:
			row.createProperty = true
			row.resolved["property_create"] = !planned[key]
			planned[key] = true
		}
		row.resolved["property"] = parsed.Property
		row.resolved["unit"] = parsed.Unit
		row.resolved["rent_amount"] = parsed.RentAmount
		row.resolved["rent_period_days"] = parsed.RentPeriodDays
		row.resolved["status"] = parsed.Status

		unitKey := lowerKey(parsed.Property, parsed.Unit)
		if takenUnits[unitKey] {
			row.errs.Add("unit", "this file already creates a unit with this name in this property")
			continue
		}
		takenUnits[unitKey] = true
		if prop == nil {
			continue
		}
		// One read per property, not per line: a 5 000-row sheet is checked
		// against a set in memory.
		names, cached := existingUnits[db.UUIDString(prop.ID)]
		if !cached {
			stored, err := q.ListUnitNamesForProperty(ctx, sqlc.ListUnitNamesForPropertyParams{
				OrgID: p.OrgID, PropertyID: prop.ID,
			})
			if err != nil {
				return err
			}
			names = make(map[string]bool, len(stored))
			for _, n := range stored {
				names[n] = true
			}
			existingUnits[db.UUIDString(prop.ID)] = names
		}
		if names[strings.ToLower(strings.TrimSpace(parsed.Unit))] {
			row.errs.Add("unit", "this property already has a unit with this name")
		}
	}
	return nil
}

// resolveRenterRows finds or plans the renter account behind each phone number
// and, when the row names a unit, the tenancy the commit will draw up. Nothing
// is activated: the contract is created `pending_signature`, exactly as an
// approved application creates one (PLAN2 §16.2).
func (s *Server) resolveRenterRows(
	ctx context.Context, q *sqlc.Queries, p auth.Principal, rows []*importRow,
) error {
	seenPhones := map[string]bool{}
	claimedUnits := map[string]bool{}
	unitCache := map[string]*sqlc.GetUnitRow{}
	var periodID pgtype.UUID
	var periodResolved bool

	for _, row := range rows {
		parsed, errs := importer.ParseRenterRow(row.raw)
		mergeErrors(row, errs)
		row.renter = &parsed
		if !row.ok() {
			continue
		}
		row.locale = parsed.Locale
		row.resolved["full_name"] = parsed.FullName
		row.resolved["phone"] = parsed.Phone
		row.resolved["locale"] = parsed.Locale

		if seenPhones[parsed.Phone] {
			row.errs.Add("phone", "this file already has a row for this number")
			continue
		}
		seenPhones[parsed.Phone] = true

		phone := parsed.Phone
		user, err := q.GetUserByPhone(ctx, &phone)
		switch {
		case isNoRows(err):
			row.createRenter = true
			row.resolved["renter_create"] = true
		case err != nil:
			return err
		case user.Kind != auth.KindRenter:
			row.errs.Add("phone", "this number belongs to a staff account")
			continue
		default:
			// Known to the platform: the import attaches the existing person to
			// this org rather than making a second account for them.
			row.renterUserID = user.ID
			row.resolved["renter_create"] = false
			row.resolved["user_id"] = db.UUIDString(user.ID)
		}

		if parsed.Unit == "" {
			row.resolved["contract"] = nil
			continue
		}

		unit, ok, err := s.resolveImportUnit(ctx, q, p, row, parsed.Property, parsed.Unit, unitCache)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if claimedUnits[db.UUIDString(unit.ID)] {
			row.errs.Add("unit", "this file already gives this unit to another renter")
			continue
		}
		claimedUnits[db.UUIDString(unit.ID)] = true
		if unit.Status != statusVacant {
			row.errs.Add("unit", "only a vacant unit can be given to a renter (this one is "+unit.Status+")")
			continue
		}
		if !unit.PriceID.Valid || unit.PriceAmount <= 0 {
			row.errs.Add("unit", "this unit has no rent price yet; set one before importing renters")
			continue
		}
		if _, err := q.GetDefaultContractTemplate(ctx, p.OrgID); isNoRows(err) {
			row.errs.Add("unit", "the org has no default contract template")
			continue
		} else if err != nil {
			return err
		}
		if !periodResolved {
			id, err := s.importPaymentPeriod(ctx, q, p.OrgID)
			if err != nil {
				return err
			}
			periodID, periodResolved = id, true
		}
		if !periodID.Valid {
			row.errs.Add("unit", "the org offers no active payment period")
			continue
		}
		row.periodID = periodID
		row.unitID = unit.ID
		row.unitName = unit.Name
		row.propertyName = unit.PropertyName
		row.propertyID = unit.PropertyID
		row.resolved["unit_id"] = db.UUIDString(unit.ID)
		row.resolved["unit"] = unit.Name
		row.resolved["property"] = unit.PropertyName
		row.resolved["contract"] = contractPendingSignature
		row.resolved["term_days"] = importTermDays
	}
	return nil
}

// importPaymentPeriod picks the cadence a contract drawn up by an import uses:
// the org's recommended period, else its first active one.
func (s *Server) importPaymentPeriod(ctx context.Context, q *sqlc.Queries, orgID pgtype.UUID) (pgtype.UUID, error) {
	if period, err := q.GetRecommendedPaymentPeriod(ctx, orgID); err == nil {
		return period.ID, nil
	} else if !isNoRows(err) {
		return pgtype.UUID{}, err
	}
	active, err := q.ListActivePaymentPeriods(ctx, orgID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if len(active) == 0 {
		return pgtype.UUID{}, nil
	}
	return active[0].ID, nil
}

// resolveImportUnit finds the unit a row names. With a property given, the pair
// must match; without one, the unit name must be unique across the org —
// "A1" in two blocks is ambiguous, and an import must never guess which
// tenancy a landlord meant.
func (s *Server) resolveImportUnit(
	ctx context.Context, q *sqlc.Queries, p auth.Principal, row *importRow,
	propertyName, unitName string, cache map[string]*sqlc.GetUnitRow,
) (sqlc.GetUnitRow, bool, error) {
	var zero sqlc.GetUnitRow
	// A sheet naming the same unit on many lines asks the same question many
	// times; the answer is read once. Only hits are cached — a miss has its own
	// wording (missing property, missing unit, ambiguous name) and is rare.
	key := lowerKey(propertyName, unitName)
	if hit, ok := cache[key]; ok && hit != nil {
		return *hit, true, nil
	}
	found, ok, err := s.lookupImportUnit(ctx, q, p, row, propertyName, unitName)
	if err != nil || !ok {
		return zero, false, err
	}
	cache[key] = &found
	return found, true, nil
}

// lookupImportUnit is resolveImportUnit without the cache.
func (s *Server) lookupImportUnit(
	ctx context.Context, q *sqlc.Queries, p auth.Principal, row *importRow, propertyName, unitName string,
) (sqlc.GetUnitRow, bool, error) {
	var zero sqlc.GetUnitRow
	if propertyName != "" {
		props, err := q.FindPropertyByName(ctx, sqlc.FindPropertyByNameParams{
			OrgID: p.OrgID, Name: propertyName,
		})
		if err != nil {
			return zero, false, err
		}
		if len(props) == 0 {
			row.errs.Add("property", "no property of this org has this name")
			return zero, false, nil
		}
		units, err := q.FindUnitByNameInProperty(ctx, sqlc.FindUnitByNameInPropertyParams{
			OrgID: p.OrgID, PropertyID: props[0].ID, Name: unitName,
		})
		if err != nil {
			return zero, false, err
		}
		if len(units) == 0 {
			row.errs.Add("unit", "this property has no unit with this name")
			return zero, false, nil
		}
		full, err := q.GetUnit(ctx, sqlc.GetUnitParams{OrgID: p.OrgID, ID: units[0].ID})
		if err != nil {
			return zero, false, err
		}
		return full, true, nil
	}

	units, err := q.FindUnitByNameInOrg(ctx, sqlc.FindUnitByNameInOrgParams{
		OrgID: p.OrgID, Name: unitName,
	})
	if err != nil {
		return zero, false, err
	}
	switch len(units) {
	case 0:
		row.errs.Add("unit", "no unit of this org has this name")
		return zero, false, nil
	case 1:
		full, err := q.GetUnit(ctx, sqlc.GetUnitParams{OrgID: p.OrgID, ID: units[0].ID})
		if err != nil {
			return zero, false, err
		}
		return full, true, nil
	default:
		row.errs.Add("unit", "more than one property has a unit with this name; "+
			"rename one or add the property column")
		return zero, false, nil
	}
}

// resolvePaymentRows resolves each row to the renter's contract on the named
// unit and then *simulates* the allocation, in the order the commit will apply
// it (paid_at, then line). A row the contract cannot absorb is an error in the
// preview, where the landlord can still fix the sheet or the contract dates.
func (s *Server) resolvePaymentRows(
	ctx context.Context, q *sqlc.Queries, p auth.Principal, rows []*importRow,
) error {
	now := time.Now().UTC()
	users := map[string]*sqlc.User{}
	unitCache := map[string]*sqlc.GetUnitRow{}
	for _, row := range rows {
		parsed, errs := importer.ParsePaymentRow(row.raw, now)
		mergeErrors(row, errs)
		row.payment = &parsed
		if !row.ok() {
			continue
		}
		row.resolved["amount"] = parsed.Amount
		row.resolved["method"] = parsed.Method
		row.resolved["paid_at"] = parsed.PaidAt.Format(time.RFC3339)

		user, cached := users[parsed.RenterPhone]
		if !cached {
			phone := parsed.RenterPhone
			found, err := q.GetUserByPhone(ctx, &phone)
			switch {
			case isNoRows(err):
			case err != nil:
				return err
			default:
				user = &found
			}
			users[parsed.RenterPhone] = user
		}
		if user == nil {
			row.errs.Add("renter_phone", "no renter of this org has this number")
			continue
		}
		row.renterUserID = user.ID
		row.resolved["renter_name"] = user.FullName

		unit, ok, err := s.resolveImportUnit(ctx, q, p, row, "", parsed.Unit, unitCache)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		row.unitID = unit.ID
		row.unitName = unit.Name
		row.propertyName = unit.PropertyName
		row.resolved["unit"] = unit.Name
		row.resolved["property"] = unit.PropertyName

		contracts, err := q.FindContractForUnitAndRenter(ctx, sqlc.FindContractForUnitAndRenterParams{
			OrgID: p.OrgID, UnitID: unit.ID, RenterUserID: user.ID,
		})
		if err != nil {
			return err
		}
		if len(contracts) == 0 {
			row.errs.Add("unit", "this renter has no contract on this unit")
			continue
		}
		row.contractID = contracts[0].ID
		row.resolved["contract_id"] = db.UUIDString(contracts[0].ID)
		row.resolved["contract_status"] = contracts[0].Status
	}
	return s.simulateImportPayments(ctx, q, p, rows)
}

// simulateImportPayments walks the ok rows in the commit's own order against an
// in-memory copy of each contract's schedules. Nothing is written: the copy is
// what tells a row it would overshoot the contract's balance.
func (s *Server) simulateImportPayments(
	ctx context.Context, q *sqlc.Queries, p auth.Principal, rows []*importRow,
) error {
	order := paymentRowOrder(rows)
	books := map[string][]payment.Schedule{}
	for _, row := range order {
		cid := db.UUIDString(row.contractID)
		book, ok := books[cid]
		if !ok {
			stored, err := q.ListSchedulesForContract(ctx, sqlc.ListSchedulesForContractParams{
				OrgID: p.OrgID, ContractID: row.contractID,
			})
			if err != nil {
				return err
			}
			book = allocSchedules(stored)
			books[cid] = book
		}
		applied, err := allocateAgainst(book, row.payment.Amount)
		if err != nil {
			row.errs.Add("amount", importErrExceedsBalance)
			continue
		}
		for _, a := range applied {
			for i := range book {
				if book[i].ID == a.ScheduleID {
					book[i].PaidAmount = a.NewPaid
					book[i].Status = a.NewStatus
				}
			}
		}
		row.resolved["applies_to"] = appliedAudit(applied)
	}
	return nil
}

// importErrExceedsBalance is the row error PLAN2 names for a payment bigger
// than everything the contract still owes.
const importErrExceedsBalance = "exceeds_contract_balance"

// paymentRowOrder is the order money is applied in: by the date it was
// received, and by the sheet's own order within a day. Allocation walks
// forward through a contract's schedules, so the sequence decides which
// schedule each payment lands on.
func paymentRowOrder(rows []*importRow) []*importRow {
	out := make([]*importRow, 0, len(rows))
	for _, row := range rows {
		if row.ok() && row.payment != nil && row.contractID.Valid {
			out = append(out, row)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].payment.PaidAt.Equal(out[j].payment.PaidAt) {
			return out[i].payment.PaidAt.Before(out[j].payment.PaidAt)
		}
		return out[i].line < out[j].line
	})
	return out
}

// allocateAgainst runs the existing allocator against a contract's schedules
// with the rollover an import always takes: historical money settles whatever
// it reaches, without a confirm prompt nobody is there to answer.
func allocateAgainst(book []payment.Schedule, amount int64) ([]payment.Alloc, error) {
	target := payment.EarliestUnpaid(book)
	if target < 0 {
		return nil, payment.ErrExceedsContractBalance
	}
	return payment.Allocate(amount, book[target], book[target+1:], true)
}

func allocSchedules(rows []sqlc.PaymentSchedule) []payment.Schedule {
	out := make([]payment.Schedule, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAllocSchedule(row))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DueDate < out[j].DueDate })
	return out
}

func mergeErrors(row *importRow, errs importer.RowErrors) {
	for k, v := range errs {
		row.errs.Add(k, v)
	}
}

// ------------------------------------------------------------ the commit --

// applyImportRows writes every ok row through the ordinary service paths, in
// the caller's transaction. A row that no longer resolves aborts the whole
// thing: half a spreadsheet is the one outcome this feature exists to prevent.
func (s *Server) applyImportRows(
	ctx context.Context, q *sqlc.Queries, p auth.Principal,
	batch sqlc.ImportBatch, rows []*importRow, skipErrors bool,
) (importCreated, []string, error) {
	var created importCreated
	var notifyIDs []string

	for _, row := range rows {
		if !row.ok() {
			if skipErrors {
				continue
			}
			return created, nil, row.fail(firstErrorColumn(row.errs), firstErrorReason(row.errs))
		}
	}

	switch batch.Kind {
	case importer.KindUnits:
		props := map[string]pgtype.UUID{}
		for _, row := range rows {
			if !row.ok() {
				continue
			}
			propertyID := row.propertyID
			if row.createProperty {
				key := lowerKey(row.propertyName)
				if id, ok := props[key]; ok {
					propertyID = id
				} else {
					prop, err := s.createPropertyIn(ctx, q, p, row.propertyName)
					if err != nil {
						return created, nil, err
					}
					propertyID = prop.ID
					props[key] = prop.ID
					created.Properties++
				}
			}
			unit, err := s.createUnitIn(ctx, q, p, propertyID, row.unitName,
				&unitPriceInput{Amount: row.unit.RentAmount, PeriodDays: row.unit.RentPeriodDays}, nil)
			if err != nil {
				return created, nil, err
			}
			created.Units++
			unitID := db.MustUUID(unit.ID)
			if row.unit.Status == importer.StatusUnlisted {
				status := importer.StatusUnlisted
				if _, err := q.UpdateUnit(ctx, sqlc.UpdateUnitParams{
					OrgID: p.OrgID, ID: unitID, Status: &status,
				}); err != nil {
					return created, nil, err
				}
			}
			row.resolved["unit_id"] = unit.ID
			row.resolved["property_id"] = db.UUIDString(propertyID)
			row.resolved["property_created"] = row.createProperty
			if err := s.stampImportRow(ctx, q, p, row, audit.EntityUnit, unitID); err != nil {
				return created, nil, err
			}
		}

	case importer.KindRenters:
		for _, row := range rows {
			if !row.ok() {
				continue
			}
			userID := row.renterUserID
			if row.createRenter {
				user, err := s.createImportedRenter(ctx, q, p, row)
				if err != nil {
					return created, nil, err
				}
				userID = user.ID
				created.Renters++
			}
			row.resolved["user_id"] = db.UUIDString(userID)
			row.resolved["renter_created"] = row.createRenter

			if row.unitID.Valid {
				linkID, contractID, notifyID, err := s.createImportedTenancy(ctx, q, p, row, userID)
				if err != nil {
					return created, nil, err
				}
				created.Contracts++
				row.resolved["link_request_id"] = db.UUIDString(linkID)
				row.resolved["contract_id"] = db.UUIDString(contractID)
				if notifyID != "" {
					notifyIDs = append(notifyIDs, notifyID)
				}
			}
			if err := s.stampImportRow(ctx, q, p, row, audit.EntityUser, userID); err != nil {
				return created, nil, err
			}
		}

	case importer.KindPayments:
		books := map[string][]payment.Schedule{}
		for _, row := range paymentRowOrder(rows) {
			paymentID, err := s.applyImportedPayment(ctx, q, p, batch, row, books)
			if err != nil {
				var failed *importRowFailedError
				if errors.As(err, &failed) {
					return created, nil, err
				}
				return created, nil, err
			}
			created.Payments++
			row.resolved["payment_id"] = db.UUIDString(paymentID)
			if err := s.stampImportRow(ctx, q, p, row, audit.EntityPayment, paymentID); err != nil {
				return created, nil, err
			}
		}
	}
	return created, notifyIDs, nil
}

func firstErrorColumn(errs importer.RowErrors) string {
	keys := make([]string, 0, len(errs))
	for k := range errs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return importer.RowErrorKey
	}
	return keys[0]
}

func firstErrorReason(errs importer.RowErrors) string {
	return errs[firstErrorColumn(errs)]
}

// stampImportRow records what a row became, so the undo knows what to take back.
func (s *Server) stampImportRow(
	ctx context.Context, q *sqlc.Queries, p auth.Principal,
	row *importRow, entityType string, entityID pgtype.UUID,
) error {
	kind := entityType
	return q.MarkImportRowEntity(ctx, sqlc.MarkImportRowEntityParams{
		EntityType: &kind, EntityID: entityID, Resolved: jsonOrNil(row.resolved),
		OrgID: p.OrgID, ID: row.rowID,
	})
}

// createPropertyIn writes one property through the same statement and audit row
// as POST /properties.
func (s *Server) createPropertyIn(
	ctx context.Context, q *sqlc.Queries, p auth.Principal, name string,
) (sqlc.Property, error) {
	created, err := q.CreateProperty(ctx, sqlc.CreatePropertyParams{OrgID: p.OrgID, Name: name})
	if err != nil {
		return sqlc.Property{}, err
	}
	if err := audit.Record(ctx, q, audit.Entry{
		OrgID:       p.OrgIDString(),
		ActorUserID: p.UserIDString(),
		Action:      audit.ActionPropertyCreate,
		EntityType:  audit.EntityProperty,
		EntityID:    db.UUIDString(created.ID),
		After:       map[string]any{"name": created.Name, "source": "import"},
	}); err != nil {
		return sqlc.Property{}, err
	}
	return created, nil
}

// createImportedRenter pre-registers a renter by phone: an account with no PIN,
// which the person claims with the ordinary OTP sign-in. The renter profile row
// is written the same way registration writes it, so the directory and the KYC
// screens see a normal renter.
func (s *Server) createImportedRenter(
	ctx context.Context, q *sqlc.Queries, p auth.Principal, row *importRow,
) (sqlc.User, error) {
	phone := row.renter.Phone
	locale := row.locale
	user, err := q.CreateUser(ctx, sqlc.CreateUserParams{
		Kind: auth.KindRenter, Phone: &phone, FullName: row.renter.FullName, Locale: &locale,
	})
	if err != nil {
		return sqlc.User{}, err
	}
	if _, err := q.UpsertRenterProfile(ctx, sqlc.UpsertRenterProfileParams{
		UserID: user.ID, FullName: row.renter.FullName, EncKey: s.cfg.NidaEncKey,
	}); err != nil {
		return sqlc.User{}, err
	}
	if err := audit.Record(ctx, q, audit.Entry{
		OrgID:       p.OrgIDString(),
		ActorUserID: p.UserIDString(),
		Action:      audit.ActionRegisterRenter,
		EntityType:  audit.EntityUser,
		EntityID:    db.UUIDString(user.ID),
		After: map[string]any{
			"phone": phone, "full_name": row.renter.FullName,
			"locale": user.Locale, "source": "import",
		},
	}); err != nil {
		return sqlc.User{}, err
	}
	return user, nil
}

// createImportedTenancy is the renters sheet's second half: an approved link
// request and the contract it creates, through `onLinkApproved` — the same hook
// the landlord's Approve button runs (DECISIONS: approval creates the
// contract). The contract is `pending_signature`; an import never activates one.
func (s *Server) createImportedTenancy(
	ctx context.Context, q *sqlc.Queries, p auth.Principal, row *importRow, userID pgtype.UUID,
) (pgtype.UUID, pgtype.UUID, string, error) {
	var zero pgtype.UUID
	start := time.Now().UTC().Truncate(24 * time.Hour)
	term := int32(importTermDays)
	end := start.AddDate(0, 0, importTermDays)

	link, err := q.CreateLinkRequest(ctx, sqlc.CreateLinkRequestParams{
		OrgID: p.OrgID, UnitID: row.unitID, RenterUserID: userID, Status: linkApproved,
		PaymentPeriodID: row.periodID, TermDays: &term,
		StartDate: pgtype.Date{Time: start, Valid: true},
		EndDate:   pgtype.Date{Time: end, Valid: true},
		DecidedAt: db.TS(time.Now().UTC()), DecidedByUserID: p.UserID,
	})
	if err != nil {
		return zero, zero, "", err
	}
	if err := audit.Record(ctx, q, audit.Entry{
		OrgID:       p.OrgIDString(),
		ActorUserID: p.UserIDString(),
		Action:      audit.ActionLinkApprove,
		EntityType:  audit.EntityLinkRequest,
		EntityID:    db.UUIDString(link.ID),
		After: map[string]any{
			"status": linkApproved, "unit_id": db.UUIDString(row.unitID),
			"start_date": start.Format(dateLayout), "term_days": term, "source": "import",
		},
	}); err != nil {
		return zero, zero, "", err
	}

	contract, notifyID, made, err := s.onLinkApproved(ctx, q, link, p.UserIDString())
	if err != nil {
		var createErr error = err
		if code := importCreateFailure(createErr); code != "" {
			return zero, zero, "", row.fail("unit", code)
		}
		return zero, zero, "", err
	}
	if !made {
		return link.ID, zero, "", row.fail("unit", "the contract could not be created")
	}
	return link.ID, contract.ID, notifyID, nil
}

// importCreateFailure names the contract-creation refusals in words a landlord
// can act on; anything else is a real error and is reported as one.
func importCreateFailure(err error) string {
	switch {
	case errors.Is(err, errUnitOccupied):
		return "the unit was let while the preview was open"
	case errors.Is(err, errUnitUnavailable):
		return "the unit is not available"
	case errors.Is(err, errUnitNotPriced):
		return "the unit has no rent price"
	case errors.Is(err, errTemplateNotFound):
		return "the org has no default contract template"
	case errors.Is(err, errPeriodNotOffered):
		return "the payment period is not offered on this unit"
	case errors.Is(err, errContractExists):
		return "this unit already has a live contract"
	case errors.Is(err, errRenterUnknown):
		return "the renter is not known to this org"
	default:
		return ""
	}
}

// applyImportedPayment records one historical payment: the existing allocator
// decides where the money lands, the payment carries the batch it arrived on,
// and no thank-you SMS is sent — a landlord filling in last year's ledger is
// not telling forty renters they have just paid.
func (s *Server) applyImportedPayment(
	ctx context.Context, q *sqlc.Queries, p auth.Principal, batch sqlc.ImportBatch,
	row *importRow, books map[string][]payment.Schedule,
) (pgtype.UUID, error) {
	var zero pgtype.UUID
	cid := db.UUIDString(row.contractID)
	book, ok := books[cid]
	if !ok {
		locked, err := q.LockSchedulesForContract(ctx, sqlc.LockSchedulesForContractParams{
			OrgID: p.OrgID, ContractID: row.contractID,
		})
		if err != nil {
			return zero, err
		}
		book = allocSchedules(locked)
		books[cid] = book
	}
	applied, err := allocateAgainst(book, row.payment.Amount)
	if err != nil {
		return zero, row.fail("amount", importErrExceedsBalance)
	}

	target := payment.EarliestUnpaid(book)
	pay, err := q.CreatePayment(ctx, sqlc.CreatePaymentParams{
		OrgID: p.OrgID, ContractID: row.contractID,
		ScheduleID: db.MustUUID(book[target].ID), Amount: row.payment.Amount,
		Method: row.payment.Method, Reference: db.Str(row.payment.Reference),
		PaidAt: db.TS(row.payment.PaidAt), RecordedByUserID: p.UserID,
		Note: db.Str(row.payment.Note), ImportBatchID: batch.ID,
	})
	if err != nil {
		return zero, err
	}
	for _, a := range applied {
		schedID := db.MustUUID(a.ScheduleID)
		if _, err := q.CreatePaymentAllocation(ctx, sqlc.CreatePaymentAllocationParams{
			OrgID: p.OrgID, PaymentID: pay.ID, ScheduleID: schedID, Amount: a.Amount,
		}); err != nil {
			return zero, err
		}
		if _, err := q.ApplyPaymentToSchedule(ctx, sqlc.ApplyPaymentToScheduleParams{
			PaidAmount: a.NewPaid, OrgID: p.OrgID, ID: schedID,
		}); err != nil {
			return zero, err
		}
		for i := range book {
			if book[i].ID == a.ScheduleID {
				book[i].PaidAmount = a.NewPaid
				book[i].Status = a.NewStatus
			}
		}
	}
	books[cid] = book

	if err := audit.Record(ctx, q, audit.Entry{
		OrgID:       p.OrgIDString(),
		ActorUserID: p.UserIDString(),
		Action:      audit.ActionPaymentRecord,
		EntityType:  audit.EntityPayment,
		EntityID:    db.UUIDString(pay.ID),
		After: map[string]any{
			"contract_id": cid, "amount": row.payment.Amount, "method": row.payment.Method,
			"paid_at": row.payment.PaidAt.Format(time.RFC3339), "applied": appliedAudit(applied),
			"import_batch_id": db.UUIDString(batch.ID), "source": "import",
		},
	}); err != nil {
		return zero, err
	}
	return pay.ID, nil
}

// -------------------------------------------------------------- the undo --

// undoImportBatch takes a committed batch back off, in the reverse order it was
// written: money first, then the tenancies, then the rows themselves.
//
// "Untouched" is deliberately a simple test, and the same one everywhere:
// nothing else may reference the row. A unit with any contract stays, a
// property with any live unit stays, a contract with any payment stays, and a
// renter account stays the moment it holds a contract in this org or has ever
// been signed into. Anything kept is reported in the counts as not taken back.
func (s *Server) undoImportBatch(
	ctx context.Context, q *sqlc.Queries, p auth.Principal, batch sqlc.ImportBatch, graceDays int32,
) (importUndoCounts, error) {
	var out importUndoCounts

	// 1. Money. The reversal is the existing path: the payment stays, marked
	//    reversed with its reason, and every schedule it touched is credited
	//    back and recomputed (FLOWS 7.4).
	payments, err := q.ListImportedPayments(ctx, sqlc.ListImportedPaymentsParams{
		OrgID: p.OrgID, ImportBatchID: batch.ID,
	})
	if err != nil {
		return out, err
	}
	const undoReason = "import undone"
	for _, pay := range payments {
		allocations, err := q.ListAllocationsForPayments(ctx, sqlc.ListAllocationsForPaymentsParams{
			OrgID: p.OrgID, PaymentIds: []pgtype.UUID{pay.ID},
		})
		if err != nil {
			return out, err
		}
		reason := undoReason
		if _, err := q.ReversePayment(ctx, sqlc.ReversePaymentParams{
			ReversalReason: &reason, ReversedByUserID: p.UserID, OrgID: p.OrgID, ID: pay.ID,
		}); err != nil {
			if isNoRows(err) {
				continue // reversed by hand in the meantime
			}
			return out, err
		}
		for _, a := range allocations {
			if _, err := q.UnapplyPaymentFromSchedule(ctx, sqlc.UnapplyPaymentFromScheduleParams{
				Delta: a.Amount, GraceDays: graceDays, OrgID: p.OrgID, ID: a.ScheduleID,
			}); err != nil {
				return out, err
			}
		}
		if err := audit.Record(ctx, q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionPaymentReverse,
			EntityType:  audit.EntityPayment,
			EntityID:    db.UUIDString(pay.ID),
			Before:      map[string]any{"status": "recorded", "amount": pay.Amount},
			After: map[string]any{
				"status": paymentReversed, "reason": undoReason,
				"import_batch_id": db.UUIDString(batch.ID),
			},
		}); err != nil {
			return out, err
		}
		out.Payments++
	}

	rows, err := q.ListImportRows(ctx, sqlc.ListImportRowsParams{OrgID: p.OrgID, BatchID: batch.ID})
	if err != nil {
		return out, err
	}

	// 2. Tenancies drawn up by a renters import, then the accounts behind them.
	for _, row := range rows {
		res := resolvedOf(row)
		if contractID := uuidFrom(res["contract_id"]); contractID.Valid {
			used, err := q.CountPaymentsForContract(ctx, sqlc.CountPaymentsForContractParams{
				OrgID: p.OrgID, ContractID: contractID,
			})
			if err != nil {
				return out, err
			}
			if used == 0 {
				if _, err := q.WithdrawImportedContract(ctx, sqlc.WithdrawImportedContractParams{
					OrgID: p.OrgID, ID: contractID,
				}); err == nil {
					out.Contracts++
				} else if !isNoRows(err) {
					return out, err
				}
			}
		}
		if linkID := uuidFrom(res["link_request_id"]); linkID.Valid {
			if err := q.WithdrawImportedLinkRequest(ctx, sqlc.WithdrawImportedLinkRequestParams{
				OrgID: p.OrgID, ID: linkID,
			}); err != nil {
				return out, err
			}
		}
	}

	// 3. Units, and the properties the import had to make for them.
	properties := map[string]pgtype.UUID{}
	for _, row := range rows {
		res := resolvedOf(row)
		if row.EntityType != nil && *row.EntityType == audit.EntityUnit && row.EntityID.Valid {
			used, err := q.CountContractsForUnit(ctx, sqlc.CountContractsForUnitParams{
				OrgID: p.OrgID, UnitID: row.EntityID,
			})
			if err != nil {
				return out, err
			}
			if used == 0 {
				if _, err := q.SoftDeleteUnit(ctx, sqlc.SoftDeleteUnitParams{
					OrgID: p.OrgID, ID: row.EntityID,
				}); err == nil {
					out.Units++
				} else if !isNoRows(err) {
					return out, err
				}
			}
		}
		if created, _ := res["property_created"].(bool); created {
			if id := uuidFrom(res["property_id"]); id.Valid {
				properties[db.UUIDString(id)] = id
			}
		}
	}
	for _, id := range properties {
		live, err := q.CountLiveUnitsForProperty(ctx, sqlc.CountLiveUnitsForPropertyParams{
			OrgID: p.OrgID, PropertyID: id,
		})
		if err != nil {
			return out, err
		}
		if live > 0 {
			continue
		}
		if _, err := q.SoftDeleteProperty(ctx, sqlc.SoftDeletePropertyParams{
			OrgID: p.OrgID, ID: id,
		}); err == nil {
			out.Properties++
		} else if !isNoRows(err) {
			return out, err
		}
	}

	// 4. Renter accounts the import itself created, and only those that never
	//    became a login and hold nothing in this org.
	for _, row := range rows {
		res := resolvedOf(row)
		if created, _ := res["renter_created"].(bool); !created {
			continue
		}
		userID := uuidFrom(res["user_id"])
		if !userID.Valid {
			continue
		}
		contracts, err := q.CountContractsForRenter(ctx, sqlc.CountContractsForRenterParams{
			OrgID: p.OrgID, RenterUserID: userID,
		})
		if err != nil {
			return out, err
		}
		if contracts > 0 {
			continue
		}
		links, err := q.ListLinkRequestsForRenterInOrg(ctx, sqlc.ListLinkRequestsForRenterInOrgParams{
			OrgID: p.OrgID, RenterUserID: userID,
		})
		if err != nil {
			return out, err
		}
		if len(links) > 0 {
			continue
		}
		if err := q.SoftDeleteImportedRenter(ctx, userID); err != nil {
			return out, err
		}
		out.Renters++
	}
	return out, nil
}

func resolvedOf(row sqlc.ImportRow) map[string]any {
	out := map[string]any{}
	if len(row.Resolved) > 0 {
		_ = json.Unmarshal(row.Resolved, &out)
	}
	return out
}

func uuidFrom(v any) pgtype.UUID {
	s, ok := v.(string)
	if !ok || s == "" {
		return pgtype.UUID{}
	}
	return db.MustUUID(s)
}
