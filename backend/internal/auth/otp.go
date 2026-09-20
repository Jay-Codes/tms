package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/redis/go-redis/v9"
)

// Short-lived state that deliberately lives only in Redis — there is no table
// for OTPs, email-verification tokens or invite tokens (SPEC §4 note).
const (
	// OTPTTL is the lifetime of a 6-digit OTP (SPEC §3).
	OTPTTL = 5 * time.Minute
	// OTPResendCooldown is the minimum gap between sends to one phone.
	OTPResendCooldown = 60 * time.Second
	// OTPTokenTTL is the lifetime of the token handed out after a successful
	// `register` verification and spent by POST /auth/register/renter.
	OTPTokenTTL = 10 * time.Minute
	// EmailVerifyTTL is the lifetime of an email verification link.
	EmailVerifyTTL = 24 * time.Hour
	// InviteTTL is the lifetime of a staff invite link.
	InviteTTL = 7 * 24 * time.Hour
)

// Token key prefixes.
const (
	prefixOTP        = "otp:"
	prefixOTPCooldwn = "otp:cooldown:"
	prefixOTPToken   = "otptok:"
	// prefixAssist marks a slot whose code was shown on a landlord's screen
	// rather than sent (Phase 18, FLOWS 2b). The value is the assist session
	// id, so the register/login handlers can stamp the session the renter
	// just came from without the renter's device ever carrying it.
	prefixAssist = "otp:assist:"
	// prefixWitness marks a contract whose signing code was shown in person.
	// The value is the org user who showed it, which becomes
	// contract_signatures.witnessed_by_user_id.
	prefixWitness = "otp:witness:"
	// PrefixEmailVerify keys email-verification tokens.
	PrefixEmailVerify = "emailverify:"
	// PrefixInvite keys staff invite tokens.
	PrefixInvite = "invite:"
)

// ErrCacheUnavailable means Redis is required for this operation but is down.
// Unlike rate limiting, OTP and token flows cannot fail open.
var ErrCacheUnavailable = errors.New("auth: cache unavailable")

// ErrNotFound means the OTP or token is unknown, expired or already spent.
var ErrNotFound = errors.New("auth: token not found")

// ErrCooldown means an OTP was requested again inside the resend cooldown.
var ErrCooldown = errors.New("auth: resend cooldown active")

// Store holds the ephemeral auth state (OTP codes and one-shot tokens).
type Store struct {
	Redis *redis.Client
}

// GenerateOTP returns a uniformly random 6-digit code.
func GenerateOTP() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "000000"
	}
	return fmt.Sprintf("%06d", n.Int64())
}

// GenerateToken returns a 32-byte URL-safe random token.
func GenerateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// PutOTP stores a code for (purpose, phone) and arms the resend cooldown.
// It returns ErrCooldown if a code was issued less than OTPResendCooldown ago.
func (s *Store) PutOTP(ctx context.Context, purpose, phone, code string) error {
	if s == nil || s.Redis == nil {
		return ErrCacheUnavailable
	}
	cool := prefixOTPCooldwn + purpose + ":" + phone
	set, err := s.Redis.SetNX(ctx, cool, "1", OTPResendCooldown).Result()
	if err != nil {
		return ErrCacheUnavailable
	}
	if !set {
		return ErrCooldown
	}
	if err := s.Redis.Set(ctx, prefixOTP+purpose+":"+phone, code, OTPTTL).Err(); err != nil {
		return ErrCacheUnavailable
	}
	return nil
}

// PutOTPAssisted stores a code for (purpose, phone) the way the landlord's
// screen issues one (Phase 18, SPEC §5.15).
//
// It differs from PutOTP in exactly one respect: it neither checks nor arms
// the per-phone resend cooldown. The cooldown exists to stop a stranger
// walking a number's inbox from a public endpoint; an assisted code is issued
// by an authenticated org user who is standing next to the renter, and is
// limited per org (30/h) and per session (10) instead. It overwrites whatever
// is in the slot, so a text still in flight carries a dead code — which is the
// documented edge case, not a bug (FLOWS 2b).
func (s *Store) PutOTPAssisted(ctx context.Context, purpose, phone, code string) error {
	if s == nil || s.Redis == nil {
		return ErrCacheUnavailable
	}
	if err := s.Redis.Set(ctx, prefixOTP+purpose+":"+phone, code, OTPTTL).Err(); err != nil {
		return ErrCacheUnavailable
	}
	return nil
}

// SetAssistMarker records that the live code for (purpose, phone) was shown in
// person by the given assist session. It expires with the code.
func (s *Store) SetAssistMarker(ctx context.Context, purpose, phone, sessionID string) error {
	if s == nil || s.Redis == nil {
		return ErrCacheUnavailable
	}
	if err := s.Redis.Set(ctx, prefixAssist+purpose+":"+phone, sessionID, OTPTTL).Err(); err != nil {
		return ErrCacheUnavailable
	}
	return nil
}

// PeekAssistMarker reads the marker without spending it. The `register` flow
// reads it twice — once at verify, when the account does not exist yet, and
// once at registration, when it does — so the verify half must leave it.
func (s *Store) PeekAssistMarker(ctx context.Context, purpose, phone string) string {
	if s == nil || s.Redis == nil {
		return ""
	}
	v, err := s.Redis.Get(ctx, prefixAssist+purpose+":"+phone).Result()
	if err != nil {
		return ""
	}
	return v
}

// TakeAssistMarker reads and spends the marker. A missing or unreadable marker
// is an empty string: the assisted stamp is a nicety on top of a flow that has
// already succeeded, so it never fails the request.
func (s *Store) TakeAssistMarker(ctx context.Context, purpose, phone string) string {
	if s == nil || s.Redis == nil {
		return ""
	}
	v, err := s.Redis.GetDel(ctx, prefixAssist+purpose+":"+phone).Result()
	if err != nil {
		return ""
	}
	return v
}

// SetWitnessMarker records the org user who showed a contract's signing code.
func (s *Store) SetWitnessMarker(ctx context.Context, contractID, userID string) error {
	if s == nil || s.Redis == nil {
		return ErrCacheUnavailable
	}
	if err := s.Redis.Set(ctx, prefixWitness+contractID, userID, OTPTTL).Err(); err != nil {
		return ErrCacheUnavailable
	}
	return nil
}

// TakeWitnessMarker spends the witness marker for a contract, returning the org
// user who showed the code (empty when the code came by SMS).
func (s *Store) TakeWitnessMarker(ctx context.Context, contractID string) string {
	if s == nil || s.Redis == nil {
		return ""
	}
	v, err := s.Redis.GetDel(ctx, prefixWitness+contractID).Result()
	if err != nil {
		return ""
	}
	return v
}

// CheckOTP compares a submitted code against the stored one. A correct code is
// consumed (single use); an incorrect one leaves the stored code in place so
// the caller's remaining attempts are governed by the rate limiter.
func (s *Store) CheckOTP(ctx context.Context, purpose, phone, code string) error {
	if s == nil || s.Redis == nil {
		return ErrCacheUnavailable
	}
	key := prefixOTP + purpose + ":" + phone
	want, err := s.Redis.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return ErrNotFound
	}
	if err != nil {
		return ErrCacheUnavailable
	}
	if subtle.ConstantTimeCompare([]byte(want), []byte(code)) != 1 {
		return ErrNotFound
	}
	s.Redis.Del(ctx, key)
	return nil
}

// PutToken stores value under a fresh random token and returns the token.
func (s *Store) PutToken(ctx context.Context, prefix, value string, ttl time.Duration) (string, error) {
	if s == nil || s.Redis == nil {
		return "", ErrCacheUnavailable
	}
	token := GenerateToken()
	if token == "" {
		return "", ErrCacheUnavailable
	}
	if err := s.Redis.Set(ctx, prefix+token, value, ttl).Err(); err != nil {
		return "", ErrCacheUnavailable
	}
	return token, nil
}

// ConsumeToken reads and deletes a one-shot token, returning its value.
func (s *Store) ConsumeToken(ctx context.Context, prefix, token string) (string, error) {
	if s == nil || s.Redis == nil {
		return "", ErrCacheUnavailable
	}
	if token == "" {
		return "", ErrNotFound
	}
	value, err := s.Redis.GetDel(ctx, prefix+token).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", ErrCacheUnavailable
	}
	return value, nil
}

// PutOTPToken stores the phone number a verified `register` OTP belongs to and
// returns the otp_token handed to the client.
func (s *Store) PutOTPToken(ctx context.Context, phone string) (string, error) {
	return s.PutToken(ctx, prefixOTPToken, phone, OTPTokenTTL)
}

// ConsumeOTPToken spends an otp_token, returning the phone it was issued for.
func (s *Store) ConsumeOTPToken(ctx context.Context, token string) (string, error) {
	return s.ConsumeToken(ctx, prefixOTPToken, token)
}
