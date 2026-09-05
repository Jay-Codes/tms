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
