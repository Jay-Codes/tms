package httpserver

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/validate"
)

// Phase 22 §22.1 — which contract template a tenancy is written on.
//
// Resolution, first hit wins (ResolveUnitTemplate): the template named at
// approval or creation → the unit's → the property's → the org default. The
// routes here set the middle two and show the answer for one unit.

const (
	templateSourceExplicit = "explicit"
	bulkTemplateMaxUnits   = 500
)

func optUUIDString(id pgtype.UUID) *string {
	if !id.Valid {
		return nil
	}
	s := db.UUIDString(id)
	return &s
}

// templateUsage is TemplateUsage keyed by template id.
func (s *Server) templateUsage(ctx context.Context, orgID pgtype.UUID) (map[string]templateUsage, error) {
	rows, err := s.q.TemplateUsage(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]templateUsage, len(rows))
	for _, r := range rows {
		out[db.UUIDString(r.ID)] = templateUsage{Units: r.Units, Properties: r.Properties}
	}
	return out, nil
}

// assignableTemplate reads `template_id` as the assignment routes take it:
// the key is required, `null` clears, and an id must be a live template of the
// caller's org. ok=false means the response has been written.
func (s *Server) assignableTemplate(
	w http.ResponseWriter, r *http.Request, p auth.Principal, f validate.Fields, raw json.RawMessage,
) (pgtype.UUID, bool) {
	// A plain RawMessage, not a pointer: encoding/json sets a pointer to nil
	// for a literal `null`, which would make "clear" read as "absent".
	if len(raw) == 0 {
		f.Add("template_id", "required (null clears the assignment)")
		return pgtype.UUID{}, true
	}
	var v *string
	if err := json.Unmarshal(raw, &v); err != nil {
		f.Add("template_id", "must be a template id or null")
		return pgtype.UUID{}, true
	}
	if v == nil {
		return pgtype.UUID{}, true
	}
	id := uuidField(f, "template_id", *v, true)
	if !id.Valid {
		return pgtype.UUID{}, true
	}
	if _, err := s.q.GetContractTemplate(r.Context(), sqlc.GetContractTemplateParams{OrgID: p.OrgID, ID: id}); err != nil {
		if isNoRows(err) {
			f.Add("template_id", "no such template in this organisation")
			return pgtype.UUID{}, true
		}
		s.serverError(w, r, "template.assign.lookup", err)
		return pgtype.UUID{}, false
	}
	return id, true
}

// -------------------------------------------- POST /units/bulk-template --

// handleBulkUnitTemplate assigns one template to many units, or clears it.
// Ids outside the org are not an error — they match nothing, and `updated`
// says how many did.
func (s *Server) handleBulkUnitTemplate(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	var body struct {
		UnitIDs    []string        `json:"unit_ids"`
		TemplateID json.RawMessage `json:"template_id"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	if len(body.UnitIDs) == 0 || len(body.UnitIDs) > bulkTemplateMaxUnits {
		f.Add("unit_ids", "between 1 and 500 unit ids")
	}
	ids := make([]pgtype.UUID, 0, len(body.UnitIDs))
	for _, raw := range body.UnitIDs {
		id, err := db.ParseUUID(raw)
		if err != nil {
			f.Add("unit_ids", "must all be unit ids")
			break
		}
		ids = append(ids, id)
	}
	templateID, ok := s.assignableTemplate(w, r, p, f, body.TemplateID)
	if !ok {
		return
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	var updated []pgtype.UUID
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		updated, err = q.SetUnitsTemplate(r.Context(), sqlc.SetUnitsTemplateParams{
			TemplateID: templateID, OrgID: p.OrgID, UnitIds: ids,
		})
		if err != nil {
			return err
		}
		for _, id := range updated {
			if err := audit.Record(r.Context(), q, audit.Entry{
				OrgID:       p.OrgIDString(),
				ActorUserID: p.UserIDString(),
				Action:      audit.ActionUnitTemplateSet,
				EntityType:  audit.EntityUnit,
				EntityID:    db.UUIDString(id),
				After:       map[string]any{"contract_template_id": optUUIDString(templateID)},
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		s.serverError(w, r, "unit.template.tx", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"updated": len(updated), "contract_template_id": optUUIDString(templateID),
	})
}

// --------------------------------------- PUT /properties/{id}/template --

func (s *Server) handleSetPropertyTemplate(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	current, ok := s.orgProperty(w, r, p)
	if !ok {
		return
	}
	var body struct {
		TemplateID json.RawMessage `json:"template_id"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	templateID, ok := s.assignableTemplate(w, r, p, f, body.TemplateID)
	if !ok {
		return
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.SetPropertyTemplate(r.Context(), sqlc.SetPropertyTemplateParams{
			TemplateID: templateID, OrgID: p.OrgID, ID: current.ID,
		}); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionPropertyTemplateSet,
			EntityType:  audit.EntityProperty,
			EntityID:    db.UUIDString(current.ID),
			Before:      map[string]any{"contract_template_id": optUUIDString(current.ContractTemplateID)},
			After:       map[string]any{"contract_template_id": optUUIDString(templateID)},
		})
	}); err != nil {
		s.serverError(w, r, "property.template.tx", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"contract_template_id": optUUIDString(templateID)})
}

// ------------------------------------------- GET /units/{id}/template --

// handleGetUnitTemplate answers "which template would a tenancy of this unit
// be written on, and why" — the approve sheet shows it before the landlord
// decides whether to pick another.
func (s *Server) handleGetUnitTemplate(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	unit, ok := s.orgUnit(w, r, p)
	if !ok {
		return
	}
	res, err := s.q.ResolveUnitTemplate(r.Context(), sqlc.ResolveUnitTemplateParams{OrgID: p.OrgID, UnitID: unit.ID})
	if isNoRows(err) {
		WriteJSON(w, http.StatusOK, map[string]any{"template": nil})
		return
	}
	if err != nil {
		s.serverError(w, r, "unit.template.resolve", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"template": map[string]any{
		"id": db.UUIDString(res.ID), "name": res.Name, "source": res.Source,
	}})
}
