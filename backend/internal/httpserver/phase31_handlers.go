package httpserver

import (
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

// Phase 31: a change to a running contract needs an owner's approval
// (maker-checker). An amendment is a contract row in status `draft` while it
// is reviewed; `amendment_stage` says where it is. A manager or owner drafts,
// an owner approves (their own draft too), and only then does the renter see
// it — with the note in their own language — and sign or decline.
const (
	amendStageDraft     = "draft"
	amendStageSubmitted = "submitted"
	amendStageApproved  = "approved"
	amendStageRejected  = "rejected"
	amendStageDeclined  = "declined"
	amendStageWithdrawn = "withdrawn"

	amendReasonMax = 200
	amendNoteMax   = 200
)

// amendBody is the change a landlord asks for. On `amend` an absent field is
// carried from the running contract; on `PATCH …/amendment` it keeps the
// draft's value.
type amendBody struct {
	EffectiveDate   *string `json:"effective_date"`
	Reason          *string `json:"reason"`
	RentAmount      *int64  `json:"rent_amount"`
	RentPeriodDays  *int32  `json:"rent_period_days"`
	PaymentPeriodID *string `json:"payment_period_id"`
	DueDay          *int32  `json:"due_day"`
	TermDays        *int32  `json:"term_days"`
	TemplateID      *string `json:"template_id"`
	Language        *string `json:"language"`
	NoteSW          *string `json:"note_sw"`
	NoteEN          *string `json:"note_en"`
	// BodyHTML is this contract's own wording in template form; "" goes back
	// to rendering the template.
	BodyHTML *string `json:"body_html"`
}

// apply validates the present fields onto in. Effective date and term are the
// caller's, because their defaults depend on the running contract.
func (b amendBody) apply(f validate.Fields, in *contractInput) {
	if b.RentAmount != nil {
		if *b.RentAmount < 1 {
			f.Add("rent_amount", "must be above zero")
		}
		in.RentAmount = *b.RentAmount
	}
	if b.RentPeriodDays != nil {
		if *b.RentPeriodDays < 1 || *b.RentPeriodDays > 366 {
			f.Add("rent_period_days", "between 1 and 366 days")
		}
		in.RentPeriodDays = *b.RentPeriodDays
	}
	if b.PaymentPeriodID != nil {
		if v := uuidField(f, "payment_period_id", *b.PaymentPeriodID, false); v.Valid {
			in.PaymentPeriodID = v
		}
	}
	if b.DueDay != nil {
		if *b.DueDay < 1 || *b.DueDay > 31 {
			f.Add("due_day", "between 1 and 31")
		}
		in.DueDay = b.DueDay
	}
	if b.TemplateID != nil {
		in.TemplateID = uuidField(f, "template_id", *b.TemplateID, false)
	}
	if b.Language != nil {
		if l := strings.TrimSpace(*b.Language); l != "" {
			in.Language = f.OneOf("language", l, notify.LangSwahili, notify.LangEnglish)
		}
	}
	if b.TermDays != nil && (*b.TermDays < 1 || *b.TermDays > termDaysMax) {
		f.Add("term_days", "must be a whole number of days between 1 and 3650")
	}
	if b.NoteSW != nil {
		in.NoteSW = strings.TrimSpace(*b.NoteSW)
	}
	if b.NoteEN != nil {
		in.NoteEN = strings.TrimSpace(*b.NoteEN)
	}
	f.MaxLen("note_sw", f.Required("note_sw", in.NoteSW), amendNoteMax)
	f.MaxLen("note_en", f.Required("note_en", in.NoteEN), amendNoteMax)
	if b.BodyHTML != nil {
		in.BodyHTML = templateBody(f, "body_html", *b.BodyHTML, false)
	}
	if b.Reason != nil {
		in.AmendReason = f.MaxLen("reason", strings.TrimSpace(*b.Reason), amendReasonMax)
	}
}

// noteFor is the note in lang, else the other one.
func noteFor(lang, sw, en string) string {
	if lang == notify.LangEnglish && en != "" || sw == "" {
		return en
	}
	return sw
}

// renewsEnded: renewing a tenancy that already ran out, from its end date.
func renewsEnded(old sqlc.GetContractRow, effective time.Time) bool {
	return old.Status == contractEnded && !old.SupersededByContractID.Valid &&
		effective.Format(dateLayout) == old.EndDate.Time.Format(dateLayout)
}

// checkAmendable answers 409 unless old may be amended from effective: a
// running contract, or an ended one renewed from its end date.
func checkAmendable(w http.ResponseWriter, old sqlc.GetContractRow, effective time.Time) bool {
	if old.Status == contractActive || old.Status == contractExpiring || renewsEnded(old, effective) {
		return true
	}
	conflictCode(w, "contract_not_active", "contract not running",
		"only a running contract can be amended, or an ended one renewed from its end date")
	return false
}

// checkAmendEffective answers 422 unless effective opens one of old's periods
// on or after today, or is its end date (a renewal): a date inside a period
// would charge that period twice, once on each contract.
func (s *Server) checkAmendEffective(
	w http.ResponseWriter, r *http.Request, old sqlc.GetContractRow, effective time.Time,
) bool {
	schedules, err := s.q.ListSchedulesForContract(r.Context(), sqlc.ListSchedulesForContractParams{
		OrgID: old.OrgID, ContractID: old.ID,
	})
	if err != nil {
		s.serverError(w, r, "contract.amend.schedules", err)
		return false
	}
	today := todayEAT()
	want := effective.Format(dateLayout)
	allowed := []string{}
	for _, sc := range schedules {
		if sc.PeriodStart.Time.Before(today) {
			continue
		}
		d := sc.PeriodStart.Time.Format(dateLayout)
		allowed = append(allowed, d)
		if d == want {
			return true
		}
	}
	end := old.EndDate.Time.Format(dateLayout)
	if end == want {
		return true
	}
	allowed = append(allowed, end)
	if len(allowed) > 6 {
		allowed = allowed[:6]
	}
	httpx.WriteProblemExtra(w, http.StatusUnprocessableEntity, "effective_not_period_start",
		"effective date must start a period",
		"an amendment takes effect on the first day of one of this contract's periods, or on its end date to renew",
		map[string]any{"allowed": allowed})
	return false
}

// amendTerm is the term when the landlord sets none: to the old end date, or
// the old term again for a renewal.
func amendTerm(old sqlc.GetContractRow, effective time.Time) int32 {
	if effective.Before(old.EndDate.Time) {
		return int32(old.EndDate.Time.Sub(effective).Hours() / 24)
	}
	return old.TermDays
}

// amendAudit is the before/after the amendment audit rows carry.
func amendAudit(row sqlc.GetContractRow) map[string]any {
	return map[string]any{
		"rent_amount": row.RentAmount, "rent_period_days": row.RentPeriodDays,
		"term_days": row.TermDays, "due_day": row.DueDay,
		"start_date": row.StartDate.Time.Format(dateLayout), "end_date": row.EndDate.Time.Format(dateLayout),
		"payment_period_id": db.UUIDString(row.PaymentPeriodID), "template_id": db.UUIDString(row.TemplateID),
		"language": row.Language, "custom_wording": row.AmendmentBodyHtml != nil,
		"note_sw": db.StrVal(row.AmendmentNoteSw), "note_en": db.StrVal(row.AmendmentNoteEn),
	}
}

// ------------------------------------------- POST /contracts/{id}/amend --

// handleAmendContract writes an amendment draft of this running contract,
// pre-filled from it with the changes applied (§22.4, Phase 31). This one
// keeps collecting until the amendment activates; nothing reaches the renter
// until an owner approves it. A renewal is an amendment whose effective date
// is this contract's end date.
func (s *Server) handleAmendContract(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	old, ok := s.loadActionableContract(w, r)
	if !ok {
		return
	}
	var body amendBody
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	effective := requiredDate(f, "effective_date", deref(body.EffectiveDate))
	in := contractInput{
		OrgID: old.OrgID, UnitID: old.UnitID, RenterUserID: old.RenterUserID,
		PaymentPeriodID: old.PaymentPeriodID, DueDay: old.DueDay, Language: old.Language,
		ActorUserID: p.UserIDString(), Amends: old.ID,
		RentAmount: old.RentAmount, RentPeriodDays: old.RentPeriodDays,
	}
	body.apply(f, &in)
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	if !checkAmendable(w, old, effective) || !s.checkAmendEffective(w, r, old, effective) {
		return
	}
	in.AmendsClosed = renewsEnded(old, effective)
	in.AmendsEffective, in.StartDate = effective, effective
	in.TermDays = amendTerm(old, effective)
	if body.TermDays != nil {
		in.TermDays = *body.TermDays
	}
	if in.AmendReason == "" {
		in.AmendReason = noteFor(in.Language, in.NoteSW, in.NoteEN)
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
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		var err error
		created, _, err = s.createContractTx(r.Context(), q, in)
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
				"end_date": old.EndDate.Time.Format(dateLayout), "due_day": old.DueDay,
			},
			After: map[string]any{
				"amendment_id": db.UUIDString(created.ID), "effective_date": effective.Format(dateLayout),
				"reason": in.AmendReason, "rent_amount": in.RentAmount, "rent_period_days": in.RentPeriodDays,
				"term_days": in.TermDays, "due_day": in.DueDay, "stage": amendStageDraft,
				"note_sw": in.NoteSW, "note_en": in.NoteEN, "custom_wording": in.BodyHTML != "",
			},
		})
	}); err != nil {
		if writeCreateError(w, err) {
			return
		}
		s.serverError(w, r, "contract.amend.tx", err)
		return
	}
	out, ok := s.reloadContract(w, r, created.ID, p.OrgID, pgtype.UUID{})
	if !ok {
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"contract": out})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// loadAmendment resolves {id} to an amendment of this org and the contract it
// amends.
func (s *Server) loadAmendment(
	w http.ResponseWriter, r *http.Request,
) (draft, old sqlc.GetContractRow, ok bool) {
	draft, ok = s.loadContract(w, r)
	if !ok {
		return draft, old, false
	}
	if !draft.SupersedesContractID.Valid || !draft.AmendmentEffectiveDate.Valid || draft.AmendmentStage == nil {
		conflictCode(w, "not_an_amendment", "not an amendment", "this contract does not amend another")
		return draft, old, false
	}
	old, err := s.q.GetContract(r.Context(), sqlc.GetContractParams{
		ID: draft.SupersedesContractID, OrgID: draft.OrgID,
	})
	if err != nil {
		s.serverError(w, r, "contract.amendment.old", err)
		return draft, old, false
	}
	return draft, old, true
}

func amendStageOf(row sqlc.GetContractRow) string { return db.StrVal(row.AmendmentStage) }

func requireOwner(w http.ResponseWriter, p auth.Principal) bool {
	if p.HasRole(auth.RoleOwner) {
		return true
	}
	httpx.WriteProblemCode(w, http.StatusForbidden, "owner_required", "owner approval required",
		"only an owner of this organisation may approve, return or reject a contract change")
	return false
}

func amendmentNotInStage(w http.ResponseWriter) {
	conflictCode(w, "amendment_stage", "not possible at this stage",
		"this contract change has moved on; reload it")
}

// ------------------------------------- PATCH /contracts/{id}/amendment --

// handlePatchAmendment edits a draft: any org member while it is a draft, an
// owner while it awaits approval. The document is rendered again.
func (s *Server) handlePatchAmendment(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	draft, old, ok := s.loadAmendment(w, r)
	if !ok {
		return
	}
	stage := amendStageOf(draft)
	switch {
	case draft.Status != contractDraft:
		amendmentNotInStage(w)
		return
	case stage == amendStageSubmitted && !p.HasRole(auth.RoleOwner):
		conflictCode(w, "amendment_submitted", "awaiting approval",
			"a change awaiting approval can only be edited by an owner, or returned to you")
		return
	case stage != amendStageDraft && stage != amendStageSubmitted:
		amendmentNotInStage(w)
		return
	}
	var body amendBody
	if !DecodeJSON(w, r, &body) {
		return
	}
	f := validate.Fields{}
	effective := draft.AmendmentEffectiveDate.Time
	if body.EffectiveDate != nil {
		effective = requiredDate(f, "effective_date", *body.EffectiveDate)
	}
	in := contractInput{
		OrgID: draft.OrgID, UnitID: draft.UnitID, RenterUserID: draft.RenterUserID,
		TemplateID: draft.TemplateID, PaymentPeriodID: draft.PaymentPeriodID,
		TermDays: draft.TermDays, DueDay: draft.DueDay, Language: draft.Language,
		ActorUserID: p.UserIDString(), Amends: old.ID,
		AmendReason: db.StrVal(draft.AmendmentReason),
		RentAmount:  draft.RentAmount, RentPeriodDays: draft.RentPeriodDays,
		BodyHTML: db.StrVal(draft.AmendmentBodyHtml),
		NoteSW:   db.StrVal(draft.AmendmentNoteSw), NoteEN: db.StrVal(draft.AmendmentNoteEn),
	}
	body.apply(f, &in)
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	if !checkAmendable(w, old, effective) || !s.checkAmendEffective(w, r, old, effective) {
		return
	}
	if !effective.Equal(draft.AmendmentEffectiveDate.Time) && body.TermDays == nil {
		in.TermDays = amendTerm(old, effective)
	}
	if body.TermDays != nil {
		in.TermDays = *body.TermDays
	}
	if body.Reason == nil && (body.NoteSW != nil || body.NoteEN != nil || body.Language != nil) {
		in.AmendReason = noteFor(in.Language, in.NoteSW, in.NoteEN)
	}
	in.AmendsClosed = renewsEnded(old, effective)
	in.AmendsEffective, in.StartDate = effective, effective

	stages := []string{amendStageDraft}
	if p.HasRole(auth.RoleOwner) {
		stages = append(stages, amendStageSubmitted)
	}
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		b, err := s.buildContract(r.Context(), q, in)
		if err != nil {
			return err
		}
		c := b.Params
		var policy []byte
		if b.PolicyCanonical != "" {
			policy = []byte(b.PolicyCanonical)
		}
		if _, err := q.UpdateAmendmentDraft(r.Context(), sqlc.UpdateAmendmentDraftParams{
			TemplateID: c.TemplateID, TermsSnapshotHtml: c.TermsSnapshotHtml,
			RentAmount: c.RentAmount, RentPeriodDays: c.RentPeriodDays,
			PaymentPeriodID: c.PaymentPeriodID, PaymentPeriodDays: c.PaymentPeriodDays,
			PaymentPeriodMonths: c.PaymentPeriodMonths, TermDays: c.TermDays,
			StartDate: c.StartDate, EndDate: c.EndDate, DueDay: c.DueDay,
			SnapshotHash: c.SnapshotHash, Language: db.StrVal(c.Language), Policy: policy,
			AmendmentEffectiveDate: c.AmendmentEffectiveDate, AmendmentReason: c.AmendmentReason,
			AmendmentNoteSw: c.AmendmentNoteSw, AmendmentNoteEn: c.AmendmentNoteEn,
			AmendmentBodyHtml: c.AmendmentBodyHtml,
			OrgID:             draft.OrgID, ID: draft.ID, Stages: stages,
		}); err != nil {
			return err
		}
		fresh, err := q.GetContract(r.Context(), sqlc.GetContractParams{ID: draft.ID, OrgID: draft.OrgID})
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID: db.UUIDString(draft.OrgID), ActorUserID: p.UserIDString(),
			Action: audit.ActionContractAmendUpdate, EntityType: audit.EntityContract,
			EntityID: db.UUIDString(draft.ID),
			Before:   amendAudit(draft), After: amendAudit(fresh),
		})
	}); err != nil {
		if isNoRows(err) {
			amendmentNotInStage(w)
			return
		}
		if writeCreateError(w, err) {
			return
		}
		s.serverError(w, r, "contract.amendment.update", err)
		return
	}
	s.writeContract(w, r, draft.ID, p.OrgID)
}

func (s *Server) writeContract(w http.ResponseWriter, r *http.Request, id, orgID pgtype.UUID) {
	out, ok := s.reloadContract(w, r, id, orgID, pgtype.UUID{})
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"contract": out})
}

// amendmentInbox is the org's bell item for a step in the review.
func (s *Server) amendmentInbox(
	r *http.Request, q *sqlc.Queries, row sqlc.GetContractRow, actor pgtype.UUID, kind, title, body string,
) error {
	text := row.UnitName + " · " + row.PropertyName + " — " + row.RenterName
	if body != "" {
		text += ". " + body
	}
	return s.inbox(r.Context(), q, row.OrgID, actor, inboxItem{
		Kind: kind, Title: title, Body: text, EntityType: "contract", EntityID: row.ID,
		Link: "/contracts/" + db.UUIDString(row.ID),
	})
}

// ------------------------------- POST /contracts/{id}/amendment/submit --

func (s *Server) handleSubmitAmendment(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	draft, _, ok := s.loadAmendment(w, r)
	if !ok {
		return
	}
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.SubmitAmendment(r.Context(), sqlc.SubmitAmendmentParams{
			Actor: p.UserID, OrgID: draft.OrgID, ID: draft.ID,
		}); err != nil {
			return err
		}
		if err := s.amendmentInbox(r, q, draft, p.UserID, "amendment_submitted",
			"Contract change awaiting approval", db.StrVal(draft.AmendmentNoteEn)); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID: db.UUIDString(draft.OrgID), ActorUserID: p.UserIDString(),
			Action: audit.ActionContractAmendSubmit, EntityType: audit.EntityContract,
			EntityID: db.UUIDString(draft.ID),
			Before:   map[string]any{"stage": amendStageDraft},
			After:    map[string]any{"stage": amendStageSubmitted},
		})
	}); err != nil {
		if isNoRows(err) {
			amendmentNotInStage(w)
			return
		}
		s.serverError(w, r, "contract.amendment.submit", err)
		return
	}
	s.writeContract(w, r, draft.ID, p.OrgID)
}

// ------------------------------ POST /contracts/{id}/amendment/approve --

// handleApproveAmendment is the owner's yes: the draft goes to the renter,
// with an SMS carrying the note in the renter's own language. An owner may
// approve a draft they wrote themselves without submitting it first.
func (s *Server) handleApproveAmendment(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	if !requireOwner(w, p) {
		return
	}
	draft, old, ok := s.loadAmendment(w, r)
	if !ok {
		return
	}
	stage := amendStageOf(draft)
	ownDraft := stage == amendStageDraft && draft.AmendmentDraftedBy == p.UserID
	if draft.Status != contractDraft || (stage != amendStageSubmitted && !ownDraft) {
		conflictCode(w, "amendment_not_submitted", "not awaiting approval",
			"only a change submitted for approval, or your own draft, can be approved")
		return
	}
	effective := draft.AmendmentEffectiveDate.Time
	if !renewsEnded(old, effective) && old.Status != contractActive && old.Status != contractExpiring {
		conflictCode(w, "amendment_stale", "amended contract no longer running",
			"the contract this change amends has ended or been terminated; withdraw the change")
		return
	}
	if !s.checkAmendEffective(w, r, old, effective) {
		return
	}
	org, err := s.q.GetOrg(r.Context(), draft.OrgID)
	if err != nil {
		s.serverError(w, r, "contract.amendment.approve.org", err)
		return
	}
	settings := parseSettings(org.Settings)
	brand := s.brandingAssets(r.Context(), draft.OrgID, org.Name)

	var notifyID string
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.ApproveAmendment(r.Context(), sqlc.ApproveAmendmentParams{
			Actor: p.UserID, OrgID: draft.OrgID, ID: draft.ID, FromStage: &stage,
		}); err != nil {
			return err
		}
		if err := audit.Record(r.Context(), q, audit.Entry{
			OrgID: db.UUIDString(draft.OrgID), ActorUserID: p.UserIDString(),
			Action: audit.ActionContractAmendApprove, EntityType: audit.EntityContract,
			EntityID: db.UUIDString(draft.ID),
			Before:   map[string]any{"stage": stage, "status": contractDraft},
			After: map[string]any{
				"stage": amendStageApproved, "status": contractPendingSignature,
				"drafted_by":    db.UUIDString(draft.AmendmentDraftedBy),
				"self_approved": draft.AmendmentDraftedBy == p.UserID,
			},
		}); err != nil {
			return err
		}
		sw, en := db.StrVal(draft.AmendmentNoteSw), db.StrVal(draft.AmendmentNoteEn)
		var err error
		notifyID, err = s.queueContractSMS(r.Context(), q, contractMessage{
			OrgID: db.UUIDString(draft.OrgID), UserID: db.UUIDString(draft.RenterUserID),
			ContractID: db.UUIDString(draft.ID), Kind: notify.KindContractAmendment,
			Lang: settings.SMSLanguage, Phone: db.StrVal(draft.RenterPhone),
			Overrides: settings.notifyOverrides(),
			Vars: notify.Vars{
				Name: draft.RenterName, Unit: draft.UnitName, Property: draft.PropertyName,
				Org: brand.DisplayName, Date: effective.Format(dateLayout),
				Link: s.cfg.EnduserURL() + "/contract/" + db.UUIDString(draft.ID),
			},
			ReasonByLang: map[string]string{
				notify.LangSwahili: noteFor(notify.LangSwahili, sw, en),
				notify.LangEnglish: noteFor(notify.LangEnglish, sw, en),
			},
		})
		return err
	}); err != nil {
		if isNoRows(err) {
			amendmentNotInStage(w)
			return
		}
		s.serverError(w, r, "contract.amendment.approve", err)
		return
	}
	s.enqueueNotifications(r.Context(), notifyID)
	s.writeContract(w, r, draft.ID, p.OrgID)
}

// reviewReason reads the owner's (or renter's) required one-line reason.
func reviewReason(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		Reason string `json:"reason"`
	}
	if !DecodeJSON(w, r, &body) {
		return "", false
	}
	f := validate.Fields{}
	reason := f.MaxLen("reason", f.Required("reason", strings.TrimSpace(body.Reason)), amendReasonMax)
	if !f.Empty() {
		badRequest(w, f)
		return "", false
	}
	return reason, true
}

// ------------------------------- POST /contracts/{id}/amendment/return --

func (s *Server) handleReturnAmendment(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	if !requireOwner(w, p) {
		return
	}
	draft, _, ok := s.loadAmendment(w, r)
	if !ok {
		return
	}
	reason, ok := reviewReason(w, r)
	if !ok {
		return
	}
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.ReturnAmendment(r.Context(), sqlc.ReturnAmendmentParams{
			Actor: p.UserID, Note: &reason, OrgID: draft.OrgID, ID: draft.ID,
		}); err != nil {
			return err
		}
		if err := s.amendmentInbox(r, q, draft, p.UserID, "amendment_returned",
			"Contract change returned for edits", reason); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID: db.UUIDString(draft.OrgID), ActorUserID: p.UserIDString(),
			Action: audit.ActionContractAmendReturn, EntityType: audit.EntityContract,
			EntityID: db.UUIDString(draft.ID),
			Before:   map[string]any{"stage": amendStageSubmitted},
			After:    map[string]any{"stage": amendStageDraft, "reason": reason},
		})
	}); err != nil {
		if isNoRows(err) {
			amendmentNotInStage(w)
			return
		}
		s.serverError(w, r, "contract.amendment.return", err)
		return
	}
	s.writeContract(w, r, draft.ID, p.OrgID)
}

// ------------------- POST /contracts/{id}/amendment/{reject|withdraw} --

// closeDraft ends a draft that never reached the renter. Reject is the
// owner's (with a reason); withdraw is any org member's.
func (s *Server) closeDraft(w http.ResponseWriter, r *http.Request, to string) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	if to == amendStageRejected && !requireOwner(w, p) {
		return
	}
	draft, _, ok := s.loadAmendment(w, r)
	if !ok {
		return
	}
	reason := "withdrawn"
	if to == amendStageRejected {
		if reason, ok = reviewReason(w, r); !ok {
			return
		}
	} else if r.ContentLength > 0 {
		var body struct {
			Reason string `json:"reason"`
		}
		if !DecodeJSON(w, r, &body) {
			return
		}
		f := validate.Fields{}
		if v := f.MaxLen("reason", strings.TrimSpace(body.Reason), amendReasonMax); v != "" {
			reason = v
		}
		if !f.Empty() {
			badRequest(w, f)
			return
		}
	}
	action, kind, title := audit.ActionContractAmendWithdraw, "", ""
	if to == amendStageRejected {
		action, kind, title = audit.ActionContractAmendReject, "amendment_rejected", "Contract change rejected"
	}
	stage := amendStageOf(draft)
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.CloseAmendmentDraft(r.Context(), sqlc.CloseAmendmentDraftParams{
			ToStage: &to, Reason: &reason, Actor: p.UserID, OrgID: draft.OrgID, ID: draft.ID,
			FromStages: []string{amendStageDraft, amendStageSubmitted},
		}); err != nil {
			return err
		}
		if kind != "" {
			if err := s.amendmentInbox(r, q, draft, p.UserID, kind, title, reason); err != nil {
				return err
			}
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID: db.UUIDString(draft.OrgID), ActorUserID: p.UserIDString(),
			Action: action, EntityType: audit.EntityContract, EntityID: db.UUIDString(draft.ID),
			Before: map[string]any{"stage": stage, "status": contractDraft},
			After:  map[string]any{"stage": to, "status": contractTerminated, "reason": reason},
		})
	}); err != nil {
		if isNoRows(err) {
			amendmentNotInStage(w)
			return
		}
		s.serverError(w, r, "contract.amendment.close", err)
		return
	}
	s.writeContract(w, r, draft.ID, p.OrgID)
}

func (s *Server) handleRejectAmendment(w http.ResponseWriter, r *http.Request) {
	s.closeDraft(w, r, amendStageRejected)
}

func (s *Server) handleWithdrawAmendment(w http.ResponseWriter, r *http.Request) {
	s.closeDraft(w, r, amendStageWithdrawn)
}

// ------------------------------------ POST /me/contracts/{id}/decline --

// handleDeclineAmendment is the renter's no to an approved change they have
// not signed. The running contract is not touched; the org hears in the bell.
func (s *Server) handleDeclineAmendment(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	id, err := db.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		notFoundContract(w)
		return
	}
	row, err := s.q.GetContract(r.Context(), sqlc.GetContractParams{ID: id, RenterUserID: p.UserID})
	if isNoRows(err) {
		notFoundContract(w)
		return
	}
	if err != nil {
		s.serverError(w, r, "contract.decline.load", err)
		return
	}
	reason, ok := reviewReason(w, r)
	if !ok {
		return
	}
	if !row.AmendmentEffectiveDate.Valid {
		conflictCode(w, "not_an_amendment", "not an amendment",
			"only a change to a running contract can be declined")
		return
	}
	if err := s.inTx(r.Context(), func(q *sqlc.Queries) error {
		if _, err := q.DeclineAmendment(r.Context(), sqlc.DeclineAmendmentParams{
			Reason: &reason, ID: row.ID, RenterUserID: p.UserID,
		}); err != nil {
			return err
		}
		if err := s.amendmentInbox(r, q, row, p.UserID, "amendment_declined",
			"Renter declined the contract change — "+row.RenterName, reason); err != nil {
			return err
		}
		return audit.Record(r.Context(), q, audit.Entry{
			OrgID: db.UUIDString(row.OrgID), ActorUserID: p.UserIDString(),
			Action: audit.ActionContractAmendDecline, EntityType: audit.EntityContract,
			EntityID: db.UUIDString(row.ID),
			Before:   map[string]any{"stage": amendStageApproved, "status": contractPendingSignature},
			After:    map[string]any{"stage": amendStageDeclined, "status": contractTerminated, "reason": reason},
		})
	}); err != nil {
		if isNoRows(err) {
			conflictCode(w, "not_declinable", "cannot decline",
				"only a change you have not signed yet can be declined")
			return
		}
		s.serverError(w, r, "contract.decline", err)
		return
	}
	out, ok := s.reloadContract(w, r, row.ID, pgtype.UUID{}, p.UserID)
	if !ok {
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"contract": out})
}
