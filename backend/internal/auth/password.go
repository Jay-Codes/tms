// Package auth owns credentials (argon2id password/PIN hashing), opaque
// sessions (Redis primary, Postgres fallback), audience cookies and the
// role/scope middleware described in SPEC §3 and API.md.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters (OWASP "second recommended option": 19 MiB, t=2, p=1).
const (
	argonTime    uint32 = 2
	argonMemory  uint32 = 19 * 1024
	argonThreads uint8  = 1
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// ErrInvalidHash is returned when a stored hash cannot be parsed.
var ErrInvalidHash = errors.New("auth: invalid password hash")

// HashSecret hashes a password or PIN with argon2id and returns the standard
// PHC string ($argon2id$v=19$m=...,t=...,p=...$salt$hash).
func HashSecret(secret string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(secret), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifySecret reports whether secret matches the encoded argon2id hash. It is
// constant-time in the comparison and never distinguishes "no hash stored"
// from "wrong secret" to callers beyond returning false.
func VerifySecret(encoded, secret string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(secret), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// RandomPassword returns a URL-safe random secret, used for invited staff
// accounts before the invitee sets their own password.
func RandomPassword() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "tms-" + base64.RawURLEncoding.EncodeToString([]byte("fallback-entropy-unavailable"))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
