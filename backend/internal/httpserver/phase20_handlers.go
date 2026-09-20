// Phase 20 §20.3 — settling a tenancy that predates TMS.
//
// The decision this implements (PLAN2, DECISIONS): such a tenancy is represented
// **truthfully**. The contract's `start_date` is the real move-in date, the
// generator produces every past period, and the landlord then settles those
// periods with real backdated payments or marks them waived. There is no
// ledger-only "historical" row, because a rent book that cannot say whether a
// period was ever paid is not a rent book.
//
// One call closes the lot: N periods is one decision, so it is one audit row for
// the decision, one `payment.record` row per period (the ledger has to keep its
// grain), and exactly one SMS.
package httpserver

import (
	"context"
	"encoding/csv"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/notify"
	"tms/backend/internal/payment"
	"tms/backend/internal/report"
	"tms/backend/internal/validate"
)

// Payment sources (migration 000021). `manual` is the default the column
// carries, so only the two exceptions are ever written explicitly.
const (
	sourceManual   = "manual"
	sourceImport   = "import"
	sourceBackfill = "backfill"
)

// paymentSources are the values `GET /payments?source=` accepts.
//
//nolint:gochecknoglobals // fixed value set, read-only.
var paymentSources = map[string]bool{
	sourceManual: true, sourceImport: true, sourceBackfill: true,
}

// Backfill modes and bounds (API.md Phase 20).
const (
	backfillModePaid   = "paid"
	backfillModeWaived = "waived"
	// paidAtDueDate is the `paid_at` value that says "use each row's own due
	// date" rather than one date for every period — the honest default, since
	// that is when the money was actually owed and, for a tenancy being
	// reconstructed, roughly when it was handed over.
	paidAtDueDate = "due_date"
	// contractStartBackstopDays is how far into the past a landlord-created
	// contract may start: ten years. A renter's own application keeps the
	// 7-day backstop in link_handlers (startBackstopDays) — a renter must not
	// be able to draft a back-dated tenancy alone.
	contractStartBackstopDays = 3650
)

// ------------------------------------------ POST /contracts/{id}/backfill --

func (s *Server) handleContractBackfill(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	contract, ok := s.loadContract(w, r)
	if !ok {
		return
	}

	var body struct {
		Until     string  `json:"until"`
		Mode      string  `json:"mode"`
		PaidAt    string  `json:"paid_at"`
		Method    string  `json:"method"`
		Reference *string `json:"reference"`
		Note      *string `json:"note"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}

	f := validate.Fields{}
	until := requiredDate(f, "until", body.Until)
	mode := f.OneOf("mode", strings.TrimSpace(body.Mode), backfillModePaid, backfillModeWaived)
	method := strings.TrimSpace(body.Method)
	if mode == backfillModePaid {
		if method == "" {
			method = methodCash
		}
		if !paymentMethods[method] {
			f.Add("method", "must be cash, bank_transfer or mobile_money_manual")
		}
	}
	reference := trimmedOpt(f, "reference", body.Reference, paymentReferenceMax)
	note := trimmedOpt(f, "note", body.Note, paymentNoteMax)
	if mode == backfillModeWaived && (note == nil || strings.TrimSpace(*note) == "") {
		f.Add("note", "a reason is required when periods are waived")
	}
	// A single date for every period, or each row's own due date. The literal
	// `due_date` is the default because it is the one answer that is true of
	// every row at once.
	perRowPaidAt := true
	var flatPaidAt time.Time
	if v := strings.TrimSpace(body.PaidAt); v != "" && v != paidAtDueDate {
		flatPaidAt = requiredDate(f, "paid_at", v)
		perRowPaidAt = false
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	if contract.Status != contractActive && contract.Status != contractExpiring {
		conflictCode(w, "contract_not_active", "contract not running",
			"history can only be settled on a running contract")
		return
	}
	// 422, not 400: the request parsed and its fields are well formed — it is
	// the combination with the contract that cannot be honoured (API.md).
	today := todayEAT()
	switch {
	case until.After(today):
		unprocessable(w, validate.Fields{"until": "must not be in the future"})
		return
	case until.Before(contract.StartDate.Time):
		unprocessable(w, validate.Fields{
			"until": "must not be before the contract's start date (" +
				contract.StartDate.Time.Format(dateLayout) + ")",
		})
		return
	}

	org, err := s.q.GetOrg(r.Context(), contract.OrgID)
	if err != nil {
		s.serverError(w, r, "contract.backfill.org", err)
		return
	}
	settings := parseSettings(org.Settings)
	brand := s.brandingAssets(r.Context(), contract.OrgID, org.Name)

	req := backfillRequest{
		OrgID: contract.OrgID, ActorUserID: p.UserID, Contract: contract,
		Until: until, Mode: mode, Method: method, Reference: reference, Note: note,
		PerRowPaidAt: perRowPaidAt, PaidAt: flatPaidAt,
		OrgName: brand.DisplayName, Settings: settings,
	}
	var out backfillOutcome
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		out, err = s.runBackfill(r.Context(), q, req)
		return err
	}); err != nil {
		s.serverError(w, r, "contract.backfill.tx", err)
		return
	}
	s.enqueueNotifications(r.Context(), out.NotifyID)

	WriteJSON(w, http.StatusOK, map[string]any{
		"settled": out.Settled, "skipped": out.Skipped, "total": out.Total,
		"schedules": out.Schedules,
	})
}

// backfillRequest is one settlement of everything due up to a date.
type backfillRequest struct {
	OrgID        pgtype.UUID
	ActorUserID  pgtype.UUID
	Contract     sqlc.GetContractRow
	Until        time.Time
	Mode         string
	Method       string
	Reference    *string
	Note         *string
	PerRowPaidAt bool
	PaidAt       time.Time
	OrgName      string
	Settings     OrgSettings
}

// backfillOutcome is what the caller needs after the transaction commits.
type backfillOutcome struct {
	Settled   int    `json:"settled"`
	Skipped   int    `json:"skipped"`
	Total     int64  `json:"total"`
	NotifyID  string `json:"-"`
	Schedules []scheduleResponse
}

// runBackfill closes every unsettled row due on or before `until`.
//
// It does not go through allocatePayment, and the reason is worth stating: the
// allocator's job is to decide *where* a sum of money lands, rolling an
// overpayment forward. A backfill has no such question — each period is settled
// for exactly its own remainder, one payment per row, so the money can never
// spill from one period into the next and every row's paid_amount is its own
// amount. Going through the allocator would also queue one `thank_you` per
// period, which is the N texts about last year's rent this endpoint exists to
// avoid.
func (s *Server) runBackfill(
	ctx context.Context, q *sqlc.Queries, req backfillRequest,
) (backfillOutcome, error) {
	var out backfillOutcome

	rows, err := q.LockBackfillSchedules(ctx, sqlc.LockBackfillSchedulesParams{
		OrgID: req.OrgID, ContractID: req.Contract.ID,
		Until: pgtype.Date{Time: req.Until, Valid: true},
	})
	if err != nil {
		return out, err
	}

	for _, row := range rows {
		outstanding := row.Amount - row.PaidAmount
		// `paid` and `waived` rows are the ones already answered. A `partial`
		// row is not: it is settled for its remainder, which is what a landlord
		// reconstructing a year means by "this period is paid".
		if outstanding <= 0 || row.Status == payment.StatusWaived || row.Status == payment.StatusPaid {
			out.Skipped++
			out.Schedules = append(out.Schedules, toSchedule(row))
			continue
		}

		if req.Mode == backfillModeWaived {
			updated, err := q.WaiveSchedule(ctx, sqlc.WaiveScheduleParams{OrgID: req.OrgID, ID: row.ID})
			if err != nil {
				if isNoRows(err) {
					// Another writer settled it between the lock and here —
					// impossible under FOR UPDATE, but the row is reported as
					// skipped rather than crashing the call if it ever is.
					out.Skipped++
					continue
				}
				return out, err
			}
			out.Settled++
			out.Schedules = append(out.Schedules, toSchedule(updated))
			continue
		}

		paidAt := req.PaidAt
		if req.PerRowPaidAt {
			paidAt = row.DueDate.Time
		}
		pay, err := q.CreatePayment(ctx, sqlc.CreatePaymentParams{
			OrgID: req.OrgID, ContractID: req.Contract.ID, ScheduleID: row.ID,
			Amount: outstanding, Method: req.Method, Reference: req.Reference,
			PaidAt: db.TS(paidAt), RecordedByUserID: req.ActorUserID, Note: req.Note,
			Source: strPtr(sourceBackfill),
		})
		if err != nil {
			return out, err
		}
		if _, err := q.CreatePaymentAllocation(ctx, sqlc.CreatePaymentAllocationParams{
			OrgID: req.OrgID, PaymentID: pay.ID, ScheduleID: row.ID, Amount: outstanding,
		}); err != nil {
			return out, err
		}
		updated, err := q.ApplyPaymentToSchedule(ctx, sqlc.ApplyPaymentToScheduleParams{
			PaidAmount: row.Amount, OrgID: req.OrgID, ID: row.ID,
		})
		if err != nil {
			return out, err
		}
		// One `payment.record` row per payment, as for any other money: the
		// ledger's grain is the payment, and a reversal later has to find the
		// row it took back.
		if err := audit.Record(ctx, q, audit.Entry{
			OrgID:       db.UUIDString(req.OrgID),
			ActorUserID: db.UUIDString(req.ActorUserID),
			Action:      audit.ActionPaymentRecord,
			EntityType:  audit.EntityPayment,
			EntityID:    db.UUIDString(pay.ID),
			After: map[string]any{
				"contract_id": db.UUIDString(req.Contract.ID), "amount": outstanding,
				"method": req.Method, "paid_at": paidAt.Format(time.RFC3339),
				"schedule_id": db.UUIDString(row.ID), "source": sourceBackfill,
			},
		}); err != nil {
			return out, err
		}
		out.Settled++
		out.Total += outstanding
		out.Schedules = append(out.Schedules, toSchedule(updated))
	}
	if out.Schedules == nil {
		out.Schedules = []scheduleResponse{}
	}

	// One row for the decision, carrying the counts, beside the per-payment
	// rows above: "who decided a year of this tenancy was settled?" is one
	// question with one answer however many periods it closed.
	if err := audit.Record(ctx, q, audit.Entry{
		OrgID:       db.UUIDString(req.OrgID),
		ActorUserID: db.UUIDString(req.ActorUserID),
		Action:      audit.ActionContractBackfill,
		EntityType:  audit.EntityContract,
		EntityID:    db.UUIDString(req.Contract.ID),
		After: map[string]any{
			"until": req.Until.Format(dateLayout), "mode": req.Mode,
			"settled": out.Settled, "skipped": out.Skipped, "total": out.Total,
		},
	}); err != nil {
		return out, err
	}

	// Exactly one message, and only when something moved. A backfill that
	// settled nothing is a landlord checking, not news.
	if out.Settled > 0 {
		out.NotifyID, err = s.queueBackfillDone(ctx, q, backfillMessage{
			OrgID: db.UUIDString(req.OrgID), UserID: db.UUIDString(req.Contract.RenterUserID),
			ContractID: db.UUIDString(req.Contract.ID),
			Phone:      db.StrVal(req.Contract.RenterPhone),
			Name:       req.Contract.RenterName, Unit: req.Contract.UnitName,
			Property: req.Contract.PropertyName, OrgName: req.OrgName,
			Date: req.Until.Format(dateLayout), Lang: req.Settings.SMSLanguage,
			Overrides: req.Settings.notifyOverrides(),
			Enabled:   notificationSettingsOf(req.Settings).Kinds.BackfillDoneEnabled(),
		})
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// backfillMessage carries what the one `backfill_done` SMS needs across the
// transaction boundary.
type backfillMessage struct {
	OrgID      string
	UserID     string
	ContractID string
	Phone      string
	Name       string
	Unit       string
	Property   string
	OrgName    string
	Date       string
	Lang       string
	Overrides  notify.Overrides
	Enabled    bool
}

// queueBackfillDone writes the notification row inside the caller's
// transaction. The dedupe key carries the date, so backfilling further forward
// later sends a second message and re-running the same call does not.
func (s *Server) queueBackfillDone(
	ctx context.Context, q *sqlc.Queries, m backfillMessage,
) (string, error) {
	if !m.Enabled || m.Phone == "" {
		return "", nil
	}
	lang := s.recipientLang(ctx, q, m.UserID, m.Lang)
	id, err := notify.Queue(ctx, q, notify.Msg{
		OrgID: m.OrgID, UserID: m.UserID, Kind: notify.KindBackfillDone,
		DedupeKey: notify.KindBackfillDone + ":" + m.ContractID + ":" + m.Date,
		Phone:     m.Phone,
		Body: notify.Render(notify.KindBackfillDone, lang, notify.Vars{
			Name: m.Name, Unit: m.Unit, Property: m.Property, Org: m.OrgName,
			Date: m.Date, PayLink: notify.PayLink(s.cfg.EnduserURL()),
			Link: notify.PayLink(s.cfg.EnduserURL()),
		}, m.Overrides),
		Language: lang,
	})
	if errors.Is(err, notify.ErrDuplicate) {
		return "", nil
	}
	return id, err
}

// ---------------------------------------------------- last_payment_source --

// lastPaymentSources maps schedule ids to the `source` of the newest live
// payment allocated to each, for the chip both sides of the product render. A
// schedule nothing has been allocated to is simply absent from the map, which is
// the `null` the API promises.
func (s *Server) lastPaymentSources(ctx context.Context, ids []pgtype.UUID) map[string]string {
	if len(ids) == 0 {
		return map[string]string{}
	}
	rows, err := s.q.ListLastPaymentSourceForSchedules(ctx, ids)
	if err != nil {
		// A missing chip is not worth a 500 on the money screen.
		s.logger.Warn("last payment source unavailable", "error", err)
		return map[string]string{}
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[db.UUIDString(row.ScheduleID)] = row.Source
	}
	return out
}

// ----------------------------------------------------------------- helpers --

// strPtr is the one-liner the optional `source` argument needs.
func strPtr(v string) *string { return &v }

// -------------------------------------------- GET /payments?format=csv --

// paymentsCSVHeader is the export's column order. It is part of the contract:
// a landlord's spreadsheet formulas point at column letters, so a column is
// appended, never inserted.
//
//nolint:gochecknoglobals // fixed vocabulary, read-only.
var paymentsCSVHeader = []string{
	"paid_at", "renter_name", "property", "unit", "amount", "method",
	"reference", "source", "status", "note",
}

// writePaymentsCSV writes the ledger as a spreadsheet. Every free-text cell is
// defused first: a renter or a reference called `=HYPERLINK(...)` is a formula
// the landlord's spreadsheet would run on open, exactly as the payment-status
// and expenses exports already guard against.
func writePaymentsCSV(w http.ResponseWriter, items []paymentResponse) {
	filename := "payments-" + todayEAT().Format(dateLayout) + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)

	cw := csv.NewWriter(w)
	_ = cw.Write(paymentsCSVHeader)
	rec := make([]string, len(paymentsCSVHeader))
	for _, it := range items {
		rec[0] = it.PaidAt.Format(time.RFC3339)
		rec[1] = report.CSVCell(it.RenterName)
		rec[2] = report.CSVCell(it.PropertyName)
		rec[3] = report.CSVCell(it.UnitName)
		rec[4] = strconv.FormatInt(it.Amount, 10)
		rec[5] = it.Method
		rec[6] = ""
		if it.Reference != nil {
			rec[6] = report.CSVCell(*it.Reference)
		}
		rec[7] = it.Source
		rec[8] = it.Status
		rec[9] = ""
		if it.Note != nil {
			rec[9] = report.CSVCell(*it.Note)
		}
		_ = cw.Write(rec)
	}
	cw.Flush()
}
