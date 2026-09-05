// Package notify owns SMS delivery. The provider sits behind an interface so
// dev runs log messages (OTP codes readable from the API log) while production
// sends through Beem (SPEC §6).
package notify

import (
	"context"
	"errors"
)

// SMSProvider sends a single SMS and returns the provider's message id.
//
// senderName is the sender ID the message should appear from: the org's own
// approved name where it has one, empty to fall back to the platform default
// (SPEC §6). A provider that cannot vary the sender ignores it.
type SMSProvider interface {
	Send(ctx context.Context, to, body, senderName string) (providerMsgID string, err error)
}

// ErrBeemNotConfigured is returned when Beem credentials are missing.
var ErrBeemNotConfigured = errors.New("beem: not configured")

// ErrDevProviderInProd is returned by the provider selectors when ENV=prod
// would fall back to a dev LogProvider. The dev providers write the message
// body — OTP codes, verification and invite links — to the API log, so they
// must never run in production.
var ErrDevProviderInProd = errors.New("dev log provider refused in ENV=prod")

// ErrProviderDisabled is returned by the disabled providers.
var ErrProviderDisabled = errors.New("notify: provider not configured")

// DisabledSMSProvider fails every send. It stands in when no usable provider
// could be selected, so messages error loudly instead of leaking to the log.
type DisabledSMSProvider struct{}

// Send always fails.
func (DisabledSMSProvider) Send(context.Context, string, string, string) (string, error) {
	return "", ErrProviderDisabled
}

// DisabledEmailProvider fails every send (see DisabledSMSProvider).
type DisabledEmailProvider struct{}

// Send always fails.
func (DisabledEmailProvider) Send(context.Context, string, string, string, string) (string, error) {
	return "", ErrProviderDisabled
}
