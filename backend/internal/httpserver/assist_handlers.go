package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	qrcode "github.com/skip2/go-qrcode"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/notify"
	"tms/backend/internal/ratelimit"
	"tms/backend/internal/validate"
)

// Landlord-assisted onboarding (PLAN2 Phase 18, SPEC §5.15, FLOWS 2b).
//
// Beem accepted an OTP and left it `pending`: the renter never got a code and
// had no way forward. Landlord and renter are almost always in the same room
// at onboarding — the QR is on the door — so the landlord's screen becomes the
// channel the code travels on. Nothing is sent, and the renter still acts on
// their own device, so the account, the PIN and the signature stay theirs.
//
// The whole design rests on one decision: the assisted code is written into
// *the same Redis slot* the SMS path writes (`otp:{purpose}:{phone}`, or the
// contract's sign slot). POST /auth/otp/verify, POST /auth/register/renter and
// POST /contracts/{id}/sign are therefore untouched — there is no second
// verification path to keep in step, and no second way to get an account.

const (
	// assistIssueLimit caps codes revealed per org per hour. The limiter is
	// per *org*, not per phone, because the landlord vouches for the number
	// they are standing next to: the per-phone cooldown exists to stop a
	// stranger walking someone's inbox from a public endpoint, which an
	// authenticated org user is not doing. Thirty an hour is a busy morning of
	// move-ins; three hundred is a script.
	assistIssueLimit  = 30
	assistIssueWindow = time.Hour
	// assistMaxCodes caps reveals within one session. A renter who has needed
	// ten codes is not having a typing problem, and the session should be
	// closed and reopened (which the org budget then counts again).
	assistMaxCodes = 10
	// assistSessionTTL is how long a session stays live, pushed out by every
	// new code. Thirty minutes is the length of the encounter it models.
	assistSessionTTL = 30 * time.Minute
	// assistListLimit caps GET /assist. Open sessions are a handful by
	// construction — they expire in half an hour.
	assistListLimit = 100
)

// status_detail values (API.md Phase 18). They are derived from the session's
// stamps and the link request's own status, never stored: a column would be a
// second copy of facts the link request already owns.
const (
	assistWaiting    = "waiting"
	assistRegistered = "registered"
	assistRequested  = "requested"
	assistApproved   = "approved"
	assistClosedName = "closed"
)

const (
	assistStatusOpen   = "open"
	assistStatusClosed = "closed"
)

func notFoundAssist(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such assist session")
}

// assistClosed is the 409 for a code or a refresh on a session that has been
// closed or has run out its thirty minutes. Expiry and closure are one answer:
// from the landlord's screen they are the same event.
func assistClosed(w http.ResponseWriter) {
	conflictCode(w, "assist_closed", "session closed",
		"this assist session has been closed or has expired; start a new one")
}

func cacheUnavailableAssist(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusServiceUnavailable, "verification unavailable",
		"one-time codes cannot be issued right now")
}

// ------------------------------------------------------------------ DTOs --

// assistSession is the `session` object of API.md Phase 18. It carries the
// phone because the landlord typed it and is looking at it; the *public*
// lookup carries neither it nor anything else of the org's.
type assistSession struct {
	ID              string     `json:"id"`
	OrgID           string     `json:"org_id"`
	UnitID          string     `json:"unit_id"`
	UnitCode        string     `json:"unit_code"`
	UnitName        string     `json:"unit_name"`
	Phone           string     `json:"phone"`
	Purpose         string     `json:"purpose"`
	Status          string     `json:"status"`
	CodeIssuedCount int32      `json:"code_issued_count"`
	ExpiresAt       time.Time  `json:"expires_at"`
	LastCodeAt      *time.Time `json:"last_code_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

// assistDetail is GET /assist/{id}: the session plus how far the renter has
// got. It never carries the code — the code exists only in Redis and on the
// screen it was first written to.
type assistDetail struct {
	Session      assistSession  `json:"session"`
	StatusDetail string         `json:"status_detail"`
	Renter       *witnessRef    `json:"renter"`
	LinkRequest  *assistLinkRef `json:"link_request"`
}

type assistLinkRef struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// assistRow is what the two list/get queries have in common, so one mapper
// serves both.
type assistRow struct {
	sqlc.AssistSession
	UnitCode          string
	UnitName          string
	RenterName        string
	LinkRequestStatus string
}

func assistRowOfGet(r sqlc.GetAssistSessionRow) assistRow {
	return assistRow{
		AssistSession: sqlc.AssistSession{
			ID: r.ID, OrgID: r.OrgID, UnitID: r.UnitID, Phone: r.Phone, Purpose: r.Purpose,
			StartedByUserID: r.StartedByUserID, RenterUserID: r.RenterUserID,
			LinkRequestID: r.LinkRequestID, Status: r.Status,
			CodeIssuedCount: r.CodeIssuedCount, LastCodeAt: r.LastCodeAt,
			ExpiresAt: r.ExpiresAt, ClosedAt: r.ClosedAt,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		},
		UnitCode: r.UnitCode, UnitName: r.UnitName,
		RenterName: r.RenterName, LinkRequestStatus: r.LinkRequestStatus,
	}
}

func assistRowOfList(r sqlc.ListOpenAssistSessionsRow) assistRow {
	return assistRowOfGet(sqlc.GetAssistSessionRow(r))
}

// assistExpired reports whether a session has run out its window. It is read
// wherever `status` is, because an expired session is closed in every sense
// that matters and no sweep runs to write the column.
func assistExpired(r assistRow) bool {
	return !r.ExpiresAt.Valid || !r.ExpiresAt.Time.After(time.Now().UTC())
}

func assistLive(r assistRow) bool {
	return r.Status == assistStatusOpen && !assistExpired(r)
}

func toAssistSession(r assistRow) assistSession {
	out := assistSession{
		ID: db.UUIDString(r.ID), OrgID: db.UUIDString(r.OrgID),
		UnitID: db.UUIDString(r.UnitID), UnitCode: r.UnitCode, UnitName: r.UnitName,
		Phone: r.Phone, Purpose: r.Purpose, Status: r.Status,
		CodeIssuedCount: r.CodeIssuedCount,
		ExpiresAt:       r.ExpiresAt.Time, CreatedAt: r.CreatedAt.Time,
	}
	if !assistLive(r) {
		out.Status = assistStatusClosed
	}
	if r.LastCodeAt.Valid {
		t := r.LastCodeAt.Time
		out.LastCodeAt = &t
	}
	return out
}

// assistStatusDetail is the line the landlord's screen reads: waiting →
// registered → requested → approved, or closed.
func assistStatusDetail(r assistRow) string {
	if !assistLive(r) {
		return assistClosedName
	}
	if r.LinkRequestID.Valid {
		if r.LinkRequestStatus == linkApproved {
			return assistApproved
		}
		return assistRequested
	}
	if r.RenterUserID.Valid {
		return assistRegistered
	}
	return assistWaiting
}

func toAssistDetail(r assistRow) assistDetail {
	out := assistDetail{Session: toAssistSession(r), StatusDetail: assistStatusDetail(r)}
	if r.RenterUserID.Valid {
		out.Renter = &witnessRef{ID: db.UUIDString(r.RenterUserID), FullName: r.RenterName}
	}
	if r.LinkRequestID.Valid {
		out.LinkRequest = &assistLinkRef{
			ID: db.UUIDString(r.LinkRequestID), Status: r.LinkRequestStatus,
		}
	}
	return out
}

// assistLink is the URL the QR encodes: the unit's ordinary renter landing
// page, carrying the session id so the enduser app knows to skip "Send code".
func (s *Server) assistLink(unitCode, sessionID string) string {
	return s.cfg.EnduserURL() + "/u/" + unitCode + "?assist=" + sessionID
}

// assistQRSize is the PNG edge in pixels. The code is scanned off a phone
// screen held a hand's width away, not off a printed sticker, so it is half
// the size of the unit QR.
const assistQRSize = 256

// assistLinkQR renders the link as a `data:image/png;base64,…` URL for the
// landlord's screen (Phase 25). It travels inline rather than through the
// `qrcodes` bucket because it lives only as long as the session and is shown
// once: storing it would leave an object behind for every encounter. An
// encoding failure yields "" and the screen falls back to the printed link.
func assistLinkQR(link string) string {
	png, err := qrcode.Encode(link, qrcode.Medium, assistQRSize)
	if err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
}

// ------------------------------------------------------ the shown record --

// recordShownCode writes the notification_log row for one reveal.
//
// The row exists because "a code was revealed for this number" is exactly what
// an operator investigating a disputed account needs to find, and the delivery
// log is where every other code already appears. What it deliberately does not
// carry is the code: `body` is empty by construction (the column is written by
// the query, not by this caller), the channel is `in_person`, the status is
// `shown`, and no credit is debited — the worker's claim requires `queued`, so
// the row can never be picked up and sent.
func (s *Server) recordShownCode(
	ctx context.Context, q *sqlc.Queries, orgID pgtype.UUID, userID pgtype.UUID,
	phone, dedupeKey, lang string,
) error {
	payload, err := json.Marshal(map[string]any{
		"kind": notify.KindOTP, "to": phone, "channel": "in_person", "language": lang,
	})
	if err != nil {
		return err
	}
	_, err = q.InsertShownNotification(ctx, sqlc.InsertShownNotificationParams{
		OrgID: orgID, UserID: userID, Kind: notify.KindOTP,
		DedupeKey: dedupeKey, Payload: payload, ToPhone: phone,
		Language: notify.LanguageFor(lang, ""),
	})
	// A repeated dedupe key writes nothing and returns no row. That is the
	// deduplication working, not a failure of the reveal.
	if isNoRows(err) {
		return nil
	}
	return err
}

// assistDedupeKey names one reveal: the session and its running count.
func assistDedupeKey(sessionID string, n int32) string {
	return "assist:" + sessionID + ":" + strconv.Itoa(int(n))
}

// --------------------------------------------------------- POST /assist --

func (s *Server) handleCreateAssist(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	var body struct {
		Phone  string `json:"phone"`
		UnitID string `json:"unit_id"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	phone := f.Phone("phone", body.Phone)
	unitID := uuidField(f, "unit_id", strings.TrimSpace(body.UnitID), false)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	if res := s.limiter.Allow(r.Context(), "assist:issue:"+p.OrgIDString(),
		assistIssueLimit, assistIssueWindow); !res.Allowed {
		tooMany(w, res, "too many codes shown for this organisation; try again shortly")
		return
	}

	unit, err := s.q.GetUnit(r.Context(), sqlc.GetUnitParams{OrgID: p.OrgID, ID: unitID})
	if isNoRows(err) {
		notFoundUnit(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "assist.unit", err)
		return
	}

	// Which slot the code goes into is the server's call, from the number
	// alone: a phone with no account is registering, a phone with a renter
	// account is logging in, and a phone belonging to staff or the platform
	// admin is refused outright. The last case is the one that matters: the
	// assisted path bypasses the per-phone cooldown, so it must never become a
	// way for a manager to mint a login code for the owner's own account.
	purpose := "register"
	var renterUser pgtype.UUID
	var renterLocale string
	switch existing, uErr := s.q.GetUserByPhone(r.Context(), &phone); {
	case uErr == nil && existing.Kind != auth.KindRenter:
		conflictCode(w, "not_a_renter_phone", "not a renter number",
			"that number belongs to a staff or platform account and cannot be onboarded here")
		return
	case uErr == nil:
		purpose, renterUser, renterLocale = "login", existing.ID, existing.Locale
	case isNoRows(uErr):
	default:
		s.serverError(w, r, "assist.user", uErr)
		return
	}

	// One open session per (org, phone). The partial unique index is the real
	// guard; this read turns the refusal into a useful one by naming the
	// session already running, which is what the landlord wants to reopen.
	if open, oErr := s.q.FindOpenAssistSessionByPhone(r.Context(),
		sqlc.FindOpenAssistSessionByPhoneParams{OrgID: p.OrgID, Phone: phone}); oErr == nil {
		httpx.WriteProblemExtra(w, http.StatusConflict, "assist_open", "session already open",
			"an assist session for this number is already open in this organisation",
			map[string]any{"session_id": db.UUIDString(open.ID)})
		return
	} else if !isNoRows(oErr) {
		s.serverError(w, r, "assist.open.lookup", oErr)
		return
	}

	expiresAt := time.Now().UTC().Add(assistSessionTTL)
	var created sqlc.AssistSession
	err = s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var txErr error
		created, txErr = q.CreateAssistSession(r.Context(), sqlc.CreateAssistSessionParams{
			OrgID: p.OrgID, UnitID: unit.ID, Phone: phone, Purpose: purpose,
			StartedByUserID: p.UserID, ExpiresAt: db.TS(expiresAt),
		})
		if txErr != nil {
			return txErr
		}
		sessionID := db.UUIDString(created.ID)
		if txErr := s.recordShownCode(r.Context(), q, p.OrgID, renterUser, phone,
			assistDedupeKey(sessionID, 1), renterLocale); txErr != nil {
			return txErr
		}
		if txErr := audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionAssistStart,
			EntityType:  audit.EntityAssistSession,
			EntityID:    sessionID,
			After: map[string]any{
				"unit_id": db.UUIDString(unit.ID), "unit_code": unit.UnitCode,
				"phone": phone, "purpose": purpose,
			},
		}); txErr != nil {
			return txErr
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionAssistCode,
			EntityType:  audit.EntityAssistSession,
			EntityID:    sessionID,
			After:       map[string]any{"n": 1, "phone": phone, "purpose": purpose},
		})
	})
	if isUnique(err) {
		// A second request for the same number won the race.
		conflictCode(w, "assist_open", "session already open",
			"an assist session for this number is already open in this organisation")
		return
	}
	if err != nil {
		s.serverError(w, r, "assist.create.tx", err)
		return
	}

	sessionID := db.UUIDString(created.ID)
	code, codeExpiry, ok := s.issueAssistCode(w, r, purpose, phone, sessionID)
	if !ok {
		// The row is durable but no code reached the slot; close it rather
		// than leave a session nobody can use holding the org's one open slot
		// for this number.
		if _, cErr := s.q.CloseAssistSession(r.Context(),
			sqlc.CloseAssistSessionParams{OrgID: p.OrgID, ID: created.ID}); cErr != nil {
			s.logger.Warn("could not close an assist session whose code failed", "error", cErr)
		}
		return
	}

	row := assistRow{AssistSession: created, UnitCode: unit.UnitCode, UnitName: unit.Name}
	link := s.assistLink(unit.UnitCode, sessionID)
	WriteJSON(w, http.StatusCreated, map[string]any{
		"session":         toAssistSession(row),
		"code":            code,
		"code_expires_at": codeExpiry,
		"link":            link,
		"link_qr":         assistLinkQR(link),
	})
}

// issueAssistCode writes a fresh code into the SMS path's own slot and marks
// the slot as assisted, so the register/login handlers can stamp the session
// the renter came from. It answers 503 and reports false when Redis is down:
// a code nobody can verify is worse than no code.
func (s *Server) issueAssistCode(
	w http.ResponseWriter, r *http.Request, purpose, phone, sessionID string,
) (string, time.Time, bool) {
	code := auth.GenerateOTP()
	if err := s.store.PutOTPAssisted(r.Context(), purpose, phone, code); err != nil {
		cacheUnavailableAssist(w)
		return "", time.Time{}, false
	}
	if err := s.store.SetAssistMarker(r.Context(), purpose, phone, sessionID); err != nil {
		// The code works; only the stamp would be lost. That is a cosmetic
		// loss on a flow the renter is already walking, so it is logged and
		// the reveal goes ahead.
		s.logger.Warn("assist marker not written", "session_id", sessionID)
	}
	return code, time.Now().UTC().Add(auth.OTPTTL), true
}

// ---------------------------------------------------------- GET /assist --

func (s *Server) handleListAssist(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	rows, err := s.q.ListOpenAssistSessions(r.Context(), sqlc.ListOpenAssistSessionsParams{
		OrgID: p.OrgID, RowLimit: assistListLimit,
	})
	if err != nil {
		s.serverError(w, r, "assist.list", err)
		return
	}
	items := make([]assistDetail, 0, len(rows))
	for _, row := range rows {
		items = append(items, toAssistDetail(assistRowOfList(row)))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// loadAssist reads one session, scoped to the caller's org. Another org's
// session is a 404, exactly as another org's unit is.
func (s *Server) loadAssist(w http.ResponseWriter, r *http.Request) (assistRow, bool) {
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundAssist(w)
		return assistRow{}, false
	}
	row, err := s.q.GetAssistSession(r.Context(), sqlc.GetAssistSessionParams{
		OrgID: p.OrgID, ID: id,
	})
	if isNoRows(err) {
		notFoundAssist(w)
		return assistRow{}, false
	}
	if err != nil {
		s.serverError(w, r, "assist.get", err)
		return assistRow{}, false
	}
	return assistRowOfGet(row), true
}

func (s *Server) handleGetAssist(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	row, ok := s.loadAssist(w, r)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, toAssistDetail(row))
}

// -------------------------------------------------- POST /assist/{id}/code --

func (s *Server) handleAssistCode(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.loadAssist(w, r)
	if !ok {
		return
	}
	if !assistLive(row) {
		assistClosed(w)
		return
	}
	if row.CodeIssuedCount >= assistMaxCodes {
		tooMany(w, ratelimit.Result{},
			"this session has shown its ten codes; close it and start a new one")
		return
	}
	if res := s.limiter.Allow(r.Context(), "assist:issue:"+p.OrgIDString(),
		assistIssueLimit, assistIssueWindow); !res.Allowed {
		tooMany(w, res, "too many codes shown for this organisation; try again shortly")
		return
	}

	sessionID := db.UUIDString(row.ID)
	var issued sqlc.AssistSession
	err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var txErr error
		// The UPDATE re-checks `open` and the expiry, so the per-session cap
		// cannot be raced past by two tabs pressing "New code" together.
		issued, txErr = q.IssueAssistCode(r.Context(), sqlc.IssueAssistCodeParams{
			OrgID: p.OrgID, ID: row.ID,
			ExpiresAt: db.TS(time.Now().UTC().Add(assistSessionTTL)),
		})
		if txErr != nil {
			return txErr
		}
		if txErr := s.recordShownCode(r.Context(), q, p.OrgID, issued.RenterUserID, issued.Phone,
			assistDedupeKey(sessionID, issued.CodeIssuedCount), ""); txErr != nil {
			return txErr
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionAssistCode,
			EntityType:  audit.EntityAssistSession,
			EntityID:    sessionID,
			After: map[string]any{
				"n": issued.CodeIssuedCount, "phone": issued.Phone, "purpose": issued.Purpose,
			},
		})
	})
	if isNoRows(err) {
		assistClosed(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "assist.code.tx", err)
		return
	}

	code, codeExpiry, ok := s.issueAssistCode(w, r, issued.Purpose, issued.Phone, sessionID)
	if !ok {
		return
	}
	// Phase 25: the link and its QR ride along, so a landlord who reopens a
	// session (from the list, or after a 409 `assist_open`) gets everything
	// the first screen showed from the one call that reveals a code — the
	// session read never carries either.
	link := s.assistLink(row.UnitCode, sessionID)
	WriteJSON(w, http.StatusOK, map[string]any{
		"code":              code,
		"code_expires_at":   codeExpiry,
		"code_issued_count": issued.CodeIssuedCount,
		"expires_at":        issued.ExpiresAt.Time,
		"link":              link,
		"link_qr":           assistLinkQR(link),
	})
}

// ------------------------------------------------- POST /assist/{id}/close --

func (s *Server) handleCloseAssist(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.loadAssist(w, r)
	if !ok {
		return
	}
	// Idempotent: closing a closed session is a 200, not a 409. The landlord
	// pressing Done twice is not an error, and the audit row records the
	// press either way.
	var closed sqlc.AssistSession
	err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var txErr error
		closed, txErr = q.CloseAssistSession(r.Context(), sqlc.CloseAssistSessionParams{
			OrgID: p.OrgID, ID: row.ID,
		})
		if txErr != nil {
			return txErr
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionAssistClose,
			EntityType:  audit.EntityAssistSession,
			EntityID:    db.UUIDString(row.ID),
			After:       map[string]any{"codes_shown": row.CodeIssuedCount},
		})
	})
	if isNoRows(err) {
		notFoundAssist(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "assist.close.tx", err)
		return
	}
	out := row
	out.AssistSession = closed
	WriteJSON(w, http.StatusOK, map[string]any{"session": toAssistSession(out)})
}

// --------------------------------------- POST /contracts/{id}/witness-otp --

// handleContractWitnessOTP is FLOWS 2b.6: the landlord shows the signing code
// instead of texting it. The renter still signs on their own device through
// the unchanged POST /contracts/{id}/sign; the only difference the signature
// carries is `witnessed_by_user_id`, set from the marker this writes.
//
// The landlord cannot sign for the renter here. That remains the deliberate,
// separately audited `landlord_recorded` activation (FLOWS 3.6).
func (s *Server) handleContractWitnessOTP(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.loadActionableContract(w, r)
	if !ok {
		return
	}
	if row.Status != contractPendingSignature {
		conflictCode(w, "not_signable", "contract not awaiting signature",
			"only a contract awaiting signature can be signed")
		return
	}
	signed, err := s.q.CountContractSignatures(r.Context(), sqlc.CountContractSignaturesParams{
		OrgID: row.OrgID, ContractID: row.ID, Party: partyRenter,
	})
	if err != nil {
		s.serverError(w, r, "assist.witness.count", err)
		return
	}
	if signed > 0 {
		conflictCode(w, "already_signed", "already signed",
			"this contract has already been signed by the renter")
		return
	}
	phone := db.StrVal(row.RenterPhone)
	if phone == "" {
		httpx.WriteProblem(w, http.StatusPreconditionFailed, "no phone number",
			"this account has no phone number to key a signing code to")
		return
	}

	// A witnessed signing code is a revealed code like any other, so it is
	// counted against the same org budget.
	if res := s.limiter.Allow(r.Context(), "assist:issue:"+p.OrgIDString(),
		assistIssueLimit, assistIssueWindow); !res.Allowed {
		tooMany(w, res, "too many codes shown for this organisation; try again shortly")
		return
	}

	contractID := db.UUIDString(row.ID)
	code := auth.GenerateOTP()
	if err := s.store.PutOTPAssisted(r.Context(), signPurpose(contractID), phone, code); err != nil {
		cacheUnavailableAssist(w)
		return
	}
	if err := s.store.SetWitnessMarker(r.Context(), contractID, p.UserIDString()); err != nil {
		s.logger.Warn("witness marker not written", "contract_id", contractID)
	}

	nonce, err := assistNonce()
	if err != nil {
		s.serverError(w, r, "assist.witness.nonce", err)
		return
	}
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if txErr := s.recordShownCode(r.Context(), q, row.OrgID, row.RenterUserID, phone,
			"assist:witness:"+contractID+":"+nonce, ""); txErr != nil {
			return txErr
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       db.UUIDString(row.OrgID),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionContractWitnessOTP,
			EntityType:  audit.EntityContract,
			EntityID:    contractID,
			After:       map[string]any{"purpose": "sign", "channel": "in_person"},
		})
	}); err != nil {
		s.serverError(w, r, "assist.witness.tx", err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"code":            code,
		"code_expires_at": time.Now().UTC().Add(auth.OTPTTL),
	})
}

// assistNonce names one witnessed reveal in the delivery log. Unlike an assist
// session's reveals there is no running count to key on — a contract may be
// witnessed, left, and witnessed again — so the row is keyed by a random
// suffix, which is all `dedupe_key` needs to be here: there is nothing to
// deduplicate, only a unique name to give.
func assistNonce() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// ---------------------------------------------- GET /public/assist/{id} --

// handlePublicAssist is what the renter's own device asks after scanning the
// QR: it holds a session id and nothing else, and needs to know which unit it
// is looking at and whether to show "register" or "sign in".
//
// The projection is deliberately tiny — unit code, purpose, status. Never the
// phone (the session id would otherwise be a lookup from a sticker to somebody
// else's number), never the org's rows.
func (s *Server) handlePublicAssist(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundAssist(w)
		return
	}
	row, err := s.q.GetPublicAssistSession(r.Context(), id)
	if isNoRows(err) {
		notFoundAssist(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "assist.public", err)
		return
	}
	status := assistStatusOpen
	if row.Status != assistStatusOpen || !row.ExpiresAt.Time.After(time.Now().UTC()) {
		status = assistStatusClosed
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"unit_code": row.UnitCode, "purpose": row.Purpose, "status": status,
	})
}

// ------------------------------------------------------------- the hooks --

// stampAssistRenter attaches a renter account to the session the landlord
// opened for their number, if there is one.
//
// It is called from POST /auth/otp/verify and POST /auth/register/renter,
// neither of which has an org context: the authorisation is the marker itself,
// which only an authenticated org user's reveal could have written, and which
// expires with the code.
//
// Failure is logged and swallowed. The renter has an account either way; the
// stamp is what the landlord's screen reads, and losing it must not turn a
// successful registration into an error.
func (s *Server) stampAssistRenter(ctx context.Context, sessionID string, userID pgtype.UUID) {
	if sessionID == "" || !userID.Valid || s.q == nil {
		return
	}
	id, err := db.ParseUUID(sessionID)
	if err != nil {
		return
	}
	if err := s.q.StampAssistRenter(ctx, sqlc.StampAssistRenterParams{
		ID: id, RenterUserID: userID,
	}); err != nil {
		s.logger.Warn("could not stamp assist session with the renter", "error", err)
	}
}

// stampAssistLinkRequest moves the landlord's screen from "Registered" to
// "Request received". It is keyed by org, unit and renter — the three facts
// the request itself carries — rather than by a marker, because the renter's
// device may well have been reloaded between verifying the code and applying.
func (s *Server) stampAssistLinkRequest(
	ctx context.Context, orgID, unitID, renterID pgtype.UUID, phone, requestID string,
) {
	if s.q == nil || !orgID.Valid || !unitID.Valid {
		return
	}
	open, err := s.q.FindOpenAssistSessionForLink(ctx, sqlc.FindOpenAssistSessionForLinkParams{
		OrgID: orgID, UnitID: unitID, RenterUserID: renterID, Phone: phone,
	})
	if err != nil {
		if !isNoRows(err) {
			s.logger.Warn("could not look up an assist session for the link request", "error", err)
		}
		return
	}
	id, err := db.ParseUUID(requestID)
	if err != nil {
		return
	}
	if err := s.q.StampAssistLinkRequest(ctx, sqlc.StampAssistLinkRequestParams{
		OrgID: orgID, ID: open.ID, LinkRequestID: id, RenterUserID: renterID,
	}); err != nil {
		s.logger.Warn("could not stamp assist session with the link request", "error", err)
	}
}

// assistSessionForAudit is the session id an auth handler records in its audit
// `after`, so "this account was created with a code a landlord showed" is
// answerable from the trail alone. An empty string means the ordinary SMS path.
func (s *Server) assistSessionForAudit(ctx context.Context, purpose, phone string) string {
	return s.store.PeekAssistMarker(ctx, purpose, phone)
}
