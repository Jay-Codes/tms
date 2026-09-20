package config_test

import (
	"net/http"
	"testing"

	"tms/backend/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	for _, k := range []string{"ENV", "API_PORT", "PORT", "MINIO_USE_SSL", "APP_BASE_URL", "PUBLIC_BASE_URL", "SESSION_TTL_HOURS"} {
		t.Setenv(k, "")
	}

	cfg := config.Load()
	if cfg.Port != "8081" {
		t.Errorf("Port = %q, want 8081", cfg.Port)
	}
	if cfg.Addr() != ":8081" {
		t.Errorf("Addr() = %q", cfg.Addr())
	}
	if !cfg.IsDev() {
		t.Error("IsDev() = false, want true by default")
	}
	if cfg.SessionTTLHours != 720 {
		t.Errorf("SessionTTLHours = %d, want 720", cfg.SessionTTLHours)
	}
	if cfg.MinioUseSSL {
		t.Error("MinioUseSSL = true, want false")
	}
}

func TestPublicBaseURLFallsBackToAppBaseURL(t *testing.T) {
	t.Setenv("APP_BASE_URL", "https://example.ngrok-free.app")
	t.Setenv("PUBLIC_BASE_URL", "")

	if got := config.Load().PublicBaseURL; got != "https://example.ngrok-free.app" {
		t.Errorf("PublicBaseURL = %q", got)
	}
}

func TestEnvProd(t *testing.T) {
	t.Setenv("ENV", "prod")
	if config.Load().IsDev() {
		t.Error("IsDev() = true for ENV=prod")
	}
}

func TestDeriveMinioPublicURL(t *testing.T) {
	tests := []struct {
		name, minio, app, want string
	}{
		{"explicit wins", "http://localhost:8080", "https://app.example", "http://localhost:8080"},
		{"falls back to app base url", "", "https://x.ngrok-free.app", "https://x.ngrok-free.app"},
		{"both empty falls back to minio", "", "", config.DefaultMinioPublicURL},
		{"whitespace counts as empty", "   ", "  ", config.DefaultMinioPublicURL},
		{"trailing slash trimmed", "http://localhost:8080/", "", "http://localhost:8080"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := config.DeriveMinioPublicURL(tt.minio, tt.app); got != tt.want {
				t.Errorf("DeriveMinioPublicURL(%q, %q) = %q, want %q", tt.minio, tt.app, got, tt.want)
			}
		})
	}
}

func TestLoadMinioPublicURLFromEnv(t *testing.T) {
	t.Setenv("MINIO_PUBLIC_URL", "")
	t.Setenv("APP_BASE_URL", "https://y.ngrok-free.app")
	if got := config.Load().MinioPublicURL; got != "https://y.ngrok-free.app" {
		t.Errorf("MinioPublicURL = %q, want the APP_BASE_URL fallback", got)
	}

	t.Setenv("MINIO_PUBLIC_URL", "http://localhost:8080")
	if got := config.Load().MinioPublicURL; got != "http://localhost:8080" {
		t.Errorf("MinioPublicURL = %q", got)
	}
}

func TestAppURLsDeriveFromPublicOrigin(t *testing.T) {
	c := config.Config{AppBaseURL: "http://localhost:8080/"}
	if got := c.EnduserURL(); got != "http://localhost:8080/enduser" {
		t.Fatalf("EnduserURL = %q", got)
	}
	if got := c.TenantURL(); got != "http://localhost:8080/tenant" {
		t.Fatalf("TenantURL = %q", got)
	}
	c.PublicBaseURL = "https://pub.example"
	if got := c.EnduserURL(); got != "https://pub.example/enduser" {
		t.Fatalf("EnduserURL with PublicBaseURL = %q", got)
	}
}

func TestAppURLsExplicitOverride(t *testing.T) {
	t.Setenv("ENDUSER_BASE_URL", "https://tms.kuzo.co.tz/")
	t.Setenv("TENANT_BASE_URL", "https://lms.kuzo.co.tz")
	t.Setenv("CORS_ALLOWED_ORIGINS", " https://tms.kuzo.co.tz, https://lms.kuzo.co.tz ,,https://tms-admin.kuzo.co.tz")
	t.Setenv("COOKIE_SAME_SITE", "None")
	t.Setenv("COOKIE_SECURE", "1")
	c := config.Load()
	if got := c.EnduserURL(); got != "https://tms.kuzo.co.tz" {
		t.Fatalf("EnduserURL = %q", got)
	}
	if got := c.TenantURL(); got != "https://lms.kuzo.co.tz" {
		t.Fatalf("TenantURL = %q", got)
	}
	if len(c.CORSAllowedOrigins) != 3 || c.CORSAllowedOrigins[1] != "https://lms.kuzo.co.tz" {
		t.Fatalf("CORSAllowedOrigins = %v", c.CORSAllowedOrigins)
	}
	if c.CookieSameSite() != http.SameSiteNoneMode {
		t.Fatalf("CookieSameSite = %v", c.CookieSameSite())
	}
	if !c.CookieSecure() {
		t.Fatal("COOKIE_SECURE=1 should force Secure in dev")
	}
}

func TestCookieDefaultsLaxNotSecure(t *testing.T) {
	c := config.Load()
	if c.CookieSameSite() != http.SameSiteLaxMode {
		t.Fatalf("default SameSite = %v", c.CookieSameSite())
	}
	if c.CookieSecure() {
		t.Fatal("dev default must not be Secure")
	}
	if len(c.CORSAllowedOrigins) != 0 {
		t.Fatalf("default CORS list = %v", c.CORSAllowedOrigins)
	}
}
