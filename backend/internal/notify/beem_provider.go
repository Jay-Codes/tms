package notify

import (
	"context"
	"fmt"
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

// SMSProviderFor picks the SMS provider for the current configuration: Beem
// when BEEM_API_KEY is set, otherwise the dev LogProvider. In ENV=prod the dev
// fallback is refused (ErrDevProviderInProd) so the binary fails startup rather
// than printing OTP codes to the production log.
func SMSProviderFor(cfg config.Config, logger *slog.Logger) (SMSProvider, error) {
	if cfg.BeemAPIKey != "" {
		return NewBeemProvider(cfg), nil
	}
	if !cfg.IsDev() {
		return nil, fmt.Errorf("sms: %w (set BEEM_API_KEY/BEEM_SECRET_KEY)", ErrDevProviderInProd)
	}
	return NewLogProvider(logger), nil
}

// EmailProviderFor picks the email provider. Only the dev LogEmailProvider
// exists in MVP scope (DECISIONS.md), so ENV=prod is refused.
func EmailProviderFor(cfg config.Config, logger *slog.Logger) (EmailProvider, error) {
	if !cfg.IsDev() {
		return nil, fmt.Errorf("email: %w (no production email provider is configured)", ErrDevProviderInProd)
	}
	return NewLogEmailProvider(logger), nil
}
