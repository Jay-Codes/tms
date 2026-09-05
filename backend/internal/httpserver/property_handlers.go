package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/validate"
)

// errStorageUnavailable is returned when a QR PNG cannot be stored because
// MinIO is unreachable.
var errStorageUnavailable = errors.New("object storage unavailable")

// Field bounds for properties and units (API.md).
const (
	propertyNameMax  = 120
	locationTextMax  = 200
	propertyNotesMax = 2000
	unitNameMax      = 60
)

// notFoundProperty / notFoundUnit keep cross-org probing indistinguishable
// from a missing row: the lookup is org-scoped, the answer is always 404.
func notFoundProperty(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such property")
}

func notFoundUnit(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such unit")
}

// orgProperty loads a property inside the caller's org, writing the 404 itself
// when there is no such row (including a malformed id).
func (s *Server) orgProperty(w http.ResponseWriter, r *http.Request, p auth.Principal) (sqlc.GetPropertyRow, bool) {
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundProperty(w)
		return sqlc.GetPropertyRow{}, false
	}
	row, err := s.q.GetProperty(r.Context(), sqlc.GetPropertyParams{OrgID: p.OrgID, ID: id})
	if isNoRows(err) {
		notFoundProperty(w)
		return sqlc.GetPropertyRow{}, false
	}
	if err != nil {
		s.serverError(w, r, "properties.get", err)
		return sqlc.GetPropertyRow{}, false
	}
	return row, true
}

// ------------------------------------------------------- POST /properties --

func (s *Server) handleCreateProperty(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		Name         string   `json:"name"`
		LocationText string   `json:"location_text"`
		Lat          *float64 `json:"lat"`
		Lng          *float64 `json:"lng"`
		Notes        *string  `json:"notes"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}

	f := validate.Fields{}
	name := f.MaxLen("name", f.Required("name", body.Name), propertyNameMax)
	location := f.MaxLen("location_text", body.LocationText, locationTextMax)
	if body.Lat != nil && (*body.Lat < -90 || *body.Lat > 90) {
		f.Add("lat", "must be between -90 and 90")
	}
	if body.Lng != nil && (*body.Lng < -180 || *body.Lng > 180) {
		f.Add("lng", "must be between -180 and 180")
	}
	if body.Notes != nil {
		f.MaxLen("notes", *body.Notes, propertyNotesMax)
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	var created sqlc.Property
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		created, err = q.CreateProperty(r.Context(), sqlc.CreatePropertyParams{
			OrgID: p.OrgID, Name: name, LocationText: location,
			Lat: body.Lat, Lng: body.Lng, Notes: body.Notes,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionPropertyCreate,
			EntityType:  audit.EntityProperty,
			EntityID:    db.UUIDString(created.ID),
			After:       toProperty(created, unitCounts{}),
		})
	}); err != nil {
		s.serverError(w, r, "properties.create.tx", err)
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"property": toProperty(created, unitCounts{})})
}

// -------------------------------------------------------- GET /properties --

func (s *Server) handleListProperties(w http.ResponseWriter, r *http.Request) {
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

	rows, err := s.q.ListProperties(r.Context(), sqlc.ListPropertiesParams{
		OrgID: p.OrgID, CursorAt: page.CursorAt, CursorID: page.CursorID, RowLimit: page.Limit,
	})
	if err != nil {
		s.serverError(w, r, "properties.list", err)
		return
	}
	items := make([]propertyResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, toPropertyRow(row))
	}
	var next *string
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		next = nextCursor(len(rows), page.Limit, last.CreatedAt.Time, db.UUIDString(last.ID))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) handleGetProperty(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.orgProperty(w, r, p)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"property": toGetPropertyRow(row)})
}

// ------------------------------------------------- PATCH /properties/{id} --

func (s *Server) handlePatchProperty(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	current, ok := s.orgProperty(w, r, p)
	if !ok {
		return
	}

	var body struct {
		Name         *string          `json:"name"`
		LocationText *string          `json:"location_text"`
		Lat          *json.RawMessage `json:"lat"`
		Lng          *json.RawMessage `json:"lng"`
		Notes        *json.RawMessage `json:"notes"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}

	f := validate.Fields{}
	params := sqlc.UpdatePropertyParams{OrgID: p.OrgID, ID: current.ID}
	if body.Name != nil {
		name := f.MaxLen("name", f.Required("name", *body.Name), propertyNameMax)
		params.Name = &name
	}
	if body.LocationText != nil {
		loc := f.MaxLen("location_text", *body.LocationText, locationTextMax)
		params.LocationText = &loc
	}
	params.SetLat, params.Lat = jsonNumberPtr(f, "lat", body.Lat, -90, 90)
	params.SetLng, params.Lng = jsonNumberPtr(f, "lng", body.Lng, -180, 180)
	params.SetNotes, params.Notes = jsonStringPtr(f, "notes", body.Notes, propertyNotesMax)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	var updated sqlc.Property
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		updated, err = q.UpdateProperty(r.Context(), params)
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionPropertyUpdate,
			EntityType:  audit.EntityProperty,
			EntityID:    db.UUIDString(updated.ID),
			Before:      toGetPropertyRow(current),
			After:       toProperty(updated, unitCounts{}),
		})
	}); err != nil {
		s.serverError(w, r, "properties.patch.tx", err)
		return
	}
	counts := unitCounts{
		Total: current.Total, Vacant: current.Vacant, Occupied: current.Occupied,
		Maintenance: current.Maintenance, Unlisted: current.Unlisted,
	}
	WriteJSON(w, http.StatusOK, map[string]any{"property": toProperty(updated, counts)})
}

// ------------------------------------------------ DELETE /properties/{id} --

func (s *Server) handleDeleteProperty(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	current, ok := s.orgProperty(w, r, p)
	if !ok {
		return
	}

	// A property whose units still carry a live tenancy cannot be removed:
	// the contract, not the listing, is the commitment (API.md → 409).
	blocking, err := s.q.CountBlockingContractsForProperty(r.Context(),
		sqlc.CountBlockingContractsForPropertyParams{OrgID: p.OrgID, PropertyID: current.ID})
	if err != nil {
		s.serverError(w, r, "properties.delete.contracts", err)
		return
	}
	if blocking > 0 {
		httpx.WriteProblem(w, http.StatusConflict, "property in use",
			"this property has units with an active or unsigned contract")
		return
	}

	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.SoftDeleteProperty(r.Context(), sqlc.SoftDeletePropertyParams{
			OrgID: p.OrgID, ID: current.ID,
		}); err != nil {
			return err
		}
		// Units of a deleted property go with it, otherwise their QR codes
		// would keep resolving publicly.
		if err := q.SoftDeleteUnitsOfProperty(r.Context(), sqlc.SoftDeleteUnitsOfPropertyParams{
			OrgID: p.OrgID, PropertyID: current.ID,
		}); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionPropertyDelete,
			EntityType:  audit.EntityProperty,
			EntityID:    db.UUIDString(current.ID),
			Before:      toGetPropertyRow(current),
		})
	}); err != nil {
		s.serverError(w, r, "properties.delete.tx", err)
		return
	}
	NoContent(w)
}

// ------------------------------------------- /properties/{id}/units (list) --

func (s *Server) handleListPropertyUnits(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	property, ok := s.orgProperty(w, r, p)
	if !ok {
		return
	}
	rows, err := s.q.ListUnits(r.Context(), sqlc.ListUnitsParams{
		OrgID: p.OrgID, PropertyID: property.ID, RowLimit: unitsPerPropertyLimit,
	})
	if err != nil {
		s.serverError(w, r, "properties.units.list", err)
		return
	}
	items := make([]unitResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, s.toUnit(unitRowOfList(row)))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ----------------------------------------- /properties/{id}/units (create) --

// unitPriceInput is the optional first price supplied with a new unit.
type unitPriceInput struct {
	Amount     int64 `json:"amount"`
	PeriodDays int32 `json:"period_days"`
}

// validate checks the inline price and applies the default 30-day basis.
func (in *unitPriceInput) validated(f validate.Fields, prefix string) *unitPriceInput {
	if in == nil {
		return nil
	}
	if in.Amount <= 0 {
		f.Add(prefix+".amount", "must be a whole number greater than 0")
	}
	if in.PeriodDays == 0 {
		in.PeriodDays = 30
	}
	if in.PeriodDays <= 0 {
		f.Add(prefix+".period_days", "must be a whole number greater than 0")
	}
	return in
}

func (s *Server) handleCreateUnit(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	property, ok := s.orgProperty(w, r, p)
	if !ok {
		return
	}

	var body struct {
		Name             string          `json:"name"`
		Price            *unitPriceInput `json:"price"`
		AllowedPeriodIDs []string        `json:"allowed_period_ids"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	name := f.MaxLen("name", f.Required("name", body.Name), unitNameMax)
	price := body.Price.validated(f, "price")
	allowed := s.validateAllowedPeriods(r, f, p, body.AllowedPeriodIDs)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	unit, err := s.createUnitTx(r, p, property.ID, name, price, allowed)
	if err != nil {
		s.serverError(w, r, "units.create.tx", err)
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"unit": unit})
}

func (s *Server) handleBulkCreateUnits(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	property, ok := s.orgProperty(w, r, p)
	if !ok {
		return
	}

	var body struct {
		Names            []string        `json:"names"`
		Price            *unitPriceInput `json:"price"`
		AllowedPeriodIDs []string        `json:"allowed_period_ids"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	if len(body.Names) == 0 || len(body.Names) > bulkUnitsMax {
		f.Add("names", "must contain between 1 and 200 unit names")
	}
	names := make([]string, 0, len(body.Names))
	for _, raw := range body.Names {
		names = append(names, f.MaxLen("names", f.Required("names", raw), unitNameMax))
	}
	price := body.Price.validated(f, "price")
	allowed := s.validateAllowedPeriods(r, f, p, body.AllowedPeriodIDs)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	items := make([]unitResponse, 0, len(names))
	for _, name := range names {
		unit, err := s.createUnitTx(r, p, property.ID, name, price, allowed)
		if err != nil {
			s.serverError(w, r, "units.bulk.tx", err)
			return
		}
		items = append(items, unit)
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"items": items})
}

// createUnitTx creates one unit (plus its optional first price) with its audit
// row, retrying once if the generated unit_code loses a race on the unique
// index.
func (s *Server) createUnitTx(r *http.Request, p auth.Principal, propertyID pgtype.UUID,
	name string, price *unitPriceInput, allowed []pgtype.UUID,
) (unitResponse, error) {
	var out unitResponse
	var lastErr error
	for attempt := 0; attempt < unitCodeAttempts; attempt++ {
		lastErr = s.inTx(r.Context(), func(q *sqlc.Queries) error {
			code, err := s.newUnitCode(r.Context(), q)
			if err != nil {
				return err
			}
			unit, err := q.CreateUnit(r.Context(), sqlc.CreateUnitParams{
				OrgID: p.OrgID, PropertyID: propertyID, Name: name,
				UnitCode: code, AllowedPeriodIds: allowed,
			})
			if err != nil {
				return err
			}
			if price != nil {
				if _, err := q.CreatePricePlan(r.Context(), sqlc.CreatePricePlanParams{
					OrgID: p.OrgID, UnitID: unit.ID, Amount: price.Amount, Currency: currencyTZS,
					PeriodDays: price.PeriodDays, EffectiveFrom: todayDate(),
					CreatedByUserID: p.UserID,
				}); err != nil {
					return err
				}
			}
			if err := audit.Record(r.Context(), q, audit.Entry{
				OrgID:       p.OrgIDString(),
				ActorUserID: p.UserIDString(),
				Action:      audit.ActionUnitCreate,
				EntityType:  audit.EntityUnit,
				EntityID:    db.UUIDString(unit.ID),
				After:       map[string]any{"name": unit.Name, "unit_code": unit.UnitCode, "property_id": db.UUIDString(propertyID)},
			}); err != nil {
				return err
			}
			row, err := q.GetUnit(r.Context(), sqlc.GetUnitParams{OrgID: p.OrgID, ID: unit.ID})
			if err != nil {
				return err
			}
			out = s.toUnit(unitRowOfGet(row))
			return nil
		})
		if lastErr == nil {
			return out, nil
		}
		if !isUnique(lastErr) {
			return unitResponse{}, lastErr
		}
	}
	return unitResponse{}, lastErr
}

// ----------------------------------------------- /properties/{id}/qr-sheet --

// qrSheetItem is one printable sticker on the per-property sheet.
type qrSheetItem struct {
	UnitID   string `json:"unit_id"`
	UnitName string `json:"unit_name"`
	UnitCode string `json:"unit_code"`
	ScanURL  string `json:"scan_url"`
	PNGURL   string `json:"png_url"`
}

func (s *Server) handleQRSheet(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	property, ok := s.orgProperty(w, r, p)
	if !ok {
		return
	}
	if s.deps.Storage == nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "storage unavailable",
			"QR codes cannot be generated right now")
		return
	}

	rows, err := s.q.ListUnits(r.Context(), sqlc.ListUnitsParams{
		OrgID: p.OrgID, PropertyID: property.ID, RowLimit: unitsPerPropertyLimit,
	})
	if err != nil {
		s.serverError(w, r, "qrsheet.units", err)
		return
	}

	items := make([]qrSheetItem, 0, len(rows))
	for _, row := range rows {
		unitID := db.UUIDString(row.ID)
		scanURL, pngURL, err := s.generateQR(r.Context(), p.OrgIDString(), unitID, row.UnitCode)
		if err != nil {
			s.serverError(w, r, "qrsheet.generate", err)
			return
		}
		items = append(items, qrSheetItem{
			UnitID: unitID, UnitName: row.Name, UnitCode: row.UnitCode,
			ScanURL: scanURL, PNGURL: pngURL,
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}
