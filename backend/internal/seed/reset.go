package seed

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"tms/backend/internal/db"
)

// orgScopedDeletes is the delete order for one org's data: children before
// parents, every statement filtered on org_id. audit_log and
// contract_signatures are append-only in normal operation; the reset runs with
// session_replication_role = replica so the guard triggers stand down for this
// transaction only, and never for the API's own connections.
var orgScopedDeletes = []string{
	"DELETE FROM audit_log            WHERE org_id = $1",
	"DELETE FROM notification_log     WHERE org_id = $1",
	"DELETE FROM payment_allocations  WHERE org_id = $1",
	"DELETE FROM payments             WHERE org_id = $1",
	"DELETE FROM payment_schedules    WHERE org_id = $1",
	"DELETE FROM contract_signatures  WHERE org_id = $1",
	"DELETE FROM unit_link_requests   WHERE org_id = $1",
	"DELETE FROM contracts            WHERE org_id = $1",
	"DELETE FROM contract_templates   WHERE org_id = $1",
	"DELETE FROM price_plans          WHERE org_id = $1",
	"DELETE FROM units                WHERE org_id = $1",
	"DELETE FROM properties           WHERE org_id = $1",
	"DELETE FROM payment_periods      WHERE org_id = $1",
	"DELETE FROM org_branding         WHERE org_id = $1",
	"DELETE FROM sessions             WHERE org_id = $1",
}

// ResetResult reports what a reset removed.
type ResetResult struct {
	OrgName string
	OrgID   string
	Rows    int64
	Users   int64
}

// Reset deletes one org and everything that belongs to it, by org_id. It never
// touches a row carrying another org's id.
//
// Users are global (the users table has no org_id), so only two groups go:
// this org's own staff, and renters whose only tenancies and applications were
// with this org. A renter who also rents from someone else is left alone.
func (s *Seeder) Reset(ctx context.Context, slug string) (ResetResult, error) {
	var out ResetResult

	org, err := s.q.GetOrgBySlug(ctx, slug)
	if err == pgx.ErrNoRows || isNoRows(err) {
		return out, fmt.Errorf("seed: no org with slug %q", slug)
	}
	if err != nil {
		return out, err
	}
	orgID := org.ID
	out.OrgName = org.Name
	out.OrgID = db.UUIDString(orgID)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Stand the append-only and cascade triggers down for this transaction.
	if _, err := tx.Exec(ctx, "SET LOCAL session_replication_role = 'replica'"); err != nil {
		return out, fmt.Errorf("seed: reset needs a superuser connection to bypass the "+
			"append-only audit triggers: %w", err)
	}

	// Users to remove, resolved before the rows that name them are deleted.
	// A renter belongs to this reset when they either rent from this org, or
	// were registered by this org's seed run (the `auth.register_renter` audit
	// row the seeder stamps with the org id) — and, either way, have no tie to
	// any other org. A renter who also rents elsewhere stays.
	const usersSQL = `
SELECT u.id FROM users u
JOIN org_members m ON m.user_id = u.id AND m.org_id = $1
UNION
SELECT u.id FROM users u
WHERE u.kind = 'renter'
  AND (
      EXISTS (
          SELECT 1 FROM contracts c WHERE c.renter_user_id = u.id AND c.org_id = $1
          UNION ALL
          SELECT 1 FROM unit_link_requests r WHERE r.renter_user_id = u.id AND r.org_id = $1
      )
      OR EXISTS (
          SELECT 1 FROM audit_log a
          WHERE a.actor_user_id = u.id AND a.org_id = $1
            AND a.action = 'auth.register_renter'
      )
  )
  AND NOT EXISTS (
      SELECT 1 FROM contracts c WHERE c.renter_user_id = u.id AND c.org_id <> $1
      UNION ALL
      SELECT 1 FROM unit_link_requests r WHERE r.renter_user_id = u.id AND r.org_id <> $1
  )`
	rows, err := tx.Query(ctx, usersSQL, orgID)
	if err != nil {
		return out, err
	}
	var userIDs []any
	for rows.Next() {
		var id any
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		userIDs = append(userIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	for _, stmt := range orgScopedDeletes {
		tag, err := tx.Exec(ctx, stmt, orgID)
		if err != nil {
			return out, fmt.Errorf("seed: reset %q: %w", stmt, err)
		}
		out.Rows += tag.RowsAffected()
	}
	if tag, err := tx.Exec(ctx, "DELETE FROM org_members WHERE org_id = $1", orgID); err != nil {
		return out, err
	} else {
		out.Rows += tag.RowsAffected()
	}
	if tag, err := tx.Exec(ctx, "DELETE FROM orgs WHERE id = $1", orgID); err != nil {
		return out, err
	} else {
		out.Rows += tag.RowsAffected()
	}

	for _, id := range userIDs {
		if _, err := tx.Exec(ctx, "DELETE FROM renter_profiles WHERE user_id = $1", id); err != nil {
			return out, err
		}
		// Platform-level audit rows (a renter registering) carry no org_id, so
		// the org-scoped sweep above misses them; leaving them behind would
		// orphan a foreign key onto a user that is about to go.
		if _, err := tx.Exec(ctx,
			"DELETE FROM audit_log WHERE org_id IS NULL AND actor_user_id = $1", id); err != nil {
			return out, err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM sessions WHERE user_id = $1", id); err != nil {
			return out, err
		}
		tag, err := tx.Exec(ctx, "DELETE FROM users WHERE id = $1", id)
		if err != nil {
			return out, err
		}
		out.Users += tag.RowsAffected()
	}

	if err := tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, nil
}
