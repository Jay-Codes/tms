package httpserver

import (
	"encoding/json"
	"time"

	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/importer"
)

// Phase 16 §16.2 — the CSV import of previous records.
//
// The shapes here are what the Settings → Import data screen renders: a batch
// (one uploaded file and its lifecycle) and a row (one line of that file, its
// errors, and what it will become or became).

// Batch statuses.
const (
	importPreviewed = "previewed"
	importCommitted = "committed"
	importUndone    = "undone"
)

// Import bounds (PLAN2 §16.2).
const (
	// importPreviewLimit is the ceiling on uploads: parsing a 5 000-row file is
	// real work, and a preview is the one import step anyone can repeat freely.
	importPreviewLimit  = 20
	importPreviewWindow = time.Hour
	// importUndoWindow is how long a committed batch may be taken back. After
	// it, the records are the landlord's own history and are corrected through
	// the ordinary paths (reverse a payment, terminate a contract).
	importUndoWindow = 24 * time.Hour
	// importTermDays is the term a contract drawn up by a renters import
	// carries. The sheet has no column for it — the landlord adjusts the
	// contract before it is signed, or issues a new one — so a year is the
	// stated default rather than a guess made per row.
	importTermDays = 365
	// importMultipartMemory is how much of an upload is buffered in memory
	// before the rest spills to a temp file; the whole ceiling is MaxBytes.
	importMultipartMemory = 1 << 20
)

// importActor names the landlord who uploaded a batch.
type importActor struct {
	UserID string `json:"user_id,omitempty"`
	Name   string `json:"name"`
}

// importBatchResponse is the `batch` shape every import endpoint returns.
type importBatchResponse struct {
	ID          string       `json:"id"`
	Kind        string       `json:"kind"`
	Filename    string       `json:"filename"`
	RowCount    int          `json:"row_count"`
	OkCount     int          `json:"ok_count"`
	ErrorCount  int          `json:"error_count"`
	Status      string       `json:"status"`
	CreatedBy   *importActor `json:"created_by"`
	CommittedAt *time.Time   `json:"committed_at"`
	UndoneAt    *time.Time   `json:"undone_at"`
	// CanUndo is `status == committed` and still inside the 24 h window, so the
	// history list can show or hide the button without doing date arithmetic.
	CanUndo   bool      `json:"can_undo"`
	CreatedAt time.Time `json:"created_at"`
}

// importRowResponse is one line of the preview table.
type importRowResponse struct {
	Line int               `json:"line"`
	Raw  map[string]string `json:"raw"`
	// Errors is null for a row that is ready to commit, and otherwise maps a
	// column name to the reason it was refused. The key `_row` carries a
	// problem with the whole line.
	Errors map[string]string `json:"errors"`
	// Resolved is what the row will hit or create: the names and ids the
	// landlord recognises, so the preview reads as English rather than UUIDs.
	Resolved   map[string]any `json:"resolved"`
	EntityType *string        `json:"entity_type"`
	EntityID   *string        `json:"entity_id"`
}

// toImportBatch maps a stored batch, with `now` deciding the undo window.
func toImportBatch(b sqlc.ImportBatch, createdByName *string, now time.Time) importBatchResponse {
	out := importBatchResponse{
		ID:         db.UUIDString(b.ID),
		Kind:       b.Kind,
		Filename:   b.Filename,
		RowCount:   int(b.RowCount),
		OkCount:    int(b.OkCount),
		ErrorCount: int(b.ErrorCount),
		Status:     b.Status,
		CreatedAt:  b.CreatedAt.Time,
	}
	if b.CommittedAt.Valid {
		t := b.CommittedAt.Time
		out.CommittedAt = &t
	}
	if b.UndoneAt.Valid {
		t := b.UndoneAt.Time
		out.UndoneAt = &t
	}
	out.CanUndo = b.Status == importCommitted && b.CommittedAt.Valid &&
		now.Sub(b.CommittedAt.Time) <= importUndoWindow
	if createdByName != nil {
		out.CreatedBy = &importActor{UserID: db.UUIDString(b.CreatedByUserID), Name: *createdByName}
	}
	return out
}

// batchOfGet / batchOfList narrow the joined read rows to the table row.
func batchOfGet(r sqlc.GetImportBatchRow) sqlc.ImportBatch {
	return sqlc.ImportBatch{
		ID: r.ID, OrgID: r.OrgID, Kind: r.Kind, Filename: r.Filename,
		RowCount: r.RowCount, OkCount: r.OkCount, ErrorCount: r.ErrorCount,
		Status: r.Status, CreatedByUserID: r.CreatedByUserID,
		CommittedAt: r.CommittedAt, UndoneAt: r.UndoneAt,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, DeletedAt: r.DeletedAt,
	}
}

func batchOfList(r sqlc.ListImportBatchesRow) sqlc.ImportBatch {
	return sqlc.ImportBatch(batchOfGet(sqlc.GetImportBatchRow(r)))
}

// toImportRow maps a stored row back into the preview shape.
func toImportRow(r sqlc.ImportRow) importRowResponse {
	out := importRowResponse{Line: int(r.Line), Raw: map[string]string{}}
	_ = json.Unmarshal(r.Raw, &out.Raw)
	if len(r.Errors) > 0 {
		errs := map[string]string{}
		if err := json.Unmarshal(r.Errors, &errs); err == nil && len(errs) > 0 {
			out.Errors = errs
		}
	}
	if len(r.Resolved) > 0 {
		res := map[string]any{}
		if err := json.Unmarshal(r.Resolved, &res); err == nil && len(res) > 0 {
			out.Resolved = res
		}
	}
	out.EntityType = r.EntityType
	if r.EntityID.Valid {
		id := db.UUIDString(r.EntityID)
		out.EntityID = &id
	}
	return out
}

// importCreated counts what a commit wrote, by entity. Every key is always
// present so the screen can render "0 renters" without a nil check.
type importCreated struct {
	Units      int `json:"units"`
	Properties int `json:"properties"`
	Renters    int `json:"renters"`
	Contracts  int `json:"contracts"`
	Payments   int `json:"payments"`
}

// importUndoCounts says what an undo took back.
type importUndoCounts struct {
	Payments   int `json:"payments"`
	Contracts  int `json:"contracts"`
	Units      int `json:"units"`
	Properties int `json:"properties"`
	Renters    int `json:"renters"`
}

// importTemplateColumn is one row of the column reference the screen prints
// beside the download button.
type importTemplateColumn struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Example  string `json:"example"`
	Help     string `json:"help"`
}

func importColumns(kind string) []importTemplateColumn {
	cols := importer.Columns(kind)
	out := make([]importTemplateColumn, 0, len(cols))
	for _, c := range cols {
		out = append(out, importTemplateColumn{
			Name: c.Name, Required: c.Required, Example: c.Example, Help: c.Help,
		})
	}
	return out
}
