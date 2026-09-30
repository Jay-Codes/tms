package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/contract"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/validate"
)

// Phase 22 §22.5 — settling up when a tenancy ends, and the deposit ledger.
//
// The settlement is computed by contract.ComputeSettlement from the contract's
// own policy and applied here, inside the termination's transaction, before the
// existing waiver of unlived periods runs.

const methodDeposit = "deposit"

var (
	errPrepaidChoice = errors.New("settle: the landlord must choose refund or forfeit")
	errRefundMethod  = errors.New("settle: a refund needs a method")
)

type settleInput struct {
	Choice    string
	Method    string
	Reference *string
	Reason    string
	Actor     pgtype.UUID
}

func (s *Server) writeSettlementError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, errPrepaidChoice):
		httpx.WriteProblemCode(w, http.StatusUnprocessableEntity, "prepaid_choice_required",
			"refund or keep the prepaid rent?",
			"this contract leaves prepaid rent to you: send prepaid_action refund or forfeit")
	case errors.Is(err, errRefundMethod):
		f := validate.Fields{}
		f.Add("refund_method", "required to refund: cash, bank_transfer or mobile_money_manual")
		badRequest(w, f)
	default:
		return false
	}
	return true
}

// settlementRows reads the contract's rows in the shape the settlement wants.
func settlementRows(rows []sqlc.PaymentSchedule) []contract.SettlementRow {
	out := make([]contract.SettlementRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, contract.SettlementRow{
			ID: db.UUIDString(r.ID), PeriodStart: r.PeriodStart.Time, PeriodEnd: r.PeriodEnd.Time,
			Amount: r.Amount, Paid: r.PaidAmount, Status: r.Status,
		})
	}
	return out
}

func depositHeld(t sqlc.DepositTotalsRow) int64 {
	return t.Received - t.Deducted - t.Refunded - t.Applied
}

// takeBack removes up to `amount` of money from one period, newest allocation
// first, and says how much came off which payment.
func takeBack(ctx context.Context, q *sqlc.Queries, org pgtype.UUID, scheduleID pgtype.UUID, amount int64,
	into map[pgtype.UUID]int64) (int64, error) {
	allocs, err := q.ListLiveAllocationsForSchedule(ctx, sqlc.ListLiveAllocationsForScheduleParams{
		OrgID: org, ScheduleID: scheduleID,
	})
	if err != nil {
		return 0, err
	}
	var taken int64
	for _, a := range allocs {
		if taken >= amount {
			break
		}
		take := min(a.Amount, amount-taken)
		if take == a.Amount {
			err = q.DeletePaymentAllocation(ctx, sqlc.DeletePaymentAllocationParams{OrgID: org, ID: a.ID})
		} else {
			err = q.ShrinkPaymentAllocation(ctx, sqlc.ShrinkPaymentAllocationParams{
				Amount: a.Amount - take, OrgID: org, ID: a.ID,
			})
		}
		if err != nil {
			return 0, err
		}
		into[a.PaymentID] += take
		taken += take
	}
	return taken, nil
}

// applySettlementTx settles a running contract ending on `effective`.
func (s *Server) applySettlementTx(
	ctx context.Context, q *sqlc.Queries, row sqlc.GetContractRow, effective time.Time, graceDays int,
	in settleInput,
) (contract.Settlement, error) {
	org := row.OrgID
	rows, err := q.LockSchedulesForContract(ctx, sqlc.LockSchedulesForContractParams{OrgID: org, ContractID: row.ID})
	if err != nil {
		return contract.Settlement{}, err
	}
	totals, err := q.DepositTotals(ctx, sqlc.DepositTotalsParams{OrgID: org, ContractID: row.ID})
	if err != nil {
		return contract.Settlement{}, err
	}
	policy := parsedPolicy(row.Policy)
	st := contract.ComputeSettlement(policy, effective, settlementRows(rows), depositHeld(totals), in.Choice)
	if st.Prepaid > 0 && st.PrepaidAction == "" {
		return st, errPrepaidChoice
	}
	if st.Refund > 0 && in.Method == "" {
		return st, errRefundMethod
	}
	if st.Refund > 0 && !paymentMethods[in.Method] {
		return st, errRefundMethod
	}

	eff := effective.UTC().Truncate(24 * time.Hour)
	refundFrom := map[pgtype.UUID]int64{}
	for _, r := range rows {
		if r.Status == "waived" || r.Status == "written_off" {
			continue
		}
		switch {
		case st.Straddle != nil && db.UUIDString(r.ID) == st.Straddle.ScheduleID:
			if st.Straddle.Charged != r.Amount {
				if _, err := q.SetScheduleAmount(ctx, sqlc.SetScheduleAmountParams{
					Amount: st.Straddle.Charged, GraceDays: int32(graceDays), OrgID: org, ID: r.ID,
				}); err != nil {
					return st, err
				}
			}
			if excess := r.PaidAmount - st.Straddle.Charged; excess > 0 && st.PrepaidAction == contract.PrepaidRefund {
				taken, err := takeBack(ctx, q, org, r.ID, excess, refundFrom)
				if err != nil {
					return st, err
				}
				if err := q.SetSchedulePaid(ctx, sqlc.SetSchedulePaidParams{
					PaidAmount: r.PaidAmount - taken, OrgID: org, ID: r.ID,
				}); err != nil {
					return st, err
				}
			}
		case r.PeriodStart.Time.After(eff) && r.PaidAmount > 0:
			left := r.PaidAmount
			if st.PrepaidAction == contract.PrepaidRefund {
				taken, err := takeBack(ctx, q, org, r.ID, r.PaidAmount, refundFrom)
				if err != nil {
					return st, err
				}
				left -= taken
			}
			// Paid for time never lived: not owed any more either way. Money
			// kept (forfeit) stays on the row; refunded money has left it.
			if err := q.WaiveScheduleFrom(ctx, sqlc.WaiveScheduleFromParams{
				PaidAmount: left, OrgID: org, ID: r.ID,
			}); err != nil {
				return st, err
			}
		}
	}

	if st.Refund > 0 {
		var total int64
		for _, v := range refundFrom {
			total += v
		}
		refund, err := q.CreateRentRefund(ctx, sqlc.CreateRentRefundParams{
			OrgID: org, ContractID: row.ID, Amount: total, Method: in.Method, Reference: in.Reference,
			Reason:     "Prepaid rent refunded on ending the tenancy: " + in.Reason,
			RefundedAt: db.TS(time.Now()), RecordedByUserID: in.Actor,
		})
		if err != nil {
			return st, err
		}
		for paymentID, amount := range refundFrom {
			if err := q.AddRentRefundItem(ctx, sqlc.AddRentRefundItemParams{
				RefundID: refund.ID, OrgID: org, PaymentID: paymentID, Amount: amount,
			}); err != nil {
				return st, err
			}
		}
		st.Refund = total
	}

	raw, _ := json.Marshal(st)
	if err := q.SetContractSettlement(ctx, sqlc.SetContractSettlementParams{Settlement: raw, OrgID: org, ID: row.ID}); err != nil {
		return st, err
	}
	return st, audit.Record(ctx, q, audit.Entry{
		OrgID:       db.UUIDString(org),
		ActorUserID: db.UUIDString(in.Actor),
		Action:      audit.ActionContractSettle,
		EntityType:  audit.EntityContract,
		EntityID:    db.UUIDString(row.ID),
		After:       map[string]any{"settlement": st},
	})
}

// ------------------------------------ GET /contracts/{id}/settlement --

// handleSettlementPreview is what terminating on `effective_date` would do,
// without doing it: the termination sheet shows it before the landlord confirms.
func (s *Server) handleSettlementPreview(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	row, ok := s.loadContract(w, r)
	if !ok {
		return
	}
	f := validate.Fields{}
	qs := r.URL.Query()
	effective := time.Now().UTC().Truncate(24 * time.Hour)
	if v := strings.TrimSpace(qs.Get("effective_date")); v != "" {
		effective = requiredDate(f, "effective_date", v)
	}
	choice := strings.TrimSpace(qs.Get("prepaid_action"))
	if choice != "" {
		f.OneOf("prepaid_action", choice, contract.PrepaidRefund, contract.PrepaidForfeit)
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	rows, err := s.q.ListSchedulesForContract(r.Context(), sqlc.ListSchedulesForContractParams{
		OrgID: row.OrgID, ContractID: row.ID,
	})
	if err != nil {
		s.serverError(w, r, "contract.settlement.schedules", err)
		return
	}
	totals, err := s.q.DepositTotals(r.Context(), sqlc.DepositTotalsParams{OrgID: row.OrgID, ContractID: row.ID})
	if err != nil {
		s.serverError(w, r, "contract.settlement.deposit", err)
		return
	}
	st := contract.ComputeSettlement(parsedPolicy(row.Policy), effective, settlementRows(rows), depositHeld(totals), choice)
	WriteJSON(w, http.StatusOK, map[string]any{
		"settlement": st, "policy": parsedPolicy(row.Policy),
		"needs_choice": st.Prepaid > 0 && st.PrepaidAction == "",
	})
}

// ------------------------------------------ /contracts/{id}/deposit --

type depositView struct {
	Required *int64 `json:"required"`
	Received int64  `json:"received"`
	Held     int64  `json:"held"`
	// OwedBeyondDeposit is deductions above what was held, when the policy
	// lets deductions exceed the deposit: still for the renter to pay.
	OwedBeyondDeposit int64          `json:"owed_beyond_deposit"`
	Entries           []depositEntry `json:"entries"`
	RentRefunds       []rentRefund   `json:"rent_refunds"`
}

type depositEntry struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Amount     int64     `json:"amount"`
	Method     *string   `json:"method"`
	Reference  *string   `json:"reference"`
	Reason     *string   `json:"reason"`
	PaymentID  *string   `json:"payment_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

type rentRefund struct {
	ID         string    `json:"id"`
	Amount     int64     `json:"amount"`
	Method     string    `json:"method"`
	Reference  *string   `json:"reference"`
	Reason     string    `json:"reason"`
	RefundedAt time.Time `json:"refunded_at"`
}

func (s *Server) depositOf(ctx context.Context, row sqlc.GetContractRow) (depositView, error) {
	out := depositView{Entries: []depositEntry{}, RentRefunds: []rentRefund{}}
	if pol := parsedPolicy(row.Policy); pol != nil && pol.DepositMode != contract.DepositNone {
		v := pol.DepositAmount
		out.Required = &v
	}
	totals, err := s.q.DepositTotals(ctx, sqlc.DepositTotalsParams{OrgID: row.OrgID, ContractID: row.ID})
	if err != nil {
		return out, err
	}
	out.Received = totals.Received
	out.Held = depositHeld(totals)
	if out.Held < 0 {
		out.OwedBeyondDeposit, out.Held = -out.Held, 0
	}
	entries, err := s.q.ListDepositEntries(ctx, sqlc.ListDepositEntriesParams{OrgID: row.OrgID, ContractID: row.ID})
	if err != nil {
		return out, err
	}
	for _, e := range entries {
		out.Entries = append(out.Entries, depositEntry{
			ID: db.UUIDString(e.ID), Kind: e.Kind, Amount: e.Amount, Method: e.Method,
			Reference: e.Reference, Reason: e.Reason, PaymentID: optUUIDString(e.PaymentID),
			OccurredAt: e.OccurredAt.Time,
		})
	}
	refunds, err := s.q.ListRentRefunds(ctx, sqlc.ListRentRefundsParams{OrgID: row.OrgID, ContractID: row.ID})
	if err != nil {
		return out, err
	}
	for _, rf := range refunds {
		out.RentRefunds = append(out.RentRefunds, rentRefund{
			ID: db.UUIDString(rf.ID), Amount: rf.Amount, Method: rf.Method, Reference: rf.Reference,
			Reason: rf.Reason, RefundedAt: rf.RefundedAt.Time,
		})
	}
	return out, nil
}

func (s *Server) handleGetDeposit(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	row, ok := s.loadContract(w, r)
	if !ok {
		return
	}
	out, err := s.depositOf(r.Context(), row)
	if err != nil {
		s.serverError(w, r, "deposit.get", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"deposit": out})
}

// handlePostDeposit records one deposit movement: received, deduction (with a
// reason), refund, or applied_to_rent — which becomes an ordinary rent payment
// (method `deposit`) through the same allocator as every other payment.
func (s *Server) handlePostDeposit(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.loadActionableContract(w, r)
	if !ok {
		return
	}
	var body struct {
		Kind       string  `json:"kind"`
		Amount     int64   `json:"amount"`
		Method     string  `json:"method"`
		Reference  *string `json:"reference"`
		Reason     *string `json:"reason"`
		OccurredAt string  `json:"occurred_at"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	kind := f.OneOf("kind", strings.TrimSpace(body.Kind), "received", "deduction", "refund", "applied_to_rent")
	if body.Amount < 1 {
		f.Add("amount", "must be above zero")
	}
	var method *string
	if kind == "received" || kind == "refund" {
		m := strings.TrimSpace(body.Method)
		if !paymentMethods[m] {
			f.Add("method", "must be cash, bank_transfer or mobile_money_manual")
		}
		method = &m
	}
	reference := trimmedOpt(f, "reference", body.Reference, paymentReferenceMax)
	reason := trimmedOpt(f, "reason", body.Reason, 200)
	if kind == "deduction" && (reason == nil || *reason == "") {
		f.Add("reason", "a deduction needs a reason")
	}
	occurred := time.Now()
	if v := strings.TrimSpace(body.OccurredAt); v != "" {
		occurred = requiredDate(f, "occurred_at", v)
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	if row.Status == contractPendingSignature || row.Status == contractDraft {
		conflictCode(w, "contract_not_signed", "contract not signed",
			"a deposit is recorded once the tenancy is signed")
		return
	}
	totals, err := s.q.DepositTotals(r.Context(), sqlc.DepositTotalsParams{OrgID: row.OrgID, ContractID: row.ID})
	if err != nil {
		s.serverError(w, r, "deposit.totals", err)
		return
	}
	held := depositHeld(totals)
	pol := parsedPolicy(row.Policy)
	switch kind {
	case "refund", "applied_to_rent":
		if body.Amount > held {
			httpx.WriteProblemExtra(w, http.StatusUnprocessableEntity, "exceeds_deposit_held",
				"more than the deposit held", "the deposit holds less than that",
				map[string]any{"held": max(held, 0)})
			return
		}
	case "deduction":
		if body.Amount > held && (pol == nil || !pol.DeductionsMayExceedDeposit) {
			httpx.WriteProblemExtra(w, http.StatusUnprocessableEntity, "exceeds_deposit_held",
				"more than the deposit held",
				"this contract does not let deductions exceed the deposit",
				map[string]any{"held": max(held, 0)})
			return
		}
	}

	org, err := s.q.GetOrg(r.Context(), row.OrgID)
	if err != nil {
		s.serverError(w, r, "deposit.org", err)
		return
	}
	settings := parseSettings(org.Settings)
	brand := s.brandingAssets(r.Context(), row.OrgID, org.Name)
	var notifyID string
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var paymentID pgtype.UUID
		if kind == "applied_to_rent" {
			note := "Applied from the deposit"
			out, err := s.allocatePayment(r.Context(), q, allocationRequest{
				OrgID: row.OrgID, ActorUserID: p.UserID, Contract: row, Amount: body.Amount,
				Method: methodDeposit, Reference: reference, Note: &note, PaidAt: occurred,
				Settings: settings, OrgName: brand.DisplayName,
			})
			if err != nil {
				return err
			}
			paymentID = db.MustUUID(out.PaymentID)
			notifyID = out.NotifyID
		}
		entry, err := q.CreateDepositEntry(r.Context(), sqlc.CreateDepositEntryParams{
			OrgID: row.OrgID, ContractID: row.ID, Kind: kind, Amount: body.Amount, Method: method,
			Reference: reference, Reason: reason, PaymentID: paymentID, OccurredAt: db.TS(occurred),
			RecordedByUserID: p.UserID,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionDepositRecord,
			EntityType:  audit.EntityContract,
			EntityID:    db.UUIDString(row.ID),
			After: map[string]any{
				"entry_id": db.UUIDString(entry.ID), "kind": kind, "amount": body.Amount, "reason": reason,
			},
		})
	}); err != nil {
		if !s.allocationRefused(w, r, err, "deposit.tx") {
			return
		}
		return
	}
	s.enqueueNotifications(r.Context(), notifyID)
	out, err := s.depositOf(r.Context(), row)
	if err != nil {
		s.serverError(w, r, "deposit.reload", err)
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"deposit": out})
}
