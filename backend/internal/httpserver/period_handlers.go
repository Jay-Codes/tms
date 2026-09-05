package httpserver

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/validate"
)

const periodLabelMax = 40

func notFoundPeriod(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such payment period")
}

// ----------------------------------------------- GET /org/payment-periods --

func (s *Server) handleListPaymentPeriods(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	includeInactive := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_inactive")), "true")

	rows, err := s.q.ListPaymentPeriods(r.Context(), sqlc.ListPaymentPeriodsParams{
		OrgID: p.OrgID, IncludeInactive: includeInactive,
	})
	if err != nil {
		s.serverError(w, r, "periods.list", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": toPeriods(rows)})
}

// ---------------------------------------------- POST /org/payment-periods --

func (s *Server) handleCreatePaymentPeriod(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		Label string `json:"label"`
		Days  int32  `json:"days"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	label := f.MaxLen("label", f.Required("label", body.Label), periodLabelMax)
	if body.Days <= 0 {
		f.Add("days", "must be a whole number of days greater than 0")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	active, err := s.q.ListActivePaymentPeriods(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "periods.create.list", err)
		return
	}
	for _, existing := range active {
		if existing.Days == body.Days || strings.EqualFold(existing.Label, label) {
			httpx.WriteProblem(w, http.StatusConflict, "duplicate payment period",
				"this organisation already offers a period with that label or day count")
			return
		}
	}
	sortOrder, err := s.q.MaxPaymentPeriodSortOrder(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "periods.create.sort", err)
		return
	}

	var created sqlc.PaymentPeriod
	err = s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		created, err = q.CreatePaymentPeriod(r.Context(), sqlc.CreatePaymentPeriodParams{
			OrgID: p.OrgID, Label: label, Days: body.Days,
			IsRecommended: false, SortOrder: sortOrder + 1,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionPeriodCreate,
			EntityType:  audit.EntityPaymentPeriod,
			EntityID:    db.UUIDString(created.ID),
			After:       toPeriod(created),
		})
	})
	if isUnique(err) {
		httpx.WriteProblem(w, http.StatusConflict, "duplicate payment period",
			"this organisation already offers a period with that label or day count")
		return
	}
	if err != nil {
		s.serverError(w, r, "periods.create.tx", err)
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"period": toPeriod(created)})
}

// --------------------------------------- PATCH /org/payment-periods/{id} --

func (s *Server) handlePatchPaymentPeriod(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	current, ok := s.orgPeriod(w, r, p)
	if !ok {
		return
	}

	var body struct {
		Label     *string `json:"label"`
		Days      *int32  `json:"days"`
		SortOrder *int32  `json:"sort_order"`
		Active    *bool   `json:"active"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	params := sqlc.UpdatePaymentPeriodParams{OrgID: p.OrgID, ID: current.ID}
	if body.Label != nil {
		label := f.MaxLen("label", f.Required("label", *body.Label), periodLabelMax)
		params.Label = &label
	}
	if body.Days != nil {
		if *body.Days <= 0 {
			f.Add("days", "must be a whole number of days greater than 0")
		}
		params.Days = body.Days
	}
	params.SortOrder = body.SortOrder
	params.Active = body.Active
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	// Deactivating the last active period would leave renters with nothing to
	// choose at contract time (API.md → 409).
	if body.Active != nil && !*body.Active && current.Active {
		if blocked, err := s.lastActivePeriod(w, r, p); err != nil || blocked {
			return
		}
	}

	var updated sqlc.PaymentPeriod
	err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		updated, err = q.UpdatePaymentPeriod(r.Context(), params)
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionPeriodUpdate,
			EntityType:  audit.EntityPaymentPeriod,
			EntityID:    db.UUIDString(current.ID),
			Before:      toPeriod(current),
			After:       toPeriod(updated),
		})
	})
	if isUnique(err) {
		httpx.WriteProblem(w, http.StatusConflict, "duplicate payment period",
			"this organisation already offers a period with that label or day count")
		return
	}
	if err != nil {
		s.serverError(w, r, "periods.patch.tx", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"period": toPeriod(updated)})
}

// -------------------------------------- DELETE /org/payment-periods/{id} --

func (s *Server) handleDeletePaymentPeriod(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	current, ok := s.orgPeriod(w, r, p)
	if !ok {
		return
	}
	if current.Active {
		if blocked, err := s.lastActivePeriod(w, r, p); err != nil || blocked {
			return
		}
	}

	inactive := false
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		// Soft delete: periods are referenced by historical contracts, so the
		// row stays and only stops being offered (API.md).
		if _, err := q.UpdatePaymentPeriod(r.Context(), sqlc.UpdatePaymentPeriodParams{
			OrgID: p.OrgID, ID: current.ID, Active: &inactive,
		}); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionPeriodDelete,
			EntityType:  audit.EntityPaymentPeriod,
			EntityID:    db.UUIDString(current.ID),
			Before:      toPeriod(current),
		})
	}); err != nil {
		s.serverError(w, r, "periods.delete.tx", err)
		return
	}
	NoContent(w)
}

// ------------------------- POST /org/payment-periods/restore-recommended --

func (s *Server) handleRestoreRecommendedPeriods(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	existing, err := s.q.ListPaymentPeriods(r.Context(), sqlc.ListPaymentPeriodsParams{
		OrgID: p.OrgID, IncludeInactive: true,
	})
	if err != nil {
		s.serverError(w, r, "periods.restore.list", err)
		return
	}
	sortOrder, err := s.q.MaxPaymentPeriodSortOrder(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "periods.restore.sort", err)
		return
	}

	// Idempotent: a preset already offered is left exactly as it is, one that
	// was deactivated comes back, one that was never there is created.
	var restored []map[string]any
	err = s.inTx(r.Context(), func(q *sqlc.Queries) error {
		for i, preset := range recommendedPeriods {
			var match *sqlc.PaymentPeriod
			labelTaken := false
			for idx := range existing {
				row := existing[idx]
				if row.Days == preset.days {
					match = &existing[idx]
				}
				if row.Active && strings.EqualFold(row.Label, preset.label) {
					labelTaken = true
				}
			}
			switch {
			case match != nil && match.Active:
				continue // already offered
			case match != nil:
				label := match.Label
				if !labelTaken {
					label = preset.label
				}
				row, err := q.ReactivatePaymentPeriod(r.Context(), sqlc.ReactivatePaymentPeriodParams{
					OrgID: p.OrgID, ID: match.ID, Label: label, SortOrder: match.SortOrder,
				})
				if err != nil {
					return err
				}
				restored = append(restored, map[string]any{"id": db.UUIDString(row.ID), "days": row.Days, "action": "reactivated"})
			default:
				label := preset.label
				if labelTaken {
					label = preset.label + " (recommended)"
				}
				row, err := q.CreatePaymentPeriod(r.Context(), sqlc.CreatePaymentPeriodParams{
					OrgID: p.OrgID, Label: label, Days: preset.days,
					IsRecommended: true, SortOrder: sortOrder + int32(i) + 1,
				})
				if err != nil {
					return err
				}
				restored = append(restored, map[string]any{"id": db.UUIDString(row.ID), "days": row.Days, "action": "created"})
			}
		}
		if len(restored) == 0 {
			return nil
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionPeriodRestore,
			EntityType:  audit.EntityPaymentPeriod,
			After:       map[string]any{"restored": restored},
		})
	})
	if err != nil {
		s.serverError(w, r, "periods.restore.tx", err)
		return
	}

	rows, err := s.q.ListPaymentPeriods(r.Context(), sqlc.ListPaymentPeriodsParams{OrgID: p.OrgID})
	if err != nil {
		s.serverError(w, r, "periods.restore.reload", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": toPeriods(rows)})
}

// --------------------------------------------------------------- helpers --

func (s *Server) orgPeriod(w http.ResponseWriter, r *http.Request, p auth.Principal) (sqlc.PaymentPeriod, bool) {
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundPeriod(w)
		return sqlc.PaymentPeriod{}, false
	}
	row, err := s.q.GetPaymentPeriod(r.Context(), sqlc.GetPaymentPeriodParams{OrgID: p.OrgID, ID: id})
	if isNoRows(err) {
		notFoundPeriod(w)
		return sqlc.PaymentPeriod{}, false
	}
	if err != nil {
		s.serverError(w, r, "periods.get", err)
		return sqlc.PaymentPeriod{}, false
	}
	return row, true
}

// lastActivePeriod writes the 409 (or a 500) and reports whether the caller
// should stop.
func (s *Server) lastActivePeriod(w http.ResponseWriter, r *http.Request, p auth.Principal) (bool, error) {
	count, err := s.q.CountActivePaymentPeriods(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "periods.count", err)
		return true, err
	}
	if count <= 1 {
		httpx.WriteProblem(w, http.StatusConflict, "last payment period",
			"an organisation must always offer at least one payment period")
		return true, nil
	}
	return false, nil
}
