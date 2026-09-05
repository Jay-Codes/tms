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
