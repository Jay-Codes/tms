package config_test

import (
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
