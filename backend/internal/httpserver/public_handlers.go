package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/db"
	"tms/backend/internal/httpx"
	"tms/backend/internal/storage"
)

// The public surface is unauthenticated (a QR sticker is the only credential),
// so it is rate limited per IP (SPEC §8).
const (
	publicLimit  = 60
	publicWindow = time.Minute
)

// orgTheme is the branding theme block (packages/ui applies it client-side).
type orgTheme struct {
	PrimaryColor string `json:"primary_color"`
	FontID       string `json:"font_id"`
}

func defaultTheme() orgTheme { return orgTheme{PrimaryColor: "#1B4DB1", FontID: "bricolage"} }

func parseTheme(raw []byte) orgTheme {
	t := defaultTheme()
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &t)
	}
	return t
}

// publicOrg is the compact org identity shown pre-auth.
type publicOrg struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// publicBranding is the branding block of both public endpoints.
type publicBranding struct {
	DisplayName string   `json:"display_name"`
	LogoURL     *string  `json:"logo_url"`
	Theme       orgTheme `json:"theme"`
}

// publicRateLimited wraps the public routes in the per-IP fixed window.
func (s *Server) publicRateLimited(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := audit.RequestInfoFrom(r.Context()).IP
		if res := s.limiter.Allow(r.Context(), "public:"+ip, publicLimit, publicWindow); !res.Allowed {
			tooMany(w, res, "too many requests from this address")
			return
		}
		next(w, r)
	}
}

// brandingFor loads an org's branding, tolerating a missing row (an org
// created before branding existed still resolves, with defaults).
func (s *Server) brandingFor(ctx context.Context, orgID pgtype.UUID, fallbackName string) publicBranding {
	out := publicBranding{DisplayName: fallbackName, Theme: defaultTheme()}
	row, err := s.q.GetOrgBranding(ctx, orgID)
	if err != nil {
		return out
	}
	out.DisplayName = row.DisplayName
	out.Theme = parseTheme(row.Theme)
	if row.LogoObjectKey != nil && *row.LogoObjectKey != "" && s.deps.Storage != nil {
		if url, err := s.deps.Storage.PresignGet(ctx, storage.BucketBranding, *row.LogoObjectKey, storage.DefaultPresignTTL); err == nil {
			out.LogoURL = &url
		}
	}
	return out
}

// ------------------------------------ GET /public/orgs/{slug}/branding --

func (s *Server) handlePublicBranding(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	org, err := s.q.GetOrgBySlug(r.Context(), slug)
	if isNoRows(err) || (err == nil && org.Status != "active") {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such organisation")
		return
	}
	if err != nil {
		s.serverError(w, r, "public.branding", err)
		return
	}
	b := s.brandingFor(r.Context(), org.ID, org.Name)
	WriteJSON(w, http.StatusOK, map[string]any{
		"org":          publicOrg{ID: db.UUIDString(org.ID), Name: org.Name, Slug: org.Slug},
		"display_name": b.DisplayName,
		"logo_url":     b.LogoURL,
		"theme":        b.Theme,
	})
}

// ----------------------------------------- GET /public/units/{unit_code} --

// publicPeriod is one offered payment period with its prorated amount.
type publicPeriod struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Days          int32  `json:"days"`
	IsRecommended bool   `json:"is_recommended"`
	Amount        *int64 `json:"amount"`
}

func (s *Server) handlePublicUnit(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	code := strings.ToUpper(strings.TrimSpace(chi.URLParam(r, "unit_code")))
	notFound := func() {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such unit")
	}

	unit, err := s.q.GetUnitByCode(r.Context(), code)
	if isNoRows(err) {
		notFound()
		return
	}
	if err != nil {
		s.serverError(w, r, "public.unit", err)
		return
	}
	// An unlisted unit is deliberately invisible to the public: its sticker
	// must read as an unknown code, not as "exists but hidden" (API.md).
	if unit.Status == statusUnlisted {
		notFound()
		return
	}

	org, err := s.q.GetOrg(r.Context(), unit.OrgID)
	if isNoRows(err) || (err == nil && org.Status != "active") {
		notFound()
		return
	}
	if err != nil {
		s.serverError(w, r, "public.unit.org", err)
		return
	}

	periods, err := s.q.ListActivePaymentPeriods(r.Context(), unit.OrgID)
	if err != nil {
		s.serverError(w, r, "public.unit.periods", err)
		return
	}

	allowed := allowedSet(unit.AllowedPeriodIds)
	items := make([]publicPeriod, 0, len(periods))
	for _, period := range periods {
		if allowed != nil && !allowed[db.UUIDString(period.ID)] {
			continue
		}
		item := publicPeriod{
			ID:            db.UUIDString(period.ID),
			Label:         period.Label,
			Days:          period.Days,
			IsRecommended: period.IsRecommended,
		}
		if unit.PriceID.Valid && unit.PricePeriodDays > 0 {
			amount := prorate(unit.PriceAmount, period.Days, unit.PricePeriodDays)
			item.Amount = &amount
		}
		items = append(items, item)
	}

	var price any
	if unit.PriceID.Valid {
		price = map[string]any{
			"amount": unit.PriceAmount, "currency": unit.PriceCurrency, "period_days": unit.PricePeriodDays,
		}
	}
	b := s.brandingFor(r.Context(), unit.OrgID, org.Name)

	WriteJSON(w, http.StatusOK, map[string]any{
		"org":      publicOrg{ID: db.UUIDString(org.ID), Name: org.Name, Slug: org.Slug},
		"branding": b,
		"property": map[string]any{"name": unit.PropertyName, "location_text": unit.PropertyLocationText},
		"unit":     map[string]any{"id": db.UUIDString(unit.ID), "name": unit.Name, "status": unit.Status},
		"price":    price,
		"periods":  items,
		// An occupied unit still resolves — the renter sees "unit occupied —
		// contact landlord" rather than a dead sticker (FLOWS flow 2).
		"occupied": unit.Status == statusOccupied,
	})
}

// allowedSet turns a unit's allowed_period_ids into a lookup set; nil means
// "no restriction" (SPEC §4: NULL = every org period is offered).
func allowedSet(ids []pgtype.UUID) map[string]bool {
	if ids == nil {
		return nil
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[db.UUIDString(id)] = true
	}
	return out
}

// prorate scales a price to a different period length, rounding half away from
// zero to whole TZS (SPEC §4).
func prorate(amount int64, days, basisDays int32) int64 {
	if basisDays <= 0 {
		return 0
	}
	num := amount*int64(days)*2 + int64(basisDays)
	return num / (int64(basisDays) * 2)
}
