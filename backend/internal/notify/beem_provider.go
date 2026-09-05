package notify

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"tms/backend/internal/config"
)

// BeemEndpoint is Beem Africa's send-SMS endpoint (SPEC §6, API.md Phase 6).
const BeemEndpoint = "https://apisms.beem.africa/v1/send"

// beemTimeout bounds one send. The worker holds a claimed row while it waits,
// so a provider that never answers must not pin a worker indefinitely.
const beemTimeout = 10 * time.Second

// DefaultSenderID is the sender name used when neither the org nor
// BEEM_SENDER_ID names one. Beem requires a non-empty source address.
const DefaultSenderID = "INFO"

// senderIDMaxLen is the GSM alphanumeric sender-ID limit.
const senderIDMaxLen = 11

// beemMaxErrBody caps how much of a provider error response is read back into
// the notification_log `error` column.
const beemMaxErrBody = 2048

// BeemProvider sends SMS through the Beem Africa HTTP API.
type BeemProvider struct {
	APIKey    string
	SecretKey string
	SenderID  string
	// Endpoint overrides BeemEndpoint (tests point it at an httptest server).
	Endpoint string
	// HTTP overrides the default client (tests inject one without a timeout
	// of their own).
	HTTP *http.Client
}

// NewBeemProvider builds a Beem provider from config.
func NewBeemProvider(cfg config.Config) *BeemProvider {
	return &BeemProvider{
		APIKey:    cfg.BeemAPIKey,
		SecretKey: cfg.BeemSecretKey,
		SenderID:  cfg.BeemSenderID,
		Endpoint:  BeemEndpoint,
		HTTP:      &http.Client{Timeout: beemTimeout},
	}
}

// Configured reports whether credentials are present.
func (p *BeemProvider) Configured() bool {
	return p != nil && p.APIKey != "" && p.SecretKey != ""
}

// beemRecipient is one addressee of a send. Beem numbers the recipients of a
// request itself; the MVP sends one message per row, so it is always 1.
type beemRecipient struct {
	RecipientID int    `json:"recipient_id"`
	DestAddr    string `json:"dest_addr"`
}

// beemRequest is the JSON body Beem's /v1/send expects. `schedule_time` empty
// means "now"; `encoding` 0 is GSM-7.
type beemRequest struct {
	SourceAddr   string          `json:"source_addr"`
	ScheduleTime string          `json:"schedule_time"`
	Encoding     int             `json:"encoding"`
	Message      string          `json:"message"`
	Recipients   []beemRecipient `json:"recipients"`
}

// beemResponse is the answer. `successful` false carries the reason in
// `message`, even on a 200, so both are checked.
type beemResponse struct {
	Successful bool   `json:"successful"`
	RequestID  any    `json:"request_id"`
	Code       int    `json:"code"`
	Message    string `json:"message"`
	Error      string `json:"error"`
}

// SourceAddr resolves the sender name for one send: the org's own approved
// sender ID if it has one, else the platform's BEEM_SENDER_ID, else "INFO"
// (SPEC §6: "sender ID per org where approved; platform default otherwise").
// The name is sanitised here as well as validated on the way in: it travels
// from org settings into a provider request, so the send path never trusts
// that whatever is stored is still a legal GSM sender ID.
func (p *BeemProvider) SourceAddr(senderName string) string {
	if v := sanitizeSenderID(senderName); v != "" {
		return v
	}
	if p != nil {
		if v := sanitizeSenderID(p.SenderID); v != "" {
			return v
		}
	}
	return DefaultSenderID
}

// sanitizeSenderID reduces a sender name to what an alphanumeric sender ID may
// be: letters and digits, at most 11 of them.
func sanitizeSenderID(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		if b.Len() >= senderIDMaxLen {
			break
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Send delivers one SMS through Beem and returns its request id.
//
// A non-2xx status, an unreadable body or `successful: false` are all errors:
// the worker records them against the row and retries with backoff, so a
// message is never quietly dropped.
func (p *BeemProvider) Send(ctx context.Context, to, body, senderName string) (string, error) {
	if !p.Configured() {
		return "", ErrBeemNotConfigured
	}

	payload, err := json.Marshal(beemRequest{
		SourceAddr:   p.SourceAddr(senderName),
		ScheduleTime: "",
		Encoding:     0,
		Message:      body,
		Recipients:   []beemRecipient{{RecipientID: 1, DestAddr: destAddr(to)}},
	})
	if err != nil {
		return "", fmt.Errorf("beem: marshal request: %w", err)
	}

	endpoint := p.Endpoint
	if endpoint == "" {
		endpoint = BeemEndpoint
	}
	sendCtx, cancel := context.WithTimeout(ctx, beemTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(sendCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("beem: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Basic "+basicAuth(p.APIKey, p.SecretKey))

	client := p.HTTP
	if client == nil {
		client = &http.Client{Timeout: beemTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("beem: send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, beemMaxErrBody))
	if err != nil {
		return "", fmt.Errorf("beem: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("beem: http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out beemResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("beem: decode response: %w", err)
	}
	if !out.Successful {
		reason := firstNonEmpty(out.Message, out.Error, strings.TrimSpace(string(raw)))
		return "", fmt.Errorf("beem: rejected (code %d): %s", out.Code, reason)
	}
	return requestIDString(out.RequestID), nil
}

// destAddr renders a phone number the way Beem wants it: digits only, country
// code included, no leading `+`.
func destAddr(phone string) string {
	return strings.TrimPrefix(strings.TrimSpace(phone), "+")
}

func basicAuth(user, secret string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + secret))
}

// requestIDString renders Beem's `request_id`, which arrives as a number in
// some responses and a string in others.
func requestIDString(v any) string {
	switch id := v.(type) {
	case nil:
		return ""
	case string:
		return id
	case float64:
		return fmt.Sprintf("%.0f", id)
	default:
		return fmt.Sprint(id)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return "no reason given"
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
