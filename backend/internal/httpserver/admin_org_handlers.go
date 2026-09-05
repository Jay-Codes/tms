package httpserver

import (
	"encoding/json"
	"net/http"
	"strconv"
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

const (
	adminOrgsDefaultLimit = 25
	adminOrgsMaxLimit     = 100
	suspendReasonMax      = 500
)

// adminJobs is the fixed job catalogue GET /admin/jobs reports, each paired
// with the audit action its run writes. There is no `job_runs` table: the jobs
// already record themselves in the audit trail, and a second store could only
// disagree with it (DECISIONS.md).
//
//nolint:gochecknoglobals // fixed catalogue, read-only.
var adminJobs = []adminJobRow{
	{
		Name: "contract-lifecycle", Action: audit.ActionContractLifecycleRun, Runnable: true,
		Description: "moves contracts into `expiring` and `ended`, freeing their units",
	},
	{
		Name: "overdue", Action: audit.ActionOverdueRun, Runnable: true,
		Description: "flips lapsed payment schedules to `overdue`",
	},
	{
		Name: "notifications", Action: audit.ActionNotificationRun, Runnable: true,
		Description: "derives the day's reminders and queues them for the SMS workers",
	},
}

// likeEscaper neutralises the LIKE metacharacters in a user-typed search term.
// The queries wrap `q` as `'%' || q || '%'`, so an unescaped `%` or `_` would
// silently turn a search into a wildcard the operator did not ask for.
// Backslash goes first: it is Postgres' default LIKE escape character, so it
// has to be doubled before it is used to escape anything else.
//
//nolint:gochecknoglobals // stateless replacer, safe for concurrent use.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// escapeLike prepares a search term for the `ILIKE '%' || $1 || '%'` filters.
func escapeLike(q string) string { return likeEscaper.Replace(q) }

// toAdminOrgRow renders one org list row.
func toAdminOrgRow(row sqlc.AdminListOrgsPageRow) adminOrgRow {
	out := adminOrgRow{
		ID:     db.UUIDString(row.ID),
		Name:   row.Name,
		Slug:   row.Slug,
		Status: row.Status,
		Owner:  adminOrgOwner{Name: row.OwnerName, Email: row.OwnerEmail},
		Counts: adminOrgCounts{
			Properties:      row.Properties,
			Units:           row.Units,
			Renters:         row.Renters,
			ActiveContracts: row.ActiveContracts,
		},
		SMS:             adminOrgSMS{Sent30d: row.SmsSent30d, Failed30d: row.SmsFailed30d},
		SuspendedReason: row.SuspendedReason,
		CreatedAt:       row.CreatedAt.Time,
	}
	if row.SuspendedAt.Valid {
		at := row.SuspendedAt.Time.UTC()
		out.SuspendedAt = &at
	}
	return out
}

// ---------------------------------------------------------- GET /admin/orgs --

func (s *Server) handleAdminListOrgs(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	qs := r.URL.Query()
	f := validate.Fields{}
	params := sqlc.AdminListOrgsPageParams{RowLimit: adminOrgsDefaultLimit}

	if v := strings.TrimSpace(qs.Get("q")); v != "" {
		term := escapeLike(v)
		params.Q = &term
	}
	if v := strings.TrimSpace(qs.Get("status")); v != "" {
		status := f.OneOf("status", strings.ToLower(v), "active", "suspended")
		params.Status = &status
	}
	if v := strings.TrimSpace(qs.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > adminOrgsMaxLimit {
			f.Add("limit", "must be between 1 and "+strconv.Itoa(adminOrgsMaxLimit))
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

	rows, err := s.q.AdminListOrgsPage(r.Context(), params)
	if err != nil {
		s.serverError(w, r, "admin.orgs.list", err)
		return
	}
	items := make([]adminOrgRow, 0, len(rows))
	for _, row := range rows {
		items = append(items, toAdminOrgRow(row))
	}
	var next *string
	if len(rows) == int(params.RowLimit) && len(rows) > 0 {
		last := rows[len(rows)-1]
		c := encodeCursor(last.CreatedAt.Time, db.UUIDString(last.ID))
		next = &c
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// ----------------------------------------------------- GET /admin/orgs/{id} --

func (s *Server) handleAdminGetOrg(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	orgID, ok := s.adminOrgID(w, r)
	if !ok {
		return
	}

	rows, err := s.q.AdminListOrgsPage(r.Context(), sqlc.AdminListOrgsPageParams{
		OrgID: orgID, RowLimit: 1,
	})
	if err != nil {
		s.serverError(w, r, "admin.orgs.get", err)
		return
	}
	if len(rows) == 0 {
		adminOrgNotFound(w)
		return
	}
	org, err := s.q.GetOrg(r.Context(), orgID)
	if err != nil {
		s.serverError(w, r, "admin.orgs.get.settings", err)
		return
	}
	members, err := s.q.AdminListOrgMembers(r.Context(), orgID)
	if err != nil {
		s.serverError(w, r, "admin.orgs.get.members", err)
		return
	}

	settings := parseSettings(org.Settings)
	detail := adminOrgDetail{
		adminOrgRow: toAdminOrgRow(rows[0]),
		Settings: adminOrgSettings{
			AutoApproveLinks:     settings.AutoApproveLinks,
			GraceDays:            settings.GraceDays,
			UnsignedReminderDays: settings.UnsignedReminderDays,
			SMSLanguage:          settings.SMSLanguage,
			BankAccountSet:       settings.BankAccount != nil,
			NotificationsSet:     settings.Notifications != nil,
		},
		Members: make([]adminOrgMember, 0, len(members)),
	}
	for _, m := range members {
		detail.Members = append(detail.Members, adminOrgMember{
			ID:       db.UUIDString(m.ID),
			UserID:   db.UUIDString(m.UserID),
			FullName: m.FullName,
			Email:    db.StrVal(m.Email),
			Role:     m.Role,
			Status:   m.Status,
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"org": detail})
}

// ------------------------------- POST /admin/orgs/{id}/{suspend|activate} --

func (s *Server) handleAdminSuspendOrg(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	orgID, ok := s.adminOrgID(w, r)
	if !ok {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		Reason string `json:"reason"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	reason := f.MaxLen("reason", f.Required("reason", strings.TrimSpace(body.Reason)), suspendReasonMax)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	var updated sqlc.Org
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		before, err := q.GetOrg(r.Context(), orgID)
		if err != nil {
			return err
		}
		updated, err = q.AdminSuspendOrg(r.Context(), sqlc.AdminSuspendOrgParams{
			ID: orgID, Reason: &reason,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       db.UUIDString(orgID),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionOrgSuspend,
			EntityType:  audit.EntityOrg,
			EntityID:    db.UUIDString(orgID),
			Before:      map[string]any{"status": before.Status},
			After:       map[string]any{"status": updated.Status, "reason": reason},
		})
	}); err != nil {
		if isNoRows(err) {
			adminOrgNotFound(w)
			return
		}
		s.serverError(w, r, "admin.orgs.suspend", err)
		return
	}
	// The flag is written after the commit: a rolled-back suspension must not
	// leave a cache that locks the org's staff out of a tenant that is still
	// active.
	s.sessions.SetSuspended(r.Context(), db.UUIDString(orgID), true)
	WriteJSON(w, http.StatusOK, map[string]any{"org": toOrg(updated)})
}

func (s *Server) handleAdminActivateOrg(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	orgID, ok := s.adminOrgID(w, r)
	if !ok {
		return
	}
	p := auth.MustFromContext(r.Context())

	var updated sqlc.Org
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		before, err := q.GetOrg(r.Context(), orgID)
		if err != nil {
			return err
		}
		updated, err = q.AdminActivateOrg(r.Context(), orgID)
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       db.UUIDString(orgID),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionOrgActivate,
			EntityType:  audit.EntityOrg,
			EntityID:    db.UUIDString(orgID),
			Before:      map[string]any{"status": before.Status, "reason": before.SuspendedReason},
			After:       map[string]any{"status": updated.Status},
		})
	}); err != nil {
		if isNoRows(err) {
			adminOrgNotFound(w)
			return
		}
		s.serverError(w, r, "admin.orgs.activate", err)
		return
	}
	s.sessions.SetSuspended(r.Context(), db.UUIDString(orgID), false)
	WriteJSON(w, http.StatusOK, map[string]any{"org": toOrg(updated)})
}

// ------------------------------------------------------- GET /admin/metrics --

func (s *Server) handleAdminMetrics(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	m, err := s.q.AdminMetrics(r.Context())
	if err != nil {
		s.serverError(w, r, "admin.metrics", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"orgs":      map[string]any{"total": m.OrgsTotal, "active": m.OrgsActive, "suspended": m.OrgsSuspended},
		"renters":   map[string]any{"total": m.RentersTotal},
		"units":     map[string]any{"total": m.UnitsTotal, "occupied": m.UnitsOccupied},
		"contracts": map[string]any{"active": m.ContractsActive},
		"sms": map[string]any{
			"sent_24h": m.SmsSent24h, "failed_24h": m.SmsFailed24h, "queued": m.SmsQueued,
		},
		"payments": map[string]any{
			"recorded_30d": m.PaymentsRecorded30d, "amount_30d": m.PaymentsAmount30d,
		},
		"db":    map[string]any{"ok": pingState(r.Context(), s.deps.DB) == statusOK},
		"redis": map[string]any{"ok": pingState(r.Context(), s.deps.Redis) == statusOK},
		"minio": map[string]any{"ok": pingState(r.Context(), s.deps.Minio) == statusOK},
	})
}

// ---------------------------------------------------- GET /admin/audit-log --

func (s *Server) handleAdminAuditLog(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	qs := r.URL.Query()
	f := validate.Fields{}
	params := sqlc.AdminListAuditLogParams{RowLimit: auditDefaultLimit}

	uuidParam := func(field string) (uuid pgtype.UUID, ok bool) {
		v := strings.TrimSpace(qs.Get(field))
		if v == "" {
			return pgtype.UUID{}, false
		}
		id, err := db.ParseUUID(v)
		if err != nil {
			f.Add(field, "must be a UUID")
			return pgtype.UUID{}, false
		}
		return id, true
	}
	if id, ok := uuidParam("org_id"); ok {
		params.OrgID = id
	}
	if id, ok := uuidParam("actor"); ok {
		params.ActorUserID = id
	}
	if id, ok := uuidParam("entity_id"); ok {
		params.EntityID = id
	}
	if v := strings.TrimSpace(qs.Get("entity_type")); v != "" {
		params.EntityType = &v
	}
	if v := strings.TrimSpace(qs.Get("q")); v != "" {
		term := escapeLike(v)
		params.Q = &term
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

	rows, err := s.q.AdminListAuditLog(r.Context(), params)
	if err != nil {
		s.serverError(w, r, "admin.audit.list", err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{
			"id":            db.UUIDString(row.ID),
			"org_id":        db.UUIDString(row.OrgID),
			"org_name":      row.OrgName,
			"actor_user_id": db.UUIDString(row.ActorUserID),
			"actor_name":    db.StrVal(row.ActorName),
			"action":        row.Action,
			"entity_type":   row.EntityType,
			"entity_id":     db.UUIDString(row.EntityID),
			"before":        json.RawMessage(row.Before),
			"after":         json.RawMessage(row.After),
			"ip":            db.StrVal(row.Ip),
			"at":            row.At.Time,
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

// ---------------------------------------------------------- GET /admin/jobs --

func (s *Server) handleAdminJobs(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	actions := make([]string, 0, len(adminJobs))
	for _, j := range adminJobs {
		actions = append(actions, j.Action)
	}
	rows, err := s.q.AdminJobLastRuns(r.Context(), actions)
	if err != nil {
		s.serverError(w, r, "admin.jobs", err)
		return
	}
	byAction := make(map[string]sqlc.AdminJobLastRunsRow, len(rows))
	for _, row := range rows {
		byAction[row.Action] = row
	}

	items := make([]adminJobRow, 0, len(adminJobs))
	for _, j := range adminJobs {
		job := j
		if row, ok := byAction[j.Action]; ok {
			if row.At.Valid {
				at := row.At.Time.UTC()
				job.LastRunAt = &at
			}
			if len(row.After) > 0 {
				job.LastResult = json.RawMessage(row.After)
			}
		}
		items = append(items, job)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ------------------------------------------------------------------ shared --

// adminOrgID reads the {id} path parameter. A malformed id is a 404, not a 400:
// the caller is naming an org, and an org that cannot exist is one that is not
// there.
func (s *Server) adminOrgID(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		adminOrgNotFound(w)
		return pgtype.UUID{}, false
	}
	return id, true
}

func adminOrgNotFound(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such organisation")
}
