package httpserver

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/validate"
)

// currencyTZS is the only currency in the MVP (SPEC §4).
const currencyTZS = "TZS"

func todayDate() pgtype.Date {
	now := time.Now().UTC()
	return pgtype.Date{Time: time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
}

// orgUnit loads a unit inside the caller's org (404 otherwise, including for a
// malformed id — ids must not be probeable across orgs).
func (s *Server) orgUnit(w http.ResponseWriter, r *http.Request, p auth.Principal) (sqlc.GetUnitRow, bool) {
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundUnit(w)
		return sqlc.GetUnitRow{}, false
	}
	row, err := s.q.GetUnit(r.Context(), sqlc.GetUnitParams{OrgID: p.OrgID, ID: id})
	if isNoRows(err) {
		notFoundUnit(w)
		return sqlc.GetUnitRow{}, false
	}
	if err != nil {
		s.serverError(w, r, "units.get", err)
		return sqlc.GetUnitRow{}, false
	}
	return row, true
}

// validateAllowedPeriods turns the request's allowed_period_ids into a UUID
// array, rejecting ids that are not active payment periods of this org (they
// would silently offer nothing to the renter).
func (s *Server) validateAllowedPeriods(r *http.Request, f validate.Fields, p auth.Principal, ids []string) []pgtype.UUID {
	if len(ids) == 0 {
		return nil
	}
	parsed := parseUUIDList(f, "allowed_period_ids", ids)
	if parsed == nil {
		return nil
	}
	found, err := s.q.ListPaymentPeriodsByIDs(r.Context(), sqlc.ListPaymentPeriodsByIDsParams{
		OrgID: p.OrgID, Ids: parsed,
	})
	if err != nil {
		f.Add("allowed_period_ids", "could not be verified")
		return nil
	}
	if len(found) != len(parsed) {
		f.Add("allowed_period_ids", "must all be payment periods of this organisation")
		return nil
	}
	return parsed
}

// ------------------------------------------------------------- GET /units --

func (s *Server) handleListUnits(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	f := validate.Fields{}
	page := parseListPage(r, f)
	qs := r.URL.Query()

	params := sqlc.ListUnitsParams{
		OrgID: p.OrgID, CursorAt: page.CursorAt, CursorID: page.CursorID, RowLimit: page.Limit,
	}
	if v := strings.TrimSpace(qs.Get("status")); v != "" {
		status := f.OneOf("status", v, statusVacant, statusOccupied, statusUnlisted, statusMaintenance)
		params.Status = &status
	}
	if v := strings.TrimSpace(qs.Get("property_id")); v != "" {
		id, err := db.ParseUUID(v)
		if err != nil {
			f.Add("property_id", "must be a property id (UUID)")
		} else {
			params.PropertyID = id
		}
	}
	if v := strings.TrimSpace(qs.Get("q")); v != "" {
		// The value goes into an ILIKE pattern; escape the wildcards so a
		// search for "100%" cannot match everything.
		escaped := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(v)
		params.Q = &escaped
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	rows, err := s.q.ListUnits(r.Context(), params)
	if err != nil {
		s.serverError(w, r, "units.list", err)
		return
	}
	items := make([]unitResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, s.toUnit(unitRowOfList(row)))
	}
	var next *string
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		next = nextCursor(len(rows), page.Limit, last.CreatedAt.Time, db.UUIDString(last.ID))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) handleGetUnit(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.orgUnit(w, r, p)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"unit": s.toUnit(unitRowOfGet(row))})
}

// ------------------------------------------------------ PATCH /units/{id} --

func (s *Server) handlePatchUnit(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	current, ok := s.orgUnit(w, r, p)
	if !ok {
		return
	}

	var body struct {
		Name             *string          `json:"name"`
		Status           *string          `json:"status"`
		AllowedPeriodIDs *json.RawMessage `json:"allowed_period_ids"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}

	f := validate.Fields{}
	params := sqlc.UpdateUnitParams{OrgID: p.OrgID, ID: current.ID}
	if body.Name != nil {
		name := f.MaxLen("name", f.Required("name", *body.Name), unitNameMax)
		params.Name = &name
	}
	if body.Status != nil {
		// `occupied` is derived from contract state, never set by hand: a unit
		// becomes occupied when a contract activates (SPEC §4).
		if *body.Status == statusOccupied {
			f.Add("status", "occupied is derived from contracts and cannot be set directly")
		} else {
			status := f.OneOf("status", *body.Status, statusVacant, statusUnlisted, statusMaintenance)
			params.Status = &status
			// unlisted/maintenance are landlord overrides; returning the unit
			// to vacant hands control back to the contract lifecycle.
			override := status != statusVacant
			params.StatusOverride = &override
		}
	}
	if body.AllowedPeriodIDs != nil {
		var ids *[]string
		if err := json.Unmarshal(*body.AllowedPeriodIDs, &ids); err != nil {
			f.Add("allowed_period_ids", "must be a list of ids or null")
		} else {
			params.SetAllowed = true
			if ids != nil {
				params.AllowedPeriodIds = s.validateAllowedPeriods(r, f, p, *ids)
				if len(*ids) > 0 && params.AllowedPeriodIds == nil {
					f.Add("allowed_period_ids", "must all be payment periods of this organisation")
				}
			}
		}
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	var updated sqlc.Unit
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		updated, err = q.UpdateUnit(r.Context(), params)
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionUnitUpdate,
			EntityType:  audit.EntityUnit,
			EntityID:    db.UUIDString(updated.ID),
			Before:      map[string]any{"name": current.Name, "status": current.Status, "status_override": current.StatusOverride},
			After:       map[string]any{"name": updated.Name, "status": updated.Status, "status_override": updated.StatusOverride},
		})
	}); err != nil {
		s.serverError(w, r, "units.patch.tx", err)
		return
	}

	row, err := s.q.GetUnit(r.Context(), sqlc.GetUnitParams{OrgID: p.OrgID, ID: updated.ID})
	if err != nil {
		s.serverError(w, r, "units.patch.reload", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"unit": s.toUnit(unitRowOfGet(row))})
}

// ----------------------------------------------------- DELETE /units/{id} --

func (s *Server) handleDeleteUnit(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	current, ok := s.orgUnit(w, r, p)
	if !ok {
		return
	}

	blocking, err := s.q.CountBlockingContractsForUnit(r.Context(),
		sqlc.CountBlockingContractsForUnitParams{OrgID: p.OrgID, UnitID: current.ID})
	if err != nil {
		s.serverError(w, r, "units.delete.contracts", err)
		return
	}
	if blocking > 0 {
		httpx.WriteProblem(w, http.StatusConflict, "unit in use",
			"this unit has an active or unsigned contract")
		return
	}

	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.SoftDeleteUnit(r.Context(), sqlc.SoftDeleteUnitParams{OrgID: p.OrgID, ID: current.ID}); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionUnitDelete,
			EntityType:  audit.EntityUnit,
			EntityID:    db.UUIDString(current.ID),
			Before:      map[string]any{"name": current.Name, "unit_code": current.UnitCode, "status": current.Status},
		})
	}); err != nil {
		s.serverError(w, r, "units.delete.tx", err)
		return
	}
	NoContent(w)
}

// ------------------------------------------------------ POST /units/{id}/qr --

func (s *Server) handleUnitQR(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	unit, ok := s.orgUnit(w, r, p)
	if !ok {
		return
	}
	if s.deps.Storage == nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "storage unavailable",
			"QR codes cannot be generated right now")
		return
	}

	unitID := db.UUIDString(unit.ID)
	scanURL, pngURL, err := s.generateQR(r.Context(), p.OrgIDString(), unitID, unit.UnitCode)
	if err != nil {
		s.serverError(w, r, "units.qr", err)
		return
	}
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionUnitQRGenerate,
			EntityType:  audit.EntityUnit,
			EntityID:    unitID,
			After:       map[string]any{"unit_code": unit.UnitCode, "object_key": qrObjectKey(p.OrgIDString(), unitID)},
		})
	}); err != nil {
		s.serverError(w, r, "units.qr.audit", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"unit_code": unit.UnitCode, "scan_url": scanURL, "png_url": pngURL,
	})
}

// ------------------------------------------------------------- prices --

func (s *Server) handleListPrices(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	unit, ok := s.orgUnit(w, r, p)
	if !ok {
		return
	}
	rows, err := s.q.ListPricePlans(r.Context(), sqlc.ListPricePlansParams{OrgID: p.OrgID, UnitID: unit.ID})
	if err != nil {
		s.serverError(w, r, "prices.list", err)
		return
	}
	items := make([]priceHistoryResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, priceHistoryResponse{
			priceResponse: priceResponse{
				ID:            db.UUIDString(row.ID),
				Amount:        row.Amount,
				Currency:      row.Currency,
				PeriodDays:    row.PeriodDays,
				EffectiveFrom: row.EffectiveFrom.Time.Format(dateLayout),
			},
			CreatedAt:     row.CreatedAt.Time,
			CreatedByName: db.StrVal(row.CreatedByName),
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCreatePrice(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	unit, ok := s.orgUnit(w, r, p)
	if !ok {
		return
	}

	var body struct {
		Amount        int64   `json:"amount"`
		PeriodDays    int32   `json:"period_days"`
		EffectiveFrom *string `json:"effective_from"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	if body.Amount <= 0 {
		f.Add("amount", "must be a whole number greater than 0")
	}
	if body.PeriodDays <= 0 {
		f.Add("period_days", "must be a whole number greater than 0")
	}
	effectiveFrom := optDate(f, "effective_from", body.EffectiveFrom)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	price, err := s.insertPrice(r, p, unit.ID, body.Amount, body.PeriodDays, effectiveFrom,
		audit.ActionPriceCreate, map[string]any{"unit_id": db.UUIDString(unit.ID)})
	if err != nil {
		s.serverError(w, r, "prices.create.tx", err)
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"price": price})
}

// insertPrice writes one price row plus its audit entry in a transaction.
func (s *Server) insertPrice(r *http.Request, p auth.Principal, unitID pgtype.UUID,
	amount int64, periodDays int32, effectiveFrom pgtype.Date, action string, extra map[string]any,
) (priceResponse, error) {
	var out priceResponse
	err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		row, err := q.CreatePricePlan(r.Context(), sqlc.CreatePricePlanParams{
			OrgID: p.OrgID, UnitID: unitID, Amount: amount, Currency: currencyTZS,
			PeriodDays: periodDays, EffectiveFrom: effectiveFrom, CreatedByUserID: p.UserID,
		})
		if err != nil {
			return err
		}
		after := map[string]any{
			"amount": row.Amount, "period_days": row.PeriodDays,
			"effective_from": row.EffectiveFrom.Time.Format(dateLayout),
		}
		for k, v := range extra {
			after[k] = v
		}
		if err := audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      action,
			EntityType:  audit.EntityPricePlan,
			EntityID:    db.UUIDString(row.ID),
			After:       after,
		}); err != nil {
			return err
		}
		out = priceResponse{
			ID:            db.UUIDString(row.ID),
			Amount:        row.Amount,
			Currency:      row.Currency,
			PeriodDays:    row.PeriodDays,
			EffectiveFrom: row.EffectiveFrom.Time.Format(dateLayout),
		}
		return nil
	})
	return out, err
}

// -------------------------------------------------- POST /units/bulk-price --

func (s *Server) handleBulkPrice(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		UnitIDs       []string `json:"unit_ids"`
		Mode          string   `json:"mode"`
		Value         float64  `json:"value"`
		PeriodDays    *int32   `json:"period_days"`
		EffectiveFrom *string  `json:"effective_from"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}

	f := validate.Fields{}
	if len(body.UnitIDs) == 0 || len(body.UnitIDs) > bulkUnitsMax {
		f.Add("unit_ids", "must contain between 1 and 200 unit ids")
	}
	unitIDs := parseUUIDList(f, "unit_ids", body.UnitIDs)
	mode := f.OneOf("mode", body.Mode, "percent", "set")
	if mode == "set" && body.Value <= 0 {
		f.Add("value", "must be greater than 0")
	}
	if mode == "percent" && body.Value <= -100 {
		f.Add("value", "must be greater than -100 (a price cannot go below zero)")
	}
	if body.PeriodDays != nil && *body.PeriodDays <= 0 {
		f.Add("period_days", "must be a whole number greater than 0")
	}
	effectiveFrom := optDate(f, "effective_from", body.EffectiveFrom)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	items := make([]priceResponse, 0, len(unitIDs))
	for _, unitID := range unitIDs {
		unit, err := s.q.GetUnit(r.Context(), sqlc.GetUnitParams{OrgID: p.OrgID, ID: unitID})
		if isNoRows(err) {
			// One foreign id fails the whole call: a partial bulk price change
			// is worse than none.
			notFoundUnit(w)
			return
		}
		if err != nil {
			s.serverError(w, r, "prices.bulk.unit", err)
			return
		}

		amount, periodDays, ok := s.bulkAmountFor(w, unit, mode, body.Value, body.PeriodDays)
		if !ok {
			return
		}
		price, err := s.insertPrice(r, p, unit.ID, amount, periodDays, effectiveFrom,
			audit.ActionPriceBulkUpdate, map[string]any{
				"unit_id": db.UUIDString(unit.ID), "mode": mode, "value": body.Value,
			})
		if err != nil {
			s.serverError(w, r, "prices.bulk.tx", err)
			return
		}
		items = append(items, price)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// bulkAmountFor resolves the new amount and period basis for one unit.
// `percent` needs an existing price to scale; a unit without one is a 409
// rather than a silent skip.
func (s *Server) bulkAmountFor(w http.ResponseWriter, unit sqlc.GetUnitRow, mode string,
	value float64, periodDays *int32,
) (int64, int32, bool) {
	basis := int32(30)
	if unit.PriceID.Valid {
		basis = unit.PricePeriodDays
	}
	if periodDays != nil {
		basis = *periodDays
	}
	if mode == "set" {
		return int64(math.Round(value)), basis, true
	}
	if !unit.PriceID.Valid {
		httpx.WriteProblem(w, http.StatusConflict, "no current price",
			"unit "+unit.Name+" has no price to apply a percentage to")
		return 0, 0, false
	}
	amount := int64(math.Round(float64(unit.PriceAmount) * (1 + value/100)))
	if amount < 0 {
		amount = 0
	}
	return amount, basis, true
}
