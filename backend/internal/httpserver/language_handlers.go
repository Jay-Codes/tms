package httpserver

import (
	"context"
	"net/http"
	"strings"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/notify"
	"tms/backend/internal/validate"
)

// Phase 13 — language (SPEC §3.2, PLAN2 Phase 13).
//
// A person's language is a fact about the person, not about their landlord:
// `users.locale` drives their screens and every SMS addressed to them, and the
// org's `sms_language` survives only as the default for renters who have never
// said. Both audiences change their own locale here — the renter through
// PATCH /me, the org user through PATCH /org/members/me — and nobody changes
// anybody else's.

// optLocale validates an optional `locale` field: `sw`, `en`, or absent.
// Absent (or blank) returns nil, which the CreateUser query reads as "use the
// column default", `sw`.
func optLocale(f validate.Fields, field, in string) *string {
	v := strings.TrimSpace(in)
	if v == "" {
		return nil
	}
	if v != notify.LangSwahili && v != notify.LangEnglish {
		f.Add(field, "must be one of: sw, en")
		return nil
	}
	return &v
}

// requiredLocale validates the `locale` of a PATCH that exists to set it.
func requiredLocale(f validate.Fields, field string, in *string) string {
	if in == nil {
		f.Add(field, "locale is required")
		return ""
	}
	return f.OneOf(field, strings.TrimSpace(*in), notify.LangSwahili, notify.LangEnglish)
}

// ------------------------------------------------------------- PATCH /me --

// handlePatchMyLocale is the renter's language switch (renter Profile). It is
// deliberately its own endpoint rather than a field on PUT /me/profile: the
// profile is KYC data a landlord may read, and the language is neither.
func (s *Server) handlePatchMyLocale(w http.ResponseWriter, r *http.Request) {
	s.patchLocale(w, r, "me.locale")
}

// ------------------------------------------ PATCH /org/members/me --

// handlePatchMemberLocale is the same switch for a landlord or manager
// (Settings → Preferences). The route is under /org/members because it is the
// member's own row; it names no id, so no member can move another's.
func (s *Server) handlePatchMemberLocale(w http.ResponseWriter, r *http.Request) {
	s.patchLocale(w, r, "org.member.locale")
}

// patchLocale updates the caller's own `users.locale` and audits the change.
func (s *Server) patchLocale(w http.ResponseWriter, r *http.Request, op string) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	// Keyed by the user, because the row being changed is theirs: a landlord
	// with five staff does not spend one budget between them, and a renter
	// cannot spend anybody's but their own.
	if res := s.limiter.Allow(r.Context(), "locale:patch:"+p.UserIDString(),
		localePatchLimit, localePatchWindow); !res.Allowed {
		tooMany(w, res, "too many language changes; try again shortly")
		return
	}

	var body struct {
		Locale *string `json:"locale"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	locale := requiredLocale(f, "locale", body.Locale)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	before, err := s.q.GetUserByID(r.Context(), p.UserID)
	if isNoRows(err) {
		httpx.WriteProblem(w, http.StatusUnauthorized, "unauthenticated", "a valid session is required")
		return
	}
	if err != nil {
		s.serverError(w, r, op+".get", err)
		return
	}

	var updated sqlc.User
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		updated, err = q.SetUserLocale(r.Context(), sqlc.SetUserLocaleParams{
			ID: p.UserID, Locale: locale,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionLocaleUpdate,
			EntityType:  audit.EntityUser,
			EntityID:    p.UserIDString(),
			Before:      map[string]any{"locale": before.Locale},
			After:       map[string]any{"locale": updated.Locale},
		})
	}); err != nil {
		s.serverError(w, r, op+".tx", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"user": toUser(updated)})
}

// ------------------------------------------------------------- resolution --

// recipientLang resolves the language one renter's message is written in: the
// renter's own `users.locale`, then the org's default, then Swahili.
//
// The lookup is one column by primary key, on the same handle (and so inside
// the same transaction) as the notification row it is about to render — the
// language and the message cannot disagree. A user id that no longer resolves
// falls back to the org's setting rather than failing the send: the message is
// already addressed to a phone number.
func (s *Server) recipientLang(ctx context.Context, q *sqlc.Queries, userID, orgLanguage string) string {
	if q == nil || userID == "" {
		return notify.LanguageFor("", orgLanguage)
	}
	id, err := db.ParseUUID(userID)
	if err != nil {
		return notify.LanguageFor("", orgLanguage)
	}
	locale, err := q.GetUserLocale(ctx, id)
	if err != nil {
		return notify.LanguageFor("", orgLanguage)
	}
	return notify.LanguageFor(locale, orgLanguage)
}
