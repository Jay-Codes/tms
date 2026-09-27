package config_test

import (
	"testing"

	"tms/backend/internal/config"
)

// Phase 27: Snippe is off unless both secrets are set, and the webhook URL
// falls back to the single-origin proxy layout.

func TestSnippeDisabledByDefault(t *testing.T) {
	for _, k := range []string{"SNIPPE_API_KEY", "SNIPPE_WEBHOOK_SECRET", "SNIPPE_BASE_URL", "SNIPPE_WEBHOOK_URL", "SMS_STOCK_BUFFER"} {
		t.Setenv(k, "")
	}
	cfg := config.Load()
	if cfg.SnippeEnabled() {
		t.Error("SnippeEnabled() = true with no keys, want false")
	}
	if cfg.SnippeBaseURL != config.DefaultSnippeBaseURL {
		t.Errorf("SnippeBaseURL = %q, want %q", cfg.SnippeBaseURL, config.DefaultSnippeBaseURL)
	}
	if cfg.SMSStockBuffer != config.DefaultSMSStockBuffer {
		t.Errorf("SMSStockBuffer = %d, want %d", cfg.SMSStockBuffer, config.DefaultSMSStockBuffer)
	}
}

func TestSnippeNeedsBothSecrets(t *testing.T) {
	t.Setenv("SNIPPE_API_KEY", "sk_test")
	t.Setenv("SNIPPE_WEBHOOK_SECRET", "")
	if config.Load().SnippeEnabled() {
		t.Error("enabled with the API key alone: a payment the API cannot verify must not be offered")
	}
	t.Setenv("SNIPPE_WEBHOOK_SECRET", "whsec")
	if !config.Load().SnippeEnabled() {
		t.Error("SnippeEnabled() = false with both secrets set")
	}
}

func TestSnippeWebhookURL(t *testing.T) {
	cfg := config.Config{AppBaseURL: "https://abc.ngrok-free.app/"}
	if got, want := cfg.SnippeWebhookURL(), "https://abc.ngrok-free.app/api/v1/webhooks/snippe"; got != want {
		t.Errorf("fallback = %q, want %q", got, want)
	}
	cfg.PublicBaseURL = "https://tms.example"
	if got, want := cfg.SnippeWebhookURL(), "https://tms.example/api/v1/webhooks/snippe"; got != want {
		t.Errorf("PublicBaseURL fallback = %q, want %q", got, want)
	}
	cfg.SnippeWebhookURLOverride = "https://api.tms.example/api/v1/webhooks/snippe"
	if got := cfg.SnippeWebhookURL(); got != cfg.SnippeWebhookURLOverride {
		t.Errorf("override = %q, want %q", got, cfg.SnippeWebhookURLOverride)
	}
}
