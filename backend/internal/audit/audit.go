// Package audit writes the append-only audit trail (SPEC §8). Every mutating
// handler records one entry inside the same transaction as the mutation, so a
// rolled-back change leaves no audit row and a committed change always has one.
package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
)

// Actions used in Phase 1. Values are stable strings: they are queried by
// operators and shown in the landlord's audit view.
const (
	ActionLogin          = "auth.login"
	ActionLoginFailed    = "auth.login_failed"
	ActionLogout         = "auth.logout"
	ActionOTPSend        = "auth.otp_send"
	ActionOTPVerify      = "auth.otp_verify"
	ActionRegisterRenter = "auth.register_renter"
	ActionVerifyEmail    = "auth.verify_email"
	ActionInviteAccept   = "auth.invite_accept"
	ActionOrgCreate      = "org.create"
	ActionOrgUpdate      = "org.update"
	ActionMemberInvite   = "org.member_invite"
	ActionMemberRemove   = "org.member_remove"

	// Phase 2 — properties, units, pricing, payment periods.
	ActionPropertyCreate  = "property.create"
	ActionPropertyUpdate  = "property.update"
	ActionPropertyDelete  = "property.delete"
	ActionUnitCreate      = "unit.create"
	ActionUnitUpdate      = "unit.update"
	ActionUnitDelete      = "unit.delete"
	ActionUnitQRGenerate  = "unit.qr_generate"
	ActionPriceCreate     = "price.create"
	ActionPriceBulkUpdate = "price.bulk_update"
	ActionPeriodCreate    = "payment_period.create"
	ActionPeriodUpdate    = "payment_period.update"
	ActionPeriodDelete    = "payment_period.delete"
	ActionPeriodRestore   = "payment_period.restore_recommended"

	// Phase 3 — renter KYC and unit link requests. `kyc.view` records every
	// read of an identity document (SPEC §8: access to KYC is audited); the
	// NIDA number itself never appears in an audit payload, only
	// `nida_changed`.
	ActionProfileUpdate     = "renter_profile.update"
	ActionKYCUpload         = "kyc.upload"
	ActionKYCView           = "kyc.view"
	ActionLinkRequestCreate = "link_request.create"
	ActionLinkRequestCancel = "link_request.cancel"
	ActionLinkApprove       = "link_request.approve"
	ActionLinkReject        = "link_request.reject"

	// Phase 4 — contract templates, contracts, signing, branding.
	//
	// `contract.activate_landlord_recorded` is deliberately its own action
	// rather than a flag on `contract.activate`: it is the one path that
	// binds a renter without their signature (FLOWS 3.6), so it must be
	// findable by filtering the trail on the action alone.
	ActionTemplateCreate   = "contract_template.create"
	ActionTemplateUpdate   = "contract_template.update"
	ActionTemplateDelete   = "contract_template.delete"
	ActionContractCreate   = "contract.create"
	ActionContractSign     = "contract.sign"
	ActionContractActivate = "contract.activate"
	// ActionContractActivateLandlordRecorded is activation without a renter
	// signature, on the landlord's own record.
	ActionContractActivateLandlordRecorded = "contract.activate_landlord_recorded"
	ActionContractTerminate                = "contract.terminate"
	ActionBrandingUpdate                   = "org.branding_update"
	ActionBrandingAsset                    = "org.branding_asset"
	ActionContractLifecycleRun             = "contract.lifecycle_run"

	// Phase 5 — offline payments, reversals, the overdue sweep and the bank
	// collection account renters are told to pay into.
	//
	// `payment.reverse` is its own action rather than a flag on the record:
	// a correction is the one movement that takes money back off a schedule
	// (FLOWS 7.4), so it must be findable by action alone.
	ActionPaymentRecord     = "payment.record"
	ActionPaymentReverse    = "payment.reverse"
	ActionOverdueRun        = "payment.overdue_run"
	ActionBankAccountUpdate = "org.bank_account_update"

	// Phase 6 — notification settings, the landlord's bulk SMS, a manual
	// retry and the scheduler sweep.
	//
	// `notification.custom` records the message body as well as the count:
	// a broadcast is the one send a landlord composes themselves, so the
	// trail must be able to answer "what did they text forty renters?".
	ActionNotificationSettings = "org.notification_settings_update"
	ActionNotificationCustom   = "notification.custom"
	ActionNotificationRetry    = "notification.retry"
	ActionNotificationRun      = "notification.scheduler_run"

	// Phase 7 — platform-admin suspension. Both rows carry the *target* org
	// as org_id and the admin as actor, so the trail reads the same from the
	// org's own audit page as from the platform one.
	ActionOrgSuspend  = "org.suspend"
	ActionOrgActivate = "org.activate"
)

// Entity types.
const (
	EntityUser      = "user"
	EntityOrg       = "org"
	EntityOrgMember = "org_member"
	EntitySession   = "session"

	EntityProperty      = "property"
	EntityUnit          = "unit"
	EntityPricePlan     = "price_plan"
	EntityPaymentPeriod = "payment_period"

	EntityRenterProfile = "renter_profile"
	EntityLinkRequest   = "link_request"

	EntityContractTemplate = "contract_template"
	EntityContract         = "contract"
	EntityOrgBranding      = "org_branding"

	EntityPayment         = "payment"
	EntityPaymentSchedule = "payment_schedule"

	EntityNotification = "notification"
)

type ctxKey int

const requestKey ctxKey = iota

// RequestInfo is the transport metadata attached to every audit row.
type RequestInfo struct {
	IP        string
	UserAgent string
}

// WithRequestInfo stores the caller's IP and user agent for later audit rows.
// The HTTP middleware calls this once per request.
func WithRequestInfo(ctx context.Context, ip, userAgent string) context.Context {
	return context.WithValue(ctx, requestKey, RequestInfo{IP: ip, UserAgent: userAgent})
}

// RequestInfoFrom retrieves the stored request metadata (zero value if unset).
func RequestInfoFrom(ctx context.Context) RequestInfo {
	info, _ := ctx.Value(requestKey).(RequestInfo)
	return info
}

// Entry is one audit record. OrgID and ActorUserID are optional: platform-level
// events (a renter registering, a failed login) have no org, and anonymous
// events have no actor.
type Entry struct {
	OrgID       string
	ActorUserID string
	Action      string
	EntityType  string
	EntityID    string
	Before      any
	After       any
}

// Record appends one audit row using the supplied queries handle. Pass a
// transaction-bound handle (sqlc.Queries.WithTx) so the row shares the fate of
// the mutation it describes.
func Record(ctx context.Context, q *sqlc.Queries, e Entry) error {
	if q == nil {
		return fmt.Errorf("audit: nil queries handle")
	}
	before, err := toJSON(e.Before)
	if err != nil {
		return err
	}
	after, err := toJSON(e.After)
	if err != nil {
		return err
	}
	info := RequestInfoFrom(ctx)

	_, err = q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{
		OrgID:       optUUID(e.OrgID),
		ActorUserID: optUUID(e.ActorUserID),
		Action:      e.Action,
		EntityType:  e.EntityType,
		EntityID:    optUUID(e.EntityID),
		Before:      before,
		After:       after,
		Ip:          db.Str(info.IP),
		UserAgent:   db.Str(info.UserAgent),
	})
	if err != nil {
		return fmt.Errorf("audit: insert %s: %w", e.Action, err)
	}
	return nil
}

func optUUID(s string) pgtype.UUID {
	if s == "" {
		return pgtype.UUID{}
	}
	u, err := db.ParseUUID(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return u
}

func toJSON(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("audit: marshal payload: %w", err)
	}
	return b, nil
}
