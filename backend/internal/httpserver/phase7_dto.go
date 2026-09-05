package httpserver

import "time"

// ------------------------------------------------------------ reports --

// reportAssets is the asset block of GET /reports/summary.
type reportAssets struct {
	Properties    int64   `json:"properties"`
	Units         int64   `json:"units"`
	Occupied      int64   `json:"occupied"`
	Vacant        int64   `json:"vacant"`
	Maintenance   int64   `json:"maintenance"`
	Unlisted      int64   `json:"unlisted"`
	OccupancyRate float64 `json:"occupancy_rate"`
}

type reportRenters struct {
	Active int64 `json:"active"`
}

type reportContracts struct {
	Active           int64 `json:"active"`
	Expiring         int64 `json:"expiring"`
	PendingSignature int64 `json:"pending_signature"`
}

// reportPeriod is the money block: what the period asked for, what came in.
type reportPeriod struct {
	From          string `json:"from"`
	To            string `json:"to"`
	Expected      int64  `json:"expected"`
	Collected     int64  `json:"collected"`
	Outstanding   int64  `json:"outstanding"`
	OverdueCount  int64  `json:"overdue_count"`
	OverdueAmount int64  `json:"overdue_amount"`
}

type vacantUnitResponse struct {
	UnitID       string `json:"unit_id"`
	Name         string `json:"name"`
	PropertyName string `json:"property_name"`
	DaysVacant   int    `json:"days_vacant"`
}

type reportSummaryResponse struct {
	Assets      reportAssets         `json:"assets"`
	Renters     reportRenters        `json:"renters"`
	Contracts   reportContracts      `json:"contracts"`
	Period      reportPeriod         `json:"period"`
	VacantUnits []vacantUnitResponse `json:"vacant_units"`
}

// paymentStatusRow is one renter's line of GET /reports/payment-status.
type paymentStatusRow struct {
	RenterUserID  string     `json:"renter_user_id"`
	RenterName    string     `json:"renter_name"`
	Phone         string     `json:"phone"`
	UnitName      string     `json:"unit_name"`
	PropertyName  string     `json:"property_name"`
	ContractID    string     `json:"contract_id"`
	Status        string     `json:"status"`
	NextDueDate   *string    `json:"next_due_date"`
	NextDueAmount *int64     `json:"next_due_amount"`
	Outstanding   int64      `json:"outstanding"`
	OverdueAmount int64      `json:"overdue_amount"`
	LastPaymentAt *time.Time `json:"last_payment_at"`
}

type collectionBucket struct {
	Start     string `json:"start"`
	Expected  int64  `json:"expected"`
	Collected int64  `json:"collected"`
}

type collectionTotals struct {
	Expected  int64 `json:"expected"`
	Collected int64 `json:"collected"`
}

// -------------------------------------------------------- platform admin --

// adminOrgCounts is the per-org headline the admin list shows.
type adminOrgCounts struct {
	Properties      int64 `json:"properties"`
	Units           int64 `json:"units"`
	Renters         int64 `json:"renters"`
	ActiveContracts int64 `json:"active_contracts"`
}

type adminOrgSMS struct {
	Sent30d   int64 `json:"sent_30d"`
	Failed30d int64 `json:"failed_30d"`
}

type adminOrgOwner struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// adminOrgRow is one row of GET /admin/orgs and the core of the detail view.
type adminOrgRow struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Slug            string         `json:"slug"`
	Status          string         `json:"status"`
	Owner           adminOrgOwner  `json:"owner"`
	Counts          adminOrgCounts `json:"counts"`
	SMS             adminOrgSMS    `json:"sms"`
	SuspendedAt     *time.Time     `json:"suspended_at"`
	SuspendedReason *string        `json:"suspended_reason"`
	CreatedAt       time.Time      `json:"created_at"`
}

// adminOrgMember is one row of the detail view's member list.
type adminOrgMember struct {
	ID       string `json:"id"`
	UserID   string `json:"user_id"`
	FullName string `json:"full_name"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	Status   string `json:"status"`
}

// adminOrgDetail is GET /admin/orgs/{id}: the list row plus the settings the
// platform operator is asked about ("are their reminders on?") and the members.
type adminOrgDetail struct {
	adminOrgRow
	Settings adminOrgSettings `json:"settings"`
	Members  []adminOrgMember `json:"members"`
}

// adminOrgSettings is the settings summary — the handful of switches worth
// showing without dumping the org's whole JSON blob.
type adminOrgSettings struct {
	AutoApproveLinks     bool   `json:"auto_approve_links"`
	GraceDays            int    `json:"grace_days"`
	UnsignedReminderDays int    `json:"unsigned_reminder_days"`
	SMSLanguage          string `json:"sms_language"`
	BankAccountSet       bool   `json:"bank_account_set"`
	NotificationsSet     bool   `json:"notifications_set"`
}

// adminJobRow is one entry of GET /admin/jobs.
type adminJobRow struct {
	Name        string     `json:"name"`
	Action      string     `json:"action"`
	Description string     `json:"description"`
	Runnable    bool       `json:"runnable"`
	LastRunAt   *time.Time `json:"last_run_at"`
	LastResult  any        `json:"last_result"`
}
