// Package validate holds server-side request validation (SPEC §8: frontend
// validation is UX only). Handlers accumulate field errors in a Fields map and
// surface them as the `errors` object of an RFC-7807 problem document.
package validate

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode"
)

// Fields collects per-field validation messages keyed by request field name.
type Fields map[string]string

// Add records a message for a field (first message per field wins).
func (f Fields) Add(field, msg string) {
	if f == nil {
		return
	}
	if _, exists := f[field]; !exists {
		f[field] = msg
	}
}

// Empty reports whether validation passed.
func (f Fields) Empty() bool { return len(f) == 0 }

// ErrInvalidPhone is returned by NormalizePhone for unusable input.
var ErrInvalidPhone = errors.New("invalid Tanzanian phone number")

// NormalizePhone converts the accepted Tanzanian input forms to E.164.
//
// Accepted: 07XXXXXXXX, 7XXXXXXXX, 2557XXXXXXXX, +2557XXXXXXXX (spaces,
// dashes, parentheses and dots ignored). Output is always +2557XXXXXXXX.
// Mobile prefixes in Tanzania are 6 and 7; both are accepted.
func NormalizePhone(in string) (string, error) {
	var digits strings.Builder
	plus := false
	for i, r := range strings.TrimSpace(in) {
		switch {
		case r == '+' && i == 0:
			plus = true
		case unicode.IsDigit(r):
			digits.WriteRune(r)
		case r == ' ' || r == '-' || r == '(' || r == ')' || r == '.':
			// separators are ignored
		default:
			return "", ErrInvalidPhone
		}
	}
	d := digits.String()

	var national string // the 9 digits after the country code
	switch {
	case strings.HasPrefix(d, "255") && len(d) == 12:
		national = d[3:]
	case strings.HasPrefix(d, "0") && len(d) == 10:
		national = d[1:]
	case len(d) == 9 && !plus:
		national = d
	default:
		return "", ErrInvalidPhone
	}

	if national[0] != '6' && national[0] != '7' {
		return "", ErrInvalidPhone
	}
	return "+255" + national, nil
}

// Phone validates and normalizes, recording a field error on failure.
func (f Fields) Phone(field, in string) string {
	if strings.TrimSpace(in) == "" {
		f.Add(field, "phone is required")
		return ""
	}
	p, err := NormalizePhone(in)
	if err != nil {
		f.Add(field, "must be a Tanzanian mobile number (07…, 2557…, +2557…)")
		return ""
	}
	return p
}

// Email validates an address and returns it lower-cased and trimmed.
func (f Fields) Email(field, in string) string {
	v := strings.ToLower(strings.TrimSpace(in))
	if v == "" {
		f.Add(field, "email is required")
		return ""
	}
	addr, err := mail.ParseAddress(v)
	if err != nil || addr.Address != v || !strings.Contains(v, ".") {
		f.Add(field, "must be a valid email address")
		return ""
	}
	return v
}

// Required trims and requires a non-empty value.
func (f Fields) Required(field, in string) string {
	v := strings.TrimSpace(in)
	if v == "" {
		f.Add(field, field+" is required")
	}
	return v
}

// MaxLen caps a string length.
func (f Fields) MaxLen(field, in string, max int) string {
	if len([]rune(in)) > max {
		f.Add(field, fmt.Sprintf("must be at most %d characters", max))
	}
	return in
}

// Password enforces the minimum password policy (SPEC/API.md: min 8).
func (f Fields) Password(field, in string) string {
	if len(in) < 8 {
		f.Add(field, "must be at least 8 characters")
	}
	if len(in) > 200 {
		f.Add(field, "must be at most 200 characters")
	}
	return in
}

// PIN enforces a 4–6 digit renter PIN.
func (f Fields) PIN(field, in string) string {
	if len(in) < 4 || len(in) > 6 || !allDigits(in) {
		f.Add(field, "must be 4 to 6 digits")
	}
	return in
}

// OneOf checks membership in an allowed set.
func (f Fields) OneOf(field, in string, allowed ...string) string {
	for _, a := range allowed {
		if in == a {
			return in
		}
	}
	f.Add(field, "must be one of: "+strings.Join(allowed, ", "))
	return in
}

// allDigits reports whether s is non-empty and made only of ASCII 0-9. The
// check is deliberately ASCII-only: unicode.IsDigit also accepts Arabic-Indic
// and other digit forms, which are not valid PIN/OTP input and would not
// round-trip through the numeric comparisons downstream.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// IsDigits reports whether s is non-empty and all ASCII digits.
func IsDigits(s string) bool { return allDigits(s) }

// Slugify turns an org name into a URL-safe slug.
func Slugify(name string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "org"
	}
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	return s
}
