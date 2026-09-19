package httpserver

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/notify"
	"tms/backend/internal/validate"
)

// platformBodyMax is the longest platform template body an admin may save.
//
// It is larger than the org's 320 (notify.BodyMaxLen) because the platform's
// own wording is the one that has to work in both languages for every tenant:
// Swahili runs longer than English for the same sentence, and a three-segment
// ceiling is where the cost of a message starts to be worth arguing about.
const platformBodyMax = 480

// platformTemplateRow is one kind as GET /admin/templates reports it.
type platformTemplateRow struct {
	Kind      string         `json:"kind"`
	SW        string         `json:"sw"`
	EN        string         `json:"en"`
	Variables []string       `json:"variables"`
	Locked    bool           `json:"locked"`
	Version   int32          `json:"version"`
	UpdatedBy string         `json:"updated_by"`
	UpdatedAt time.Time      `json:"updated_at"`
	Segments  map[string]int `json:"segments"`
}

func toPlatformTemplateRow(kind, sw, en string, vars []string, locked bool,
	version int32, updatedBy string, updatedAt time.Time,
) platformTemplateRow {
	if vars == nil {
		vars = []string{}
	}
	return platformTemplateRow{
		Kind: kind, SW: sw, EN: en, Variables: vars, Locked: locked,
		Version: version, UpdatedBy: updatedBy, UpdatedAt: updatedAt,
		Segments: notify.SegmentsPair(sw, en),
	}
}

// adminTemplateEditAllowed meters the three write paths on the platform
// catalogue — save, lock and revert — against one counter per admin.
//
// They share a bucket on purpose: what the limit protects is the wording every
// tenant's SMS is rendered from, and a caller who cannot rewrite a kind sixty
// times an hour should not be able to lock and revert it sixty more.
func (s *Server) adminTemplateEditAllowed(w http.ResponseWriter, r *http.Request) bool {
	p := auth.MustFromContext(r.Context())
	if res := s.limiter.Allow(r.Context(), "admin:template:edit:"+p.UserIDString(),
		adminTemplateEditLimit, adminTemplateEditWindow); !res.Allowed {
		tooMany(w, res, "too many template changes; try again shortly")
		return false
	}
	return true
}

// ------------------------------------------------------ GET /admin/templates --

func (s *Server) handleAdminListTemplates(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	rows, err := s.q.ListPlatformTemplates(r.Context())
	if err != nil {
		s.serverError(w, r, "admin.templates.list", err)
		return
	}
	items := make([]platformTemplateRow, 0, len(rows))
	for _, row := range rows {
		items = append(items, toPlatformTemplateRow(row.Kind, row.Sw, row.En,
			row.Variables, row.Locked, row.Version, row.UpdatedBy, row.UpdatedAt.Time))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ----------------------------------------------- PUT /admin/templates/{kind} --

func (s *Server) handleAdminPutTemplate(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	if !s.adminTemplateEditAllowed(w, r) {
		return
	}
	p := auth.MustFromContext(r.Context())
	kind, current, ok := s.platformTemplate(w, r)
	if !ok {
		return
	}

	var body struct {
		SW string `json:"sw"`
		EN string `json:"en"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	sw := strings.TrimSpace(body.SW)
	en := strings.TrimSpace(body.EN)
	// Both languages are required. A kind with wording in one of them only
	// would send half the platform's renters a blank message, and there is no
	// sensible fallback for a template the admin is in the middle of writing.
	checkPlatformBody(f, "sw", sw, current.Variables)
	checkPlatformBody(f, "en", en, current.Variables)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	var updated sqlc.PlatformTemplate
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		// The version row carries the body being replaced, so history reads
		// "version N said this" rather than "version N+1 replaced something".
		if err := q.InsertPlatformTemplateVersion(r.Context(), sqlc.InsertPlatformTemplateVersionParams{
			Kind: kind, Version: current.Version, Sw: current.Sw, En: current.En,
		}); err != nil {
			return err
		}
		var err error
		updated, err = q.UpdatePlatformTemplate(r.Context(), sqlc.UpdatePlatformTemplateParams{
			Kind: kind, Sw: sw, En: en, AdminUserID: p.UserID,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionPlatformTemplateUpdate,
			EntityType:  audit.EntityPlatformTemplate,
			Before:      map[string]any{"kind": kind, "version": current.Version, "sw": current.Sw, "en": current.En},
			After:       map[string]any{"kind": kind, "version": updated.Version, "sw": sw, "en": en},
		})
	}); err != nil {
		s.serverError(w, r, "admin.templates.put", err)
		return
	}
	s.invalidateTemplate(r, kind)

	out := s.templateRow(r, kind, updated)
	resp := map[string]any{"template": out, "segments": out.Segments}
	// A body over three segments is not refused — a landlord's own notice may
	// legitimately be long — but the admin is told what it will cost, because
	// the price of a platform template is paid by every tenant.
	if warns := segmentWarnings(out.Segments); len(warns) > 0 {
		resp["warnings"] = warns
	}
	WriteJSON(w, http.StatusOK, resp)
}

// --------------------------------------------- PATCH /admin/templates/{kind} --

func (s *Server) handleAdminPatchTemplate(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	if !s.adminTemplateEditAllowed(w, r) {
		return
	}
	p := auth.MustFromContext(r.Context())
	kind, current, ok := s.platformTemplate(w, r)
	if !ok {
		return
	}

	var body struct {
		Locked *bool `json:"locked"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	if body.Locked == nil {
		f := validate.Fields{}
		f.Add("locked", "locked is required")
		badRequest(w, f)
		return
	}

	var updated sqlc.PlatformTemplate
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		updated, err = q.SetPlatformTemplateLocked(r.Context(), sqlc.SetPlatformTemplateLockedParams{
			Kind: kind, Locked: *body.Locked, AdminUserID: p.UserID,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionPlatformTemplateLock,
			EntityType:  audit.EntityPlatformTemplate,
			Before:      map[string]any{"kind": kind, "locked": current.Locked},
			After:       map[string]any{"kind": kind, "locked": updated.Locked},
		})
	}); err != nil {
		s.serverError(w, r, "admin.templates.lock", err)
		return
	}
	s.invalidateTemplate(r, kind)
	WriteJSON(w, http.StatusOK, map[string]any{"template": s.templateRow(r, kind, updated)})
}

// --------------------------------------- POST /admin/templates/{kind}/preview --

// handleAdminPreviewTemplate renders the stored wording with sample values, so
// an admin sees the sentence a renter will read — including what it costs —
// before anybody is texted.
func (s *Server) handleAdminPreviewTemplate(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	kind, current, ok := s.platformTemplate(w, r)
	if !ok {
		return
	}

	var body struct {
		Language string            `json:"language"`
		Sample   map[string]string `json:"sample"`
		// SW / EN preview wording that has not been saved yet, so the editor
		// can show the sentence as it is typed.
		SW *string `json:"sw"`
		EN *string `json:"en"`
	}
	if r.ContentLength > 0 && !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	lang := notify.LangSwahili
	if v := strings.TrimSpace(body.Language); v != "" {
		lang = f.OneOf("language", v, notify.LangSwahili, notify.LangEnglish)
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	tpl := notify.Template{SW: current.Sw, EN: current.En}
	if body.SW != nil {
		tpl.SW = strings.TrimSpace(*body.SW)
	}
	if body.EN != nil {
		tpl.EN = strings.TrimSpace(*body.EN)
	}
	rendered := notify.Substitute(tpl.Pick(lang), previewVars(body.Sample))
	WriteJSON(w, http.StatusOK, map[string]any{
		"kind": kind, "language": lang, "body": rendered,
		"segments": notify.Segments(rendered),
		"encoding": notify.Encoding(rendered),
	})
}

// -------------------------------------- GET /admin/templates/{kind}/versions --

func (s *Server) handleAdminTemplateVersions(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	kind, _, ok := s.platformTemplate(w, r)
	if !ok {
		return
	}
	rows, err := s.q.ListPlatformTemplateVersions(r.Context(), kind)
	if err != nil {
		s.serverError(w, r, "admin.templates.versions", err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{
			"version": row.Version, "sw": row.Sw, "en": row.En,
			"admin_name": row.AdminName, "created_at": row.CreatedAt.Time,
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ---------------------------------------- POST /admin/templates/{kind}/revert --

// handleAdminRevertTemplate restores an old wording as a *new* version rather
// than by rewinding the counter. History is append-only for the same reason
// the ledger is: "we went back to what version 2 said" is a fact worth
// keeping, and a version number that can go backwards is one nobody can cite.
func (s *Server) handleAdminRevertTemplate(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	if !s.adminTemplateEditAllowed(w, r) {
		return
	}
	p := auth.MustFromContext(r.Context())
	kind, current, ok := s.platformTemplate(w, r)
	if !ok {
		return
	}

	var body struct {
		Version *int `json:"version"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	if body.Version == nil || *body.Version < 1 {
		f := validate.Fields{}
		f.Add("version", "must be a version number")
		badRequest(w, f)
		return
	}

	target, err := s.q.GetPlatformTemplateVersion(r.Context(), sqlc.GetPlatformTemplateVersionParams{
		Kind: kind, Version: int32(*body.Version),
	})
	if isNoRows(err) {
		httpx.WriteProblem(w, http.StatusNotFound, "not found",
			"no such version of this template")
		return
	}
	if err != nil {
		s.serverError(w, r, "admin.templates.revert.get", err)
		return
	}

	var updated sqlc.PlatformTemplate
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if err := q.InsertPlatformTemplateVersion(r.Context(), sqlc.InsertPlatformTemplateVersionParams{
			Kind: kind, Version: current.Version, Sw: current.Sw, En: current.En,
		}); err != nil {
			return err
		}
		var err error
		updated, err = q.UpdatePlatformTemplate(r.Context(), sqlc.UpdatePlatformTemplateParams{
			Kind: kind, Sw: target.Sw, En: target.En, AdminUserID: p.UserID,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionPlatformTemplateRevert,
			EntityType:  audit.EntityPlatformTemplate,
			Before:      map[string]any{"kind": kind, "version": current.Version},
			After: map[string]any{
				"kind": kind, "version": updated.Version, "restored_from": target.Version,
			},
		})
	}); err != nil {
		s.serverError(w, r, "admin.templates.revert", err)
		return
	}
	s.invalidateTemplate(r, kind)
	WriteJSON(w, http.StatusOK, map[string]any{
		"template":      s.templateRow(r, kind, updated),
		"restored_from": target.Version,
	})
}

// ------------------------------------------------------------------ shared --

// platformTemplate reads {kind} and loads the row. An unknown kind is a 404:
// the caller is naming a template, and one the catalogue does not carry does
// not exist.
func (s *Server) platformTemplate(w http.ResponseWriter, r *http.Request) (string, sqlc.GetPlatformTemplateRow, bool) {
	kind := strings.TrimSpace(chi.URLParam(r, "kind"))
	row, err := s.q.GetPlatformTemplate(r.Context(), kind)
	if isNoRows(err) {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such notification template")
		return "", sqlc.GetPlatformTemplateRow{}, false
	}
	if err != nil {
		s.serverError(w, r, "admin.templates.get", err)
		return "", sqlc.GetPlatformTemplateRow{}, false
	}
	return kind, row, true
}

// templateRow renders the saved template, re-reading it so the editor's name
// on the row is the one the database resolved rather than one the handler
// guessed at.
func (s *Server) templateRow(r *http.Request, kind string, updated sqlc.PlatformTemplate) platformTemplateRow {
	if row, err := s.q.GetPlatformTemplate(r.Context(), kind); err == nil {
		return toPlatformTemplateRow(row.Kind, row.Sw, row.En, row.Variables,
			row.Locked, row.Version, row.UpdatedBy, row.UpdatedAt.Time)
	}
	return toPlatformTemplateRow(updated.Kind, updated.Sw, updated.En, updated.Variables,
		updated.Locked, updated.Version, "", updated.UpdatedAt.Time)
}

// invalidateTemplate drops the saved kind from the Redis and in-process caches
// so the next render reads the new wording rather than waiting out the TTL.
func (s *Server) invalidateTemplate(r *http.Request, kind string) {
	if s.templates != nil {
		s.templates.Invalidate(r.Context(), kind)
	}
}

// checkPlatformBody applies the platform template rules to one language.
func checkPlatformBody(f validate.Fields, field, body string, allowed []string) {
	if body == "" {
		f.Add(field, field+" is required")
		return
	}
	if len([]rune(body)) > platformBodyMax {
		f.Add(field, "must be at most "+strconv.Itoa(platformBodyMax)+" characters")
	}
	if notify.HasControlChars(body) {
		f.Add(field, "must not contain control characters")
	}
	if unknown := notify.UnknownVariables(body, allowed); len(unknown) > 0 {
		f.Add(field, "unknown variables: {{"+strings.Join(unknown, "}}, {{")+"}}")
	}
}

// segmentWarnings names a language whose wording runs past three segments.
func segmentWarnings(segments map[string]int) []string {
	var out []string
	for _, lang := range []string{notify.LangSwahili, notify.LangEnglish} {
		if n := segments[lang]; n > 3 {
			out = append(out, lang+": "+strconv.Itoa(n)+" segments — each send of this "+
				"message costs "+strconv.Itoa(n)+" credits")
		}
	}
	return out
}

// previewVars fills the preview with the caller's sample values, falling back
// to a representative Tanzanian tenancy for anything they did not supply.
func previewVars(sample map[string]string) notify.Vars {
	get := func(key, fallback string) string {
		if v, ok := sample[key]; ok && strings.TrimSpace(v) != "" {
			return v
		}
		return fallback
	}
	return notify.Vars{
		Name:        get("name", "Asha Mwinyi"),
		Amount:      get("amount", "TZS 250,000"),
		DueDate:     get("due_date", "2026-10-01"),
		Property:    get("property", "Mbezi Beach Block A"),
		Unit:        get("unit", "A-12"),
		Org:         get("org", "JJnE Properties"),
		NextDueDate: get("next_due_date", "2026-11-01"),
		Link:        get("link", "https://tms.example/c/AB12CD"),
		// Phase 16 §16.4: the preview resolves `{{pay_link}}` against the
		// platform's own origin, so an admin sees the link a renter will get
		// rather than the placeholder.
		PayLink:    get("pay_link", "https://tms.example/enduser/payments"),
		Reason:     get("reason", "unit no longer available"),
		StartDate:  get("start_date", "2026-10-01"),
		NextAmount: get("next_amount", "TZS 250,000"),
		Code:       get("code", "123456"),
	}
}
