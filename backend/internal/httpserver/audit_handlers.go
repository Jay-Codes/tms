package httpserver

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/validate"
)

const (
	auditDefaultLimit = 50
	auditMaxLimit     = 200
)

// auditEntryResponse is one row of GET /audit-log.
type auditEntryResponse struct {
	ID          string          `json:"id"`
	ActorUserID string          `json:"actor_user_id"`
	ActorName   string          `json:"actor_name"`
	Action      string          `json:"action"`
	EntityType  string          `json:"entity_type"`
	EntityID    string          `json:"entity_id"`
	Before      json.RawMessage `json:"before"`
	After       json.RawMessage `json:"after"`
	IP          string          `json:"ip"`
	At          time.Time       `json:"at"`
}

// encodeCursor packs (at, id) into an opaque base64 cursor.
func encodeCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "," + id))
}

// decodeCursor unpacks a cursor produced by encodeCursor.
func decodeCursor(cursor string) (time.Time, pgtype.UUID, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, pgtype.UUID{}, false
	}
	at, id, found := strings.Cut(string(raw), ",")
	if !found {
		return time.Time{}, pgtype.UUID{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, pgtype.UUID{}, false
	}
	u, err := db.ParseUUID(id)
	if err != nil {
		return time.Time{}, pgtype.UUID{}, false
	}
	return t, u, true
}

// handleListAuditLog serves the org-scoped audit trail with filters and cursor
// pagination. The query is filtered by the session's org_id, so entries from
// another org are simply absent (and a direct entity lookup 404s).
func (s *Server) handleListAuditLog(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	qs := r.URL.Query()
	f := validate.Fields{}

	params := sqlc.ListAuditLogParams{OrgID: p.OrgID, RowLimit: auditDefaultLimit}

	if v := strings.TrimSpace(qs.Get("entity_type")); v != "" {
		params.EntityType = &v
	}
	if v := strings.TrimSpace(qs.Get("actor")); v != "" {
		u, err := db.ParseUUID(v)
		if err != nil {
			f.Add("actor", "must be a user id (UUID)")
		} else {
			params.ActorUserID = u
		}
	}
	if v := strings.TrimSpace(qs.Get("from")); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			f.Add("from", "must be an RFC3339 timestamp")
		} else {
			params.FromAt = db.TS(t)
		}
	}
	if v := strings.TrimSpace(qs.Get("to")); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			f.Add("to", "must be an RFC3339 timestamp")
		} else {
			params.ToAt = db.TS(t)
		}
	}
	if v := strings.TrimSpace(qs.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > auditMaxLimit {
			f.Add("limit", "must be between 1 and "+strconv.Itoa(auditMaxLimit))
		} else {
			params.RowLimit = int32(n)
		}
	}
	if v := strings.TrimSpace(qs.Get("cursor")); v != "" {
		at, id, ok := decodeCursor(v)
		if !ok {
			f.Add("cursor", "malformed cursor")
		} else {
			params.CursorAt = db.TS(at)
			params.CursorID = id
		}
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	rows, err := s.q.ListAuditLog(r.Context(), params)
	if err != nil {
		s.serverError(w, r, "audit.list", err)
		return
	}

	items := make([]auditEntryResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, auditEntryResponse{
			ID:          db.UUIDString(row.ID),
			ActorUserID: db.UUIDString(row.ActorUserID),
			ActorName:   db.StrVal(row.ActorName),
			Action:      row.Action,
			EntityType:  row.EntityType,
			EntityID:    db.UUIDString(row.EntityID),
			Before:      json.RawMessage(row.Before),
			After:       json.RawMessage(row.After),
			IP:          db.StrVal(row.Ip),
			At:          row.At.Time,
		})
	}

	var next *string
	if len(rows) == int(params.RowLimit) && len(rows) > 0 {
		last := rows[len(rows)-1]
		c := encodeCursor(last.At.Time, db.UUIDString(last.ID))
		next = &c
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// handleGetAuditEntry serves a single org-scoped audit entry. It exists to make
// cross-org probing testable: an entry belonging to another org returns 404.
func (s *Server) handleGetAuditEntry(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	entryID, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such audit entry")
		return
	}
	row, err := s.q.GetAuditLogEntry(r.Context(), sqlc.GetAuditLogEntryParams{OrgID: p.OrgID, ID: entryID})
	if isNoRows(err) {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such audit entry")
		return
	}
	if err != nil {
		s.serverError(w, r, "audit.get", err)
		return
	}
	WriteJSON(w, http.StatusOK, auditEntryResponse{
		ID:          db.UUIDString(row.ID),
		ActorUserID: db.UUIDString(row.ActorUserID),
		ActorName:   db.StrVal(row.ActorName),
		Action:      row.Action,
		EntityType:  row.EntityType,
		EntityID:    db.UUIDString(row.EntityID),
		Before:      json.RawMessage(row.Before),
		After:       json.RawMessage(row.After),
		IP:          db.StrVal(row.Ip),
		At:          row.At.Time,
	})
}
