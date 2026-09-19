package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/notify"
	"tms/backend/internal/storage"
	"tms/backend/internal/validate"
)

// Proof of payment (PLAN2 §16.1, SPEC §5.7, FLOWS 7).
//
// The renter files a claim with a photo; the landlord accepts or rejects it.
// Accepting runs the *same* allocator POST /payments runs (allocatePayment),
// so the overpay confirm, the exceeds-balance refusal and the later reversal
// all behave exactly as the landlord already knows them — the proof is a
// doorway to the payment path, never a second copy of it.

func notFoundProof(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such proof of payment")
}

// proofNotPending is the 409 for a decision on a claim that has already been
// decided, and for a withdrawal of one. The status is the whole answer.
func proofNotPending(w http.ResponseWriter) {
	conflictCode(w, "proof_not_pending", "proof already decided",
		"this proof of payment has already been accepted or rejected")
}

// storageUnavailableProofs is the 503 the proof routes answer with MinIO down:
// the evidence cannot be stored or read, and a claim without it is not one.
func storageUnavailableProofs(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusServiceUnavailable, "storage unavailable",
		"proofs of payment cannot be uploaded or read right now")
}

// cacheUnavailableProofs is the 503 for an upload ticket that cannot be
// written or read. The ticket is what binds an object key to the renter who
// was allowed to write it, so a submission without one is refused rather than
// waved through: Redis being ephemeral costs a re-upload here, not isolation.
func cacheUnavailableProofs(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusServiceUnavailable, "upload unavailable",
		"the upload could not be confirmed; ask for a new upload link and try again")
}

// ------------------------------------------------------------- the ticket --

// proofTicket is what an issued presigned PUT promises: who may redeem it, for
// which contract, and exactly what they said they were uploading.
//
// The completion callback compares MinIO's own answer against these fields, so
// a renter cannot presign a 2 KB PNG and submit a 4 MiB PDF, and cannot name
// an object key that was issued to somebody else.
type proofTicket struct {
	OrgID       string `json:"org_id"`
	RenterID    string `json:"renter_id"`
	ContractID  string `json:"contract_id"`
	ObjectKey   string `json:"object_key"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

const proofTicketPrefix = "proof:ticket:"

func (s *Server) putProofTicket(ctx context.Context, proofID string, t proofTicket) error {
	rc := redisOf(s.deps.Cache)
	if rc == nil {
		return errors.New("proof: no cache")
	}
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return rc.Set(ctx, proofTicketPrefix+proofID, raw, proofTicketTTL).Err()
}

// takeProofTicket redeems a ticket once. A second submission against the same
// key finds nothing, which is what stops one upload becoming two claims.
func (s *Server) takeProofTicket(ctx context.Context, proofID string) (proofTicket, bool, error) {
	rc := redisOf(s.deps.Cache)
	if rc == nil {
		return proofTicket{}, false, errors.New("proof: no cache")
	}
	raw, err := rc.GetDel(ctx, proofTicketPrefix+proofID).Result()
	if errors.Is(err, redis.Nil) {
		return proofTicket{}, false, nil
	}
	if err != nil {
		return proofTicket{}, false, err
	}
	var t proofTicket
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return proofTicket{}, false, err
	}
	return t, true, nil
}

// newProofID mints the id the object key is built from. It is a v4 UUID from
// crypto/rand: the key is public in the sense that it appears in a presigned
// URL, so it must not be guessable from another proof's.
func newProofID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

// ------------------------------------------------- POST /me/proofs/upload --

// handleProofUpload mints the presigned PUT and the ticket that goes with it.
//
// The key is derived from the org and a freshly minted proof id — never from
// anything the client sends — so a caller cannot address another org's prefix
// or overwrite an existing proof whatever they ask for.
func (s *Server) handleProofUpload(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	// Counted before object storage is consulted: the limit is on how often a
	// renter may ask, and it must hold whether or not MinIO is reachable.
	if res := s.limiter.Allow(r.Context(), "proof:upload:"+p.UserIDString(),
		proofUploadLimit, proofUploadWindow); !res.Allowed {
		tooMany(w, res, "too many upload links; try again shortly")
		return
	}

	var body struct {
		ContractID  string `json:"contract_id"`
		ContentType string `json:"content_type"`
		SizeBytes   int64  `json:"size_bytes"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	contractID := uuidField(f, "contract_id", body.ContractID, true)
	contentType := strings.ToLower(strings.TrimSpace(body.ContentType))
	ext, ok := proofContentTypes[contentType]
	if !ok {
		f.Add("content_type", "must be image/jpeg, image/png or application/pdf")
	}
	if body.SizeBytes <= 0 || body.SizeBytes > proofMaxBytes {
		f.Add("size_bytes", "must be between 1 byte and 5 MiB")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	contract, ok := s.payableRenterContract(w, r, contractID, p.UserID)
	if !ok {
		return
	}
	if s.deps.Storage == nil {
		storageUnavailableProofs(w)
		return
	}

	proofID, err := newProofID()
	if err != nil {
		s.serverError(w, r, "proof.upload.id", err)
		return
	}
	orgID := db.UUIDString(contract.OrgID)
	objectKey := proofKey(orgID, proofID, ext)
	url, err := s.deps.Storage.PresignPut(r.Context(), storage.BucketProofs, objectKey, proofUploadTTL)
	if err != nil {
		s.logger.Error("proof presign put failed", "error", err)
		storageUnavailableProofs(w)
		return
	}
	if err := s.putProofTicket(r.Context(), proofID, proofTicket{
		OrgID: orgID, RenterID: p.UserIDString(), ContractID: db.UUIDString(contract.ID),
		ObjectKey: objectKey, ContentType: contentType, SizeBytes: body.SizeBytes,
	}); err != nil {
		s.logger.Error("proof ticket could not be stored", "error", err)
		cacheUnavailableProofs(w)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"proof_id":   proofID,
		"upload_url": url,
		"object_key": objectKey,
		"expires_in": int(proofUploadTTL.Seconds()),
		// The completion callback checks the type MinIO recorded, so the PUT
		// has to carry it: a presigned URL does not set it for the client.
		"headers": map[string]string{"Content-Type": contentType},
	})
}

// -------------------------------------------------------- POST /me/proofs --

// handleCreateProof files the claim once the object has landed.
//
// A presigned PUT can enforce neither type nor size, so both are checked here
// against what MinIO holds *and* against what the ticket promised, and a
// rejected object is removed rather than left unreferenced in the bucket
// (SPEC §7).
func (s *Server) handleCreateProof(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	if res := s.limiter.Allow(r.Context(), "proof:submit:"+p.UserIDString(),
		proofSubmitLimit, proofSubmitWindow); !res.Allowed {
		tooMany(w, res, "too many proofs of payment today; try again tomorrow")
		return
	}

	var body struct {
		ContractID string  `json:"contract_id"`
		ScheduleID string  `json:"schedule_id"`
		Amount     int64   `json:"amount"`
		PaidAt     string  `json:"paid_at"`
		Method     string  `json:"method"`
		Reference  *string `json:"reference"`
		Note       *string `json:"note"`
		ObjectKey  string  `json:"object_key"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}

	f := validate.Fields{}
	contractID := uuidField(f, "contract_id", body.ContractID, true)
	scheduleID := uuidField(f, "schedule_id", body.ScheduleID, false)
	checkAmount(f, "amount", body.Amount)
	method := strings.TrimSpace(body.Method)
	if !proofMethods[method] {
		f.Add("method", "must be bank_transfer or mobile_money_manual")
	}
	reference := trimmedOpt(f, "reference", body.Reference, proofReferenceMax)
	note := trimmedOpt(f, "note", body.Note, proofNoteMax)
	paidAt := parsePaidAt(f, "paid_at", body.PaidAt)
	objectKey := strings.TrimSpace(body.ObjectKey)
	if objectKey == "" {
		f.Add("object_key", "object_key is required")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	contract, ok := s.payableRenterContract(w, r, contractID, p.UserID)
	if !ok {
		return
	}
	// A named instalment must be one of this contract's own, in this org. A
	// schedule id from elsewhere is a 404 like any other foreign id.
	if scheduleID.Valid {
		sched, err := s.q.GetSchedule(r.Context(), sqlc.GetScheduleParams{
			OrgID: contract.OrgID, ID: scheduleID,
		})
		if isNoRows(err) || (err == nil && db.UUIDString(sched.ContractID) != db.UUIDString(contract.ID)) {
			httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such schedule on this contract")
			return
		}
		if err != nil {
			s.serverError(w, r, "proof.create.schedule", err)
			return
		}
	}

	// The daily ceiling again, in Postgres. The Redis limiter fails open when
	// the cache is unreachable (SPEC §8) and a write ceiling must not.
	filedToday, err := s.q.CountProofsToday(r.Context(), p.UserID)
	if err != nil {
		s.serverError(w, r, "proof.create.count", err)
		return
	}
	if filedToday >= proofSubmitLimit {
		tooMany(w, s.limiter.Allow(r.Context(), "proof:submit:"+p.UserIDString(), 0, proofSubmitWindow),
			"too many proofs of payment today; try again tomorrow")
		return
	}

	if s.deps.Storage == nil {
		storageUnavailableProofs(w)
		return
	}

	// The proof id is the middle segment of the key the server issued; a key
	// of any other shape was not issued here.
	proofID, ok := proofIDFromKey(db.UUIDString(contract.OrgID), objectKey)
	if !ok {
		httpx.WriteProblemFields(w, http.StatusBadRequest, "invalid object key",
			"that key was not issued for this renter",
			map[string]string{"object_key": "must be an upload issued for this proof"})
		return
	}
	ticket, found, err := s.takeProofTicket(r.Context(), proofID)
	if err != nil {
		s.logger.Error("proof ticket could not be read", "error", err)
		cacheUnavailableProofs(w)
		return
	}
	if !found || ticket.ObjectKey != objectKey ||
		ticket.RenterID != p.UserIDString() ||
		ticket.ContractID != db.UUIDString(contract.ID) {
		httpx.WriteProblemFields(w, http.StatusBadRequest, "invalid object key",
			"that key was not issued for this renter",
			map[string]string{"object_key": "must be an upload issued for this proof"})
		return
	}

	info, err := s.deps.Storage.Stat(r.Context(), storage.BucketProofs, objectKey)
	if err != nil {
		httpx.WriteProblemFields(w, http.StatusBadRequest, "upload not found",
			"no uploaded file was found for this proof",
			map[string]string{"object_key": "no object has been uploaded under this key"})
		return
	}
	reject := func(field, msg string) {
		if err := s.deps.Storage.Remove(r.Context(), storage.BucketProofs, objectKey); err != nil {
			s.logger.Warn("could not remove rejected proof", "object_key", objectKey, "error", err)
		}
		httpx.WriteProblemFields(w, http.StatusBadRequest, "invalid proof",
			"the uploaded file was rejected", map[string]string{field: msg})
	}
	if info.Size > proofMaxBytes || info.Size != ticket.SizeBytes {
		reject("size_bytes", "must match the size the upload link was issued for, and be at most 5 MiB")
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(info.ContentType))
	ext, allowed := proofContentTypes[contentType]
	if !allowed || contentType != ticket.ContentType ||
		proofKey(db.UUIDString(contract.OrgID), proofID, ext) != objectKey {
		reject("content_type", "must match the type the upload link was issued for")
		return
	}

	var created sqlc.PaymentProof
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		created, err = q.CreatePaymentProof(r.Context(), sqlc.CreatePaymentProofParams{
			ID: db.MustUUID(proofID), OrgID: contract.OrgID, ContractID: contract.ID,
			ScheduleID: scheduleID, RenterUserID: p.UserID,
			Amount: body.Amount, PaidAt: db.TS(paidAt), Method: method,
			Reference: reference, Note: note,
			ObjectKey: objectKey, ContentType: contentType, SizeBytes: info.Size,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       db.UUIDString(contract.OrgID),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionProofSubmit,
			EntityType:  audit.EntityPaymentProof,
			EntityID:    proofID,
			After: map[string]any{
				"contract_id": db.UUIDString(contract.ID), "amount": body.Amount,
				"method": method, "paid_at": paidAt.Format(time.RFC3339),
				"object_key": objectKey, "content_type": contentType, "size_bytes": info.Size,
			},
		})
	}); err != nil {
		s.serverError(w, r, "proof.create.tx", err)
		return
	}

	out, ok := s.reloadProof(w, r, created.ID, pgtype.UUID{}, p.UserID, "proof.create.get")
	if !ok {
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"proof": out})
}

// --------------------------------------------------------- GET /me/proofs --

func (s *Server) handleListMyProofs(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	f := validate.Fields{}
	page := parseListPage(r, f)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	rows, err := s.q.ListMyPaymentProofs(r.Context(), sqlc.ListMyPaymentProofsParams{
		RenterUserID: p.UserID, RowLimit: page.Limit,
		CursorAt: page.CursorAt, CursorID: page.CursorID,
	})
	if err != nil {
		s.serverError(w, r, "proof.list.mine", err)
		return
	}
	items := make([]proofResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, toProof(proofRowOfMine(row)))
	}
	var next *string
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		next = nextCursor(len(rows), page.Limit, last.CreatedAt.Time, db.UUIDString(last.ID))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// --------------------------------------------------- DELETE /me/proofs/{id} --

// handleWithdrawProof lets a renter take back a claim nobody has ruled on.
//
// The row is really deleted, which is deliberate and the only such case in the
// money tables: an unreviewed claim the renter retracted is not a fact the
// ledger needs. The audit row records that it happened, and the object goes
// with it — a photo of somebody's bank app has no reason to outlive the claim.
func (s *Server) handleWithdrawProof(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundProof(w)
		return
	}
	row, err := s.q.GetPaymentProof(r.Context(), sqlc.GetPaymentProofParams{
		ID: id, RenterUserID: p.UserID,
	})
	if isNoRows(err) {
		notFoundProof(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "proof.withdraw.get", err)
		return
	}
	if row.Status != proofSubmitted {
		proofNotPending(w)
		return
	}

	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.WithdrawPaymentProof(r.Context(), sqlc.WithdrawPaymentProofParams{
			ID: id, RenterUserID: p.UserID,
		}); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       db.UUIDString(row.OrgID),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionProofWithdraw,
			EntityType:  audit.EntityPaymentProof,
			EntityID:    db.UUIDString(id),
			Before: map[string]any{
				"status": row.Status, "amount": row.Amount,
				"contract_id": db.UUIDString(row.ContractID),
			},
		})
	}); err != nil {
		if isNoRows(err) {
			// Decided between the read and the write.
			proofNotPending(w)
			return
		}
		s.serverError(w, r, "proof.withdraw.tx", err)
		return
	}
	// The row no longer references the object, so a failure here leaves a file
	// nothing points at — untidy, not incorrect.
	if s.deps.Storage != nil {
		if err := s.deps.Storage.Remove(r.Context(), storage.BucketProofs, row.ObjectKey); err != nil {
			s.logger.Warn("could not remove withdrawn proof", "object_key", row.ObjectKey, "error", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------ GET /proofs --

// handleListProofs is the landlord's review queue. It is ordered oldest first,
// unlike every other listing in the API: a queue is worked from the front, and
// the renter who has waited longest is the one owed an answer.
func (s *Server) handleListProofs(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	f := validate.Fields{}
	page := parseListPage(r, f)

	status := proofSubmitted
	if v := strings.TrimSpace(r.URL.Query().Get("status")); v != "" {
		if !proofStatuses[strings.ToLower(v)] {
			f.Add("status", "must be submitted, accepted or rejected")
		} else {
			status = strings.ToLower(v)
		}
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	rows, err := s.q.ListPaymentProofs(r.Context(), sqlc.ListPaymentProofsParams{
		OrgID: p.OrgID, Status: status, RowLimit: page.Limit,
		CursorAt: page.CursorAt, CursorID: page.CursorID,
	})
	if err != nil {
		s.serverError(w, r, "proof.list", err)
		return
	}
	items := make([]proofResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, toProof(proofRowOfList(row)))
	}
	var next *string
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		next = nextCursor(len(rows), page.Limit, last.CreatedAt.Time, db.UUIDString(last.ID))
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items": items, "next_cursor": next, "status": status,
	})
}

// ---------------------------------------------------- GET /proofs/summary --

// handleProofSummary is the nav badge, and nothing else. It is a separate
// endpoint rather than a field on the listing because the badge is drawn on
// every landlord screen, including the ones that never list a proof.
func (s *Server) handleProofSummary(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	count, err := s.q.CountSubmittedProofs(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "proof.summary", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"submitted_count": count})
}

// ------------------------------------------------------- GET /proofs/{id} --

// handleGetProof returns the claim with a short-lived link to the evidence.
// Issuing that link is audited (`proof.view`), the way reading a KYC document
// is: the file is a renter's banking screenshot, and who looked at it is part
// of the record (SPEC §8).
func (s *Server) handleGetProof(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundProof(w)
		return
	}
	row, err := s.q.GetPaymentProof(r.Context(), sqlc.GetPaymentProofParams{ID: id, OrgID: p.OrgID})
	if isNoRows(err) {
		notFoundProof(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "proof.get", err)
		return
	}

	out := toProof(row)
	if s.deps.Storage != nil {
		url, err := s.deps.Storage.PresignGet(r.Context(), storage.BucketProofs, row.ObjectKey, proofReadTTL)
		if err != nil {
			s.logger.Error("proof presign get failed", "error", err)
		} else {
			out.ViewURL = &url
			if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
				return audit.Record(r.Context(), q, audit.Entry{
					OrgID:       p.OrgIDString(),
					ActorUserID: p.UserIDString(),
					Action:      audit.ActionProofView,
					EntityType:  audit.EntityPaymentProof,
					EntityID:    db.UUIDString(id),
					After:       map[string]any{"object_key": row.ObjectKey},
				})
			}); err != nil {
				s.serverError(w, r, "proof.view.audit", err)
				return
			}
		}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"proof": out})
}

// ----------------------------------------------- POST /proofs/{id}/accept --

// handleAcceptProof turns a believed claim into a payment.
//
// It runs allocatePayment — the same function POST /payments runs — with the
// proof's own fields, overridden by anything the landlord corrected in the
// review sheet. Every refusal the allocator can raise reaches the client
// unchanged, `overpay_confirm_required` included, which is what lets the UI
// reuse the record-payment confirm sheet verbatim (PLAN2 §16.1).
func (s *Server) handleAcceptProof(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundProof(w)
		return
	}
	var body struct {
		Amount               *int64  `json:"amount"`
		ScheduleID           *string `json:"schedule_id"`
		PaidAt               *string `json:"paid_at"`
		AllowOverpayRollover bool    `json:"allow_overpay_rollover"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}

	row, err := s.q.GetPaymentProof(r.Context(), sqlc.GetPaymentProofParams{ID: id, OrgID: p.OrgID})
	if isNoRows(err) {
		notFoundProof(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "proof.accept.get", err)
		return
	}
	if row.Status != proofSubmitted {
		proofNotPending(w)
		return
	}

	// The claim's own fields are the defaults; the landlord may correct any of
	// them, and the audit row carries both readings.
	f := validate.Fields{}
	amount := row.Amount
	if body.Amount != nil {
		amount = *body.Amount
		checkAmount(f, "amount", amount)
	}
	scheduleID := row.ScheduleID
	if body.ScheduleID != nil {
		scheduleID = optUUIDPtr(f, "schedule_id", body.ScheduleID)
	}
	paidAt := row.PaidAt.Time
	if body.PaidAt != nil {
		paidAt = parsePaidAt(f, "paid_at", *body.PaidAt)
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	contract, err := s.q.GetContract(r.Context(), sqlc.GetContractParams{
		ID: row.ContractID, OrgID: p.OrgID,
	})
	if isNoRows(err) {
		notFoundProof(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "proof.accept.contract", err)
		return
	}
	org, err := s.q.GetOrg(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "proof.accept.org", err)
		return
	}
	settings := parseSettings(org.Settings)
	brand := s.brandingAssets(r.Context(), p.OrgID, org.Name)

	var (
		out      allocationOutcome
		accepted sqlc.PaymentProof
	)
	txErr := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		out, err = s.allocatePayment(r.Context(), q, allocationRequest{
			OrgID: p.OrgID, ActorUserID: p.UserID, Contract: contract,
			ScheduleID: scheduleID, Amount: amount, Method: row.Method,
			Reference: row.Reference, Note: row.Note, PaidAt: paidAt,
			AllowOverpayRollover: body.AllowOverpayRollover,
			Settings:             settings, OrgName: brand.DisplayName,
		})
		if err != nil {
			return err
		}
		accepted, err = q.AcceptPaymentProof(r.Context(), sqlc.AcceptPaymentProofParams{
			OrgID: p.OrgID, ID: id,
			PaymentID:        db.MustUUID(out.PaymentID),
			ReviewedByUserID: p.UserID,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionProofAccept,
			EntityType:  audit.EntityPaymentProof,
			EntityID:    db.UUIDString(id),
			Before: map[string]any{
				"status": row.Status, "amount": row.Amount,
				"schedule_id": db.UUIDString(row.ScheduleID),
				"paid_at":     row.PaidAt.Time.Format(time.RFC3339),
			},
			After: map[string]any{
				"status": accepted.Status, "amount": amount,
				"schedule_id": db.UUIDString(scheduleID),
				"paid_at":     paidAt.Format(time.RFC3339),
				"payment_id":  out.PaymentID,
				"rollover":    body.AllowOverpayRollover,
			},
		})
	})
	if isNoRows(txErr) {
		// The proof was decided between the read and the write.
		proofNotPending(w)
		return
	}
	if !s.allocationRefused(w, r, txErr, "proof.accept.tx") {
		return
	}
	s.enqueueNotifications(r.Context(), out.NotifyID)

	proofOut, ok := s.reloadProof(w, r, id, p.OrgID, pgtype.UUID{}, "proof.accept.get")
	if !ok {
		return
	}
	paymentOut, ok := s.reloadPayment(w, r, out.PaymentID, p.OrgID, pgtype.UUID{}, true)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"proof":     proofOut,
		"payment":   paymentOut,
		"schedules": affectedItems(out.Affected, contractBlock(contract)),
	})
}

// ----------------------------------------------- POST /proofs/{id}/reject --

// handleRejectProof refuses a claim and tells the renter why. The reason is
// mandatory and reaches them by SMS: a proof that simply vanished from their
// screen would leave them with no idea what to do next (FLOWS 7).
func (s *Server) handleRejectProof(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundProof(w)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	reason := f.MaxLen("reason", f.Required("reason", body.Reason), proofReasonMax)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	row, err := s.q.GetPaymentProof(r.Context(), sqlc.GetPaymentProofParams{ID: id, OrgID: p.OrgID})
	if isNoRows(err) {
		notFoundProof(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "proof.reject.get", err)
		return
	}
	if row.Status != proofSubmitted {
		proofNotPending(w)
		return
	}
	org, err := s.q.GetOrg(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "proof.reject.org", err)
		return
	}
	settings := parseSettings(org.Settings)
	brand := s.brandingAssets(r.Context(), p.OrgID, org.Name)

	contract, err := s.q.GetContract(r.Context(), sqlc.GetContractParams{
		ID: row.ContractID, OrgID: p.OrgID,
	})
	if err != nil && !isNoRows(err) {
		s.serverError(w, r, "proof.reject.contract", err)
		return
	}

	var notifyID string
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		rejected, err := q.RejectPaymentProof(r.Context(), sqlc.RejectPaymentProofParams{
			OrgID: p.OrgID, ID: id, RejectionReason: &reason, ReviewedByUserID: p.UserID,
		})
		if err != nil {
			return err
		}
		if err := audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionProofReject,
			EntityType:  audit.EntityPaymentProof,
			EntityID:    db.UUIDString(id),
			Before:      map[string]any{"status": row.Status, "amount": row.Amount},
			After:       map[string]any{"status": rejected.Status, "reason": reason},
		}); err != nil {
			return err
		}
		// The proof id is the dedupe subject: one message per decision, and a
		// renter who files a second claim gets a second answer.
		notifyID, err = s.queueContractSMS(r.Context(), q, contractMessage{
			OrgID: p.OrgIDString(), UserID: db.UUIDString(row.RenterUserID),
			ContractID: db.UUIDString(id), Kind: notify.KindProofRejected,
			Lang: settings.SMSLanguage, Phone: db.StrVal(contract.RenterPhone),
			Overrides: settings.notifyOverrides(),
			Vars: notify.Vars{
				Name: row.RenterName, Amount: formatTZS(row.Amount),
				Unit: row.UnitName, Property: row.PropertyName,
				Org: brand.DisplayName, Reason: reason,
			},
		})
		return err
	}); err != nil {
		if isNoRows(err) {
			proofNotPending(w)
			return
		}
		s.serverError(w, r, "proof.reject.tx", err)
		return
	}
	s.enqueueNotifications(r.Context(), notifyID)

	out, ok := s.reloadProof(w, r, id, p.OrgID, pgtype.UUID{}, "proof.reject.get")
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"proof": out})
}

// --------------------------------------------------------------- helpers --

// payableRenterContract loads a contract the caller rents and refuses one that
// is not running. A contract belonging to somebody else is a 404 — "not yours"
// and "not there" must be indistinguishable (API.md).
func (s *Server) payableRenterContract(
	w http.ResponseWriter, r *http.Request, contractID, renterID pgtype.UUID,
) (sqlc.GetContractRow, bool) {
	contract, err := s.q.GetContract(r.Context(), sqlc.GetContractParams{
		ID: contractID, RenterUserID: renterID,
	})
	if isNoRows(err) {
		notFoundContract(w)
		return sqlc.GetContractRow{}, false
	}
	if err != nil {
		s.serverError(w, r, "proof.contract", err)
		return sqlc.GetContractRow{}, false
	}
	if contract.Status != contractActive && contract.Status != contractExpiring {
		conflictCode(w, "contract_not_active", "contract not running",
			"a proof of payment can only be filed against a running contract")
		return sqlc.GetContractRow{}, false
	}
	return contract, true
}

// reloadProof re-reads a proof for the response body, scoped to whichever
// principal asked for it.
func (s *Server) reloadProof(
	w http.ResponseWriter, r *http.Request, id, orgID, renterID pgtype.UUID, op string,
) (proofResponse, bool) {
	row, err := s.q.GetPaymentProof(r.Context(), sqlc.GetPaymentProofParams{
		ID: id, OrgID: orgID, RenterUserID: renterID,
	})
	if isNoRows(err) {
		notFoundProof(w)
		return proofResponse{}, false
	}
	if err != nil {
		s.serverError(w, r, op, err)
		return proofResponse{}, false
	}
	return toProof(row), true
}

// proofIDFromKey pulls the proof id out of an object key, refusing anything
// that is not `{org_id}/{uuid}.{ext}` under the caller's own org. The key is
// never trusted as a path: it is parsed, and its parts are checked.
func proofIDFromKey(orgID, key string) (string, bool) {
	prefix := orgID + "/"
	if !strings.HasPrefix(key, prefix) {
		return "", false
	}
	rest := key[len(prefix):]
	dot := strings.LastIndex(rest, ".")
	if dot < 0 {
		return "", false
	}
	id, ext := rest[:dot], rest[dot+1:]
	if !validProofExt(ext) {
		return "", false
	}
	if _, err := db.ParseUUID(id); err != nil {
		return "", false
	}
	return id, true
}

func validProofExt(ext string) bool {
	for _, e := range proofContentTypes {
		if e == ext {
			return true
		}
	}
	return false
}

// parsePaidAt reads the "when the money moved" field the proof and the payment
// endpoints share: RFC3339, and never more than a day ahead of now.
func parsePaidAt(f validate.Fields, field, in string) time.Time {
	v := strings.TrimSpace(in)
	if v == "" {
		return time.Now().UTC()
	}
	t, err := time.Parse(time.RFC3339, v)
	switch {
	case err != nil:
		f.Add(field, "must be a timestamp (RFC3339)")
		return time.Now().UTC()
	case t.After(time.Now().UTC().Add(paidAtFutureToleranceH * time.Hour)):
		f.Add(field, "cannot be more than a day in the future")
		return time.Now().UTC()
	default:
		return t.UTC()
	}
}
