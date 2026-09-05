package notify

import (
	"context"
	"log/slog"

	"tms/backend/internal/config"
)

// BeemProvider sends SMS through the Beem Africa HTTP API.
//
// Phase 0 stub: the transport lands in Phase 6. Until then Send reports that
// the provider is not configured rather than silently dropping messages.
type BeemProvider struct {
	APIKey    string
	SecretKey string
	SenderID  string
}

// NewBeemProvider builds a Beem provider from config.
func NewBeemProvider(cfg config.Config) *BeemProvider {
	return &BeemProvider{
		APIKey:    cfg.BeemAPIKey,
		SecretKey: cfg.BeemSecretKey,
		SenderID:  cfg.BeemSenderID,
	}
}

// Configured reports whether credentials are present.
func (p *BeemProvider) Configured() bool {
	return p != nil && p.APIKey != "" && p.SecretKey != ""
}

// Send delivers an SMS via Beem. Not implemented until Phase 6.
func (p *BeemProvider) Send(_ context.Context, _, _ string) (string, error) {
	if !p.Configured() {
		return "", ErrBeemNotConfigured
	}
	return "", ErrBeemNotConfigured
}

// ProviderFor picks the SMS provider for the current configuration: Beem when
// BEEM_API_KEY is set, otherwise the dev LogProvider.
func ProviderFor(cfg config.Config, logger *slog.Logger) SMSProvider {
	if cfg.BeemAPIKey != "" {
		return NewBeemProvider(cfg)
	}
	return NewLogProvider(logger)
}
