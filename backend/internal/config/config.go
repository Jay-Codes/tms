// Package config loads application configuration from environment variables.
//
// Configuration is read once at startup (see Load). Every value has a
// dev-friendly default so the API can boot with an empty environment.
package config

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Env names the deployment environment.
type Env string

const (
	EnvDev  Env = "dev"
	EnvProd Env = "prod"
)

// DefaultTrustedProxyCIDRs is the TRUSTED_PROXY_CIDRS default: loopback only,
// i.e. the dev Go proxy is the only hop allowed to set forwarded-for headers.
const DefaultTrustedProxyCIDRs = "127.0.0.0/8,::1/128"

// DefaultDBMaxConns is the DB_MAX_CONNS default: the pgx pool size. Every
// request holds a connection for the length of its queries, so this caps
// in-flight database work; size it against Postgres max_connections.
const DefaultDBMaxConns = 20

// DefaultNotifyWorkers is the NOTIFY_WORKERS default: three SMS workers drain
// the queue in parallel (API.md Phase 6).
const DefaultNotifyWorkers = 3

// Config holds all runtime configuration for the API binary.
type Config struct {
	Env  Env
	Port string

	DatabaseURL string
	// DBMaxConns is the pgx pool's maximum connection count (DB_MAX_CONNS).
	DBMaxConns int
	RedisURL   string

	MinioEndpoint  string
	MinioAccessKey string
	MinioSecretKey string
	MinioUseSSL    bool
	// MinioPublicURL is the origin presigned URLs are signed against, so the
	// links work from outside the host network (phones via ngrok). Requests
	// reach MinIO through the dev proxy, which preserves the Host header.
	MinioPublicURL string

	// AppBaseURL is the public origin the apps are served from (ngrok in dev).
	AppBaseURL string
	// PublicBaseURL is the origin used when building public links (QR codes,
	// verification links). Defaults to AppBaseURL when unset.
	PublicBaseURL string
	// EnduserBaseURL / TenantBaseURL are where the renter and landlord apps
	// live, including any basePath. In dev they are derived from the single
	// proxy origin (…/enduser, …/tenant); deployed on separate domains each
	// is set explicitly (ENDUSER_BASE_URL, TENANT_BASE_URL). Read them via
	// EnduserURL() / TenantURL(), which apply the fallback.
	EnduserBaseURL string
	TenantBaseURL  string

	// CORSAllowedOrigins is the allowlist for cross-origin browser calls
	// (CORS_ALLOWED_ORIGINS, comma-separated origins). Empty disables CORS
	// entirely — the dev loop is same-origin behind the proxy.
	CORSAllowedOrigins []string
	// CookieSecureFlag forces the Secure attribute on session cookies even
	// outside ENV=prod (COOKIE_SECURE). SameSite=None needs it.
	CookieSecureFlag bool
	// CookieSameSiteMode is "lax" (default), "none" (apps on other origins
	// than the API) or "strict" (COOKIE_SAME_SITE).
	CookieSameSiteMode string

	SessionTTLHours int

	// NidaEncKey is the pgcrypto symmetric key for renter_profiles.nida_number.
	NidaEncKey string

	// AdminEmail/AdminPassword seed the first platform admin on startup when
	// no platform_admin user exists yet. Empty disables seeding.
	AdminEmail    string
	AdminPassword string

	BeemAPIKey    string
	BeemSecretKey string
	BeemSenderID  string

	// NotifyWorkers is the size of the SMS sending pool (API.md Phase 6: 3).
	NotifyWorkers int

	// SMSCreditExemptKinds is the comma-separated list of notification kinds
	// that send without debiting a credit (Phase 14). Empty means the default,
	// `otp`: the platform absorbs the cost of letting somebody into their own
	// account. The literal "none" charges for everything.
	SMSCreditExemptKinds string

	// TrustedProxyCIDRs is the comma-separated set of networks whose requests
	// may carry X-Forwarded-For / X-Real-IP on a caller's behalf.
	TrustedProxyCIDRs string
}

// Load reads configuration from the process environment.
func Load() Config {
	c := Config{
		Env:  Env(getenv("ENV", string(EnvDev))),
		Port: getenv("API_PORT", getenv("PORT", "8081")),

		DatabaseURL: getenv("DATABASE_URL", "postgres://tms:tms_dev@localhost:5432/tms?sslmode=disable"),
		DBMaxConns:  getint("DB_MAX_CONNS", DefaultDBMaxConns),
		RedisURL:    getenv("REDIS_URL", "redis://localhost:6379/0"),

		MinioEndpoint:  getenv("MINIO_ENDPOINT", "localhost:9000"),
		MinioAccessKey: getenv("MINIO_ROOT_USER", "tms"),
		MinioSecretKey: getenv("MINIO_ROOT_PASSWORD", "tms_dev_secret"),
		MinioUseSSL:    getbool("MINIO_USE_SSL", false),
		MinioPublicURL: DeriveMinioPublicURL(getenv("MINIO_PUBLIC_URL", ""), getenv("APP_BASE_URL", "")),

		AppBaseURL:    getenv("APP_BASE_URL", "http://localhost:8080"),
		PublicBaseURL: getenv("PUBLIC_BASE_URL", ""),

		SessionTTLHours: getint("SESSION_TTL_HOURS", 720),

		NidaEncKey:    getenv("NIDA_ENC_KEY", "dev-nida-key-change-me"),
		AdminEmail:    getenv("ADMIN_EMAIL", ""),
		AdminPassword: getenv("ADMIN_PASSWORD", ""),

		BeemAPIKey:    getenv("BEEM_API_KEY", ""),
		BeemSecretKey: getenv("BEEM_SECRET_KEY", ""),
		BeemSenderID:  getenv("BEEM_SENDER_ID", ""),

		NotifyWorkers:        getint("NOTIFY_WORKERS", DefaultNotifyWorkers),
		SMSCreditExemptKinds: getenv("SMS_CREDIT_EXEMPT_KINDS", ""),

		TrustedProxyCIDRs: getenv("TRUSTED_PROXY_CIDRS", DefaultTrustedProxyCIDRs),

		EnduserBaseURL: getenv("ENDUSER_BASE_URL", ""),
		TenantBaseURL:  getenv("TENANT_BASE_URL", ""),

		CORSAllowedOrigins: splitList(getenv("CORS_ALLOWED_ORIGINS", "")),
		CookieSecureFlag:   getbool("COOKIE_SECURE", false),
		CookieSameSiteMode: strings.ToLower(getenv("COOKIE_SAME_SITE", "lax")),
	}
	if c.PublicBaseURL == "" {
		c.PublicBaseURL = c.AppBaseURL
	}
	return c
}

// EnduserURL is the origin (plus basePath) of the renter app, no trailing
// slash: ENDUSER_BASE_URL, else "{PublicBaseURL or AppBaseURL}/enduser" —
// the dev proxy layout. Renter links (QR targets, contract and payment links
// in SMS) are built on it.
func (c Config) EnduserURL() string {
	if v := strings.TrimRight(strings.TrimSpace(c.EnduserBaseURL), "/"); v != "" {
		return v
	}
	return c.publicOrigin() + "/enduser"
}

// TenantURL is the same for the landlord app: TENANT_BASE_URL, else
// "{PublicBaseURL or AppBaseURL}/tenant". Invite and verify links use it.
func (c Config) TenantURL() string {
	if v := strings.TrimRight(strings.TrimSpace(c.TenantBaseURL), "/"); v != "" {
		return v
	}
	return c.publicOrigin() + "/tenant"
}

func (c Config) publicOrigin() string {
	if v := strings.TrimRight(strings.TrimSpace(c.PublicBaseURL), "/"); v != "" {
		return v
	}
	return strings.TrimRight(strings.TrimSpace(c.AppBaseURL), "/")
}

// CookieSameSite maps COOKIE_SAME_SITE to the http.SameSite the session
// cookies carry. Unknown values fall back to Lax.
func (c Config) CookieSameSite() http.SameSite {
	switch c.CookieSameSiteMode {
	case "none":
		return http.SameSiteNoneMode
	case "strict":
		return http.SameSiteStrictMode
	default:
		return http.SameSiteLaxMode
	}
}

// splitList parses a comma-separated env value into trimmed, non-empty items.
func splitList(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// DefaultMinioPublicURL is used when neither MINIO_PUBLIC_URL nor APP_BASE_URL
// is set: MinIO's own published port on the host.
const DefaultMinioPublicURL = "http://localhost:9000"

// DeriveMinioPublicURL resolves the origin presigned URLs are signed against.
// MINIO_PUBLIC_URL wins; otherwise APP_BASE_URL (the proxy/ngrok origin, which
// forwards the bucket prefixes to MinIO); otherwise MinIO direct.
func DeriveMinioPublicURL(minioPublicURL, appBaseURL string) string {
	if v := strings.TrimRight(strings.TrimSpace(minioPublicURL), "/"); v != "" {
		return v
	}
	if v := strings.TrimRight(strings.TrimSpace(appBaseURL), "/"); v != "" {
		return v
	}
	return DefaultMinioPublicURL
}

// IsDev reports whether the API is running in the dev environment.
func (c Config) IsDev() bool { return c.Env != EnvProd }

// Addr is the listen address for the HTTP server.
func (c Config) Addr() string { return ":" + c.Port }

// SessionTTL is the opaque-session lifetime.
func (c Config) SessionTTL() time.Duration {
	return time.Duration(c.SessionTTLHours) * time.Hour
}

// CookieSecure reports whether session cookies must carry the Secure flag:
// always in prod, and in dev when COOKIE_SECURE asks for it (a SameSite=None
// cookie is rejected by browsers without it).
func (c Config) CookieSecure() bool { return !c.IsDev() || c.CookieSecureFlag }

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func getbool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return b
}

func getint(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}
