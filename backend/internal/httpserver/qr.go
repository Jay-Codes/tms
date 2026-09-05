package httpserver

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/storage"
)

// Crockford base32 (no I, L, O, U) — unambiguous when a code is read off a
// printed sticker and typed by hand (SPEC §3.1).
const (
	crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	unitCodeLen       = 10
	unitCodeAttempts  = 5
	qrPNGSize         = 512
)

// randomUnitCode draws a 10-character code from crypto/rand. 256 is a multiple
// of the 32-symbol alphabet, so the modulo introduces no bias.
func randomUnitCode() (string, error) {
	buf := make([]byte, unitCodeLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("unit code: %w", err)
	}
	out := make([]byte, unitCodeLen)
	for i, b := range buf {
		out[i] = crockfordAlphabet[int(b)%len(crockfordAlphabet)]
	}
	return string(out), nil
}

// newUnitCode returns a code that is free at the time of the check. The unique
// index on units.unit_code is still the authority: callers retry the insert on
// a unique violation.
func (s *Server) newUnitCode(ctx context.Context, q *sqlc.Queries) (string, error) {
	for i := 0; i < unitCodeAttempts; i++ {
		code, err := randomUnitCode()
		if err != nil {
			return "", err
		}
		taken, err := q.UnitCodeTaken(ctx, code)
		if err != nil {
			return "", err
		}
		if !taken {
			return code, nil
		}
	}
	return "", fmt.Errorf("unit code: no free code after %d attempts", unitCodeAttempts)
}

// qrObjectKey is the deterministic MinIO key for a unit's QR PNG, so
// regenerating overwrites rather than accumulating objects (SPEC §7).
func qrObjectKey(orgID, unitID string) string { return orgID + "/" + unitID + ".png" }

// generateQR renders the unit's scan URL as a PNG, stores it in the `qrcodes`
// bucket and returns a short-lived presigned download URL.
func (s *Server) generateQR(ctx context.Context, orgID, unitID, unitCode string) (scanURL, pngURL string, err error) {
	scanURL = s.scanURL(unitCode)
	if s.deps.Storage == nil {
		return scanURL, "", errStorageUnavailable
	}
	png, err := qrcode.Encode(scanURL, qrcode.Medium, qrPNGSize)
	if err != nil {
		return scanURL, "", fmt.Errorf("qr encode: %w", err)
	}
	key := qrObjectKey(orgID, unitID)
	// A storage failure is wrapped so the handlers can answer 503 rather than
	// 500: MinIO being down is not a bug in the request (API.md).
	if err := s.deps.Storage.PutBytes(ctx, storage.BucketQR, key, png, "image/png"); err != nil {
		return scanURL, "", fmt.Errorf("%w: %v", errStorageUnavailable, err)
	}
	pngURL, err = s.deps.Storage.PresignGet(ctx, storage.BucketQR, key, qrPresignTTL)
	if err != nil {
		return scanURL, "", fmt.Errorf("%w: %v", errStorageUnavailable, err)
	}
	return scanURL, pngURL, nil
}

// qrPresignTTL is the lifetime of the QR download links (API.md: 15 minutes).
const qrPresignTTL = 15 * time.Minute
