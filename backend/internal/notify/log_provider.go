package notify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
)

// LogProvider is the dev SMS provider: it logs the message at INFO with
// structured `sms_to` / `sms_body` fields so OTP codes can be read straight
// out of the API log.
type LogProvider struct {
	Logger *slog.Logger
}

// NewLogProvider builds a LogProvider, defaulting to the global slog logger.
func NewLogProvider(logger *slog.Logger) *LogProvider {
	if logger == nil {
		logger = slog.Default()
	}
	return &LogProvider{Logger: logger}
}

// Send logs the message and returns a synthetic message id.
func (p *LogProvider) Send(_ context.Context, to, body string) (string, error) {
	id := "log-" + randomID()
	logger := p.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("sms (dev log provider)",
		"sms_to", to,
		"sms_body", body,
		"provider_msg_id", id,
	)
	return id, nil
}

func randomID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(b[:])
}
