package httpserver

import (
	"time"

	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
)

// ------------------------------------------------------------- KYC status --

// KYC statuses on the wire (API.md). The column stores `incomplete` for a
// profile that has not been filled in; the API calls that `none`, which reads
// better in a UI. kycOut / kycIn translate between the two at the edge so the
// mapping lives in exactly one place.
const (
	kycNone      = "none"
	kycSubmitted = "submitted"
	kycVerified  = "verified"
	kycRejected  = "rejected"

	kycIncompleteDB = "incomplete"
)

// kycOut renders a stored status for the API.
func kycOut(stored string) string {
	if stored == kycIncompleteDB || stored == "" {
		return kycNone
	}
	return stored
}

// kycIn turns an API status back into the stored form.
func kycIn(wire string) string {
	if wire == kycNone {
		return kycIncompleteDB
	}
	return wire
}

// deriveKYC is the rule from API.md: a profile carrying a NIDA number and a
// complete next of kin has been submitted for checking; anything less is not
// started. `verified` is an operator's decision and is never lowered here.
func deriveKYC(current, nida string, kinName, kinPhone *string) string {
	if current == kycVerified {
		return kycVerified
	}
	if nida != "" && db.StrVal(kinName) != "" && db.StrVal(kinPhone) != "" {
		return kycSubmitted
	}
	return kycIncompleteDB
}

// maskNIDA renders a NIDA number as its last four digits (SPEC §8: masked in
// UI, access audited). An empty or too-short number yields no mask at all
// rather than a partial one.
func maskNIDA(nida string) *string {
	if len(nida) < 4 {
		return nil
	}
	masked := "••••••••" + nida[len(nida)-4:]
	return &masked
}

// ------------------------------------------------------------- responses --

// profileResponse is the `profile` block of GET/PUT /me/profile.
type profileResponse struct {
	FullName       string    `json:"full_name"`
	NidaMasked     *string   `json:"nida_masked"`
	NextOfKinName  *string   `json:"next_of_kin_name"`
	NextOfKinPhone *string   `json:"next_of_kin_phone"`
	Email          *string   `json:"email"`
	KycStatus      string    `json:"kyc_status"`
	KycDocUploaded bool      `json:"kyc_doc_uploaded"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// toProfile maps a decrypted profile row, taking the email from the user
// record (renter_profiles has no email column of its own).
func toProfile(row sqlc.GetRenterProfileDecryptedRow, email *string) profileResponse {
	return profileResponse{
		FullName:       row.FullName,
		NidaMasked:     maskNIDA(row.NidaNumber),
		NextOfKinName:  row.NextOfKinName,
		NextOfKinPhone: row.NextOfKinPhone,
		Email:          email,
		KycStatus:      kycOut(row.KycStatus),
		KycDocUploaded: row.KycDocObjectKey != nil && *row.KycDocObjectKey != "",
		UpdatedAt:      row.UpdatedAt.Time,
	}
}

// linkUnit / linkOrg / linkPeriod are the nested blocks of a link request.
type linkUnit struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	PropertyName string `json:"property_name"`
}

type linkOrg struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type linkPeriod struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Days   int32  `json:"days"`
	Amount *int64 `json:"amount"`
}

// linkRenter is the renter block the landlord's inbox shows.
type linkRenter struct {
	UserID    string  `json:"user_id"`
	FullName  string  `json:"full_name"`
	Phone     *string `json:"phone"`
	KycStatus string  `json:"kyc_status"`
}

// schedulePreview is the summary of what the renter is committing to. It is
// computed with the same generator that Phase 4 uses to write the real
// payment_schedules rows, so the preview cannot drift from the contract.
type schedulePreview struct {
	Count       int    `json:"count"`
	FirstDue    string `json:"first_due"`
	AmountFirst int64  `json:"amount_first"`
	AmountLast  int64  `json:"amount_last"`
	Total       int64  `json:"total"`
}

// linkRequestResponse is the `request` shape from API.md.
type linkRequestResponse struct {
	ID              string           `json:"id"`
	Unit            linkUnit         `json:"unit"`
	Org             linkOrg          `json:"org"`
	Renter          *linkRenter      `json:"renter,omitempty"`
	Status          string           `json:"status"`
	PaymentPeriod   *linkPeriod      `json:"payment_period"`
	TermDays        *int32           `json:"term_days"`
	StartDate       *string          `json:"start_date"`
	EndDate         *string          `json:"end_date"`
	SchedulePreview *schedulePreview `json:"schedule_preview,omitempty"`
	RejectionReason *string          `json:"rejection_reason"`
	CreatedAt       time.Time        `json:"created_at"`
	DecidedAt       *time.Time       `json:"decided_at"`
}

// renterProfileBlock is the masked profile the landlord sees on a request
// detail or in the directory. It never carries the NIDA number itself.
type renterProfileBlock struct {
	FullName       string  `json:"full_name"`
	NidaMasked     *string `json:"nida_masked"`
	NextOfKinName  *string `json:"next_of_kin_name"`
	NextOfKinPhone *string `json:"next_of_kin_phone"`
	KycStatus      string  `json:"kyc_status"`
	KycDocUploaded bool    `json:"kyc_doc_uploaded"`
}

// renterUnitLink is one unit a renter is connected to, in the directory.
type renterUnitLink struct {
	UnitID       string `json:"unit_id"`
	UnitName     string `json:"unit_name"`
	PropertyName string `json:"property_name"`
	LinkStatus   string `json:"link_status"`
}

// renterDirectoryEntry is one row of GET /renters.
type renterDirectoryEntry struct {
	UserID    string           `json:"user_id"`
	FullName  string           `json:"full_name"`
	Phone     *string          `json:"phone"`
	Email     *string          `json:"email"`
	KycStatus string           `json:"kyc_status"`
	Units     []renterUnitLink `json:"units"`
	CreatedAt time.Time        `json:"created_at"`
}

// ------------------------------------------------------------- mapping --

// linkRow is the union of the Get/List link-request row shapes, so one mapper
// serves both endpoints.
type linkRow struct {
	ID              string
	OrgID           string
	UnitID          string
	RenterUserID    string
	Status          string
	PaymentPeriodID string
	TermDays        *int32
	StartDate       *string
	EndDate         *string
	RejectionReason *string
	DecidedAt       *time.Time
	CreatedAt       time.Time
	UnitName        string
	PropertyName    string
	OrgName         string
	OrgSlug         string
	RenterName      string
	RenterPhone     *string
	PeriodLabel     string
	PeriodDays      int32
	PriceAmount     int64
	PricePeriodDays int32
	HasPrice        bool
	KycStatus       string
	KycDocUploaded  bool
}

func linkRowOfGet(r sqlc.GetLinkRequestRow) linkRow {
	return linkRow{
		ID: db.UUIDString(r.ID), OrgID: db.UUIDString(r.OrgID),
		UnitID: db.UUIDString(r.UnitID), RenterUserID: db.UUIDString(r.RenterUserID),
		Status: r.Status, PaymentPeriodID: db.UUIDString(r.PaymentPeriodID),
		TermDays: r.TermDays, StartDate: dateStr(r.StartDate.Valid, r.StartDate.Time),
		EndDate: dateStr(r.EndDate.Valid, r.EndDate.Time), RejectionReason: r.RejectionReason,
		DecidedAt: timePtr(r.DecidedAt.Valid, r.DecidedAt.Time), CreatedAt: r.CreatedAt.Time,
		UnitName: r.UnitName, PropertyName: r.PropertyName,
		OrgName: r.OrgName, OrgSlug: r.OrgSlug,
		RenterName: r.RenterName, RenterPhone: r.RenterPhone,
		PeriodLabel: db.StrVal(r.PeriodLabel), PeriodDays: r.PeriodDays,
		PriceAmount: r.PriceAmount, PricePeriodDays: r.PricePeriodDays, HasPrice: r.HasPrice,
		KycStatus: r.KycStatus, KycDocUploaded: r.KycDocUploaded,
	}
}

func linkRowOfList(r sqlc.ListLinkRequestsRow) linkRow {
	return linkRowOfGet(sqlc.GetLinkRequestRow(r))
}

// toLinkRequest renders a request. withRenter adds the renter block (the
// landlord's view); the renter's own list omits it — they know who they are.
func toLinkRequest(r linkRow, withRenter bool) linkRequestResponse {
	out := linkRequestResponse{
		ID:              r.ID,
		Unit:            linkUnit{ID: r.UnitID, Name: r.UnitName, PropertyName: r.PropertyName},
		Org:             linkOrg{Name: r.OrgName, Slug: r.OrgSlug},
		Status:          r.Status,
		TermDays:        r.TermDays,
		StartDate:       r.StartDate,
		EndDate:         r.EndDate,
		RejectionReason: r.RejectionReason,
		CreatedAt:       r.CreatedAt,
		DecidedAt:       r.DecidedAt,
	}
	if r.PaymentPeriodID != "" {
		period := linkPeriod{ID: r.PaymentPeriodID, Label: r.PeriodLabel, Days: r.PeriodDays}
		if r.HasPrice && r.PricePeriodDays > 0 {
			amount := prorate(r.PriceAmount, r.PeriodDays, r.PricePeriodDays)
			period.Amount = &amount
		}
		out.PaymentPeriod = &period
	}
	if withRenter {
		out.Renter = &linkRenter{
			UserID: r.RenterUserID, FullName: r.RenterName,
			Phone: r.RenterPhone, KycStatus: kycOut(r.KycStatus),
		}
	}
	return out
}

func dateStr(valid bool, t time.Time) *string {
	if !valid {
		return nil
	}
	s := t.Format(dateLayout)
	return &s
}

func timePtr(valid bool, t time.Time) *time.Time {
	if !valid {
		return nil
	}
	return &t
}
