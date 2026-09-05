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

// Send logs the message and returns a synthetic message id. The sender name is
// logged too, so a smoke test can see which sender ID a live send would have
// used without a Beem account.
func (p *LogProvider) Send(_ context.Context, to, body, senderName string) (string, error) {
	id := "log-" + randomID()
	logger := p.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if senderName == "" {
		senderName = DefaultSenderID
	}
	logger.Info("sms (dev log provider)",
		"sms_to", to,
		"sms_sender", senderName,
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

// NewBatchID mints the UUID v4 that groups one landlord broadcast
// (`custom:{batch_id}:{user_id}`). It is generated in the application rather
// than by Postgres because the dedupe key is built before the rows are written.
func NewBatchID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A failing CSPRNG is not something a broadcast can recover from;
		// a timestamp-shaped fallback still yields a unique-enough key.
		return "00000000-0000-4000-8000-" + hex.EncodeToString(b[10:16])
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	s := hex.EncodeToString(b[:])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}
