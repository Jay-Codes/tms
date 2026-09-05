package httpserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// secretMarkers are substrings that must never reach an audit_log payload: the
// credential columns, the ephemeral auth tokens and the KYC identifier
// (SPEC §8 — the trail records what changed, never the secret itself).
var secretMarkers = []string{
	"password_hash", "pin_hash", "\"password\"", "\"pin\"",
	"otp_token", "\"code\"", "token_hash", "nida",
}

// TestAuditPayloadsCarryNoSecrets exercises every Phase 1 mutating endpoint and
// then reads audit_log straight from Postgres: no before/after payload may
// mention a credential, a one-shot token or a NIDA number.
func TestAuditPayloadsCarryNoSecrets(t *testing.T) {
	h := newHarness(t)

	owner, _ := h.createOrg("Secret Ltd", "Owner", "secret@jjne.test", "0712000090", "supersecret")
	h.registerRenter("+255712000091", "Renter One", "4321")

	// A failed login (records the identifier, never the secret).
	h.client().do(http.MethodPost, "/auth/login",
		map[string]any{"email": "secret@jjne.test", "password": "wrong-password"}).
		mustStatus(t, http.StatusUnauthorized, "bad password")

	// Invite + settings update + removal.
	invited := owner.do(http.MethodPost, "/org/members", map[string]any{
		"email": "staff@secret.test", "full_name": "Staff", "role": "org_manager",
	}).mustStatus(t, http.StatusCreated, "invite")
	memberID := invited.str(t, "member", "id")

	owner.do(http.MethodPatch, "/org", map[string]any{
		"name": "Secret Ltd Renamed", "settings": map[string]any{"grace_days": 5},
	}).mustStatus(t, http.StatusOK, "patch org")

	token := tokenFromLink(t, h.email.LastLink("staff@secret.test"))
	h.client().do(http.MethodPost, "/auth/invite/accept",
		map[string]any{"token": token, "password": "staffsecret1"}).
		mustStatus(t, http.StatusOK, "accept invite")

	owner.do(http.MethodDelete, "/org/members/"+memberID, nil).
		mustStatus(t, http.StatusNoContent, "remove member")

	rows, err := h.pool.Query(context.Background(),
		`SELECT action, coalesce(before::text, ''), coalesce(after::text, '') FROM audit_log`)
	if err != nil {
		t.Fatalf("read audit_log: %v", err)
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var action, before, after string
		if err := rows.Scan(&action, &before, &after); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		seen++
		payload := strings.ToLower(before + " " + after)
		for _, marker := range secretMarkers {
			if strings.Contains(payload, marker) {
				t.Errorf("audit row %q leaks %q: before=%s after=%s", action, marker, before, after)
			}
		}
		// Belt and braces: the literal secrets used above must not appear.
		for _, literal := range []string{"supersecret", "staffsecret1", "wrong-password", "4321", token} {
			if strings.Contains(before+after, literal) {
				t.Errorf("audit row %q contains a literal secret (%q)", action, literal)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit rows: %v", err)
	}
	if seen == 0 {
		t.Fatal("no audit rows were written — the scan proved nothing")
	}
}

// TestAuditLogIsAppendOnly proves the migration's trigger, not just its
// comment: UPDATE and DELETE against audit_log must be refused.
func TestAuditLogIsAppendOnly(t *testing.T) {
	h := newHarness(t)
	h.createOrg("Ledger Ltd", "Owner", "ledger@jjne.test", "0712000092", "supersecret")

	ctx := context.Background()
	var id string
	if err := h.pool.QueryRow(ctx, `SELECT id FROM audit_log LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("no audit row to test against: %v", err)
	}

	if _, err := h.pool.Exec(ctx, `UPDATE audit_log SET action = 'tampered' WHERE id = $1`, id); err == nil {
		t.Error("UPDATE on audit_log succeeded — the append-only trigger is not enforcing")
	}
	if _, err := h.pool.Exec(ctx, `DELETE FROM audit_log WHERE id = $1`, id); err == nil {
		t.Error("DELETE on audit_log succeeded — the append-only trigger is not enforcing")
	}

	var action string
	if err := h.pool.QueryRow(ctx, `SELECT action FROM audit_log WHERE id = $1`, id).Scan(&action); err != nil {
		t.Fatalf("audit row disappeared: %v", err)
	}
	if action == "tampered" {
		t.Fatal("audit row was mutated despite the trigger")
	}
}

// TestRemovedMemberLosesSessionImmediately covers the window between a staff
// removal and the session's natural expiry: the removed member's cookie must
// stop working at once, in both the Redis cache and the Postgres fallback.
func TestRemovedMemberLosesSessionImmediately(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.createOrg("Revoke Ltd", "Owner", "revoke@jjne.test", "0712000093", "supersecret")

	invited := owner.do(http.MethodPost, "/org/members", map[string]any{
		"email": "leaver@revoke.test", "full_name": "Leaver", "role": "org_manager",
	}).mustStatus(t, http.StatusCreated, "invite")
	memberID := invited.str(t, "member", "id")

	// The invitee accepts and is signed in.
	staff := h.client()
	staff.do(http.MethodPost, "/auth/invite/accept", map[string]any{
		"token":    tokenFromLink(t, h.email.LastLink("leaver@revoke.test")),
		"password": "leaversecret1",
	}).mustStatus(t, http.StatusOK, "accept invite")
	staff.do(http.MethodGet, "/org", nil).mustStatus(t, http.StatusOK, "staff sees org before removal")

	owner.do(http.MethodDelete, "/org/members/"+memberID, nil).
		mustStatus(t, http.StatusNoContent, "remove member")

	staff.do(http.MethodGet, "/org", nil).
		mustStatus(t, http.StatusUnauthorized, "removed member still holds an org session")

	var live int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE u.email = 'leaver@revoke.test' AND s.revoked_at IS NULL`).Scan(&live); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if live != 0 {
		t.Fatalf("%d session rows survived the removal in Postgres", live)
	}
}

// TestLogoutRevokesThePostgresRow guards the "Redis flush must not resurrect a
// session" half of SPEC §3: logout writes revoked_at, it does not only drop the
// cache entry.
func TestLogoutRevokesThePostgresRow(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.createOrg("Exit Ltd", "Owner", "exit@jjne.test", "0712000094", "supersecret")

	var before int
	ctx := context.Background()
	if err := h.pool.QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE audience = 'org' AND revoked_at IS NULL`).Scan(&before); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if before != 1 {
		t.Fatalf("expected exactly one live org session, got %d", before)
	}

	owner.do(http.MethodPost, "/auth/logout?audience=org", nil).
		mustStatus(t, http.StatusNoContent, "logout")

	var after int
	if err := h.pool.QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE audience = 'org' AND revoked_at IS NULL`).Scan(&after); err != nil {
		t.Fatalf("count sessions after logout: %v", err)
	}
	if after != 0 {
		t.Fatalf("logout left %d live session rows in Postgres", after)
	}
}
