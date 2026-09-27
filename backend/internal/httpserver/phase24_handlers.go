package httpserver

import (
	"context"
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
	"tms/backend/internal/validate"
)

// Phase 24 — correcting payments, and telling people.
//
// A payment is never edited: a correction is a reversal plus a new payment in
// one transaction, the new one naming the old. Recording refuses a likely
// duplicate unless the landlord confirms it, and an Idempotency-Key header
// makes a double-tap replay the first answer. The renter is texted about any
// reversal or correction; the landlord's side is told through the in-app
// inbox (the bell), the free channel to landlords.

const idempotencyKeyMax = 80

// ------------------------------------------------------------- inbox --

type inboxItem struct {
	Kind, Title, Body string
	EntityType        string
	EntityID          pgtype.UUID
	Link              string
}

// inbox writes one landlord notice inside the caller's transaction.
func (s *Server) inbox(ctx context.Context, q *sqlc.Queries, org, actor pgtype.UUID, it inboxItem) error {
	var et, link *string
	if it.EntityType != "" {
		et = &it.EntityType
	}
	if it.Link != "" {
		link = &it.Link
	}
	_, err := q.CreateInboxItem(ctx, sqlc.CreateInboxItemParams{
		OrgID: org, Kind: it.Kind, Title: truncate(it.Title, 160), Body: truncate(it.Body, 500),
		EntityType: et, EntityID: it.EntityID, Link: link, ActorUserID: actor,
	})
	return err
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func (s *Server) handleListInbox(w http.ResponseWriter, r *http.Request) {
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
	rows, err := s.q.ListInbox(r.Context(), sqlc.ListInboxParams{
		UserID: p.UserID, OrgID: p.OrgID, CursorAt: page.CursorAt, CursorID: page.CursorID, RowLimit: page.Limit,
	})
	if err != nil {
		s.serverError(w, r, "inbox.list", err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, n := range rows {
		items = append(items, map[string]any{
			"id": db.UUIDString(n.ID), "kind": n.Kind, "title": n.Title, "body": n.Body,
			"entity_type": n.EntityType, "entity_id": optUUIDString(n.EntityID), "link": n.Link,
			"read": n.Read, "created_at": n.CreatedAt.Time,
		})
	}
	var next *string
	if k := len(rows); k > 0 {
		next = nextCursor(k, page.Limit, rows[k-1].CreatedAt.Time, db.UUIDString(rows[k-1].ID))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) handleInboxUnread(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	n, err := s.q.CountUnreadInbox(r.Context(), sqlc.CountUnreadInboxParams{OrgID: p.OrgID, UserID: p.UserID})
	if err != nil {
		s.serverError(w, r, "inbox.unread", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"unread": n})
}

func (s *Server) handleInboxRead(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	var body struct {
		IDs []string `json:"ids"`
		All bool     `json:"all"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	ids := make([]pgtype.UUID, 0, len(body.IDs))
	for _, raw := range body.IDs {
		id, err := db.ParseUUID(raw)
		if err != nil {
			f.Add("ids", "must be notice ids")
			break
		}
		ids = append(ids, id)
	}
	if !body.All && len(ids) == 0 {
		f.Add("ids", "name the notices, or send all: true")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	if err := s.q.MarkInboxRead(r.Context(), sqlc.MarkInboxReadParams{
		UserID: p.UserID, OrgID: p.OrgID, AllIds: body.All, Ids: ids,
	}); err != nil {
		s.serverError(w, r, "inbox.read", err)
		return
	}
	s.handleInboxUnread(w, r)
}

// ------------------------------------------------- duplicates, replay --

type duplicateMatch struct {
	ID        string    `json:"id"`
	Amount    int64     `json:"amount"`
	PaidAt    time.Time `json:"paid_at"`
	Method    string    `json:"method"`
	Reference *string   `json:"reference"`
}

// possibleDuplicates: the same contract and amount within three days, or the
// same reference in the org within 90 days.
func (s *Server) possibleDuplicates(ctx context.Context, org, contractID pgtype.UUID, amount int64,
	paidAt time.Time, reference *string, except pgtype.UUID) ([]duplicateMatch, error) {
	rows, err := s.q.PossibleDuplicates(ctx, sqlc.PossibleDuplicatesParams{
		OrgID: org, ContractID: contractID, Amount: amount, PaidAt: db.TS(paidAt), Reference: reference,
	})
	if err != nil {
		return nil, err
	}
	out := []duplicateMatch{}
	for _, m := range rows {
		if except.Valid && m.ID == except {
			continue
		}
		out = append(out, duplicateMatch{
			ID: db.UUIDString(m.ID), Amount: m.Amount, PaidAt: m.PaidAt.Time, Method: m.Method, Reference: m.Reference,
		})
	}
	return out, nil
}

func writeDuplicate(w http.ResponseWriter, matches []duplicateMatch) {
	httpx.WriteProblemExtra(w, http.StatusConflict, "possible_duplicate", "this looks like a payment already recorded",
		"a payment with the same amount on this contract, or the same reference, is already on the books; "+
			"send confirm_duplicate: true to record it anyway",
		map[string]any{"matches": matches})
}

// idempotencyKey reads the Idempotency-Key header (≤80 characters).
func idempotencyKey(r *http.Request, f validate.Fields) *string {
	k := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if k == "" {
		return nil
	}
	if len(k) > idempotencyKeyMax {
		f.Add("Idempotency-Key", "at most 80 characters")
		return nil
	}
	return &k
}

// ---------------------------------------------------- reversal, shared --

// reversible refuses what cannot be reversed or corrected; ok=false means
// the response has been written.
func (s *Server) reversible(w http.ResponseWriter, r *http.Request, row sqlc.GetPaymentRow) bool {
	if row.Status == paymentReversed {
		conflictCode(w, "already_reversed", "payment already reversed",
			"this payment has already been reversed")
		return false
	}
	// §22.5: money that came from the deposit is undone from the deposit
	// ledger, and money partly refunded cannot be reversed as if all held.
	if row.Method == methodDeposit {
		conflictCode(w, "deposit_payment", "paid from the deposit",
			"this payment was applied from the deposit; it cannot be reversed here")
		return false
	}
	refunded, err := s.q.PaymentRefunded(r.Context(), sqlc.PaymentRefundedParams{OrgID: row.OrgID, PaymentID: row.ID})
	if err != nil {
		s.serverError(w, r, "payment.reverse.refunded", err)
		return false
	}
	if refunded {
		conflictCode(w, "payment_refunded", "payment partly refunded",
			"part of this payment was refunded when the tenancy ended; it cannot be reversed")
		return false
	}
	return true
}

// reverseTx takes a payment's money back off every period it reached.
func (s *Server) reverseTx(ctx context.Context, q *sqlc.Queries, p auth.Principal, row sqlc.GetPaymentRow,
	reason string, grace int32) ([]sqlc.PaymentSchedule, error) {
	allocations, err := q.ListAllocationsForPayments(ctx, sqlc.ListAllocationsForPaymentsParams{
		OrgID: p.OrgID, PaymentIds: []pgtype.UUID{row.ID},
	})
	if err != nil {
		return nil, err
	}
	reversed, err := q.ReversePayment(ctx, sqlc.ReversePaymentParams{
		ReversalReason: &reason, ReversedByUserID: p.UserID, OrgID: p.OrgID, ID: row.ID,
	})
	if err != nil {
		return nil, err
	}
	var affected []sqlc.PaymentSchedule
	for _, a := range allocations {
		updated, err := q.UnapplyPaymentFromSchedule(ctx, sqlc.UnapplyPaymentFromScheduleParams{
			Delta: a.Amount, GraceDays: grace, OrgID: p.OrgID, ID: a.ScheduleID,
		})
		if err != nil {
			return nil, err
		}
		affected = append(affected, updated)
	}
	return affected, audit.Record(ctx, q, audit.Entry{
		OrgID:       p.OrgIDString(),
		ActorUserID: p.UserIDString(),
		Action:      audit.ActionPaymentReverse,
		EntityType:  audit.EntityPayment,
		EntityID:    db.UUIDString(row.ID),
		Before:      map[string]any{"status": row.Status, "amount": row.Amount},
		After: map[string]any{
			"status": reversed.Status, "reason": reason, "unapplied": allocationAudit(allocations),
		},
	})
}

// afterReversal tells the renter (SMS) and the org (inbox). A correction
// passes the new amount; a plain reversal passes nil.
func (s *Server) afterReversal(ctx context.Context, q *sqlc.Queries, p auth.Principal, row sqlc.GetPaymentRow,
	reason string, correctedTo *int64) (string, error) {
	contract, err := q.GetContract(ctx, sqlc.GetContractParams{ID: row.ContractID, OrgID: p.OrgID})
	if err != nil {
		return "", err
	}
	kind := notify.KindPaymentReversed
	vars := notify.Vars{Amount: formatTZS(row.Amount), Date: row.PaidAt.Time.Format(dateLayout), Reason: reason}
	title := "Payment reversed: " + formatTZS(row.Amount) + " — " + contract.RenterName
	if correctedTo != nil {
		kind = notify.KindPaymentCorrected
		vars.NextAmount = formatTZS(*correctedTo)
		title = "Payment corrected: " + formatTZS(row.Amount) + " → " + formatTZS(*correctedTo) + " — " + contract.RenterName
	}
	msg, err := s.contractSMS(ctx, contract, kind, vars)
	if err != nil {
		return "", err
	}
	if err := s.inbox(ctx, q, p.OrgID, p.UserID, inboxItem{
		Kind: kind, Title: title, Body: contract.UnitName + " · " + contract.PropertyName + ". " + reason,
		EntityType: "payment", EntityID: row.ID, Link: "/contracts/" + db.UUIDString(row.ContractID),
	}); err != nil {
		return "", err
	}
	return s.queueKeyedSMS(ctx, q, msg, db.UUIDString(row.ID))
}

// -------------------------------------------- POST /payments/{id}/correct --

// handleCorrectPayment reverses a payment and records the corrected one in a
// single transaction: wrong amount, wrong tenant/contract, wrong date, method
// or reference. Anything not sent is taken from the original.
func (s *Server) handleCorrectPayment(w http.ResponseWriter, r *http.Request) {
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
		Reason               string  `json:"reason"`
		Amount               *int64  `json:"amount"`
		ContractID           string  `json:"contract_id"`
		ScheduleID           string  `json:"schedule_id"`
		PaidAt               string  `json:"paid_at"`
		Method               string  `json:"method"`
		Reference            *string `json:"reference"`
		AllowOverpayRollover bool    `json:"allow_overpay_rollover"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	reason := f.MaxLen("reason", f.Required("reason", strings.TrimSpace(body.Reason)), reversalReasonMax)
	row, err := s.q.GetPayment(r.Context(), sqlc.GetPaymentParams{ID: id, OrgID: p.OrgID})
	if isNoRows(err) {
		notFoundPayment(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "payment.correct.get", err)
		return
	}
	amount := row.Amount
	if body.Amount != nil {
		checkAmount(f, "amount", *body.Amount)
		amount = *body.Amount
	}
	contractID := row.ContractID
	if v := uuidField(f, "contract_id", body.ContractID, false); v.Valid {
		contractID = v
	}
	scheduleID := uuidField(f, "schedule_id", body.ScheduleID, false)
	method := row.Method
	if m := strings.TrimSpace(body.Method); m != "" {
		if !paymentMethods[m] {
			f.Add("method", "must be cash, bank_transfer or mobile_money_manual")
		}
		method = m
	}
	reference := row.Reference
	if body.Reference != nil {
		reference = trimmedOpt(f, "reference", body.Reference, paymentReferenceMax)
	}
	paidAt := row.PaidAt.Time
	if v := strings.TrimSpace(body.PaidAt); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			f.Add("paid_at", "must be a timestamp (RFC3339)")
		} else {
			paidAt = t.UTC()
		}
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	if !s.reversible(w, r, row) {
		return
	}
	contract, err := s.q.GetContract(r.Context(), sqlc.GetContractParams{ID: contractID, OrgID: p.OrgID})
	if isNoRows(err) {
		notFoundContract(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "payment.correct.contract", err)
		return
	}
	org, err := s.q.GetOrg(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "payment.correct.org", err)
		return
	}
	settings := parseSettings(org.Settings)
	brand := s.brandingAssets(r.Context(), p.OrgID, org.Name)
	note := "Correction of an earlier payment: " + reason

	var out allocationOutcome
	var notifyIDs []string
	txErr := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := s.reverseTx(r.Context(), q, p, row, "Corrected: "+reason, int32(settings.GraceDays)); err != nil {
			return err
		}
		var err error
		out, err = s.allocatePayment(r.Context(), q, allocationRequest{
			OrgID: p.OrgID, ActorUserID: p.UserID, Contract: contract, ScheduleID: scheduleID,
			Amount: amount, Method: method, Reference: reference, Note: &note, PaidAt: paidAt,
			AllowOverpayRollover: body.AllowOverpayRollover, Settings: settings, OrgName: brand.DisplayName,
		})
		if err != nil {
			return err
		}
		if err := q.SetPaymentExtras(r.Context(), sqlc.SetPaymentExtrasParams{
			CorrectsPaymentID: row.ID, OrgID: p.OrgID, ID: db.MustUUID(out.PaymentID),
		}); err != nil {
			return err
		}
		if err := audit.Record(r.Context(), q, audit.Entry{
			OrgID: p.OrgIDString(), ActorUserID: p.UserIDString(), Action: audit.ActionPaymentCorrect,
			EntityType: audit.EntityPayment, EntityID: db.UUIDString(row.ID),
			Before: map[string]any{"amount": row.Amount, "contract_id": db.UUIDString(row.ContractID),
				"paid_at": row.PaidAt.Time, "method": row.Method, "reference": row.Reference},
			After: map[string]any{"new_payment_id": out.PaymentID, "amount": amount,
				"contract_id": db.UUIDString(contractID), "paid_at": paidAt, "method": method,
				"reference": reference, "reason": reason},
		}); err != nil {
			return err
		}
		// The renter of the original payment hears about the correction;
		// the thank-you the allocator queued covers the new one.
		id, err := s.afterReversal(r.Context(), q, p, row, reason, &amount)
		notifyIDs = append(notifyIDs, id, out.NotifyID)
		return err
	})
	if !s.allocationRefused(w, r, txErr, "payment.correct.tx") {
		return
	}
	s.enqueueNotifications(r.Context(), notifyIDs...)
	newPayment, ok := s.reloadPayment(w, r, out.PaymentID, p.OrgID, pgtype.UUID{}, true)
	if !ok {
		return
	}
	old, ok := s.reloadPayment(w, r, db.UUIDString(row.ID), p.OrgID, pgtype.UUID{}, true)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"payment": newPayment, "reversed": old})
}
