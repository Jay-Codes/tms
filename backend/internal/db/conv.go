package db

import (
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// ErrBadUUID is returned by ParseUUID for malformed input.
var ErrBadUUID = errors.New("db: invalid uuid")

// ParseUUID converts a canonical UUID string to pgtype.UUID.
func ParseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, ErrBadUUID
	}
	return u, nil
}

// MustUUID parses s, returning the zero (NULL) UUID on failure. Use only where
// an invalid id is semantically equivalent to "no match".
func MustUUID(s string) pgtype.UUID {
	u, err := ParseUUID(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return u
}

// UUIDString renders a pgtype.UUID as its canonical string ("" when NULL).
func UUIDString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	v, err := u.Value()
	if err != nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// TS wraps a time as a non-NULL pgtype.Timestamptz.
func TS(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// TSPtr wraps an optional time as a possibly-NULL pgtype.Timestamptz.
func TSPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// Str returns a pointer to s, or nil when s is empty — the shape sqlc uses for
// nullable text columns.
func Str(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// StrVal dereferences an optional string, yielding "" for nil.
func StrVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
