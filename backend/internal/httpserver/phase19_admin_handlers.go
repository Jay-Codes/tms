// Phase 19 §19.2 — the platform user directory.
//
// Platform admin already had org-level views (`/admin/orgs`); what support needs
// first is the other axis — "who is this phone number?" — because the person on
// the telephone gives their number, not their landlord's slug.
//
// Two rules shape every route here. The **list** carries no national ID field,
// masked or otherwise: a search result is the last place a number nobody asked
// for should travel. And opening the **detail** is audited, although it is a
// read, because the page assembles one person's tenancies, payments,
// memberships and audit trail across every tenant on the platform.
package httpserver

import (
	"encoding/json"
	"net/http"
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

// The user kinds and statuses `?kind=` and `?status=` accept. They are the
// column's own vocabulary, listed rather than derived so an added kind has to be
// admitted here deliberately.
const (
	userKindRenter  = "renter"
	userKindOrgUser = "org_user"
	userKindAdmin   = "platform_admin"

	userStatusActive    = "active"
	userStatusSuspended = "suspended"
)

// adminUserAuditShown is how much of a user's trail the detail page opens with.
const adminUserAuditShown = 50

// ------------------------------------------------------------- responses --

// adminUserOrg is one entry of a directory row's `orgs[]`.
type adminUserOrg struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Role is set for an org user, Relationship for a renter; the other is
	// null. A person who is both staff somewhere and a renter elsewhere
	// therefore reads correctly on both lines.
	Role         *string `json:"role"`
	Relationship *string `json:"relationship"`
}

// adminUserRowResponse is the list row and the detail's `user` block.
//
// `nida_masked` is on the struct but `omitempty`, and is only ever filled by the
// detail handler: the list leaves it nil, so the field is simply absent there.
type adminUserRowResponse struct {
	ID            string         `json:"id"`
	Kind          string         `json:"kind"`
	FullName      string         `json:"full_name"`
	Phone         *string        `json:"phone"`
	Email         *string        `json:"email"`
	Status        string         `json:"status"`
	CreatedAt     time.Time      `json:"created_at"`
	Orgs          []adminUserOrg `json:"orgs"`
	ContractsLive *int64         `json:"contracts_live,omitempty"`
	KycStatus     *string        `json:"kyc_status,omitempty"`
	NidaMasked    *string        `json:"nida_masked,omitempty"`
}

// adminUserContract is one tenancy on the detail page.
type adminUserContract struct {
	ID           string `json:"id"`
	OrgID        string `json:"org_id"`
	OrgName      string `json:"org_name"`
	UnitName     string `json:"unit_name"`
	PropertyName string `json:"property_name"`
	Status       string `json:"status"`
	StartDate    string `json:"start_date"`
	EndDate      string `json:"end_date"`
	RentAmount   int64  `json:"rent_amount"`
}

// adminUserLinkRequest is one application on the detail page.
type adminUserLinkRequest struct {
	ID              string  `json:"id"`
	OrgID           string  `json:"org_id"`
	OrgName         string  `json:"org_name"`
	UnitName        string  `json:"unit_name"`
	PropertyName    string  `json:"property_name"`
	Status          string  `json:"status"`
	RejectionReason *string `json:"rejection_reason"`
	StartDate       *string `json:"start_date"`
	CreatedAt       string  `json:"created_at"`
}

// adminUserMembership is one staff role on the detail page.
type adminUserMembership struct {
	OrgID     string    `json:"org_id"`
	OrgName   string    `json:"org_name"`
	OrgStatus string    `json:"org_status"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// adminUserPayments is the money line: what this person has actually paid,
// anywhere. Reversed rows are excluded — the figure answers "how much reached a
// landlord", and a reversal took it back.
type adminUserPayments struct {
	Count      int64      `json:"count"`
	Total      int64      `json:"total"`
	LastPaidAt *time.Time `json:"last_paid_at"`
}

// --------------------------------------------------------- GET /admin/users --

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	qs := r.URL.Query()
	f := validate.Fields{}
	page := parseListPage(r, f)

	params := sqlc.AdminListUsersParams{
		RowLimit: page.Limit, CursorAt: page.CursorAt, CursorID: page.CursorID,
	}
	if v := strings.TrimSpace(qs.Get("kind")); v != "" {
		kind := f.OneOf("kind", v, userKindRenter, userKindOrgUser, userKindAdmin)
		params.Kind = &kind
	}
	if v := strings.TrimSpace(qs.Get("status")); v != "" {
		status := f.OneOf("status", v, userStatusActive, userStatusSuspended)
		params.Status = &status
	}
	if v := strings.TrimSpace(qs.Get("org_id")); v != "" {
		id, err := db.ParseUUID(v)
		if err != nil {
			f.Add("org_id", "must be an org id (UUID)")
		} else {
			params.OrgID = id
		}
	}
	if v := strings.TrimSpace(qs.Get("q")); v != "" {
		if len([]rune(v)) > 120 {
			f.Add("q", "must be at most 120 characters")
		}
		// The value lands in an ILIKE pattern, so its wildcards are escaped:
		// a search for "_" must not match every user on the platform.
		term := escapeLike(v)
		params.Q = &term
		// A phone is matched on the stored form, which is normalised. The
		// operator types whatever the caller reads out — `0755…`, `255755…`,
		// `+255 755 …` — and only a value that normalises is offered to the
		// phone branch at all, so a name search never runs as a phone prefix.
		if phone, err := validate.NormalizePhone(v); err == nil {
			escaped := escapeLike(phone)
			params.Phone = &escaped
		}
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	rows, err := s.q.AdminListUsers(r.Context(), params)
	if err != nil {
		s.serverError(w, r, "admin.users.list", err)
		return
	}
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	orgs, err := s.adminUserOrgs(r, ids)
	if err != nil {
		s.serverError(w, r, "admin.users.list.orgs", err)
		return
	}

	items := make([]adminUserRowResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, toAdminUserRow(row, orgs[db.UUIDString(row.ID)]))
	}
	var next *string
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		next = nextCursor(len(rows), page.Limit, last.CreatedAt.Time, db.UUIDString(last.ID))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// adminUserOrgs groups the `orgs[]` blocks of a page of users by user id.
func (s *Server) adminUserOrgs(r *http.Request, ids []pgtype.UUID) (map[string][]adminUserOrg, error) {
	out := map[string][]adminUserOrg{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.q.AdminUserOrgs(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		entry := adminUserOrg{ID: db.UUIDString(row.OrgID), Name: row.OrgName}
		if row.Role != "" {
			role := row.Role
			entry.Role = &role
		}
		if row.Relationship != "" {
			rel := row.Relationship
			entry.Relationship = &rel
		}
		key := db.UUIDString(row.UserID)
		out[key] = append(out[key], entry)
	}
	return out, nil
}

// toAdminUserRow renders one directory row. The renter-only fields are pointers
// so an org user's row carries no `contracts_live: 0` that reads like a fact.
func toAdminUserRow(row sqlc.AdminListUsersRow, orgs []adminUserOrg) adminUserRowResponse {
	if orgs == nil {
		orgs = []adminUserOrg{}
	}
	out := adminUserRowResponse{
		ID: db.UUIDString(row.ID), Kind: row.Kind, FullName: row.FullName,
		Phone: row.Phone, Email: row.Email, Status: row.Status,
		CreatedAt: row.CreatedAt.Time, Orgs: orgs,
	}
	if row.Kind == userKindRenter {
		live := row.ContractsLive
		kyc := kycOut(row.KycStatus)
		out.ContractsLive = &live
		out.KycStatus = &kyc
	}
	return out
}

// ---------------------------------------------------- GET /admin/users/{id} --

// adminUserRow resolves the {id} route parameter, writing the 404 itself.
func (s *Server) adminUserRow(w http.ResponseWriter, r *http.Request) (sqlc.AdminGetUserRow, bool) {
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such user")
		return sqlc.AdminGetUserRow{}, false
	}
	row, err := s.q.AdminGetUser(r.Context(), id)
	if isNoRows(err) {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such user")
		return sqlc.AdminGetUserRow{}, false
	}
	if err != nil {
		s.serverError(w, r, "admin.users.get", err)
		return sqlc.AdminGetUserRow{}, false
	}
	return row, true
}

func (s *Server) handleAdminGetUser(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.adminUserRow(w, r)
	if !ok {
		return
	}

	orgs, err := s.adminUserOrgs(r, []pgtype.UUID{row.ID})
	if err != nil {
		s.serverError(w, r, "admin.users.get.orgs", err)
		return
	}
	user := toAdminUserRow(sqlc.AdminListUsersRow(row), orgs[db.UUIDString(row.ID)])

	// The masked value only — a reveal is its own POST, and this page is a read.
	if row.Kind == userKindRenter {
		if profile, err := s.loadProfile(r.Context(), row.ID); err == nil {
			user.NidaMasked = maskNIDA(profile.NidaNumber)
		} else if !isNoRows(err) {
			s.serverError(w, r, "admin.users.get.profile", err)
			return
		}
	}

	requests, err := s.q.AdminUserLinkRequests(r.Context(), sqlc.AdminUserLinkRequestsParams{
		UserID: row.ID, RowLimit: nidaRevealsShown,
	})
	if err != nil {
		s.serverError(w, r, "admin.users.get.requests", err)
		return
	}
	linkRequests := make([]adminUserLinkRequest, 0, len(requests))
	for _, lr := range requests {
		item := adminUserLinkRequest{
			ID: db.UUIDString(lr.ID), OrgID: db.UUIDString(lr.OrgID), OrgName: lr.OrgName,
			UnitName: lr.UnitName, PropertyName: lr.PropertyName, Status: lr.Status,
			RejectionReason: lr.RejectionReason,
			CreatedAt:       lr.CreatedAt.Time.Format(time.RFC3339),
		}
		if lr.StartDate.Valid {
			start := lr.StartDate.Time.Format(dateLayout)
			item.StartDate = &start
		}
		linkRequests = append(linkRequests, item)
	}

	contractRows, err := s.q.AdminUserContracts(r.Context(), row.ID)
	if err != nil {
		s.serverError(w, r, "admin.users.get.contracts", err)
		return
	}
	contracts := make([]adminUserContract, 0, len(contractRows))
	for _, c := range contractRows {
		contracts = append(contracts, adminUserContract{
			ID: db.UUIDString(c.ID), OrgID: db.UUIDString(c.OrgID), OrgName: c.OrgName,
			UnitName: c.UnitName, PropertyName: c.PropertyName, Status: c.Status,
			StartDate: c.StartDate.Time.Format(dateLayout),
			EndDate:   c.EndDate.Time.Format(dateLayout), RentAmount: c.RentAmount,
		})
	}

	money, err := s.q.AdminUserPaymentsSummary(r.Context(), row.ID)
	if err != nil {
		s.serverError(w, r, "admin.users.get.payments", err)
		return
	}
	payments := adminUserPayments{Count: money.Count, Total: money.Total}
	if at, ok := money.LastPaidAt.(time.Time); ok {
		payments.LastPaidAt = &at
	}

	memberRows, err := s.q.AdminUserMemberships(r.Context(), row.ID)
	if err != nil {
		s.serverError(w, r, "admin.users.get.memberships", err)
		return
	}
	memberships := make([]adminUserMembership, 0, len(memberRows))
	for _, m := range memberRows {
		memberships = append(memberships, adminUserMembership{
			OrgID: db.UUIDString(m.OrgID), OrgName: m.OrgName, OrgStatus: m.OrgStatus,
			Role: m.Role, Status: m.Status, CreatedAt: m.CreatedAt.Time,
		})
	}

	auditBlock, err := s.adminUserAudit(r, row.ID)
	if err != nil {
		s.serverError(w, r, "admin.users.get.audit", err)
		return
	}

	// The read is recorded before it is answered, and inside a transaction, so a
	// page that opened always has a row and a row never describes a page that
	// failed to render.
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		return audit.Record(r.Context(), q, audit.Entry{
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionAdminUserView,
			EntityType:  audit.EntityUser,
			EntityID:    db.UUIDString(row.ID),
		})
	}); err != nil {
		s.serverError(w, r, "admin.users.get.audit_row", err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"user": user, "link_requests": linkRequests, "contracts": contracts,
		"payments": payments, "memberships": memberships, "audit": auditBlock,
	})
}

// adminUserAudit is the Activity tab: every row where this person is the actor
// or the subject, paged by `?audit_cursor=` so the block can be walked without a
// second endpoint.
func (s *Server) adminUserAudit(r *http.Request, userID pgtype.UUID) (map[string]any, error) {
	params := sqlc.AdminListAuditForUserParams{UserID: userID, RowLimit: adminUserAuditShown}
	if v := strings.TrimSpace(r.URL.Query().Get("audit_cursor")); v != "" {
		if at, id, ok := decodeCursor(v); ok {
			params.CursorAt = db.TS(at)
			params.CursorID = id
		}
	}
	rows, err := s.q.AdminListAuditForUser(r.Context(), params)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{
			"id": db.UUIDString(row.ID), "org_id": db.UUIDString(row.OrgID),
			"org_name": row.OrgName, "actor_user_id": db.UUIDString(row.ActorUserID),
			"actor_name": db.StrVal(row.ActorName), "action": row.Action,
			"entity_type": row.EntityType, "entity_id": db.UUIDString(row.EntityID),
			"before": json.RawMessage(row.Before), "after": json.RawMessage(row.After),
			"ip": db.StrVal(row.Ip), "at": row.At.Time,
		})
	}
	var next *string
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		next = nextCursor(len(rows), params.RowLimit, last.At.Time, db.UUIDString(last.ID))
	}
	return map[string]any{"items": items, "next_cursor": next}, nil
}

// ----------------------------------------------------- PATCH /admin/users/{id} --

func (s *Server) handleAdminPatchUser(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.adminUserRow(w, r)
	if !ok {
		return
	}

	var body struct {
		FullName string `json:"full_name"`
		Reason   string `json:"reason"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	name := cleanFullName(f, "full_name", body.FullName)
	reason := f.MaxLen("reason", f.Required("reason", strings.TrimSpace(body.Reason)), adminReasonMax)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	var notifyID string
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		updated, err := q.SetUserFullName(r.Context(), sqlc.SetUserFullNameParams{
			ID: row.ID, FullName: name,
		})
		if err != nil {
			return err
		}
		if row.Kind == userKindRenter {
			if _, err := q.SetRenterProfileFullName(r.Context(), sqlc.SetRenterProfileFullNameParams{
				UserID: row.ID, FullName: name,
			}); err != nil && !isNoRows(err) {
				return err
			}
		}
		if err := audit.Record(r.Context(), q, audit.Entry{
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionAdminUserUpdate,
			EntityType:  audit.EntityUser,
			EntityID:    db.UUIDString(row.ID),
			Before:      map[string]any{"full_name": row.FullName},
			After:       map[string]any{"full_name": updated.FullName, "reason": reason},
		}); err != nil {
			return err
		}
		// The renamed account is told, whichever side of the platform it is on:
		// a correction made about somebody by somebody else has to be visible to
		// them, or it is a silent rewrite of whose tenancy this is.
		if row.Kind == userKindRenter {
			notifyID, err = s.queueNameCorrected(r.Context(), q, nameCorrectedMessage{
				UserID: db.UUIDString(row.ID), Phone: db.StrVal(row.Phone),
				Name: name, OrgName: "TMS", Enabled: true,
			})
			return err
		}
		return nil
	}); err != nil {
		s.serverError(w, r, "admin.users.patch.tx", err)
		return
	}
	s.enqueueNotifications(r.Context(), notifyID)
	if row.Kind != userKindRenter {
		s.emailNameCorrected(r, row, name)
	}
	s.writeAdminUser(w, r, row.ID, "admin.users.patch")
}

// emailNameCorrected tells an org user or an admin that their display name was
// changed for them. It goes out after the commit, through the existing mailer,
// and a failure is logged rather than failing the rename: the write is the fact,
// the message is the courtesy.
func (s *Server) emailNameCorrected(r *http.Request, row sqlc.AdminGetUserRow, name string) {
	if row.Email == nil || *row.Email == "" || s.deps.Email == nil {
		return
	}
	body := "Your display name on TMS was corrected to \"" + name + "\" by platform support. " +
		"If that is wrong, contact your organisation's owner."
	if _, err := s.deps.Email.Send(r.Context(), *row.Email,
		"Your name on TMS was updated", s.cfg.TenantURL()+"/settings", body); err != nil {
		s.logger.Warn("name correction e-mail failed", "user_id", db.UUIDString(row.ID), "error", err)
	}
}

// --------------------------- POST /admin/users/{id}/suspend | /activate --

func (s *Server) handleAdminSuspendUser(w http.ResponseWriter, r *http.Request) {
	s.adminSetUserStatus(w, r, userStatusSuspended)
}

func (s *Server) handleAdminActivateUser(w http.ResponseWriter, r *http.Request) {
	s.adminSetUserStatus(w, r, userStatusActive)
}

// adminSetUserStatus is both switches. Suspension revokes every live session of
// the account, whichever audience or org it was issued for — a suspended person
// must be out of the product, not merely unable to sign in again.
func (s *Server) adminSetUserStatus(w http.ResponseWriter, r *http.Request, status string) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.adminUserRow(w, r)
	if !ok {
		return
	}

	var body struct {
		Reason string `json:"reason"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	reason := f.MaxLen("reason", f.Required("reason", strings.TrimSpace(body.Reason)), adminReasonMax)
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	if row.Status == status {
		code := "already_active"
		if status == userStatusSuspended {
			code = "already_suspended"
		}
		conflictCode(w, code, "nothing to change", "this account is already "+status)
		return
	}

	action := audit.ActionAdminUserSuspend
	if status == userStatusActive {
		action = audit.ActionAdminUserActive
	}
	var hashes []string
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.AdminSetUserStatus(r.Context(), sqlc.AdminSetUserStatusParams{
			ID: row.ID, Status: status,
		}); err != nil {
			return err
		}
		if status == userStatusSuspended {
			var err error
			if hashes, err = q.RevokeSessionsForUser(r.Context(), row.ID); err != nil {
				return err
			}
		}
		return audit.Record(r.Context(), q, audit.Entry{
			ActorUserID: p.UserIDString(),
			Action:      action,
			EntityType:  audit.EntityUser,
			EntityID:    db.UUIDString(row.ID),
			Before:      map[string]any{"status": row.Status},
			After:       map[string]any{"status": status, "reason": reason},
		})
	}); err != nil {
		if isNoRows(err) {
			httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such user")
			return
		}
		s.serverError(w, r, "admin.users.status.tx", err)
		return
	}
	// The Redis copies go after the commit: a rolled-back suspension must not
	// leave a cache that locks an active account out.
	s.sessions.EvictCached(r.Context(), hashes)
	s.writeAdminUser(w, r, row.ID, "admin.users.status")
}

// writeAdminUser re-reads one directory row for a mutation's response, so the
// admin app redraws the header from the answer.
func (s *Server) writeAdminUser(w http.ResponseWriter, r *http.Request, id pgtype.UUID, op string) {
	row, err := s.q.AdminGetUser(r.Context(), id)
	if err != nil {
		s.serverError(w, r, op+".reload", err)
		return
	}
	orgs, err := s.adminUserOrgs(r, []pgtype.UUID{id})
	if err != nil {
		s.serverError(w, r, op+".reload.orgs", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"user": toAdminUserRow(sqlc.AdminListUsersRow(row), orgs[db.UUIDString(id)]),
	})
}
