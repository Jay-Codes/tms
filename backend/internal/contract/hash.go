package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// Snapshot is everything a contract's hash covers: the rendered terms plus the
// commercial facts they describe (SPEC §5.5 — "nothing about the document can
// change after it enters pending_signature").
type Snapshot struct {
	TermsHTML         string
	UnitID            string
	RenterUserID      string
	RentAmount        int64
	RentPeriodDays    int
	PaymentPeriodDays int
	TermDays          int
	StartDate         string // YYYY-MM-DD
	EndDate           string // YYYY-MM-DD
	DueDay            *int
}

// Hash is the `snapshot_hash` stored on the contract and recomputed by
// GET /contracts/{id}/verify: the hex SHA-256 of the fields joined by "|", in
// the order API.md fixes. A NULL due day contributes an empty field, so a
// contract without one hashes differently from one due on the 1st.
func (s Snapshot) Hash() string {
	dueDay := ""
	if s.DueDay != nil {
		dueDay = strconv.Itoa(*s.DueDay)
	}
	payload := strings.Join([]string{
		s.TermsHTML,
		s.UnitID,
		s.RenterUserID,
		strconv.FormatInt(s.RentAmount, 10),
		strconv.Itoa(s.RentPeriodDays),
		strconv.Itoa(s.PaymentPeriodDays),
		strconv.Itoa(s.TermDays),
		s.StartDate,
		s.EndDate,
		dueDay,
	}, "|")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}
