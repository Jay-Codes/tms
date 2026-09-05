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
