package httpserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"tms/backend/internal/contract"
	"tms/backend/internal/storage"
)

// ------------------------------------------------------------- fixtures --

// contractFixture is one org with priced units, one renter known to it (they
// applied for Room 1), and the contract that approval created.
type contractFixture struct {
	owner       *client
	renter      *client
	orgID       string
	unitIDs     []string
	unitCodes   []string
	renterID    string
	renterPhone string
	periodID    string
	contractID  string
}

const phase4Rent = 250_000

// newContractFixture walks the real path: org + units, renter registers and
// completes KYC, applies for Room 1, landlord approves — which is what creates
// the contract (FLOWS 3.3).
func (h *harness) newContractFixture(t *testing.T, tag, ownerPhone, renterPhone string) contractFixture {
	t.Helper()
	fix := h.newOrgWithUnits(tag, strings.ToLower(tag)+"@jjne.test", ownerPhone,
		[]string{"Room 1", "Room 2", "Room 3", "Room 4"}, phase4Rent)

	out := contractFixture{
		owner: fix.client, orgID: fix.orgID,
		unitIDs: fix.unitIDs, unitCodes: fix.unitCodes, renterPhone: renterPhone,
	}
	out.renter = h.registerRenter(renterPhone, tag+" Renter", defaultPIN)
	out.renter.completeProfile(t, tag+" Renter", validNIDA)
	out.periodID = fix.client.periodIDByDays(t, 30)

	me := out.renter.do(http.MethodGet, "/auth/me?audience=renter", nil).
		mustStatus(t, http.StatusOK, "renter me")
	out.renterID = me.str(t, "user", "id")

	applied := out.renter.do(http.MethodPost, "/units/"+out.unitCodes[0]+"/link",
		linkBody(out.periodID, testTermDays)).
		mustStatus(t, http.StatusCreated, "apply")
	approved := out.owner.do(http.MethodPost,
		"/link-requests/"+applied.str(t, "request", "id")+"/approve", nil).
		mustStatus(t, http.StatusOK, "approve")
	out.contractID = approved.str(t, "contract", "id")
	return out
}

// signAsRenter runs the renter's half of the signature: request a code, read it
// the way dev does, submit it.
func (h *harness) signAsRenter(t *testing.T, c *client, contractID, phone string) response {
	t.Helper()
	c.do(http.MethodPost, "/contracts/"+contractID+"/sign/otp", nil).
		mustStatus(t, http.StatusAccepted, "sign otp")
	code := h.sms.LastOTP(phone)
	if code == "" {
		t.Fatalf("no signing OTP captured for %s", phone)
	}
	return c.do(http.MethodPost, "/contracts/"+contractID+"/sign",
		map[string]any{"otp_code": code}).mustStatus(t, http.StatusOK, "sign")
}

// customPeriod adds a payment period of the given length to an org.
func (c *client) customPeriod(t *testing.T, label string, days int) string {
	t.Helper()
	return c.do(http.MethodPost, "/org/payment-periods",
		map[string]any{"label": label, "days": days}).
		mustStatus(t, http.StatusCreated, "create period").str(t, "period", "id")
}

// contractOn writes a contract straight from the landlord's side, the manual
// path beside link approval.
func (c *client) contractOn(t *testing.T, body map[string]any) response {
	t.Helper()
	return c.do(http.MethodPost, "/contracts", body)
}

func (h *harness) unitStatus(t *testing.T, c *client, unitID string) string {
	t.Helper()
	return c.do(http.MethodGet, "/units/"+unitID, nil).
		mustStatus(t, http.StatusOK, "read unit").str(t, "unit", "status")
}

// ------------------------------------------------- approval → contract --

// TestApprovalCreatesTheContract is FLOWS 3.3: approving an application is what
// produces the document, snapshotted and hashed, with the renter told to sign.
func TestApprovalCreatesTheContract(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "Appr", "0714000100", "+255714000101")

	got := fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID, nil).
		mustStatus(t, http.StatusOK, "read contract")
	if status := got.str(t, "contract", "status"); status != "pending_signature" {
		t.Errorf("status = %q, want pending_signature", status)
	}
	if hash := got.str(t, "contract", "snapshot_hash"); len(hash) != 64 {
		t.Errorf("snapshot_hash = %q, want a 64-char sha256 hex digest", hash)
	}
	if got.Body["contract"].(map[string]any)["link_request_id"] == nil {
		t.Error("the contract does not record the request it came from")
	}
	if amount := num(t, got, "contract", "rent_amount"); amount != phase4Rent {
		t.Errorf("rent_amount = %v, want the unit's price %d", amount, phase4Rent)
	}

	// The unit stays vacant until activation — an approved-but-unsigned
	// contract must not strand it (API.md Phase 3 notes).
	if s := h.unitStatus(t, fix.owner, fix.unitIDs[0]); s != "vacant" {
		t.Errorf("unit status = %q, want vacant until the contract is activated", s)
	}

	notes := ofKind(h.notifications(t), "contract_ready")
	if len(notes) != 1 {
		t.Fatalf("contract_ready notifications = %+v, want 1", notes)
	}
	if want := "/enduser/contract/" + fix.contractID; !strings.Contains(notes[0].Body, want) {
		t.Errorf("the SMS body %q does not carry the signing link %q", notes[0].Body, want)
	}

	// And the renter sees it as their own.
	mine := fix.renter.do(http.MethodGet, "/me/contracts", nil).
		mustStatus(t, http.StatusOK, "renter contracts")
	if got := len(listOf(t, mine)); got != 1 {
		t.Fatalf("the renter sees %d contracts, want 1", got)
	}
}

// TestApprovalBackfillsAMissingContract: a request approved before contracts
// existed carries none, and re-approving it is how the landlord asks for one
// (API.md, Phase 4 notes). Once it has one, re-approving is a 409 again.
func TestApprovalBackfillsAMissingContract(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "Backfill", "0714000110", "+255714000111")

	// Apply for a second unit and approve it, then delete the contract the way
	// a pre-Phase-4 database would have had none at all.
	applied := fix.renter.do(http.MethodPost, "/units/"+fix.unitCodes[1]+"/link",
		linkBody(fix.periodID, testTermDays)).
		mustStatus(t, http.StatusCreated, "apply for Room 2")
	requestID := applied.str(t, "request", "id")
	fix.owner.do(http.MethodPost, "/link-requests/"+requestID+"/approve", nil).
		mustStatus(t, http.StatusOK, "approve Room 2")

	if _, err := h.pool.Exec(context.Background(),
		"DELETE FROM contracts WHERE link_request_id = $1", requestID); err != nil {
		t.Fatalf("drop the contract: %v", err)
	}

	backfilled := fix.owner.do(http.MethodPost, "/link-requests/"+requestID+"/approve", nil).
		mustStatus(t, http.StatusOK, "backfill approve")
	newID := backfilled.str(t, "contract", "id")
	if newID == "" {
		t.Fatal("the backfill did not return a contract")
	}
	if status := backfilled.str(t, "request", "status"); status != "approved" {
		t.Errorf("request status = %q, want approved", status)
	}

	// A second run has nothing to do.
	fix.owner.do(http.MethodPost, "/link-requests/"+requestID+"/approve", nil).
		mustStatus(t, http.StatusConflict, "re-approve with a contract in place")
}

// ------------------------------------------------------ creation rules --

// TestContractCreateRules pins the refusals from API.md: one live contract per
// unit, a unit that is available and priced, a renter the org knows, a period
// the unit offers.
func TestContractCreateRules(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "Rules", "0714000120", "+255714000121")
	today := time.Now().UTC().Format("2006-01-02")

	base := func(unitID string) map[string]any {
		return map[string]any{
			"unit_id": unitID, "renter_user_id": fix.renterID,
			"payment_period_id": fix.periodID, "term_days": 90, "start_date": today,
		}
	}

	t.Run("a unit already under contract is a 409", func(t *testing.T) {
		resp := fix.owner.contractOn(t, base(fix.unitIDs[0])).
			mustStatus(t, http.StatusConflict, "second contract on Room 1")
		if got := resp.str(t, "type"); got != "contract_exists" {
			t.Errorf("type = %q, want contract_exists", got)
		}
	})

	t.Run("a renter the org has never met is a 404", func(t *testing.T) {
		stranger := h.registerRenter("+255714000199", "Stranger", defaultPIN)
		me := stranger.do(http.MethodGet, "/auth/me?audience=renter", nil).
			mustStatus(t, http.StatusOK, "stranger me")
		body := base(fix.unitIDs[1])
		body["renter_user_id"] = me.str(t, "user", "id")
		fix.owner.contractOn(t, body).mustStatus(t, http.StatusNotFound, "unknown renter")
	})

	t.Run("a period the unit does not offer is a 409", func(t *testing.T) {
		otherOrg := h.newOrgWithUnits("RulesB", "rulesb@jjne.test", "0714000130", nil, 0)
		foreign := otherOrg.client.periodIDByDays(t, 90)
		body := base(fix.unitIDs[1])
		body["payment_period_id"] = foreign
		resp := fix.owner.contractOn(t, body).
			mustStatus(t, http.StatusConflict, "another org's period")
		if got := resp.str(t, "type"); got != "period_not_offered" {
			t.Errorf("type = %q, want period_not_offered", got)
		}
	})

	t.Run("an unpriced unit is a 409", func(t *testing.T) {
		prop := fix.owner.do(http.MethodGet, "/properties", nil).
			mustStatus(t, http.StatusOK, "properties")
		propertyID, _ := listOf(t, prop)[0]["id"].(string)
		created := fix.owner.do(http.MethodPost, "/properties/"+propertyID+"/units",
			map[string]any{"name": "Unpriced"}).
			mustStatus(t, http.StatusCreated, "unpriced unit")
		resp := fix.owner.contractOn(t, base(created.str(t, "unit", "id"))).
			mustStatus(t, http.StatusConflict, "contract on an unpriced unit")
		if got := resp.str(t, "type"); got != "unit_not_priced" {
			t.Errorf("type = %q, want unit_not_priced", got)
		}
	})

	t.Run("a unit under maintenance is a 409", func(t *testing.T) {
		fix.owner.do(http.MethodPatch, "/units/"+fix.unitIDs[2],
			map[string]any{"status": "maintenance"}).
			mustStatus(t, http.StatusOK, "set maintenance")
		resp := fix.owner.contractOn(t, base(fix.unitIDs[2])).
			mustStatus(t, http.StatusConflict, "contract on a unit under maintenance")
		if got := resp.str(t, "type"); got != "unit_unavailable" {
			t.Errorf("type = %q, want unit_unavailable", got)
		}
		fix.owner.do(http.MethodPatch, "/units/"+fix.unitIDs[2],
			map[string]any{"status": "vacant"}).mustStatus(t, http.StatusOK, "back to vacant")
	})

	t.Run("bad input is a 400 with fields", func(t *testing.T) {
		body := base(fix.unitIDs[1])
		body["term_days"] = 0
		body["due_day"] = 45
		resp := fix.owner.contractOn(t, body).
			mustStatus(t, http.StatusBadRequest, "invalid contract")
		errs, ok := resp.Body["errors"].(map[string]any)
		if !ok || errs["term_days"] == nil || errs["due_day"] == nil {
			t.Errorf("errors = %v, want term_days and due_day", resp.Body["errors"])
		}
	})
}

// ------------------------------------------------------------ signing --

// TestSignFlow is the SPEC §5.5 evidence bundle end to end: OTP to the phone on
// the account, a signature row carrying the hash that was on screen, and a
// verification that notices if the stored terms are edited afterwards.
func TestSignFlow(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "Sign", "0714000140", "+255714000141")
	id := fix.contractID

	fix.renter.do(http.MethodPost, "/contracts/"+id+"/sign/otp", nil).
		mustStatus(t, http.StatusAccepted, "sign otp")

	t.Run("a wrong code is a 400", func(t *testing.T) {
		resp := fix.renter.do(http.MethodPost, "/contracts/"+id+"/sign",
			map[string]any{"otp_code": "000000"}).
			mustStatus(t, http.StatusBadRequest, "wrong code")
		if errs, ok := resp.Body["errors"].(map[string]any); !ok || errs["otp_code"] == nil {
			t.Errorf("errors = %v, want otp_code", resp.Body["errors"])
		}
	})

	code := h.sms.LastOTP(fix.renterPhone)
	signed := fix.renter.do(http.MethodPost, "/contracts/"+id+"/sign",
		map[string]any{"otp_code": code}).mustStatus(t, http.StatusOK, "sign")

	sigs := arrayOf(t, response{Body: signed.Body["contract"].(map[string]any), Raw: signed.Raw}, "signatures")
	if len(sigs) != 1 {
		t.Fatalf("signatures = %v, want one renter row", sigs)
	}
	if sigs[0]["party"] != "renter" {
		t.Errorf("party = %v, want renter", sigs[0]["party"])
	}
	if sigs[0]["method"] != "otp_accept" {
		t.Errorf("method = %v, want otp_accept (no drawn image was supplied)", sigs[0]["method"])
	}
	if masked, _ := sigs[0]["phone_masked"].(string); !strings.HasSuffix(masked, fix.renterPhone[len(fix.renterPhone)-4:]) {
		t.Errorf("phone_masked = %v, want the last four digits", sigs[0]["phone_masked"])
	}

	// The row carries the transport evidence the spec asks for.
	var ip, ua, hash string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT coalesce(ip,''), coalesce(user_agent,''), snapshot_hash
		 FROM contract_signatures WHERE contract_id = $1 AND party = 'renter'`, id).
		Scan(&ip, &ua, &hash); err != nil {
		t.Fatalf("read the signature row: %v", err)
	}
	if ip == "" {
		t.Error("the signature row records no IP address")
	}
	if hash != signed.str(t, "contract", "snapshot_hash") {
		t.Error("the signature row does not carry the hash that was signed")
	}

	t.Run("signing twice is a 409", func(t *testing.T) {
		// Asking for another code is already refused, and so is a second
		// signature — before the code is spent, so the answer is a 409 rather
		// than "your code is wrong".
		otp := fix.renter.do(http.MethodPost, "/contracts/"+id+"/sign/otp", nil).
			mustStatus(t, http.StatusConflict, "second signing code")
		if got := otp.str(t, "type"); got != "already_signed" {
			t.Errorf("type = %q, want already_signed", got)
		}
		resp := fix.renter.do(http.MethodPost, "/contracts/"+id+"/sign",
			map[string]any{"otp_code": code}).
			mustStatus(t, http.StatusConflict, "second signature")
		if got := resp.str(t, "type"); got != "already_signed" {
			t.Errorf("type = %q, want already_signed", got)
		}
	})

	t.Run("verify is valid, and notices tampering", func(t *testing.T) {
		ok := fix.owner.do(http.MethodGet, "/contracts/"+id+"/verify", nil).
			mustStatus(t, http.StatusOK, "verify")
		if valid, _ := ok.Body["valid"].(bool); !valid {
			t.Fatalf("a freshly signed contract does not verify: %s", ok.Raw)
		}
		if ok.str(t, "computed_hash") != ok.str(t, "stored_hash") {
			t.Error("computed and stored hashes differ on an untouched contract")
		}

		// Edit the snapshot the way only direct database access could.
		if _, err := h.pool.Exec(context.Background(),
			`UPDATE contracts SET terms_snapshot_html = terms_snapshot_html || '<p>and a pony</p>'
			 WHERE id = $1`, id); err != nil {
			t.Fatalf("tamper with the snapshot: %v", err)
		}
		bad := fix.owner.do(http.MethodGet, "/contracts/"+id+"/verify", nil).
			mustStatus(t, http.StatusOK, "verify after tampering")
		if valid, _ := bad.Body["valid"].(bool); valid {
			t.Error("a tampered contract still verifies as valid")
		}
		if bad.str(t, "computed_hash") == bad.str(t, "stored_hash") {
			t.Error("the recomputed hash did not change after the terms did")
		}
	})
}

// TestSignRefusesADocumentThatChanged: the renter must never be bound to terms
// that moved between reading and signing (SPEC §5.5).
func TestSignRefusesADocumentThatChanged(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "Tamper", "0714000150", "+255714000151")

	fix.renter.do(http.MethodPost, "/contracts/"+fix.contractID+"/sign/otp", nil).
		mustStatus(t, http.StatusAccepted, "sign otp")
	code := h.sms.LastOTP(fix.renterPhone)

	if _, err := h.pool.Exec(context.Background(),
		`UPDATE contracts SET rent_amount = rent_amount + 1 WHERE id = $1`, fix.contractID); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	resp := fix.renter.do(http.MethodPost, "/contracts/"+fix.contractID+"/sign",
		map[string]any{"otp_code": code}).
		mustStatus(t, http.StatusConflict, "sign a changed document")
	if got := resp.str(t, "type"); got != "snapshot_mismatch" {
		t.Errorf("type = %q, want snapshot_mismatch", got)
	}
}

// ------------------------------------------------------- activation --

// TestActivateRequiresARenterSignature is the 412 from API.md, and the one
// deliberate way past it (FLOWS 3.6).
func TestActivateRequiresARenterSignature(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "Activate", "0714000160", "+255714000161")

	resp := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/activate", nil).
		mustStatus(t, http.StatusPreconditionFailed, "activate unsigned")
	if got := resp.str(t, "type"); got != "renter_signature_required" {
		t.Errorf("type = %q, want renter_signature_required", got)
	}

	t.Run("landlord_recorded needs a reason", func(t *testing.T) {
		fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/activate",
			map[string]any{"landlord_recorded": true}).
			mustStatus(t, http.StatusBadRequest, "no reason given")
	})

	t.Run("landlord_recorded activates under its own audit action", func(t *testing.T) {
		activated := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/activate",
			map[string]any{"landlord_recorded": true, "reason": "renter has no phone"}).
			mustStatus(t, http.StatusOK, "landlord-recorded activation")
		if status := activated.str(t, "contract", "status"); status != "active" {
			t.Errorf("status = %q, want active", status)
		}
		rows := h.auditPayloads(t, "contract.activate_landlord_recorded")
		if len(rows) != 1 {
			t.Fatalf("audit_log holds %d contract.activate_landlord_recorded rows, want 1", len(rows))
		}
		if !strings.Contains(rows[0], "renter has no phone") {
			t.Errorf("the audit row does not record the reason: %s", rows[0])
		}
		if got := len(h.auditPayloads(t, "contract.activate")); got != 0 {
			t.Errorf("a landlord-recorded activation also wrote %d plain contract.activate rows", got)
		}
		// No renter signature row exists — only the landlord's.
		sigs := arrayOf(t, response{Body: activated.Body["contract"].(map[string]any), Raw: activated.Raw}, "signatures")
		if len(sigs) != 1 || sigs[0]["party"] != "landlord" {
			t.Errorf("signatures = %v, want only the landlord's", sigs)
		}
	})
}

// TestActivationGeneratesSchedulesAndOccupiesTheUnit is the exit criterion of
// Phase 4: the whole span materialises at the contract's cadence, prorated from
// the snapshotted rent, and the unit leaves the vacancy board.
func TestActivationGeneratesSchedulesAndOccupiesTheUnit(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "Sched", "0714000170", "+255714000171")

	signed := h.signAsRenter(t, fix.renter, fix.contractID, fix.renterPhone)
	if status := signed.str(t, "contract", "status"); status != "pending_signature" {
		t.Errorf("status after signing = %q, want pending_signature until the landlord activates", status)
	}

	activated := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/activate", nil).
		mustStatus(t, http.StatusOK, "activate")
	if status := activated.str(t, "contract", "status"); status != "active" {
		t.Fatalf("status = %q, want active", status)
	}
	if activated.Body["contract"].(map[string]any)["activated_at"] == nil {
		t.Error("activated_at is null after activation")
	}

	// 180-day term at a 30-day cadence: six full rows of the full rent.
	rows := listOf(t, fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID+"/schedules", nil).
		mustStatus(t, http.StatusOK, "schedules"))
	if len(rows) != 6 {
		t.Fatalf("schedules = %d rows, want 6 (180 days at 30-day cadence)", len(rows))
	}
	for i, row := range rows {
		if got := int64(mustFloat(t, row, "amount")); got != phase4Rent {
			t.Errorf("row %d amount = %d, want %d", i, got, phase4Rent)
		}
		if row["status"] != "pending" {
			t.Errorf("row %d status = %v, want pending", i, row["status"])
		}
		if got := mustFloat(t, row, "paid_amount"); got != 0 {
			t.Errorf("row %d paid_amount = %v, want 0", i, got)
		}
	}

	if s := h.unitStatus(t, fix.owner, fix.unitIDs[0]); s != "occupied" {
		t.Errorf("unit status = %q, want occupied", s)
	}
	if notes := ofKind(h.notifications(t), "welcome"); len(notes) != 1 {
		t.Errorf("welcome notifications = %+v, want 1", notes)
	}

	// The renter's own money view lines up with it.
	mine := fix.renter.do(http.MethodGet, "/me/schedules", nil).
		mustStatus(t, http.StatusOK, "my schedules")
	if got := len(listOf(t, mine)); got != 6 {
		t.Errorf("the renter sees %d schedule rows, want 6", got)
	}
	next, ok := mine.Body["next_due"].(map[string]any)
	if !ok {
		t.Fatalf("next_due is missing: %s", mine.Raw)
	}
	if next["due_date"] != rows[0]["due_date"] {
		t.Errorf("next_due.due_date = %v, want the first row's %v", next["due_date"], rows[0]["due_date"])
	}
}

// TestScheduleShapes pins the two cadence cases from PLAN Phase 4 against the
// rows actually written, not only against the pure generator.
func TestScheduleShapes(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "Shapes", "0714000180", "+255714000181")
	start := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")

	cases := []struct {
		name       string
		label      string
		unitIndex  int
		periodDays int
		termDays   int
		wantDays   []int
	}{
		{"180 days at a 45-day cadence", "45-day", 1, 45, 180, []int{45, 45, 45, 45}},
		{"100 days at a 30-day cadence truncates the last row", "100-day run", 2, 30, 100, []int{30, 30, 30, 10}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The seeded 30-day period is reused; anything else is added.
			periodID := fix.periodID
			if tc.periodDays != 30 {
				periodID = fix.owner.customPeriod(t, tc.label, tc.periodDays)
			}
			created := fix.owner.contractOn(t, map[string]any{
				"unit_id": fix.unitIDs[tc.unitIndex], "renter_user_id": fix.renterID,
				"payment_period_id": periodID, "term_days": tc.termDays, "start_date": start,
			}).mustStatus(t, http.StatusCreated, "create contract")
			id := created.str(t, "contract", "id")

			h.signAsRenter(t, fix.renter, id, fix.renterPhone)
			fix.owner.do(http.MethodPost, "/contracts/"+id+"/activate", nil).
				mustStatus(t, http.StatusOK, "activate")

			rows := listOf(t, fix.owner.do(http.MethodGet, "/contracts/"+id+"/schedules", nil).
				mustStatus(t, http.StatusOK, "schedules"))
			if len(rows) != len(tc.wantDays) {
				t.Fatalf("schedules = %d rows, want %d", len(rows), len(tc.wantDays))
			}
			var total int64
			for i, row := range rows {
				want := contract.Prorate(phase4Rent, tc.wantDays[i], 30)
				got := int64(mustFloat(t, row, "amount"))
				if got != want {
					t.Errorf("row %d amount = %d, want %d (%d days prorated from a 30-day basis)",
						i, got, want, tc.wantDays[i])
				}
				total += got

				from, _ := row["period_start"].(string)
				to, _ := row["period_end"].(string)
				if days := inclusiveDays(t, from, to); days != tc.wantDays[i] {
					t.Errorf("row %d spans %d days (%s…%s), want %d", i, days, from, to, tc.wantDays[i])
				}
			}
			if total <= 0 {
				t.Error("the schedule totals nothing")
			}
		})
	}
}

// TestDueDaySnapsScheduleDueDates: with a due day set, every row falls due on
// that day of the month rather than on the day its period happens to start.
func TestDueDaySnapsScheduleDueDates(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "DueDay", "0714000190", "+255714000191")

	created := fix.owner.contractOn(t, map[string]any{
		"unit_id": fix.unitIDs[1], "renter_user_id": fix.renterID,
		"payment_period_id": fix.periodID, "term_days": 90,
		"start_date": "2026-10-03", "due_day": 5,
	}).mustStatus(t, http.StatusCreated, "create with a due day")
	id := created.str(t, "contract", "id")
	if got := num(t, created, "contract", "due_day"); got != 5 {
		t.Errorf("due_day = %v, want 5", got)
	}

	h.signAsRenter(t, fix.renter, id, fix.renterPhone)
	fix.owner.do(http.MethodPost, "/contracts/"+id+"/activate", nil).
		mustStatus(t, http.StatusOK, "activate")

	rows := listOf(t, fix.owner.do(http.MethodGet, "/contracts/"+id+"/schedules", nil).
		mustStatus(t, http.StatusOK, "schedules"))
	if len(rows) != 3 {
		t.Fatalf("schedules = %d rows, want 3", len(rows))
	}
	for i, row := range rows {
		due, _ := row["due_date"].(string)
		if !strings.HasSuffix(due, "-05") {
			t.Errorf("row %d due_date = %q, want the 5th of a month", i, due)
		}
		start, _ := row["period_start"].(string)
		if due < start {
			t.Errorf("row %d falls due (%s) before its period starts (%s)", i, due, start)
		}
	}
}

// ------------------------------------------------------- termination --

// TestTerminateWaivesRemainingSchedulesAndFreesTheUnit is FLOWS 6.5.
func TestTerminateWaivesRemainingSchedulesAndFreesTheUnit(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "Term", "0714000200", "+255714000201")
	h.signAsRenter(t, fix.renter, fix.contractID, fix.renterPhone)
	fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/activate", nil).
		mustStatus(t, http.StatusOK, "activate")

	terminated := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/terminate",
		map[string]any{"reason": "renter moving abroad"}).
		mustStatus(t, http.StatusOK, "terminate")
	if status := terminated.str(t, "contract", "status"); status != "terminated" {
		t.Fatalf("status = %q, want terminated", status)
	}
	if reason := terminated.str(t, "contract", "termination_reason"); reason != "renter moving abroad" {
		t.Errorf("termination_reason = %q", reason)
	}

	rows := listOf(t, fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID+"/schedules", nil).
		mustStatus(t, http.StatusOK, "schedules"))
	var waived, standing int
	for _, row := range rows {
		if row["status"] == "waived" {
			waived++
			continue
		}
		standing++
	}
	if waived == 0 {
		t.Error("termination waived nothing")
	}
	// The period the renter is living through is not waived — they lived there.
	if standing != 1 {
		t.Errorf("%d rows still stand, want 1 (the period in progress)", standing)
	}

	if s := h.unitStatus(t, fix.owner, fix.unitIDs[0]); s != "vacant" {
		t.Errorf("unit status = %q, want vacant after termination", s)
	}
	if notes := ofKind(h.notifications(t), "contract_terminated"); len(notes) != 1 {
		t.Errorf("contract_terminated notifications = %+v, want 1", notes)
	}
	// And the unit can be let again.
	fix.owner.contractOn(t, map[string]any{
		"unit_id": fix.unitIDs[0], "renter_user_id": fix.renterID,
		"payment_period_id": fix.periodID, "term_days": 30,
		"start_date": time.Now().UTC().Format("2006-01-02"),
	}).mustStatus(t, http.StatusCreated, "re-let the freed unit")
}

// TestTerminateCancelsAnUnsignedContract: FLOWS 3 edge case — the renter never
// signed, so the landlord cancels through the same endpoint.
func TestTerminateCancelsAnUnsignedContract(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "Cancel", "0714000210", "+255714000211")

	fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/terminate",
		map[string]any{"reason": "never signed"}).
		mustStatus(t, http.StatusOK, "cancel an unsigned contract")
	if s := h.unitStatus(t, fix.owner, fix.unitIDs[0]); s != "vacant" {
		t.Errorf("unit status = %q, want vacant", s)
	}
	// Terminating twice is a 409 — there is nothing left to end.
	fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/terminate",
		map[string]any{"reason": "again"}).
		mustStatus(t, http.StatusConflict, "terminate twice")
}

// -------------------------------------------------------- lifecycle --

// TestLifecycleFlagsExpiringAndEndsLapsedContracts is FLOWS 6.3/6.4: the sweep
// is what moves a contract through the end of its own term.
func TestLifecycleFlagsExpiringAndEndsLapsedContracts(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "Life", "0714000220", "+255714000221")
	h.signAsRenter(t, fix.renter, fix.contractID, fix.renterPhone)
	fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/activate", nil).
		mustStatus(t, http.StatusOK, "activate")

	setEnd := func(t *testing.T, expr string) {
		t.Helper()
		if _, err := h.pool.Exec(context.Background(),
			"UPDATE contracts SET end_date = "+expr+" WHERE id = $1", fix.contractID); err != nil {
			t.Fatalf("move the end date: %v", err)
		}
	}
	statusNow := func(t *testing.T) string {
		t.Helper()
		return fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID, nil).
			mustStatus(t, http.StatusOK, "read contract").str(t, "contract", "status")
	}

	// Far from its end date, a sweep changes nothing.
	if _, err := contract.RunLifecycle(context.Background(), h.pool); err != nil {
		t.Fatalf("lifecycle sweep: %v", err)
	}
	if got := statusNow(t); got != "active" {
		t.Fatalf("status = %q, want active well before the end date", got)
	}

	// Inside the 30-day notice window it flags as expiring.
	setEnd(t, "CURRENT_DATE + 10")
	if res, err := contract.RunLifecycle(context.Background(), h.pool); err != nil {
		t.Fatalf("lifecycle sweep: %v", err)
	} else if res.Expiring != 1 {
		t.Errorf("sweep flagged %d contracts expiring, want 1", res.Expiring)
	}
	if got := statusNow(t); got != "expiring" {
		t.Errorf("status = %q, want expiring", got)
	}

	// Past its end date it closes, and the unit returns to the board.
	setEnd(t, "CURRENT_DATE - 1")
	res, err := contract.RunLifecycle(context.Background(), h.pool)
	if err != nil {
		t.Fatalf("lifecycle sweep: %v", err)
	}
	if res.Ended != 1 || res.Freed != 1 {
		t.Errorf("sweep ended %d and freed %d, want 1 and 1", res.Ended, res.Freed)
	}
	if got := statusNow(t); got != "ended" {
		t.Errorf("status = %q, want ended", got)
	}
	if s := h.unitStatus(t, fix.owner, fix.unitIDs[0]); s != "vacant" {
		t.Errorf("unit status = %q, want vacant once the tenancy ended", s)
	}

	// The sweep is idempotent: running it again moves nothing.
	if again, err := contract.RunLifecycle(context.Background(), h.pool); err != nil {
		t.Fatalf("second sweep: %v", err)
	} else if again.Ended != 0 || again.Expiring != 0 {
		t.Errorf("a repeated sweep moved %+v, want nothing", again)
	}
}

// --------------------------------------------------------- documents --

// TestContractDocumentServesBothParties: one document, two readers (SPEC §5.5).
func TestContractDocumentServesBothParties(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "Doc", "0714000230", "+255714000231")

	for name, c := range map[string]*client{"landlord": fix.owner, "renter": fix.renter} {
		doc := c.do(http.MethodGet, "/contracts/"+fix.contractID+"/document", nil).
			mustStatus(t, http.StatusOK, name+" reads the document")
		if got := doc.str(t, "contract_id"); got != fix.contractID {
			t.Errorf("%s: contract_id = %q", name, got)
		}
		terms := doc.str(t, "terms_html")
		if !strings.Contains(terms, "Tenancy Agreement") {
			t.Errorf("%s: the document carries no rendered terms: %q", name, terms)
		}
		if strings.Contains(terms, "{{") {
			t.Errorf("%s: the document still carries unresolved variables: %q", name, terms)
		}
		if got := doc.str(t, "parties", "renter", "name"); got == "" {
			t.Errorf("%s: the document names no renter", name)
		}
		if len(doc.str(t, "snapshot_hash")) != 64 {
			t.Errorf("%s: the document carries no verification hash", name)
		}
		// Before activation the schedule is the preview; the renter must see
		// the money before agreeing to it.
		if got := len(arrayOf(t, doc, "schedule")); got != 6 {
			t.Errorf("%s: the document shows %d schedule rows, want 6", name, got)
		}
	}
}

// TestRenterCannotReadAnotherRentersContract: the renter routes are scoped to
// the caller, and another renter's contract is a 404 rather than a 403.
func TestRenterCannotReadAnotherRentersContract(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "Priv", "0714000240", "+255714000241")
	other := h.registerRenter("+255714000249", "Someone Else", defaultPIN)

	for _, path := range []string{"", "/document", "/verify", "/schedules"} {
		other.do(http.MethodGet, "/contracts/"+fix.contractID+path, nil).
			mustStatus(t, http.StatusNotFound, "another renter reading "+path)
	}
	other.do(http.MethodPost, "/contracts/"+fix.contractID+"/sign/otp", nil).
		mustStatus(t, http.StatusNotFound, "another renter requesting a signing code")
	other.do(http.MethodPost, "/contracts/"+fix.contractID+"/sign",
		map[string]any{"otp_code": "123456"}).
		mustStatus(t, http.StatusNotFound, "another renter signing")

	if got := len(listOf(t, other.do(http.MethodGet, "/me/contracts", nil).
		mustStatus(t, http.StatusOK, "their own list"))); got != 0 {
		t.Errorf("an unrelated renter sees %d contracts, want 0", got)
	}
}

// ---------------------------------------------------------- templates --

// TestTemplateCRUDAndPreview covers the terms library, including the sanitizer
// at the endpoint: what a landlord pastes is not what is stored.
func TestTemplateCRUDAndPreview(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Tpl", "tpl@jjne.test", "0714000250", nil, 0)
	c := fix.client

	seeded := listOf(t, c.do(http.MethodGet, "/contract-templates", nil).
		mustStatus(t, http.StatusOK, "list templates"))
	if len(seeded) != 1 {
		t.Fatalf("a new org has %d templates, want the seeded default", len(seeded))
	}
	if seeded[0]["name"] != "Standard tenancy agreement" || seeded[0]["is_default"] != true {
		t.Errorf("the seeded template is %v, want the default standard agreement", seeded[0])
	}
	defaultID, _ := seeded[0]["id"].(string)

	full := c.do(http.MethodGet, "/contract-templates/"+defaultID, nil).
		mustStatus(t, http.StatusOK, "read the default")
	tpl, ok := full.Body["template"].(map[string]any)
	if !ok {
		t.Fatalf("the response carries no template object: %s", full.Raw)
	}
	if vars, ok := tpl["variables"].([]any); !ok || len(vars) != len(contract.Variables) {
		t.Errorf("the template offers %v variables, want %d", tpl["variables"], len(contract.Variables))
	}

	t.Run("a submitted body is sanitized before it is stored", func(t *testing.T) {
		created := c.do(http.MethodPost, "/contract-templates", map[string]any{
			"name": "Short form",
			"body_html": `<h1>Terms</h1><script>alert(1)</script>` +
				`<p onclick="x()">Rent {{rent}}</p><a href="https://evil.example">click</a>`,
		}).mustStatus(t, http.StatusCreated, "create template")
		body := created.str(t, "template", "body_html")
		for _, banned := range []string{"script", "onclick", "href", "alert(1)"} {
			if strings.Contains(body, banned) {
				t.Errorf("the stored body still carries %q: %s", banned, body)
			}
		}
		if !strings.Contains(body, "<h1>Terms</h1>") || !strings.Contains(body, "{{rent}}") {
			t.Errorf("the sanitizer ate the usable content: %s", body)
		}

		id := created.str(t, "template", "id")
		t.Run("a body with nothing left after sanitizing is a 400", func(t *testing.T) {
			c.do(http.MethodPost, "/contract-templates", map[string]any{
				"name": "Empty", "body_html": `<script>alert(1)</script>`,
			}).mustStatus(t, http.StatusBadRequest, "all-markup body")
		})

		t.Run("preview resolves the variables with sample values", func(t *testing.T) {
			preview := c.do(http.MethodPost, "/contract-templates/"+id+"/preview",
				map[string]any{"sample": true}).
				mustStatus(t, http.StatusOK, "preview")
			html := preview.str(t, "html")
			if strings.Contains(html, "{{") {
				t.Errorf("the preview left variables unresolved: %s", html)
			}
			if !strings.Contains(html, "TZS 250,000") {
				t.Errorf("the preview does not carry the sample rent: %s", html)
			}
			if preview.str(t, "display_name") == "" {
				t.Error("the preview carries no display name for the letterhead")
			}
		})

		t.Run("promoting a template demotes the incumbent", func(t *testing.T) {
			c.do(http.MethodPatch, "/contract-templates/"+id,
				map[string]any{"is_default": true}).
				mustStatus(t, http.StatusOK, "promote")
			for _, row := range listOf(t, c.do(http.MethodGet, "/contract-templates", nil).
				mustStatus(t, http.StatusOK, "list")) {
				want := row["id"] == id
				if row["is_default"] != want {
					t.Errorf("template %v is_default = %v, want %v", row["id"], row["is_default"], want)
				}
			}
		})

		t.Run("the default cannot be deleted", func(t *testing.T) {
			c.do(http.MethodDelete, "/contract-templates/"+id, nil).
				mustStatus(t, http.StatusConflict, "delete the default")
			c.do(http.MethodDelete, "/contract-templates/"+defaultID, nil).
				mustStatus(t, http.StatusNoContent, "delete a non-default")
			if got := len(listOf(t, c.do(http.MethodGet, "/contract-templates", nil).
				mustStatus(t, http.StatusOK, "list after delete"))); got != 1 {
				t.Errorf("templates = %d, want 1 after the delete", got)
			}
		})
	})
}

// ----------------------------------------------------------- branding --

// TestBranding covers the settings a contract document is dressed with.
func TestBranding(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Brand", "brand@jjne.test", "0714000260", nil, 0)
	c := fix.client

	got := c.do(http.MethodGet, "/org/branding", nil).
		mustStatus(t, http.StatusOK, "read branding")
	if got.str(t, "display_name") != "Brand" {
		t.Errorf("display_name = %q, want the org name", got.str(t, "display_name"))
	}
	if got.Body["logo_url"] != nil || got.Body["letterhead_url"] != nil {
		t.Error("a fresh org already has image URLs")
	}

	updated := c.do(http.MethodPut, "/org/branding", map[string]any{
		"display_name":         "Brand Estates",
		"theme":                map[string]any{"primary_color": "#0A7C4A", "font_id": "archivo"},
		"document_footer_text": "Registered in Dar es Salaam",
	}).mustStatus(t, http.StatusOK, "update branding")
	if updated.str(t, "branding", "display_name") != "Brand Estates" {
		t.Errorf("display_name = %q", updated.str(t, "branding", "display_name"))
	}
	// Phase 12 canonicalises colours to lower-case `#rrggbb`: one form for a
	// value that ends up in a CSS custom property.
	if updated.str(t, "branding", "theme", "primary_color") != "#0a7c4a" {
		t.Errorf("primary_color = %q", updated.str(t, "branding", "theme", "primary_color"))
	}
	if updated.str(t, "branding", "document_footer_text") != "Registered in Dar es Salaam" {
		t.Errorf("document_footer_text = %q", updated.str(t, "branding", "document_footer_text"))
	}

	t.Run("a colour or font outside the whitelist is a 400", func(t *testing.T) {
		resp := c.do(http.MethodPut, "/org/branding", map[string]any{
			"theme": map[string]any{"primary_color": "red", "font_id": "comic-sans"},
		}).mustStatus(t, http.StatusBadRequest, "bad theme")
		errs, _ := resp.Body["errors"].(map[string]any)
		if errs["theme.primary_color"] == nil || errs["theme.font_id"] == nil {
			t.Errorf("errors = %v, want both theme fields", resp.Body["errors"])
		}
	})

	t.Run("an upload key issued to another org is refused", func(t *testing.T) {
		if h.store == nil {
			t.Skip("SKIP: MinIO unreachable")
		}
		c.do(http.MethodPost, "/org/branding/logo/complete",
			map[string]any{"object_key": "00000000-0000-0000-0000-000000000000/logo.png"}).
			mustStatus(t, http.StatusBadRequest, "another org's key")
	})

	t.Run("the public endpoint shows the display name", func(t *testing.T) {
		anon := h.client()
		pub := anon.do(http.MethodGet, "/public/orgs/brand/branding", nil).
			mustStatus(t, http.StatusOK, "public branding")
		if pub.str(t, "display_name") != "Brand Estates" {
			t.Errorf("public display_name = %q", pub.str(t, "display_name"))
		}
	})
}

// ---------------------------------------------------------- isolation --

// TestPhase4OrgIsolation is the SPEC §8 promise on every route Phase 4 adds:
// org B gets a 404, never a 403 and never another org's data.
func TestPhase4OrgIsolation(t *testing.T) {
	h := newHarness(t)
	a := h.newContractFixture(t, "IsoA4", "0714000270", "+255714000271")
	b := h.newOrgWithUnits("IsoB4", "isob4@jjne.test", "0714000280", []string{"Room 1"}, phase4Rent)

	templateID, _ := listOf(t, a.owner.do(http.MethodGet, "/contract-templates", nil).
		mustStatus(t, http.StatusOK, "org A templates"))[0]["id"].(string)

	reads := []struct{ method, path string }{
		{http.MethodGet, "/contracts/" + a.contractID},
		{http.MethodGet, "/contracts/" + a.contractID + "/document"},
		{http.MethodGet, "/contracts/" + a.contractID + "/verify"},
		{http.MethodGet, "/contracts/" + a.contractID + "/schedules"},
		{http.MethodGet, "/contract-templates/" + templateID},
	}
	for _, tc := range reads {
		b.client.do(tc.method, tc.path, nil).
			mustStatus(t, http.StatusNotFound, "org B "+tc.method+" "+tc.path)
	}

	writes := []struct {
		method, path string
		body         map[string]any
	}{
		{http.MethodPost, "/contracts/" + a.contractID + "/activate", nil},
		{http.MethodPost, "/contracts/" + a.contractID + "/terminate", map[string]any{"reason": "not mine"}},
		{http.MethodPatch, "/contract-templates/" + templateID, map[string]any{"name": "mine now"}},
		{http.MethodDelete, "/contract-templates/" + templateID, nil},
		{http.MethodPost, "/contract-templates/" + templateID + "/preview", map[string]any{"sample": true}},
	}
	for _, tc := range writes {
		b.client.do(tc.method, tc.path, tc.body).
			mustStatus(t, http.StatusNotFound, "org B "+tc.method+" "+tc.path)
	}

	// Nor can org B write a contract against org A's unit or renter.
	b.client.contractOn(t, map[string]any{
		"unit_id": a.unitIDs[1], "renter_user_id": a.renterID,
		"payment_period_id": b.client.periodIDByDays(t, 30), "term_days": 30,
		"start_date": time.Now().UTC().Format("2006-01-02"),
	}).mustStatus(t, http.StatusNotFound, "org B contracting on org A's unit")

	// Org B's list is empty, and org A's contract is untouched.
	if got := len(listOf(t, b.client.do(http.MethodGet, "/contracts", nil).
		mustStatus(t, http.StatusOK, "org B contracts"))); got != 0 {
		t.Errorf("org B's contract list holds %d rows, want 0", got)
	}
	if got := a.owner.do(http.MethodGet, "/contracts/"+a.contractID, nil).
		mustStatus(t, http.StatusOK, "org A reads its own").str(t, "contract", "status"); got != "pending_signature" {
		t.Errorf("org A's contract status = %q, want pending_signature", got)
	}
}

// ------------------------------------------------------------ helpers --

// inclusiveDays counts the days a period covers, both ends included.
func inclusiveDays(t *testing.T, from, to string) int {
	t.Helper()
	start, err := time.Parse("2006-01-02", from)
	if err != nil {
		t.Fatalf("parse %q: %v", from, err)
	}
	end, err := time.Parse("2006-01-02", to)
	if err != nil {
		t.Fatalf("parse %q: %v", to, err)
	}
	return int(end.Sub(start).Hours()/24) + 1
}

// TestCreatingATemplateAsDefaultDemotesTheIncumbent: an org has at most one
// default (partial unique index), so creating one straight to default has to
// demote the seeded template rather than collide with it.
func TestCreatingATemplateAsDefaultDemotesTheIncumbent(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("TplDef", "tpldef@jjne.test", "0714000252", nil, 0)
	c := fix.client

	created := c.do(http.MethodPost, "/contract-templates", map[string]any{
		"name": "House rules", "body_html": "<p>Rent {{rent}}</p>", "is_default": true,
	}).mustStatus(t, http.StatusCreated, "create as default")
	id := created.str(t, "template", "id")

	for _, row := range listOf(t, c.do(http.MethodGet, "/contract-templates", nil).
		mustStatus(t, http.StatusOK, "list")) {
		want := row["id"] == id
		if row["is_default"] != want {
			t.Errorf("template %v is_default = %v, want %v", row["id"], row["is_default"], want)
		}
	}
}

// TestActivateRefusesADocumentThatChanged: the landlord countersigns the same
// document the renter signed, so activation rechecks the hash the way signing
// does — a row edited under a signature must not become a live tenancy.
func TestActivateRefusesADocumentThatChanged(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "ActTamper", "0714000290", "+255714000291")
	h.signAsRenter(t, fix.renter, fix.contractID, fix.renterPhone)

	if _, err := h.pool.Exec(context.Background(),
		`UPDATE contracts SET rent_amount = rent_amount + 1 WHERE id = $1`, fix.contractID); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	resp := fix.owner.do(http.MethodPost, "/contracts/"+fix.contractID+"/activate", nil).
		mustStatus(t, http.StatusConflict, "activate a changed document")
	if got := resp.str(t, "type"); got != "snapshot_mismatch" {
		t.Errorf("type = %q, want snapshot_mismatch", got)
	}
	var status string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT status FROM contracts WHERE id = $1`, fix.contractID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "pending_signature" {
		t.Errorf("contract status = %q, want it left pending_signature", status)
	}
}

// TestRenterCannotCallOrgWrites: the reads are shared between the two parties,
// the writes are not (API.md Phase 4 notes). A renter session on an org-only
// write is refused, never served.
func TestRenterCannotCallOrgWrites(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "RWrite", "0714000300", "+255714000301")

	writes := []struct {
		method, path string
		body         map[string]any
	}{
		{http.MethodPost, "/contracts", map[string]any{
			"unit_id": fix.unitIDs[1], "renter_user_id": fix.renterID,
			"payment_period_id": fix.periodID, "term_days": 30,
			"start_date": time.Now().UTC().Format("2006-01-02"),
		}},
		{http.MethodPost, "/contracts/" + fix.contractID + "/activate", nil},
		{http.MethodPost, "/contracts/" + fix.contractID + "/terminate", map[string]any{"reason": "mine now"}},
		{http.MethodPost, "/contract-templates", map[string]any{"name": "x", "body_html": "<p>x</p>"}},
		{http.MethodGet, "/contract-templates", nil},
		{http.MethodPut, "/org/branding", map[string]any{"display_name": "Renter Estates"}},
	}
	for _, tc := range writes {
		resp := fix.renter.do(tc.method, tc.path, tc.body)
		if resp.Code != http.StatusUnauthorized && resp.Code != http.StatusNotFound {
			t.Errorf("renter %s %s: status = %d, want 401 or 404 — body: %s",
				tc.method, tc.path, resp.Code, resp.Raw)
		}
	}
	// And the contract is untouched.
	if got := fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID, nil).
		mustStatus(t, http.StatusOK, "owner reads").str(t, "contract", "status"); got != "pending_signature" {
		t.Errorf("contract status = %q, want pending_signature", got)
	}
}

// TestTermsReadWithoutADueDay: most contracts have no due day (the rows fall
// due on the day their period starts), and the seeded template still has to
// read as English for them.
func TestTermsReadWithoutADueDay(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "DueWords", "0714000310", "+255714000311")

	doc := fix.renter.do(http.MethodGet, "/contracts/"+fix.contractID+"/document", nil).
		mustStatus(t, http.StatusOK, "document")
	terms := doc.str(t, "terms_html")
	if !strings.Contains(terms, "on or before the first day of each payment period") {
		t.Errorf("the rent clause does not read without a due day: %s", terms)
	}
	if strings.Contains(terms, "before  of") || strings.Contains(terms, "day  of") {
		t.Errorf("the rent clause has a hole where the due day would be: %s", terms)
	}
}

// TestDrawnSignatureIsCheckedAgainstWhatLanded: the presigned PUT enforces
// neither content type nor size, so `POST /sign` checks the object itself —
// otherwise a "signature" could be an HTML page served back to both parties
// from the same origin as the app.
func TestDrawnSignatureIsCheckedAgainstWhatLanded(t *testing.T) {
	h := newHarness(t)
	if h.store == nil {
		t.Skip("SKIP: MinIO unreachable")
	}
	fix := h.newContractFixture(t, "Drawn", "0714000320", "+255714000321")
	id := fix.contractID

	ticket := fix.renter.do(http.MethodPost, "/contracts/"+id+"/signature-upload",
		map[string]any{"content_type": "image/png", "size_bytes": 2048}).
		mustStatus(t, http.StatusOK, "signature upload ticket")
	key := ticket.str(t, "object_key")

	// One code, used twice: a refused image must not burn the renter's OTP,
	// because the resend cooldown would then lock them out of signing.
	fix.renter.do(http.MethodPost, "/contracts/"+id+"/sign/otp", nil).
		mustStatus(t, http.StatusAccepted, "sign otp")
	code := h.sms.LastOTP(fix.renterPhone)
	sign := func(t *testing.T) response {
		t.Helper()
		return fix.renter.do(http.MethodPost, "/contracts/"+id+"/sign",
			map[string]any{"otp_code": code, "signature_object_key": key})
	}

	t.Run("an object that is not a PNG is refused and removed", func(t *testing.T) {
		if err := h.store.PutBytes(context.Background(), storage.BucketSignatures, key,
			[]byte("<html><script>alert(1)</script></html>"), "text/html"); err != nil {
			t.Fatalf("seed object: %v", err)
		}
		sign(t).mustStatus(t, http.StatusBadRequest, "html signature")
		if h.store.Exists(context.Background(), storage.BucketSignatures, key) {
			t.Error("the rejected upload was left in the bucket")
		}
	})

	t.Run("a PNG is accepted and recorded as drawn", func(t *testing.T) {
		if err := h.store.PutBytes(context.Background(), storage.BucketSignatures, key,
			make([]byte, 1024), "image/png"); err != nil {
			t.Fatalf("seed object: %v", err)
		}
		signed := sign(t).mustStatus(t, http.StatusOK, "drawn signature")
		sigs := arrayOf(t, response{Body: signed.Body["contract"].(map[string]any), Raw: signed.Raw}, "signatures")
		if len(sigs) != 1 || sigs[0]["method"] != "drawn" {
			t.Errorf("signatures = %v, want one drawn row", sigs)
		}
	})
}
