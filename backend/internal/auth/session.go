package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
)

// Audiences. One session cookie per audience so one browser can hold a renter
// and a landlord session at the same time (DECISIONS.md).
const (
	AudienceRenter = "renter"
	AudienceOrg    = "org"
	AudienceAdmin  = "admin"
)

// Cookie names per audience (API.md).
const (
	CookieRenter = "tms_r"
	CookieOrg    = "tms_o"
	CookieAdmin  = "tms_a"
)

// User kinds (users.kind).
const (
	KindRenter        = "renter"
	KindOrgUser       = "org_user"
	KindPlatformAdmin = "platform_admin"
)

// Org roles (org_members.role).
const (
	RoleOwner   = "org_owner"
	RoleManager = "org_manager"
)

// ErrNoSession means no valid session backs the presented token.
var ErrNoSession = errors.New("auth: no session")

// CookieName maps an audience to its cookie name.
func CookieName(audience string) string {
	switch audience {
	case AudienceOrg:
		return CookieOrg
	case AudienceAdmin:
		return CookieAdmin
	default:
		return CookieRenter
	}
}

// AudienceForKind is the audience a user of the given kind authenticates into.
func AudienceForKind(kind string) string {
	switch kind {
	case KindOrgUser:
		return AudienceOrg
	case KindPlatformAdmin:
		return AudienceAdmin
	default:
		return AudienceRenter
	}
}

// Session is the resolved session payload. It is stored in Redis as JSON and
// mirrored into the Postgres `sessions` table so a Redis flush does not log
// everyone out (SPEC §3).
type Session struct {
	TokenHash string    `json:"token_hash"`
	UserID    string    `json:"user_id"`
	Kind      string    `json:"kind"`
	OrgID     string    `json:"org_id,omitempty"`
	Role      string    `json:"role,omitempty"`
	Audience  string    `json:"audience"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Manager issues, resolves and revokes sessions.
type Manager struct {
	Q      *sqlc.Queries
	Redis  *redis.Client
	TTL    time.Duration
	Secure bool
	Logger *slog.Logger
}

func (m *Manager) logger() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

// NewToken returns a fresh opaque token (32 random bytes, base64url) together
// with its sha256 hex hash — only the hash is ever persisted.
func NewToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("auth: read token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// HashToken returns the sha256 hex digest of an opaque session token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Issue persists a new session row using the supplied queries handle (pass a
// transaction-bound handle to make the session atomic with the mutation that
// created it) and returns the plaintext token plus the session payload.
//
// Redis is intentionally not written here: call Cache after the transaction
// commits so a rollback cannot leave a phantom session in the cache.
func (m *Manager) Issue(ctx context.Context, q *sqlc.Queries, userID pgtype.UUID, kind, orgID, role, ip, userAgent string) (string, Session, error) {
	token, hash, err := NewToken()
	if err != nil {
		return "", Session{}, err
	}
	audience := AudienceForKind(kind)
	expires := time.Now().Add(m.TTL)

	var org pgtype.UUID
	if orgID != "" {
		if org, err = db.ParseUUID(orgID); err != nil {
			return "", Session{}, err
		}
	}

	if _, err := q.CreateSession(ctx, sqlc.CreateSessionParams{
		TokenHash: hash,
		UserID:    userID,
		OrgID:     org,
		Audience:  audience,
		Role:      db.Str(role),
		Ip:        db.Str(ip),
		UserAgent: db.Str(userAgent),
		ExpiresAt: db.TS(expires),
	}); err != nil {
		return "", Session{}, fmt.Errorf("auth: create session: %w", err)
	}

	return token, Session{
		TokenHash: hash,
		UserID:    db.UUIDString(userID),
		Kind:      kind,
		OrgID:     orgID,
		Role:      role,
		Audience:  audience,
		ExpiresAt: expires,
	}, nil
}

// Cache writes the session into Redis (best effort — Redis is ephemeral).
func (m *Manager) Cache(ctx context.Context, s Session) {
	if m.Redis == nil {
		return
	}
	payload, err := json.Marshal(s)
	if err != nil {
		return
	}
	ttl := time.Until(s.ExpiresAt)
	if ttl <= 0 {
		return
	}
	if err := m.Redis.Set(ctx, redisKey(s.TokenHash), payload, ttl).Err(); err != nil {
		m.logger().Warn("session cache write failed; falling back to postgres", "error", err)
	}
}

// Lookup resolves an opaque token. Redis is the primary store; on a miss (or
// with Redis down) it falls back to Postgres and repopulates the cache.
func (m *Manager) Lookup(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, ErrNoSession
	}
	hash := HashToken(token)

	if m.Redis != nil {
		raw, err := m.Redis.Get(ctx, redisKey(hash)).Bytes()
		switch {
		case err == nil:
			var s Session
			if jsonErr := json.Unmarshal(raw, &s); jsonErr == nil && s.ExpiresAt.After(time.Now()) {
				return s, nil
			}
		case errors.Is(err, redis.Nil):
			// cache miss — fall through to Postgres
		default:
			m.logger().Warn("session cache read failed; falling back to postgres", "error", err)
		}
	}

	if m.Q == nil {
		return Session{}, ErrNoSession
	}
	row, err := m.Q.GetSessionByHash(ctx, hash)
	if err != nil {
		return Session{}, ErrNoSession
	}
	user, err := m.Q.GetUserByID(ctx, row.UserID)
	if err != nil {
		return Session{}, ErrNoSession
	}
	s := Session{
		TokenHash: row.TokenHash,
		UserID:    db.UUIDString(row.UserID),
		Kind:      user.Kind,
		OrgID:     db.UUIDString(row.OrgID),
		Role:      db.StrVal(row.Role),
		Audience:  row.Audience,
		ExpiresAt: row.ExpiresAt.Time,
	}
	m.Cache(ctx, s)
	return s, nil
}

// Revoke invalidates a token in both stores.
func (m *Manager) Revoke(ctx context.Context, token string) {
	if token == "" {
		return
	}
	hash := HashToken(token)
	if m.Redis != nil {
		if err := m.Redis.Del(ctx, redisKey(hash)).Err(); err != nil {
			m.logger().Warn("session cache delete failed", "error", err)
		}
	}
	if m.Q != nil {
		if err := m.Q.RevokeSession(ctx, hash); err != nil {
			m.logger().Warn("session revoke failed", "error", err)
		}
	}
}

// EvictCached drops cached copies of already-revoked sessions from Redis. The
// Postgres rows are revoked by the caller's transaction (so the revocation
// shares the fate of the mutation that caused it); this only clears the cache,
// which would otherwise keep answering Lookup until the TTL expires.
func (m *Manager) EvictCached(ctx context.Context, tokenHashes []string) {
	if m.Redis == nil || len(tokenHashes) == 0 {
		return
	}
	keys := make([]string, 0, len(tokenHashes))
	for _, h := range tokenHashes {
		keys = append(keys, redisKey(h))
	}
	if err := m.Redis.Del(ctx, keys...).Err(); err != nil {
		m.logger().Warn("session cache eviction failed", "error", err)
	}
}

// SetCookie writes the audience cookie carrying the opaque token.
func (m *Manager) SetCookie(w http.ResponseWriter, audience, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName(audience),
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(m.TTL.Seconds()),
	})
}

// ClearCookie expires the audience cookie.
func (m *Manager) ClearCookie(w http.ResponseWriter, audience string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName(audience),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   m.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// TokenFrom reads the opaque token for an audience out of the request.
func TokenFrom(r *http.Request, audience string) string {
	c, err := r.Cookie(CookieName(audience))
	if err != nil || c == nil {
		return ""
	}
	return c.Value
}

func redisKey(hash string) string { return "session:" + hash }
