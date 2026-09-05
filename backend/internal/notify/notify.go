// Package notify owns SMS delivery. The provider sits behind an interface so
// dev runs log messages (OTP codes readable from the API log) while production
// sends through Beem (SPEC §6).
package notify

import (
	"context"
	"errors"
)

// SMSProvider sends a single SMS and returns the provider's message id.
type SMSProvider interface {
	Send(ctx context.Context, to, body string) (providerMsgID string, err error)
}

// ErrBeemNotConfigured is returned when Beem credentials are missing.
var ErrBeemNotConfigured = errors.New("beem: not configured")
