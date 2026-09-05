package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/contract"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/notify"
	"tms/backend/internal/payment"
	"tms/backend/internal/ratelimit"
	"tms/backend/internal/storage"
	"tms/backend/internal/validate"
)

// Signing limits (API.md). The sign OTP is keyed to the contract, not only to
// the phone: a renter with two contracts to sign must be able to ask for a code
// for each without one cancelling the other.
const (
	signOTPLimit    = 3
	signOTPWindow   = 10 * time.Minute
	signatureTTL    = 10 * time.Minute
	signatureGetTTL = time.Hour
)

// Creation failures that map to a specific HTTP answer. They are sentinels
// rather than strings because the contract is created from two places (the
// endpoint and the link-approval hook) and both must answer the same way.
var (
	errUnitNotFound     = errors.New("contract: unit not found")
	errUnitOccupied     = errors.New("contract: unit occupied")
	errUnitUnavailable  = errors.New("contract: unit not available")
	errUnitNotPriced    = errors.New("contract: unit has no current price")
	errContractExists   = errors.New("contract: unit already has a live contract")
	errRenterUnknown    = errors.New("contract: renter unknown to this org")
	errPeriodNotOffered = errors.New("contract: payment period not offered")
	errTemplateNotFound = errors.New("contract: no template")
)

func notFoundContract(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such contract")
}

// writeCreateError turns a creation sentinel into its API.md answer. It reports
// whether it handled the error, so callers can fall through to a 500.
func writeCreateError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, errUnitNotFound), errors.Is(err, errRenterUnknown):
		httpx.WriteProblem(w, http.StatusNotFound, "not found",
			"no such unit or renter in this organisation")
	case errors.Is(err, errUnitOccupied):
		conflictCode(w, "unit_occupied", "unit occupied", "this unit is already let")
	case errors.Is(err, errUnitUnavailable):
		conflictCode(w, "unit_unavailable", "unit unavailable",
			"this unit is not listed for letting")
	case errors.Is(err, errUnitNotPriced):
		conflictCode(w, "unit_not_priced", "unit has no price",
			"set a price on the unit before writing a contract for it")
	case errors.Is(err, errContractExists):
		conflictCode(w, "contract_exists", "contract already exists",
			"this unit already has a contract awaiting signature or running")
	case errors.Is(err, errPeriodNotOffered):
		conflictCode(w, "period_not_offered", "payment period not offered",
			"that payment period is not offered for this unit")
	case errors.Is(err, errTemplateNotFound):
		conflictCode(w, "template_not_found", "no contract template",
			"this organisation has no contract template to render from")
	default:
		return false
	}
	return true
}

// ------------------------------------------------------------- creation --

// contractInput is everything a contract needs that the caller decides. The
// commercial facts (rent, cadence length, property) are resolved from the unit
// and the period inside the transaction, so the client cannot supply them.
type contractInput struct {
	OrgID           pgtype.UUID
	UnitID          pgtype.UUID
	RenterUserID    pgtype.UUID
	TemplateID      pgtype.UUID
	PaymentPeriodID pgtype.UUID
	TermDays        int32
	StartDate       time.Time
	DueDay          *int32
	LinkRequestID   pgtype.UUID
	ActorUserID     string
}

// createContractTx writes one contract and its audit row through the supplied
// transaction handle, and returns the id of the queued `contract_ready` SMS.
//
// Everything the document says is resolved and frozen here: the terms are
// rendered from the template, the rent and cadence are copied off the unit's
// current price and the chosen period, and `snapshot_hash` is computed over the
// result (SPEC §5.5 — nothing about the document may change afterwards).
func (s *Server) createContractTx(
	ctx context.Context, q *sqlc.Queries, in contractInput,
) (sqlc.Contract, string, error) {
	var zero sqlc.Contract

	unit, err := q.GetUnit(ctx, sqlc.GetUnitParams{OrgID: in.OrgID, ID: in.UnitID})
	if isNoRows(err) {
		return zero, "", errUnitNotFound
	}
	if err != nil {
		return zero, "", err
	}
	switch unit.Status {
	case statusOccupied:
		return zero, "", errUnitOccupied
	case statusUnlisted, statusMaintenance:
		return zero, "", errUnitUnavailable
	}
	if !unit.PriceID.Valid || unit.PricePeriodDays <= 0 {
		return zero, "", errUnitNotPriced
	}

	live, err := q.CountLiveContractsForUnit(ctx, sqlc.CountLiveContractsForUnitParams{
		OrgID: in.OrgID, UnitID: in.UnitID,
	})
	if err != nil {
		return zero, "", err
	}
	if live > 0 {
		return zero, "", errContractExists
	}

	known, err := q.RenterKnownToOrg(ctx, sqlc.RenterKnownToOrgParams{
		OrgID: in.OrgID, RenterUserID: in.RenterUserID,
	})
	if err != nil && !isNoRows(err) {
		return zero, "", err
	}
	if isNoRows(err) || !known {
		return zero, "", errRenterUnknown
	}
	renter, err := q.GetUserByID(ctx, in.RenterUserID)
	if isNoRows(err) {
		return zero, "", errRenterUnknown
	}
	if err != nil {
		return zero, "", err
	}

	period, err := q.GetPaymentPeriod(ctx, sqlc.GetPaymentPeriodParams{
		OrgID: in.OrgID, ID: in.PaymentPeriodID,
	})
	if isNoRows(err) || (err == nil && !period.Active) {
		return zero, "", errPeriodNotOffered
	}
	if err != nil {
		return zero, "", err
	}
	if allowed := allowedSet(unit.AllowedPeriodIds); allowed != nil && !allowed[db.UUIDString(period.ID)] {
		return zero, "", errPeriodNotOffered
	}

	var tpl sqlc.ContractTemplate
	if in.TemplateID.Valid {
		tpl, err = q.GetContractTemplate(ctx, sqlc.GetContractTemplateParams{OrgID: in.OrgID, ID: in.TemplateID})
	} else {
		tpl, err = q.GetDefaultContractTemplate(ctx, in.OrgID)
	}
	if isNoRows(err) {
		return zero, "", errTemplateNotFound
	}
	if err != nil {
		return zero, "", err
	}

	org, err := q.GetOrg(ctx, in.OrgID)
	if err != nil {
		return zero, "", err
	}
	settings := parseSettings(org.Settings)
	dueDay := in.DueDay
	if dueDay == nil && settings.DueDay != nil {
		v := int32(*settings.DueDay)
		dueDay = &v
	}
	displayName := org.Name
	if brand, bErr := q.GetOrgBranding(ctx, in.OrgID); bErr == nil && brand.DisplayName != "" {
		displayName = brand.DisplayName
	}

	start := in.StartDate.UTC().Truncate(24 * time.Hour)
	end := contract.EndDate(start, int(in.TermDays))

	// Sanitizing again on render keeps a body stored before a policy change
	// from escaping the current allowlist.
	terms := contract.Render(contract.SanitizeHTML(tpl.BodyHtml), map[string]string{
		"renter_name":    renter.FullName,
		"unit":           unit.Name,
		"property":       unit.PropertyName,
		"rent":           formatTZS(unit.PriceAmount),
		"start_date":     start.Format(dateLayout),
		"end_date":       end.Format(dateLayout),
		"payment_period": fmt.Sprintf("%s (%d days)", period.Label, period.Days),
		"org_name":       displayName,
		"term_days":      strconv.Itoa(int(in.TermDays)),
		"due_day":        contract.DueDayPhrase(intPtr(dueDay)),
	})

	hash := contract.Snapshot{
		TermsHTML:         terms,
		UnitID:            db.UUIDString(unit.ID),
		RenterUserID:      db.UUIDString(renter.ID),
		RentAmount:        unit.PriceAmount,
		RentPeriodDays:    int(unit.PricePeriodDays),
		PaymentPeriodDays: int(period.Days),
		TermDays:          int(in.TermDays),
		StartDate:         start.Format(dateLayout),
		EndDate:           end.Format(dateLayout),
		DueDay:            intPtr(dueDay),
	}.Hash()

	created, err := q.CreateContract(ctx, sqlc.CreateContractParams{
		OrgID: in.OrgID, UnitID: unit.ID, RenterUserID: renter.ID,
		TemplateID: tpl.ID, TermsSnapshotHtml: terms,
		RentAmount: unit.PriceAmount, RentPeriodDays: unit.PricePeriodDays,
		PaymentPeriodID: period.ID, PaymentPeriodDays: period.Days,
		TermDays:  in.TermDays,
		StartDate: pgtype.Date{Time: start, Valid: true},
		EndDate:   pgtype.Date{Time: end, Valid: true},
		DueDay:    dueDay, Status: contractPendingSignature,
		SnapshotHash: &hash, LinkRequestID: in.LinkRequestID,
	})
	if err != nil {
		// The partial unique index on (unit_id) over the live statuses is the
		// real guard; a race that gets past the count lands here.
		if isUnique(err) {
			return zero, "", errContractExists
		}
		return zero, "", err
	}

	if err := audit.Record(ctx, q, audit.Entry{
		OrgID:       db.UUIDString(in.OrgID),
		ActorUserID: in.ActorUserID,
		Action:      audit.ActionContractCreate,
		EntityType:  audit.EntityContract,
		EntityID:    db.UUIDString(created.ID),
		After: map[string]any{
			"unit_id": db.UUIDString(unit.ID), "renter_user_id": db.UUIDString(renter.ID),
			"template_id": db.UUIDString(tpl.ID), "rent_amount": unit.PriceAmount,
			"rent_period_days": unit.PricePeriodDays, "payment_period_days": period.Days,
			"term_days": in.TermDays, "start_date": start.Format(dateLayout),
			"end_date": end.Format(dateLayout), "due_day": dueDayString(dueDay),
			"status": contractPendingSignature, "snapshot_hash": hash,
		},
	}); err != nil {
		return zero, "", err
	}

	notifyID, err := s.queueContractSMS(ctx, q, contractMessage{
		OrgID: db.UUIDString(in.OrgID), UserID: db.UUIDString(renter.ID),
		ContractID: db.UUIDString(created.ID), Kind: notify.KindContractReady,
		Lang: settings.SMSLanguage, Phone: db.StrVal(renter.Phone),
		Overrides: settings.notifyOverrides(),
		Vars: notify.Vars{
			Name: renter.FullName, Unit: unit.Name, Org: displayName,
			Link: s.cfg.AppBaseURL + "/enduser/contract/" + db.UUIDString(created.ID),
		},
	})
	return created, notifyID, err
}

// ------------------------------------------------------- POST /contracts --

func (s *Server) handleCreateContract(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		UnitID          string `json:"unit_id"`
		RenterUserID    string `json:"renter_user_id"`
		TemplateID      string `json:"template_id"`
		PaymentPeriodID string `json:"payment_period_id"`
		TermDays        int32  `json:"term_days"`
		StartDate       string `json:"start_date"`
		DueDay          *int32 `json:"due_day"`
		LinkRequestID   string `json:"link_request_id"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}

	f := validate.Fields{}
	in := contractInput{OrgID: p.OrgID, TermDays: body.TermDays, DueDay: body.DueDay, ActorUserID: p.UserIDString()}
	in.UnitID = uuidField(f, "unit_id", body.UnitID, true)
	in.RenterUserID = uuidField(f, "renter_user_id", body.RenterUserID, true)
	in.PaymentPeriodID = uuidField(f, "payment_period_id", body.PaymentPeriodID, true)
	in.TemplateID = uuidField(f, "template_id", body.TemplateID, false)
	in.LinkRequestID = uuidField(f, "link_request_id", body.LinkRequestID, false)
	if body.TermDays <= 0 || body.TermDays > termDaysMax {
		f.Add("term_days", "must be a whole number of days between 1 and 3650")
	}
	in.StartDate = requiredDate(f, "start_date", body.StartDate)
	if body.DueDay != nil && (*body.DueDay < dueDayMin || *body.DueDay > dueDayMax) {
		f.Add("due_day", "must be a day of the month between 1 and 31")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	var created sqlc.Contract
	var notifyID string
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		created, notifyID, err = s.createContractTx(r.Context(), q, in)
		return err
	}); err != nil {
		if writeCreateError(w, err) {
			return
		}
		s.serverError(w, r, "contract.create.tx", err)
		return
	}
	s.enqueueNotifications(r.Context(), notifyID)

	out, ok := s.reloadContract(w, r, created.ID, p.OrgID, pgtype.UUID{})
	if !ok {
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"contract": out})
}

// --------------------------------------------------- reading contracts --

// reloadContract re-reads a contract with its signatures for the response body.
func (s *Server) reloadContract(
	w http.ResponseWriter, r *http.Request, id, orgID, renterID pgtype.UUID,
) (contractResponse, bool) {
	row, err := s.q.GetContract(r.Context(), sqlc.GetContractParams{
		ID: id, OrgID: orgID, RenterUserID: renterID,
	})
	if isNoRows(err) {
		notFoundContract(w)
		return contractResponse{}, false
	}
	if err != nil {
		s.serverError(w, r, "contract.reload", err)
		return contractResponse{}, false
	}
	sigs, err := s.q.ListContractSignatures(r.Context(), sqlc.ListContractSignaturesParams{
		OrgID: row.OrgID, ContractID: row.ID,
	})
	if err != nil {
		s.serverError(w, r, "contract.reload.signatures", err)
		return contractResponse{}, false
	}
	return toContract(row, toSignatures(sigs)), true
}

// loadContract resolves the {id} route parameter for whichever audience is
// calling. A renter may read only their own contract; an org user may read any
// contract of their org. Anything else is a 404, never a 403 (API.md).
func (s *Server) loadContract(w http.ResponseWriter, r *http.Request) (sqlc.GetContractRow, bool) {
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundContract(w)
		return sqlc.GetContractRow{}, false
	}
	params := sqlc.GetContractParams{ID: id}
	if p.Kind == auth.KindRenter {
		params.RenterUserID = p.UserID
	} else {
		params.OrgID = p.OrgID
	}
	row, err := s.q.GetContract(r.Context(), params)
	if isNoRows(err) {
		notFoundContract(w)
		return sqlc.GetContractRow{}, false
	}
	if err != nil {
		s.serverError(w, r, "contract.load", err)
		return sqlc.GetContractRow{}, false
	}
	return row, true
}

func (s *Server) handleListContracts(w http.ResponseWriter, r *http.Request) {
	s.listContracts(w, r, false)
}

func (s *Server) handleListMyContracts(w http.ResponseWriter, r *http.Request) {
	s.listContracts(w, r, true)
}

func (s *Server) listContracts(w http.ResponseWriter, r *http.Request, renterScope bool) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	f := validate.Fields{}
	page := parseListPage(r, f)
	qs := r.URL.Query()

	params := sqlc.ListContractsParams{
		CursorAt: page.CursorAt, CursorID: page.CursorID, RowLimit: page.Limit,
	}
	if renterScope {
		params.RenterUserID = p.UserID
	} else {
		params.OrgID = p.OrgID
		if v := strings.TrimSpace(qs.Get("renter_user_id")); v != "" {
			params.RenterUserID = uuidField(f, "renter_user_id", v, true)
		}
	}
	if v := strings.TrimSpace(qs.Get("status")); v != "" {
		status := f.OneOf("status", v, contractDraft, contractPendingSignature,
			contractActive, contractExpiring, contractEnded, contractTerminated)
		params.Status = &status
	}
	if v := strings.TrimSpace(qs.Get("unit_id")); v != "" {
		params.UnitID = uuidField(f, "unit_id", v, true)
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	rows, err := s.q.ListContracts(r.Context(), params)
	if err != nil {
		s.serverError(w, r, "contract.list", err)
		return
	}
	items := make([]contractResponse, 0, len(rows))
	for _, row := range rows {
		c := contractRowOfList(row)
		sigs, err := s.q.ListContractSignatures(r.Context(), sqlc.ListContractSignaturesParams{
			OrgID: c.OrgID, ContractID: c.ID,
		})
		if err != nil {
			s.serverError(w, r, "contract.list.signatures", err)
			return
		}
		items = append(items, toContract(c, toSignatures(sigs)))
	}
	var cursor *string
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		cursor = nextCursor(len(rows), page.Limit, last.CreatedAt.Time, db.UUIDString(last.ID))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": cursor})
}

func (s *Server) handleGetContract(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	row, ok := s.loadContract(w, r)
	if !ok {
		return
	}
	sigs, err := s.q.ListContractSignatures(r.Context(), sqlc.ListContractSignaturesParams{
		OrgID: row.OrgID, ContractID: row.ID,
	})
	if err != nil {
		s.serverError(w, r, "contract.get.signatures", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"contract": toContract(row, toSignatures(sigs))})
}

// -------------------------------------------- GET /contracts/{id}/document --

func (s *Server) handleContractDocument(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	row, ok := s.loadContract(w, r)
	if !ok {
		return
	}
	brand := s.brandingAssets(r.Context(), row.OrgID, row.OrgName)

	sigRows, err := s.q.ListContractSignatures(r.Context(), sqlc.ListContractSignaturesParams{
		OrgID: row.OrgID, ContractID: row.ID,
	})
	if err != nil {
		s.serverError(w, r, "contract.document.signatures", err)
		return
	}
	signatures := make([]map[string]any, 0, len(sigRows))
	for _, sg := range sigRows {
		item := map[string]any{
			"party": sg.Party, "name": sg.SignerName,
			"signed_at": sg.SignedAt.Time, "method": sg.Method,
			"phone_masked": maskPhone(db.StrVal(sg.SignerPhone)), "signature_image_url": nil,
		}
		if sg.SignatureObjectKey != nil && *sg.SignatureObjectKey != "" && s.deps.Storage != nil {
			if url, err := s.deps.Storage.PresignGet(
				r.Context(), storage.BucketSignatures, *sg.SignatureObjectKey, signatureGetTTL,
			); err == nil {
				item["signature_image_url"] = url
			}
		}
		signatures = append(signatures, item)
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"contract_id": db.UUIDString(row.ID),
		"status":      row.Status,
		"org": map[string]any{
			"display_name": brand.DisplayName, "logo_url": brand.LogoURL,
			"letterhead_url": brand.LetterheadURL, "footer_text": brand.FooterText,
			"theme": brand.Theme,
		},
		"parties": map[string]any{
			"landlord": map[string]any{"name": brand.DisplayName},
			"renter": map[string]any{
				"name": row.RenterName, "phone_masked": maskPhone(db.StrVal(row.RenterPhone)),
			},
		},
		"terms_html":    row.TermsSnapshotHtml,
		"schedule":      s.documentSchedule(r, row),
		"signatures":    signatures,
		"snapshot_hash": db.StrVal(row.SnapshotHash),
		"generated_at":  time.Now().UTC(),
	})
}

// documentSchedule shows the materialised rows once a contract is active, and
// the same generator's preview before that — the document must show the money
// the renter is agreeing to before there is anything in the table.
func (s *Server) documentSchedule(r *http.Request, row sqlc.GetContractRow) []map[string]any {
	stored, err := s.q.ListSchedulesForContract(r.Context(), sqlc.ListSchedulesForContractParams{
		OrgID: row.OrgID, ContractID: row.ID,
	})
	if err == nil && len(stored) > 0 {
		out := make([]map[string]any, 0, len(stored))
		for _, sc := range stored {
			out = append(out, map[string]any{
				"period_start": sc.PeriodStart.Time.Format(dateLayout),
				"period_end":   sc.PeriodEnd.Time.Format(dateLayout),
				"due_date":     sc.DueDate.Time.Format(dateLayout),
				"amount":       sc.Amount,
			})
		}
		return out
	}
	rows := contract.Generate(int(row.RentAmount), int(row.RentPeriodDays),
		int(row.TermDays), int(row.PaymentPeriodDays), row.StartDate.Time, intPtr(row.DueDay))
	out := make([]map[string]any, 0, len(rows))
	for _, gen := range rows {
		out = append(out, map[string]any{
			"period_start": gen.PeriodStart.Format(dateLayout),
			"period_end":   gen.PeriodEnd.Format(dateLayout),
			"due_date":     gen.DueDate.Format(dateLayout),
			"amount":       gen.Amount,
		})
	}
	return out
}

// ------------------------------------------- POST /contracts/{id}/sign/otp --

func (s *Server) handleContractSignOTP(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.loadContract(w, r)
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
		s.serverError(w, r, "contract.sign.otp.count", err)
		return
	}
	if signed > 0 {
		conflictCode(w, "already_signed", "already signed", "you have already signed this contract")
		return
	}
	phone := db.StrVal(row.RenterPhone)
	if phone == "" {
		httpx.WriteProblem(w, http.StatusPreconditionFailed, "no phone number",
			"this account has no phone number to send a code to")
		return
	}

	// Keyed by contract as well as phone: two contracts awaiting signature must
	// not share (or cancel) one another's code.
	purpose := signPurpose(db.UUIDString(row.ID))
	if res := s.limiter.Allow(r.Context(), "otp:sign:"+db.UUIDString(row.ID), signOTPLimit, signOTPWindow); !res.Allowed {
		tooMany(w, res, "too many signing codes requested for this contract")
		return
	}

	code := auth.GenerateOTP()
	switch err := s.store.PutOTP(r.Context(), purpose, phone, code); {
	case errors.Is(err, auth.ErrCooldown):
		tooMany(w, ratelimit.Result{RetryAfter: auth.OTPResendCooldown},
			"a code was already sent; wait before requesting another")
		return
	case errors.Is(err, auth.ErrCacheUnavailable):
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "verification unavailable",
			"one-time codes cannot be issued right now")
		return
	case err != nil:
		s.serverError(w, r, "contract.sign.otp.put", err)
		return
	}

	body := fmt.Sprintf("TMS: your code to sign the contract for %s is %s. It expires in 5 minutes.",
		row.UnitName, code)
	// The signing code names the landlord's own sender ID where they have one:
	// the renter is being asked to sign that org's contract.
	sender := ""
	if org, err := s.q.GetOrg(r.Context(), row.OrgID); err == nil {
		sender = parseSettings(org.Settings).senderNameOf()
	}
	if _, err := s.deps.SMS.Send(r.Context(), phone, body, sender); err != nil {
		s.logger.Error("contract sign otp send failed", "error", err)
	}
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       db.UUIDString(row.OrgID),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionOTPSend,
			EntityType:  audit.EntityContract,
			EntityID:    db.UUIDString(row.ID),
			After:       map[string]any{"purpose": "sign", "contract_id": db.UUIDString(row.ID)},
		})
	}); err != nil {
		s.serverError(w, r, "contract.sign.otp.audit", err)
		return
	}
	WriteJSON(w, http.StatusAccepted, map[string]any{"resend_after_seconds": otpResendAfterSeconds})
}

// ------------------------------------ POST /contracts/{id}/signature-upload --

func (s *Server) handleSignatureUpload(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	row, ok := s.loadContract(w, r)
	if !ok {
		return
	}
	if s.deps.Storage == nil {
		signatureStorageUnavailable(w)
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
	if contentType != signatureContentType {
		f.Add("content_type", "must be image/png")
	}
	if body.SizeBytes <= 0 || body.SizeBytes > signatureMaxBytes {
		f.Add("size_bytes", "must be between 1 byte and 512 KiB")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	objectKey := signatureKey(db.UUIDString(row.OrgID), db.UUIDString(row.ID))
	url, err := s.deps.Storage.PresignPut(r.Context(), storage.BucketSignatures, objectKey, signatureTTL)
	if err != nil {
		s.logger.Error("signature presign put failed", "error", err)
		signatureStorageUnavailable(w)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"upload_url": url, "object_key": objectKey,
		"headers": map[string]string{"Content-Type": signatureContentType},
	})
}

// ----------------------------------------------- POST /contracts/{id}/sign --

func (s *Server) handleSignContract(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.loadContract(w, r)
	if !ok {
		return
	}
	var body struct {
		OTPCode            string `json:"otp_code"`
		SignatureObjectKey string `json:"signature_object_key"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	if len(body.OTPCode) != 6 || !validate.IsDigits(body.OTPCode) {
		f.Add("otp_code", "must be a 6-digit code")
	}
	objectKey := strings.TrimSpace(body.SignatureObjectKey)
	if objectKey != "" && objectKey != signatureKey(db.UUIDString(row.OrgID), db.UUIDString(row.ID)) {
		f.Add("signature_object_key", "must be an upload issued for this contract")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	if row.Status != contractPendingSignature {
		conflictCode(w, "not_signable", "contract not awaiting signature",
			"only a contract awaiting signature can be signed")
		return
	}
	// A second signature is refused before the code is spent: the unique index
	// on (contract_id, party) is the real guard, but reaching it would burn the
	// renter's OTP and answer 400 for what is really a 409.
	signed, err := s.q.CountContractSignatures(r.Context(), sqlc.CountContractSignaturesParams{
		OrgID: row.OrgID, ContractID: row.ID, Party: partyRenter,
	})
	if err != nil {
		s.serverError(w, r, "contract.sign.count", err)
		return
	}
	if signed > 0 {
		conflictCode(w, "already_signed", "already signed", "you have already signed this contract")
		return
	}

	// The hash is rechecked against the stored terms before the signature is
	// recorded: a renter must never be bound to a document that changed under
	// them between reading and signing (SPEC §5.5).
	if computed := hashOf(row); computed != db.StrVal(row.SnapshotHash) {
		conflictCode(w, "snapshot_mismatch", "document changed",
			"this document no longer matches what was presented for signature")
		return
	}

	// A drawn signature is optional; when one is claimed it must actually be in
	// the bucket, or the evidence bundle would reference nothing. A presigned
	// PUT enforces neither type nor size, so both are checked against what
	// actually landed (SPEC §7) — the same rule the branding and KYC
	// completions apply, and what stops a "signature" that is really an HTML
	// page served back to both parties from the same origin.
	method := methodOTPAccept
	var storedKey *string
	if objectKey != "" {
		if s.deps.Storage == nil {
			signatureStorageUnavailable(w)
			return
		}
		info, err := s.deps.Storage.Stat(r.Context(), storage.BucketSignatures, objectKey)
		if err != nil {
			httpx.WriteProblemFields(w, http.StatusBadRequest, "signature not found",
				"no signature image was found for that key",
				map[string]string{"signature_object_key": "no object has been uploaded under this key"})
			return
		}
		if info.Size > signatureMaxBytes ||
			strings.ToLower(strings.TrimSpace(info.ContentType)) != signatureContentType {
			if rmErr := s.deps.Storage.Remove(r.Context(), storage.BucketSignatures, objectKey); rmErr != nil {
				s.logger.Warn("could not remove rejected signature upload",
					"object_key", objectKey, "error", rmErr)
			}
			httpx.WriteProblemFields(w, http.StatusBadRequest, "invalid signature image",
				"the uploaded signature image was rejected",
				map[string]string{"signature_object_key": "must be a PNG of at most 512 KiB"})
			return
		}
		method = methodDrawn
		storedKey = &objectKey
	}

	// Verification attempts are capped like the login OTP's: a six-digit code
	// with a five-minute life is only strong while it cannot be guessed at
	// machine speed (SPEC §8, rate limiting on OTP).
	if res := s.limiter.Allow(r.Context(), "otp:verify:sign:"+db.UUIDString(row.ID),
		otpVerifyLimit, otpVerifyWindow); !res.Allowed {
		tooMany(w, res, "too many signing attempts for this contract")
		return
	}

	phone := db.StrVal(row.RenterPhone)
	switch err := s.store.CheckOTP(r.Context(), signPurpose(db.UUIDString(row.ID)), phone, body.OTPCode); {
	case errors.Is(err, auth.ErrNotFound):
		httpx.WriteProblemFields(w, http.StatusBadRequest, "invalid code",
			"the code is incorrect or has expired", map[string]string{"otp_code": "invalid or expired"})
		return
	case errors.Is(err, auth.ErrCacheUnavailable):
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "verification unavailable",
			"one-time codes cannot be verified right now")
		return
	case err != nil:
		s.serverError(w, r, "contract.sign.check", err)
		return
	}

	info := audit.RequestInfoFrom(r.Context())
	otpRef := "sign:" + db.UUIDString(row.ID)
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.CreateContractSignature(r.Context(), sqlc.CreateContractSignatureParams{
			OrgID: row.OrgID, ContractID: row.ID, Party: partyRenter, UserID: p.UserID,
			Method: method, OtpRef: &otpRef, SignatureObjectKey: storedKey,
			SnapshotHash: db.StrVal(row.SnapshotHash),
			Ip:           db.Str(info.IP), UserAgent: db.Str(info.UserAgent),
		}); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       db.UUIDString(row.OrgID),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionContractSign,
			EntityType:  audit.EntityContract,
			EntityID:    db.UUIDString(row.ID),
			After: map[string]any{
				"party": partyRenter, "method": method,
				"snapshot_hash": db.StrVal(row.SnapshotHash), "has_image": storedKey != nil,
			},
		})
	}); err != nil {
		if isUnique(err) {
			conflictCode(w, "already_signed", "already signed", "you have already signed this contract")
			return
		}
		s.serverError(w, r, "contract.sign.tx", err)
		return
	}

	out, ok := s.reloadContract(w, r, row.ID, pgtype.UUID{}, p.UserID)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"contract": out})
}

// ------------------------------------------- POST /contracts/{id}/activate --

func (s *Server) handleActivateContract(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.loadContract(w, r)
	if !ok {
		return
	}
	var body struct {
		LandlordRecorded bool   `json:"landlord_recorded"`
		Reason           string `json:"reason"`
	}
	if r.ContentLength > 0 && !DecodeJSON(w, r, &body) {
		return
	}
	if row.Status != contractPendingSignature {
		conflictCode(w, "not_pending_signature", "contract not awaiting signature",
			"only a contract awaiting signature can be activated")
		return
	}
	// The landlord countersigns the same document the renter read, so the hash
	// is rechecked here as well as at signing: a row edited underneath a
	// signature must not be able to become an active tenancy (SPEC §5.5).
	if computed := hashOf(row); computed != db.StrVal(row.SnapshotHash) {
		conflictCode(w, "snapshot_mismatch", "document changed",
			"this document no longer matches the terms that were signed")
		return
	}

	renterSigned, err := s.q.CountContractSignatures(r.Context(), sqlc.CountContractSignaturesParams{
		OrgID: row.OrgID, ContractID: row.ID, Party: partyRenter,
	})
	if err != nil {
		s.serverError(w, r, "contract.activate.count", err)
		return
	}

	// FLOWS 3.6: the landlord may record a tenancy for a renter who cannot
	// sign, but only deliberately, with a reason, and under its own audit
	// action — never as the quiet default.
	action := audit.ActionContractActivate
	var recordedReason string
	if renterSigned == 0 {
		if !body.LandlordRecorded {
			httpx.WriteProblemCode(w, http.StatusPreconditionFailed, "renter_signature_required",
				"renter signature required",
				"the renter has not signed yet; activate with landlord_recorded and a reason to record it yourself")
			return
		}
		f := validate.Fields{}
		recordedReason = f.MaxLen("reason", f.Required("reason", body.Reason), terminationReasonMax)
		if !f.Empty() {
			badRequest(w, f)
			return
		}
		action = audit.ActionContractActivateLandlordRecorded
	}

	org, err := s.q.GetOrg(r.Context(), row.OrgID)
	if err != nil {
		s.serverError(w, r, "contract.activate.org", err)
		return
	}
	settings := parseSettings(org.Settings)
	brand := s.brandingAssets(r.Context(), row.OrgID, org.Name)

	rows := contract.Generate(int(row.RentAmount), int(row.RentPeriodDays),
		int(row.TermDays), int(row.PaymentPeriodDays), row.StartDate.Time, intPtr(row.DueDay))
	if len(rows) == 0 {
		s.serverError(w, r, "contract.activate.schedule", errors.New("generator produced no rows"))
		return
	}

	info := audit.RequestInfoFrom(r.Context())
	landlordMethod := methodOTPAccept
	if renterSigned == 0 {
		landlordMethod = methodLandlordRecord
	}

	var notifyID string
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		activated, err := q.ActivateContract(r.Context(), sqlc.ActivateContractParams{
			OrgID: row.OrgID, ID: row.ID,
		})
		if err != nil {
			return err
		}
		// The landlord's countersignature is recorded the same way as the
		// renter's: append-only evidence carrying the hash they countersigned.
		if _, err := q.CreateContractSignature(r.Context(), sqlc.CreateContractSignatureParams{
			OrgID: row.OrgID, ContractID: row.ID, Party: partyLandlord, UserID: p.UserID,
			Method: landlordMethod, SnapshotHash: db.StrVal(row.SnapshotHash),
			Ip: db.Str(info.IP), UserAgent: db.Str(info.UserAgent),
		}); err != nil {
			return err
		}
		for _, gen := range rows {
			if _, err := q.CreatePaymentSchedule(r.Context(), sqlc.CreatePaymentScheduleParams{
				OrgID: row.OrgID, ContractID: row.ID,
				PeriodStart: pgtype.Date{Time: gen.PeriodStart, Valid: true},
				PeriodEnd:   pgtype.Date{Time: gen.PeriodEnd, Valid: true},
				DueDate:     pgtype.Date{Time: gen.DueDate, Valid: true},
				Amount:      gen.Amount,
			}); err != nil {
				return err
			}
		}
		// The unit becomes occupied only now — an approved-but-unsigned
		// application never strands a unit (API.md Phase 3 notes).
		if _, err := q.SetUnitStatusDerived(r.Context(), sqlc.SetUnitStatusDerivedParams{
			Status: statusOccupied, OrgID: row.OrgID, ID: row.UnitID,
		}); err != nil {
			return err
		}
		after := map[string]any{
			"status": contractActive, "schedules": len(rows),
			"unit_status": statusOccupied, "landlord_method": landlordMethod,
			"activated_at": activated.ActivatedAt.Time,
		}
		if recordedReason != "" {
			after["reason"] = recordedReason
			after["landlord_recorded"] = true
		}
		if err := audit.Record(r.Context(), q, audit.Entry{
			OrgID:       db.UUIDString(row.OrgID),
			ActorUserID: p.UserIDString(),
			Action:      action,
			EntityType:  audit.EntityContract,
			EntityID:    db.UUIDString(row.ID),
			Before:      map[string]any{"status": contractPendingSignature},
			After:       after,
		}); err != nil {
			return err
		}
		notifyID, err = s.queueContractSMS(r.Context(), q, contractMessage{
			OrgID: db.UUIDString(row.OrgID), UserID: db.UUIDString(row.RenterUserID),
			ContractID: db.UUIDString(row.ID), Kind: notify.KindWelcome,
			Lang: settings.SMSLanguage, Phone: db.StrVal(row.RenterPhone),
			Overrides: settings.notifyOverrides(),
			Vars: notify.Vars{
				Name: row.RenterName, Unit: row.UnitName, Org: brand.DisplayName,
				Property:  row.PropertyName,
				StartDate: row.StartDate.Time.Format(dateLayout),
				Amount:    formatTZS(rows[0].Amount),
				DueDate:   rows[0].DueDate.Format(dateLayout),
			},
		})
		return err
	}); err != nil {
		if isNoRows(err) {
			conflictCode(w, "not_pending_signature", "contract not awaiting signature",
				"only a contract awaiting signature can be activated")
			return
		}
		s.serverError(w, r, "contract.activate.tx", err)
		return
	}
	s.enqueueNotifications(r.Context(), notifyID)

	out, ok := s.reloadContract(w, r, row.ID, p.OrgID, pgtype.UUID{})
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"contract": out})
}

// ------------------------------------------ POST /contracts/{id}/terminate --

func (s *Server) handleTerminateContract(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.loadContract(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason        string `json:"reason"`
		EffectiveDate string `json:"effective_date"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	reason := f.MaxLen("reason", f.Required("reason", body.Reason), terminationReasonMax)
	effective := time.Now().UTC().Truncate(24 * time.Hour)
	if v := strings.TrimSpace(body.EffectiveDate); v != "" {
		effective = requiredDate(f, "effective_date", v)
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	switch row.Status {
	case contractPendingSignature, contractActive, contractExpiring:
	default:
		conflictCode(w, "not_terminable", "contract not running",
			"only a contract awaiting signature or running can be terminated")
		return
	}

	org, err := s.q.GetOrg(r.Context(), row.OrgID)
	if err != nil {
		s.serverError(w, r, "contract.terminate.org", err)
		return
	}
	settings := parseSettings(org.Settings)
	brand := s.brandingAssets(r.Context(), row.OrgID, org.Name)

	var waived int
	var notifyID string
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		terminated, err := q.TerminateContract(r.Context(), sqlc.TerminateContractParams{
			OrgID: row.OrgID, ID: row.ID, TerminationReason: &reason,
			TerminationEffectiveDate: pgtype.Date{Time: effective, Valid: true},
		})
		if err != nil {
			return err
		}
		// Periods the renter has already lived through stand, paid or not; only
		// what starts after the effective date is waived (FLOWS 6.5).
		ids, err := q.WaiveSchedulesAfter(r.Context(), sqlc.WaiveSchedulesAfterParams{
			OrgID: row.OrgID, ContractID: row.ID,
			EffectiveDate: pgtype.Date{Time: effective, Valid: true},
		})
		if err != nil {
			return err
		}
		waived = len(ids)

		// The unit returns to the vacancy board unless some other contract
		// still runs on it.
		others, err := q.CountOtherActiveContractsForUnit(r.Context(), sqlc.CountOtherActiveContractsForUnitParams{
			OrgID: row.OrgID, UnitID: row.UnitID, ExcludeID: row.ID,
		})
		if err != nil {
			return err
		}
		if others == 0 {
			if _, err := q.SetUnitStatusDerived(r.Context(), sqlc.SetUnitStatusDerivedParams{
				Status: statusVacant, OrgID: row.OrgID, ID: row.UnitID,
			}); err != nil {
				return err
			}
		}
		if err := audit.Record(r.Context(), q, audit.Entry{
			OrgID:       db.UUIDString(row.OrgID),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionContractTerminate,
			EntityType:  audit.EntityContract,
			EntityID:    db.UUIDString(row.ID),
			Before:      map[string]any{"status": row.Status},
			After: map[string]any{
				"status": terminated.Status, "reason": reason,
				"effective_date": effective.Format(dateLayout),
				"waived":         waived, "unit_freed": others == 0,
			},
		}); err != nil {
			return err
		}
		notifyID, err = s.queueContractSMS(r.Context(), q, contractMessage{
			OrgID: db.UUIDString(row.OrgID), UserID: db.UUIDString(row.RenterUserID),
			ContractID: db.UUIDString(row.ID), Kind: notify.KindContractTerminated,
			Lang: settings.SMSLanguage, Phone: db.StrVal(row.RenterPhone),
			Overrides: settings.notifyOverrides(),
			Vars: notify.Vars{
				Name: row.RenterName, Unit: row.UnitName, Property: row.PropertyName,
				Org: brand.DisplayName, Reason: reason,
			},
		})
		return err
	}); err != nil {
		if isNoRows(err) {
			conflictCode(w, "not_terminable", "contract not running",
				"only a contract awaiting signature or running can be terminated")
			return
		}
		s.serverError(w, r, "contract.terminate.tx", err)
		return
	}
	s.enqueueNotifications(r.Context(), notifyID)

	out, ok := s.reloadContract(w, r, row.ID, p.OrgID, pgtype.UUID{})
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"contract": out})
}

// ---------------------------------------------- GET /contracts/{id}/verify --

func (s *Server) handleVerifyContract(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	row, ok := s.loadContract(w, r)
	if !ok {
		return
	}
	sigs, err := s.q.ListContractSignatures(r.Context(), sqlc.ListContractSignaturesParams{
		OrgID: row.OrgID, ContractID: row.ID,
	})
	if err != nil {
		s.serverError(w, r, "contract.verify.signatures", err)
		return
	}
	computed := hashOf(row)
	stored := db.StrVal(row.SnapshotHash)
	WriteJSON(w, http.StatusOK, map[string]any{
		"valid":         computed == stored && stored != "",
		"computed_hash": computed,
		"stored_hash":   stored,
		"signatures":    toSignatures(sigs),
	})
}

// ------------------------------------------------------------- schedules --

func (s *Server) handleContractSchedules(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	row, ok := s.loadContract(w, r)
	if !ok {
		return
	}
	rows, err := s.q.ListSchedulesForContract(r.Context(), sqlc.ListSchedulesForContractParams{
		OrgID: row.OrgID, ContractID: row.ID,
	})
	if err != nil {
		s.serverError(w, r, "contract.schedules", err)
		return
	}
	items := make([]scheduleResponse, 0, len(rows))
	for _, sc := range rows {
		items = append(items, toSchedule(sc))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleMySchedules is the renter's money screen: every instalment they owe,
// the next one due, what is overdue in total, and the account to pay into
// (FLOWS 7.1). It sweeps overdue for the orgs the renter rents from first, so
// a date that passed since the last tick is already reflected.
func (s *Server) handleMySchedules(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	rows, err := s.q.ListSchedulesForRenter(r.Context(), p.UserID)
	if err != nil {
		s.serverError(w, r, "schedules.me", err)
		return
	}
	// A renter may rent from more than one org, and grace periods are per org,
	// so the on-demand sweep runs once per org before the rows are rendered.
	if s.flipOverdueForOrgsOf(r.Context(), rows) {
		if rows, err = s.q.ListSchedulesForRenter(r.Context(), p.UserID); err != nil {
			s.serverError(w, r, "schedules.me.reload", err)
			return
		}
	}

	today := time.Now().UTC()
	items := make([]map[string]any, 0, len(rows))
	var (
		next         map[string]any
		nextOrgID    pgtype.UUID
		overdueTotal int64
	)
	for _, sc := range rows {
		item := map[string]any{
			"id":           db.UUIDString(sc.ID),
			"period_start": sc.PeriodStart.Time.Format(dateLayout),
			"period_end":   sc.PeriodEnd.Time.Format(dateLayout),
			"due_date":     sc.DueDate.Time.Format(dateLayout),
			"amount":       sc.Amount,
			"status":       sc.Status,
			"paid_amount":  sc.PaidAmount,
			"days_overdue": daysOverdue(sc.Status, sc.DueDate.Time, today),
			"contract": map[string]any{
				"id": db.UUIDString(sc.ContractID), "unit_name": sc.UnitName,
				"property_name": sc.PropertyName, "org_name": sc.OrgName,
				"status": sc.ContractStatus,
			},
		}
		items = append(items, item)
		if sc.Status == "overdue" && sc.Amount > sc.PaidAmount {
			overdueTotal += sc.Amount - sc.PaidAmount
		}
		// The rows come back due-date ascending, so the first unsettled one on
		// a live contract is the next payment the renter owes.
		if next == nil && isLiveContract(sc.ContractStatus) &&
			(sc.Status == "pending" || sc.Status == "partial" || sc.Status == "overdue") {
			next = item
			nextOrgID = sc.OrgID
		}
	}
	// The account shown is the one behind the next payment. A renter with no
	// outstanding instalment is not being asked for money, so none is shown.
	var bank *BankAccount
	if nextOrgID.Valid {
		if org, err := s.q.GetOrg(r.Context(), nextOrgID); err == nil {
			bank = parseSettings(org.Settings).BankAccount
		}
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items": items, "next_due": next,
		"overdue_total": overdueTotal, "bank_account": bank,
	})
}

// flipOverdueForOrgsOf runs the on-demand overdue sweep once per org appearing
// in the renter's schedules, and reports whether anything changed.
func (s *Server) flipOverdueForOrgsOf(ctx context.Context, rows []sqlc.ListSchedulesForRenterRow) bool {
	seen := map[string]bool{}
	changed := false
	for _, sc := range rows {
		key := db.UUIDString(sc.OrgID)
		if seen[key] {
			continue
		}
		seen[key] = true
		flipped, err := payment.FlipOverdue(ctx, s.q, sc.OrgID)
		if err != nil {
			s.logger.Warn("on-demand overdue sweep failed", "org_id", key, "error", err)
			continue
		}
		changed = changed || flipped > 0
	}
	return changed
}

// ------------------------------------------------------------- helpers --

// contractMessage carries what a contract SMS needs across the tx boundary.
type contractMessage struct {
	OrgID      string
	UserID     string
	ContractID string
	Kind       string
	Lang       string
	Phone      string
	Vars       notify.Vars
	Overrides  notify.Overrides
}

// queueContractSMS writes the notification row inside the caller's transaction
// and returns the id to push onto Redis after the commit.
func (s *Server) queueContractSMS(ctx context.Context, q *sqlc.Queries, m contractMessage) (string, error) {
	if m.Phone == "" {
		s.logger.Warn("contract notification skipped: renter has no phone number",
			"contract_id", m.ContractID, "kind", m.Kind)
		return "", nil
	}
	id, err := notify.Queue(ctx, q, notify.Msg{
		OrgID: m.OrgID, UserID: m.UserID, Kind: m.Kind,
		DedupeKey: m.Kind + ":" + m.ContractID, Phone: m.Phone,
		Body: notify.Render(m.Kind, m.Lang, m.Vars, m.Overrides),
	})
	if errors.Is(err, notify.ErrDuplicate) {
		return "", nil
	}
	return id, err
}

// hashOf recomputes a stored contract's snapshot hash from the row itself —
// the whole point of GET /verify is that it reads the same columns a tamperer
// would have had to change.
func hashOf(row sqlc.GetContractRow) string {
	return contract.Snapshot{
		TermsHTML:         row.TermsSnapshotHtml,
		UnitID:            db.UUIDString(row.UnitID),
		RenterUserID:      db.UUIDString(row.RenterUserID),
		RentAmount:        row.RentAmount,
		RentPeriodDays:    int(row.RentPeriodDays),
		PaymentPeriodDays: int(row.PaymentPeriodDays),
		TermDays:          int(row.TermDays),
		StartDate:         row.StartDate.Time.Format(dateLayout),
		EndDate:           row.EndDate.Time.Format(dateLayout),
		DueDay:            intPtr(row.DueDay),
	}.Hash()
}

// signPurpose keys a signing OTP to one contract.
func signPurpose(contractID string) string { return "sign:" + contractID }

// signatureKey is the one object key a contract's drawn signature may occupy.
func signatureKey(orgID, contractID string) string {
	return orgID + "/" + contractID + "/renter.png"
}

func signatureStorageUnavailable(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusServiceUnavailable, "storage unavailable",
		"signature images cannot be uploaded or read right now")
}

func isLiveContract(status string) bool {
	return status == contractActive || status == contractExpiring || status == contractPendingSignature
}

// uuidField parses an optional or required UUID body field.
func uuidField(f validate.Fields, name, in string, required bool) pgtype.UUID {
	v := strings.TrimSpace(in)
	if v == "" {
		if required {
			f.Add(name, name+" is required")
		}
		return pgtype.UUID{}
	}
	id, err := db.ParseUUID(v)
	if err != nil {
		f.Add(name, "must be a "+strings.ReplaceAll(strings.TrimSuffix(name, "_id"), "_", " ")+" id (UUID)")
		return pgtype.UUID{}
	}
	return id
}

func intPtr(v *int32) *int {
	if v == nil {
		return nil
	}
	out := int(*v)
	return &out
}

func dueDayString(v *int32) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(int(*v))
}

// formatTZS renders whole shillings with thousands separators ("TZS 250,000"),
// the form the SMS and the rendered terms both use.
func formatTZS(amount int64) string { return notify.FormatTZS(amount) }
