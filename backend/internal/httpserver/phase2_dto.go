package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/validate"
)

// Pagination bounds shared by the Phase 2 list endpoints (API.md).
const (
	listDefaultLimit = 50
	listMaxLimit     = 200
	// unitsPerPropertyLimit caps the un-paginated nested listings
	// (GET /properties/{id}/units and the QR sheet).
	unitsPerPropertyLimit = 500
	// bulkUnitsMax is the cap on POST /properties/{id}/units/bulk.
	bulkUnitsMax = 200
)

// unit statuses.
const (
	statusVacant      = "vacant"
	statusOccupied    = "occupied"
	statusUnlisted    = "unlisted"
	statusMaintenance = "maintenance"
)

// dateLayout is the wire format for `effective_from` and other bare dates.
const dateLayout = "2006-01-02"

// ------------------------------------------------------------- responses --

// unitCounts is the per-property breakdown embedded in a property.
type unitCounts struct {
	Total       int64 `json:"total"`
	Vacant      int64 `json:"vacant"`
	Occupied    int64 `json:"occupied"`
	Maintenance int64 `json:"maintenance"`
	Unlisted    int64 `json:"unlisted"`
}

// propertyResponse is the `property` shape from API.md.
type propertyResponse struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	LocationText string     `json:"location_text"`
	Lat          *float64   `json:"lat"`
	Lng          *float64   `json:"lng"`
	Notes        *string    `json:"notes"`
	UnitCounts   unitCounts `json:"unit_counts"`
	CreatedAt    time.Time  `json:"created_at"`
}

// priceResponse is one price plan (`current_price` and the history rows).
type priceResponse struct {
	ID            string `json:"id"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	PeriodDays    int32  `json:"period_days"`
	EffectiveFrom string `json:"effective_from"`
}

// priceHistoryResponse adds the audit-ish fields the history endpoint shows.
type priceHistoryResponse struct {
	priceResponse
	CreatedAt     time.Time `json:"created_at"`
	CreatedByName string    `json:"created_by_name"`
}

// unitResponse is the `unit` shape from API.md.
type unitResponse struct {
	ID             string         `json:"id"`
	OrgID          string         `json:"org_id"`
	PropertyID     string         `json:"property_id"`
	PropertyName   string         `json:"property_name"`
	Name           string         `json:"name"`
	UnitCode       string         `json:"unit_code"`
	Status         string         `json:"status"`
	StatusOverride bool           `json:"status_override"`
	AllowedPeriods []string       `json:"allowed_period_ids"`
	CurrentPrice   *priceResponse `json:"current_price"`
	ScanURL        string         `json:"scan_url"`
	VacantSince    *time.Time     `json:"vacant_since"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// periodResponse is one row of GET /org/payment-periods.
type periodResponse struct {
	ID            string    `json:"id"`
	Label         string    `json:"label"`
	Days          int32     `json:"days"`
	IsRecommended bool      `json:"is_recommended"`
	SortOrder     int32     `json:"sort_order"`
	Active        bool      `json:"active"`
	CreatedAt     time.Time `json:"created_at"`
}

func toPeriod(p sqlc.PaymentPeriod) periodResponse {
	return periodResponse{
		ID:            db.UUIDString(p.ID),
		Label:         p.Label,
		Days:          p.Days,
		IsRecommended: p.IsRecommended,
		SortOrder:     p.SortOrder,
		Active:        p.Active,
		CreatedAt:     p.CreatedAt.Time,
	}
}

func toPeriods(rows []sqlc.PaymentPeriod) []periodResponse {
	out := make([]periodResponse, 0, len(rows))
	for _, p := range rows {
		out = append(out, toPeriod(p))
	}
	return out
}

func toProperty(p sqlc.Property, counts unitCounts) propertyResponse {
	return propertyResponse{
		ID:           db.UUIDString(p.ID),
		Name:         p.Name,
		LocationText: p.LocationText,
		Lat:          p.Lat,
		Lng:          p.Lng,
		Notes:        p.Notes,
		UnitCounts:   counts,
		CreatedAt:    p.CreatedAt.Time,
	}
}

func toPropertyRow(r sqlc.ListPropertiesRow) propertyResponse {
	return propertyResponse{
		ID:           db.UUIDString(r.ID),
		Name:         r.Name,
		LocationText: r.LocationText,
		Lat:          r.Lat,
		Lng:          r.Lng,
		Notes:        r.Notes,
		UnitCounts: unitCounts{
			Total: r.Total, Vacant: r.Vacant, Occupied: r.Occupied,
			Maintenance: r.Maintenance, Unlisted: r.Unlisted,
		},
		CreatedAt: r.CreatedAt.Time,
	}
}

func toGetPropertyRow(r sqlc.GetPropertyRow) propertyResponse {
	return toPropertyRow(sqlc.ListPropertiesRow(r))
}

// unitRow is the union of the ListUnits / GetUnit row shapes, so the response
// mapping lives in one place.
type unitRow struct {
	ID                 pgtype.UUID
	OrgID              pgtype.UUID
	PropertyID         pgtype.UUID
	Name               string
	UnitCode           string
	Status             string
	StatusOverride     bool
	AllowedPeriodIds   []pgtype.UUID
	CreatedAt          pgtype.Timestamptz
	UpdatedAt          pgtype.Timestamptz
	PropertyName       string
	PriceID            pgtype.UUID
	PriceAmount        int64
	PriceCurrency      string
	PricePeriodDays    int32
	PriceEffectiveFrom pgtype.Date
	VacantSince        pgtype.Timestamptz
}

func unitRowOfList(r sqlc.ListUnitsRow) unitRow { return unitRow(r) }
func unitRowOfGet(r sqlc.GetUnitRow) unitRow    { return unitRow(r) }
func (s *Server) scanURL(code string) string {
	return strings.TrimRight(s.cfg.AppBaseURL, "/") + "/enduser/u/" + code
}

func (s *Server) toUnit(r unitRow) unitResponse {
	u := unitResponse{
		ID:             db.UUIDString(r.ID),
		OrgID:          db.UUIDString(r.OrgID),
		PropertyID:     db.UUIDString(r.PropertyID),
		PropertyName:   r.PropertyName,
		Name:           r.Name,
		UnitCode:       r.UnitCode,
		Status:         r.Status,
		StatusOverride: r.StatusOverride,
		AllowedPeriods: uuidStrings(r.AllowedPeriodIds),
		ScanURL:        s.scanURL(r.UnitCode),
		CreatedAt:      r.CreatedAt.Time,
		UpdatedAt:      r.UpdatedAt.Time,
	}
	// PriceID is NULL exactly when the unit has no price effective today; the
	// amount/currency columns are COALESCEd and must not be read without it.
	if r.PriceID.Valid {
		u.CurrentPrice = &priceResponse{
			ID:            db.UUIDString(r.PriceID),
			Amount:        r.PriceAmount,
			Currency:      r.PriceCurrency,
			PeriodDays:    r.PricePeriodDays,
			EffectiveFrom: r.PriceEffectiveFrom.Time.Format(dateLayout),
		}
	}
	if r.VacantSince.Valid {
		t := r.VacantSince.Time
		u.VacantSince = &t
	}
	return u
}

// uuidStrings renders a UUID array column, preserving NULL as JSON null
// ("NULL = every org period is offered for this unit", SPEC §4).
func uuidStrings(ids []pgtype.UUID) []string {
	if ids == nil {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, db.UUIDString(id))
	}
	return out
}

// --------------------------------------------------------------- helpers --

// listPage holds the validated ?cursor=&limit= of a list endpoint.
type listPage struct {
	Limit    int32
	CursorAt pgtype.Timestamptz
	CursorID pgtype.UUID
}

// parseListPage validates the shared pagination query parameters.
func parseListPage(r *http.Request, f validate.Fields) listPage {
	page := listPage{Limit: listDefaultLimit}
	qs := r.URL.Query()
	if v := strings.TrimSpace(qs.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > listMaxLimit {
			f.Add("limit", "must be between 1 and "+strconv.Itoa(listMaxLimit))
		} else {
			page.Limit = int32(n)
		}
	}
	if v := strings.TrimSpace(qs.Get("cursor")); v != "" {
		at, id, ok := decodeCursor(v)
		if !ok {
			f.Add("cursor", "malformed cursor")
		} else {
			page.CursorAt = db.TS(at)
			page.CursorID = id
		}
	}
	return page
}

// nextCursor returns the cursor for the following page, or nil on the last one.
func nextCursor(rows int, limit int32, at time.Time, id string) *string {
	if rows == 0 || rows < int(limit) {
		return nil
	}
	c := encodeCursor(at, id)
	return &c
}

// optDate parses an optional YYYY-MM-DD field, defaulting to today.
func optDate(f validate.Fields, field string, in *string) pgtype.Date {
	if in == nil || strings.TrimSpace(*in) == "" {
		return pgtype.Date{Time: time.Now().UTC().Truncate(24 * time.Hour), Valid: true}
	}
	t, err := time.Parse(dateLayout, strings.TrimSpace(*in))
	if err != nil {
		f.Add(field, "must be a date (YYYY-MM-DD)")
		return pgtype.Date{}
	}
	return pgtype.Date{Time: t, Valid: true}
}

// parseUUIDList validates a list of UUID strings for a request field.
func parseUUIDList(f validate.Fields, field string, in []string) []pgtype.UUID {
	out := make([]pgtype.UUID, 0, len(in))
	for _, raw := range in {
		u, err := db.ParseUUID(strings.TrimSpace(raw))
		if err != nil {
			f.Add(field, "must contain only ids (UUID)")
			return nil
		}
		out = append(out, u)
	}
	return out
}

// jsonNumberPtr decodes an optional-and-nullable numeric patch field. The three
// states are: absent (raw == nil), explicit null (set, value nil) and a value.
func jsonNumberPtr(f validate.Fields, field string, raw *json.RawMessage, min, max float64) (set bool, val *float64) {
	if raw == nil {
		return false, nil
	}
	var v *float64
	if err := json.Unmarshal(*raw, &v); err != nil {
		f.Add(field, "must be a number or null")
		return false, nil
	}
	if v != nil && (*v < min || *v > max) {
		f.Add(field, fmt.Sprintf("must be between %g and %g", min, max))
		return false, nil
	}
	return true, v
}

// jsonStringPtr decodes an optional-and-nullable string patch field.
func jsonStringPtr(f validate.Fields, field string, raw *json.RawMessage, maxLen int) (set bool, val *string) {
	if raw == nil {
		return false, nil
	}
	var v *string
	if err := json.Unmarshal(*raw, &v); err != nil {
		f.Add(field, "must be a string or null")
		return false, nil
	}
	if v != nil && len([]rune(*v)) > maxLen {
		f.Add(field, fmt.Sprintf("must be at most %d characters", maxLen))
		return false, nil
	}
	return true, v
}
