package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/contract"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/expense"
	"tms/backend/internal/httpx"
	"tms/backend/internal/validate"
)

// seededPeriod is one of the payment periods every new org is bootstrapped
// with (SPEC §4: 30 / 90 / 180 / 365 days).
//
// Exactly one of them carries the "Recommended" badge — Monthly, until the
// landlord moves it with POST /org/payment-periods/{id}/recommend. The other
// three are presets: offered, restorable, unbadged. A badge on all four told
// the renter nothing (PLAN2 #8), and migration 000012's partial unique index
// now makes a second recommended period impossible.
type seededPeriod struct {
	label       string
	days        int32
	recommended bool
}

//nolint:gochecknoglobals // fixed product data, not configuration
var recommendedPeriods = []seededPeriod{
	{"Monthly", 30, true},
	{"Quarterly", 90, false},
	{"Half-year", 180, false},
	{"Yearly", 365, false},
}

// ------------------------------------------------------------- POST /orgs --

func (s *Server) handleCreateOrg(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	var body struct {
		OrgName   string `json:"org_name"`
		OwnerName string `json:"owner_name"`
		Email     string `json:"email"`
		Phone     string `json:"phone"`
		Password  string `json:"password"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}

	ip := audit.RequestInfoFrom(r.Context()).IP
	if res := s.limiter.Allow(r.Context(), "orgs:create:"+ip, orgCreateLimit, orgCreateWindow); !res.Allowed {
		tooMany(w, res, "too many organisations created from this address")
		return
	}

	f := validate.Fields{}
	orgName := f.MaxLen("org_name", f.Required("org_name", body.OrgName), 120)
	ownerName := f.MaxLen("owner_name", f.Required("owner_name", body.OwnerName), 120)
	email := f.Email("email", body.Email)
	phone := f.Phone("phone", body.Phone)
	f.Password("password", body.Password)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	if _, err := s.q.GetUserByEmail(r.Context(), email); err == nil {
		httpx.WriteProblem(w, http.StatusConflict, "already registered", "an account already exists for this email address")
		return
	} else if !isNoRows(err) {
		s.serverError(w, r, "orgs.lookup", err)
		return
	}

	slug, err := s.uniqueSlug(r.Context(), orgName)
	if err != nil {
		s.serverError(w, r, "orgs.slug", err)
		return
	}
	passwordHash, err := auth.HashSecret(body.Password)
	if err != nil {
		s.serverError(w, r, "orgs.hash", err)
		return
	}
	settings, err := json.Marshal(DefaultOrgSettings())
	if err != nil {
		s.serverError(w, r, "orgs.settings", err)
		return
	}

	var (
		org     sqlc.Org
		owner   sqlc.User
		token   string
		session auth.Session
	)
	err = s.inTx(r.Context(), func(q *sqlc.Queries) error {
		org, err = q.CreateOrg(r.Context(), sqlc.CreateOrgParams{Name: orgName, Slug: slug, Settings: settings})
		if err != nil {
			return err
		}
		owner, err = q.CreateUser(r.Context(), sqlc.CreateUserParams{
			Kind:         auth.KindOrgUser,
			Phone:        &phone,
			Email:        &email,
			FullName:     ownerName,
			PasswordHash: &passwordHash,
		})
		if err != nil {
			return err
		}
		if _, err := q.CreateOrgMember(r.Context(), sqlc.CreateOrgMemberParams{
			OrgID: org.ID, UserID: owner.ID, Role: auth.RoleOwner,
		}); err != nil {
			return err
		}
		if _, err := q.CreateOrgBranding(r.Context(), sqlc.CreateOrgBrandingParams{
			OrgID: org.ID, DisplayName: orgName,
		}); err != nil {
			return err
		}
		// Every org starts with terms it can actually issue a contract from.
		// Migration 000005 seeds the byte-identical body for orgs that already
		// existed, so old and new orgs agree on what "standard" means.
		if _, err := q.CreateContractTemplate(r.Context(), sqlc.CreateContractTemplateParams{
			OrgID: org.ID, Name: contract.DefaultTemplateName,
			BodyHtml: contract.DefaultTemplateBody, IsDefault: true,
		}); err != nil {
			return err
		}
		for i, p := range recommendedPeriods {
			if _, err := q.CreatePaymentPeriod(r.Context(), sqlc.CreatePaymentPeriodParams{
				OrgID:         org.ID,
				Label:         p.label,
				Days:          p.days,
				IsRecommended: p.recommended,
				SortOrder:     int32(i + 1),
			}); err != nil {
				return err
			}
		}
		// The expense ledger's vocabulary, seeded like the payment periods
		// above so the Record-expense sheet has a category picker from the
		// org's first minute (PLAN2 Phase 10).
		if err := expense.SeedCategories(r.Context(), q, org.ID); err != nil {
			return err
		}
		if err := audit.Record(r.Context(), q, audit.Entry{
			OrgID:       db.UUIDString(org.ID),
			ActorUserID: db.UUIDString(owner.ID),
			Action:      audit.ActionOrgCreate,
			EntityType:  audit.EntityOrg,
			EntityID:    db.UUIDString(org.ID),
			After:       map[string]any{"name": orgName, "slug": slug, "owner_email": email},
		}); err != nil {
			return err
		}
		token, session, err = s.sessions.Issue(r.Context(), q, owner.ID, auth.KindOrgUser,
			db.UUIDString(org.ID), auth.RoleOwner, ip, r.UserAgent())
		return err
	})
	if isUnique(err) {
		httpx.WriteProblem(w, http.StatusConflict, "already registered", "an account already exists for this email or phone")
		return
	}
	if err != nil {
		s.serverError(w, r, "orgs.tx", err)
		return
	}

	s.sessions.Cache(r.Context(), session)
	s.sessions.SetCookie(w, auth.AudienceOrg, token)

	if err := s.sendVerificationEmail(r.Context(), owner); err != nil {
		s.logger.Warn("verification email not sent", "error", err)
	}

	WriteJSON(w, http.StatusCreated, map[string]any{"org": toOrg(org), "user": toUser(owner)})
}

// uniqueSlug derives a URL slug from the org name, appending -2, -3 … on
// collision.
func (s *Server) uniqueSlug(ctx context.Context, name string) (string, error) {
	base := validate.Slugify(name)
	for i := 1; i <= 50; i++ {
		candidate := base
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", base, i)
		}
		exists, err := s.q.OrgSlugExists(ctx, candidate)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
	return fmt.Sprintf("%s-%d", base, time.Now().UnixNano()%100000), nil
}

// --------------------------------------------------------------- GET /org --

func (s *Server) handleGetOrg(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	org, err := s.q.GetOrg(r.Context(), p.OrgID)
	if isNoRows(err) {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such organisation")
		return
	}
	if err != nil {
		s.serverError(w, r, "org.get", err)
		return
	}
	WriteJSON(w, http.StatusOK, toOrg(org))
}

// ------------------------------------------------------------- PATCH /org --

// orgSettingsPatch mirrors OrgSettings with pointers so an absent key is
// distinguishable from an explicit null/zero value.
type orgSettingsPatch struct {
	AutoApproveLinks     *bool            `json:"auto_approve_links"`
	DueDay               *json.RawMessage `json:"due_day"`
	GraceDays            *int             `json:"grace_days"`
	ReminderOffsetsDays  *[]int           `json:"reminder_offsets_days"`
	UnsignedReminderDays *int             `json:"unsigned_reminder_days"`
	SMSLanguage          *string          `json:"sms_language"`
}

// apply merges the patch onto current settings, recording validation errors.
func (p orgSettingsPatch) apply(cur OrgSettings, f validate.Fields) OrgSettings {
	if p.AutoApproveLinks != nil {
		cur.AutoApproveLinks = *p.AutoApproveLinks
	}
	if p.DueDay != nil {
		var v *int
		if err := json.Unmarshal(*p.DueDay, &v); err != nil {
			f.Add("settings.due_day", "must be a whole number between 1 and 28, or null")
		} else if v != nil && (*v < 1 || *v > 28) {
			f.Add("settings.due_day", "must be between 1 and 28, or null")
		} else {
			cur.DueDay = v
		}
	}
	if p.GraceDays != nil {
		if *p.GraceDays < 0 || *p.GraceDays > 30 {
			f.Add("settings.grace_days", "must be between 0 and 30")
		} else {
			cur.GraceDays = *p.GraceDays
		}
	}
	if p.ReminderOffsetsDays != nil {
		offsets := *p.ReminderOffsetsDays
		if len(offsets) > 10 {
			f.Add("settings.reminder_offsets_days", "at most 10 offsets")
		}
		for _, o := range offsets {
			if o < 0 || o > 365 {
				f.Add("settings.reminder_offsets_days", "each offset must be between 0 and 365 days")
				break
			}
		}
		if f.Empty() {
			cur.ReminderOffsetsDays = offsets
		}
	}
	if p.UnsignedReminderDays != nil {
		if *p.UnsignedReminderDays < 0 || *p.UnsignedReminderDays > 90 {
			f.Add("settings.unsigned_reminder_days", "must be between 0 and 90")
		} else {
			cur.UnsignedReminderDays = *p.UnsignedReminderDays
		}
	}
	if p.SMSLanguage != nil {
		if *p.SMSLanguage != "sw" && *p.SMSLanguage != "en" {
			f.Add("settings.sms_language", "must be one of: sw, en")
		} else {
			cur.SMSLanguage = *p.SMSLanguage
		}
	}
	return cur
}

func (s *Server) handlePatchOrg(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		Name     *string           `json:"name"`
		Settings *orgSettingsPatch `json:"settings"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}

	current, err := s.q.GetOrg(r.Context(), p.OrgID)
	if isNoRows(err) {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such organisation")
		return
	}
	if err != nil {
		s.serverError(w, r, "org.patch.get", err)
		return
	}

	f := validate.Fields{}
	var namePtr *string
	if body.Name != nil {
		name := f.MaxLen("name", f.Required("name", *body.Name), 120)
		namePtr = &name
	}

	settingsJSON := []byte(nil)
	newSettings := parseSettings(current.Settings)
	if body.Settings != nil {
		newSettings = body.Settings.apply(newSettings, f)
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	if body.Settings != nil {
		if settingsJSON, err = json.Marshal(newSettings); err != nil {
			s.serverError(w, r, "org.patch.marshal", err)
			return
		}
	}

	var updated sqlc.Org
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		updated, err = q.UpdateOrg(r.Context(), sqlc.UpdateOrgParams{
			ID: p.OrgID, Name: namePtr, Settings: settingsJSON,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionOrgUpdate,
			EntityType:  audit.EntityOrg,
			EntityID:    p.OrgIDString(),
			Before:      toOrg(current),
			After:       toOrg(updated),
		})
	}); err != nil {
		s.serverError(w, r, "org.patch.tx", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"org": toOrg(updated)})
}

// ----------------------------------------------------------- org members --

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	rows, err := s.q.ListOrgMembers(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "members.list", err)
		return
	}
	items := make([]memberResponse, 0, len(rows))
	for _, m := range rows {
		items = append(items, memberResponse{
			ID:        db.UUIDString(m.ID),
			UserID:    db.UUIDString(m.UserID),
			Email:     m.Email,
			FullName:  m.FullName,
			Role:      m.Role,
			Status:    m.Status,
			CreatedAt: m.CreatedAt.Time,
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCreateMember(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		Email    string `json:"email"`
		FullName string `json:"full_name"`
		Role     string `json:"role"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	email := f.Email("email", body.Email)
	fullName := f.MaxLen("full_name", f.Required("full_name", body.FullName), 120)
	role := f.OneOf("role", body.Role, auth.RoleOwner, auth.RoleManager)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	if _, err := s.q.GetUserByEmail(r.Context(), email); err == nil {
		httpx.WriteProblem(w, http.StatusConflict, "already registered", "a user already exists with this email address")
		return
	} else if !isNoRows(err) {
		s.serverError(w, r, "members.lookup", err)
		return
	}

	// Invited staff get a random password they never learn; the invite link
	// lets them set their own (POST /auth/invite/accept).
	tempHash, err := auth.HashSecret(auth.RandomPassword())
	if err != nil {
		s.serverError(w, r, "members.hash", err)
		return
	}

	var (
		user   sqlc.User
		member sqlc.OrgMember
	)
	err = s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		user, err = q.CreateUser(r.Context(), sqlc.CreateUserParams{
			Kind: auth.KindOrgUser, Email: &email, FullName: fullName, PasswordHash: &tempHash,
		})
		if err != nil {
			return err
		}
		member, err = q.CreateOrgMember(r.Context(), sqlc.CreateOrgMemberParams{
			OrgID: p.OrgID, UserID: user.ID, Role: role,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionMemberInvite,
			EntityType:  audit.EntityOrgMember,
			EntityID:    db.UUIDString(member.ID),
			After:       map[string]any{"email": email, "full_name": fullName, "role": role},
		})
	})
	if isUnique(err) {
		httpx.WriteProblem(w, http.StatusConflict, "already registered", "a user already exists with this email address")
		return
	}
	if err != nil {
		s.serverError(w, r, "members.tx", err)
		return
	}

	invite := map[string]any{"sent": false}
	token, tokErr := s.store.PutToken(r.Context(), auth.PrefixInvite, db.UUIDString(user.ID), auth.InviteTTL)
	if tokErr != nil {
		s.logger.Warn("invite token not issued (cache unavailable)", "error", tokErr)
	} else {
		link := fmt.Sprintf("%s/tenant/invite?token=%s", s.cfg.AppBaseURL, token)
		if _, mailErr := s.deps.Email.Send(r.Context(), email,
			"You have been invited to TMS", link,
			fmt.Sprintf("%s invited you to join their team on TMS. Set your password to continue.", fullName)); mailErr != nil {
			s.logger.Warn("invite email not sent", "error", mailErr)
		}
		invite = map[string]any{
			"sent":       true,
			"link":       link,
			"expires_at": time.Now().Add(auth.InviteTTL).UTC(),
		}
	}

	WriteJSON(w, http.StatusCreated, map[string]any{
		"member": memberResponse{
			ID:        db.UUIDString(member.ID),
			UserID:    db.UUIDString(user.ID),
			Email:     user.Email,
			FullName:  user.FullName,
			Role:      member.Role,
			Status:    member.Status,
			CreatedAt: member.CreatedAt.Time,
		},
		"invite": invite,
	})
}

func (s *Server) handleDeleteMember(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	memberID, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		// A malformed id cannot name a member of this org: 404, never 400,
		// so ids are not probeable across orgs (API.md).
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such member")
		return
	}

	// Org scope: the lookup is filtered by org_id, so a member of another org
	// is indistinguishable from one that does not exist.
	existing, err := s.q.GetOrgMember(r.Context(), sqlc.GetOrgMemberParams{OrgID: p.OrgID, ID: memberID})
	if isNoRows(err) {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such member")
		return
	}
	if err != nil {
		s.serverError(w, r, "members.get", err)
		return
	}

	if existing.Role == auth.RoleOwner {
		owners, err := s.q.CountActiveOwners(r.Context(), p.OrgID)
		if err != nil {
			s.serverError(w, r, "members.owners", err)
			return
		}
		if owners <= 1 {
			httpx.WriteProblem(w, http.StatusConflict, "last owner",
				"an organisation must always have at least one owner")
			return
		}
	}

	var revoked []string
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.SoftDeleteOrgMember(r.Context(), sqlc.SoftDeleteOrgMemberParams{
			OrgID: p.OrgID, ID: memberID,
		}); err != nil {
			return err
		}
		// A removed member must lose access immediately: revoke their live
		// sessions for this org in the same transaction as the removal.
		var err error
		revoked, err = q.RevokeSessionsForOrgUser(r.Context(), sqlc.RevokeSessionsForOrgUserParams{
			UserID: existing.UserID, OrgID: p.OrgID,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionMemberRemove,
			EntityType:  audit.EntityOrgMember,
			EntityID:    db.UUIDString(memberID),
			Before: map[string]any{
				"email": db.StrVal(existing.Email), "role": existing.Role, "full_name": existing.FullName,
			},
		})
	}); err != nil {
		s.serverError(w, r, "members.delete.tx", err)
		return
	}
	s.sessions.EvictCached(r.Context(), revoked)
	NoContent(w)
}
