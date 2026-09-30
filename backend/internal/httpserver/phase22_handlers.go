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
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpx"
	"tms/backend/internal/validate"
)

// Phase 22 §22.1 — which contract template a tenancy is written on.
//
// Resolution, first hit wins (ResolveUnitTemplate): the template named at
// approval or creation → the unit's → the property's → the org default. The
// routes here set the middle two and show the answer for one unit.

const (
	templateSourceExplicit = "explicit"
	bulkTemplateMaxUnits   = 500
)

func optUUIDString(id pgtype.UUID) *string {
	if !id.Valid {
		return nil
	}
	s := db.UUIDString(id)
	return &s
}

func optDateString(d pgtype.Date) *string {
	if !d.Valid {
		return nil
	}
	s := d.Time.Format(dateLayout)
	return &s
}

// templateUsage is TemplateUsage keyed by template id.
func (s *Server) templateUsage(ctx context.Context, orgID pgtype.UUID) (map[string]templateUsage, error) {
	rows, err := s.q.TemplateUsage(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]templateUsage, len(rows))
	for _, r := range rows {
		out[db.UUIDString(r.ID)] = templateUsage{Units: r.Units, Properties: r.Properties}
	}
	return out, nil
}

// assignableTemplate reads `template_id` as the assignment routes take it:
// the key is required, `null` clears, and an id must be a live template of the
// caller's org. ok=false means the response has been written.
func (s *Server) assignableTemplate(
	w http.ResponseWriter, r *http.Request, p auth.Principal, f validate.Fields, raw json.RawMessage,
) (pgtype.UUID, bool) {
	// A plain RawMessage, not a pointer: encoding/json sets a pointer to nil
	// for a literal `null`, which would make "clear" read as "absent".
	if len(raw) == 0 {
		f.Add("template_id", "required (null clears the assignment)")
		return pgtype.UUID{}, true
	}
	var v *string
	if err := json.Unmarshal(raw, &v); err != nil {
		f.Add("template_id", "must be a template id or null")
		return pgtype.UUID{}, true
	}
	if v == nil {
		return pgtype.UUID{}, true
	}
	id := uuidField(f, "template_id", *v, true)
	if !id.Valid {
		return pgtype.UUID{}, true
	}
	if _, err := s.q.GetContractTemplate(r.Context(), sqlc.GetContractTemplateParams{OrgID: p.OrgID, ID: id}); err != nil {
		if isNoRows(err) {
			f.Add("template_id", "no such template in this organisation")
			return pgtype.UUID{}, true
		}
		s.serverError(w, r, "template.assign.lookup", err)
		return pgtype.UUID{}, false
	}
	return id, true
}

// -------------------------------------------- POST /units/bulk-template --

// handleBulkUnitTemplate assigns one template to many units, or clears it.
// Ids outside the org are not an error — they match nothing, and `updated`
// says how many did.
func (s *Server) handleBulkUnitTemplate(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	var body struct {
		UnitIDs    []string        `json:"unit_ids"`
		TemplateID json.RawMessage `json:"template_id"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	if len(body.UnitIDs) == 0 || len(body.UnitIDs) > bulkTemplateMaxUnits {
		f.Add("unit_ids", "between 1 and 500 unit ids")
	}
	ids := make([]pgtype.UUID, 0, len(body.UnitIDs))
	for _, raw := range body.UnitIDs {
		id, err := db.ParseUUID(raw)
		if err != nil {
			f.Add("unit_ids", "must all be unit ids")
			break
		}
		ids = append(ids, id)
	}
	templateID, ok := s.assignableTemplate(w, r, p, f, body.TemplateID)
	if !ok {
		return
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}

	var updated []pgtype.UUID
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		updated, err = q.SetUnitsTemplate(r.Context(), sqlc.SetUnitsTemplateParams{
			TemplateID: templateID, OrgID: p.OrgID, UnitIds: ids,
		})
		if err != nil {
			return err
		}
		for _, id := range updated {
			if err := audit.Record(r.Context(), q, audit.Entry{
				OrgID:       p.OrgIDString(),
				ActorUserID: p.UserIDString(),
				Action:      audit.ActionUnitTemplateSet,
				EntityType:  audit.EntityUnit,
				EntityID:    db.UUIDString(id),
				After:       map[string]any{"contract_template_id": optUUIDString(templateID)},
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		s.serverError(w, r, "unit.template.tx", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"updated": len(updated), "contract_template_id": optUUIDString(templateID),
	})
}

// --------------------------------------- PUT /properties/{id}/template --

func (s *Server) handleSetPropertyTemplate(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	current, ok := s.orgProperty(w, r, p)
	if !ok {
		return
	}
	var body struct {
		TemplateID json.RawMessage `json:"template_id"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	templateID, ok := s.assignableTemplate(w, r, p, f, body.TemplateID)
	if !ok {
		return
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.SetPropertyTemplate(r.Context(), sqlc.SetPropertyTemplateParams{
			TemplateID: templateID, OrgID: p.OrgID, ID: current.ID,
		}); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       p.OrgIDString(),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionPropertyTemplateSet,
			EntityType:  audit.EntityProperty,
			EntityID:    db.UUIDString(current.ID),
			Before:      map[string]any{"contract_template_id": optUUIDString(current.ContractTemplateID)},
			After:       map[string]any{"contract_template_id": optUUIDString(templateID)},
		})
	}); err != nil {
		s.serverError(w, r, "property.template.tx", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"contract_template_id": optUUIDString(templateID)})
}

// ------------------------------------------- GET /units/{id}/template --

// handleGetUnitTemplate answers "which template would a tenancy of this unit
// be written on, and why" — the approve sheet shows it before the landlord
// decides whether to pick another.
func (s *Server) handleGetUnitTemplate(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	unit, ok := s.orgUnit(w, r, p)
	if !ok {
		return
	}
	res, err := s.q.ResolveUnitTemplate(r.Context(), sqlc.ResolveUnitTemplateParams{OrgID: p.OrgID, UnitID: unit.ID})
	if isNoRows(err) {
		WriteJSON(w, http.StatusOK, map[string]any{"template": nil})
		return
	}
	if err != nil {
		s.serverError(w, r, "unit.template.resolve", err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"template": map[string]any{
		"id": db.UUIDString(res.ID), "name": res.Name, "source": res.Source,
	}})
}

// ----------------------------------------------- §22.3 reissue unsigned --

const reissueReasonMax = 200

// reissueTx withdraws one unsigned contract and writes it again from the same
// terms — unit, renter, period, term, start, due day, language, application —
// on the given template, or on its own template (now reworded), or on the
// unit's resolved one if that template is gone. One SMS goes out: the new
// contract's `contract_ready`; the withdrawal is not news to the renter.
func (s *Server) reissueTx(
	ctx context.Context, q *sqlc.Queries, row sqlc.GetContractRow, templateID pgtype.UUID, reason, actor string,
) (sqlc.Contract, string, error) {
	if !templateID.Valid && row.TemplateID.Valid {
		if _, err := q.GetContractTemplate(ctx, sqlc.GetContractTemplateParams{
			OrgID: row.OrgID, ID: row.TemplateID,
		}); err == nil {
			templateID = row.TemplateID
		} else if !isNoRows(err) {
			return sqlc.Contract{}, "", err
		}
	}
	withdrawn := "Reissued: " + reason
	if _, err := q.TerminateContract(ctx, sqlc.TerminateContractParams{
		OrgID: row.OrgID, ID: row.ID, TerminationReason: &withdrawn,
		TerminationEffectiveDate: pgtype.Date{Time: todayEAT(), Valid: true},
	}); err != nil {
		return sqlc.Contract{}, "", err
	}
	created, notifyID, err := s.createContractTx(ctx, q, contractInput{
		OrgID: row.OrgID, UnitID: row.UnitID, RenterUserID: row.RenterUserID,
		TemplateID: templateID, PaymentPeriodID: row.PaymentPeriodID, TermDays: row.TermDays,
		StartDate: row.StartDate.Time, DueDay: row.DueDay, LinkRequestID: row.LinkRequestID,
		ActorUserID: actor, Language: row.Language,
	})
	if err != nil {
		return sqlc.Contract{}, "", err
	}
	if err := q.SetContractSupersedes(ctx, sqlc.SetContractSupersedesParams{
		SupersedesContractID: row.ID, OrgID: row.OrgID, ID: created.ID,
	}); err != nil {
		return sqlc.Contract{}, "", err
	}
	if err := audit.Record(ctx, q, audit.Entry{
		OrgID:       db.UUIDString(row.OrgID),
		ActorUserID: actor,
		Action:      audit.ActionContractReissue,
		EntityType:  audit.EntityContract,
		EntityID:    db.UUIDString(row.ID),
		After:       map[string]any{"reason": reason, "new_contract_id": db.UUIDString(created.ID)},
	}); err != nil {
		return sqlc.Contract{}, "", err
	}
	return created, notifyID, nil
}

func reissueReason(f validate.Fields, raw string) string {
	reason := f.MaxLen("reason", strings.TrimSpace(raw), reissueReasonMax)
	if reason == "" {
		reason = "contract wording updated"
	}
	return reason
}

// handleReissueContract is POST /contracts/{id}/reissue {reason?, template_id?}.
func (s *Server) handleReissueContract(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	row, ok := s.loadActionableContract(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason     string `json:"reason"`
		TemplateID string `json:"template_id"`
	}
	if !DecodeJSONOptional(w, r, &body) {
		return
	}
	f := validate.Fields{}
	reason := reissueReason(f, body.Reason)
	templateID := uuidField(f, "template_id", body.TemplateID, false)
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	if row.Status != contractPendingSignature {
		conflictCode(w, "not_pending_signature", "contract already signed or closed",
			"only a contract still awaiting signature can be reissued")
		return
	}
	// A signature — even the renter's alone, before activation — is on the
	// old wording; replacing it silently would discard what they agreed to.
	sigs, err := s.q.ListContractSignatures(r.Context(), sqlc.ListContractSignaturesParams{
		OrgID: row.OrgID, ContractID: row.ID,
	})
	if err != nil {
		s.serverError(w, r, "contract.reissue.signatures", err)
		return
	}
	if len(sigs) > 0 {
		conflictCode(w, "already_signed", "contract already signed",
			"a signed contract is changed by an amendment, not a reissue")
		return
	}
	var created sqlc.Contract
	var notifyID string
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		created, notifyID, err = s.reissueTx(r.Context(), q, row, templateID, reason, p.UserIDString())
		return err
	}); err != nil {
		if writeCreateError(w, err) {
			return
		}
		s.serverError(w, r, "contract.reissue.tx", err)
		return
	}
	s.enqueueNotifications(r.Context(), notifyID)
	out, ok := s.reloadContract(w, r, created.ID, p.OrgID, pgtype.UUID{})
	if !ok {
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"contract": out})
}

// handleReissueStale is POST /contract-templates/{id}/reissue-pending
// {reason?}: every unsigned contract still on the template's old wording is
// reissued, all or none.
func (s *Server) handleReissueStale(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	tpl, ok := s.orgTemplate(w, r, p)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !DecodeJSONOptional(w, r, &body) {
		return
	}
	f := validate.Fields{}
	reason := reissueReason(f, body.Reason)
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	var notifyIDs []string
	var reissued int
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		ids, err := q.ListStalePendingContracts(r.Context(), sqlc.ListStalePendingContractsParams{
			OrgID: p.OrgID, TemplateID: tpl.ID,
		})
		if err != nil {
			return err
		}
		for _, id := range ids {
			row, err := q.GetContract(r.Context(), sqlc.GetContractParams{ID: id, OrgID: p.OrgID})
			if err != nil {
				return err
			}
			_, notifyID, err := s.reissueTx(r.Context(), q, row, pgtype.UUID{}, reason, p.UserIDString())
			if err != nil {
				return err
			}
			notifyIDs = append(notifyIDs, notifyID)
			reissued++
		}
		return nil
	}); err != nil {
		if writeCreateError(w, err) {
			return
		}
		s.serverError(w, r, "template.reissue_pending.tx", err)
		return
	}
	s.enqueueNotifications(r.Context(), notifyIDs...)
	WriteJSON(w, http.StatusOK, map[string]any{"reissued": reissued})
}

// --------------------------------------------------- §22.4 amendments --

const amendReasonMax = 200

var errAmendmentStale = errors.New("contract: the amended contract is no longer running")

// handleAmendContract is POST /contracts/{id}/amend: a new contract, pre-filled
// from this running one with the changes applied, for the renter to sign
// again. This one keeps collecting until the amendment activates. A renewal is
// an amendment whose effective date is this contract's end date.
func (s *Server) handleAmendContract(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	old, ok := s.loadActionableContract(w, r)
	if !ok {
		return
	}
	var body struct {
		EffectiveDate   string `json:"effective_date"`
		Reason          string `json:"reason"`
		RentAmount      *int64 `json:"rent_amount"`
		RentPeriodDays  *int32 `json:"rent_period_days"`
		PaymentPeriodID string `json:"payment_period_id"`
		DueDay          *int32 `json:"due_day"`
		TermDays        *int32 `json:"term_days"`
		TemplateID      string `json:"template_id"`
		Language        string `json:"language"`
	}
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	effective := requiredDate(f, "effective_date", body.EffectiveDate)
	reason := f.MaxLen("reason", f.Required("reason", strings.TrimSpace(body.Reason)), amendReasonMax)
	in := contractInput{
		OrgID: old.OrgID, UnitID: old.UnitID, RenterUserID: old.RenterUserID,
		PaymentPeriodID: old.PaymentPeriodID, DueDay: old.DueDay, Language: old.Language,
		ActorUserID: p.UserIDString(), Amends: old.ID, AmendReason: reason,
		RentAmount: old.RentAmount, RentPeriodDays: old.RentPeriodDays,
	}
	if body.RentAmount != nil {
		if *body.RentAmount < 1 {
			f.Add("rent_amount", "must be above zero")
		}
		in.RentAmount = *body.RentAmount
	}
	if body.RentPeriodDays != nil {
		if *body.RentPeriodDays < 1 || *body.RentPeriodDays > 366 {
			f.Add("rent_period_days", "between 1 and 366 days")
		}
		in.RentPeriodDays = *body.RentPeriodDays
	}
	if v := uuidField(f, "payment_period_id", body.PaymentPeriodID, false); v.Valid {
		in.PaymentPeriodID = v
	}
	if body.DueDay != nil {
		if *body.DueDay < 1 || *body.DueDay > 31 {
			f.Add("due_day", "between 1 and 31")
		}
		in.DueDay = body.DueDay
	}
	in.TemplateID = uuidField(f, "template_id", body.TemplateID, false)
	if l := strings.TrimSpace(body.Language); l != "" {
		in.Language = f.OneOf("language", l, "sw", "en")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	// A tenancy that ran out may still be renewed from its end date (the
	// holdover case): the renter never left.
	renewEnded := old.Status == contractEnded && !old.SupersededByContractID.Valid &&
		effective.Format(dateLayout) == old.EndDate.Time.Format(dateLayout)
	if old.Status != contractActive && old.Status != contractExpiring && !renewEnded {
		conflictCode(w, "contract_not_active", "contract not running",
			"only a running contract can be amended, or an ended one renewed from its end date")
		return
	}
	in.AmendsClosed = renewEnded

	// The effective date must open one of this contract's own periods (or be
	// its end, for a renewal): a date inside a period would charge that period
	// twice, once on each contract.
	schedules, err := s.q.ListSchedulesForContract(r.Context(), sqlc.ListSchedulesForContractParams{
		OrgID: old.OrgID, ContractID: old.ID,
	})
	if err != nil {
		s.serverError(w, r, "contract.amend.schedules", err)
		return
	}
	today := todayEAT()
	allowed := []string{}
	valid := false
	for _, sc := range schedules {
		if sc.PeriodStart.Time.Before(today) {
			continue
		}
		d := sc.PeriodStart.Time.Format(dateLayout)
		allowed = append(allowed, d)
		valid = valid || d == effective.Format(dateLayout)
	}
	end := old.EndDate.Time.Format(dateLayout)
	allowed = append(allowed, end)
	valid = valid || end == effective.Format(dateLayout)
	if !valid {
		if len(allowed) > 6 {
			allowed = allowed[:6]
		}
		httpx.WriteProblemExtra(w, http.StatusUnprocessableEntity, "effective_not_period_start",
			"effective date must start a period",
			"an amendment takes effect on the first day of one of this contract's periods, or on its end date to renew",
			map[string]any{"allowed": allowed})
		return
	}
	in.AmendsEffective = effective
	in.StartDate = effective

	// Term: to the old end date, unless the landlord sets one; a renewal
	// (effective on the end date) defaults to the old term again.
	switch {
	case body.TermDays != nil:
		if *body.TermDays < 1 || *body.TermDays > termDaysMax {
			f.Add("term_days", "must be a whole number of days between 1 and 3650")
			badRequest(w, f)
			return
		}
		in.TermDays = *body.TermDays
	case effective.Before(old.EndDate.Time):
		in.TermDays = int32(old.EndDate.Time.Sub(effective).Hours() / 24)
	default:
		in.TermDays = old.TermDays
	}

	// The template: the one named, else this contract's own if still live,
	// else whatever the unit resolves to now.
	if !in.TemplateID.Valid && old.TemplateID.Valid {
		if _, err := s.q.GetContractTemplate(r.Context(), sqlc.GetContractTemplateParams{
			OrgID: old.OrgID, ID: old.TemplateID,
		}); err == nil {
			in.TemplateID = old.TemplateID
		}
	}

	var created sqlc.Contract
	var notifyID string
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		created, notifyID, err = s.createContractTx(r.Context(), q, in)
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID:       db.UUIDString(old.OrgID),
			ActorUserID: p.UserIDString(),
			Action:      audit.ActionContractAmend,
			EntityType:  audit.EntityContract,
			EntityID:    db.UUIDString(old.ID),
			Before: map[string]any{
				"rent_amount": old.RentAmount, "rent_period_days": old.RentPeriodDays,
				"end_date": end, "due_day": old.DueDay,
			},
			After: map[string]any{
				"amendment_id": db.UUIDString(created.ID), "effective_date": effective.Format(dateLayout),
				"reason": reason, "rent_amount": in.RentAmount, "rent_period_days": in.RentPeriodDays,
				"term_days": in.TermDays, "due_day": in.DueDay,
			},
		})
	}); err != nil {
		if writeCreateError(w, err) {
			return
		}
		s.serverError(w, r, "contract.amend.tx", err)
		return
	}
	s.enqueueNotifications(r.Context(), notifyID)
	out, ok := s.reloadContract(w, r, created.ID, p.OrgID, pgtype.UUID{})
	if !ok {
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"contract": out})
}

// supersedeTx is the second half of activating an amendment, inside the
// activation's transaction: the old contract's periods from the effective date
// are waived, money already paid on them moves to the new contract's periods
// in order (the same payments, re-allocated), and the old contract stops the
// day before — at once if that day has passed, else by the lifecycle job.
func (s *Server) supersedeTx(
	ctx context.Context, q *sqlc.Queries, amendment sqlc.GetContractRow,
	fresh []sqlc.PaymentSchedule, actor string,
) (int64, error) {
	org, oldID := amendment.OrgID, amendment.SupersedesContractID
	effective := amendment.AmendmentEffectiveDate.Time
	locked, err := q.LockContract(ctx, sqlc.LockContractParams{OrgID: org, ID: oldID})
	if err != nil {
		return 0, err
	}
	if locked.Status == contractEnded && effective.Equal(amendmentOldEnd(ctx, q, org, oldID)) {
		// A renewal of a tenancy that had already run out: nothing to waive
		// or move, only the link.
		return 0, q.LinkRenewalOfEnded(ctx, sqlc.LinkRenewalOfEndedParams{NewID: amendment.ID, OrgID: org, ID: oldID})
	}
	if locked.Status != contractActive && locked.Status != contractExpiring {
		return 0, errAmendmentStale
	}
	oldRows, err := q.LockSchedulesForContract(ctx, sqlc.LockSchedulesForContractParams{
		OrgID: org, ContractID: oldID,
	})
	if err != nil {
		return 0, err
	}
	allocs, err := q.ListAllocationsFrom(ctx, sqlc.ListAllocationsFromParams{
		OrgID: org, ContractID: oldID, FromDate: pgtype.Date{Time: effective, Valid: true},
	})
	if err != nil {
		return 0, err
	}

	// Carry: walk the new periods in order, filling each before the next.
	paid := make([]int64, len(fresh))
	moved := map[string]int64{}
	var carried int64
	next := 0
	for _, a := range allocs {
		left := a.Amount
		for left > 0 && next < len(fresh) {
			room := fresh[next].Amount - paid[next]
			if room <= 0 {
				next++
				continue
			}
			take := min(left, room)
			if _, err := q.CreatePaymentAllocation(ctx, sqlc.CreatePaymentAllocationParams{
				OrgID: org, PaymentID: a.PaymentID, ScheduleID: fresh[next].ID, Amount: take,
			}); err != nil {
				return 0, err
			}
			paid[next] += take
			if _, err := q.ApplyPaymentToSchedule(ctx, sqlc.ApplyPaymentToScheduleParams{
				PaidAmount: paid[next], OrgID: org, ID: fresh[next].ID,
			}); err != nil {
				return 0, err
			}
			left -= take
		}
		took := a.Amount - left
		if took == 0 {
			continue
		}
		carried += took
		moved[db.UUIDString(a.ScheduleID)] += took
		if left == 0 {
			err = q.DeletePaymentAllocation(ctx, sqlc.DeletePaymentAllocationParams{OrgID: org, ID: a.ID})
		} else {
			err = q.ShrinkPaymentAllocation(ctx, sqlc.ShrinkPaymentAllocationParams{Amount: left, OrgID: org, ID: a.ID})
		}
		if err != nil {
			return 0, err
		}
	}

	waived := 0
	for _, row := range oldRows {
		if row.PeriodStart.Time.Before(effective) || row.Status == "waived" || row.Status == "written_off" {
			continue
		}
		if err := q.WaiveScheduleFrom(ctx, sqlc.WaiveScheduleFromParams{
			PaidAmount: row.PaidAmount - moved[db.UUIDString(row.ID)], OrgID: org, ID: row.ID,
		}); err != nil {
			return 0, err
		}
		waived++
	}

	reason := "Superseded by amendment from " + effective.Format(dateLayout)
	if amendment.AmendmentReason != nil {
		reason += ": " + *amendment.AmendmentReason
	}
	if _, err := q.SupersedeContract(ctx, sqlc.SupersedeContractParams{
		NewID: amendment.ID, LastDay: pgtype.Date{Time: effective.AddDate(0, 0, -1), Valid: true},
		Reason: &reason, OrgID: org, ID: oldID,
	}); err != nil {
		return 0, err
	}
	return carried, audit.Record(ctx, q, audit.Entry{
		OrgID:       db.UUIDString(org),
		ActorUserID: actor,
		Action:      audit.ActionContractSupersede,
		EntityType:  audit.EntityContract,
		EntityID:    db.UUIDString(oldID),
		After: map[string]any{
			"superseded_by": db.UUIDString(amendment.ID), "effective_date": effective.Format(dateLayout),
			"waived": waived, "carried": carried,
		},
	})
}

// amendmentOldEnd is the end date of the contract an amendment replaces.
func amendmentOldEnd(ctx context.Context, q *sqlc.Queries, org, id pgtype.UUID) time.Time {
	row, err := q.GetContract(ctx, sqlc.GetContractParams{ID: id, OrgID: org})
	if err != nil {
		return time.Time{}
	}
	return row.EndDate.Time
}
