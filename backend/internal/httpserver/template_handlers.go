package httpserver

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/contract"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/validate"
)

// noTemplateID is the "keep nothing" argument to ClearDefaultTemplate: a valid
// but all-zero UUID, which no template can carry, so every live default of the
// org is demoted. A NULL there would match no row at all (`id <> NULL`).
//
//nolint:gochecknoglobals // a constant value pgtype cannot express as a const.
var noTemplateID = pgtype.UUID{Valid: true}

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
	out.BodyHTMLSW = row.BodyHtmlSw
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
		Name       string `json:"name"`
		BodyHTML   string `json:"body_html"`
		BodyHTMLSW string `json:"body_html_sw"`
		IsDefault  bool   `json:"is_default"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	name := f.MaxLen("name", f.Required("name", body.Name), templateNameMax)
	html := templateBody(f, "body_html", body.BodyHTML, true)
	htmlSW := templateBody(f, "body_html_sw", body.BodyHTMLSW, false)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	var created sqlc.ContractTemplate
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		// The partial unique index allows one default per org and is checked
		// row by row, so the incumbent is demoted *before* the new default is
		// inserted — the other order collides with the index.
		if body.IsDefault {
			if err := q.ClearDefaultTemplate(r.Context(), sqlc.ClearDefaultTemplateParams{
				OrgID: p.OrgID, KeepID: noTemplateID,
			}); err != nil {
				return err
			}
		}
		var err error
		created, err = q.CreateContractTemplate(r.Context(), sqlc.CreateContractTemplateParams{
			OrgID: p.OrgID, Name: name, BodyHtml: html, BodyHtmlSw: &htmlSW,
			IsDefault: body.IsDefault,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionTemplateCreate,
			EntityType:  audit.EntityContractTemplate,
			EntityID:    db.UUIDString(created.ID),
			After: map[string]any{
				"name": name, "is_default": body.IsDefault,
				"body_bytes": len(html), "body_sw_bytes": len(htmlSW),
			},
		})
	}); err != nil {
		s.serverError(w, r, "template.create.tx", err)
		return
	}

	out := toTemplateSummary(created)
	out.BodyHTML = created.BodyHtml
	out.BodyHTMLSW = created.BodyHtmlSw
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
		Name       *string `json:"name"`
		BodyHTML   *string `json:"body_html"`
		BodyHTMLSW *string `json:"body_html_sw"`
		IsDefault  *bool   `json:"is_default"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	var name, html, htmlSW *string
	if body.Name != nil {
		v := f.MaxLen("name", f.Required("name", *body.Name), templateNameMax)
		name = &v
	}
	if body.BodyHTML != nil {
		v := templateBody(f, "body_html", *body.BodyHTML, true)
		html = &v
	}
	// The Swahili body may be cleared (an org that decides it does not want
	// one), so an explicit empty string is a write, not a validation failure.
	if body.BodyHTMLSW != nil {
		v := templateBody(f, "body_html_sw", *body.BodyHTMLSW, false)
		htmlSW = &v
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
			OrgID: p.OrgID, ID: existing.ID, Name: name, BodyHtml: html,
			BodyHtmlSw: htmlSW, IsDefault: body.IsDefault,
		})
		if err != nil {
			return err
		}
		before := map[string]any{"name": existing.Name, "is_default": existing.IsDefault}
		after := map[string]any{"name": updated.Name, "is_default": updated.IsDefault}
		// The body itself is too large for the audit row, but its size makes a
		// rewrite visible: an edit that changed nothing else still shows here.
		if updated.BodyHtml != existing.BodyHtml {
			before["body_bytes"] = len(existing.BodyHtml)
			after["body_bytes"] = len(updated.BodyHtml)
		}
		if updated.BodyHtmlSw != existing.BodyHtmlSw {
			before["body_sw_bytes"] = len(existing.BodyHtmlSw)
			after["body_sw_bytes"] = len(updated.BodyHtmlSw)
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionTemplateUpdate,
			EntityType:  audit.EntityContractTemplate,
			EntityID:    db.UUIDString(updated.ID),
			Before:      before,
			After:       after,
		})
	}); err != nil {
		s.serverError(w, r, "template.update.tx", err)
		return
	}

	out := toTemplateSummary(updated)
	out.BodyHTML = updated.BodyHtml
	out.BodyHTMLSW = updated.BodyHtmlSw
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
		Sample   *bool  `json:"sample"`
		Language string `json:"language"`
	}
	if r.ContentLength > 0 && !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	lang := ""
	if v := optLocale(f, "language", body.Language); v != nil {
		lang = *v
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	org, err := s.q.GetOrg(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "template.preview.org", err)
		return
	}
	brand := s.brandingAssets(r.Context(), p.OrgID, org.Name)

	// Sanitizing on render as well as on write means a body stored before a
	// policy change can never escape the current allowlist. `language` picks
	// the body the same way a contract does, and the answer says which body
	// was actually used — asking for Swahili from a template that has none
	// previews the English one rather than a blank page.
	src, resolved := contract.BodyFor(lang, row.BodyHtml, row.BodyHtmlSw)
	vars := contract.SampleVars(brand.DisplayName)
	vars["due_day"] = contract.DueDayPhraseFor(resolved, nil)
	html := contract.Render(contract.SanitizeHTML(src), vars)
	WriteJSON(w, http.StatusOK, map[string]any{
		"html":           html,
		"language":       resolved,
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
// `field` names the body being validated (`body_html` or `body_html_sw`), and
// `required` says whether an empty one is an error: the English body is the
// document, the Swahili body is optional and may be cleared.
func templateBody(f validate.Fields, field, in string, required bool) string {
	raw := strings.TrimSpace(in)
	if raw == "" {
		if required {
			f.Add(field, field+" is required")
		}
		return ""
	}
	if len(raw) > templateBodyMax {
		f.Add(field, "must be at most 200 KiB")
		return ""
	}
	clean := contract.SanitizeHTML(raw)
	if strings.TrimSpace(clean) == "" {
		f.Add(field, "contains no usable content once sanitized")
	}
	return clean
}
