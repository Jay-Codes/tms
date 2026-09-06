package httpserver

import (
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
	"tms/backend/internal/validate"
)

// Bounds on one credit movement (API.md Phase 14). The ceiling is not a
// business rule so much as a typo guard: an admin who means 500 and types
// 500000 should be told, not silently believed.
const (
	creditTopupMax = 1_000_000
	creditNoteMax  = 500
	watermarkMax   = 1_000_000
)

// smsLedgerRow is one movement as the admin's SMS tab reads it.
type smsLedgerRow struct {
	Delta          int32     `json:"delta"`
	BalanceAfter   int32     `json:"balance_after"`
	Reason         string    `json:"reason"`
	NotificationID *string   `json:"notification_id"`
	Note           string    `json:"note"`
	AdminName      string    `json:"admin_name"`
	CreatedAt      time.Time `json:"created_at"`
}

// ------------------------------------------------ GET /admin/orgs/{id}/sms --

func (s *Server) handleAdminOrgSMS(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	orgID, ok := s.adminOrgID(w, r)
	if !ok {
		return
	}
	// The org has to exist before it has a balance: a UUID nobody uses must
	// not create a credit row.
	if _, err := s.q.GetOrg(r.Context(), orgID); err != nil {
		if isNoRows(err) {
			adminOrgNotFound(w)
			return
		}
		s.serverError(w, r, "admin.sms.org", err)
		return
	}

	credits, err := s.q.EnsureOrgSMSCredits(r.Context(), orgID)
	if err != nil {
		s.serverError(w, r, "admin.sms.ensure", err)
		return
	}
	used, err := s.q.SMSCreditsUsed30d(r.Context(), orgID)
	if err != nil {
		s.serverError(w, r, "admin.sms.used", err)
		return
	}
	held, err := s.q.CountHeldNotifications(r.Context(), orgID)
	if err != nil {
		s.serverError(w, r, "admin.sms.held", err)
		return
	}
	rows, err := s.q.ListSMSCreditLedger(r.Context(), sqlc.ListSMSCreditLedgerParams{
		OrgID: orgID, RowLimit: notify.DefaultLedgerLimit,
	})
	if err != nil {
		s.serverError(w, r, "admin.sms.ledger", err)
		return
	}
	ledger := make([]smsLedgerRow, 0, len(rows))
	for _, row := range rows {
		item := smsLedgerRow{
			Delta: row.Delta, BalanceAfter: row.BalanceAfter, Reason: row.Reason,
			Note: row.Note, AdminName: row.AdminName, CreatedAt: row.CreatedAt.Time,
		}
		if row.NotificationID.Valid {
			id := db.UUIDString(row.NotificationID)
			item.NotificationID = &id
		}
		ledger = append(ledger, item)
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"balance":       credits.Balance,
		"low_watermark": credits.LowWatermark,
		"used_30d":      used,
		"held_count":    held,
		"ledger":        ledger,
	})
}

// ----------------------------------------- POST /admin/orgs/{id}/sms/topup --

func (s *Server) handleAdminOrgSMSTopup(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	orgID, ok := s.adminOrgID(w, r)
	if !ok {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		Credits int    `json:"credits"`
		Note    string `json:"note"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	if body.Credits < 1 || body.Credits > creditTopupMax {
		f.Add("credits", "must be between 1 and 1000000")
	}
	note := f.MaxLen("note", strings.TrimSpace(body.Note), creditNoteMax)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	var (
		balance  int32
		released []string
	)
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.GetOrg(r.Context(), orgID); err != nil {
			return err
		}
		before, err := notify.EnsureCredits(r.Context(), q, orgID)
		if err != nil {
			return err
		}
		balance, err = notify.Add(r.Context(), q, orgID, body.Credits,
			notify.ReasonTopup, note, p.UserID)
		if err != nil {
			return err
		}
		// The backlog leaves in the order it arrived, oldest first: the renter
		// who has been waiting longest is texted first.
		released, err = notify.ReleaseHeld(r.Context(), q, orgID)
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       db.UUIDString(orgID),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionSMSCreditTopup,
			EntityType:  audit.EntitySMSCredits,
			EntityID:    db.UUIDString(orgID),
			Before:      map[string]any{"balance": before.Balance},
			After: map[string]any{
				"balance": balance, "credits": body.Credits,
				"note": note, "released": len(released),
			},
		})
	}); err != nil {
		if isNoRows(err) {
			adminOrgNotFound(w)
			return
		}
		s.serverError(w, r, "admin.sms.topup", err)
		return
	}

	s.enqueueNotifications(r.Context(), released...)
	WriteJSON(w, http.StatusOK, map[string]any{"balance": balance, "released": len(released)})
}

// ---------------------------------------- POST /admin/orgs/{id}/sms/adjust --

func (s *Server) handleAdminOrgSMSAdjust(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	orgID, ok := s.adminOrgID(w, r)
	if !ok {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		Delta int    `json:"delta"`
		Note  string `json:"note"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	if body.Delta == 0 {
		f.Add("delta", "must not be zero")
	}
	if body.Delta < -creditTopupMax || body.Delta > creditTopupMax {
		f.Add("delta", "must be between -1000000 and 1000000")
	}
	note := f.MaxLen("note", strings.TrimSpace(body.Note), creditNoteMax)
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	var (
		balance   int32
		released  []string
		insuffErr bool
	)
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.GetOrg(r.Context(), orgID); err != nil {
			return err
		}
		before, err := notify.EnsureCredits(r.Context(), q, orgID)
		if err != nil {
			return err
		}
		balance, err = notify.Add(r.Context(), q, orgID, body.Delta,
			notify.ReasonAdjust, note, p.UserID)
		if errors.Is(err, notify.ErrNoCredit) {
			insuffErr = true
			return err
		}
		if err != nil {
			return err
		}
		if body.Delta > 0 {
			if released, err = notify.ReleaseHeld(r.Context(), q, orgID); err != nil {
				return err
			}
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       db.UUIDString(orgID),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionSMSCreditAdjust,
			EntityType:  audit.EntitySMSCredits,
			EntityID:    db.UUIDString(orgID),
			Before:      map[string]any{"balance": before.Balance},
			After: map[string]any{
				"balance": balance, "delta": body.Delta,
				"note": note, "released": len(released),
			},
		})
	}); err != nil {
		if insuffErr {
			// A prepaid balance has no overdraft: the admin is told what the
			// balance actually is rather than being given a negative one.
			f := validate.Fields{}
			f.Add("delta", "would take the balance below zero (current balance "+
				strconv.Itoa(int(balance))+")")
			badRequest(w, f)
			return
		}
		if isNoRows(err) {
			adminOrgNotFound(w)
			return
		}
		s.serverError(w, r, "admin.sms.adjust", err)
		return
	}

	s.enqueueNotifications(r.Context(), released...)
	WriteJSON(w, http.StatusOK, map[string]any{"balance": balance, "released": len(released)})
}

// ---------------------------------------------- PATCH /admin/orgs/{id}/sms --

func (s *Server) handleAdminOrgSMSWatermark(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	orgID, ok := s.adminOrgID(w, r)
	if !ok {
		return
	}
	p := auth.MustFromContext(r.Context())

	var body struct {
		LowWatermark *int `json:"low_watermark"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	if body.LowWatermark == nil {
		f.Add("low_watermark", "low_watermark is required")
	} else if *body.LowWatermark < 0 || *body.LowWatermark > watermarkMax {
		f.Add("low_watermark", "must be between 0 and 1000000")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	var updated sqlc.OrgSmsCredit
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.GetOrg(r.Context(), orgID); err != nil {
			return err
		}
		before, err := notify.EnsureCredits(r.Context(), q, orgID)
		if err != nil {
			return err
		}
		updated, err = q.SetOrgSMSLowWatermark(r.Context(), sqlc.SetOrgSMSLowWatermarkParams{
			OrgID: orgID, LowWatermark: int32(*body.LowWatermark),
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       db.UUIDString(orgID),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionSMSCreditWatermark,
			EntityType:  audit.EntitySMSCredits,
			EntityID:    db.UUIDString(orgID),
			Before:      map[string]any{"low_watermark": before.LowWatermark},
			After:       map[string]any{"low_watermark": updated.LowWatermark},
		})
	}); err != nil {
		if isNoRows(err) {
			adminOrgNotFound(w)
			return
		}
		s.serverError(w, r, "admin.sms.watermark", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"low_watermark": updated.LowWatermark})
}

// ------------------------------------------------------ GET /org/sms-credits --

// handleOrgSMSCredits is the landlord's own view of the balance. It is
// read-only by design: credits are sold by the platform, and the Notifications
// screen shows the number, the warning level and how much is waiting rather
// than a top-up form the landlord cannot use.
func (s *Server) handleOrgSMSCredits(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	credits, err := s.q.EnsureOrgSMSCredits(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "org.sms_credits.ensure", err)
		return
	}
	held, err := s.q.CountHeldNotifications(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "org.sms_credits.held", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"balance":       credits.Balance,
		"low_watermark": credits.LowWatermark,
		"held_count":    held,
		// `low` is the banner's own condition, computed once here so the three
		// apps do not each re-derive it (and disagree at the boundary).
		"low": credits.Balance < credits.LowWatermark,
	})
}

// ------------------------------------------------------------------ shared --

// creditCheck is the bulk pre-check's answer: what a send will cost and what
// the org has.
type creditCheck struct {
	Needed  int
	Balance int32
}

// checkCredits totals the segment cost of a batch and compares it with the
// org's balance, so a landlord is told about a shortfall *before* forty rows
// are queued rather than finding them held afterwards (API.md Phase 14).
//
// It is advisory, not a reservation: the worker's conditional debit is still
// the only thing that decides whether a given message is paid for. Two
// broadcasts racing the same balance can both pass this check and the second
// one's tail is held — which is exactly the behaviour the held state exists
// for.
func (s *Server) checkCredits(
	r *http.Request, orgID pgtype.UUID, bodies []string, exempt bool,
) (creditCheck, bool, error) {
	if exempt {
		return creditCheck{}, true, nil
	}
	needed := 0
	for _, b := range bodies {
		needed += notify.NeededFor(b)
	}
	credits, err := s.q.EnsureOrgSMSCredits(r.Context(), orgID)
	if err != nil {
		return creditCheck{}, false, err
	}
	return creditCheck{Needed: needed, Balance: credits.Balance},
		int(credits.Balance) >= needed, nil
}
