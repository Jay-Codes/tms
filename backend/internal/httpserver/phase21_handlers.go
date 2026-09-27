package httpserver

import (
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/validate"
)

// Phase 21 §21.2 — money owed after a tenancy has closed.
//
// A terminated or ended contract keeps the periods the renter lived through
// (FLOWS 6.5). This file is what can happen to them afterwards: the landlord
// sees who still owes (GET /arrears), keeps taking their money (POST /payments
// and proofs accept closed contracts — see contractTakesPayments), and when
// the money will never come, writes the rest off as bad debt, reversibly.

const writeOffReasonMax = 200

// contractTakesPayments is the one rule for "can money land on this contract".
// A signed tenancy takes money for as long as it has rows that owe: while it
// runs, and after it closes, so arrears can still be collected. An unsigned
// contract owes nothing yet.
func contractTakesPayments(status string) bool {
	switch status {
	case contractActive, contractExpiring, contractEnded, contractTerminated:
		return true
	}
	return false
}

func contractClosed(status string) bool {
	return status == contractEnded || status == contractTerminated
}

// ------------------------------------------------------------ GET /arrears --

type arrearsItem struct {
	ContractID     string     `json:"contract_id"`
	ContractStatus string     `json:"contract_status"`
	ClosedOn       string     `json:"closed_on"`
	Renter         arrearsWho `json:"renter"`
	UnitID         string     `json:"unit_id"`
	UnitName       string     `json:"unit_name"`
	PropertyID     string     `json:"property_id"`
	PropertyName   string     `json:"property_name"`
	Outstanding    int64      `json:"outstanding"`
	Periods        int64      `json:"periods"`
	OldestDue      string     `json:"oldest_due"`
	LastPaidAt     *time.Time `json:"last_paid_at"`
}

type arrearsWho struct {
	ID       string  `json:"id"`
	FullName string  `json:"full_name"`
	Phone    *string `json:"phone"`
}

// handleListArrears is the "former tenants who still owe" board: closed
// contracts with at least one period owing, newest closure first, cursor-paged
// by closure date, with the org-wide total on every page.
func (s *Server) handleListArrears(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	f := validate.Fields{}
	page := parseListPage(r, f)
	propertyID := optQueryUUID(f, "property_id", r.URL.Query().Get("property_id"))
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	params := sqlc.ListFormerArrearsParams{
		OrgID: p.OrgID, PropertyID: propertyID, RowLimit: page.Limit,
	}
	if page.CursorAt.Valid {
		params.CursorAt = pgtype.Date{Time: page.CursorAt.Time.UTC().Truncate(24 * time.Hour), Valid: true}
		params.CursorID = page.CursorID
	}
	rows, err := s.q.ListFormerArrears(r.Context(), params)
	if err != nil {
		s.serverError(w, r, "arrears.list", err)
		return
	}
	total, err := s.q.FormerArrearsTotal(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "arrears.total", err)
		return
	}

	items := make([]arrearsItem, 0, len(rows))
	for _, row := range rows {
		it := arrearsItem{
			ContractID:     db.UUIDString(row.ContractID),
			ContractStatus: row.ContractStatus,
			ClosedOn:       row.ClosedOn.Time.Format(dateLayout),
			Renter: arrearsWho{
				ID: db.UUIDString(row.RenterUserID), FullName: row.RenterName, Phone: row.RenterPhone,
			},
			UnitID: db.UUIDString(row.UnitID), UnitName: row.UnitName,
			PropertyID: db.UUIDString(row.PropertyID), PropertyName: row.PropertyName,
			Outstanding: row.Outstanding, Periods: row.Periods,
			OldestDue: row.OldestDue.Time.Format(dateLayout),
		}
		if row.LastPaidAt.Valid {
			t := row.LastPaidAt.Time
			it.LastPaidAt = &t
		}
		items = append(items, it)
	}
	var next *string
	if n := len(rows); n > 0 {
		last := rows[n-1]
		next = nextCursor(n, page.Limit, last.ClosedOn.Time, db.UUIDString(last.ContractID))
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items": items, "next_cursor": next,
		"total": map[string]any{"outstanding": total.Outstanding, "contracts": total.Contracts},
	})
}

// -------------------------------------------- POST /contracts/{id}/write-off --

// handleWriteOff closes every still-owing period of a closed contract as bad
// debt. Owner only: it is the decision to stop expecting money. It is refused
// on a running contract — there the answer is to collect, or to terminate.
func (s *Server) handleWriteOff(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	contract, ok := s.loadContract(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	reason := f.MaxLen("reason", f.Required("reason", strings.TrimSpace(body.Reason)), writeOffReasonMax)
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	if !contractClosed(contract.Status) {
		conflictCode(w, "contract_not_closed", "contract still running",
			"only a terminated or ended tenancy can have its arrears written off")
		return
	}

	var rows []sqlc.PaymentSchedule
	var total int64
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		rows, err = q.WriteOffSchedules(r.Context(), sqlc.WriteOffSchedulesParams{
			ActorUserID: p.UserID, Reason: &reason, OrgID: p.OrgID, ContractID: contract.ID,
		})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			if row.Amount > row.PaidAmount {
				total += row.Amount - row.PaidAmount
			}
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionContractWriteOff,
			EntityType:  audit.EntityContract,
			EntityID:    db.UUIDString(contract.ID),
			After:       map[string]any{"periods": len(rows), "amount": total, "reason": reason},
		})
	}); err != nil {
		s.serverError(w, r, "contract.write_off.tx", err)
		return
	}
	if len(rows) == 0 {
		conflictCode(w, "nothing_owing", "nothing to write off",
			"every period of this tenancy is already settled")
		return
	}
	s.writeOffAnswer(w, rows, total)
}

// --------------------------------------- POST /contracts/{id}/write-off/undo --

// handleUndoWriteOff reopens a write-off — the former tenant turned up, or the
// write-off was a mistake. Each row's status is recomputed from its own money
// and dates, and the debt is back on the arrears board.
func (s *Server) handleUndoWriteOff(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	contract, ok := s.loadContract(w, r)
	if !ok {
		return
	}
	org, err := s.q.GetOrg(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "contract.write_off_undo.org", err)
		return
	}
	grace := int32(parseSettings(org.Settings).GraceDays)

	var rows []sqlc.PaymentSchedule
	var total int64
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		rows, err = q.RestoreWrittenOff(r.Context(), sqlc.RestoreWrittenOffParams{
			GraceDays: grace, OrgID: p.OrgID, ContractID: contract.ID,
		})
		if err != nil || len(rows) == 0 {
			return err
		}
		for _, row := range rows {
			if row.Amount > row.PaidAmount {
				total += row.Amount - row.PaidAmount
			}
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionContractWriteOffUndo,
			EntityType:  audit.EntityContract,
			EntityID:    db.UUIDString(contract.ID),
			After:       map[string]any{"periods": len(rows), "amount": total},
		})
	}); err != nil {
		s.serverError(w, r, "contract.write_off_undo.tx", err)
		return
	}
	if len(rows) == 0 {
		conflictCode(w, "nothing_written_off", "nothing to reopen",
			"this tenancy has no written-off periods")
		return
	}
	s.writeOffAnswer(w, rows, total)
}

func (s *Server) writeOffAnswer(w http.ResponseWriter, rows []sqlc.PaymentSchedule, total int64) {
	out := make([]scheduleResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toSchedule(row))
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"periods": len(rows), "amount": total, "schedules": out,
	})
}
