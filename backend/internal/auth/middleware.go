package auth

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/db"
	"tms/backend/internal/httpx"
)

type ctxKey int

const principalKey ctxKey = iota

// Principal is the authenticated caller resolved from the audience cookie.
// OrgID is set only for org users; Role only for org members.
type Principal struct {
	UserID   pgtype.UUID
	Kind     string
	OrgID    pgtype.UUID
	Role     string
	Audience string
	Token    string
}

// HasRole reports whether the principal holds one of the given org roles. An
// empty role list means "any authenticated member of the audience".
func (p Principal) HasRole(roles ...string) bool {
	if len(roles) == 0 {
		return true
	}
	for _, r := range roles {
		if p.Role == r {
			return true
		}
	}
	return false
}

// OrgIDString renders the principal's org id ("" when not org-scoped).
func (p Principal) OrgIDString() string { return db.UUIDString(p.OrgID) }

// UserIDString renders the principal's user id.
func (p Principal) UserIDString() string { return db.UUIDString(p.UserID) }

// WithPrincipal stores a principal in the request context.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// FromContext retrieves the principal placed by the Require* middleware.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}

// MustFromContext retrieves the principal, panicking if the route was not
// wrapped in a Require* middleware (a programming error, not a runtime one).
func MustFromContext(ctx context.Context) Principal {
	p, ok := FromContext(ctx)
	if !ok {
		panic("auth: no principal in context — route is missing a Require* middleware")
	}
	return p
}

// Resolve turns the audience cookie on a request into a Principal.
func (m *Manager) Resolve(r *http.Request, audience string) (Principal, bool) {
	token := TokenFrom(r, audience)
	if token == "" {
		return Principal{}, false
	}
	s, err := m.Lookup(r.Context(), token)
	if err != nil || s.Audience != audience {
		return Principal{}, false
	}
	userID, err := db.ParseUUID(s.UserID)
	if err != nil {
		return Principal{}, false
	}
	p := Principal{
		UserID:   userID,
		Kind:     s.Kind,
		Role:     s.Role,
		Audience: s.Audience,
		Token:    token,
	}
	if s.OrgID != "" {
		if org, err := db.ParseUUID(s.OrgID); err == nil {
			p.OrgID = org
		}
	}
	return p, true
}

// require builds middleware for an audience, optionally restricted to roles.
//
// 401 = no or invalid session for this audience; 403 = authenticated but the
// wrong role. Cross-org access never reaches here — handlers scope by org_id
// and return 404 (API.md).
func (m *Manager) require(audience, wantKind string, roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := m.Resolve(r, audience)
			if !ok || p.Kind != wantKind {
				httpx.WriteProblem(w, http.StatusUnauthorized, "unauthenticated", "a valid session is required")
				return
			}
			if !p.HasRole(roles...) {
				httpx.WriteProblem(w, http.StatusForbidden, "forbidden", "your role is not permitted to perform this action")
				return
			}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
		})
	}
}

// RequireRenter admits renters holding a valid `tms_r` session.
func (m *Manager) RequireRenter() func(http.Handler) http.Handler {
	return m.require(AudienceRenter, KindRenter)
}

// RequireOrg admits org users holding a valid `tms_o` session. Passing roles
// restricts the route further (e.g. RequireOrg(RoleOwner)).
func (m *Manager) RequireOrg(roles ...string) func(http.Handler) http.Handler {
	return m.require(AudienceOrg, KindOrgUser, roles...)
}

// RequireAdmin admits platform admins holding a valid `tms_a` session.
func (m *Manager) RequireAdmin() func(http.Handler) http.Handler {
	return m.require(AudienceAdmin, KindPlatformAdmin)
}
