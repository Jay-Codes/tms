package httpserver

import (
	"context"
	"errors"
	"net/http"
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
	"tms/backend/internal/validate"
)

// Link-request bounds (API.md).
const (
	// startBackstopDays is how far in the past a start date may be. A renter
	// who moved in last week and is only now scanning the sticker must still
	// be able to record the real date; anything older is a typo.
	startBackstopDays = 7
	// startAheadDays caps a forward-dated start, so a request cannot reserve a
	// unit indefinitely.
	startAheadDays  = 365
	termDaysMax     = 3650
	rejectReasonMax = 200
)

// Link request statuses.
const (
	linkPending   = "pending"
	linkApproved  = "approved"
	linkRejected  = "rejected"
	linkCancelled = "cancelled"
)

// notFoundLinkRequest keeps a request belonging to another org (or another
// renter) indistinguishable from one that does not exist (API.md).
func notFoundLinkRequest(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such link request")
}

// ------------------------------------------- POST /units/{unit_code}/link --

func (s *Server) handleCreateLinkRequest(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	if res := s.limiter.Allow(r.Context(), "link:create:"+p.UserIDString(),
		linkCreateLimit, linkCreateWindow); !res.Allowed {
		tooMany(w, res, "too many link requests; try again later")
		return
	}
	code := strings.ToUpper(strings.TrimSpace(chi.URLParam(r, "unit_code")))

	var body struct {
		PaymentPeriodID string `json:"payment_period_id"`
		TermDays        int32  `json:"term_days"`
		StartDate       string `json:"start_date"`
		AcceptedTerms   bool   `json:"accepted_terms"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}

	f := validate.Fields{}
	periodID, err := db.ParseUUID(strings.TrimSpace(body.PaymentPeriodID))
	if err != nil {
		f.Add("payment_period_id", "must be a payment period id (UUID)")
	}
	if body.TermDays <= 0 || body.TermDays > termDaysMax {
		f.Add("term_days", "must be a whole number of days between 1 and 3650")
	}
	startDate := requiredDate(f, "start_date", body.StartDate)
	if !body.AcceptedTerms {
		f.Add("accepted_terms", "the terms must be accepted")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	if startDate.Before(today.AddDate(0, 0, -startBackstopDays)) {
		f.Add("start_date", "must not be more than 7 days in the past")
	}
	if startDate.After(today.AddDate(0, 0, startAheadDays)) {
		f.Add("start_date", "must not be more than a year ahead")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	// The unit is resolved by its public code — the sticker IS the capability
	// (SPEC §3.1) — and the org comes from the unit, never from the client.
	unit, err := s.q.GetUnitByCode(r.Context(), code)
	if isNoRows(err) {
		notFoundUnit(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "link.create.unit", err)
		return
	}
	// An unlisted unit is invisible to renters, exactly as on the public
	// endpoint: its sticker must read as an unknown code.
	if unit.Status == statusUnlisted {
		notFoundUnit(w)
		return
	}
	if unit.Status == statusOccupied {
		conflictCode(w, "unit_occupied", "unit occupied",
			"this unit is already let; ask the landlord to be told when it is free")
		return
	}
	if unit.Status == statusMaintenance {
		conflictCode(w, "unit_unavailable", "unit unavailable",
			"this unit is out of service and is not accepting applications")
		return
	}

	org, err := s.q.GetOrg(r.Context(), unit.OrgID)
	if isNoRows(err) || (err == nil && org.Status != "active") {
		notFoundUnit(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "link.create.org", err)
		return
	}

	// KYC gate: a landlord must have something to check before deciding.
	profile, err := s.loadProfile(r.Context(), p.UserID)
	if err != nil && !isNoRows(err) {
		s.serverError(w, r, "link.create.profile", err)
		return
	}
	if isNoRows(err) || kycOut(profile.KycStatus) == kycNone {
		httpx.WriteProblemCode(w, http.StatusPreconditionFailed, "kyc_required",
			"identity details required",
			"complete your profile (NIDA and next of kin) before applying for a unit")
		return
	}

	// The period must be one this unit actually offers: active in the org, and
	// inside allowed_period_ids when the unit restricts them.
	period, err := s.q.GetPaymentPeriod(r.Context(), sqlc.GetPaymentPeriodParams{
		OrgID: unit.OrgID, ID: periodID,
	})
	if isNoRows(err) || (err == nil && !period.Active) {
		conflictCode(w, "period_not_offered", "payment period not offered",
			"that payment period is not offered for this unit")
		return
	}
	if err != nil {
		s.serverError(w, r, "link.create.period", err)
		return
	}
	if allowed := allowedSet(unit.AllowedPeriodIds); allowed != nil && !allowed[db.UUIDString(period.ID)] {
		conflictCode(w, "period_not_offered", "payment period not offered",
			"that payment period is not offered for this unit")
		return
	}

	// One live application per renter per unit. The partial unique index is
	// the real guard; this check turns the race loser into the same 409.
	pending, err := s.q.CountPendingLinkRequest(r.Context(), sqlc.CountPendingLinkRequestParams{
		UnitID: unit.ID, RenterUserID: p.UserID,
	})
	if err != nil {
		s.serverError(w, r, "link.create.pending", err)
		return
	}
	if pending > 0 {
		conflictCode(w, "duplicate_request", "request already pending",
			"you already have a pending request for this unit")
		return
	}

	// The number a decision SMS goes to is the account's own: a renter signs
	// up by phone, and renter_profiles carries no number of its own.
	renter, err := s.q.GetUserByID(r.Context(), p.UserID)
	if err != nil {
		s.serverError(w, r, "link.create.renter", err)
		return
	}

	settings := parseSettings(org.Settings)
	status := linkPending
	var decidedAt pgtype.Timestamptz
	var decidedBy pgtype.UUID
	if settings.AutoApproveLinks {
		status = linkApproved
		decidedAt = db.TS(time.Now().UTC())
	}

	endDate := contract.EndDate(startDate, int(body.TermDays))
	orgID := db.UUIDString(unit.OrgID)

	var created sqlc.UnitLinkRequest
	var queuedID, contractQueuedID string
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		created, err = q.CreateLinkRequest(r.Context(), sqlc.CreateLinkRequestParams{
			OrgID: unit.OrgID, UnitID: unit.ID, RenterUserID: p.UserID, Status: status,
			PaymentPeriodID: period.ID, TermDays: &body.TermDays,
			StartDate: pgtype.Date{Time: startDate, Valid: true},
			EndDate:   pgtype.Date{Time: endDate, Valid: true},
			DecidedAt: decidedAt, DecidedByUserID: decidedBy,
		})
		if err != nil {
			return err
		}
		if err := audit.Record(r.Context(), q, audit.Entry{
			OrgID:       orgID,
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionLinkRequestCreate,
			EntityType:  audit.EntityLinkRequest,
			EntityID:    db.UUIDString(created.ID),
			After: map[string]any{
				"unit_id": db.UUIDString(unit.ID), "unit_code": unit.UnitCode,
				"payment_period_id": db.UUIDString(period.ID), "term_days": body.TermDays,
				"start_date": startDate.Format(dateLayout), "end_date": endDate.Format(dateLayout),
				"status": status, "auto_approved": settings.AutoApproveLinks,
			},
		}); err != nil {
			return err
		}
		if status != linkApproved {
			return nil
		}
		// Auto-approval is a decision, so it takes the same hook and the same
		// notification as a landlord pressing Approve.
		_, contractQueuedID, _, err = s.onLinkApproved(r.Context(), q, created, p.UserIDString())
		if err != nil {
			return err
		}
		queuedID, err = s.queueLinkDecision(r.Context(), q, linkDecision{
			OrgID: orgID, RenterUserID: p.UserIDString(), RequestID: db.UUIDString(created.ID),
			Kind: notify.KindLinkApproved, Lang: settings.SMSLanguage,
			Unit: unit.Name, Org: org.Name, Phone: db.StrVal(renter.Phone),
			Name: renter.FullName, Overrides: settings.notifyOverrides(),
		})
		return err
	}); err != nil {
		if isUnique(err) {
			// The partial unique index caught a second concurrent request.
			conflictCode(w, "duplicate_request", "request already pending",
				"you already have a pending request for this unit")
			return
		}
		if writeCreateError(w, err) {
			// Auto-approve could not produce a contract (an unpriced unit, a
			// missing template): the application is refused whole rather than
			// left approved with nothing to sign.
			return
		}
		s.serverError(w, r, "link.create.tx", err)
		return
	}
	// Redis only learns about the messages once the rows are durable.
	s.enqueueNotifications(r.Context(), queuedID, contractQueuedID)

	row, err := s.q.GetLinkRequest(r.Context(), sqlc.GetLinkRequestParams{
		ID: created.ID, RenterUserID: p.UserID,
	})
	if err != nil {
		s.serverError(w, r, "link.create.reload", err)
		return
	}
	out := toLinkRequest(linkRowOfGet(row), false)
	out.SchedulePreview = previewFor(linkRowOfGet(row))
	WriteJSON(w, http.StatusCreated, map[string]any{"request": out})
}

// previewFor computes the schedule summary for a request, using the same pure
// generator Phase 4 materialises into payment_schedules.
func previewFor(r linkRow) *schedulePreview {
	if r.TermDays == nil || r.PeriodDays <= 0 || !r.HasPrice || r.PricePeriodDays <= 0 || r.StartDate == nil {
		return nil
	}
	start, err := time.Parse(dateLayout, *r.StartDate)
	if err != nil {
		return nil
	}
	rows := contract.Generate(int(r.PriceAmount), int(r.PricePeriodDays),
		int(*r.TermDays), int(r.PeriodDays), start, nil)
	if len(rows) == 0 {
		return nil
	}
	var total int64
	for _, row := range rows {
		total += row.Amount
	}
	return &schedulePreview{
		Count:       len(rows),
		FirstDue:    rows[0].DueDate.Format(dateLayout),
		AmountFirst: rows[0].Amount,
		AmountLast:  rows[len(rows)-1].Amount,
		Total:       total,
	}
}

// ------------------------------------------------- GET /me/link-requests --

func (s *Server) handleListMyLinkRequests(w http.ResponseWriter, r *http.Request) {
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

	rows, err := s.q.ListLinkRequests(r.Context(), sqlc.ListLinkRequestsParams{
		RenterUserID: p.UserID, CursorAt: page.CursorAt, CursorID: page.CursorID, RowLimit: page.Limit,
	})
	if err != nil {
		s.serverError(w, r, "link.me.list", err)
		return
	}
	items := make([]linkRequestResponse, 0, len(rows))
	for _, row := range rows {
		lr := linkRowOfList(row)
		item := toLinkRequest(lr, false)
		item.SchedulePreview = previewFor(lr)
		items = append(items, item)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": cursorOfLinks(rows, page.Limit)})
}

// ------------------------------------------ DELETE /me/link-requests/{id} --

func (s *Server) handleCancelMyLinkRequest(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundLinkRequest(w)
		return
	}

	// Scoped to the caller: another renter's request is a 404, not a 403.
	existing, err := s.q.GetLinkRequest(r.Context(), sqlc.GetLinkRequestParams{
		ID: id, RenterUserID: p.UserID,
	})
	if isNoRows(err) {
		notFoundLinkRequest(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "link.cancel.load", err)
		return
	}
	if existing.Status != linkPending {
		conflictCode(w, "not_pending", "request already decided",
			"only a pending request can be cancelled")
		return
	}

	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		cancelled, err := q.CancelLinkRequest(r.Context(), sqlc.CancelLinkRequestParams{
			ID: id, RenterUserID: p.UserID,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       db.UUIDString(cancelled.OrgID),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionLinkRequestCancel,
			EntityType:  audit.EntityLinkRequest,
			EntityID:    db.UUIDString(cancelled.ID),
			Before:      map[string]any{"status": linkPending},
			After:       map[string]any{"status": linkCancelled},
		})
	}); err != nil {
		if isNoRows(err) {
			// Decided between the read and the update.
			conflictCode(w, "not_pending", "request already decided",
				"only a pending request can be cancelled")
			return
		}
		s.serverError(w, r, "link.cancel.tx", err)
		return
	}
	NoContent(w)
}

// ---------------------------------------------------- GET /link-requests --

func (s *Server) handleListLinkRequests(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	f := validate.Fields{}
	page := parseListPage(r, f)
	qs := r.URL.Query()

	params := sqlc.ListLinkRequestsParams{
		OrgID: p.OrgID, CursorAt: page.CursorAt, CursorID: page.CursorID, RowLimit: page.Limit,
	}
	if v := strings.TrimSpace(qs.Get("status")); v != "" {
		status := f.OneOf("status", v, linkPending, linkApproved, linkRejected, linkCancelled)
		params.Status = &status
	}
	if v := strings.TrimSpace(qs.Get("unit_id")); v != "" {
		id, err := db.ParseUUID(v)
		if err != nil {
			f.Add("unit_id", "must be a unit id (UUID)")
		} else {
			params.UnitID = id
		}
	}
	if v := strings.TrimSpace(qs.Get("renter_user_id")); v != "" {
		id, err := db.ParseUUID(v)
		if err != nil {
			f.Add("renter_user_id", "must be a user id (UUID)")
		} else {
			params.RenterUserID = id
		}
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	rows, err := s.q.ListLinkRequests(r.Context(), params)
	if err != nil {
		s.serverError(w, r, "link.list", err)
		return
	}
	items := make([]linkRequestResponse, 0, len(rows))
	for _, row := range rows {
		lr := linkRowOfList(row)
		item := toLinkRequest(lr, true)
		item.SchedulePreview = previewFor(lr)
		items = append(items, item)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": cursorOfLinks(rows, page.Limit)})
}

func cursorOfLinks(rows []sqlc.ListLinkRequestsRow, limit int32) *string {
	if len(rows) == 0 {
		return nil
	}
	last := rows[len(rows)-1]
	return nextCursor(len(rows), limit, last.CreatedAt.Time, db.UUIDString(last.ID))
}

// ----------------------------------------------- GET /link-requests/{id} --

// orgLinkRequest loads a request inside the caller's org (404 otherwise).
func (s *Server) orgLinkRequest(w http.ResponseWriter, r *http.Request, p auth.Principal) (sqlc.GetLinkRequestRow, bool) {
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundLinkRequest(w)
		return sqlc.GetLinkRequestRow{}, false
	}
	row, err := s.q.GetLinkRequest(r.Context(), sqlc.GetLinkRequestParams{ID: id, OrgID: p.OrgID})
	if isNoRows(err) {
		notFoundLinkRequest(w)
		return sqlc.GetLinkRequestRow{}, false
	}
	if err != nil {
		s.serverError(w, r, "link.get", err)
		return sqlc.GetLinkRequestRow{}, false
	}
	return row, true
}

func (s *Server) handleGetLinkRequest(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.orgLinkRequest(w, r, p)
	if !ok {
		return
	}
	lr := linkRowOfGet(row)
	out := toLinkRequest(lr, true)
	out.SchedulePreview = previewFor(lr)

	profile, err := s.loadProfile(r.Context(), row.RenterUserID)
	if err != nil && !isNoRows(err) {
		s.serverError(w, r, "link.get.profile", err)
		return
	}
	block := renterProfileBlock{FullName: row.RenterName, KycStatus: kycOut(row.KycStatus)}
	if err == nil {
		block = renterProfileBlock{
			FullName:       profile.FullName,
			NidaMasked:     maskNIDA(profile.NidaNumber),
			NextOfKinName:  profile.NextOfKinName,
			NextOfKinPhone: profile.NextOfKinPhone,
			KycStatus:      kycOut(profile.KycStatus),
			KycDocUploaded: profile.KycDocObjectKey != nil && *profile.KycDocObjectKey != "",
		}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"request": out, "renter_profile": block})
}

// ------------------------------------------ POST /link-requests/{id}/... --

func (s *Server) handleApproveLinkRequest(w http.ResponseWriter, r *http.Request) {
	s.decideLinkRequest(w, r, linkApproved, "")
}

func (s *Server) handleRejectLinkRequest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	reason := f.MaxLen("reason", f.Required("reason", body.Reason), rejectReasonMax)
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	s.decideLinkRequest(w, r, linkRejected, reason)
}

// decideLinkRequest is the shared approve/reject path: flip a pending request,
// audit it, queue the renter's SMS — all in one transaction.
func (s *Server) decideLinkRequest(w http.ResponseWriter, r *http.Request, status, reason string) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.orgLinkRequest(w, r, p)
	if !ok {
		return
	}
	if row.Status != linkPending {
		// Approving an already-approved request is how a landlord asks for the
		// contract that approval should have created (API.md, Phase 4 notes);
		// everything else about a decided request is still a 409.
		if status == linkApproved && row.Status == linkApproved {
			s.backfillApprovedRequest(w, r, p, row)
			return
		}
		conflictCode(w, "not_pending", "request already decided",
			"only a pending request can be approved or rejected")
		return
	}

	org, err := s.q.GetOrg(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "link.decide.org", err)
		return
	}
	settings := parseSettings(org.Settings)

	kind := notify.KindLinkApproved
	var rejectionReason *string
	if status == linkRejected {
		kind = notify.KindLinkRejected
		rejectionReason = &reason
	}

	var queuedID, contractQueuedID string
	var createdContract sqlc.Contract
	var madeContract bool
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		decided, err := q.DecideLinkRequest(r.Context(), sqlc.DecideLinkRequestParams{
			Status: status, RejectionReason: rejectionReason,
			DecidedByUserID: p.UserID, OrgID: p.OrgID, ID: row.ID,
		})
		if err != nil {
			return err
		}
		action := audit.ActionLinkApprove
		if status == linkRejected {
			action = audit.ActionLinkReject
		}
		after := map[string]any{"status": status}
		if rejectionReason != nil {
			after["rejection_reason"] = *rejectionReason
		}
		if err := audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      action,
			EntityType:  audit.EntityLinkRequest,
			EntityID:    db.UUIDString(decided.ID),
			Before:      map[string]any{"status": linkPending},
			After:       after,
		}); err != nil {
			return err
		}
		if status == linkApproved {
			createdContract, contractQueuedID, madeContract, err = s.onLinkApproved(
				r.Context(), q, decided, p.UserIDString())
			if err != nil {
				return err
			}
		}
		queuedID, err = s.queueLinkDecision(r.Context(), q, linkDecision{
			OrgID: p.OrgIDString(), RenterUserID: db.UUIDString(row.RenterUserID),
			RequestID: db.UUIDString(decided.ID), Kind: kind, Lang: settings.SMSLanguage,
			Unit: row.UnitName, Org: org.Name, Reason: reason,
			Phone: db.StrVal(row.RenterPhone),
		})
		return err
	}); err != nil {
		if isNoRows(err) {
			conflictCode(w, "not_pending", "request already decided",
				"only a pending request can be approved or rejected")
			return
		}
		if writeCreateError(w, err) {
			// The contract could not be written, so the approval is refused
			// whole: a renter must never be told "approved" with nothing to
			// sign behind it.
			return
		}
		s.serverError(w, r, "link.decide.tx", err)
		return
	}
	s.enqueueNotifications(r.Context(), queuedID, contractQueuedID)

	reloaded, err := s.q.GetLinkRequest(r.Context(), sqlc.GetLinkRequestParams{ID: row.ID, OrgID: p.OrgID})
	if err != nil {
		s.serverError(w, r, "link.decide.reload", err)
		return
	}
	lr := linkRowOfGet(reloaded)
	out := toLinkRequest(lr, true)
	out.SchedulePreview = previewFor(lr)
	body := map[string]any{"request": out}
	if madeContract {
		if contractOut, ok := s.reloadContract(w, r, createdContract.ID, p.OrgID, pgtype.UUID{}); ok {
			body["contract"] = contractOut
		}
	}
	WriteJSON(w, http.StatusOK, body)
}

// onLinkApproved is what makes approval mean something: it creates the
// contract from the request, in the approval's own transaction (SPEC §3.1 —
// "approval creates/activates the contract"; FLOWS 3.3).
//
// The unit deliberately stays vacant: it becomes occupied when the contract is
// activated, not when the application is accepted, so an approved-then-
// abandoned request cannot strand a unit.
//
// It is idempotent by way of `contracts.link_request_id`: a request that
// already has a contract yields that contract and writes nothing, which is what
// makes the backfill path on an already-approved request safe.
func (s *Server) onLinkApproved(
	ctx context.Context, q *sqlc.Queries, req sqlc.UnitLinkRequest, actorUserID string,
) (sqlc.Contract, string, bool, error) {
	if existing, err := q.GetContractForLinkRequest(ctx, sqlc.GetContractForLinkRequestParams{
		OrgID: req.OrgID, LinkRequestID: req.ID,
	}); err == nil && existing.Valid {
		return sqlc.Contract{}, "", false, nil
	} else if err != nil && !isNoRows(err) {
		return sqlc.Contract{}, "", false, err
	}

	termDays := int32(0)
	if req.TermDays != nil {
		termDays = *req.TermDays
	}
	created, notifyID, err := s.createContractTx(ctx, q, contractInput{
		OrgID:           req.OrgID,
		UnitID:          req.UnitID,
		RenterUserID:    req.RenterUserID,
		PaymentPeriodID: req.PaymentPeriodID,
		TermDays:        termDays,
		StartDate:       req.StartDate.Time,
		LinkRequestID:   req.ID,
		ActorUserID:     actorUserID,
	})
	if err != nil {
		return sqlc.Contract{}, "", false, err
	}
	return created, notifyID, true, nil
}

// backfillApprovedRequest is the second half of POST /link-requests/{id}/approve:
// a request approved before Phase 4 landed (or by a run where contract creation
// failed) carries no contract, and re-approving it is how a landlord asks for
// one. An already-approved request that DOES have a contract is still a 409 —
// there is nothing left to do (API.md, Phase 4 notes).
func (s *Server) backfillApprovedRequest(w http.ResponseWriter, r *http.Request, p auth.Principal, row sqlc.GetLinkRequestRow) {
	existing, err := s.q.GetContractForLinkRequest(r.Context(), sqlc.GetContractForLinkRequestParams{
		OrgID: p.OrgID, LinkRequestID: row.ID,
	})
	if err != nil && !isNoRows(err) {
		s.serverError(w, r, "link.backfill.lookup", err)
		return
	}
	if err == nil && existing.Valid {
		conflictCode(w, "not_pending", "request already decided",
			"this request is already approved and its contract exists")
		return
	}

	var created sqlc.Contract
	var queuedID string
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		created, queuedID, _, err = s.onLinkApproved(r.Context(), q, linkRequestOf(row), p.UserIDString())
		return err
	}); err != nil {
		if writeCreateError(w, err) {
			return
		}
		s.serverError(w, r, "link.backfill.tx", err)
		return
	}
	s.enqueueNotifications(r.Context(), queuedID)

	reloaded, err := s.q.GetLinkRequest(r.Context(), sqlc.GetLinkRequestParams{ID: row.ID, OrgID: p.OrgID})
	if err != nil {
		s.serverError(w, r, "link.backfill.reload", err)
		return
	}
	lr := linkRowOfGet(reloaded)
	out := toLinkRequest(lr, true)
	out.SchedulePreview = previewFor(lr)
	body := map[string]any{"request": out}
	if contractOut, ok := s.reloadContract(w, r, created.ID, p.OrgID, pgtype.UUID{}); ok {
		body["contract"] = contractOut
	}
	WriteJSON(w, http.StatusOK, body)
}

// linkRequestOf narrows the joined read row to the table row the hook takes.
func linkRequestOf(row sqlc.GetLinkRequestRow) sqlc.UnitLinkRequest {
	return sqlc.UnitLinkRequest{
		ID: row.ID, OrgID: row.OrgID, UnitID: row.UnitID, RenterUserID: row.RenterUserID,
		Status: row.Status, PaymentPeriodID: row.PaymentPeriodID,
		TermDays: row.TermDays, StartDate: row.StartDate, EndDate: row.EndDate,
	}
}

// linkDecision carries what a decision SMS needs.
type linkDecision struct {
	OrgID        string
	RenterUserID string
	RequestID    string
	Kind         string
	Lang         string
	Unit         string
	Org          string
	Reason       string
	Phone        string
	Name         string
	Property     string
	Overrides    notify.Overrides
}

// queueLinkDecision writes the notification_log row inside the decision's own
// transaction and returns the id to push onto Redis after the commit. A
// duplicate dedupe key is not an error — it means the message already exists.
func (s *Server) queueLinkDecision(ctx context.Context, q *sqlc.Queries, d linkDecision) (string, error) {
	if d.Phone == "" {
		// Nothing to send to; the decision itself still stands.
		s.logger.Warn("link decision not notified: renter has no phone number",
			"request_id", d.RequestID, "kind", d.Kind)
		return "", nil
	}
	body := notify.Render(d.Kind, d.Lang, notify.Vars{
		Name: d.Name, Unit: d.Unit, Property: d.Property, Org: d.Org, Reason: d.Reason,
	}, d.Overrides)
	id, err := notify.Queue(ctx, q, notify.Msg{
		OrgID: d.OrgID, UserID: d.RenterUserID, Kind: d.Kind,
		DedupeKey: d.Kind + ":" + d.RequestID, Phone: d.Phone, Body: body,
	})
	if errors.Is(err, notify.ErrDuplicate) {
		return "", nil
	}
	return id, err
}

// enqueueNotifications pushes committed notification ids onto the Redis queue.
func (s *Server) enqueueNotifications(ctx context.Context, ids ...string) {
	notify.Enqueue(ctx, redisOf(s.deps.Cache), s.logger, ids...)
}

// conflictCode writes a 409 whose `type` carries the machine-readable code
// from API.md (`unit_occupied`, `duplicate_request`, …).
func conflictCode(w http.ResponseWriter, code, title, detail string) {
	httpx.WriteProblemCode(w, http.StatusConflict, code, title, detail)
}

// requiredDate parses a mandatory YYYY-MM-DD field.
func requiredDate(f validate.Fields, field, in string) time.Time {
	v := strings.TrimSpace(in)
	if v == "" {
		f.Add(field, field+" is required")
		return time.Time{}
	}
	t, err := time.Parse(dateLayout, v)
	if err != nil {
		f.Add(field, "must be a date (YYYY-MM-DD)")
		return time.Time{}
	}
	return t
}
