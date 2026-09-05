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
	checkPeriodDays(f, "days", body.Days)
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

	// `is_recommended` is deliberately absent: the badge is exclusive and moves
	// through POST /org/payment-periods/{id}/recommend, which also clears it
	// from the period that had it. The decoder disallows unknown fields, so a
	// client that sends it here is told so with a 400 rather than silently
	// having it ignored.
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
		checkPeriodDays(f, "days", *body.Days)
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

// ----------------------------- POST /org/payment-periods/{id}/recommend --

// handleRecommendPaymentPeriod moves the org's single "Recommended" badge onto
// one period.
//
// The badge is exclusive (PLAN2 #8, confirmed with the client): it is the
// landlord saying "this is the one I suggest", which is only information while
// exactly one period carries it. Migration 000012 backs that with a partial
// unique index, so the clear and the set have to happen in one transaction —
// two requests racing for the badge leave one of them with a unique violation
// rather than an org with two recommendations.
//
// It is a separate endpoint rather than a PATCH field because it writes to a
// row the caller did not name (the period losing the badge). PATCH refuses an
// `is_recommended` key outright: the decoder disallows unknown fields, so a
// client sending it gets a 400 pointing at this route.
func (s *Server) handleRecommendPaymentPeriod(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	current, ok := s.orgPeriod(w, r, p)
	if !ok {
		return
	}

	// The badge points renters at a period they can actually choose.
	if !current.Active {
		httpx.WriteProblemCode(w, http.StatusConflict, "period_inactive", "inactive payment period",
			"a period that is not offered cannot be the recommended one")
		return
	}

	// Recorded before the write, so the audit entry names the period that lost
	// the badge as well as the one that gained it.
	previous, err := s.q.GetRecommendedPaymentPeriod(r.Context(), p.OrgID)
	hadPrevious := err == nil
	if err != nil && !isNoRows(err) {
		s.serverError(w, r, "periods.recommend.previous", err)
		return
	}

	var updated sqlc.PaymentPeriod
	err = s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if err := q.ClearRecommendedPaymentPeriod(r.Context(), sqlc.ClearRecommendedPaymentPeriodParams{
			OrgID: p.OrgID, KeepID: current.ID,
		}); err != nil {
			return err
		}
		var err error
		updated, err = q.SetRecommendedPaymentPeriod(r.Context(), sqlc.SetRecommendedPaymentPeriodParams{
			OrgID: p.OrgID, ID: current.ID,
		})
		if err != nil {
			return err
		}
		before := map[string]any{"recommended_period_id": nil}
		if hadPrevious {
			before = map[string]any{
				"recommended_period_id": db.UUIDString(previous.ID),
				"period":                toPeriod(previous),
			}
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionPeriodRecommend,
			EntityType:  audit.EntityPaymentPeriod,
			EntityID:    db.UUIDString(current.ID),
			Before:      before,
			After: map[string]any{
				"recommended_period_id": db.UUIDString(updated.ID),
				"period":                toPeriod(updated),
			},
		})
	})
	if isUnique(err) {
		// The partial unique index; another request took the badge mid-flight.
		httpx.WriteProblem(w, http.StatusConflict, "recommendation in flight",
			"another change to the recommended period is in progress — try again")
		return
	}
	if err != nil {
		s.serverError(w, r, "periods.recommend.tx", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"period": toPeriod(updated)})
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

	// The badge is exclusive (PLAN2 #8), so restoring presets must not mint a
	// second recommended period: a preset is created badged only when it is the
	// designated one (Monthly) *and* the org has nothing badged already.
	// Reactivating a preset never touches the flag — restoring an old row is
	// not a decision about which period the landlord recommends.
	hasRecommended := false
	for _, row := range existing {
		if row.IsRecommended {
			hasRecommended = true
			break
		}
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
				recommend := preset.recommended && !hasRecommended
				row, err := q.CreatePaymentPeriod(r.Context(), sqlc.CreatePaymentPeriodParams{
					OrgID: p.OrgID, Label: label, Days: preset.days,
					IsRecommended: recommend, SortOrder: sortOrder + int32(i) + 1,
				})
				if err != nil {
					return err
				}
				if recommend {
					hasRecommended = true
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
