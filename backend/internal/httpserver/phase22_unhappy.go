package httpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
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

// Phase 22 §22.5 (rest) — the tenancy's unhappy paths: one period waived or
// discounted, the renter's notice to leave, the holdover check after a tenancy
// runs out, and eviction as stages with letters.

const (
	adjustReasonMax        = 200
	defaultEvictionNotice  = 30
	defaultDemandPayWindow = 7
	evictionCloseReasonMax = 200
)

// queueKeyedSMS is queueContractSMS with a caller-chosen dedupe key, for kinds
// a contract may legitimately raise more than once (a second eviction case, a
// notice given again after being withdrawn).
func (s *Server) queueKeyedSMS(ctx context.Context, q *sqlc.Queries, m contractMessage, key string) (string, error) {
	if m.Phone == "" {
		return "", nil
	}
	lang := s.recipientLang(ctx, q, m.UserID, m.Lang)
	id, err := notify.Queue(ctx, q, notify.Msg{
		OrgID: m.OrgID, UserID: m.UserID, Kind: m.Kind, DedupeKey: m.Kind + ":" + key, Phone: m.Phone,
		Body: notify.Render(m.Kind, lang, m.Vars, m.Overrides), Language: lang,
	})
	if errors.Is(err, notify.ErrDuplicate) {
		return "", nil
	}
	return id, err
}

// contractSMS fills the parts of a contractMessage every kind here shares.
func (s *Server) contractSMS(ctx context.Context, row sqlc.GetContractRow, kind string, vars notify.Vars) (contractMessage, error) {
	org, err := s.q.GetOrg(ctx, row.OrgID)
	if err != nil {
		return contractMessage{}, err
	}
	settings := parseSettings(org.Settings)
	brand := s.brandingAssets(ctx, row.OrgID, org.Name)
	vars.Name, vars.Unit, vars.Property, vars.Org = row.RenterName, row.UnitName, row.PropertyName, brand.DisplayName
	vars.PayLink = notify.PayLink(s.cfg.EnduserURL())
	vars.Link = vars.PayLink
	return contractMessage{
		OrgID: db.UUIDString(row.OrgID), UserID: db.UUIDString(row.RenterUserID),
		ContractID: db.UUIDString(row.ID), Kind: kind, Lang: settings.SMSLanguage,
		Phone: db.StrVal(row.RenterPhone), Vars: vars, Overrides: settings.notifyOverrides(),
	}, nil
}

// arrearsDue is what a contract owes on periods past the org's grace days.
func (s *Server) arrearsDue(ctx context.Context, row sqlc.GetContractRow) (int64, error) {
	grace, err := s.orgGraceDays(ctx, row.OrgID)
	if err != nil {
		return 0, err
	}
	return s.q.ArrearsDue(ctx, sqlc.ArrearsDueParams{GraceDays: grace, OrgID: row.OrgID, ContractID: row.ID})
}

func (s *Server) orgGraceDays(ctx context.Context, org pgtype.UUID) (int32, error) {
	o, err := s.q.GetOrg(ctx, org)
	if err != nil {
		return 0, err
	}
	return int32(parseSettings(o.Settings).GraceDays), nil
}

// ------------------------------------------- POST /schedules/{id}/adjust --

// handleAdjustSchedule waives one period (rent relief) or takes a discount
// off it. Money already paid on it stays. One adjustment at a time; undo it to
// change it.
func (s *Server) handleAdjustSchedule(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such schedule")
		return
	}
	var body struct {
		Kind     string `json:"kind"`
		Discount int64  `json:"discount"`
		Reason   string `json:"reason"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	kind := f.OneOf("kind", strings.TrimSpace(body.Kind), "waive", "discount")
	reason := f.MaxLen("reason", f.Required("reason", strings.TrimSpace(body.Reason)), adjustReasonMax)
	row, err := s.q.GetSchedule(r.Context(), sqlc.GetScheduleParams{OrgID: p.OrgID, ID: id})
	if isNoRows(err) {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such schedule")
		return
	}
	if err != nil {
		s.serverError(w, r, "schedule.adjust.get", err)
		return
	}
	if kind == "discount" && (body.Discount < 1 || body.Discount >= row.Amount) {
		f.Add("discount", "above zero and below the period's rent")
	}
	if kind == "waive" {
		body.Discount = 0
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	grace, err := s.orgGraceDays(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "schedule.adjust.org", err)
		return
	}
	var updated sqlc.PaymentSchedule
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		updated, err = q.AdjustSchedule(r.Context(), sqlc.AdjustScheduleParams{
			Kind: &kind, Discount: body.Discount, GraceDays: grace, Reason: &reason,
			ActorUserID: p.UserID, OrgID: p.OrgID, ID: id,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID: p.OrgIDString(), ActorUserID: p.UserIDString(), Action: audit.ActionScheduleAdjust,
			EntityType: audit.EntityContract, EntityID: db.UUIDString(row.ContractID),
			Before: map[string]any{"schedule_id": db.UUIDString(id), "amount": row.Amount, "status": row.Status},
			After:  map[string]any{"kind": kind, "amount": updated.Amount, "status": updated.Status, "reason": reason},
		})
	}); err != nil {
		if isNoRows(err) {
			conflictCode(w, "not_adjustable", "period cannot be adjusted",
				"only an unsettled period with no adjustment yet can be waived or discounted")
			return
		}
		s.serverError(w, r, "schedule.adjust.tx", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"schedule": toSchedule(updated)})
}

func (s *Server) handleUndoScheduleAdjust(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such schedule")
		return
	}
	if _, err := s.q.GetSchedule(r.Context(), sqlc.GetScheduleParams{OrgID: p.OrgID, ID: id}); err != nil {
		if isNoRows(err) {
			httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such schedule")
			return
		}
		s.serverError(w, r, "schedule.adjust_undo.get", err)
		return
	}
	grace, err := s.orgGraceDays(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "schedule.adjust_undo.org", err)
		return
	}
	var updated sqlc.PaymentSchedule
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		updated, err = q.UndoScheduleAdjustment(r.Context(), sqlc.UndoScheduleAdjustmentParams{
			GraceDays: grace, OrgID: p.OrgID, ID: id,
		})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID: p.OrgIDString(), ActorUserID: p.UserIDString(), Action: audit.ActionScheduleAdjustUndo,
			EntityType: audit.EntityContract, EntityID: db.UUIDString(updated.ContractID),
			After: map[string]any{"schedule_id": db.UUIDString(id), "amount": updated.Amount, "status": updated.Status},
		})
	}); err != nil {
		if isNoRows(err) {
			conflictCode(w, "not_adjusted", "nothing to undo", "this period has no adjustment")
			return
		}
		s.serverError(w, r, "schedule.adjust_undo.tx", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"schedule": toSchedule(updated)})
}

// ------------------------------------------------------ notice to leave --

// handleGiveNotice records a renter's notice to leave — from the renter's own
// app (POST /me/contracts/{id}/notice) or entered by the landlord when told in
// person (POST /contracts/{id}/notice). The earliest date is today plus the
// contract's tenant notice days.
func (s *Server) handleGiveNotice(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.loadActionableContract(w, r)
	if !ok {
		return
	}
	var body struct {
		LeaveOn string  `json:"leave_on"`
		Reason  *string `json:"reason"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	leaveOn := requiredDate(f, "leave_on", body.LeaveOn)
	reason := trimmedOpt(f, "reason", body.Reason, 200)
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	if row.Status != contractActive && row.Status != contractExpiring {
		conflictCode(w, "contract_not_active", "contract not running", "notice can only be given on a running tenancy")
		return
	}
	earliest := todayEAT()
	if pol := parsedPolicy(row.Policy); pol != nil {
		earliest = earliest.AddDate(0, 0, pol.TenantNoticeDays)
	}
	if leaveOn.Before(earliest) {
		httpx.WriteProblemExtra(w, http.StatusUnprocessableEntity, "notice_too_short", "notice too short",
			"the contract asks for more notice than that", map[string]any{"earliest": earliest.Format(dateLayout)})
		return
	}
	if !leaveOn.Before(row.EndDate.Time) {
		httpx.WriteProblemExtra(w, http.StatusUnprocessableEntity, "after_end_date", "after the tenancy ends",
			"the tenancy already ends on its end date; no notice is needed", map[string]any{"end_date": row.EndDate.Time.Format(dateLayout)})
		return
	}
	msg, err := s.contractSMS(r.Context(), row, notify.KindNoticeReceived, notify.Vars{Date: leaveOn.Format(dateLayout)})
	if err != nil {
		s.serverError(w, r, "contract.notice.sms", err)
		return
	}
	var notifyID string
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.SetContractNotice(r.Context(), sqlc.SetContractNoticeParams{
			LeaveOn: pgtype.Date{Time: leaveOn, Valid: true}, Reason: reason, OrgID: row.OrgID, ID: row.ID,
		}); err != nil {
			return err
		}
		if err := audit.Record(r.Context(), q, audit.Entry{
			OrgID: db.UUIDString(row.OrgID), ActorUserID: p.UserIDString(), Action: audit.ActionContractNotice,
			EntityType: audit.EntityContract, EntityID: db.UUIDString(row.ID),
			After: map[string]any{"leave_on": leaveOn.Format(dateLayout), "reason": reason, "by": p.Kind},
		}); err != nil {
			return err
		}
		if p.Kind == auth.KindRenter {
			// Phase 24: the landlord's bell.
			if err := s.inbox(r.Context(), q, row.OrgID, p.UserID, inboxItem{
				Kind: "notice_given", Title: "Notice to leave on " + leaveOn.Format(dateLayout) + " — " + row.RenterName,
				Body: row.UnitName + " · " + row.PropertyName, EntityType: "contract", EntityID: row.ID,
				Link: "/contracts/" + db.UUIDString(row.ID),
			}); err != nil {
				return err
			}
		}
		var err error
		notifyID, err = s.queueKeyedSMS(r.Context(), q, msg, db.UUIDString(row.ID)+":"+leaveOn.Format(dateLayout))
		return err
	}); err != nil {
		s.serverError(w, r, "contract.notice.tx", err)
		return
	}
	s.enqueueNotifications(r.Context(), notifyID)
	s.writeContractFor(w, r, p, row.ID)
}

func (s *Server) handleWithdrawNotice(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.loadActionableContract(w, r)
	if !ok {
		return
	}
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.ClearContractNotice(r.Context(), sqlc.ClearContractNoticeParams{OrgID: row.OrgID, ID: row.ID}); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID: db.UUIDString(row.OrgID), ActorUserID: p.UserIDString(), Action: audit.ActionContractNoticeWithdraw,
			EntityType: audit.EntityContract, EntityID: db.UUIDString(row.ID),
		})
	}); err != nil {
		if isNoRows(err) {
			conflictCode(w, "no_notice", "no notice given", "there is no notice on this tenancy to withdraw")
			return
		}
		s.serverError(w, r, "contract.notice_withdraw.tx", err)
		return
	}
	s.writeContractFor(w, r, p, row.ID)
}

// writeContractFor answers with the contract as the caller may see it.
func (s *Server) writeContractFor(w http.ResponseWriter, r *http.Request, p auth.Principal, id pgtype.UUID) {
	orgID, renterID := p.OrgID, pgtype.UUID{}
	if p.Kind == auth.KindRenter {
		orgID, renterID = pgtype.UUID{}, p.UserID
	}
	out, ok := s.reloadContract(w, r, id, orgID, renterID)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"contract": out})
}

// ------------------------------------------------------------- holdover --

func (s *Server) handleListHoldovers(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	rows, err := s.q.ListHoldovers(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "holdovers.list", err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, h := range rows {
		items = append(items, map[string]any{
			"contract_id": db.UUIDString(h.ContractID), "end_date": h.EndDate.Time.Format(dateLayout),
			"renter":  map[string]any{"id": db.UUIDString(h.RenterUserID), "full_name": h.RenterName, "phone": h.RenterPhone},
			"unit_id": db.UUIDString(h.UnitID), "unit_name": h.UnitName, "property_name": h.PropertyName,
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleConfirmMovedOut(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.loadActionableContract(w, r)
	if !ok {
		return
	}
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.ConfirmMovedOut(r.Context(), sqlc.ConfirmMovedOutParams{OrgID: row.OrgID, ID: row.ID}); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID: p.OrgIDString(), ActorUserID: p.UserIDString(), Action: audit.ActionContractMovedOut,
			EntityType: audit.EntityContract, EntityID: db.UUIDString(row.ID),
		})
	}); err != nil {
		if isNoRows(err) {
			conflictCode(w, "not_closed", "tenancy not closed",
				"only an ended or terminated tenancy not already confirmed can be confirmed moved out")
			return
		}
		s.serverError(w, r, "contract.moved_out.tx", err)
		return
	}
	s.writeContractFor(w, r, p, row.ID)
}

// ------------------------------------------------------------- eviction --

type evictionResponse struct {
	ID             string     `json:"id"`
	ContractID     string     `json:"contract_id"`
	Stage          string     `json:"stage"`
	ArrearsAtOpen  int64      `json:"arrears_at_open"`
	ArrearsNow     *int64     `json:"arrears_now,omitempty"`
	NoticeDays     int32      `json:"notice_days"`
	DemandIssuedAt time.Time  `json:"demand_issued_at"`
	PayBy          string     `json:"pay_by"`
	NoticeIssuedAt *time.Time `json:"notice_issued_at"`
	VacateBy       *string    `json:"vacate_by"`
	ClosedAt       *time.Time `json:"closed_at"`
	CloseReason    *string    `json:"close_reason"`
	RenterName     string     `json:"renter_name,omitempty"`
	UnitName       string     `json:"unit_name,omitempty"`
	PropertyName   string     `json:"property_name,omitempty"`
}

func toEviction(e sqlc.EvictionCase) evictionResponse {
	out := evictionResponse{
		ID: db.UUIDString(e.ID), ContractID: db.UUIDString(e.ContractID), Stage: e.Stage,
		ArrearsAtOpen: e.ArrearsAtOpen, NoticeDays: e.NoticeDays, DemandIssuedAt: e.DemandIssuedAt.Time,
		PayBy: e.PayBy.Time.Format(dateLayout), VacateBy: optDateString(e.VacateBy), CloseReason: e.CloseReason,
	}
	if e.NoticeIssuedAt.Valid {
		t := e.NoticeIssuedAt.Time
		out.NoticeIssuedAt = &t
	}
	if e.ClosedAt.Valid {
		t := e.ClosedAt.Time
		out.ClosedAt = &t
	}
	return out
}

// handleOpenEviction is POST /contracts/{id}/eviction {pay_by?}: a written
// demand for the arrears, the first stage. The notice to vacate follows only
// if the demand is not met.
func (s *Server) handleOpenEviction(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.loadActionableContract(w, r)
	if !ok {
		return
	}
	var body struct {
		PayBy string `json:"pay_by"`
	}
	if !DecodeJSONOptional(w, r, &body) {
		return
	}
	f := validate.Fields{}
	payBy := todayEAT().AddDate(0, 0, defaultDemandPayWindow)
	if v := strings.TrimSpace(body.PayBy); v != "" {
		payBy = requiredDate(f, "pay_by", v)
		if payBy.Before(todayEAT()) {
			f.Add("pay_by", "must be today or later")
		}
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	if row.Status != contractActive && row.Status != contractExpiring {
		conflictCode(w, "contract_not_active", "contract not running", "eviction applies to a running tenancy")
		return
	}
	arrears, err := s.arrearsDue(r.Context(), row)
	if err != nil {
		s.serverError(w, r, "eviction.arrears", err)
		return
	}
	if arrears == 0 {
		conflictCode(w, "no_arrears", "nothing owed", "this tenancy owes nothing that is due")
		return
	}
	noticeDays := int32(defaultEvictionNotice)
	if pol := parsedPolicy(row.Policy); pol != nil {
		noticeDays = int32(pol.EvictionNoticeDays)
	}
	msg, err := s.contractSMS(r.Context(), row, notify.KindEvictionDemand, notify.Vars{
		Amount: formatTZS(arrears), Date: payBy.Format(dateLayout),
	})
	if err != nil {
		s.serverError(w, r, "eviction.sms", err)
		return
	}
	var ev sqlc.EvictionCase
	var notifyID string
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		ev, err = q.OpenEvictionCase(r.Context(), sqlc.OpenEvictionCaseParams{
			OrgID: row.OrgID, ContractID: row.ID, Arrears: arrears, NoticeDays: noticeDays,
			PayBy: pgtype.Date{Time: payBy, Valid: true}, ActorUserID: p.UserID,
		})
		if err != nil {
			return err
		}
		if err := s.auditEviction(r.Context(), q, p, ev, "demand"); err != nil {
			return err
		}
		notifyID, err = s.queueKeyedSMS(r.Context(), q, msg, db.UUIDString(ev.ID))
		return err
	}); err != nil {
		if isUnique(err) {
			conflictCode(w, "eviction_open", "a case is already open", "finish or withdraw the open case first")
			return
		}
		s.serverError(w, r, "eviction.open.tx", err)
		return
	}
	s.enqueueNotifications(r.Context(), notifyID)
	WriteJSON(w, http.StatusCreated, map[string]any{"eviction": toEviction(ev)})
}

func (s *Server) auditEviction(ctx context.Context, q *sqlc.Queries, p auth.Principal, ev sqlc.EvictionCase, step string) error {
	return audit.Record(ctx, q, audit.Entry{
		OrgID: p.OrgIDString(), ActorUserID: p.UserIDString(), Action: audit.ActionEviction,
		EntityType: audit.EntityContract, EntityID: db.UUIDString(ev.ContractID),
		After: map[string]any{"case_id": db.UUIDString(ev.ID), "step": step, "stage": ev.Stage,
			"pay_by": ev.PayBy.Time.Format(dateLayout), "vacate_by": optDateString(ev.VacateBy)},
	})
}

// orgEviction loads the {id} case with its contract.
func (s *Server) orgEviction(w http.ResponseWriter, r *http.Request, p auth.Principal) (sqlc.EvictionCase, sqlc.GetContractRow, bool) {
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err == nil {
		ev, gerr := s.q.GetEvictionCase(r.Context(), sqlc.GetEvictionCaseParams{OrgID: p.OrgID, ID: id})
		if gerr == nil {
			row, cerr := s.q.GetContract(r.Context(), sqlc.GetContractParams{ID: ev.ContractID, OrgID: p.OrgID})
			if cerr == nil {
				return ev, row, true
			}
			err = cerr
		} else {
			err = gerr
		}
	}
	if err != nil && !isNoRows(err) {
		if _, parseErr := db.ParseUUID(chi.URLParam(r, "id")); parseErr == nil {
			s.serverError(w, r, "eviction.get", err)
			return sqlc.EvictionCase{}, sqlc.GetContractRow{}, false
		}
	}
	httpx.WriteProblem(w, http.StatusNotFound, "not found", "no such eviction case")
	return sqlc.EvictionCase{}, sqlc.GetContractRow{}, false
}

// handleEvictionNotice moves a demand to a notice to vacate: vacate by today
// plus the contract's eviction notice days.
func (s *Server) handleEvictionNotice(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	ev, row, ok := s.orgEviction(w, r, p)
	if !ok {
		return
	}
	arrears, err := s.arrearsDue(r.Context(), row)
	if err != nil {
		s.serverError(w, r, "eviction.notice.arrears", err)
		return
	}
	vacateBy := todayEAT().AddDate(0, 0, int(ev.NoticeDays))
	msg, err := s.contractSMS(r.Context(), row, notify.KindEvictionNotice, notify.Vars{
		Amount: formatTZS(arrears), Date: vacateBy.Format(dateLayout),
	})
	if err != nil {
		s.serverError(w, r, "eviction.notice.sms", err)
		return
	}
	var notifyID string
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		ev, err = q.EvictionToNotice(r.Context(), sqlc.EvictionToNoticeParams{
			VacateBy: pgtype.Date{Time: vacateBy, Valid: true}, OrgID: p.OrgID, ID: ev.ID,
		})
		if err != nil {
			return err
		}
		if err := s.auditEviction(r.Context(), q, p, ev, "notice"); err != nil {
			return err
		}
		notifyID, err = s.queueKeyedSMS(r.Context(), q, msg, db.UUIDString(ev.ID))
		return err
	}); err != nil {
		if isNoRows(err) {
			conflictCode(w, "not_at_demand", "case not at the demand stage", "a notice follows an open demand")
			return
		}
		s.serverError(w, r, "eviction.notice.tx", err)
		return
	}
	s.enqueueNotifications(r.Context(), notifyID)
	WriteJSON(w, http.StatusOK, map[string]any{"eviction": toEviction(ev)})
}

// handleWithdrawEviction closes an open case — the renter paid, or a deal was
// made — and tells the renter.
func (s *Server) handleWithdrawEviction(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	ev, row, ok := s.orgEviction(w, r, p)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	reason := f.MaxLen("reason", f.Required("reason", strings.TrimSpace(body.Reason)), evictionCloseReasonMax)
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	msg, err := s.contractSMS(r.Context(), row, notify.KindEvictionWithdrawn, notify.Vars{})
	if err != nil {
		s.serverError(w, r, "eviction.withdraw.sms", err)
		return
	}
	var notifyID string
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		ev, err = q.CloseEvictionCase(r.Context(), sqlc.CloseEvictionCaseParams{
			Stage: "withdrawn", Reason: &reason, OrgID: p.OrgID, ID: ev.ID,
		})
		if err != nil {
			return err
		}
		if err := s.auditEviction(r.Context(), q, p, ev, "withdrawn"); err != nil {
			return err
		}
		notifyID, err = s.queueKeyedSMS(r.Context(), q, msg, db.UUIDString(ev.ID))
		return err
	}); err != nil {
		if isNoRows(err) {
			conflictCode(w, "case_closed", "case already closed", "this case is no longer open")
			return
		}
		s.serverError(w, r, "eviction.withdraw.tx", err)
		return
	}
	s.enqueueNotifications(r.Context(), notifyID)
	WriteJSON(w, http.StatusOK, map[string]any{"eviction": toEviction(ev)})
}

// handleContractEvictions is GET /contracts/{id}/eviction: the open case (with
// today's arrears) and every earlier one.
func (s *Server) handleContractEvictions(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	row, ok := s.loadContract(w, r)
	if !ok {
		return
	}
	cases, err := s.q.ListEvictionCasesForContract(r.Context(), sqlc.ListEvictionCasesForContractParams{
		OrgID: row.OrgID, ContractID: row.ID,
	})
	if err != nil {
		s.serverError(w, r, "eviction.list", err)
		return
	}
	var open *evictionResponse
	history := []evictionResponse{}
	for _, c := range cases {
		e := toEviction(c)
		if (c.Stage == "demand" || c.Stage == "notice") && open == nil {
			arrears, err := s.arrearsDue(r.Context(), row)
			if err != nil {
				s.serverError(w, r, "eviction.list.arrears", err)
				return
			}
			e.ArrearsNow = &arrears
			open = &e
			continue
		}
		history = append(history, e)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"open": open, "history": history})
}

// handleListEvictions is GET /evictions: every open case of the org.
func (s *Server) handleListEvictions(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	rows, err := s.q.ListOpenEvictionCases(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "eviction.list_open", err)
		return
	}
	items := make([]evictionResponse, 0, len(rows))
	for _, c := range rows {
		e := toEviction(sqlc.EvictionCase{
			ID: c.ID, OrgID: c.OrgID, ContractID: c.ContractID, Stage: c.Stage, ArrearsAtOpen: c.ArrearsAtOpen,
			NoticeDays: c.NoticeDays, DemandIssuedAt: c.DemandIssuedAt, PayBy: c.PayBy,
			NoticeIssuedAt: c.NoticeIssuedAt, VacateBy: c.VacateBy, ClosedAt: c.ClosedAt, CloseReason: c.CloseReason,
		})
		e.RenterName, e.UnitName, e.PropertyName = c.RenterName, c.UnitName, c.PropertyName
		items = append(items, e)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ------------------------------------------ GET /evictions/{id}/letter --

// letterTexts are the fixed sentences of the two letters, in both languages.
// The letter is evidence (a ward tribunal reads it), so its wording is the
// platform's, not editable per org.
var letterTexts = map[string]map[string]string{
	"en": {
		"demand_title": "Demand for payment of rent arrears",
		"notice_title": "Notice to vacate",
		"to":           "To",
		"premises":     "Premises",
		"date":         "Date",
		"demand_body":  "According to our records you owe the rent set out below. You are required to pay the full amount on or before %s. If it is not paid, a notice to vacate the premises will follow.",
		"notice_body":  "You have not paid the rent arrears set out below despite our demand of %s. You are hereby given notice to vacate the premises on or before %s, as provided in your tenancy agreement.",
		"period":       "Period",
		"due":          "Due",
		"rent":         "Rent",
		"paid":         "Paid",
		"owing":        "Owing",
		"total":        "Total owing",
		"landlord":     "For the landlord",
	},
	"sw": {
		"demand_title": "Madai ya malipo ya malimbikizo ya kodi",
		"notice_title": "Notisi ya kuhama",
		"to":           "Kwa",
		"premises":     "Eneo",
		"date":         "Tarehe",
		"demand_body":  "Kwa mujibu wa kumbukumbu zetu unadaiwa kodi iliyoorodheshwa hapa chini. Unatakiwa kulipa kiasi chote ifikapo tarehe %s. Kisipolipwa, notisi ya kuhama itafuata.",
		"notice_body":  "Hujalipa malimbikizo ya kodi yaliyoorodheshwa hapa chini licha ya madai yetu ya tarehe %s. Kwa barua hii unapewa notisi ya kuhama eneo hili ifikapo tarehe %s, kama ilivyoelezwa katika mkataba wako wa upangaji.",
		"period":       "Kipindi",
		"due":          "Tarehe ya kulipa",
		"rent":         "Kodi",
		"paid":         "Imelipwa",
		"owing":        "Deni",
		"total":        "Jumla ya deni",
		"landlord":     "Kwa niaba ya mwenye nyumba",
	},
}

var letterTemplate = template.Must(template.New("letter").Parse(`<article class="letter">
<header><strong>{{.Org}}</strong><br>{{.T.date}}: {{.Today}}</header>
<h1>{{.Title}}</h1>
<p>{{.T.to}}: <strong>{{.Renter}}</strong><br>{{.T.premises}}: {{.Unit}}, {{.Property}}</p>
<p>{{.Body}}</p>
<table>
<thead><tr><th>{{.T.period}}</th><th>{{.T.due}}</th><th>{{.T.rent}}</th><th>{{.T.paid}}</th><th>{{.T.owing}}</th></tr></thead>
<tbody>{{range .Rows}}<tr><td>{{.Period}}</td><td>{{.Due}}</td><td>{{.Rent}}</td><td>{{.Paid}}</td><td>{{.Owing}}</td></tr>{{end}}</tbody>
<tfoot><tr><td colspan="4">{{.T.total}}</td><td>{{.Total}}</td></tr></tfoot>
</table>
<footer><p>{{.T.landlord}}: ____________________</p></footer>
</article>`))

type letterRow struct{ Period, Due, Rent, Paid, Owing string }

// handleEvictionLetter renders the demand or the notice as printable HTML, in
// Swahili or English, with the arrears statement as of today.
func (s *Server) handleEvictionLetter(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	ev, row, ok := s.orgEviction(w, r, p)
	if !ok {
		return
	}
	f := validate.Fields{}
	qs := r.URL.Query()
	kind := f.OneOf("kind", strings.TrimSpace(qs.Get("kind")), "demand", "notice")
	lang := contract.LangSwahili
	if v := strings.TrimSpace(qs.Get("lang")); v != "" {
		lang = f.OneOf("lang", v, contract.LangSwahili, contract.LangEnglish)
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	if kind == "notice" && !ev.VacateBy.Valid {
		conflictCode(w, "no_notice_yet", "no notice issued", "issue the notice to vacate before printing it")
		return
	}
	schedules, err := s.q.ListSchedulesForContract(r.Context(), sqlc.ListSchedulesForContractParams{
		OrgID: row.OrgID, ContractID: row.ID,
	})
	if err != nil {
		s.serverError(w, r, "eviction.letter.schedules", err)
		return
	}
	today := todayEAT()
	var rows []letterRow
	var total int64
	for _, sc := range schedules {
		if sc.DueDate.Time.After(today) || (sc.Status != "pending" && sc.Status != "partial" && sc.Status != "overdue") {
			continue
		}
		owing := sc.Amount - sc.PaidAmount
		total += owing
		rows = append(rows, letterRow{
			Period: sc.PeriodStart.Time.Format(dateLayout) + " – " + sc.PeriodEnd.Time.Format(dateLayout),
			Due:    sc.DueDate.Time.Format(dateLayout), Rent: formatTZS(sc.Amount),
			Paid: formatTZS(sc.PaidAmount), Owing: formatTZS(owing),
		})
	}
	t := letterTexts[lang]
	org, err := s.q.GetOrg(r.Context(), row.OrgID)
	if err != nil {
		s.serverError(w, r, "eviction.letter.org", err)
		return
	}
	brand := s.brandingAssets(r.Context(), row.OrgID, org.Name)
	title, body := t["demand_title"], sprintf(t["demand_body"], ev.PayBy.Time.Format(dateLayout))
	if kind == "notice" {
		title = t["notice_title"]
		body = sprintf(t["notice_body"], ev.DemandIssuedAt.Time.Format(dateLayout), ev.VacateBy.Time.Format(dateLayout))
	}
	var buf bytes.Buffer
	if err := letterTemplate.Execute(&buf, map[string]any{
		"Org": brand.DisplayName, "Today": today.Format(dateLayout), "Title": title, "Body": body,
		"Renter": row.RenterName, "Unit": row.UnitName, "Property": row.PropertyName,
		"Rows": rows, "Total": formatTZS(total), "T": t,
	}); err != nil {
		s.serverError(w, r, "eviction.letter.render", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"html": buf.String(), "kind": kind, "lang": lang, "total": total})
}

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

func optTime(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}
