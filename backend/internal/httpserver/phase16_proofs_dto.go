package httpserver

import (
	"time"

	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
)

// Proof of payment: shapes and bounds (SPEC §5.7, PLAN2 §16.1, FLOWS 7).
//
// A proof is a renter's claim that money left their hands, with the evidence
// attached. It is not money in the ledger: only an accepted proof runs the
// allocator, and what the allocator writes is the payment.

const (
	// proofReferenceMax and proofNoteMax match the payment fields the accepted
	// proof is copied into, so nothing the renter typed is truncated on the
	// way through.
	proofReferenceMax = 80
	proofNoteMax      = 500
	// proofReasonMax bounds the landlord's "why not". Long enough for a
	// sentence a renter can act on, short enough to fit one SMS beside the
	// rest of the template.
	proofReasonMax = 200

	// The upload limits FLOWS 7 names: a photo of a transfer confirmation, or
	// the PDF a bank sends. Identical to the receipts set, because a renter's
	// phone camera and a landlord's are the same camera.
	proofMaxBytes  = 5 << 20 // 5 MiB
	proofUploadTTL = 15 * time.Minute
	// proofReadTTL is deliberately shorter than the receipts read TTL: a proof
	// carries a renter's bank details on the face of it, and the link is
	// re-issued on demand from the detail endpoint anyway (SPEC §8).
	proofReadTTL = 5 * time.Minute

	// proofSubmitLimit is the ceiling PLAN2 §16.1 names: ten claims per renter
	// per day. A renter with four tenancies filing for each of them twice is
	// still under it; a script is not.
	proofSubmitLimit  = 10
	proofSubmitWindow = 24 * time.Hour
	// proofUploadLimit caps presigned PUT URLs per renter per hour. It is not
	// the submission ceiling — an upload that fails deserves a retry — only a
	// bound on minting URLs.
	proofUploadLimit  = 30
	proofUploadWindow = time.Hour

	// proofTicketTTL is how long an issued upload ticket stays redeemable. It
	// outlives the presigned URL by a margin, so a slow phone upload that
	// finishes on the last second of the URL can still be completed.
	proofTicketTTL = 30 * time.Minute
)

// proofContentTypes maps an accepted content type to the extension its object
// key carries. The client never names the object, so it cannot name it
// `../something.png` (the receipts rule).
//
//nolint:gochecknoglobals // fixed value set, read-only.
var proofContentTypes = map[string]string{
	"image/jpeg":      "jpg",
	"image/png":       "png",
	"application/pdf": "pdf",
}

// proofMethods are the ways a renter may claim to have paid. `cash` is
// deliberately absent: money handed over in person is recorded by the landlord,
// who was there, and a photo proves nothing about it.
//
//nolint:gochecknoglobals // fixed value set, read-only.
var proofMethods = map[string]bool{
	"bank_transfer":       true,
	"mobile_money_manual": true,
}

// proofStatuses are the three states a claim can be in, and the values
// `GET /proofs?status=` accepts.
//
//nolint:gochecknoglobals // fixed value set, read-only.
var proofStatuses = map[string]bool{
	proofSubmitted: true,
	proofAccepted:  true,
	proofRejected:  true,
}

const (
	proofSubmitted = "submitted"
	proofAccepted  = "accepted"
	proofRejected  = "rejected"
)

// ------------------------------------------------------------- responses --

// proofContract is the identity block a proof carries, so a review queue row
// reads as "Asha, Unit A2, Mikocheni Flats" without a second request. It is
// the same five fields the schedule shape carries.
type proofContract struct {
	ID           string `json:"id"`
	UnitName     string `json:"unit_name"`
	PropertyName string `json:"property_name"`
	RenterName   string `json:"renter_name"`
	RenterUserID string `json:"renter_user_id"`
}

// proofResponse is the `proof` shape, identical everywhere a proof is
// returned. `view_url` is present only where one was issued — the detail
// endpoint — because minting a presigned read for every row of a listing would
// hand out fifty links to open one.
type proofResponse struct {
	ID              string        `json:"id"`
	Contract        proofContract `json:"contract"`
	ScheduleID      *string       `json:"schedule_id"`
	Amount          int64         `json:"amount"`
	PaidAt          time.Time     `json:"paid_at"`
	Method          string        `json:"method"`
	Reference       *string       `json:"reference"`
	Note            *string       `json:"note"`
	ContentType     string        `json:"content_type"`
	SizeBytes       int64         `json:"size_bytes"`
	Status          string        `json:"status"`
	PaymentID       *string       `json:"payment_id"`
	ReviewedAt      *time.Time    `json:"reviewed_at"`
	ReviewedByName  *string       `json:"reviewed_by_name"`
	RejectionReason *string       `json:"rejection_reason"`
	CreatedAt       time.Time     `json:"created_at"`
	ViewURL         *string       `json:"view_url,omitempty"`
}

// proofRow is the union of the three query row shapes: Get, the landlord's
// list and the renter's list select identical columns, so one mapper serves
// all three (the expense-ledger pattern).
type proofRow = sqlc.GetPaymentProofRow

func proofRowOfList(r sqlc.ListPaymentProofsRow) proofRow   { return proofRow(r) }
func proofRowOfMine(r sqlc.ListMyPaymentProofsRow) proofRow { return proofRow(r) }

func toProof(r proofRow) proofResponse {
	out := proofResponse{
		ID: db.UUIDString(r.ID),
		Contract: proofContract{
			ID:           db.UUIDString(r.ContractID),
			UnitName:     r.UnitName,
			PropertyName: r.PropertyName,
			RenterName:   r.RenterName,
			RenterUserID: db.UUIDString(r.RenterUserID),
		},
		Amount:          r.Amount,
		PaidAt:          r.PaidAt.Time,
		Method:          r.Method,
		Reference:       r.Reference,
		Note:            r.Note,
		ContentType:     r.ContentType,
		SizeBytes:       r.SizeBytes,
		Status:          r.Status,
		ReviewedAt:      timePtr(r.ReviewedAt.Valid, r.ReviewedAt.Time),
		ReviewedByName:  r.ReviewedByName,
		RejectionReason: r.RejectionReason,
		CreatedAt:       r.CreatedAt.Time,
	}
	if r.ScheduleID.Valid {
		id := db.UUIDString(r.ScheduleID)
		out.ScheduleID = &id
	}
	if r.PaymentID.Valid {
		id := db.UUIDString(r.PaymentID)
		out.PaymentID = &id
	}
	return out
}

// proofKey is the object key for one proof: `{org_id}/{proof_id}.{ext}`
// (PLAN2 §16.1). Both segments are ids the server minted, so the key cannot be
// steered by a request.
func proofKey(orgID, proofID, ext string) string {
	return orgID + "/" + proofID + "." + ext
}
