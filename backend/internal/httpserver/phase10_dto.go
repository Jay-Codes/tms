package httpserver

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/expense"
	"tms/backend/internal/period"
	"tms/backend/internal/tz"
	"tms/backend/internal/validate"
)

// Field bounds for the expense ledger (SPEC §5.11, PLAN2 Phase 10).
const (
	categoryNameMax   = 60
	expenseVendorMax  = 120
	expenseRefMax     = 120
	expenseNoteMax    = 1000
	voidReasonMax     = 300
	categorySortMax   = 10_000
	expenseAmountMax  = 1_000_000_000 // inclusive: one billion shillings
	expenseCSVMaxRows = 10_000
	// expenseEarliestYear bounds `incurred_on` downwards. A ledger entry from
	// before the platform's world existed is a typo — usually a year typed as
	// 2006 — and catching it here is cheaper than a landlord wondering why
	// this month's total is short.
	expenseEarliestYear = 2000
	// expenseFutureDays is how far ahead an expense may be dated: tomorrow.
	// A bill paid across midnight in another time zone is real; next month's
	// is a mistake (FLOWS 12 edge cases).
	expenseFutureDays = 1
)

// Receipt limits. The set is the one FLOWS 12.2 names — a photo of a till slip,
// or the PDF an invoice arrives as.
const (
	receiptMaxBytes  = 5 << 20 // 5 MiB
	receiptUploadTTL = 15 * time.Minute
	receiptReadTTL   = 15 * time.Minute
	// receiptUploadLimit caps presigned receipt PUT URLs per org per hour.
	// Recording thirty expenses with receipts in one hour is a busy day of
	// data entry; three hundred is a script minting upload URLs.
	receiptUploadLimit  = 30
	receiptUploadWindow = time.Hour
)

// receiptContentTypes maps an accepted content type to the extension its object
// key carries. The key is derived from the type rather than from a filename:
// the client never names the object, so it cannot name it `../something.png`.
//
//nolint:gochecknoglobals // fixed value set, read-only.
var receiptContentTypes = map[string]string{
	"image/jpeg":      "jpg",
	"image/png":       "png",
	"application/pdf": "pdf",
}

// expenseStatusFilters are the values `GET /expenses?status=` accepts. `all`
// is the only one that is not a stored status: it means "do not filter".
const expenseStatusAll = "all"

// ------------------------------------------------------------- responses --

// namedRef is the `{id, name}` block an expense carries for its property, unit
// and category, so a ledger row renders without a second request.
type namedRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// receiptBlock says whether an expense has a receipt and what it is. The type
// and size are the ones MinIO reported for the object at completion, never what
// the client claimed (SPEC §7).
type receiptBlock struct {
	Present     bool    `json:"present"`
	ContentType *string `json:"content_type"`
	Size        *int64  `json:"size"`
}

// categoryResponse is the `category` shape (API.md Phase 10).
type categoryResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	IsDefault bool      `json:"is_default"`
	SortOrder int       `json:"sort_order"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

func toCategory(c sqlc.ExpenseCategory) categoryResponse {
	return categoryResponse{
		ID:        db.UUIDString(c.ID),
		Name:      c.Name,
		IsDefault: c.IsDefault,
		SortOrder: int(c.SortOrder),
		Active:    c.Active,
		CreatedAt: c.CreatedAt.Time,
	}
}

func toCategories(rows []sqlc.ExpenseCategory) []categoryResponse {
	out := make([]categoryResponse, 0, len(rows))
	for _, c := range rows {
		out = append(out, toCategory(c))
	}
	return out
}

// expenseResponse is the `expense` shape (API.md Phase 10).
type expenseResponse struct {
	ID         string       `json:"id"`
	Property   namedRef     `json:"property"`
	Unit       *namedRef    `json:"unit"`
	Category   *namedRef    `json:"category"`
	Amount     int64        `json:"amount"`
	IncurredOn string       `json:"incurred_on"`
	Vendor     string       `json:"vendor"`
	Reference  string       `json:"reference"`
	Note       string       `json:"note"`
	Receipt    receiptBlock `json:"receipt"`
	RecordedBy *recordedBy  `json:"recorded_by"`
	Status     string       `json:"status"`
	VoidedAt   *time.Time   `json:"voided_at"`
	VoidReason *string      `json:"void_reason"`
	CreatedAt  time.Time    `json:"created_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
}

// expenseRow is the union of the Get/List expense row shapes: the two queries
// select identical columns, so one mapper serves both (the payments pattern).
type expenseRow = sqlc.GetExpenseRow

func expenseRowOfList(r sqlc.ListExpensesRow) expenseRow { return expenseRow(r) }

func toExpense(r expenseRow) expenseResponse {
	out := expenseResponse{
		ID:         db.UUIDString(r.ID),
		Property:   namedRef{ID: db.UUIDString(r.PropertyID), Name: r.PropertyName},
		Amount:     r.Amount,
		IncurredOn: r.IncurredOn.Time.Format(dateLayout),
		Vendor:     r.Vendor,
		Reference:  r.Reference,
		Note:       r.Note,
		Receipt: receiptBlock{
			Present:     r.ReceiptObjectKey != nil && *r.ReceiptObjectKey != "",
			ContentType: r.ReceiptContentType,
			Size:        r.ReceiptSize,
		},
		Status:     r.Status,
		VoidedAt:   timePtr(r.VoidedAt.Valid, r.VoidedAt.Time),
		VoidReason: r.VoidReason,
		CreatedAt:  r.CreatedAt.Time,
		UpdatedAt:  r.UpdatedAt.Time,
	}
	if r.UnitID.Valid && r.UnitName != nil {
		out.Unit = &namedRef{ID: db.UUIDString(r.UnitID), Name: *r.UnitName}
	}
	if r.CategoryID.Valid && r.CategoryName != nil {
		out.Category = &namedRef{ID: db.UUIDString(r.CategoryID), Name: *r.CategoryName}
	}
	if r.RecordedByName != nil {
		rb := &recordedBy{Name: *r.RecordedByName}
		if r.RecordedByUserID.Valid {
			id := db.UUIDString(r.RecordedByUserID)
			rb.UserID = &id
		}
		out.RecordedBy = rb
	}
	return out
}

// expenseWindowResponse is the `{from, to, cadence}` block every Part 2 report
// echoes, so a client can prove which period it is looking at.
type expenseWindowResponse = reportWindow

// expenseSummaryResponse is GET /expenses/summary.
type expenseSummaryResponse struct {
	Window        expenseWindowResponse `json:"window"`
	Previous      expenseWindowResponse `json:"previous"`
	GroupBy       string                `json:"group_by"`
	Groups        []expense.Group       `json:"groups"`
	Total         expense.Total         `json:"total"`
	PreviousTotal expense.Total         `json:"previous_total"`
	ChangePct     *float64              `json:"change_pct"`
}

// --------------------------------------------------------------- filters --

// expenseFilters is the validated `GET /expenses` query: the same set feeds the
// page, the totals and the CSV, so the three can never disagree about which
// rows they are describing.
type expenseFilters struct {
	Status     *string
	PropertyID pgtype.UUID
	UnitID     pgtype.UUID
	CategoryID pgtype.UUID
	// From is inclusive and To exclusive — the half-open window the queries
	// take. The wire dates are inclusive at both ends; parseExpenseFilters is
	// where that is turned into this.
	From pgtype.Date
	To   pgtype.Date
	Q    *string
	// FromLabel and ToLabel are the wire dates as the caller meant them, used
	// for the CSV filename. Empty when that end of the window is unbounded.
	FromLabel string
	ToLabel   string
}

// parseExpenseFilters validates the ledger's query parameters.
//
// A `cadence` resolves the window through internal/period, so "this quarter"
// means the same thing here as it does on every other Part 2 report. Without
// one, `from`/`to` are read as plain inclusive dates and either may be omitted.
func parseExpenseFilters(f validate.Fields, qs urlValues) expenseFilters {
	out := expenseFilters{}

	// Default: the live ledger. `all` is the only way to see voided rows
	// alongside recorded ones, because a total that silently included
	// corrections would be wrong.
	status := expense.StatusRecorded
	if v := strings.TrimSpace(qs.Get("status")); v != "" {
		status = f.OneOf("status", strings.ToLower(v),
			expense.StatusRecorded, expense.StatusVoided, expenseStatusAll)
	}
	if status != expenseStatusAll && status != "" {
		s := status
		out.Status = &s
	}

	out.PropertyID = optQueryUUID(f, "property_id", qs.Get("property_id"))
	out.UnitID = optQueryUUID(f, "unit_id", qs.Get("unit_id"))
	out.CategoryID = optQueryUUID(f, "category_id", qs.Get("category_id"))

	if v := strings.TrimSpace(qs.Get("q")); v != "" {
		term := escapeLike(v)
		out.Q = &term
	}

	from := optQueryDate(f, "from", qs.Get("from"))
	to := optQueryDate(f, "to", qs.Get("to"))

	if cadence := strings.TrimSpace(qs.Get("cadence")); cadence != "" {
		w, ok := resolveWindow(f, cadence, qs.Get("anchor"), from, to)
		if !ok {
			return out
		}
		out.From = dateParam(w.From)
		out.To = dateParam(w.To)
		out.FromLabel = w.From.Format(dateLayout)
		// The label is the last day *inside* the window: a caller asked for
		// September, and `expenses-2026-09-01-2026-10-01.csv` would name a
		// day the file does not contain.
		out.ToLabel = w.To.AddDate(0, 0, -1).Format(dateLayout)
		return out
	}

	if from.Valid {
		out.From = from
		out.FromLabel = from.Time.Format(dateLayout)
	}
	if to.Valid {
		// Inclusive on the wire, exclusive in the query.
		out.To = pgtype.Date{Time: to.Time.AddDate(0, 0, 1), Valid: true}
		out.ToLabel = to.Time.Format(dateLayout)
	}
	if from.Valid && to.Valid && to.Time.Before(from.Time) {
		f.Add("to", "must not be before `from`")
	}
	return out
}

// resolveWindow runs the shared cadence resolver and translates its refusals
// into field errors, so every Part 2 endpoint rejects the same spellings.
func resolveWindow(f validate.Fields, cadence, anchorRaw string, from, to pgtype.Date) (period.Window, bool) {
	w, _, ok := resolveWindowStatus(f, cadence, anchorRaw, from, to)
	return w, ok
}

// resolveWindowStatus is the same resolution, plus the status its refusal
// deserves: see windowRefusalStatus. Reports call this one; the expense
// ledger's list filter calls resolveWindow and answers 400 throughout, the
// status it shipped with.
func resolveWindowStatus(
	f validate.Fields, cadence, anchorRaw string, from, to pgtype.Date,
) (period.Window, int, bool) {
	anchor := time.Now().In(tz.Zone())
	if v := strings.TrimSpace(anchorRaw); v != "" {
		t, err := time.Parse(dateLayout, v)
		if err != nil {
			f.Add("anchor", "must be a date (YYYY-MM-DD)")
			return period.Window{}, http.StatusBadRequest, false
		}
		anchor = t
	}
	var fromPtr, toPtr *time.Time
	if from.Valid {
		t := from.Time
		fromPtr = &t
	}
	if to.Valid {
		// The resolver's custom range is half-open, and the wire dates are
		// inclusive: a caller asking for 1–30 September means the whole of
		// the 30th.
		t := to.Time.AddDate(0, 0, 1)
		toPtr = &t
	}
	w, err := period.Resolve(cadence, anchor, fromPtr, toPtr)
	if err != nil {
		f.Add("cadence", err.Error())
		return period.Window{}, windowRefusalStatus(err), false
	}
	return w, http.StatusOK, true
}

// windowRefusalStatus separates the two ways a window request can fail.
//
// A cadence the API does not know, or a date it cannot parse, is malformed:
// 400. A custom range that is well-formed but cannot exist — ends before it
// starts, or spans more than five years — is a 422: nothing about the request
// needs reformatting, the answer simply is not one this API will produce, and
// the client's fix is different in each case (PLAN2 Phase 11).
func windowRefusalStatus(err error) int {
	switch {
	case errors.Is(err, period.ErrRangeOrder),
		errors.Is(err, period.ErrRangeTooLong),
		errors.Is(err, period.ErrRangeMissing):
		return http.StatusUnprocessableEntity
	default:
		return http.StatusBadRequest
	}
}

// urlValues is the slice of net/url.Values the parsers actually use, named so
// the filter parser can be exercised without building a request.
type urlValues interface{ Get(string) string }

// --------------------------------------------------------------- cursors --

// expenseCursor is the ledger's page boundary. It carries the whole sort tuple
// — `(incurred_on, created_at, id)` — because the ledger sorts by a date, and
// a date alone repeats often enough that a cursor built on it would drop rows
// at every page boundary that lands inside a busy day.
type expenseCursor struct {
	Incurred pgtype.Date
	Created  pgtype.Timestamptz
	ID       pgtype.UUID
}

func encodeExpenseCursor(incurred time.Time, created time.Time, id string) string {
	raw := incurred.Format(dateLayout) + "," + created.UTC().Format(time.RFC3339Nano) + "," + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeExpenseCursor(cursor string) (expenseCursor, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return expenseCursor{}, false
	}
	parts := strings.SplitN(string(raw), ",", 3)
	if len(parts) != 3 {
		return expenseCursor{}, false
	}
	incurred, err := time.Parse(dateLayout, parts[0])
	if err != nil {
		return expenseCursor{}, false
	}
	created, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil {
		return expenseCursor{}, false
	}
	id, err := db.ParseUUID(parts[2])
	if err != nil {
		return expenseCursor{}, false
	}
	return expenseCursor{
		Incurred: pgtype.Date{Time: incurred, Valid: true},
		Created:  db.TS(created),
		ID:       id,
	}, true
}

// parseExpensePage reads `limit` and `cursor` for the ledger.
func parseExpensePage(r *http.Request, f validate.Fields) (int32, expenseCursor) {
	limit := int32(listDefaultLimit)
	qs := r.URL.Query()
	if v := strings.TrimSpace(qs.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > listMaxLimit {
			f.Add("limit", "must be between 1 and "+strconv.Itoa(listMaxLimit))
		} else {
			limit = int32(n)
		}
	}
	var cur expenseCursor
	if v := strings.TrimSpace(qs.Get("cursor")); v != "" {
		c, ok := decodeExpenseCursor(v)
		if !ok {
			f.Add("cursor", "malformed cursor")
		} else {
			cur = c
		}
	}
	return limit, cur
}

// --------------------------------------------------------------- helpers --

// checkExpenseAmount records the bounds an expense amount must sit in. They are
// tighter than a rent's (`checkAmount`): a single repair over a billion
// shillings is a typo, and the ledger's totals are summed in int64 across
// thousands of rows.
func checkExpenseAmount(f validate.Fields, field string, amount int64) {
	if amount < 1 || amount > expenseAmountMax {
		f.Add(field, "must be a whole number of shillings between 1 and 1,000,000,000")
	}
}

// checkIncurredOn parses and bounds the date an expense was incurred.
func checkIncurredOn(f validate.Fields, field, in string) pgtype.Date {
	v := strings.TrimSpace(in)
	if v == "" {
		f.Add(field, field+" is required")
		return pgtype.Date{}
	}
	t, err := time.Parse(dateLayout, v)
	if err != nil {
		f.Add(field, "must be a date (YYYY-MM-DD)")
		return pgtype.Date{}
	}
	// "Today" is read on the platform wall clock (internal/tz): a landlord in
	// Dar recording an expense at 01:00 means today where they are standing,
	// not yesterday in UTC.
	now := time.Now().In(tz.Zone())
	latest := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).
		AddDate(0, 0, expenseFutureDays)
	if t.After(latest) {
		f.Add(field, "cannot be more than a day in the future")
		return pgtype.Date{}
	}
	if t.Year() < expenseEarliestYear {
		f.Add(field, "must not be before "+strconv.Itoa(expenseEarliestYear)+"-01-01")
		return pgtype.Date{}
	}
	return pgtype.Date{Time: t, Valid: true}
}
