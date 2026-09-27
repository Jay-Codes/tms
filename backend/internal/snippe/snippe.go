// Package snippe is the Go API's only contact with Snippe, the mobile-money
// gateway landlords buy SMS credits through (TECHSTACK "External providers",
// PLAN2 Phase 27).
//
// Everything about Snippe's wire format lives in this one file, written from
// docs.snippe.sh (API version 2026-01-25). Where the documentation left a
// detail open, the choice is marked `UNCERTAIN:` so it can be checked against
// the sandbox in one place once the client supplies keys; the tests pin the
// behaviour against a fake Snippe (httptest), never the real API.
package snippe

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Currency is the only currency Snippe collects in.
const Currency = "TZS"

// MinAmount is Snippe's smallest payment, in TZS.
const MinAmount = 500

// FeeBasisPoints is Snippe's collection fee: 2.5%, i.e. 250 basis points.
// The margin report subtracts it from sales.
const FeeBasisPoints = 250

// Fee is Snippe's cut of amount, rounded to the nearest shilling.
func Fee(amount int64) int64 { return (amount*FeeBasisPoints + 5_000) / 10_000 }

// MaxIdempotencyKey is the longest Idempotency-Key Snippe accepts.
const MaxIdempotencyKey = 30

// WebhookTolerance is how old (or how far ahead) a webhook timestamp may be.
const WebhookTolerance = 5 * time.Minute

// Payment statuses as Snippe reports them.
const (
	StatusPending   = "pending"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusVoided    = "voided"
	StatusExpired   = "expired"
)

// Webhook event types.
const (
	EventCompleted = "payment.completed"
	EventFailed    = "payment.failed"
	EventVoided    = "payment.voided"
	EventExpired   = "payment.expired"
)

// requestTimeout bounds one call. The order handler holds the landlord's
// request open while it waits; Snippe answers a USSD push request quickly.
const requestTimeout = 15 * time.Second

// maxBody caps how much of a Snippe response is read.
const maxBody = 64 << 10

// Client calls the Snippe REST API.
type Client struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
}

// New builds a client. A blank key gives a client whose Configured is false.
func New(apiKey, baseURL string) *Client {
	return &Client{
		APIKey:  strings.TrimSpace(apiKey),
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		HTTP:    &http.Client{Timeout: requestTimeout},
	}
}

// Configured reports whether the client has a key to call with.
func (c *Client) Configured() bool { return c != nil && c.APIKey != "" && c.BaseURL != "" }

// Error is a refusal Snippe answered with (`error_code`, `message`).
type Error struct {
	HTTPStatus int
	Code       string
	Message    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("snippe: http %d %s: %s", e.HTTPStatus, e.Code, e.Message)
}

// ErrNotConfigured is returned when no API key is set.
var ErrNotConfigured = errors.New("snippe: not configured")

// Amount is Snippe's money value. Webhooks send `{value, currency}`;
// UNCERTAIN: whether GET /v1/payments/{ref} does the same or sends a bare
// integer, so both decode.
type Amount struct {
	Value    int64  `json:"value"`
	Currency string `json:"currency"`
}

// UnmarshalJSON accepts `{value, currency}`, a bare number or a numeric string.
func (a *Amount) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '{' {
		var obj struct {
			Value    json.Number `json:"value"`
			Currency string      `json:"currency"`
		}
		if err := json.Unmarshal(b, &obj); err != nil {
			return err
		}
		v, err := numberToInt(obj.Value)
		if err != nil {
			return err
		}
		a.Value, a.Currency = v, obj.Currency
		return nil
	}
	var n json.Number
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		n = json.Number(s)
	} else if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	v, err := numberToInt(n)
	if err != nil {
		return err
	}
	a.Value = v
	return nil
}

func numberToInt(n json.Number) (int64, error) {
	if n == "" {
		return 0, nil
	}
	if v, err := n.Int64(); err == nil {
		return v, nil
	}
	f, err := n.Float64()
	if err != nil {
		return 0, err
	}
	return int64(f), nil
}

// Payment is the part of a Snippe payment object the API reads.
type Payment struct {
	ID        string         `json:"id"`
	Reference string         `json:"reference"`
	Status    string         `json:"status"`
	Amount    Amount         `json:"amount"`
	Currency  string         `json:"currency"`
	Metadata  map[string]any `json:"metadata"`
}

// Meta reads one metadata value as a string ("" when absent or not a string).
func (p Payment) Meta(key string) string {
	if v, ok := p.Metadata[key].(string); ok {
		return v
	}
	return ""
}

// Currency of a payment: the amount's own, else the top-level field.
func (p Payment) CurrencyCode() string {
	if p.Amount.Currency != "" {
		return p.Amount.Currency
	}
	return p.Currency
}

// CreateRequest is one USSD push.
type CreateRequest struct {
	// Amount in whole TZS (≥ MinAmount).
	Amount int64
	// Phone is E.164 (+2557…); converted to Snippe's form here.
	Phone string
	// IdempotencyKey is our order code (≤ MaxIdempotencyKey).
	IdempotencyKey string
	Metadata       map[string]string
	WebhookURL     string
}

type createBody struct {
	Amount      int64             `json:"amount"`
	Currency    string            `json:"currency"`
	Phone       string            `json:"phone"`
	PaymentType string            `json:"payment_type"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	WebhookURL  string            `json:"webhook_url,omitempty"`
}

// envelope is Snippe's response wrapper: `{status, code, data}` on success,
// `{status:"error", code, error_code, message}` on a refusal.
type envelope struct {
	Status    string          `json:"status"`
	Code      int             `json:"code"`
	Data      json.RawMessage `json:"data"`
	ErrorCode string          `json:"error_code"`
	Message   string          `json:"message"`
}

// PhoneForSnippe renders an E.164 number the way Snippe takes it.
// UNCERTAIN: the docs show Tanzanian numbers as 2557XXXXXXXX (country code,
// no plus); a leading `+` is dropped to match.
func PhoneForSnippe(e164 string) string {
	return strings.TrimPrefix(strings.TrimSpace(e164), "+")
}

// CreatePayment asks Snippe to push a payment prompt to the payer's phone.
func (c *Client) CreatePayment(ctx context.Context, req CreateRequest) (Payment, error) {
	if !c.Configured() {
		return Payment{}, ErrNotConfigured
	}
	if len(req.IdempotencyKey) > MaxIdempotencyKey {
		return Payment{}, fmt.Errorf("snippe: idempotency key longer than %d", MaxIdempotencyKey)
	}
	raw, err := json.Marshal(createBody{
		Amount:      req.Amount,
		Currency:    Currency,
		Phone:       PhoneForSnippe(req.Phone),
		PaymentType: "mobile",
		Metadata:    req.Metadata,
		WebhookURL:  req.WebhookURL,
	})
	if err != nil {
		return Payment{}, fmt.Errorf("snippe: marshal: %w", err)
	}
	var out Payment
	err = c.do(ctx, http.MethodPost, "/v1/payments", raw, map[string]string{
		"Idempotency-Key": req.IdempotencyKey,
	}, &out)
	return out, err
}

// GetPayment reads a payment's current state by its Snippe reference.
func (c *Client) GetPayment(ctx context.Context, reference string) (Payment, error) {
	if !c.Configured() {
		return Payment{}, ErrNotConfigured
	}
	var out Payment
	err := c.do(ctx, http.MethodGet, "/v1/payments/"+pathEscape(reference), nil, nil, &out)
	return out, err
}

func (c *Client) do(
	ctx context.Context, method, path string, body []byte, headers map[string]string, out *Payment,
) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return fmt.Errorf("snippe: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("snippe: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("snippe: read response: %w", err)
	}

	var env envelope
	decodeErr := json.Unmarshal(raw, &env)
	if resp.StatusCode < 200 || resp.StatusCode > 299 || env.Status == "error" {
		e := &Error{HTTPStatus: resp.StatusCode, Code: env.ErrorCode, Message: env.Message}
		if e.Message == "" {
			e.Message = strings.TrimSpace(string(raw))
			if len(e.Message) > 200 {
				e.Message = e.Message[:200]
			}
		}
		return e
	}
	if decodeErr != nil {
		return fmt.Errorf("snippe: decode response: %w", decodeErr)
	}
	if len(env.Data) == 0 {
		return errors.New("snippe: response carries no data")
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("snippe: decode payment: %w", err)
	}
	return nil
}

func pathEscape(s string) string {
	// References are Snippe-issued tokens; anything outside a safe set is
	// escaped rather than trusted into the path.
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			fmt.Fprintf(&b, "%%%02X", r)
		}
	}
	return b.String()
}

// ------------------------------------------------------------- webhooks --

// Webhook headers.
const (
	HeaderEvent     = "X-Webhook-Event"
	HeaderTimestamp = "X-Webhook-Timestamp"
	HeaderSignature = "X-Webhook-Signature"
)

// Verification failures.
var (
	ErrBadSignature = errors.New("snippe: webhook signature does not match")
	ErrStale        = errors.New("snippe: webhook timestamp outside the tolerance")
)

// Sign is the signature Snippe computes: hex HMAC-SHA256 of
// "{timestamp}.{raw body}" keyed by the webhook signing secret. Tests use it
// to sign deliveries from the fake.
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify checks a delivery's signature and timestamp. The comparison is
// constant-time; the timestamp must be within WebhookTolerance of now either
// way, so a captured delivery cannot be replayed later.
func Verify(secret, timestamp, signature string, body []byte, now time.Time) error {
	if strings.TrimSpace(secret) == "" {
		return ErrBadSignature
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil {
		return ErrStale
	}
	if d := now.Sub(time.Unix(ts, 0)); d > WebhookTolerance || d < -WebhookTolerance {
		return ErrStale
	}
	// UNCERTAIN: the docs show a bare hex digest; a `sha256=` prefix, as some
	// gateways send, is tolerated.
	sig := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(signature), "sha256="))
	got, err := hex.DecodeString(sig)
	if err != nil {
		return ErrBadSignature
	}
	want, _ := hex.DecodeString(Sign(secret, strings.TrimSpace(timestamp), body))
	if !hmac.Equal(got, want) {
		return ErrBadSignature
	}
	return nil
}

// Event is a webhook delivery's envelope.
type Event struct {
	ID         string  `json:"id"`
	Type       string  `json:"type"`
	APIVersion string  `json:"api_version"`
	CreatedAt  string  `json:"created_at"`
	Data       Payment `json:"data"`
}

// ParseEvent decodes a verified delivery.
func ParseEvent(body []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(body, &e); err != nil {
		return Event{}, fmt.Errorf("snippe: decode event: %w", err)
	}
	if strings.TrimSpace(e.ID) == "" || strings.TrimSpace(e.Type) == "" {
		return Event{}, errors.New("snippe: event carries no id or type")
	}
	return e, nil
}
