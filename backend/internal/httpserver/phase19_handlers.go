// Phase 19 — identity visibility: the audited NIDA reveal (§19.1) and the name
// corrections a typo needs (§19.3). The platform user directory lives in
// phase19_admin_handlers.go.
//
// The rule the whole phase turns on: a renter's national ID number is masked by
// default, and a full view is an explicit, recorded act. It is therefore a POST
// — never a GET, a query flag or a field that rides along on a card — so the
// number cannot land in a URL, a browser cache, a prefetch, a proxy log or a
// server log line, and every view has an audit row the renter can read back.
package httpserver

import (
	"context"
	"errors"
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
	"tms/backend/internal/notify"
	"tms/backend/internal/validate"
)

// Phase 19 bounds (API.md).
const (
	// nidaRevealReasonMax bounds the free-text note an actor may attach.
	nidaRevealReasonMax = 200
	// nidaRevealLimit / Window: 60 reveals an hour. The budget is the org's,
	// not the member of staff's — the abuse case is a leaked landlord account
	// walking the directory, and a manager and an owner scraping in turn must
	// not get two budgets.
	nidaRevealLimit  = 60
	nidaRevealWindow = time.Hour
	// nidaRevealsShown is how much of the trail the renter's own profile shows.
	nidaRevealsShown = 10

	// fullNameMin / fullNameMax bound every name a rename may write. They are
	// the bounds PUT /me/profile already applies to a renter's own name, so a
	// landlord's correction cannot produce a name the renter could not have
	// typed themselves.
	fullNameMin = 2
	fullNameMax = 80

	// adminReasonMax bounds the reason a platform action must state.
	adminReasonMax = 200
)

// errLastOwner is the refusal that keeps an org from losing its last owner. It
// travels out of the transaction so the demotion rolls back with it.
var errLastOwner = errors.New("org: the last owner cannot be demoted")

func isLastOwner(err error) bool { return errors.Is(err, errLastOwner) }

// actorKindOrg / actorKindAdmin are the two values a reveal's audit row carries,
// and the two the renter's profile reports back.
const (
	actorKindOrg   = "org_user"
	actorKindAdmin = "platform_admin"
)

// ------------------------------------------------------------ name helper --

// cleanFullName trims a submitted name, collapses inner runs of whitespace and
// checks the shared bounds. It is the one place the rule lives, so the four
// rename endpoints cannot drift apart on what a valid name is.
func cleanFullName(f validate.Fields, field, in string) string {
	name := strings.Join(strings.Fields(in), " ")
	if name == "" {
		f.Add(field, field+" is required")
		return ""
	}
	if n := len([]rune(name)); n < fullNameMin || n > fullNameMax {
		f.Add(field, "must be between 2 and 80 characters")
		return ""
	}
	return name
}

// ------------------------------------- POST /renters/{user_id}/nida/reveal --

func (s *Server) handleRevealRenterNIDA(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	// The relationship check runs before anything else and before the limiter
	// is spent: a renter this org has never dealt with is a 404, exactly as on
	// GET /renters/{user_id}, so the endpoint cannot be used to ask whether an
	// account exists somewhere else on the platform.
	row, ok := s.orgRenter(w, r, p)
	if !ok {
		return
	}

	reason, ok := s.revealReason(w, r, false)
	if !ok {
		return
	}
	if res := s.limiter.Allow(r.Context(), "nida:reveal:"+p.OrgIDString(),
		nidaRevealLimit, nidaRevealWindow); !res.Allowed {
		tooMany(w, res, "too many NIDA reveals for this organisation; try again later")
		return
	}

	s.writeNIDAReveal(w, r, nidaRevealRequest{
		OrgID:       p.OrgIDString(),
		ActorUserID: p.UserIDString(),
		ActorKind:   actorKindOrg,
		UserID:      row.UserID,
		FullName:    row.ProfileName,
		Reason:      reason,
		Op:          "renter.nida.reveal",
	})
}

// -------------------------------------- POST /admin/users/{id}/nida/reveal --

func (s *Server) handleAdminRevealNIDA(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	user, ok := s.adminUserRow(w, r)
	if !ok {
		return
	}
	// A platform reveal is a support case, and a support case has a ticket: the
	// reason is required here where it is optional for a landlord, who is
	// looking at their own tenant's card.
	reason, ok := s.revealReason(w, r, true)
	if !ok {
		return
	}
	if res := s.limiter.Allow(r.Context(), "nida:reveal:admin:"+p.UserIDString(),
		nidaRevealLimit, nidaRevealWindow); !res.Allowed {
		tooMany(w, res, "too many NIDA reveals; try again later")
		return
	}
	s.writeNIDAReveal(w, r, nidaRevealRequest{
		// No org_id: the platform is not acting on any tenant's behalf, and a
		// row stamped with an org would put a support lookup into that
		// landlord's own audit page as if they had made it.
		ActorUserID: p.UserIDString(),
		ActorKind:   actorKindAdmin,
		UserID:      user.ID,
		FullName:    user.FullName,
		Reason:      reason,
		Op:          "admin.nida.reveal",
	})
}

// revealReason decodes and validates the optional-or-required reason body. An
// absent body is allowed where the reason is optional, because the landlord's
// confirm sheet may send none.
func (s *Server) revealReason(w http.ResponseWriter, r *http.Request, required bool) (string, bool) {
	var body struct {
		Reason string `json:"reason"`
	}
	// Decoded from the body itself, never from Content-Length: a chunked request
	// reports -1, and a length test would answer "reason is required" to a
	// reveal that carried one.
	if !DecodeJSONOptional(w, r, &body) {
		return "", false
	}
	f := validate.Fields{}
	reason := strings.TrimSpace(body.Reason)
	if required {
		reason = f.Required("reason", reason)
	}
	reason = f.MaxLen("reason", reason, nidaRevealReasonMax)
	if !f.Empty() {
		badRequest(w, f)
		return "", false
	}
	return reason, true
}

// nidaRevealRequest is one reveal, from either audience.
type nidaRevealRequest struct {
	OrgID       string
	ActorUserID string
	ActorKind   string
	UserID      pgtype.UUID
	FullName    string
	Reason      string
	Op          string
}

// writeNIDAReveal decrypts the number, records the reveal and answers. The
// audit row and the answer share a transaction: a reveal the trail does not
// carry must never reach a screen.
func (s *Server) writeNIDAReveal(w http.ResponseWriter, r *http.Request, req nidaRevealRequest) {
	profile, err := s.loadProfile(r.Context(), req.UserID)
	if isNoRows(err) {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no NIDA on file for this renter")
		return
	}
	if err != nil {
		s.serverError(w, r, req.Op+".profile", err)
		return
	}
	if strings.TrimSpace(profile.NidaNumber) == "" {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no NIDA on file for this renter")
		return
	}

	revealedAt := time.Now().UTC()
	after := map[string]any{"actor_kind": req.ActorKind}
	if req.Reason != "" {
		after["reason"] = req.Reason
	}
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       req.OrgID,
			ActorUserID: req.ActorUserID,
			Action:      audit.ActionNidaReveal,
			EntityType:  audit.EntityUser,
			EntityID:    db.UUIDString(req.UserID),
			// `after` carries who looked and why. It never carries the number:
			// the audit trail is read by more people than the reveal is.
			After: after,
		})
	}); err != nil {
		s.serverError(w, r, req.Op+".tx", err)
		return
	}

	name := req.FullName
	if profile.FullName != "" {
		name = profile.FullName
	}
	// No caching of any kind on the one response that carries the number.
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	w.Header().Set("Pragma", "no-cache")
	WriteJSON(w, http.StatusOK, map[string]any{
		"nida_number": profile.NidaNumber,
		"full_name":   name,
		"revealed_at": revealedAt,
	})
}

// nidaRevealEntry is one row of `nida_reveals` on GET /me/profile.
type nidaRevealEntry struct {
	At time.Time `json:"at"`
	// ByKind is the renter-facing word for the actor: their landlord, or the
	// platform. The stored `actor_kind` is the internal vocabulary.
	ByKind  string  `json:"by_kind"`
	OrgName *string `json:"org_name,omitempty"`
}

// myNidaReveals is the renter's own answer to "who has looked at my NIDA?",
// read off the append-only trail rather than a second table that could
// disagree with it.
func (s *Server) myNidaReveals(r *http.Request, userID pgtype.UUID) []nidaRevealEntry {
	rows, err := s.q.ListNidaRevealsForUser(r.Context(), sqlc.ListNidaRevealsForUserParams{
		UserID: userID, RowLimit: nidaRevealsShown,
	})
	if err != nil {
		// The profile screen is not worth a 500 over its footnote.
		s.logger.Warn("nida reveal history unavailable", "error", err)
		return []nidaRevealEntry{}
	}
	out := make([]nidaRevealEntry, 0, len(rows))
	for _, row := range rows {
		entry := nidaRevealEntry{At: row.At.Time, ByKind: "landlord"}
		if row.ActorKind == actorKindAdmin {
			entry.ByKind = "platform_admin"
		} else if row.OrgName != "" {
			name := row.OrgName
			entry.OrgName = &name
		}
		out = append(out, entry)
	}
	return out
}

// ------------------------------------------------- PATCH /org/members/me --

// handlePatchMemberMe is an org user fixing their own display name, their own
// language, or both. It replaces the Phase 13 locale-only handler and keeps its
// body working unchanged: `{locale}` alone still writes `user.locale_update`
// and nothing else.
func (s *Server) handlePatchMemberMe(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		FullName *string `json:"full_name"`
		Locale   *string `json:"locale"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	if body.FullName == nil && body.Locale == nil {
		badRequest(w, validate.Fields{"full_name": "full_name or locale is required"})
		return
	}
	// A locale-only call is the Phase 13 endpoint: same limiter, same audit
	// action, same response. Nothing about that path changes here.
	if body.FullName == nil {
		s.patchLocaleValue(w, r, *body.Locale, "org.member.locale")
		return
	}

	f := validate.Fields{}
	name := cleanFullName(f, "full_name", *body.FullName)
	locale := ""
	if body.Locale != nil {
		locale = requiredLocale(f, "locale", body.Locale)
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	before, err := s.q.GetUserByID(r.Context(), p.UserID)
	if err != nil {
		s.serverError(w, r, "org.member.me.get", err)
		return
	}

	var updated sqlc.User
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		updated, err = q.SetUserFullName(r.Context(), sqlc.SetUserFullNameParams{
			ID: p.UserID, FullName: name,
		})
		if err != nil {
			return err
		}
		if locale != "" && locale != before.Locale {
			if updated, err = q.SetUserLocale(r.Context(), sqlc.SetUserLocaleParams{
				ID: p.UserID, Locale: locale,
			}); err != nil {
				return err
			}
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionMemberUpdate,
			EntityType:  audit.EntityOrgMember,
			EntityID:    p.UserIDString(),
			Before:      map[string]any{"full_name": before.FullName, "locale": before.Locale},
			After:       map[string]any{"full_name": updated.FullName, "locale": updated.Locale},
		})
	}); err != nil {
		s.serverError(w, r, "org.member.me.tx", err)
		return
	}
	s.writeMemberOf(w, r, p.OrgID, updated)
}

// ----------------------------------------------- PATCH /org/members/{id} --

// handlePatchMember is the owner's correction of a colleague's name or role.
func (s *Server) handlePatchMember(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	memberID, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such member")
		return
	}

	var body struct {
		FullName *string `json:"full_name"`
		Role     *string `json:"role"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	if body.FullName == nil && body.Role == nil {
		badRequest(w, validate.Fields{"full_name": "full_name or role is required"})
		return
	}
	f := validate.Fields{}
	name := ""
	if body.FullName != nil {
		name = cleanFullName(f, "full_name", *body.FullName)
	}
	role := ""
	if body.Role != nil {
		role = f.OneOf("role", strings.TrimSpace(*body.Role), auth.RoleOwner, auth.RoleManager)
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	member, err := s.q.GetOrgMember(r.Context(), sqlc.GetOrgMemberParams{OrgID: p.OrgID, ID: memberID})
	if isNoRows(err) {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such member")
		return
	}
	if err != nil {
		s.serverError(w, r, "org.member.patch.get", err)
		return
	}
	// A role change on one's own row is refused outright rather than allowed
	// when harmless: the owner who wants to step back has to be promoted by
	// somebody else, so an org cannot lose its last owner by one person's
	// single request.
	if role != "" && role != member.Role && db.UUIDString(member.UserID) == p.UserIDString() {
		conflictCode(w, "cannot_change_own_role", "cannot change your own role",
			"ask another owner to change your role")
		return
	}

	var updated sqlc.User
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if name != "" {
			var err error
			if updated, err = q.SetUserFullName(r.Context(), sqlc.SetUserFullNameParams{
				ID: member.UserID, FullName: name,
			}); err != nil {
				return err
			}
		}
		if role != "" && role != member.Role {
			// The owner set is locked *before* the write: the guard is a
			// read-then-write across different rows, so the row's own UPDATE
			// serialises nothing against a concurrent demotion of the other
			// owner. With the lock held, the second transaction waits and then
			// counts the first one's demotion.
			if _, err := q.LockOrgOwners(r.Context(), p.OrgID); err != nil {
				return err
			}
			if _, err := q.SetOrgMemberRole(r.Context(), sqlc.SetOrgMemberRoleParams{
				OrgID: p.OrgID, ID: memberID, Role: role,
			}); err != nil {
				return err
			}
			owners, err := q.CountActiveOwners(r.Context(), p.OrgID)
			if err != nil {
				return err
			}
			if owners == 0 {
				return errLastOwner
			}
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionMemberUpdate,
			EntityType:  audit.EntityOrgMember,
			EntityID:    db.UUIDString(memberID),
			Before:      map[string]any{"full_name": member.FullName, "role": member.Role},
			After:       memberAfter(member, name, role),
		})
	}); err != nil {
		if isLastOwner(err) {
			conflictCode(w, "last_owner", "the last owner cannot be demoted",
				"promote another member to owner first")
			return
		}
		s.serverError(w, r, "org.member.patch.tx", err)
		return
	}
	if !updated.ID.Valid {
		var err error
		if updated, err = s.q.GetUserByID(r.Context(), member.UserID); err != nil {
			s.serverError(w, r, "org.member.patch.reload", err)
			return
		}
	}
	s.writeMemberOf(w, r, p.OrgID, updated)
}

// memberAfter renders the audit `after` of a member patch, leaving untouched
// fields reading as they were rather than as empty strings.
func memberAfter(before sqlc.GetOrgMemberRow, name, role string) map[string]any {
	out := map[string]any{"full_name": before.FullName, "role": before.Role}
	if name != "" {
		out["full_name"] = name
	}
	if role != "" {
		out["role"] = role
	}
	return out
}

// writeMemberOf answers a member patch with the row the staff list renders, so
// the client never has to re-read the list to redraw one line. `user` rides
// beside it because the Phase 13 locale response was `{user}` and clients
// written against it keep working.
func (s *Server) writeMemberOf(w http.ResponseWriter, r *http.Request, orgID pgtype.UUID, user sqlc.User) {
	out := memberResponse{
		UserID: db.UUIDString(user.ID), Email: user.Email,
		FullName: user.FullName, Locale: user.Locale, Status: user.Status,
		CreatedAt: user.CreatedAt.Time,
	}
	if m, err := s.q.GetOrgMemberByUser(r.Context(), sqlc.GetOrgMemberByUserParams{
		OrgID: orgID, UserID: user.ID,
	}); err == nil {
		out.ID = db.UUIDString(m.ID)
		out.Role = m.Role
		out.Status = m.Status
		out.CreatedAt = m.CreatedAt.Time
	}
	WriteJSON(w, http.StatusOK, map[string]any{"member": out, "user": toUser(user)})
}

// ------------------------------------------------ PATCH /renters/{user_id} --

// handlePatchRenter is a landlord correcting a renter's name — a CSV import
// typo, or a name misheard across a desk during in-person onboarding.
//
// Before any signature the correction is free. Once the renter has signed with
// this org (Phase 21) it still goes through, but with a reason on the audit row
// and an SMS the org cannot switch off — the renter must learn that the name on
// their account moved. The signed document itself never changes: its terms are
// a snapshot. A signature with **another** org still refuses (409
// `renter_signed_elsewhere`): that name sits on a document this landlord does
// not hold, and only the renter may change it from their Profile.
func (s *Server) handlePatchRenter(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.orgRenter(w, r, p)
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
	reason := f.MaxLen("reason", strings.TrimSpace(body.Reason), adminReasonMax)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	elsewhere, err := s.q.RenterSignedElsewhere(r.Context(), sqlc.RenterSignedElsewhereParams{
		UserID: row.UserID, OrgID: p.OrgID,
	})
	if err != nil {
		s.serverError(w, r, "renter.patch.elsewhere", err)
		return
	}
	if elsewhere {
		conflictCode(w, "renter_signed_elsewhere", "this renter has signed with another landlord",
			"ask the renter to correct it in their Profile")
		return
	}
	signed, err := s.q.RenterHasSignedAnywhere(r.Context(), row.UserID)
	if err != nil {
		s.serverError(w, r, "renter.patch.signed", err)
		return
	}
	if signed && reason == "" {
		f.Add("reason", "required once the renter has signed a contract")
		badRequest(w, f)
		return
	}

	org, err := s.q.GetOrg(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "renter.patch.org", err)
		return
	}
	settings := parseSettings(org.Settings)
	brand := s.brandingAssets(r.Context(), p.OrgID, org.Name)

	var notifyID string
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		updated, err := q.SetUserFullName(r.Context(), sqlc.SetUserFullNameParams{
			ID: row.UserID, FullName: name,
		})
		if err != nil {
			return err
		}
		// Both halves or neither: the directory reads `renter_profiles.full_name`
		// where the profile is joined and `users.full_name` where it is not, so
		// leaving them apart shows one person under two names.
		if _, err := q.SetRenterProfileFullName(r.Context(), sqlc.SetRenterProfileFullNameParams{
			UserID: row.UserID, FullName: name,
		}); err != nil && !isNoRows(err) {
			return err
		}
		if err := audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionRenterUpdate,
			EntityType:  audit.EntityUser,
			EntityID:    db.UUIDString(row.UserID),
			Before:      map[string]any{"full_name": row.ProfileName},
			After:       renameAfter(updated.FullName, reason, signed),
		}); err != nil {
			return err
		}
		notifyID, err = s.queueNameCorrected(r.Context(), q, nameCorrectedMessage{
			OrgID: p.OrgIDString(), UserID: db.UUIDString(row.UserID),
			Phone: db.StrVal(row.Phone), Name: name, OrgName: brand.DisplayName,
			Lang: settings.SMSLanguage, Overrides: settings.notifyOverrides(),
			// After a signature the renter is always told, whatever the toggle says.
			Enabled: signed || notificationSettingsOf(settings).Kinds.NameCorrectedEnabled(),
		})
		return err
	}); err != nil {
		s.serverError(w, r, "renter.patch.tx", err)
		return
	}
	s.enqueueNotifications(r.Context(), notifyID)

	// The response is the renter detail's own two blocks, so the tenant app
	// redraws the card from the answer rather than re-fetching it.
	s.handleGetRenter(w, r)
}

// renameAfter is the audit `after` of a landlord rename: the reason and the
// signed flag ride along only when there is something to say.
func renameAfter(name, reason string, signed bool) map[string]any {
	after := map[string]any{"full_name": name}
	if reason != "" {
		after["reason"] = reason
	}
	if signed {
		after["after_signature"] = true
	}
	return after
}

// nameCorrectedMessage carries what the `name_corrected` SMS needs across the
// transaction boundary.
type nameCorrectedMessage struct {
	OrgID     string
	UserID    string
	Phone     string
	Name      string
	OrgName   string
	Lang      string
	Overrides notify.Overrides
	Enabled   bool
}

// queueNameCorrected writes the notification row inside the caller's
// transaction. The dedupe key carries the new name, so a second correction is a
// second message and correcting a name back to what it was is not.
func (s *Server) queueNameCorrected(
	ctx context.Context, q *sqlc.Queries, m nameCorrectedMessage,
) (string, error) {
	if !m.Enabled || m.Phone == "" {
		return "", nil
	}
	lang := s.recipientLang(ctx, q, m.UserID, m.Lang)
	id, err := notify.Queue(ctx, q, notify.Msg{
		OrgID: m.OrgID, UserID: m.UserID, Kind: notify.KindNameCorrected,
		DedupeKey: notify.KindNameCorrected + ":" + m.UserID + ":" + m.Name,
		Phone:     m.Phone,
		Body: notify.Render(notify.KindNameCorrected, lang, notify.Vars{
			Name: m.Name, Org: m.OrgName,
			Link:    s.cfg.EnduserURL() + "/profile",
			PayLink: notify.PayLink(s.cfg.EnduserURL()),
		}, m.Overrides),
		Language: lang,
	})
	if errors.Is(err, notify.ErrDuplicate) {
		return "", nil
	}
	return id, err
}
