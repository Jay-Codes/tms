package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/storage"
	"tms/backend/internal/validate"
)

// Field bounds and KYC upload limits (API.md).
const (
	profileNameMax = 120
	nidaDigits     = 20
	kycMaxBytes    = 5 << 20 // 5 MiB
	kycUploadTTL   = 10 * time.Minute
	kycReadTTL     = 5 * time.Minute
)

// kycContentTypes are the only formats accepted for an ID document. A PDF
// would need a viewer the renter app does not have; a photo of the card is
// what people actually take.
var kycContentTypes = map[string]string{
	"image/jpeg": "jpg",
	"image/png":  "png",
}

// ------------------------------------------------------- GET /me/profile --

func (s *Server) handleGetMyProfile(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	user, err := s.q.GetUserByID(r.Context(), p.UserID)
	if err != nil {
		s.serverError(w, r, "profile.get.user", err)
		return
	}
	// A renter who has never opened the KYC screen still gets a profile shape
	// rather than a 404: the row is created empty on first read.
	if _, err := s.q.EnsureRenterProfile(r.Context(), sqlc.EnsureRenterProfileParams{
		UserID: p.UserID, FullName: user.FullName,
	}); err != nil {
		s.serverError(w, r, "profile.get.ensure", err)
		return
	}
	profile, err := s.loadProfile(r.Context(), p.UserID)
	if err != nil {
		s.serverError(w, r, "profile.get", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"id": db.UUIDString(user.ID), "phone": user.Phone,
			"full_name": user.FullName, "email": user.Email,
			// The renter Profile screen is where the language switch lives, so
			// it is handed the current value with the rest of the account
			// (Phase 13); PATCH /me writes it back.
			"locale": user.Locale,
		},
		"profile": toProfile(profile, user.Email),
	})
}

// loadProfile reads and decrypts one renter profile.
func (s *Server) loadProfile(ctx context.Context, userID pgtype.UUID) (sqlc.GetRenterProfileDecryptedRow, error) {
	return s.q.GetRenterProfileDecrypted(ctx, sqlc.GetRenterProfileDecryptedParams{
		EncKey: s.cfg.NidaEncKey, UserID: userID,
	})
}

// ------------------------------------------------------- PUT /me/profile --

func (s *Server) handlePutMyProfile(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		FullName       string  `json:"full_name"`
		NidaNumber     *string `json:"nida_number"`
		NextOfKinName  string  `json:"next_of_kin_name"`
		NextOfKinPhone string  `json:"next_of_kin_phone"`
		Email          *string `json:"email"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}

	f := validate.Fields{}
	fullName := f.MaxLen("full_name", f.Required("full_name", body.FullName), profileNameMax)
	if len([]rune(fullName)) < 2 {
		f.Add("full_name", "must be at least 2 characters")
	}
	kinName := f.MaxLen("next_of_kin_name", f.Required("next_of_kin_name", body.NextOfKinName), profileNameMax)
	if len([]rune(kinName)) < 2 {
		f.Add("next_of_kin_name", "must be at least 2 characters")
	}
	kinPhone := f.Phone("next_of_kin_phone", body.NextOfKinPhone)

	// An omitted or empty nida_number keeps whatever is already stored: the
	// masked value the client holds cannot be echoed back as an update.
	var nida *string
	if body.NidaNumber != nil {
		if trimmed := strings.TrimSpace(*body.NidaNumber); trimmed != "" {
			if len(trimmed) != nidaDigits || !validate.IsDigits(trimmed) {
				f.Add("nida_number", "must be 20 digits")
			} else {
				nida = &trimmed
			}
		}
	}
	var email *string
	if body.Email != nil {
		if trimmed := strings.TrimSpace(*body.Email); trimmed != "" {
			v := f.Email("email", trimmed)
			email = &v
		}
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	before, err := s.loadProfile(r.Context(), p.UserID)
	if err != nil && !isNoRows(err) {
		s.serverError(w, r, "profile.put.load", err)
		return
	}

	// The stored NIDA decides the derived status when the request omits one.
	effectiveNida := before.NidaNumber
	if nida != nil {
		effectiveNida = *nida
	}
	nextStatus := deriveKYC(before.KycStatus, effectiveNida, &kinName, &kinPhone)

	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.UpsertRenterProfile(r.Context(), sqlc.UpsertRenterProfileParams{
			UserID: p.UserID, FullName: fullName, NidaNumber: nida, EncKey: s.cfg.NidaEncKey,
			NextOfKinName: &kinName, NextOfKinPhone: &kinPhone,
		}); err != nil {
			return err
		}
		if _, err := q.SetRenterKycStatus(r.Context(), sqlc.SetRenterKycStatusParams{
			UserID: p.UserID, KycStatus: nextStatus,
		}); err != nil && !isNoRows(err) {
			// No row comes back when the profile is already `verified`, which
			// this handler must not lower — that is the query doing its job.
			return err
		}
		// The account carries a display name of its own, and the landlord's
		// screens read it wherever the profile row is not joined. A renter who
		// corrects their name on this form must not end up shown under two
		// names, so both records take the value they just typed.
		if _, err := q.SetUserFullName(r.Context(), sqlc.SetUserFullNameParams{
			ID: p.UserID, FullName: fullName,
		}); err != nil {
			return err
		}
		if email != nil {
			if _, err := q.SetUserEmail(r.Context(), sqlc.SetUserEmailParams{
				ID: p.UserID, Email: email,
			}); err != nil {
				return err
			}
		}
		// SPEC §8: NIDA is encrypted at rest and masked in the UI, so it must
		// not be written to the audit trail in the clear either. The audit row
		// records only THAT the number changed.
		return audit.Record(r.Context(), q, audit.Entry{
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionProfileUpdate,
			EntityType:  audit.EntityRenterProfile,
			EntityID:    p.UserIDString(),
			Before: map[string]any{
				"full_name": before.FullName, "kyc_status": kycOut(before.KycStatus),
			},
			After: map[string]any{
				"full_name": fullName, "kyc_status": kycOut(nextStatus),
				"next_of_kin_name": kinName, "nida_changed": nida != nil,
			},
		})
	}); err != nil {
		s.serverError(w, r, "profile.put.tx", err)
		return
	}

	updated, err := s.loadProfile(r.Context(), p.UserID)
	if err != nil {
		s.serverError(w, r, "profile.put.reload", err)
		return
	}
	user, err := s.q.GetUserByID(r.Context(), p.UserID)
	if err != nil {
		s.serverError(w, r, "profile.put.user", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"profile": toProfile(updated, user.Email)})
}

// ------------------------------------------- POST /me/profile/kyc-upload --

func (s *Server) handleKYCUpload(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	// Counted before object storage is consulted: the limit is on how often a
	// session may ask, and it must hold whether or not MinIO is reachable.
	if res := s.limiter.Allow(r.Context(), "kyc:upload:"+p.UserIDString(),
		kycUploadLimit, kycUploadWindow); !res.Allowed {
		tooMany(w, res, "too many upload requests; try again shortly")
		return
	}
	if s.deps.Storage == nil {
		kycStorageUnavailable(w)
		return
	}

	var body struct {
		ContentType string `json:"content_type"`
		SizeBytes   int64  `json:"size_bytes"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	contentType := strings.ToLower(strings.TrimSpace(body.ContentType))
	ext, ok := kycContentTypes[contentType]
	if !ok {
		f.Add("content_type", "must be image/jpeg or image/png")
	}
	if body.SizeBytes <= 0 || body.SizeBytes > kycMaxBytes {
		f.Add("size_bytes", "must be between 1 byte and 5 MiB")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	// The key is namespaced by user id so one renter's presigned PUT can never
	// address another's document, and carries a fresh uuid so a re-upload does
	// not overwrite the document a landlord may be looking at.
	name, err := randomUUID()
	if err != nil {
		s.serverError(w, r, "kyc.upload.uuid", err)
		return
	}
	objectKey := p.UserIDString() + "/" + name + "." + ext

	url, err := s.deps.Storage.PresignPut(r.Context(), storage.BucketKYC, objectKey, kycUploadTTL)
	if err != nil {
		s.logger.Error("kyc presign put failed", "error", err)
		kycStorageUnavailable(w)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"upload_url": url,
		"object_key": objectKey,
		"headers":    map[string]string{"Content-Type": contentType},
	})
}

// ---------------------------------- POST /me/profile/kyc-upload/complete --

func (s *Server) handleKYCUploadComplete(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	if s.deps.Storage == nil {
		kycStorageUnavailable(w)
		return
	}

	var body struct {
		ObjectKey string `json:"object_key"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	objectKey := f.Required("object_key", body.ObjectKey)
	// The key check is the authorisation: a renter may only complete an upload
	// under their own key space, whatever key they send. The shape is checked
	// too, not only the prefix — a prefix alone would accept
	// `{me}/../{someone else}/x.jpg`, which any path-normalising hop between
	// here and MinIO would resolve into another renter's document.
	if objectKey != "" && !isOwnKYCKey(objectKey, p.UserIDString()) {
		f.Add("object_key", "must be an upload issued to you")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	// A presigned PUT cannot enforce type or size, so they are checked here
	// against what actually landed (SPEC §7).
	info, err := s.deps.Storage.Stat(r.Context(), storage.BucketKYC, objectKey)
	if err != nil {
		httpx.WriteProblemFields(w, http.StatusBadRequest, "upload not found",
			"no uploaded document was found for that key", map[string]string{
				"object_key": "no object has been uploaded under this key",
			})
		return
	}
	reject := func(field, msg string) {
		// The object is unusable; deleting it keeps the bucket free of files
		// nothing references.
		if err := s.deps.Storage.Remove(r.Context(), storage.BucketKYC, objectKey); err != nil {
			s.logger.Warn("could not remove rejected kyc upload", "object_key", objectKey, "error", err)
		}
		httpx.WriteProblemFields(w, http.StatusBadRequest, "invalid document",
			"the uploaded document was rejected", map[string]string{field: msg})
	}
	if info.Size > kycMaxBytes {
		reject("size_bytes", "must be at most 5 MiB")
		return
	}
	if _, ok := kycContentTypes[strings.ToLower(strings.TrimSpace(info.ContentType))]; !ok {
		reject("content_type", "must be image/jpeg or image/png")
		return
	}

	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.SetRenterKycDoc(r.Context(), sqlc.SetRenterKycDocParams{
			UserID: p.UserID, KycDocObjectKey: &objectKey,
		}); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionKYCUpload,
			EntityType:  audit.EntityRenterProfile,
			EntityID:    p.UserIDString(),
			After:       map[string]any{"object_key": objectKey, "size_bytes": info.Size},
		})
	}); err != nil {
		s.serverError(w, r, "kyc.complete.tx", err)
		return
	}

	profile, err := s.loadProfile(r.Context(), p.UserID)
	if err != nil {
		s.serverError(w, r, "kyc.complete.reload", err)
		return
	}
	user, err := s.q.GetUserByID(r.Context(), p.UserID)
	if err != nil {
		s.serverError(w, r, "kyc.complete.user", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"profile": toProfile(profile, user.Email)})
}

// ----------------------------------------------- GET .../kyc-doc (both) --

func (s *Server) handleMyKYCDoc(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	s.writeKYCDocURL(w, r, p.UserID, "")
}

// writeKYCDocURL issues the short-lived read link and records the access.
// Every read of an ID document is audited (SPEC §8).
func (s *Server) writeKYCDocURL(w http.ResponseWriter, r *http.Request, subjectID pgtype.UUID, orgID string) {
	profile, err := s.loadProfile(r.Context(), subjectID)
	if isNoRows(err) || (err == nil && (profile.KycDocObjectKey == nil || *profile.KycDocObjectKey == "")) {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no identity document has been uploaded")
		return
	}
	if err != nil {
		s.serverError(w, r, "kyc.doc.load", err)
		return
	}
	if s.deps.Storage == nil {
		kycStorageUnavailable(w)
		return
	}
	url, err := s.deps.Storage.PresignGet(r.Context(), storage.BucketKYC, *profile.KycDocObjectKey, kycReadTTL)
	if err != nil {
		s.logger.Error("kyc presign get failed", "error", err)
		kycStorageUnavailable(w)
		return
	}

	actor := auth.MustFromContext(r.Context())
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       orgID,
			ActorUserID: actor.UserIDString(),
			Action:      audit.ActionKYCView,
			EntityType:  audit.EntityRenterProfile,
			EntityID:    db.UUIDString(subjectID),
			After:       map[string]any{"object_key": *profile.KycDocObjectKey},
		})
	}); err != nil {
		s.serverError(w, r, "kyc.doc.audit", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"url": url})
}

// kycStorageUnavailable is the 503 the KYC routes answer with MinIO down: the
// document cannot be stored or read, and pretending otherwise would lose it.
func kycStorageUnavailable(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusServiceUnavailable, "storage unavailable",
		"identity documents cannot be uploaded or read right now")
}

// kycKeyPattern is the shape handleKYCUpload issues: one prefix segment, one
// file name, one image extension. Holding the key to two segments is what
// makes the prefix check load-bearing — `{me}/../{someone else}/x.jpg` starts
// with the caller's own id but names another renter's document once any hop
// between here and MinIO normalises the path.
var kycKeyPattern = regexp.MustCompile(`^[^/]+/[A-Za-z0-9][A-Za-z0-9._-]*\.(jpg|jpeg|png)$`)

// isOwnKYCKey reports whether key is a KYC object key issued to this user.
func isOwnKYCKey(key, userID string) bool {
	return kycKeyPattern.MatchString(key) && strings.HasPrefix(key, userID+"/")
}

// randomUUID draws a version-4 UUID for an object key.
func randomUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("uuid: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}
