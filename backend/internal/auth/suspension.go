package auth

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/db"
)

// StatusSuspended is the `orgs.status` value that closes a tenant down.
const StatusSuspended = "suspended"

// suspensionTTL is how long the Redis answer is trusted. Suspend and activate
// both write the flag themselves, so the TTL only bounds staleness after a
// direct database change (a migration, an operator's psql session).
const suspensionTTL = 5 * time.Minute

// SuspensionKey is the Redis flag a platform admin sets on suspend and clears
// on activate: `org:suspended:{org_id}`, "1" or "0".
func SuspensionKey(orgID string) string { return "org:suspended:" + orgID }

// SetSuspended writes the cached suspension flag for an org. It is called by
// the admin suspend/activate handlers so the very next request from that org's
// users sees the new state without waiting for a TTL.
func (m *Manager) SetSuspended(ctx context.Context, orgID string, suspended bool) {
	if m == nil || m.Redis == nil || orgID == "" {
		return
	}
	value := "0"
	if suspended {
		value = "1"
	}
	if err := m.Redis.Set(ctx, SuspensionKey(orgID), value, suspensionTTL).Err(); err != nil {
		m.logger().Warn("could not cache org suspension flag", "org_id", orgID, "error", err)
	}
}

// OrgSuspended reports whether an org is suspended.
//
// Redis answers the common case; a cold or unreachable cache falls back to one
// primary-key lookup and caches the result. Redis is ephemeral (SPEC §2.2), so
// the database is always the truth — a missing flag can never open a suspended
// org, only cost one query.
func (m *Manager) OrgSuspended(ctx context.Context, orgID pgtype.UUID) bool {
	id := db.UUIDString(orgID)
	if id == "" {
		return false
	}
	if m.Redis != nil {
		if v, err := m.Redis.Get(ctx, SuspensionKey(id)).Result(); err == nil {
			return v == "1"
		}
	}
	if m.Q == nil {
		return false
	}
	status, err := m.Q.AdminGetOrgStatus(ctx, orgID)
	if err != nil {
		// A deleted or unreadable org is not "suspended": the handlers below
		// answer 404 for it on their own scope check.
		return false
	}
	suspended := status == StatusSuspended
	m.SetSuspended(ctx, id, suspended)
	return suspended
}
