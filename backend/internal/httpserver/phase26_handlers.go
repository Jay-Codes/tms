// Phase 26 — a backfill is reversible as a whole, and can arrive by CSV.
//
// Phase 20 made a backfill one decision (one audit row, one SMS) but left it
// undoable only one payment at a time, and a *waived* backfill not at all. The
// decision is now a row of its own (`backfill_batches`): the payments a `paid`
// batch wrote and the periods a `waived` batch closed both name it, and one
// call takes the lot back — the money through the ordinary reversal path, the
// waived periods reopened with their status recomputed.
//
// The one refusal is the honest one: once the landlord has recorded other money
// against those same months, undoing the batch would pull the floor from under
// it (409 `touched_since`). There is no time window beyond that.
package httpserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"
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

// backfillUndoReasonMax bounds the reason, which lands in every reversed
// payment's `reversal_reason` too, so it keeps the reversal's own bound.
const backfillUndoReasonMax = reversalReasonMax

// importBackfillUndoReason is what an import undo writes on the batches it
// takes back.
const importBackfillUndoReason = "import undone"

var (
	errBackfillUndone   = errors.New("backfill: already undone")
	errBackfillTouched  = errors.New("backfill: periods touched since")
	errBackfillRefunded = errors.New("backfill: a payment was partly refunded")
)

func notFoundBackfill(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such backfill")
}

// backfillResponse is one line of the contract page's "Backfills" list.
type backfillResponse struct {
	ID            string         `json:"id"`
	ContractID    string         `json:"contract_id"`
	Mode          string         `json:"mode"`
	Until         string         `json:"until"`
	Periods       int32          `json:"periods"`
	Amount        int64          `json:"amount"`
	ImportBatchID *string        `json:"import_batch_id"`
	CreatedBy     *backfillActor `json:"created_by"`
	CreatedAt     time.Time      `json:"created_at"`
	UndoneAt      *time.Time     `json:"undone_at"`
	UndoneBy      *backfillActor `json:"undone_by"`
	UndoReason    *string        `json:"undo_reason"`
	Touched       bool           `json:"touched"`
	CanUndo       bool           `json:"can_undo"`
}

// backfillActor names who did something, the way import batches do.
type backfillActor struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

func backfillActorOf(id pgtype.UUID, name *string) *backfillActor {
	if !id.Valid {
		return nil
	}
	return &backfillActor{UserID: db.UUIDString(id), Name: db.StrVal(name)}
}

func toBackfill(b sqlc.GetBackfillBatchRow, touched bool) backfillResponse {
	out := backfillResponse{
		ID: db.UUIDString(b.ID), ContractID: db.UUIDString(b.ContractID),
		Mode: b.Mode, Until: b.Until.Time.Format(dateLayout),
		Periods: b.Periods, Amount: b.Amount,
		ImportBatchID: optUUIDString(b.ImportBatchID),
		CreatedBy:     backfillActorOf(b.CreatedByUserID, b.CreatedByName),
		CreatedAt:     b.CreatedAt.Time,
		UndoneBy:      backfillActorOf(b.UndoneByUserID, b.UndoneByName),
		UndoReason:    b.UndoReason,
		Touched:       touched && !b.UndoneAt.Valid,
	}
	if b.UndoneAt.Valid {
		t := b.UndoneAt.Time
		out.UndoneAt = &t
	}
	out.CanUndo = !b.UndoneAt.Valid && !out.Touched
	return out
}

// ------------------------------------------- GET /contracts/{id}/backfills --

func (s *Server) handleListBackfills(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	contract, ok := s.loadContract(w, r)
	if !ok {
		return
	}
	rows, err := s.q.ListBackfillBatchesForContract(r.Context(), sqlc.ListBackfillBatchesForContractParams{
		OrgID: contract.OrgID, ContractID: contract.ID,
	})
	if err != nil {
		s.serverError(w, r, "backfill.list", err)
		return
	}
	items := make([]backfillResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, toBackfill(sqlc.GetBackfillBatchRow{
			ID: row.ID, OrgID: row.OrgID, ContractID: row.ContractID, Mode: row.Mode,
			Until: row.Until, Periods: row.Periods, Amount: row.Amount,
			ImportBatchID: row.ImportBatchID, CreatedByUserID: row.CreatedByUserID,
			CreatedAt: row.CreatedAt, UndoneAt: row.UndoneAt, UndoneByUserID: row.UndoneByUserID,
			UndoReason: row.UndoReason, CreatedByName: row.CreatedByName, UndoneByName: row.UndoneByName,
		}, row.Touched))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ------------------------------------------------ POST /backfills/{id}/undo --

// backfillUndoOutcome says what an undo took back.
type backfillUndoOutcome struct {
	PaymentsReversed int `json:"payments_reversed"`
	PeriodsReopened  int `json:"periods_reopened"`
}

func (s *Server) handleUndoBackfill(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundBackfill(w)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	reason := f.MaxLen("reason", f.Required("reason", body.Reason), backfillUndoReasonMax)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	existing, err := s.q.GetBackfillBatch(r.Context(), sqlc.GetBackfillBatchParams{OrgID: p.OrgID, ID: id})
	if isNoRows(err) {
		notFoundBackfill(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "backfill.undo.get", err)
		return
	}
	if existing.UndoneAt.Valid {
		writeBackfillUndoRefusal(w, errBackfillUndone)
		return
	}
	org, err := s.q.GetOrg(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "backfill.undo.org", err)
		return
	}
	grace := int32(parseSettings(org.Settings).GraceDays)

	var out backfillUndoOutcome
	err = s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		out, err = s.undoBackfillTx(r.Context(), q, p, id, reason, grace)
		return err
	})
	switch {
	case err == nil:
	case errors.Is(err, errBackfillUndone), errors.Is(err, errBackfillTouched),
		errors.Is(err, errBackfillRefunded):
		writeBackfillUndoRefusal(w, err)
		return
	default:
		s.serverError(w, r, "backfill.undo.tx", err)
		return
	}

	// No SMS: an undo is the landlord correcting their own book (DECISIONS,
	// Phase 26). The renter sees the periods reopen on their rent book.
	after, err := s.q.GetBackfillBatch(r.Context(), sqlc.GetBackfillBatchParams{OrgID: p.OrgID, ID: id})
	if err != nil {
		s.serverError(w, r, "backfill.undo.reload", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"backfill":          toBackfill(after, false),
		"payments_reversed": out.PaymentsReversed,
		"periods_reopened":  out.PeriodsReopened,
	})
}

func writeBackfillUndoRefusal(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errBackfillUndone):
		conflictCode(w, "already_undone", "backfill already undone",
			"this backfill has already been undone")
	case errors.Is(err, errBackfillTouched):
		conflictCode(w, "touched_since", "periods paid since",
			"other money has been recorded against these periods since the backfill; "+
				"reverse that first, or leave the backfill as it is")
	case errors.Is(err, errBackfillRefunded):
		conflictCode(w, "payment_refunded", "payment partly refunded",
			"part of this backfill's money was refunded when the tenancy ended; it cannot be undone")
	}
}

// undoBackfillTx takes one batch back, in the caller's transaction. Every
// refusal is decided before anything is written, so a caller that skips a
// refused batch (the import undo) leaves the transaction clean.
func (s *Server) undoBackfillTx(ctx context.Context, q *sqlc.Queries, p auth.Principal,
	id pgtype.UUID, reason string, grace int32,
) (backfillUndoOutcome, error) {
	var out backfillUndoOutcome
	batch, err := q.LockBackfillBatch(ctx, sqlc.LockBackfillBatchParams{OrgID: p.OrgID, ID: id})
	if err != nil {
		return out, err
	}
	if batch.UndoneAt.Valid {
		return out, errBackfillUndone
	}
	touched, err := q.BackfillBatchTouched(ctx, sqlc.BackfillBatchTouchedParams{
		OrgID: p.OrgID, ID: batch.ID, CreatedAt: batch.CreatedAt,
	})
	if err != nil {
		return out, err
	}
	if touched {
		return out, errBackfillTouched
	}
	ids, err := q.ListBackfillBatchPayments(ctx, sqlc.ListBackfillBatchPaymentsParams{
		OrgID: p.OrgID, BackfillBatchID: batch.ID,
	})
	if err != nil {
		return out, err
	}
	payments := make([]sqlc.GetPaymentRow, 0, len(ids))
	for _, pid := range ids {
		row, err := q.GetPayment(ctx, sqlc.GetPaymentParams{ID: pid, OrgID: p.OrgID})
		if err != nil {
			return out, err
		}
		refunded, err := q.PaymentRefunded(ctx, sqlc.PaymentRefundedParams{OrgID: p.OrgID, PaymentID: pid})
		if err != nil {
			return out, err
		}
		if refunded {
			return out, errBackfillRefunded
		}
		payments = append(payments, row)
	}

	// The money, through the ordinary reversal path: each payment stays,
	// stamped `reversed` with the reason, and the period it settled is
	// debited and recomputed.
	for _, row := range payments {
		if _, err := s.reverseTx(ctx, q, p, row, "Backfill undone: "+reason, grace); err != nil {
			return out, err
		}
		out.PaymentsReversed++
	}
	// The waivers: reopened, status recomputed from what has been paid and
	// the due date, exactly as undoing a period relief does.
	reopened, err := q.UnwaiveBackfillSchedules(ctx, sqlc.UnwaiveBackfillSchedulesParams{
		GraceDays: grace, OrgID: p.OrgID, BackfillBatchID: batch.ID,
	})
	if err != nil {
		return out, err
	}
	out.PeriodsReopened = len(reopened)

	if _, err := q.MarkBackfillBatchUndone(ctx, sqlc.MarkBackfillBatchUndoneParams{
		UndoneByUserID: p.UserID, UndoReason: &reason, OrgID: p.OrgID, ID: batch.ID,
	}); err != nil {
		return out, err
	}
	return out, audit.Record(ctx, q, audit.Entry{
		OrgID:       p.OrgIDString(),
		ActorUserID: p.UserIDString(),
		Action:      audit.ActionBackfillUndo,
		EntityType:  audit.EntityBackfillBatch,
		EntityID:    db.UUIDString(batch.ID),
		Before: map[string]any{
			"contract_id": db.UUIDString(batch.ContractID), "mode": batch.Mode,
			"until": batch.Until.Time.Format(dateLayout), "periods": batch.Periods, "amount": batch.Amount,
		},
		After: map[string]any{
			"reason": reason, "payments_reversed": out.PaymentsReversed,
			"periods_reopened": out.PeriodsReopened,
		},
	})
}

// ------------------------------------------------ CSV import kind `backfill` --

// importErrNothingToSettle is the row error for a line that would settle
// nothing: in a sheet, that is a typo in `until` far more often than a
// landlord checking.
const importErrNothingToSettle = "nothing is left to settle on or before this date"

// resolveBackfillRows resolves each line to the running contract of that
// renter on that unit and previews what the backfill would settle, from the
// rent book as it is now. It writes nothing.
func (s *Server) resolveBackfillRows(
	ctx context.Context, q *sqlc.Queries, p auth.Principal, rows []*importRow,
) error {
	users := map[string]*sqlc.User{}
	seen := map[string]int{}
	today := todayEAT()
	for _, row := range rows {
		parsed, errs := importer.ParseBackfillRow(row.raw)
		mergeErrors(row, errs)
		row.backfill = &parsed
		if !row.ok() {
			continue
		}
		row.resolved["mode"] = parsed.Mode
		row.resolved["until"] = parsed.Until.Format(dateLayout)
		if parsed.Until.After(today) {
			row.errs.Add("until", "must not be in the future")
			continue
		}

		user, err := s.importRenterByPhone(ctx, q, p, parsed.RenterPhone, users)
		if err != nil {
			return err
		}
		if user == nil {
			row.errs.Add("renter_phone", "no renter of this org has this number")
			continue
		}
		row.renterUserID = user.ID
		row.resolved["renter_name"] = user.FullName

		unit, err := q.FindUnitByCodeInOrg(ctx, sqlc.FindUnitByCodeInOrgParams{
			OrgID: p.OrgID, UnitCode: parsed.UnitCode,
		})
		if isNoRows(err) {
			row.errs.Add("unit_code", "no unit of this org has this code")
			continue
		}
		if err != nil {
			return err
		}
		row.unitID = unit.ID
		row.resolved["unit"] = unit.Name
		row.resolved["property"] = unit.PropertyName

		found, err := q.FindRunningContractForUnitAndRenter(ctx, sqlc.FindRunningContractForUnitAndRenterParams{
			OrgID: p.OrgID, UnitID: unit.ID, RenterUserID: user.ID,
		})
		if err != nil {
			return err
		}
		if len(found) == 0 {
			row.errs.Add("unit_code", "this renter has no running contract on this unit")
			continue
		}
		key := db.UUIDString(found[0])
		if line, dup := seen[key]; dup {
			row.errs.Add("unit_code", "this tenancy already has a line in this file (line "+strconv.Itoa(line)+")")
			continue
		}
		seen[key] = row.line
		contract, err := q.GetContract(ctx, sqlc.GetContractParams{ID: found[0], OrgID: p.OrgID})
		if err != nil {
			return err
		}
		row.contractID = contract.ID
		row.resolved["contract_id"] = key

		if parsed.Until.Before(contract.StartDate.Time) {
			row.errs.Add("until", "must not be before the contract's start date ("+
				contract.StartDate.Time.Format(dateLayout)+")")
			continue
		}

		book, err := q.ListSchedulesForContract(ctx, sqlc.ListSchedulesForContractParams{
			OrgID: p.OrgID, ContractID: contract.ID,
		})
		if err != nil {
			return err
		}
		periods, amount := backfillPreview(book, parsed.Until)
		if periods == 0 {
			row.errs.Add("until", importErrNothingToSettle)
			continue
		}
		row.resolved["periods"] = periods
		row.resolved["amount"] = amount
	}
	return nil
}

// backfillPreview counts what runBackfill would close up to `until`: every row
// due by then that is neither paid nor waived and still owes something, and
// the sum it owes.
func backfillPreview(book []sqlc.PaymentSchedule, until time.Time) (int, int64) {
	var periods int
	var amount int64
	for _, row := range book {
		if row.DueDate.Time.After(until) {
			continue
		}
		outstanding := row.Amount - row.PaidAmount
		if outstanding <= 0 || row.Status == payment.StatusWaived || row.Status == payment.StatusPaid {
			continue
		}
		periods++
		amount += outstanding
	}
	return periods, amount
}

// importRenterByPhone finds a renter this org knows by phone. A number
// belonging to another org's tenant and a number belonging to nobody give the
// same answer (nil), exactly as the payments sheet does (SPEC §8).
func (s *Server) importRenterByPhone(ctx context.Context, q *sqlc.Queries, p auth.Principal,
	phone string, cache map[string]*sqlc.User,
) (*sqlc.User, error) {
	if user, ok := cache[phone]; ok {
		return user, nil
	}
	var user *sqlc.User
	found, err := q.GetUserByPhone(ctx, &phone)
	switch {
	case isNoRows(err):
	case err != nil:
		return nil, err
	default:
		known, err := q.RenterKnownToOrg(ctx, sqlc.RenterKnownToOrgParams{
			OrgID: p.OrgID, RenterUserID: found.ID,
		})
		if err != nil {
			return nil, err
		}
		if known {
			user = &found
		}
	}
	cache[phone] = user
	return user, nil
}

// applyBackfillRows commits a `backfill` sheet: each line is one call of the
// same core the contract page's button runs, so each line is one batch, one
// `contract.backfill` audit row and at most one `backfill_done` SMS.
func (s *Server) applyBackfillRows(
	ctx context.Context, q *sqlc.Queries, p auth.Principal, batch sqlc.ImportBatch, rows []*importRow,
) (int, []string, error) {
	org, err := q.GetOrg(ctx, p.OrgID)
	if err != nil {
		return 0, nil, err
	}
	settings := parseSettings(org.Settings)
	brand := s.brandingAssets(ctx, p.OrgID, org.Name)

	var made int
	var notifyIDs []string
	for _, row := range rows {
		if !row.ok() || row.backfill == nil {
			continue
		}
		contract, err := q.GetContract(ctx, sqlc.GetContractParams{ID: row.contractID, OrgID: p.OrgID})
		if err != nil {
			return made, nil, err
		}
		b := row.backfill
		out, err := s.runBackfill(ctx, q, backfillRequest{
			OrgID: p.OrgID, ActorUserID: p.UserID, Contract: contract,
			Until: b.Until, Mode: b.Mode, Method: b.Method,
			Reference: db.Str(b.Reference), Note: db.Str(b.Note),
			PerRowPaidAt: b.PaidAt.IsZero(), PaidAt: b.PaidAt,
			OrgName: brand.DisplayName, Settings: settings, ImportBatchID: batch.ID,
		})
		if err != nil {
			return made, nil, err
		}
		if !out.BatchID.Valid {
			return made, nil, row.fail("until", importErrNothingToSettle)
		}
		if out.NotifyID != "" {
			notifyIDs = append(notifyIDs, out.NotifyID)
		}
		made++
		row.resolved["backfill_id"] = db.UUIDString(out.BatchID)
		row.resolved["settled"] = out.Settled
		if err := s.stampImportRow(ctx, q, p, row, audit.EntityBackfillBatch, out.BatchID); err != nil {
			return made, nil, err
		}
	}
	return made, notifyIDs, nil
}

// undoImportedBackfills takes back the batches a `backfill` import wrote.
// Like every import undo, what has been touched since is kept and simply not
// counted: a batch whose periods have since taken other money stays, and can
// still be undone from the contract page once that money is dealt with.
func (s *Server) undoImportedBackfills(ctx context.Context, q *sqlc.Queries, p auth.Principal,
	importID pgtype.UUID, grace int32,
) (int, error) {
	batches, err := q.ListBackfillBatchesForImport(ctx, sqlc.ListBackfillBatchesForImportParams{
		OrgID: p.OrgID, ImportBatchID: importID,
	})
	if err != nil {
		return 0, err
	}
	var undone int
	for _, b := range batches {
		_, err := s.undoBackfillTx(ctx, q, p, b.ID, importBackfillUndoReason, grace)
		switch {
		case err == nil:
			undone++
		case errors.Is(err, errBackfillUndone), errors.Is(err, errBackfillTouched),
			errors.Is(err, errBackfillRefunded):
		default:
			return undone, err
		}
	}
	return undone, nil
}
