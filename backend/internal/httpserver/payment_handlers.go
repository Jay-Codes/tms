package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/notify"
	"tms/backend/internal/payment"
	"tms/backend/internal/validate"
)

// errContractNotPayable is a payment recorded against a contract that is not
// running: nothing is owed on a draft, an unsigned or a finished tenancy.
var errContractNotPayable = errors.New("payment: contract is not active")

func notFoundPayment(w http.ResponseWriter) {
	httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such payment")
}

// ------------------------------------------------------------ POST /payments --

// handleRecordPayment records money received outside the system and applies it
// to the contract's schedules (FLOWS 7.2).
//
// The allocation itself is pure (internal/payment.Allocate); this handler's job
// is to lock the contract's schedules, hand them to it, write what it decided,
// and turn its refusals into the three 409s API.md names.
func (s *Server) handleRecordPayment(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		ContractID           string  `json:"contract_id"`
		ScheduleID           string  `json:"schedule_id"`
		Amount               int64   `json:"amount"`
		Method               string  `json:"method"`
		Reference            *string `json:"reference"`
		PaidAt               string  `json:"paid_at"`
		Note                 *string `json:"note"`
		AllowOverpayRollover bool    `json:"allow_overpay_rollover"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}

	f := validate.Fields{}
	contractID := uuidField(f, "contract_id", body.ContractID, true)
	scheduleID := uuidField(f, "schedule_id", body.ScheduleID, false)
	checkAmount(f, "amount", body.Amount)
	method := strings.TrimSpace(body.Method)
	if !paymentMethods[method] {
		f.Add("method", "must be cash, bank_transfer or mobile_money_manual")
	}
	reference := trimmedOpt(f, "reference", body.Reference, paymentReferenceMax)
	note := trimmedOpt(f, "note", body.Note, paymentNoteMax)
	paidAt := time.Now().UTC()
	if v := strings.TrimSpace(body.PaidAt); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		switch {
		case err != nil:
			f.Add("paid_at", "must be a timestamp (RFC3339)")
		case t.After(time.Now().UTC().Add(paidAtFutureToleranceH * time.Hour)):
			f.Add("paid_at", "cannot be more than a day in the future")
		default:
			paidAt = t.UTC()
		}
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	contract, err := s.q.GetContract(r.Context(), sqlc.GetContractParams{ID: contractID, OrgID: p.OrgID})
	if isNoRows(err) {
		notFoundContract(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "payment.record.contract", err)
		return
	}
	org, err := s.q.GetOrg(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "payment.record.org", err)
		return
	}
	settings := parseSettings(org.Settings)
	brand := s.brandingAssets(r.Context(), p.OrgID, org.Name)

	var out allocationOutcome
	txErr := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		out, err = s.allocatePayment(r.Context(), q, allocationRequest{
			OrgID: p.OrgID, ActorUserID: p.UserID, Contract: contract,
			ScheduleID: scheduleID, Amount: body.Amount, Method: method,
			Reference: reference, Note: note, PaidAt: paidAt,
			AllowOverpayRollover: body.AllowOverpayRollover,
			Settings:             settings, OrgName: brand.DisplayName,
		})
		return err
	})
	if !s.allocationRefused(w, r, txErr, "payment.record.tx") {
		return
	}
	paymentID, affected := out.PaymentID, out.Affected
	s.enqueueNotifications(r.Context(), out.NotifyID)

	outPayment, ok := s.reloadPayment(w, r, paymentID, p.OrgID, pgtype.UUID{}, true)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{
		"payment":   outPayment,
		"schedules": affectedItems(affected, contractBlock(contract)),
	})
}

// ------------------------------------------------------- the allocator path --

// allocationRequest is one application of money to a contract's schedules.
//
// It exists so POST /payments and POST /proofs/{id}/accept share the movement
// rather than describing it twice: a proof the landlord believes becomes a
// payment by the same route the landlord's own entry takes, which is the whole
// reason the overpay confirm and the reversal flow work unchanged on it
// (PLAN2 §16.1).
type allocationRequest struct {
	OrgID       pgtype.UUID
	ActorUserID pgtype.UUID
	// Contract is already loaded and org-scoped by the caller; its status is
	// checked inside the transaction, where it cannot have moved since.
	Contract sqlc.GetContractRow
	// ScheduleID is the row the payer aimed at. Invalid means "the earliest
	// one that still owes something" (API.md Phase 5).
	ScheduleID           pgtype.UUID
	Amount               int64
	Method               string
	Reference            *string
	Note                 *string
	PaidAt               time.Time
	AllowOverpayRollover bool
	Settings             OrgSettings
	// OrgName is the branded display name the SMS says.
	OrgName string
}

// allocationOutcome is what the caller needs after the transaction commits.
type allocationOutcome struct {
	PaymentID string
	Affected  []sqlc.PaymentSchedule
	// NotifyID is the queued thank-you, empty when the kind is switched off,
	// deduplicated, or the renter has no phone.
	NotifyID string
}

// allocatePayment locks the contract's schedules, allocates, writes the
// payment, its allocations and the audit row, and queues the thank-you — all
// inside the caller's transaction.
//
// Its refusals are returned unchanged (errContractNotPayable,
// errScheduleNotFound, payment.ErrSchedulePaid, ErrExceedsContractBalance,
// *payment.OverpayError) so every caller answers with the same status and the
// same body; allocationRefused is the one place that mapping lives.
func (s *Server) allocatePayment(
	ctx context.Context, q *sqlc.Queries, req allocationRequest,
) (allocationOutcome, error) {
	var out allocationOutcome
	contract := req.Contract

	if contract.Status != contractActive && contract.Status != contractExpiring {
		return out, errContractNotPayable
	}
	rows, err := q.LockSchedulesForContract(ctx, sqlc.LockSchedulesForContractParams{
		OrgID: req.OrgID, ContractID: contract.ID,
	})
	if err != nil {
		return out, err
	}
	schedules := make([]payment.Schedule, 0, len(rows))
	for _, row := range rows {
		schedules = append(schedules, toAllocSchedule(row))
	}

	// The target is the row the payer named, or the earliest one that still
	// owes something (API.md).
	target := -1
	if req.ScheduleID.Valid {
		want := db.UUIDString(req.ScheduleID)
		for i, sc := range schedules {
			if sc.ID == want {
				target = i
				break
			}
		}
		if target < 0 {
			return out, errScheduleNotFound
		}
	} else {
		target = payment.EarliestUnpaid(schedules)
		if target < 0 {
			return out, payment.ErrExceedsContractBalance
		}
	}

	applied, err := payment.Allocate(
		req.Amount, schedules[target], schedules[target+1:], req.AllowOverpayRollover)
	if err != nil {
		return out, err
	}

	pay, err := q.CreatePayment(ctx, sqlc.CreatePaymentParams{
		OrgID: req.OrgID, ContractID: contract.ID,
		ScheduleID: db.MustUUID(schedules[target].ID), Amount: req.Amount,
		Method: req.Method, Reference: req.Reference, PaidAt: db.TS(req.PaidAt),
		RecordedByUserID: req.ActorUserID, Note: req.Note,
	})
	if err != nil {
		return out, err
	}
	out.PaymentID = db.UUIDString(pay.ID)

	for _, a := range applied {
		schedID := db.MustUUID(a.ScheduleID)
		if _, err := q.CreatePaymentAllocation(ctx, sqlc.CreatePaymentAllocationParams{
			OrgID: req.OrgID, PaymentID: pay.ID, ScheduleID: schedID, Amount: a.Amount,
		}); err != nil {
			return out, err
		}
		updated, err := q.ApplyPaymentToSchedule(ctx, sqlc.ApplyPaymentToScheduleParams{
			PaidAmount: a.NewPaid, OrgID: req.OrgID, ID: schedID,
		})
		if err != nil {
			return out, err
		}
		out.Affected = append(out.Affected, updated)
		// Keep the local view in step so the next-due lookup below sees the
		// state the transaction is committing.
		for i := range schedules {
			if schedules[i].ID == a.ScheduleID {
				schedules[i].PaidAmount = a.NewPaid
				schedules[i].Status = a.NewStatus
			}
		}
	}

	if err := audit.Record(ctx, q, audit.Entry{
		OrgID:       db.UUIDString(req.OrgID),
		ActorUserID: db.UUIDString(req.ActorUserID),
		Action:      audit.ActionPaymentRecord,
		EntityType:  audit.EntityPayment,
		EntityID:    out.PaymentID,
		After: map[string]any{
			"contract_id": db.UUIDString(contract.ID), "amount": req.Amount,
			"method": req.Method, "paid_at": req.PaidAt.Format(time.RFC3339),
			"applied": appliedAudit(applied), "rollover": req.AllowOverpayRollover,
		},
	}); err != nil {
		return out, err
	}

	// The thank-you names what comes next, or says everything is settled.
	vars := notify.Vars{
		Name: contract.RenterName, Amount: formatTZS(req.Amount),
		Unit: contract.UnitName, Property: contract.PropertyName, Org: req.OrgName,
	}
	if next := payment.EarliestUnpaid(schedules); next >= 0 {
		vars.NextDueDate = schedules[next].DueDate
		vars.NextAmount = formatTZS(schedules[next].Amount - schedules[next].PaidAmount)
	}
	out.NotifyID, err = s.queuePaymentSMS(ctx, q, paymentMessage{
		OrgID: db.UUIDString(req.OrgID), UserID: db.UUIDString(contract.RenterUserID),
		PaymentID: out.PaymentID, Lang: req.Settings.SMSLanguage,
		Phone: db.StrVal(contract.RenterPhone), Vars: vars,
		Overrides: req.Settings.notifyOverrides(),
		Enabled:   notificationSettingsOf(req.Settings).Kinds.ThankYou.Enabled,
	})
	return out, err
}

// allocationRefused turns allocatePayment's refusals into the statuses API.md
// names, and reports whether the caller may carry on. Every caller of the
// allocator answers through it, so the proof review sheet sees exactly the
// bodies the record-payment sheet was written against — the overpay confirm
// included (PLAN2 §16.1: "propagate unchanged").
func (s *Server) allocationRefused(
	w http.ResponseWriter, r *http.Request, txErr error, op string,
) bool {
	var overpay *payment.OverpayError
	switch {
	case txErr == nil:
		return true
	case errors.Is(txErr, errContractNotPayable):
		conflictCode(w, "contract_not_active", "contract not running",
			"payments can only be recorded against a running contract")
	case errors.Is(txErr, errScheduleNotFound):
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such schedule on this contract")
	case errors.Is(txErr, payment.ErrSchedulePaid):
		conflictCode(w, "schedule_paid", "schedule already settled",
			"that schedule owes nothing; pick another or omit schedule_id")
	case errors.Is(txErr, payment.ErrExceedsContractBalance):
		conflictCode(w, "exceeds_contract_balance", "more than the contract owes",
			"the payment is larger than everything still outstanding on this contract")
	case errors.As(txErr, &overpay):
		writeOverpayConfirm(w, overpay)
	default:
		s.serverError(w, r, op, txErr)
	}
	return false
}

// errScheduleNotFound is an explicit schedule_id that names no row of the
// contract — a 404, like any other id that is not the caller's.
var errScheduleNotFound = errors.New("payment: schedule not found on this contract")

// writeOverpayConfirm is the 409 that drives the UI's confirm prompt: how much
// is left over and where it would go (FLOWS 7.2).
func writeOverpayConfirm(w http.ResponseWriter, e *payment.OverpayError) {
	w.Header().Set("Content-Type", httpx.ProblemContentType)
	w.WriteHeader(http.StatusConflict)
	WriteRawJSON(w, map[string]any{
		"type":   "overpay_confirm_required",
		"title":  "payment is larger than the schedule",
		"status": http.StatusConflict,
		"detail": "the excess can be applied to the next schedule; resend with allow_overpay_rollover",
		"excess": e.Excess,
		"next_schedule": map[string]any{
			"id": e.Next.ID, "due_date": e.Next.DueDate,
			"amount": e.Next.Amount, "paid_amount": e.Next.PaidAmount,
			"status": e.Next.Status,
		},
	})
}

// ------------------------------------------- POST /payments/{id}/reverse --

// handleReversePayment undoes a recorded payment: the money comes back off
// every schedule it touched and the statuses are recomputed, overdue included
// (FLOWS 7.4). The payment row stays, marked `reversed` with its reason — a
// correction is part of the record, not a deletion.
func (s *Server) handleReversePayment(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundPayment(w)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	reason := f.MaxLen("reason", f.Required("reason", body.Reason), reversalReasonMax)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	row, err := s.q.GetPayment(r.Context(), sqlc.GetPaymentParams{ID: id, OrgID: p.OrgID})
	if isNoRows(err) {
		notFoundPayment(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "payment.reverse.get", err)
		return
	}
	if row.Status == paymentReversed {
		conflictCode(w, "already_reversed", "payment already reversed",
			"this payment has already been reversed")
		return
	}
	org, err := s.q.GetOrg(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "payment.reverse.org", err)
		return
	}
	grace := int32(parseSettings(org.Settings).GraceDays)

	var affected []sqlc.PaymentSchedule
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		allocations, err := q.ListAllocationsForPayments(r.Context(), sqlc.ListAllocationsForPaymentsParams{
			OrgID: p.OrgID, PaymentIds: []pgtype.UUID{id},
		})
		if err != nil {
			return err
		}
		reversed, err := q.ReversePayment(r.Context(), sqlc.ReversePaymentParams{
			ReversalReason: &reason, ReversedByUserID: p.UserID, OrgID: p.OrgID, ID: id,
		})
		if err != nil {
			return err
		}
		affected = affected[:0]
		for _, a := range allocations {
			updated, err := q.UnapplyPaymentFromSchedule(r.Context(), sqlc.UnapplyPaymentFromScheduleParams{
				Delta: a.Amount, GraceDays: grace, OrgID: p.OrgID, ID: a.ScheduleID,
			})
			if err != nil {
				return err
			}
			affected = append(affected, updated)
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionPaymentReverse,
			EntityType:  audit.EntityPayment,
			EntityID:    db.UUIDString(id),
			Before:      map[string]any{"status": row.Status, "amount": row.Amount},
			After: map[string]any{
				"status": reversed.Status, "reason": reason,
				"unapplied": allocationAudit(allocations),
			},
		})
	}); err != nil {
		if isNoRows(err) {
			conflictCode(w, "already_reversed", "payment already reversed",
				"this payment has already been reversed")
			return
		}
		s.serverError(w, r, "payment.reverse.tx", err)
		return
	}

	out, ok := s.reloadPayment(w, r, db.UUIDString(id), p.OrgID, pgtype.UUID{}, true)
	if !ok {
		return
	}
	contract, err := s.q.GetContract(r.Context(), sqlc.GetContractParams{ID: row.ContractID, OrgID: p.OrgID})
	var block *scheduleContract
	if err == nil {
		block = contractBlock(contract)
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"payment": out, "schedules": affectedItems(affected, block),
	})
}

// ------------------------------------------------- GET /payments, /{id} --

func (s *Server) handleListPayments(w http.ResponseWriter, r *http.Request) {
	s.listPayments(w, r, false)
}

func (s *Server) handleListMyPayments(w http.ResponseWriter, r *http.Request) {
	s.listPayments(w, r, true)
}

// listPayments serves both the landlord's history and the renter's own. The
// scope decides which side of the query is filled in: an org id for the
// landlord, the caller's user id for the renter — never neither.
func (s *Server) listPayments(w http.ResponseWriter, r *http.Request, renterScope bool) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	qs := r.URL.Query()
	f := validate.Fields{}
	page := parseListPage(r, f)

	params := sqlc.ListPaymentsParams{
		RowLimit: page.Limit, CursorAt: page.CursorAt, CursorID: page.CursorID,
		ContractID: optQueryUUID(f, "contract_id", qs.Get("contract_id")),
		PaidFrom:   optQueryTime(f, "from", qs.Get("from")),
		PaidTo:     optQueryTime(f, "to", qs.Get("to")),
	}
	if v := strings.TrimSpace(qs.Get("method")); v != "" {
		if !paymentMethods[v] {
			f.Add("method", "must be cash, bank_transfer or mobile_money_manual")
		}
		params.Method = &v
	}
	if renterScope {
		params.RenterUserID = p.UserID
	} else {
		params.OrgID = p.OrgID
		params.RenterUserID = optQueryUUID(f, "renter_user_id", qs.Get("renter_user_id"))
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	rows, err := s.q.ListPayments(r.Context(), params)
	if err != nil {
		s.serverError(w, r, "payment.list", err)
		return
	}
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	byPayment, err := s.allocationsFor(r.Context(), p.OrgID, ids, renterScope)
	if err != nil {
		s.serverError(w, r, "payment.list.allocations", err)
		return
	}

	items := make([]paymentResponse, 0, len(rows))
	for _, row := range rows {
		pr := paymentRowOfList(row)
		items = append(items, toPayment(pr, byPayment[db.UUIDString(row.ID)], !renterScope))
	}
	var next *string
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		next = nextCursor(len(rows), page.Limit, last.PaidAt.Time, db.UUIDString(last.ID))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) handleGetPayment(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id := chi.URLParam(r, "id")
	out, ok := s.reloadPayment(w, r, id, p.OrgID, pgtype.UUID{}, true)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"payment": out})
}

// ------------------------------------------------------- GET /schedules --

// handleListSchedules is the landlord's money board: every schedule of the org,
// filterable, with the contract identity attached so an overdue list reads as
// "who owes what on which unit".
//
// It runs the overdue sweep for its own org first, so a schedule that lapsed
// since the last tick is already `overdue` in the answer (API.md Phase 5).
func (s *Server) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	s.flipOverdueFor(r.Context(), p.OrgID)

	qs := r.URL.Query()
	f := validate.Fields{}
	page := parseSchedulePage(r, f)
	params := sqlc.ListSchedulesParams{
		OrgID: p.OrgID, RowLimit: page.Limit,
		CursorDue: page.CursorDue, CursorID: page.CursorID,
		ContractID:   optQueryUUID(f, "contract_id", qs.Get("contract_id")),
		RenterUserID: optQueryUUID(f, "renter_user_id", qs.Get("renter_user_id")),
		DueFrom:      optQueryDate(f, "due_from", qs.Get("due_from")),
		DueTo:        optQueryDate(f, "due_to", qs.Get("due_to")),
	}
	if v := strings.TrimSpace(qs.Get("status")); v != "" {
		if !scheduleStatuses[v] {
			f.Add("status", "must be pending, paid, partial, overdue or waived")
		}
		params.Status = &v
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	rows, err := s.q.ListSchedules(r.Context(), params)
	if err != nil {
		s.serverError(w, r, "schedules.list", err)
		return
	}
	today := time.Now().UTC()
	items := make([]scheduleItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, toScheduleItem(row, today))
	}
	var next *string
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		next = nextScheduleCursor(len(rows), page.Limit, last.DueDate.Time, db.UUIDString(last.ID))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// ---------------------------------------------------- /org/bank-account --

func (s *Server) handleGetBankAccount(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	org, err := s.q.GetOrg(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "org.bank_account.get", err)
		return
	}
	settings := parseSettings(org.Settings)
	account, wallet := payInstructions(settings)
	WriteJSON(w, http.StatusOK, map[string]any{
		"bank_account": account,
		// Phase 16 §16.4: the wallet beside the account, and the one flag the
		// landlord's setup nudge asks about — "can a renter be told where to
		// pay?" — so the dashboard does not have to infer it from two nulls.
		"mobile_money":             wallet,
		"payment_instructions_set": paymentInstructionsSet(settings),
	})
}

func (s *Server) handlePutBankAccount(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	var body struct {
		BankAccount
		// Phase 16 §16.4. The raw message distinguishes the three things a
		// client can say about the wallet: nothing (keep what is stored),
		// `null` (clear it), or an object (replace it). A plain pointer would
		// collapse the first two, and a landlord editing their bank details
		// would silently lose their mobile money.
		MobileMoney json.RawMessage `json:"mobile_money"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	acct := BankAccount{
		BankName:      f.MaxLen("bank_name", f.Required("bank_name", body.BankName), bankFieldMax),
		AccountName:   f.MaxLen("account_name", f.Required("account_name", body.AccountName), bankFieldMax),
		AccountNumber: f.MaxLen("account_number", f.Required("account_number", body.AccountNumber), bankFieldMax),
		Instructions:  f.MaxLen("instructions", strings.TrimSpace(body.Instructions), bankInstructionsMax),
	}
	var (
		wallet     *MobileMoney
		walletSent = len(body.MobileMoney) > 0
		walletNull = walletSent && string(body.MobileMoney) == "null"
	)
	if walletSent && !walletNull {
		var in MobileMoney
		if err := json.Unmarshal(body.MobileMoney, &in); err != nil {
			f.Add("mobile_money", "must be an object with provider, number and name")
		} else {
			wallet = validateMobileMoney(f, &in)
		}
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	org, err := s.q.GetOrg(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "org.bank_account.get", err)
		return
	}
	settings := parseSettings(org.Settings)
	before := map[string]any{"bank_account": settings.BankAccount, "mobile_money": settings.MobileMoney}
	settings.BankAccount = &acct
	if walletSent {
		settings.MobileMoney = wallet
	}
	raw, err := marshalSettings(settings)
	if err != nil {
		s.serverError(w, r, "org.bank_account.marshal", err)
		return
	}

	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.UpdateOrg(r.Context(), sqlc.UpdateOrgParams{ID: p.OrgID, Settings: raw}); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionBankAccountUpdate,
			EntityType:  audit.EntityOrg,
			EntityID:    p.OrgIDString(),
			Before:      before,
			After:       map[string]any{"bank_account": acct, "mobile_money": settings.MobileMoney},
		})
	}); err != nil {
		s.serverError(w, r, "org.bank_account.tx", err)
		return
	}
	account, saved := payInstructions(settings)
	WriteJSON(w, http.StatusOK, map[string]any{
		"bank_account": account, "mobile_money": saved,
		"payment_instructions_set": paymentInstructionsSet(settings),
	})
}

// ------------------------------------------- POST /admin/jobs/overdue --

// handleOverdueJob runs the overdue sweep platform-wide on demand, the way
// /admin/jobs/contract-lifecycle runs the contract sweep: a tester should not
// have to wait an hour to watch a schedule lapse.
func (s *Server) handleOverdueJob(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	flipped, err := payment.FlipOverdue(r.Context(), s.q, pgtype.UUID{})
	if err != nil {
		s.serverError(w, r, "admin.overdue", err)
		return
	}
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		return audit.Record(r.Context(), q, audit.Entry{
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionOverdueRun,
			EntityType:  audit.EntityPaymentSchedule,
			After:       map[string]any{"flipped": flipped},
		})
	}); err != nil {
		s.serverError(w, r, "admin.overdue.audit", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"flipped": flipped})
}

// --------------------------------------------------------------- helpers --

// flipOverdueFor runs the on-demand sweep for one org before a read. A failure
// is logged, not fatal: a stale `pending` is a worse answer than none at all,
// but it is not worth refusing the request over.
func (s *Server) flipOverdueFor(ctx context.Context, orgID pgtype.UUID) {
	if _, err := payment.FlipOverdue(ctx, s.q, orgID); err != nil {
		s.logger.Warn("on-demand overdue sweep failed", "org_id", db.UUIDString(orgID), "error", err)
	}
}

// reloadPayment re-reads a payment with its allocations for the response body.
func (s *Server) reloadPayment(
	w http.ResponseWriter, r *http.Request, id string, orgID, renterID pgtype.UUID, withActor bool,
) (paymentResponse, bool) {
	pid, err := db.ParseUUID(id)
	if err != nil {
		notFoundPayment(w)
		return paymentResponse{}, false
	}
	row, err := s.q.GetPayment(r.Context(), sqlc.GetPaymentParams{
		ID: pid, OrgID: orgID, RenterUserID: renterID,
	})
	if isNoRows(err) {
		notFoundPayment(w)
		return paymentResponse{}, false
	}
	if err != nil {
		s.serverError(w, r, "payment.get", err)
		return paymentResponse{}, false
	}
	byPayment, err := s.allocationsFor(r.Context(), row.OrgID, []pgtype.UUID{pid}, false)
	if err != nil {
		s.serverError(w, r, "payment.get.allocations", err)
		return paymentResponse{}, false
	}
	return toPayment(row, byPayment[id], withActor), true
}

// allocationsFor loads the `applied[]` rows for a set of payments. The renter's
// history spans every org they rent from, so it uses the cross-org variant —
// safe because the ids it is given came from a renter-scoped query.
func (s *Server) allocationsFor(
	ctx context.Context, orgID pgtype.UUID, ids []pgtype.UUID, crossOrg bool,
) (map[string][]appliedAlloc, error) {
	if len(ids) == 0 {
		return map[string][]appliedAlloc{}, nil
	}
	if crossOrg {
		rows, err := s.q.ListAllocationsForPaymentsAnyOrg(ctx, ids)
		if err != nil {
			return nil, err
		}
		converted := make([]sqlc.ListAllocationsForPaymentsRow, 0, len(rows))
		for _, a := range rows {
			converted = append(converted, sqlc.ListAllocationsForPaymentsRow(a))
		}
		return allocIndex(converted), nil
	}
	rows, err := s.q.ListAllocationsForPayments(ctx, sqlc.ListAllocationsForPaymentsParams{
		OrgID: orgID, PaymentIds: ids,
	})
	if err != nil {
		return nil, err
	}
	return allocIndex(rows), nil
}

// contractBlock builds the identity block the schedule shape carries.
func contractBlock(c sqlc.GetContractRow) *scheduleContract {
	return &scheduleContract{
		ID:           db.UUIDString(c.ID),
		UnitName:     c.UnitName,
		PropertyName: c.PropertyName,
		RenterName:   c.RenterName,
		RenterUserID: db.UUIDString(c.RenterUserID),
	}
}

func affectedItems(rows []sqlc.PaymentSchedule, block *scheduleContract) []scheduleItem {
	today := time.Now().UTC()
	out := make([]scheduleItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, scheduleItemOf(row, block, today))
	}
	return out
}

func appliedAudit(applied []payment.Alloc) []map[string]any {
	out := make([]map[string]any, 0, len(applied))
	for _, a := range applied {
		out = append(out, map[string]any{
			"schedule_id": a.ScheduleID, "amount": a.Amount, "status": a.NewStatus,
		})
	}
	return out
}

func allocationAudit(rows []sqlc.ListAllocationsForPaymentsRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, a := range rows {
		out = append(out, map[string]any{
			"schedule_id": db.UUIDString(a.ScheduleID), "amount": a.Amount,
		})
	}
	return out
}

// paymentMessage carries what a thank-you SMS needs across the tx boundary.
type paymentMessage struct {
	OrgID     string
	UserID    string
	PaymentID string
	// Lang is the org's default for renters without a preference; the
	// recipient's own `users.locale` wins over it (Phase 13).
	Lang      string
	Phone     string
	Vars      notify.Vars
	Overrides notify.Overrides
	// Enabled is the org's `kinds.thank_you.enabled` switch.
	Enabled bool
}

// queuePaymentSMS writes the thank-you row inside the caller's transaction and
// returns the id to push onto Redis after the commit.
func (s *Server) queuePaymentSMS(ctx context.Context, q *sqlc.Queries, m paymentMessage) (string, error) {
	// `kinds.thank_you.enabled` is a switch the landlord is offered on the
	// notifications screen; an event-driven kind has to read it too, or turning
	// it off would change the screen and nothing else.
	if !m.Enabled {
		return "", nil
	}
	if m.Phone == "" {
		s.logger.Warn("thank-you notification skipped: renter has no phone number",
			"payment_id", m.PaymentID)
		return "", nil
	}
	lang := s.recipientLang(ctx, q, m.UserID, m.Lang)
	id, err := notify.Queue(ctx, q, notify.Msg{
		OrgID: m.OrgID, UserID: m.UserID, Kind: notify.KindThankYou,
		DedupeKey: notify.KindThankYou + ":" + m.PaymentID, Phone: m.Phone,
		Body:     notify.Render(notify.KindThankYou, lang, m.Vars, m.Overrides),
		Language: lang,
	})
	if errors.Is(err, notify.ErrDuplicate) {
		return "", nil
	}
	return id, err
}

// trimmedOpt validates an optional, length-bounded free-text field.
func trimmedOpt(f validate.Fields, name string, in *string, maxLen int) *string {
	if in == nil {
		return nil
	}
	v := strings.TrimSpace(*in)
	if v == "" {
		return nil
	}
	return db.Str(f.MaxLen(name, v, maxLen))
}
