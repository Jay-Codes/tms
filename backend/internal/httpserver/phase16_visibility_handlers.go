package httpserver

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/tz"
	"tms/backend/internal/validate"
)

// Phase 16 §16.3–§16.4: next-due visibility and payment instructions.
//
// Everything here answers one question a landlord or a renter asks in a
// different place — "when is the next money due, and where does it go?" — so
// the counting rule lives once, in daysUntilDue and in upcoming.sql, and every
// screen reads the same numbers.

// upcomingWindows are the windows GET /reports/upcoming offers. They are a
// fixed set rather than any integer: the dashboard card, the "Due soon" tab and
// the seven-day nudge are the three questions the product asks, and an
// arbitrary `days=137` would be a query nobody has an index or a screen for.
//
//nolint:gochecknoglobals // fixed vocabulary, read-only.
var upcomingWindows = []int{7, 14, 30}

const (
	// upcomingDaysDefault is the "Due soon" tab's own window (API.md §16.3).
	upcomingDaysDefault = 14
	// dashboardUpcomingDays is the window the dashboard card quotes, so the
	// summary can carry it without a second call.
	dashboardUpcomingDays = 7
)

// Mobile-money field bounds (§16.4).
const (
	mobileMoneyProviderMax = 40
	mobileMoneyNumberMax   = 20
	mobileMoneyNameMax     = 120
)

// MobileMoney is the second half of an org's payment instructions: the wallet a
// renter can send rent to. It lives beside `bank_account` inside orgs.settings
// — one record per org, replaced whole — because it is the same kind of fact
// and the renter's "How to pay" card renders both from one payload.
type MobileMoney struct {
	Provider string `json:"provider"`
	Number   string `json:"number"`
	Name     string `json:"name"`
}

// bankAccountPayload is the collection account as the renter and the landlord
// read it: the Phase 5 fields, plus the wallet beside them.
type bankAccountPayload struct {
	BankAccount
	MobileMoney *MobileMoney `json:"mobile_money"`
}

// payInstructions renders an org's payment instructions for a response body.
//
// The wallet is carried both inside the account block (where §16.4 puts it) and
// beside it, because an org may have set one and not the other: a renter whose
// landlord takes mobile money only must still be told where to send the money.
func payInstructions(s OrgSettings) (*bankAccountPayload, *MobileMoney) {
	if s.BankAccount == nil {
		return nil, s.MobileMoney
	}
	return &bankAccountPayload{BankAccount: *s.BankAccount, MobileMoney: s.MobileMoney}, s.MobileMoney
}

// paymentInstructionsSet reports whether a renter would be told anything at all
// — the flag behind the landlord's setup nudge (§16.4).
func paymentInstructionsSet(s OrgSettings) bool {
	return s.BankAccount != nil || s.MobileMoney != nil
}

// validateMobileMoney reads an optional mobile-money block. An explicit `null`
// clears it; an object must be complete, because half a wallet is not something
// a renter can pay into.
func validateMobileMoney(f validate.Fields, in *MobileMoney) *MobileMoney {
	if in == nil {
		return nil
	}
	out := MobileMoney{
		Provider: f.MaxLen("mobile_money.provider",
			f.Required("mobile_money.provider", in.Provider), mobileMoneyProviderMax),
		Number: f.MaxLen("mobile_money.number",
			f.Required("mobile_money.number", in.Number), mobileMoneyNumberMax),
		Name: f.MaxLen("mobile_money.name",
			f.Required("mobile_money.name", in.Name), mobileMoneyNameMax),
	}
	return &out
}

// publicBaseURL is the origin renter-facing links are built against — the one
// `{{pay_link}}` resolves to (§16.4).
//
// config.Load defaults PUBLIC_BASE_URL to APP_BASE_URL, but a Server can be
// built from a Config literal (the test harness does), and a link that reads
// "/enduser/payments" with no origin in front of it is not one a renter can
// open from an SMS. The fallback lives here so both paths agree.
func (s *Server) publicBaseURL() string {
	if v := strings.TrimSpace(s.cfg.PublicBaseURL); v != "" {
		return strings.TrimRight(v, "/")
	}
	return strings.TrimRight(strings.TrimSpace(s.cfg.AppBaseURL), "/")
}

// --------------------------------------------------------- days_until_due --

// todayEAT is the calendar day every countdown on the platform is read against
// (internal/tz): the renter's own wall clock, not the server's UTC one.
func todayEAT() time.Time { return tz.LocalDate(time.Now().In(tz.Zone())) }

// daysUntilDue counts whole days from today to a due date: 0 on the day itself,
// negative once it has passed.
//
// Both arguments are calendar dates at UTC midnight (that is how Postgres hands
// back a `date` and what todayEAT produces), so the subtraction is exact and
// carries no clock-drift of its own.
func daysUntilDue(due, today time.Time) int {
	d := due.UTC().Truncate(24 * time.Hour).Sub(today.UTC().Truncate(24 * time.Hour))
	return int(d.Hours() / 24)
}

// ------------------------------------------------ GET /reports/upcoming --

// upcomingItem is one row of the "due soon" list: the Phase 5 schedule shape,
// flattened, with the renter and unit identity the landlord needs to act on it.
type upcomingItem struct {
	scheduleItem
	RenterName   string  `json:"renter_name"`
	RenterUserID string  `json:"renter_user_id"`
	Phone        *string `json:"phone"`
	UnitName     string  `json:"unit_name"`
	PropertyName string  `json:"property_name"`
	DaysUntilDue int     `json:"days_until_due"`
}

// handleReportUpcoming is the landlord's "what is about to be owed" list
// (PLAN2 §16.3): everything unsettled on a running tenancy falling due between
// today and the end of the window, oldest first.
//
// Like every other schedule read it runs the org's overdue sweep first, so a
// row that lapsed overnight is already `overdue` here rather than `pending`.
func (s *Server) handleReportUpcoming(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	qs := r.URL.Query()
	f := validate.Fields{}

	days := upcomingDaysDefault
	if v := strings.TrimSpace(qs.Get("days")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || !containsInt(upcomingWindows, n) {
			f.Add("days", "must be 7, 14 or 30")
		} else {
			days = n
		}
	}
	propertyID := optQueryUUID(f, "property_id", qs.Get("property_id"))
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	// A property id from another org is a 404 rather than an empty list: the
	// answer must not distinguish "no rows" from "not yours" (API.md).
	if propertyID.Valid {
		if _, err := s.q.GetProperty(r.Context(), sqlc.GetPropertyParams{
			ID: propertyID, OrgID: p.OrgID,
		}); isNoRows(err) {
			notFoundProperty(w)
			return
		} else if err != nil {
			s.serverError(w, r, "report.upcoming.property", err)
			return
		}
	}
	s.flipOverdueFor(r.Context(), p.OrgID)

	today := todayEAT()
	to := today.AddDate(0, 0, days)
	rows, err := s.q.ListUpcomingSchedules(r.Context(), sqlc.ListUpcomingSchedulesParams{
		OrgID:      p.OrgID,
		FromDate:   pgtype.Date{Time: today, Valid: true},
		ToDate:     pgtype.Date{Time: to, Valid: true},
		PropertyID: propertyID,
	})
	if err != nil {
		s.serverError(w, r, "report.upcoming", err)
		return
	}

	items := make([]upcomingItem, 0, len(rows))
	var totalDue int64
	for _, row := range rows {
		outstanding := row.Amount - row.PaidAmount
		if outstanding < 0 {
			outstanding = 0
		}
		totalDue += outstanding
		items = append(items, upcomingItem{
			scheduleItem: scheduleItemOf(sqlc.PaymentSchedule{
				ID: row.ID, ContractID: row.ContractID, PeriodStart: row.PeriodStart,
				PeriodEnd: row.PeriodEnd, DueDate: row.DueDate, Amount: row.Amount,
				PaidAmount: row.PaidAmount, Status: row.Status,
			}, nil, time.Now().UTC()),
			RenterName:   row.RenterName,
			RenterUserID: db.UUIDString(row.RenterUserID),
			Phone:        row.RenterPhone,
			UnitName:     row.UnitName,
			PropertyName: row.PropertyName,
			DaysUntilDue: daysUntilDue(row.DueDate.Time, today),
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items": items, "total_due": totalDue, "count": len(items), "days": days,
		"window": map[string]string{
			"from": today.Format(dateLayout), "to": to.Format(dateLayout),
		},
	})
}

// upcomingCard is the dashboard's seven-day figure, carried on GET
// /reports/summary so the `upcoming` card needs no call of its own.
type upcomingCard struct {
	Count int64 `json:"count"`
	Total int64 `json:"total"`
}

// upcomingSummary reads the dashboard card's window. A failure is not fatal to
// the summary around it: an empty card is better than no dashboard.
func (s *Server) upcomingSummary(ctx context.Context, orgID pgtype.UUID) upcomingCard {
	today := todayEAT()
	row, err := s.q.UpcomingTotals(ctx, sqlc.UpcomingTotalsParams{
		OrgID:    orgID,
		FromDate: pgtype.Date{Time: today, Valid: true},
		ToDate:   pgtype.Date{Time: today.AddDate(0, 0, dashboardUpcomingDays), Valid: true},
	})
	if err != nil {
		s.logger.Warn("upcoming summary failed", "org_id", db.UUIDString(orgID), "error", err)
		return upcomingCard{}
	}
	return upcomingCard{Count: row.RowCount, Total: row.TotalDue}
}

// ------------------------------------------------- renters / units figures --

// renterDue is the money block the renters list carries per renter (§16.3),
// aggregated across every running tenancy that renter holds with this org.
type renterDue struct {
	NextDueDate   *string
	NextDueAmount *int64
	OverdueAmount int64
}

// renterDueFigures computes the Next due / Overdue columns of GET /renters.
//
// It runs the *same three queries* GET /reports/payment-status runs, so the two
// screens cannot disagree: the report quotes them per contract, this quotes
// them per renter, and the arithmetic between the two is a sum, not a second
// definition. Four round trips for any number of renters, as there.
func (s *Server) renterDueFigures(ctx context.Context, orgID pgtype.UUID) (map[string]renterDue, error) {
	tenancies, err := s.q.ReportTenancies(ctx, sqlc.ReportTenanciesParams{OrgID: orgID})
	if err != nil {
		return nil, err
	}
	balances, err := s.q.ReportContractBalances(ctx, orgID)
	if err != nil {
		return nil, err
	}
	nextDue, err := s.q.ReportNextDue(ctx, orgID)
	if err != nil {
		return nil, err
	}

	byContract := make(map[string]sqlc.ReportContractBalancesRow, len(balances))
	for _, b := range balances {
		byContract[db.UUIDString(b.ContractID)] = b
	}
	dueByContract := make(map[string]sqlc.ReportNextDueRow, len(nextDue))
	for _, d := range nextDue {
		dueByContract[db.UUIDString(d.ContractID)] = d
	}

	out := make(map[string]renterDue, len(tenancies))
	for _, t := range tenancies {
		contractID := db.UUIDString(t.ContractID)
		renterID := db.UUIDString(t.RenterUserID)
		cur := out[renterID]
		cur.OverdueAmount += byContract[contractID].OverdueAmount
		if d, ok := dueByContract[contractID]; ok && d.DueDate.Valid {
			date := d.DueDate.Time.Format(dateLayout)
			if cur.NextDueDate == nil || date < *cur.NextDueDate {
				amount := d.Amount
				cur.NextDueDate = &date
				cur.NextDueAmount = &amount
			}
		}
		out[renterID] = cur
	}
	return out, nil
}

// renterDueFor is the one-renter form, for the detail header. A failure leaves
// the figures empty rather than failing the page: the renter's identity, KYC
// and link history are what the screen is for, and they are already read.
func (s *Server) renterDueFor(r *http.Request, orgID pgtype.UUID, userID string) renterDue {
	s.flipOverdueFor(r.Context(), orgID)
	figures, err := s.renterDueFigures(r.Context(), orgID)
	if err != nil {
		s.logger.Warn("renter due figures failed", "org_id", db.UUIDString(orgID), "error", err)
		return renterDue{}
	}
	return figures[userID]
}

// unitNextDue maps each unit of the org with a running tenancy to the next date
// money is owed on it — the board chip of §16.3.
func (s *Server) unitNextDue(ctx context.Context, orgID pgtype.UUID) (map[string]string, error) {
	rows, err := s.q.ListUnitNextDue(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		if row.DueDate.Valid {
			out[db.UUIDString(row.UnitID)] = row.DueDate.Time.Format(dateLayout)
		}
	}
	return out, nil
}

// scheduleProof is the `proof` block on the renter's next due instalment: the
// claim they have already filed and are waiting on an answer to.
type scheduleProof struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// mySubmittedProofs maps a renter's instalments to the proof still awaiting a
// decision against each, so the app can show "Awaiting confirmation" rather
// than a stale "overdue" chip.
func (s *Server) mySubmittedProofs(ctx context.Context, userID pgtype.UUID) map[string]scheduleProof {
	rows, err := s.q.ListMySubmittedProofsBySchedule(ctx, userID)
	if err != nil {
		// A proof is a decoration on a screen that must render anyway: a
		// renter who cannot see their own instalments because the proofs table
		// hiccuped is a worse failure than a missing chip.
		s.logger.Warn("submitted proofs lookup failed", "user_id", db.UUIDString(userID), "error", err)
		return nil
	}
	out := make(map[string]scheduleProof, len(rows))
	for _, row := range rows {
		if !row.ScheduleID.Valid {
			continue
		}
		out[db.UUIDString(row.ScheduleID)] = scheduleProof{
			ID: db.UUIDString(row.ID), Status: row.Status,
		}
	}
	return out
}

// containsInt reports whether v is one of the allowed values.
func containsInt(allowed []int, v int) bool {
	for _, a := range allowed {
		if a == v {
			return true
		}
	}
	return false
}
