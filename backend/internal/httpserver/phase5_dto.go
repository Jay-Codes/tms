package httpserver

import (
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/payment"
	"tms/backend/internal/validate"
)

// Payment methods the MVP records. `gateway` exists in the column for the
// post-MVP seam (SPEC §5.7) but is not a value a landlord may send.
const (
	methodCash              = "cash"
	methodBankTransfer      = "bank_transfer"
	methodMobileMoneyManual = "mobile_money_manual"
)

// paymentReversed is the status a correction leaves behind (API.md Phase 5);
// a live payment is `recorded`.
const paymentReversed = "reversed"

// Field bounds (API.md Phase 5).
const (
	paymentReferenceMax    = 80
	paymentNoteMax         = 500
	reversalReasonMax      = 200
	bankInstructionsMax    = 300
	bankFieldMax           = 120
	paidAtFutureToleranceH = 24 // paid_at may not be more than a day ahead
	// A payment's amount uses the shared money bounds (checkAmount): 1 …
	// 999,999,999,999 TZS, the same range a rent or a price plan accepts.
)

// scheduleStatuses are the values `GET /schedules?status=` accepts.
//
//nolint:gochecknoglobals // fixed value set, read-only.
var scheduleStatuses = map[string]bool{
	payment.StatusPending: true, payment.StatusPaid: true, payment.StatusPartial: true,
	payment.StatusOverdue: true, payment.StatusWaived: true,
}

//nolint:gochecknoglobals // fixed value set, read-only.
var paymentMethods = map[string]bool{
	methodCash: true, methodBankTransfer: true, methodMobileMoneyManual: true,
}

// ------------------------------------------------------------- responses --

// appliedAlloc is one entry of a payment's `applied[]`: the schedule the money
// reached and how much of it landed there.
type appliedAlloc struct {
	ScheduleID string `json:"schedule_id"`
	Amount     int64  `json:"amount"`
}

// recordedBy names the staff member who entered the payment. The renter's own
// view carries the name only — who keyed it in is the org's business, not an
// identifier the renter needs (API.md: "recorded_by name only").
type recordedBy struct {
	UserID *string `json:"user_id,omitempty"`
	Name   string  `json:"name"`
}

// paymentResponse is the `payment` shape from API.md.
type paymentResponse struct {
	ID             string         `json:"id"`
	ContractID     string         `json:"contract_id"`
	ScheduleID     *string        `json:"schedule_id"`
	Amount         int64          `json:"amount"`
	Method         string         `json:"method"`
	Reference      *string        `json:"reference"`
	PaidAt         time.Time      `json:"paid_at"`
	Note           *string        `json:"note"`
	Status         string         `json:"status"`
	RecordedBy     *recordedBy    `json:"recorded_by"`
	ReversedAt     *time.Time     `json:"reversed_at"`
	ReversalReason *string        `json:"reversal_reason"`
	Applied        []appliedAlloc `json:"applied"`
	// ImportBatchID names the CSV import this payment arrived on, and is null
	// for money the landlord keyed in. It is what puts the "imported" chip on
	// a ledger row (Phase 16 §16.2).
	ImportBatchID *string   `json:"import_batch_id"`
	UnitName      string    `json:"unit_name"`
	PropertyName  string    `json:"property_name"`
	RenterName    string    `json:"renter_name"`
	CreatedAt     time.Time `json:"created_at"`
}

// scheduleContract is the identity block each schedule of the landlord's board
// carries, so an overdue list reads as "who owes what on which unit".
type scheduleContract struct {
	ID           string `json:"id"`
	UnitName     string `json:"unit_name"`
	PropertyName string `json:"property_name"`
	RenterName   string `json:"renter_name"`
	RenterUserID string `json:"renter_user_id"`
}

// scheduleItem is the `schedule` shape from API.md Phase 5.
type scheduleItem struct {
	ID          string            `json:"id"`
	ContractID  string            `json:"contract_id"`
	PeriodStart string            `json:"period_start"`
	PeriodEnd   string            `json:"period_end"`
	DueDate     string            `json:"due_date"`
	Amount      int64             `json:"amount"`
	PaidAmount  int64             `json:"paid_amount"`
	Status      string            `json:"status"`
	DaysOverdue int               `json:"days_overdue"`
	Contract    *scheduleContract `json:"contract,omitempty"`
}

// BankAccount is the org's collection account: what a renter is told to pay
// into, since the MVP moves no money itself (FLOWS 7.2). It lives inside
// orgs.settings rather than a table of its own — one record per org, replaced
// whole by PUT /org/bank-account.
type BankAccount struct {
	BankName      string `json:"bank_name"`
	AccountName   string `json:"account_name"`
	AccountNumber string `json:"account_number"`
	Instructions  string `json:"instructions"`
}

// --------------------------------------------------------------- mapping --

// paymentRow is the union of the Get/List payment row shapes: the two queries
// select identical columns, so one mapper serves both.
type paymentRow = sqlc.GetPaymentRow

func paymentRowOfList(r sqlc.ListPaymentsRow) paymentRow { return paymentRow(r) }

// toPayment maps a payment row plus its allocations. withActor is false for the
// renter's own history, which sees the recorder's name but not their id.
func toPayment(r paymentRow, applied []appliedAlloc, withActor bool) paymentResponse {
	out := paymentResponse{
		ID:             db.UUIDString(r.ID),
		ContractID:     db.UUIDString(r.ContractID),
		Amount:         r.Amount,
		Method:         r.Method,
		Reference:      r.Reference,
		PaidAt:         r.PaidAt.Time,
		Note:           r.Note,
		Status:         r.Status,
		ReversedAt:     timePtr(r.ReversedAt.Valid, r.ReversedAt.Time),
		ReversalReason: r.ReversalReason,
		Applied:        applied,
		UnitName:       r.UnitName,
		PropertyName:   r.PropertyName,
		RenterName:     r.RenterName,
		CreatedAt:      r.CreatedAt.Time,
	}
	if out.Applied == nil {
		out.Applied = []appliedAlloc{}
	}
	if r.ScheduleID.Valid {
		id := db.UUIDString(r.ScheduleID)
		out.ScheduleID = &id
	}
	if r.ImportBatchID.Valid {
		id := db.UUIDString(r.ImportBatchID)
		out.ImportBatchID = &id
	}
	if r.RecordedByName != nil {
		rb := &recordedBy{Name: *r.RecordedByName}
		if withActor && r.RecordedByUserID.Valid {
			id := db.UUIDString(r.RecordedByUserID)
			rb.UserID = &id
		}
		out.RecordedBy = rb
	}
	return out
}

// toScheduleItem maps the landlord's schedule board row.
func toScheduleItem(r sqlc.ListSchedulesRow, today time.Time) scheduleItem {
	return scheduleItemOf(sqlc.PaymentSchedule{
		ID: r.ID, ContractID: r.ContractID, PeriodStart: r.PeriodStart,
		PeriodEnd: r.PeriodEnd, DueDate: r.DueDate, Amount: r.Amount,
		Status: r.Status, PaidAmount: r.PaidAmount,
	}, &scheduleContract{
		ID:           db.UUIDString(r.ContractID),
		UnitName:     r.UnitName,
		PropertyName: r.PropertyName,
		RenterName:   r.RenterName,
		RenterUserID: db.UUIDString(r.RenterUserID),
	}, today)
}

// scheduleItemOf maps a stored schedule row, optionally with the contract
// identity block the landlord's views carry.
func scheduleItemOf(s sqlc.PaymentSchedule, c *scheduleContract, today time.Time) scheduleItem {
	return scheduleItem{
		ID:          db.UUIDString(s.ID),
		ContractID:  db.UUIDString(s.ContractID),
		PeriodStart: s.PeriodStart.Time.Format(dateLayout),
		PeriodEnd:   s.PeriodEnd.Time.Format(dateLayout),
		DueDate:     s.DueDate.Time.Format(dateLayout),
		Amount:      s.Amount,
		PaidAmount:  s.PaidAmount,
		Status:      s.Status,
		DaysOverdue: daysOverdue(s.Status, s.DueDate.Time, today),
		Contract:    c,
	}
}

// daysOverdue counts whole days a schedule is late. A settled or waived row is
// never late, and neither is one whose due date has not passed — so the number
// is 0 rather than negative.
func daysOverdue(status string, due, today time.Time) int {
	if status == payment.StatusPaid || status == payment.StatusWaived {
		return 0
	}
	d := int(today.Truncate(24*time.Hour).Sub(due.UTC().Truncate(24*time.Hour)).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

// allocIndex groups allocation rows by their payment, preserving order.
func allocIndex(rows []sqlc.ListAllocationsForPaymentsRow) map[string][]appliedAlloc {
	out := map[string][]appliedAlloc{}
	for _, a := range rows {
		id := db.UUIDString(a.PaymentID)
		out[id] = append(out[id], appliedAlloc{
			ScheduleID: db.UUIDString(a.ScheduleID), Amount: a.Amount,
		})
	}
	return out
}

// --------------------------------------------------------------- helpers --

// schedulePage is the cursor of GET /schedules. The board reads forward in due
// order — the next thing owed is the first thing shown — so the cursor carries
// the due date rather than the created_at the other listings page by.
type schedulePage struct {
	Limit     int32
	CursorDue pgtype.Date
	CursorID  pgtype.UUID
}

func parseSchedulePage(r *http.Request, f validate.Fields) schedulePage {
	base := parseListPage(r, f)
	page := schedulePage{Limit: base.Limit}
	if base.CursorAt.Valid {
		page.CursorDue = pgtype.Date{Time: base.CursorAt.Time, Valid: true}
		page.CursorID = base.CursorID
	}
	return page
}

// nextScheduleCursor encodes the last row of a full page as the next cursor.
func nextScheduleCursor(rows int, limit int32, due time.Time, id string) *string {
	return nextCursor(rows, limit, due, id)
}

// optQueryDate parses an optional YYYY-MM-DD query parameter.
func optQueryDate(f validate.Fields, field, in string) pgtype.Date {
	v := strings.TrimSpace(in)
	if v == "" {
		return pgtype.Date{}
	}
	t, err := time.Parse(dateLayout, v)
	if err != nil {
		f.Add(field, "must be a date (YYYY-MM-DD)")
		return pgtype.Date{}
	}
	return pgtype.Date{Time: t, Valid: true}
}

// optQueryTime parses an optional RFC3339 query parameter, also accepting a
// bare date so `from=2026-09-01` works the way a human expects.
func optQueryTime(f validate.Fields, field, in string) pgtype.Timestamptz {
	v := strings.TrimSpace(in)
	if v == "" {
		return pgtype.Timestamptz{}
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return db.TS(t)
	}
	if t, err := time.Parse(dateLayout, v); err == nil {
		return db.TS(t)
	}
	f.Add(field, "must be a timestamp (RFC3339) or a date (YYYY-MM-DD)")
	return pgtype.Timestamptz{}
}

// optQueryUUID parses an optional id query parameter.
func optQueryUUID(f validate.Fields, field, in string) pgtype.UUID {
	v := strings.TrimSpace(in)
	if v == "" {
		return pgtype.UUID{}
	}
	id, err := db.ParseUUID(v)
	if err != nil {
		f.Add(field, "must be an id (UUID)")
		return pgtype.UUID{}
	}
	return id
}

// toAllocSchedule converts a stored schedule row into the value the pure
// allocator works on.
func toAllocSchedule(s sqlc.PaymentSchedule) payment.Schedule {
	return payment.Schedule{
		ID:         db.UUIDString(s.ID),
		Amount:     s.Amount,
		PaidAmount: s.PaidAmount,
		Status:     s.Status,
		DueDate:    s.DueDate.Time.Format(dateLayout),
	}
}
