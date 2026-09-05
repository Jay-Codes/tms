package notify

import (
	"context"
	"log/slog"
)

// EmailProvider delivers a transactional email carrying a single action link
// (verification, staff invite). No mail vendor is in MVP scope (DECISIONS.md),
// so dev runs log the link and production swaps in a real implementation.
type EmailProvider interface {
	Send(ctx context.Context, to, subject, link, body string) (providerMsgID string, err error)
}

// LogEmailProvider is the dev EmailProvider: it logs the message with
// structured `email_to`, `email_subject` and `email_link` fields so links can
// be read straight out of the API log (.dev/api.log).
type LogEmailProvider struct {
	Logger *slog.Logger
}

// NewLogEmailProvider builds a LogEmailProvider, defaulting to slog.Default.
func NewLogEmailProvider(logger *slog.Logger) *LogEmailProvider {
	if logger == nil {
		logger = slog.Default()
	}
	return &LogEmailProvider{Logger: logger}
}

// Send logs the email and returns a synthetic message id.
func (p *LogEmailProvider) Send(_ context.Context, to, subject, link, body string) (string, error) {
	id := "logmail-" + randomID()
	logger := p.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("email (dev log provider)",
		"email_to", to,
		"email_subject", subject,
		"email_link", link,
		"email_body", body,
		"provider_msg_id", id,
	)
	return id, nil
}
