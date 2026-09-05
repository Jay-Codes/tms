// Package config loads application configuration from environment variables.
//
// Configuration is read once at startup (see Load). Every value has a
// dev-friendly default so the API can boot with an empty environment.
package config

import (
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

// Config holds all runtime configuration for the API binary.
type Config struct {
	Env  Env
	Port string

	DatabaseURL string
	RedisURL    string

	MinioEndpoint  string
	MinioAccessKey string
	MinioSecretKey string
	MinioUseSSL    bool

	// AppBaseURL is the public origin the apps are served from (ngrok in dev).
	AppBaseURL string
	// PublicBaseURL is the origin used when building public links (QR codes,
	// verification links). Defaults to AppBaseURL when unset.
	PublicBaseURL string

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
}

// Load reads configuration from the process environment.
func Load() Config {
	c := Config{
		Env:  Env(getenv("ENV", string(EnvDev))),
		Port: getenv("API_PORT", getenv("PORT", "8081")),

		DatabaseURL: getenv("DATABASE_URL", "postgres://tms:tms_dev@localhost:5432/tms?sslmode=disable"),
		RedisURL:    getenv("REDIS_URL", "redis://localhost:6379/0"),

		MinioEndpoint:  getenv("MINIO_ENDPOINT", "localhost:9000"),
		MinioAccessKey: getenv("MINIO_ROOT_USER", "tms"),
		MinioSecretKey: getenv("MINIO_ROOT_PASSWORD", "tms_dev_secret"),
		MinioUseSSL:    getbool("MINIO_USE_SSL", false),

		AppBaseURL:    getenv("APP_BASE_URL", "http://localhost:8080"),
		PublicBaseURL: getenv("PUBLIC_BASE_URL", ""),

		SessionTTLHours: getint("SESSION_TTL_HOURS", 720),

		NidaEncKey:    getenv("NIDA_ENC_KEY", "dev-nida-key-change-me"),
		AdminEmail:    getenv("ADMIN_EMAIL", ""),
		AdminPassword: getenv("ADMIN_PASSWORD", ""),

		BeemAPIKey:    getenv("BEEM_API_KEY", ""),
		BeemSecretKey: getenv("BEEM_SECRET_KEY", ""),
		BeemSenderID:  getenv("BEEM_SENDER_ID", ""),
	}
	if c.PublicBaseURL == "" {
		c.PublicBaseURL = c.AppBaseURL
	}
	return c
}

// IsDev reports whether the API is running in the dev environment.
func (c Config) IsDev() bool { return c.Env != EnvProd }

// Addr is the listen address for the HTTP server.
func (c Config) Addr() string { return ":" + c.Port }

// SessionTTL is the opaque-session lifetime.
func (c Config) SessionTTL() time.Duration {
	return time.Duration(c.SessionTTLHours) * time.Hour
}

// CookieSecure reports whether session cookies must carry the Secure flag.
func (c Config) CookieSecure() bool { return !c.IsDev() }

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
