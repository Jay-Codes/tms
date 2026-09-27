package httpserver

import (
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/contract"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
)

// Contract statuses (SPEC §4).
const (
	contractDraft            = "draft"
	contractPendingSignature = "pending_signature"
	contractActive           = "active"
	contractExpiring         = "expiring"
	contractEnded            = "ended"
	contractTerminated       = "terminated"
)

// Signature parties and methods.
const (
	partyRenter   = "renter"
	partyLandlord = "landlord"

	methodOTPAccept       = "otp_accept"
	methodDrawn           = "drawn"
	methodLandlordRecord  = "landlord_recorded"
	signatureContentType  = "image/png"
	signatureMaxBytes     = 512 << 10 // 512 KiB
	brandingImageMaxBytes = 2 << 20   // 2 MiB
)

// Field bounds (API.md).
const (
	templateNameMax      = 80
	templateBodyMax      = 200 << 10 // 200 KiB
	terminationReasonMax = 200
	footerTextMax        = 500
	dueDayMin            = 1
	dueDayMax            = 31
)

// ------------------------------------------------------------- responses --

// contractUnit / contractRenter / contractPeriod are the nested identity blocks.
type contractUnit struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	PropertyName string `json:"property_name"`
}

type contractRenter struct {
	UserID   string  `json:"user_id"`
	FullName string  `json:"full_name"`
	Phone    *string `json:"phone"`
}

type contractPeriod struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Days  int32  `json:"days"`
}

// signatureBlock is one row of contract_signatures as the API shows it. The
// phone is masked and the drawn image is referenced by presence only: the
// document endpoint issues the presigned URL, a contract listing does not.
type signatureBlock struct {
	Party       string    `json:"party"`
	Name        string    `json:"name"`
	SignedAt    time.Time `json:"signed_at"`
	Method      string    `json:"method"`
	PhoneMasked *string   `json:"phone_masked"`
	HasImage    bool      `json:"has_image"`
	// WitnessedBy is the org user who showed the signing code in person
	// (Phase 18, FLOWS 2b.6). Null for every signature made from a code that
	// arrived by SMS, which is what makes the in-person path visible on the
	// document rather than indistinguishable from the ordinary one.
	WitnessedBy *witnessRef `json:"witnessed_by"`
}

// witnessRef names the org user who witnessed a signature.
type witnessRef struct {
	ID       string `json:"id"`
	FullName string `json:"full_name"`
}

// witnessOf builds the witness reference from a signature row's join.
func witnessOf(id pgtype.UUID, name string) *witnessRef {
	if !id.Valid {
		return nil
	}
	return &witnessRef{ID: db.UUIDString(id), FullName: name}
}

// schedulesSummary is the money view of a contract without loading its rows.
type schedulesSummary struct {
	Count         int64   `json:"count"`
	Total         int64   `json:"total"`
	NextDueDate   *string `json:"next_due_date"`
	NextDueAmount *int64  `json:"next_due_amount"`
	PaidCount     int64   `json:"paid_count"`
	OverdueCount  int64   `json:"overdue_count"`
}

// contractResponse is the `contract` shape from API.md.
type contractResponse struct {
	ID             string         `json:"id"`
	Unit           contractUnit   `json:"unit"`
	Renter         contractRenter `json:"renter"`
	TemplateID     *string        `json:"template_id"`
	Status         string         `json:"status"`
	RentAmount     int64          `json:"rent_amount"`
	RentPeriodDays int32          `json:"rent_period_days"`
	// RentPerPeriod is rent_amount scaled from rent_period_days to the payment
	// period, with the schedule's rounding: the figure the document states and
	// the amount a full schedule row carries. Derived, never stored — the
	// snapshot columns remain the source of truth (PLAN2 Phase 9).
	RentPerPeriod int64           `json:"rent_per_period"`
	PaymentPeriod *contractPeriod `json:"payment_period"`
	TermDays      int32           `json:"term_days"`
	StartDate     string          `json:"start_date"`
	EndDate       string          `json:"end_date"`
	DueDay        *int32          `json:"due_day"`
	// Language is the language the terms were rendered in, frozen with them
	// (Phase 13). Contracts issued before Part 2 read 'en'.
	Language     string `json:"language"`
	SnapshotHash string `json:"snapshot_hash"`
	// Policy is the rules this tenancy was signed under (Phase 22), null for
	// a contract written without one.
	Policy *contract.Policy `json:"policy"`
	// SupersedesContractID names the contract this one replaced (§22.3).
	SupersedesContractID *string `json:"supersedes_contract_id"`
	// §22.4: set on an amendment (the day it governs from, and why), and on
	// the contract it replaced once it activates.
	AmendmentEffectiveDate   *string `json:"amendment_effective_date"`
	AmendmentReason          *string `json:"amendment_reason"`
	SupersededByContractID   *string `json:"superseded_by_contract_id"`
	TerminationEffectiveDate *string `json:"termination_effective_date"`
	// TemplateChanged: unsigned, and its template's wording or policy changed
	// after it was written — offer a reissue. Set on the single read only.
	TemplateChanged bool `json:"template_changed"`
	// Settlement is what ending this tenancy applied (§22.5), absent until then.
	Settlement json.RawMessage `json:"settlement,omitempty"`
	// §22.5: the renter's notice to leave, and the holdover confirmation.
	NoticeGivenAt       *time.Time       `json:"notice_given_at"`
	NoticeLeaveOn       *string          `json:"notice_leave_on"`
	NoticeReason        *string          `json:"notice_reason"`
	MovedOutConfirmedAt *time.Time       `json:"moved_out_confirmed_at"`
	Signatures          []signatureBlock `json:"signatures"`
	LinkRequestID       *string          `json:"link_request_id"`
	CreatedAt           time.Time        `json:"created_at"`
	ActivatedAt         *time.Time       `json:"activated_at"`
	TerminatedAt        *time.Time       `json:"terminated_at"`
	TerminationReason   *string          `json:"termination_reason"`
	SchedulesSummary    schedulesSummary `json:"schedules_summary"`
}

// scheduleResponse is one payment_schedules row.
type scheduleResponse struct {
	ID          string `json:"id"`
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	DueDate     string `json:"due_date"`
	Amount      int64  `json:"amount"`
	Status      string `json:"status"`
	PaidAmount  int64  `json:"paid_amount"`
	// LastPaymentSource is `manual` | `import` | `backfill`, or null when no
	// live payment has reached this instalment (Phase 20 §20.3). It is what
	// puts the "imported" and "backfilled" chips on a rent-book row.
	LastPaymentSource *string `json:"last_payment_source"`
	// WriteOffReason is set on a `written_off` row (Phase 21): the landlord's
	// one line on why the debt is no longer chased.
	WriteOffReason *string `json:"write_off_reason,omitempty"`
	// §22.5: a waiver or discount on this one period, and what it was before.
	OriginalAmount   *int64  `json:"original_amount,omitempty"`
	AdjustmentKind   *string `json:"adjustment_kind,omitempty"`
	AdjustmentReason *string `json:"adjustment_reason,omitempty"`
}

// templateResponse is the `template` shape from API.md.
type templateResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	BodyHTML string `json:"body_html,omitempty"`
	// BodyHTMLSW is the Swahili body (Phase 13). It is sent alongside
	// `body_html` on the single-template reads, empty when the org has not
	// written one — the editor shows two tabs and only one of them is filled.
	BodyHTMLSW string   `json:"body_html_sw,omitempty"`
	IsDefault  bool     `json:"is_default"`
	Variables  []string `json:"variables,omitempty"`
	// Policy is what contracts written on this template will stipulate
	// (Phase 22), null when the template sets none.
	Policy *contract.Policy `json:"policy"`
	// Usage is where the template is assigned (Phase 22), on the list only.
	Usage     *templateUsage `json:"usage,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// templateUsage counts the units and properties a template is assigned to.
type templateUsage struct {
	Units      int64 `json:"units"`
	Properties int64 `json:"properties"`
}

// brandingResponse is GET/PUT /org/branding.
type brandingResponse struct {
	DisplayName        string         `json:"display_name"`
	LogoURL            *string        `json:"logo_url"`
	LetterheadURL      *string        `json:"letterhead_url"`
	Theme              themeBlock     `json:"theme"`
	DashboardPrefs     map[string]any `json:"dashboard_prefs"`
	DocumentFooterText *string        `json:"document_footer_text"`
}

// --------------------------------------------------------------- mapping --

// contractRow is the union of the Get/List contract row shapes, so one mapper
// serves both endpoints (the two queries select identical columns).
type contractRow = sqlc.GetContractRow

func contractRowOfList(r sqlc.ListContractsRow) contractRow { return contractRow(r) }

func toContract(r contractRow, signatures []signatureBlock) contractResponse {
	out := contractResponse{
		ID:             db.UUIDString(r.ID),
		Unit:           contractUnit{ID: db.UUIDString(r.UnitID), Name: r.UnitName, PropertyName: r.PropertyName},
		Renter:         contractRenter{UserID: db.UUIDString(r.RenterUserID), FullName: r.RenterName, Phone: r.RenterPhone},
		Status:         r.Status,
		RentAmount:     r.RentAmount,
		RentPeriodDays: r.RentPeriodDays,
		RentPerPeriod: contract.RentPerPeriod(
			r.RentAmount, int(r.RentPeriodDays), int(r.PaymentPeriodDays)),
		TermDays:                 r.TermDays,
		StartDate:                r.StartDate.Time.Format(dateLayout),
		EndDate:                  r.EndDate.Time.Format(dateLayout),
		DueDay:                   r.DueDay,
		Language:                 r.Language,
		SnapshotHash:             db.StrVal(r.SnapshotHash),
		Policy:                   parsedPolicy(r.Policy),
		Settlement:               json.RawMessage(r.Settlement),
		NoticeGivenAt:            optTime(r.NoticeGivenAt),
		NoticeLeaveOn:            optDateString(r.NoticeLeaveOn),
		NoticeReason:             r.NoticeReason,
		MovedOutConfirmedAt:      optTime(r.MovedOutConfirmedAt),
		SupersedesContractID:     optUUIDString(r.SupersedesContractID),
		AmendmentEffectiveDate:   optDateString(r.AmendmentEffectiveDate),
		AmendmentReason:          r.AmendmentReason,
		SupersededByContractID:   optUUIDString(r.SupersededByContractID),
		TerminationEffectiveDate: optDateString(r.TerminationEffectiveDate),
		Signatures:               signatures,
		CreatedAt:                r.CreatedAt.Time,
		ActivatedAt:              timePtr(r.ActivatedAt.Valid, r.ActivatedAt.Time),
		TerminatedAt:             timePtr(r.TerminatedAt.Valid, r.TerminatedAt.Time),

		TerminationReason: r.TerminationReason,
		SchedulesSummary: schedulesSummary{
			Count:        r.ScheduleCount,
			Total:        r.ScheduleTotal,
			PaidCount:    r.SchedulePaidCount,
			OverdueCount: r.ScheduleOverdueCount,
			NextDueDate:  dateStr(r.NextDueDate.Valid, r.NextDueDate.Time),
		},
	}
	if out.Signatures == nil {
		out.Signatures = []signatureBlock{}
	}
	if r.NextDueDate.Valid {
		amount := r.NextDueAmount
		out.SchedulesSummary.NextDueAmount = &amount
	}
	if r.TemplateID.Valid {
		id := db.UUIDString(r.TemplateID)
		out.TemplateID = &id
	}
	if r.LinkRequestID.Valid {
		id := db.UUIDString(r.LinkRequestID)
		out.LinkRequestID = &id
	}
	if r.PaymentPeriodID.Valid {
		out.PaymentPeriod = &contractPeriod{
			ID:    db.UUIDString(r.PaymentPeriodID),
			Label: db.StrVal(r.PeriodLabel),
			Days:  r.PaymentPeriodDays,
		}
	}
	return out
}

func toSignatures(rows []sqlc.ListContractSignaturesRow) []signatureBlock {
	out := make([]signatureBlock, 0, len(rows))
	for _, sg := range rows {
		out = append(out, signatureBlock{
			Party:       sg.Party,
			Name:        sg.SignerName,
			SignedAt:    sg.SignedAt.Time,
			Method:      sg.Method,
			PhoneMasked: maskPhone(db.StrVal(sg.SignerPhone)),
			HasImage:    sg.SignatureObjectKey != nil && *sg.SignatureObjectKey != "",
			WitnessedBy: witnessOf(sg.WitnessedByUserID, sg.WitnessName),
		})
	}
	return out
}

func toSchedule(s sqlc.PaymentSchedule) scheduleResponse {
	return scheduleResponse{
		ID:               db.UUIDString(s.ID),
		PeriodStart:      s.PeriodStart.Time.Format(dateLayout),
		PeriodEnd:        s.PeriodEnd.Time.Format(dateLayout),
		DueDate:          s.DueDate.Time.Format(dateLayout),
		Amount:           s.Amount,
		Status:           s.Status,
		PaidAmount:       s.PaidAmount,
		WriteOffReason:   s.WriteOffReason,
		OriginalAmount:   s.OriginalAmount,
		AdjustmentKind:   s.AdjustmentKind,
		AdjustmentReason: s.AdjustmentReason,
	}
}

// parsedPolicy is a stored policy for a response; one that does not parse is
// shown as none rather than failing the read.
func parsedPolicy(raw []byte) *contract.Policy {
	p, err := contract.ParsePolicy(raw)
	if err != nil {
		return nil
	}
	return p
}

func toTemplateSummary(t sqlc.ContractTemplate) templateResponse {
	return templateResponse{
		Policy:    parsedPolicy(t.Policy),
		ID:        db.UUIDString(t.ID),
		Name:      t.Name,
		IsDefault: t.IsDefault,
		CreatedAt: t.CreatedAt.Time,
		UpdatedAt: t.UpdatedAt.Time,
	}
}

// maskPhone renders a phone as its last four digits, the form the signature
// block prints ("signed … via phone •••1234"). Anything too short to mask
// yields no mask rather than a partial number.
func maskPhone(phone string) *string {
	if len(phone) < 4 {
		return nil
	}
	masked := "•••" + phone[len(phone)-4:]
	return &masked
}
