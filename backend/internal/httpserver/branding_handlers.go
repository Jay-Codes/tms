package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/storage"
	"tms/backend/internal/validate"
)

// Branding upload limits (API.md). The read TTL is an hour rather than the
// 15-minute default: a contract document is read, printed and re-read, and a
// letterhead that expires mid-print is worse than one that lives an hour.
const (
	brandingUploadTTL = 10 * time.Minute
	brandingReadTTL   = time.Hour
)

// brandingFonts is the whitelist from API.md — the four faces packages/ui ships.
// An arbitrary font id would be a name the frontend cannot resolve, so it is
// refused at the edge rather than stored and silently ignored.
//
//nolint:gochecknoglobals // fixed vocabulary, read-only.
var brandingFonts = []string{"bricolage", "archivo", "instrument", "hanken"}

// hexColor is a 6-digit CSS hex colour. Shorthand (#abc) and named colours are
// refused: the theme is injected into CSS custom properties, and one canonical
// form keeps that substitution trivially safe.
var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// brandingContentTypes are the accepted image formats and their extensions.
//
//nolint:gochecknoglobals // fixed vocabulary, read-only.
var brandingContentTypes = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpg",
}

// brandingAssetSet is the resolved branding a document or preview renders with:
// display name, presigned image URLs and the footer line.
type brandingAssetSet struct {
	DisplayName   string
	LogoURL       *string
	LetterheadURL *string
	FooterText    *string
	Theme         orgTheme
}

// brandingAssets loads an org's branding and presigns whatever images it has.
// A missing row or an unreachable MinIO degrades to name + defaults rather than
// failing the document: the terms are the contract, the letterhead is dressing.
func (s *Server) brandingAssets(ctx context.Context, orgID pgtype.UUID, fallbackName string) brandingAssetSet {
	out := brandingAssetSet{DisplayName: fallbackName, Theme: defaultTheme()}
	row, err := s.q.GetOrgBranding(ctx, orgID)
	if err != nil {
		return out
	}
	if row.DisplayName != "" {
		out.DisplayName = row.DisplayName
	}
	out.Theme = parseTheme(row.Theme)
	out.FooterText = row.DocumentFooterText
	out.LogoURL = s.presignBranding(ctx, row.LogoObjectKey)
	out.LetterheadURL = s.presignBranding(ctx, row.LetterheadObjectKey)
	return out
}

func (s *Server) presignBranding(ctx context.Context, key *string) *string {
	if key == nil || *key == "" || s.deps.Storage == nil {
		return nil
	}
	url, err := s.deps.Storage.PresignGet(ctx, storage.BucketBranding, *key, brandingReadTTL)
	if err != nil {
		s.logger.Warn("branding presign failed", "object_key", *key, "error", err)
		return nil
	}
	return &url
}

// toBranding renders the GET/PUT /org/branding body.
func (s *Server) toBranding(ctx context.Context, row sqlc.OrgBranding) brandingResponse {
	prefs := map[string]any{}
	if len(row.DashboardPrefs) > 0 {
		_ = json.Unmarshal(row.DashboardPrefs, &prefs)
	}
	return brandingResponse{
		DisplayName:        row.DisplayName,
		LogoURL:            s.presignBranding(ctx, row.LogoObjectKey),
		LetterheadURL:      s.presignBranding(ctx, row.LetterheadObjectKey),
		Theme:              parseTheme(row.Theme),
		DashboardPrefs:     prefs,
		DocumentFooterText: row.DocumentFooterText,
	}
}

// ------------------------------------------------------- GET /org/branding --

func (s *Server) handleGetBranding(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, err := s.q.GetOrgBranding(r.Context(), p.OrgID)
	if isNoRows(err) {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "this organisation has no branding record")
		return
	}
	if err != nil {
		s.serverError(w, r, "branding.get", err)
		return
	}
	WriteJSON(w, http.StatusOK, s.toBranding(r.Context(), row))
}

// ------------------------------------------------------- PUT /org/branding --

func (s *Server) handlePutBranding(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		DisplayName *string `json:"display_name"`
		Theme       *struct {
			PrimaryColor string `json:"primary_color"`
			FontID       string `json:"font_id"`
		} `json:"theme"`
		DashboardPrefs     map[string]any `json:"dashboard_prefs"`
		DocumentFooterText *string        `json:"document_footer_text"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}

	f := validate.Fields{}
	params := sqlc.UpdateOrgBrandingParams{OrgID: p.OrgID}
	if body.DisplayName != nil {
		v := f.MaxLen("display_name", f.Required("display_name", *body.DisplayName), 120)
		params.DisplayName = &v
	}
	if body.Theme != nil {
		colour := strings.TrimSpace(body.Theme.PrimaryColor)
		if !hexColor.MatchString(colour) {
			f.Add("theme.primary_color", "must be a hex colour like #1B4DB1")
		}
		font := f.OneOf("theme.font_id", strings.ToLower(strings.TrimSpace(body.Theme.FontID)), brandingFonts...)
		if f.Empty() {
			raw, err := json.Marshal(orgTheme{PrimaryColor: colour, FontID: font})
			if err != nil {
				s.serverError(w, r, "branding.theme", err)
				return
			}
			params.Theme = raw
		}
	}
	if body.DashboardPrefs != nil {
		raw, err := json.Marshal(body.DashboardPrefs)
		if err != nil {
			f.Add("dashboard_prefs", "must be a JSON object")
		} else {
			params.DashboardPrefs = raw
		}
	}
	if body.DocumentFooterText != nil {
		// An explicit null clears the footer; a string sets it. The query needs
		// to be told which, because COALESCE cannot distinguish the two.
		params.SetFooter = true
		if v := strings.TrimSpace(*body.DocumentFooterText); v != "" {
			footer := f.MaxLen("document_footer_text", v, footerTextMax)
			params.DocumentFooterText = &footer
		}
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	var updated sqlc.OrgBranding
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		updated, err = q.UpdateOrgBranding(r.Context(), params)
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionBrandingUpdate,
			EntityType:  audit.EntityOrgBranding,
			EntityID:    p.OrgIDString(),
			After: map[string]any{
				"display_name": updated.DisplayName,
				"theme":        json.RawMessage(updated.Theme),
				"footer_set":   params.SetFooter,
			},
		})
	}); err != nil {
		if isNoRows(err) {
			httpx.WriteProblem(w, http.StatusNotFound, "not found", "this organisation has no branding record")
			return
		}
		s.serverError(w, r, "branding.put.tx", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"branding": s.toBranding(r.Context(), updated)})
}

// --------------------------------- POST /org/branding/{logo|letterhead} ... --

// brandingAsset names the column a request addresses. The route segment is the
// only place the two paths differ, so the handlers are shared.
func brandingAssetOf(r *http.Request) string {
	if strings.Contains(r.URL.Path, "/letterhead") {
		return "letterhead"
	}
	return "logo"
}

func (s *Server) handleBrandingUpload(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	if s.deps.Storage == nil {
		brandingStorageUnavailable(w)
		return
	}
	which := brandingAssetOf(r)

	var body struct {
		ContentType string `json:"content_type"`
		SizeBytes   int64  `json:"size_bytes"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	contentType := strings.ToLower(strings.TrimSpace(body.ContentType))
	ext, ok := brandingContentTypes[contentType]
	if !ok {
		f.Add("content_type", "must be image/png or image/jpeg")
	}
	if body.SizeBytes <= 0 || body.SizeBytes > brandingImageMaxBytes {
		f.Add("size_bytes", "must be between 1 byte and 2 MiB")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	// The key is fixed per org and asset, so a re-upload replaces the image and
	// nothing accumulates in the bucket.
	objectKey := p.OrgIDString() + "/" + which + "." + ext
	url, err := s.deps.Storage.PresignPut(r.Context(), storage.BucketBranding, objectKey, brandingUploadTTL)
	if err != nil {
		s.logger.Error("branding presign put failed", "error", err)
		brandingStorageUnavailable(w)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"upload_url": url,
		"object_key": objectKey,
		"headers":    map[string]string{"Content-Type": contentType},
	})
}

func (s *Server) handleBrandingUploadComplete(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	if s.deps.Storage == nil {
		brandingStorageUnavailable(w)
		return
	}
	which := brandingAssetOf(r)

	var body struct {
		ObjectKey string `json:"object_key"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	objectKey := f.Required("object_key", body.ObjectKey)
	// The key check is the authorisation: an org may only claim an object under
	// its own prefix, with the exact shape handleBrandingUpload issues.
	if objectKey != "" && !isOwnBrandingKey(objectKey, p.OrgIDString(), which) {
		f.Add("object_key", "must be an upload issued to you")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	// A presigned PUT enforces neither type nor size, so both are checked
	// against what actually landed (SPEC §7).
	info, err := s.deps.Storage.Stat(r.Context(), storage.BucketBranding, objectKey)
	if err != nil {
		httpx.WriteProblemFields(w, http.StatusBadRequest, "upload not found",
			"no uploaded image was found for that key",
			map[string]string{"object_key": "no object has been uploaded under this key"})
		return
	}
	reject := func(field, msg string) {
		if rmErr := s.deps.Storage.Remove(r.Context(), storage.BucketBranding, objectKey); rmErr != nil {
			s.logger.Warn("could not remove rejected branding upload", "object_key", objectKey, "error", rmErr)
		}
		httpx.WriteProblemFields(w, http.StatusBadRequest, "invalid image",
			"the uploaded image was rejected", map[string]string{field: msg})
	}
	if info.Size > brandingImageMaxBytes {
		reject("size_bytes", "must be at most 2 MiB")
		return
	}
	if _, ok := brandingContentTypes[strings.ToLower(strings.TrimSpace(info.ContentType))]; !ok {
		reject("content_type", "must be image/png or image/jpeg")
		return
	}

	s.writeBrandingAsset(w, r, p, which, &objectKey)
}

func (s *Server) handleBrandingDelete(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	which := brandingAssetOf(r)

	// The object goes with the reference: a logo nothing points at is dead
	// weight in the bucket. A failed removal is logged, not fatal — the row is
	// the source of truth for what the document shows.
	if s.deps.Storage != nil {
		if row, err := s.q.GetOrgBranding(r.Context(), p.OrgID); err == nil {
			key := row.LogoObjectKey
			if which == "letterhead" {
				key = row.LetterheadObjectKey
			}
			if key != nil && *key != "" {
				if err := s.deps.Storage.Remove(r.Context(), storage.BucketBranding, *key); err != nil {
					s.logger.Warn("could not remove branding image", "object_key", *key, "error", err)
				}
			}
		}
	}
	s.writeBrandingAsset(w, r, p, which, nil)
}

// writeBrandingAsset sets (or clears) one image key and answers with the row.
func (s *Server) writeBrandingAsset(
	w http.ResponseWriter, r *http.Request, p auth.Principal, which string, objectKey *string,
) {
	var updated sqlc.OrgBranding
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		updated, err = q.SetBrandingAsset(r.Context(), sqlc.SetBrandingAssetParams{
			Which: which, ObjectKey: objectKey, OrgID: p.OrgID,
		})
		if err != nil {
			return err
		}
		after := map[string]any{"asset": which, "object_key": nil}
		if objectKey != nil {
			after["object_key"] = *objectKey
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionBrandingAsset,
			EntityType:  audit.EntityOrgBranding,
			EntityID:    p.OrgIDString(),
			After:       after,
		})
	}); err != nil {
		if isNoRows(err) {
			httpx.WriteProblem(w, http.StatusNotFound, "not found", "this organisation has no branding record")
			return
		}
		s.serverError(w, r, "branding.asset.tx", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"branding": s.toBranding(r.Context(), updated)})
}

// brandingKeyPattern is the shape handleBrandingUpload issues: one org prefix,
// the asset name, one image extension. Pinning the whole shape (not just the
// prefix) is what stops `{me}/../{other org}/logo.png` from resolving into
// another org's object once a path-normalising hop sees it.
var brandingKeyPattern = regexp.MustCompile(`^[^/]+/(logo|letterhead)\.(png|jpg|jpeg)$`)

func isOwnBrandingKey(key, orgID, which string) bool {
	m := brandingKeyPattern.FindStringSubmatch(key)
	return m != nil && m[1] == which && strings.HasPrefix(key, orgID+"/")
}

func brandingStorageUnavailable(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusServiceUnavailable, "storage unavailable",
		"branding images cannot be uploaded or read right now")
}
