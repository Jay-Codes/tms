package httpserver

import (
	"time"

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
	Language          string           `json:"language"`
	SnapshotHash      string           `json:"snapshot_hash"`
	Signatures        []signatureBlock `json:"signatures"`
	LinkRequestID     *string          `json:"link_request_id"`
	CreatedAt         time.Time        `json:"created_at"`
	ActivatedAt       *time.Time       `json:"activated_at"`
	TerminatedAt      *time.Time       `json:"terminated_at"`
	TerminationReason *string          `json:"termination_reason"`
	SchedulesSummary  schedulesSummary `json:"schedules_summary"`
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
}

// templateResponse is the `template` shape from API.md.
type templateResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	BodyHTML string `json:"body_html,omitempty"`
	// BodyHTMLSW is the Swahili body (Phase 13). It is sent alongside
	// `body_html` on the single-template reads, empty when the org has not
	// written one — the editor shows two tabs and only one of them is filled.
	BodyHTMLSW string    `json:"body_html_sw,omitempty"`
	IsDefault  bool      `json:"is_default"`
	Variables  []string  `json:"variables,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
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
		TermDays:     r.TermDays,
		StartDate:    r.StartDate.Time.Format(dateLayout),
		EndDate:      r.EndDate.Time.Format(dateLayout),
		DueDay:       r.DueDay,
		Language:     r.Language,
		SnapshotHash: db.StrVal(r.SnapshotHash),
		Signatures:   signatures,
		CreatedAt:    r.CreatedAt.Time,
		ActivatedAt:  timePtr(r.ActivatedAt.Valid, r.ActivatedAt.Time),
		TerminatedAt: timePtr(r.TerminatedAt.Valid, r.TerminatedAt.Time),

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
		})
	}
	return out
}

func toSchedule(s sqlc.PaymentSchedule) scheduleResponse {
	return scheduleResponse{
		ID:          db.UUIDString(s.ID),
		PeriodStart: s.PeriodStart.Time.Format(dateLayout),
		PeriodEnd:   s.PeriodEnd.Time.Format(dateLayout),
		DueDate:     s.DueDate.Time.Format(dateLayout),
		Amount:      s.Amount,
		Status:      s.Status,
		PaidAmount:  s.PaidAmount,
	}
}

func toTemplateSummary(t sqlc.ContractTemplate) templateResponse {
	return templateResponse{
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
