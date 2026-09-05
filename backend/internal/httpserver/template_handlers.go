package httpserver

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/contract"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/validate"
)

// notFoundTemplate keeps another org's template indistinguishable from one that
// never existed (API.md: cross-org is 404, never 403).
func notFoundTemplate(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such contract template")
}

// ----------------------------------------------------- GET /contract-templates --

func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	rows, err := s.q.ListContractTemplates(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "template.list", err)
		return
	}
	items := make([]templateResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, toTemplateSummary(row))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// orgTemplate loads one template inside the caller's org (404 otherwise).
func (s *Server) orgTemplate(w http.ResponseWriter, r *http.Request, p auth.Principal) (sqlc.ContractTemplate, bool) {
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundTemplate(w)
		return sqlc.ContractTemplate{}, false
	}
	row, err := s.q.GetContractTemplate(r.Context(), sqlc.GetContractTemplateParams{OrgID: p.OrgID, ID: id})
	if isNoRows(err) {
		notFoundTemplate(w)
		return sqlc.ContractTemplate{}, false
	}
	if err != nil {
		s.serverError(w, r, "template.get", err)
		return sqlc.ContractTemplate{}, false
	}
	return row, true
}

func (s *Server) handleGetTemplate(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.orgTemplate(w, r, p)
	if !ok {
		return
	}
	out := toTemplateSummary(row)
	out.BodyHTML = row.BodyHtml
	out.Variables = contract.Variables
	WriteJSON(w, http.StatusOK, map[string]any{"template": out})
}

// ---------------------------------------------------- POST /contract-templates --

func (s *Server) handleCreateTemplate(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		Name      string `json:"name"`
		BodyHTML  string `json:"body_html"`
		IsDefault bool   `json:"is_default"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	name := f.MaxLen("name", f.Required("name", body.Name), templateNameMax)
	html := templateBody(f, body.BodyHTML)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	var created sqlc.ContractTemplate
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		created, err = q.CreateContractTemplate(r.Context(), sqlc.CreateContractTemplateParams{
			OrgID: p.OrgID, Name: name, BodyHtml: html, IsDefault: body.IsDefault,
		})
		if err != nil {
			return err
		}
		// The partial unique index allows one default per org, so promoting a
		// template demotes the incumbent first.
		if body.IsDefault {
			if err := q.ClearDefaultTemplate(r.Context(), sqlc.ClearDefaultTemplateParams{
				OrgID: p.OrgID, KeepID: created.ID,
			}); err != nil {
				return err
			}
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionTemplateCreate,
			EntityType:  audit.EntityContractTemplate,
			EntityID:    db.UUIDString(created.ID),
			After:       map[string]any{"name": name, "is_default": body.IsDefault, "body_bytes": len(html)},
		})
	}); err != nil {
		s.serverError(w, r, "template.create.tx", err)
		return
	}

	out := toTemplateSummary(created)
	out.BodyHTML = created.BodyHtml
	out.Variables = contract.Variables
	out.IsDefault = body.IsDefault
	WriteJSON(w, http.StatusCreated, map[string]any{"template": out})
}

// -------------------------------------------- PATCH /contract-templates/{id} --

func (s *Server) handlePatchTemplate(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	existing, ok := s.orgTemplate(w, r, p)
	if !ok {
		return
	}

	var body struct {
		Name      *string `json:"name"`
		BodyHTML  *string `json:"body_html"`
		IsDefault *bool   `json:"is_default"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	var name, html *string
	if body.Name != nil {
		v := f.MaxLen("name", f.Required("name", *body.Name), templateNameMax)
		name = &v
	}
	if body.BodyHTML != nil {
		v := templateBody(f, *body.BodyHTML)
		html = &v
	}
	// Demoting the only default would leave an org with no template to fall
	// back to when a contract is created without naming one.
	if body.IsDefault != nil && !*body.IsDefault && existing.IsDefault {
		f.Add("is_default", "promote another template instead of clearing the default")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	var updated sqlc.ContractTemplate
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if body.IsDefault != nil && *body.IsDefault {
			if err := q.ClearDefaultTemplate(r.Context(), sqlc.ClearDefaultTemplateParams{
				OrgID: p.OrgID, KeepID: existing.ID,
			}); err != nil {
				return err
			}
		}
		var err error
		updated, err = q.UpdateContractTemplate(r.Context(), sqlc.UpdateContractTemplateParams{
			OrgID: p.OrgID, ID: existing.ID, Name: name, BodyHtml: html, IsDefault: body.IsDefault,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionTemplateUpdate,
			EntityType:  audit.EntityContractTemplate,
			EntityID:    db.UUIDString(updated.ID),
			Before:      map[string]any{"name": existing.Name, "is_default": existing.IsDefault},
			After:       map[string]any{"name": updated.Name, "is_default": updated.IsDefault},
		})
	}); err != nil {
		s.serverError(w, r, "template.update.tx", err)
		return
	}

	out := toTemplateSummary(updated)
	out.BodyHTML = updated.BodyHtml
	out.Variables = contract.Variables
	WriteJSON(w, http.StatusOK, map[string]any{"template": out})
}

// ------------------------------------------- DELETE /contract-templates/{id} --

func (s *Server) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	existing, ok := s.orgTemplate(w, r, p)
	if !ok {
		return
	}
	// The default is the org's fallback for every contract created without an
	// explicit template; deleting it would leave contract creation with nothing
	// to render (API.md).
	if existing.IsDefault {
		conflictCode(w, "template_is_default", "template is the default",
			"promote another template to default before deleting this one")
		return
	}

	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		deleted, err := q.SoftDeleteContractTemplate(r.Context(), sqlc.SoftDeleteContractTemplateParams{
			OrgID: p.OrgID, ID: existing.ID,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionTemplateDelete,
			EntityType:  audit.EntityContractTemplate,
			EntityID:    db.UUIDString(deleted.ID),
			Before:      map[string]any{"name": existing.Name},
		})
	}); err != nil {
		s.serverError(w, r, "template.delete.tx", err)
		return
	}
	NoContent(w)
}

// ------------------------------------ POST /contract-templates/{id}/preview --

func (s *Server) handlePreviewTemplate(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.orgTemplate(w, r, p)
	if !ok {
		return
	}
	// The body carries an optional {sample:bool}; a preview has nothing but
	// sample values to render with, so the flag only documents the intent.
	var body struct {
		Sample *bool `json:"sample"`
	}
	if r.ContentLength > 0 && !DecodeJSON(w, r, &body) {
		return
	}

	org, err := s.q.GetOrg(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "template.preview.org", err)
		return
	}
	brand := s.brandingAssets(r.Context(), p.OrgID, org.Name)

	// Sanitizing on render as well as on write means a body stored before a
	// policy change can never escape the current allowlist.
	html := contract.Render(contract.SanitizeHTML(row.BodyHtml), contract.SampleVars(brand.DisplayName))
	WriteJSON(w, http.StatusOK, map[string]any{
		"html":           html,
		"letterhead_url": brand.LetterheadURL,
		"logo_url":       brand.LogoURL,
		"display_name":   brand.DisplayName,
		"footer_text":    brand.FooterText,
	})
}

// ------------------------------------------------------------------ helpers --

// templateBody validates and sanitizes a submitted template body. The stored
// value is always the sanitized one: what a landlord sees on reload is exactly
// what a contract will snapshot.
func templateBody(f validate.Fields, in string) string {
	raw := strings.TrimSpace(in)
	if raw == "" {
		f.Add("body_html", "body_html is required")
		return ""
	}
	if len(raw) > templateBodyMax {
		f.Add("body_html", "must be at most 200 KiB")
		return ""
	}
	clean := contract.SanitizeHTML(raw)
	if strings.TrimSpace(clean) == "" {
		f.Add("body_html", "contains no usable content once sanitized")
	}
	return clean
}
