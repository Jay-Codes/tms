package httpserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"tms/backend/internal/storage"
)

// ------------------------------------------------------------- fixtures --

const (
	validNIDA      = "19900101123456789012" // 20 digits
	otherNIDA      = "20000202987654321098"
	testKinName    = "Neema Mrisho"
	testKinPhone   = "0713000999"
	defaultPIN     = "1234"
	testTermDays   = 180
	testUnitAmount = 250_000
)

// completeProfile fills in a renter's KYC so they pass the link-request gate.
func (c *client) completeProfile(t *testing.T, name, nida string) response {
	t.Helper()
	return c.do(http.MethodPut, "/me/profile", map[string]any{
		"full_name":         name,
		"nida_number":       nida,
		"next_of_kin_name":  testKinName,
		"next_of_kin_phone": testKinPhone,
	}).mustStatus(t, http.StatusOK, "put profile")
}

// periodIDByDays finds one of an org's seeded payment periods by day count.
func (c *client) periodIDByDays(t *testing.T, days int) string {
	t.Helper()
	list := c.do(http.MethodGet, "/org/payment-periods", nil).
		mustStatus(t, http.StatusOK, "list periods")
	for _, p := range listOf(t, list) {
		if int(mustFloat(t, p, "days")) == days {
			id, _ := p["id"].(string)
			return id
		}
	}
	t.Fatalf("no seeded payment period of %d days", days)
	return ""
}

// linkBody is the standard application: monthly cadence, 180-day term, today.
func linkBody(periodID string, termDays int) map[string]any {
	return map[string]any{
		"payment_period_id": periodID,
		"term_days":         termDays,
		"start_date":        time.Now().UTC().Format("2006-01-02"),
		"accepted_terms":    true,
	}
}

// setAutoApprove flips the org setting that approves link requests on arrival.
func (c *client) setAutoApprove(t *testing.T, on bool) {
	t.Helper()
	c.do(http.MethodPatch, "/org", map[string]any{
		"settings": map[string]any{"auto_approve_links": on},
	}).mustStatus(t, http.StatusOK, "patch org settings")
}

// notificationRows reads notification_log straight from Postgres — the row is
// the durable contract; Redis only carries the work item.
type notificationRow struct {
	Kind      string
	DedupeKey string
	Status    string
	ToPhone   string
	Body      string
}

func (h *harness) notifications(t *testing.T) []notificationRow {
	t.Helper()
	rows, err := h.pool.Query(context.Background(),
		`SELECT kind, dedupe_key, status, to_phone, body FROM notification_log ORDER BY created_at`)
	if err != nil {
		t.Fatalf("read notification_log: %v", err)
	}
	defer rows.Close()
	var out []notificationRow
	for rows.Next() {
		var n notificationRow
		if err := rows.Scan(&n.Kind, &n.DedupeKey, &n.Status, &n.ToPhone, &n.Body); err != nil {
			t.Fatalf("scan notification_log: %v", err)
		}
		out = append(out, n)
	}
	return out
}

// auditPayloads returns the before/after JSON of every audit row for an action.
func (h *harness) auditPayloads(t *testing.T, action string) []string {
	t.Helper()
	rows, err := h.pool.Query(context.Background(),
		`SELECT coalesce(before::text, '') || ' ' || coalesce(after::text, '')
		 FROM audit_log WHERE action = $1`, action)
	if err != nil {
		t.Fatalf("read audit_log: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan audit_log: %v", err)
		}
		out = append(out, s)
	}
	return out
}

// ------------------------------------------------------------ profile --

// TestProfileMasksNIDAAndKeepsItOutOfTheAuditTrail is the SPEC §8 promise: the
// number is encrypted at rest, only ever shown as its last four digits, and
// never written to the audit trail — the trail records only that it changed.
func TestProfileMasksNIDAAndKeepsItOutOfTheAuditTrail(t *testing.T) {
	h := newHarness(t)
	renter := h.registerRenter("+255712003001", "Asha Mrisho", defaultPIN)

	empty := renter.do(http.MethodGet, "/me/profile", nil).
		mustStatus(t, http.StatusOK, "get empty profile")
	if got := empty.str(t, "profile", "kyc_status"); got != "none" {
		t.Fatalf("a fresh profile has kyc_status %q, want none", got)
	}
	if nida := empty.Body["profile"].(map[string]any)["nida_masked"]; nida != nil {
		t.Fatalf("a fresh profile carries nida_masked %v, want null", nida)
	}

	saved := renter.completeProfile(t, "Asha Mrisho", validNIDA)
	masked := saved.str(t, "profile", "nida_masked")
	if want := "••••••••" + validNIDA[len(validNIDA)-4:]; masked != want {
		t.Errorf("nida_masked = %q, want %q", masked, want)
	}
	if strings.Contains(saved.Raw, validNIDA) {
		t.Errorf("the response body leaks the full NIDA number: %s", saved.Raw)
	}
	if got := saved.str(t, "profile", "kyc_status"); got != "submitted" {
		t.Errorf("kyc_status = %q, want submitted (nida + next of kin present)", got)
	}

	// The audit row must record the change without the number itself.
	payloads := h.auditPayloads(t, "renter_profile.update")
	if len(payloads) == 0 {
		t.Fatal("the profile update wrote no audit row")
	}
	for _, p := range payloads {
		if strings.Contains(p, validNIDA) {
			t.Errorf("an audit payload contains the NIDA number: %s", p)
		}
		if !strings.Contains(p, `"nida_changed": true`) && !strings.Contains(p, `"nida_changed":true`) {
			t.Errorf("audit payload does not record nida_changed: %s", p)
		}
	}

	// The ciphertext in the column must not be the number in the clear.
	var stored []byte
	if err := h.pool.QueryRow(context.Background(),
		`SELECT nida_number_enc FROM renter_profiles WHERE full_name = 'Asha Mrisho'`).Scan(&stored); err != nil {
		t.Fatalf("read nida_number_enc: %v", err)
	}
	if strings.Contains(string(stored), validNIDA) {
		t.Error("nida_number_enc holds the number in the clear")
	}
}

// TestProfileKeepsExistingNIDAWhenOmitted: the client only ever holds the mask,
// so an update that leaves the field out must not wipe the stored number.
func TestProfileKeepsExistingNIDAWhenOmitted(t *testing.T) {
	h := newHarness(t)
	renter := h.registerRenter("+255712003002", "Juma Ally", defaultPIN)
	renter.completeProfile(t, "Juma Ally", validNIDA)

	updated := renter.do(http.MethodPut, "/me/profile", map[string]any{
		"full_name":         "Juma A. Ally",
		"next_of_kin_name":  testKinName,
		"next_of_kin_phone": testKinPhone,
	}).mustStatus(t, http.StatusOK, "put profile without nida")

	if got := updated.str(t, "profile", "nida_masked"); got != "••••••••"+validNIDA[len(validNIDA)-4:] {
		t.Errorf("nida_masked = %q after an update that omitted it, want the stored value", got)
	}
	if got := updated.str(t, "profile", "kyc_status"); got != "submitted" {
		t.Errorf("kyc_status = %q, want submitted", got)
	}
}

func TestProfileValidation(t *testing.T) {
	h := newHarness(t)
	renter := h.registerRenter("+255712003003", "Halima Said", defaultPIN)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"a short NIDA is rejected", map[string]any{
			"full_name": "Halima Said", "nida_number": "12345",
			"next_of_kin_name": testKinName, "next_of_kin_phone": testKinPhone,
		}},
		{"a non-numeric NIDA is rejected", map[string]any{
			"full_name": "Halima Said", "nida_number": "1990010112345678901X",
			"next_of_kin_name": testKinName, "next_of_kin_phone": testKinPhone,
		}},
		{"a missing next of kin is rejected", map[string]any{
			"full_name": "Halima Said", "nida_number": validNIDA,
		}},
		{"a bad next-of-kin phone is rejected", map[string]any{
			"full_name": "Halima Said", "nida_number": validNIDA,
			"next_of_kin_name": testKinName, "next_of_kin_phone": "12345",
		}},
		{"an empty name is rejected", map[string]any{
			"full_name": "", "next_of_kin_name": testKinName, "next_of_kin_phone": testKinPhone,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			renter.do(http.MethodPut, "/me/profile", tc.body).
				mustStatus(t, http.StatusBadRequest, tc.name)
		})
	}
}

// TestProfileRequiresRenterSession: the KYC surface is the renter audience
// only — a landlord session must not reach it.
func TestProfileRequiresRenterSession(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.createOrg("Gate Ltd", "Owner", "gate@jjne.test", "0712003010", "supersecret")
	owner.do(http.MethodGet, "/me/profile", nil).
		mustStatus(t, http.StatusUnauthorized, "org session reading /me/profile")
	h.client().do(http.MethodGet, "/me/profile", nil).
		mustStatus(t, http.StatusUnauthorized, "anonymous reading /me/profile")
}

// ---------------------------------------------------------- KYC upload --

func TestKYCUploadValidation(t *testing.T) {
	h := newHarness(t)
	renter := h.registerRenter("+255712003004", "Neema Paul", defaultPIN)

	renter.do(http.MethodPost, "/me/profile/kyc-upload", map[string]any{
		"content_type": "application/pdf", "size_bytes": 1000,
	}).mustStatus(t, http.StatusBadRequest, "pdf content type")

	renter.do(http.MethodPost, "/me/profile/kyc-upload", map[string]any{
		"content_type": "image/png", "size_bytes": 6 << 20,
	}).mustStatus(t, http.StatusBadRequest, "oversize declared")

	if h.store == nil {
		t.Skip("SKIP: MinIO unreachable — the presign half of the upload needs it")
	}
	issued := renter.do(http.MethodPost, "/me/profile/kyc-upload", map[string]any{
		"content_type": "image/jpeg", "size_bytes": 1000,
	}).mustStatus(t, http.StatusOK, "issue upload url")
	key := issued.str(t, "object_key")
	// The key must be namespaced by the renter's own id, so a presigned PUT
	// can never address someone else's document.
	me := renter.do(http.MethodGet, "/me/profile", nil).mustStatus(t, http.StatusOK, "me")
	if !strings.HasPrefix(key, me.str(t, "user", "id")+"/") {
		t.Errorf("object_key %q is not namespaced by the renter's user id", key)
	}
	if !strings.HasSuffix(key, ".jpg") {
		t.Errorf("object_key %q does not carry the jpeg extension", key)
	}
}

// TestKYCUploadCompleteChecksTheObject: a presigned PUT cannot enforce type or
// size, so completion checks what actually landed (SPEC §7).
func TestKYCUploadCompleteChecksTheObject(t *testing.T) {
	h := newHarness(t)
	if h.store == nil {
		t.Skip("SKIP: MinIO unreachable — this test uploads real objects")
	}
	renter := h.registerRenter("+255712003005", "Salma Omary", defaultPIN)
	me := renter.do(http.MethodGet, "/me/profile", nil).mustStatus(t, http.StatusOK, "me")
	userID := me.str(t, "user", "id")

	put := func(t *testing.T, key, contentType string, size int) {
		t.Helper()
		if err := h.store.PutBytes(context.Background(), storage.BucketKYC, key,
			make([]byte, size), contentType); err != nil {
			t.Fatalf("seed object: %v", err)
		}
	}

	t.Run("a wrong content type is rejected", func(t *testing.T) {
		key := userID + "/wrong-type.jpg"
		put(t, key, "application/pdf", 100)
		renter.do(http.MethodPost, "/me/profile/kyc-upload/complete",
			map[string]any{"object_key": key}).
			mustStatus(t, http.StatusBadRequest, "pdf object")
	})

	t.Run("an oversize object is rejected", func(t *testing.T) {
		key := userID + "/too-big.png"
		put(t, key, "image/png", (5<<20)+1)
		renter.do(http.MethodPost, "/me/profile/kyc-upload/complete",
			map[string]any{"object_key": key}).
			mustStatus(t, http.StatusBadRequest, "oversize object")
	})

	t.Run("an object under another renter's prefix is rejected", func(t *testing.T) {
		other := h.registerRenter("+255712003006", "Other Renter", defaultPIN)
		otherMe := other.do(http.MethodGet, "/me/profile", nil).mustStatus(t, http.StatusOK, "me")
		key := otherMe.str(t, "user", "id") + "/theirs.png"
		put(t, key, "image/png", 100)
		renter.do(http.MethodPost, "/me/profile/kyc-upload/complete",
			map[string]any{"object_key": key}).
			mustStatus(t, http.StatusBadRequest, "another renter's key")
	})

	t.Run("a key that was never uploaded is rejected", func(t *testing.T) {
		renter.do(http.MethodPost, "/me/profile/kyc-upload/complete",
			map[string]any{"object_key": userID + "/never-uploaded.png"}).
			mustStatus(t, http.StatusBadRequest, "missing object")
	})

	t.Run("a valid png completes and is then readable", func(t *testing.T) {
		key := userID + "/good.png"
		put(t, key, "image/png", 2048)
		done := renter.do(http.MethodPost, "/me/profile/kyc-upload/complete",
			map[string]any{"object_key": key}).
			mustStatus(t, http.StatusOK, "complete upload")
		if uploaded, _ := done.Body["profile"].(map[string]any)["kyc_doc_uploaded"].(bool); !uploaded {
			t.Error("kyc_doc_uploaded is false after a successful completion")
		}
		doc := renter.do(http.MethodGet, "/me/profile/kyc-doc", nil).
			mustStatus(t, http.StatusOK, "read own kyc doc")
		if url := doc.str(t, "url"); !strings.Contains(url, "X-Amz-Signature") {
			t.Errorf("kyc-doc url is not presigned: %s", url)
		}
		// Every read of an ID document is audited (SPEC §8).
		if len(h.auditPayloads(t, "kyc.view")) == 0 {
			t.Error("reading the KYC document wrote no kyc.view audit row")
		}
	})
}

func TestKYCDocIsNotFoundWhenNoneUploaded(t *testing.T) {
	h := newHarness(t)
	renter := h.registerRenter("+255712003007", "No Doc", defaultPIN)
	renter.completeProfile(t, "No Doc", validNIDA)
	renter.do(http.MethodGet, "/me/profile/kyc-doc", nil).
		mustStatus(t, http.StatusNotFound, "no document uploaded")
}

// ------------------------------------------------------- link requests --

// linkFixture is an org with one priced vacant unit plus a KYC-complete renter.
type linkFixture struct {
	h        *harness
	owner    *client
	renter   *client
	orgID    string
	unitID   string
	unitCode string
	periodID string
	renterID string
}

func (h *harness) newLinkFixture(t *testing.T, tag, ownerPhone, renterPhone string) linkFixture {
	t.Helper()
	fix := h.newOrgWithUnits(tag, tag+"@jjne.test", ownerPhone, []string{"Room 1"}, testUnitAmount)
	renter := h.registerRenter(renterPhone, tag+" Renter", defaultPIN)
	renter.completeProfile(t, tag+" Renter", validNIDA)
	me := renter.do(http.MethodGet, "/me/profile", nil).mustStatus(t, http.StatusOK, "me")
	return linkFixture{
		h: h, owner: fix.client, renter: renter, orgID: fix.orgID,
		unitID: fix.unitIDs[0], unitCode: fix.unitCodes[0],
		periodID: fix.client.periodIDByDays(t, 30), renterID: me.str(t, "user", "id"),
	}
}

// TestLinkRequestHappyPath walks Flow 2: a KYC-complete renter applies from a
// scanned sticker and the landlord sees it in the inbox.
func TestLinkRequestHappyPath(t *testing.T) {
	h := newHarness(t)
	fix := h.newLinkFixture(t, "Happy", "0712003100", "+255712003101")

	created := fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link",
		linkBody(fix.periodID, testTermDays)).
		mustStatus(t, http.StatusCreated, "create link request")

	if got := created.str(t, "request", "status"); got != "pending" {
		t.Errorf("status = %q, want pending (auto-approve is off)", got)
	}
	if got := created.str(t, "request", "unit", "name"); got != "Room 1" {
		t.Errorf("unit.name = %q, want Room 1", got)
	}
	if got := created.str(t, "request", "end_date"); got == "" {
		t.Error("end_date is empty")
	}
	// The preview uses the same generator Phase 4 writes schedules with:
	// 180 days at a 30-day cadence is six rows of one month's rent.
	if got := num(t, created, "request", "schedule_preview", "count"); got != 6 {
		t.Errorf("schedule_preview.count = %v, want 6", got)
	}
	if got := num(t, created, "request", "schedule_preview", "total"); got != 6*testUnitAmount {
		t.Errorf("schedule_preview.total = %v, want %d", got, 6*testUnitAmount)
	}
	if got := num(t, created, "request", "payment_period", "amount"); got != testUnitAmount {
		t.Errorf("payment_period.amount = %v, want %d", got, testUnitAmount)
	}

	// The renter sees their own request.
	mine := fix.renter.do(http.MethodGet, "/me/link-requests", nil).
		mustStatus(t, http.StatusOK, "list my requests")
	if got := len(listOf(t, mine)); got != 1 {
		t.Fatalf("the renter sees %d requests, want 1", got)
	}

	// The landlord sees it, with the renter's KYC status attached.
	inbox := fix.owner.do(http.MethodGet, "/link-requests?status=pending", nil).
		mustStatus(t, http.StatusOK, "landlord inbox")
	items := listOf(t, inbox)
	if len(items) != 1 {
		t.Fatalf("the inbox holds %d requests, want 1", len(items))
	}
	renterBlock, _ := items[0]["renter"].(map[string]any)
	if renterBlock == nil || renterBlock["kyc_status"] != "submitted" {
		t.Errorf("inbox row carries renter %v, want kyc_status submitted", renterBlock)
	}

	// The detail view carries the masked profile and no NIDA in the clear.
	requestID, _ := items[0]["id"].(string)
	detail := fix.owner.do(http.MethodGet, "/link-requests/"+requestID, nil).
		mustStatus(t, http.StatusOK, "request detail")
	if got := detail.str(t, "renter_profile", "nida_masked"); !strings.HasPrefix(got, "••••") {
		t.Errorf("renter_profile.nida_masked = %q, want a masked value", got)
	}
	if strings.Contains(detail.Raw, validNIDA) {
		t.Error("the request detail leaks the full NIDA number")
	}
}

// TestLinkRequestRules covers every refusal in the API.md contract.
func TestLinkRequestRules(t *testing.T) {
	t.Run("an occupied unit is a 409 unit_occupied", func(t *testing.T) {
		h := newHarness(t)
		fix := h.newLinkFixture(t, "Occupied", "0712003110", "+255712003111")
		h.occupy(fix.unitID)

		res := fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link",
			linkBody(fix.periodID, testTermDays))
		res.mustStatus(t, http.StatusConflict, "occupied unit")
		if got, _ := res.Body["type"].(string); got != "unit_occupied" {
			t.Errorf("problem type = %q, want unit_occupied", got)
		}
	})

	t.Run("an incomplete profile is a 412 kyc_required", func(t *testing.T) {
		h := newHarness(t)
		fix := h.newOrgWithUnits("NoKYC", "nokyc@jjne.test", "0712003120", []string{"Room 1"}, testUnitAmount)
		renter := h.registerRenter("+255712003121", "Unverified", defaultPIN)

		res := renter.do(http.MethodPost, "/units/"+fix.unitCodes[0]+"/link",
			linkBody(fix.client.periodIDByDays(t, 30), testTermDays))
		res.mustStatus(t, http.StatusPreconditionFailed, "no KYC")
		if got, _ := res.Body["type"].(string); got != "kyc_required" {
			t.Errorf("problem type = %q, want kyc_required", got)
		}
	})

	t.Run("a period the unit does not offer is a 409", func(t *testing.T) {
		h := newHarness(t)
		fix := h.newLinkFixture(t, "Restricted", "0712003130", "+255712003131")
		quarterly := fix.owner.periodIDByDays(t, 90)
		// Restrict the unit to the monthly period only.
		fix.owner.do(http.MethodPatch, "/units/"+fix.unitID, map[string]any{
			"allowed_period_ids": []string{fix.periodID},
		}).mustStatus(t, http.StatusOK, "restrict periods")

		res := fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link",
			linkBody(quarterly, testTermDays))
		res.mustStatus(t, http.StatusConflict, "period not offered")
		if got, _ := res.Body["type"].(string); got != "period_not_offered" {
			t.Errorf("problem type = %q, want period_not_offered", got)
		}
		// The allowed one still works.
		fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link",
			linkBody(fix.periodID, testTermDays)).
			mustStatus(t, http.StatusCreated, "allowed period")
	})

	t.Run("a period from another org is a 409", func(t *testing.T) {
		h := newHarness(t)
		fix := h.newLinkFixture(t, "OrgA", "0712003140", "+255712003141")
		other := h.newOrgWithUnits("OrgB", "orgb2@jjne.test", "0712003142", nil, 0)
		foreign := other.client.periodIDByDays(t, 30)

		fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link",
			linkBody(foreign, testTermDays)).
			mustStatus(t, http.StatusConflict, "another org's period")
	})

	t.Run("a second pending request for the same unit is a 409", func(t *testing.T) {
		h := newHarness(t)
		fix := h.newLinkFixture(t, "Dup", "0712003150", "+255712003151")
		fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link",
			linkBody(fix.periodID, testTermDays)).
			mustStatus(t, http.StatusCreated, "first request")

		res := fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link",
			linkBody(fix.periodID, testTermDays))
		res.mustStatus(t, http.StatusConflict, "duplicate request")
		if got, _ := res.Body["type"].(string); got != "duplicate_request" {
			t.Errorf("problem type = %q, want duplicate_request", got)
		}
	})

	t.Run("an unlisted unit is a 404", func(t *testing.T) {
		h := newHarness(t)
		fix := h.newLinkFixture(t, "Unlisted", "0712003160", "+255712003161")
		fix.owner.do(http.MethodPatch, "/units/"+fix.unitID,
			map[string]any{"status": "unlisted"}).
			mustStatus(t, http.StatusOK, "unlist")

		fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link",
			linkBody(fix.periodID, testTermDays)).
			mustStatus(t, http.StatusNotFound, "unlisted unit")
	})

	t.Run("an unknown unit code is a 404", func(t *testing.T) {
		h := newHarness(t)
		fix := h.newLinkFixture(t, "Unknown", "0712003170", "+255712003171")
		fix.renter.do(http.MethodPost, "/units/ZZZZZZZZZZ/link",
			linkBody(fix.periodID, testTermDays)).
			mustStatus(t, http.StatusNotFound, "unknown code")
	})

	t.Run("field validation", func(t *testing.T) {
		h := newHarness(t)
		fix := h.newLinkFixture(t, "Validate", "0712003180", "+255712003181")
		old := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")

		cases := []struct {
			name string
			body map[string]any
		}{
			{"a start date more than 7 days back", map[string]any{
				"payment_period_id": fix.periodID, "term_days": testTermDays,
				"start_date": old, "accepted_terms": true,
			}},
			{"unaccepted terms", map[string]any{
				"payment_period_id": fix.periodID, "term_days": testTermDays,
				"start_date": time.Now().UTC().Format("2006-01-02"), "accepted_terms": false,
			}},
			{"a zero term", map[string]any{
				"payment_period_id": fix.periodID, "term_days": 0,
				"start_date": time.Now().UTC().Format("2006-01-02"), "accepted_terms": true,
			}},
			{"a malformed period id", map[string]any{
				"payment_period_id": "not-a-uuid", "term_days": testTermDays,
				"start_date": time.Now().UTC().Format("2006-01-02"), "accepted_terms": true,
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link", tc.body).
					mustStatus(t, http.StatusBadRequest, tc.name)
			})
		}

		// Seven days back is the boundary and must be accepted.
		fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link", map[string]any{
			"payment_period_id": fix.periodID, "term_days": testTermDays,
			"start_date":     time.Now().UTC().AddDate(0, 0, -7).Format("2006-01-02"),
			"accepted_terms": true,
		}).mustStatus(t, http.StatusCreated, "seven days back is allowed")
	})
}

// TestLinkRequestAutoApprove: with the org setting on, the request is approved
// as it arrives and the renter is told immediately.
func TestLinkRequestAutoApprove(t *testing.T) {
	h := newHarness(t)
	fix := h.newLinkFixture(t, "Auto", "0712003200", "+255712003201")
	fix.owner.setAutoApprove(t, true)

	created := fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link",
		linkBody(fix.periodID, testTermDays)).
		mustStatus(t, http.StatusCreated, "auto-approved request")
	if got := created.str(t, "request", "status"); got != "approved" {
		t.Fatalf("status = %q, want approved", got)
	}

	notes := h.notifications(t)
	if len(notes) != 1 {
		t.Fatalf("notification_log holds %d rows, want 1: %+v", len(notes), notes)
	}
	if notes[0].Kind != "link_approved" {
		t.Errorf("kind = %q, want link_approved", notes[0].Kind)
	}
	if notes[0].ToPhone != "+255712003201" {
		t.Errorf("to_phone = %q, want the renter's number", notes[0].ToPhone)
	}

	// Approval is a decision, not an activation: the unit stays vacant until a
	// contract activates in Phase 4.
	unit := fix.owner.do(http.MethodGet, "/units/"+fix.unitID, nil).
		mustStatus(t, http.StatusOK, "unit after auto-approval")
	if got := unit.str(t, "unit", "status"); got != "vacant" {
		t.Errorf("unit status = %q, want vacant (contracts arrive in Phase 4)", got)
	}
}

// TestLinkDecisionStateMachine covers approve/reject and their notifications.
func TestLinkDecisionStateMachine(t *testing.T) {
	t.Run("approve", func(t *testing.T) {
		h := newHarness(t)
		fix := h.newLinkFixture(t, "Approve", "0712003210", "+255712003211")
		created := fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link",
			linkBody(fix.periodID, testTermDays)).
			mustStatus(t, http.StatusCreated, "create")
		id := created.str(t, "request", "id")

		approved := fix.owner.do(http.MethodPost, "/link-requests/"+id+"/approve", nil).
			mustStatus(t, http.StatusOK, "approve")
		if got := approved.str(t, "request", "status"); got != "approved" {
			t.Errorf("status = %q, want approved", got)
		}
		if approved.Body["request"].(map[string]any)["decided_at"] == nil {
			t.Error("decided_at is null after approval")
		}

		notes := h.notifications(t)
		if len(notes) != 1 || notes[0].Kind != "link_approved" {
			t.Fatalf("notifications = %+v, want one link_approved", notes)
		}
		if want := "link_approved:" + id; notes[0].DedupeKey != want {
			t.Errorf("dedupe_key = %q, want %q", notes[0].DedupeKey, want)
		}
		if notes[0].Status != "queued" {
			t.Errorf("status = %q, want queued (the worker sends it)", notes[0].Status)
		}
		if !strings.Contains(notes[0].Body, "Room 1") {
			t.Errorf("body %q does not name the unit", notes[0].Body)
		}

		// A decided request cannot be decided again.
		fix.owner.do(http.MethodPost, "/link-requests/"+id+"/approve", nil).
			mustStatus(t, http.StatusConflict, "re-approve")
		fix.owner.do(http.MethodPost, "/link-requests/"+id+"/reject",
			map[string]any{"reason": "changed my mind"}).
			mustStatus(t, http.StatusConflict, "reject after approve")
		// And the renter can no longer cancel it.
		fix.renter.do(http.MethodDelete, "/me/link-requests/"+id, nil).
			mustStatus(t, http.StatusConflict, "cancel after approve")

		// Deciding twice must not produce a second SMS.
		if got := len(h.notifications(t)); got != 1 {
			t.Errorf("notification_log holds %d rows after repeated decisions, want 1", got)
		}
	})

	t.Run("reject", func(t *testing.T) {
		h := newHarness(t)
		fix := h.newLinkFixture(t, "Reject", "0712003220", "+255712003221")
		created := fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link",
			linkBody(fix.periodID, testTermDays)).
			mustStatus(t, http.StatusCreated, "create")
		id := created.str(t, "request", "id")

		// A reason is mandatory — the renter is told why.
		fix.owner.do(http.MethodPost, "/link-requests/"+id+"/reject", map[string]any{"reason": ""}).
			mustStatus(t, http.StatusBadRequest, "empty reason")

		rejected := fix.owner.do(http.MethodPost, "/link-requests/"+id+"/reject",
			map[string]any{"reason": "unit promised to another applicant"}).
			mustStatus(t, http.StatusOK, "reject")
		if got := rejected.str(t, "request", "status"); got != "rejected" {
			t.Errorf("status = %q, want rejected", got)
		}
		if got := rejected.str(t, "request", "rejection_reason"); got != "unit promised to another applicant" {
			t.Errorf("rejection_reason = %q", got)
		}

		notes := h.notifications(t)
		if len(notes) != 1 || notes[0].Kind != "link_rejected" {
			t.Fatalf("notifications = %+v, want one link_rejected", notes)
		}
		if !strings.Contains(notes[0].Body, "unit promised to another applicant") {
			t.Errorf("body %q does not carry the reason", notes[0].Body)
		}

		// The renter sees the reason on their own list.
		mine := fix.renter.do(http.MethodGet, "/me/link-requests", nil).
			mustStatus(t, http.StatusOK, "my requests")
		if got := listOf(t, mine)[0]["rejection_reason"]; got != "unit promised to another applicant" {
			t.Errorf("the renter's list shows rejection_reason %v", got)
		}

		// Rejection frees the renter to apply again.
		fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link",
			linkBody(fix.periodID, testTermDays)).
			mustStatus(t, http.StatusCreated, "re-apply after rejection")
	})

	t.Run("cancel", func(t *testing.T) {
		h := newHarness(t)
		fix := h.newLinkFixture(t, "Cancel", "0712003230", "+255712003231")
		created := fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link",
			linkBody(fix.periodID, testTermDays)).
			mustStatus(t, http.StatusCreated, "create")
		id := created.str(t, "request", "id")

		fix.renter.do(http.MethodDelete, "/me/link-requests/"+id, nil).
			mustStatus(t, http.StatusNoContent, "cancel")
		fix.renter.do(http.MethodDelete, "/me/link-requests/"+id, nil).
			mustStatus(t, http.StatusConflict, "cancel twice")

		// A cancelled request cannot be approved by the landlord.
		fix.owner.do(http.MethodPost, "/link-requests/"+id+"/approve", nil).
			mustStatus(t, http.StatusConflict, "approve a cancelled request")
		// Cancelling sends nothing: the renter did it themselves.
		if got := len(h.notifications(t)); got != 0 {
			t.Errorf("cancelling queued %d notifications, want 0", got)
		}
		// And it frees the slot for a fresh application.
		fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link",
			linkBody(fix.periodID, testTermDays)).
			mustStatus(t, http.StatusCreated, "re-apply after cancelling")
	})
}

// TestLinkNotificationLanguage: the body follows the org's sms_language.
func TestLinkNotificationLanguage(t *testing.T) {
	h := newHarness(t)
	fix := h.newLinkFixture(t, "Lang", "0712003240", "+255712003241")
	fix.owner.do(http.MethodPatch, "/org", map[string]any{
		"settings": map[string]any{"sms_language": "en"},
	}).mustStatus(t, http.StatusOK, "english")

	created := fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link",
		linkBody(fix.periodID, testTermDays)).
		mustStatus(t, http.StatusCreated, "create")
	fix.owner.do(http.MethodPost, "/link-requests/"+created.str(t, "request", "id")+"/approve", nil).
		mustStatus(t, http.StatusOK, "approve")

	notes := h.notifications(t)
	if len(notes) != 1 {
		t.Fatalf("notifications = %+v", notes)
	}
	if !strings.Contains(notes[0].Body, "was approved") {
		t.Errorf("english body = %q, want the English template", notes[0].Body)
	}
}

// ---------------------------------------------------- renter directory --

func TestRenterDirectory(t *testing.T) {
	h := newHarness(t)
	fix := h.newLinkFixture(t, "Directory", "0712003300", "+255712003301")

	// A renter with no relationship to the org is invisible.
	stranger := h.registerRenter("+255712003302", "Stranger", defaultPIN)
	strangerMe := stranger.do(http.MethodGet, "/me/profile", nil).
		mustStatus(t, http.StatusOK, "stranger me")
	strangerID := strangerMe.str(t, "user", "id")

	empty := fix.owner.do(http.MethodGet, "/renters", nil).
		mustStatus(t, http.StatusOK, "empty directory")
	if got := len(listOf(t, empty)); got != 0 {
		t.Fatalf("the directory lists %d renters before any request, want 0", got)
	}
	fix.owner.do(http.MethodGet, "/renters/"+strangerID, nil).
		mustStatus(t, http.StatusNotFound, "unrelated renter")

	// Applying is what makes a renter known to the org.
	fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link",
		linkBody(fix.periodID, testTermDays)).
		mustStatus(t, http.StatusCreated, "apply")

	listed := fix.owner.do(http.MethodGet, "/renters", nil).
		mustStatus(t, http.StatusOK, "directory")
	items := listOf(t, listed)
	if len(items) != 1 {
		t.Fatalf("the directory lists %d renters, want 1", len(items))
	}
	if items[0]["user_id"] != fix.renterID {
		t.Errorf("directory lists %v, want the applicant %s", items[0]["user_id"], fix.renterID)
	}
	units, _ := items[0]["units"].([]any)
	if len(units) != 1 {
		t.Errorf("the directory row lists %d units, want 1", len(units))
	}

	t.Run("filters", func(t *testing.T) {
		byName := fix.owner.do(http.MethodGet, "/renters?q=Directory", nil).
			mustStatus(t, http.StatusOK, "search by name")
		if got := len(listOf(t, byName)); got != 1 {
			t.Errorf("q=Directory matched %d renters, want 1", got)
		}
		byPhone := fix.owner.do(http.MethodGet, "/renters?q=712003301", nil).
			mustStatus(t, http.StatusOK, "search by phone")
		if got := len(listOf(t, byPhone)); got != 1 {
			t.Errorf("a phone search matched %d renters, want 1", got)
		}
		miss := fix.owner.do(http.MethodGet, "/renters?q=Nobody", nil).
			mustStatus(t, http.StatusOK, "search miss")
		if got := len(listOf(t, miss)); got != 0 {
			t.Errorf("q=Nobody matched %d renters, want 0", got)
		}
		submitted := fix.owner.do(http.MethodGet, "/renters?kyc_status=submitted", nil).
			mustStatus(t, http.StatusOK, "kyc filter")
		if got := len(listOf(t, submitted)); got != 1 {
			t.Errorf("kyc_status=submitted matched %d renters, want 1", got)
		}
		none := fix.owner.do(http.MethodGet, "/renters?kyc_status=none", nil).
			mustStatus(t, http.StatusOK, "kyc filter none")
		if got := len(listOf(t, none)); got != 0 {
			t.Errorf("kyc_status=none matched %d renters, want 0", got)
		}
		fix.owner.do(http.MethodGet, "/renters?kyc_status=bogus", nil).
			mustStatus(t, http.StatusBadRequest, "bad kyc filter")
	})

	t.Run("detail", func(t *testing.T) {
		detail := fix.owner.do(http.MethodGet, "/renters/"+fix.renterID, nil).
			mustStatus(t, http.StatusOK, "renter detail")
		if got := detail.str(t, "profile", "kyc_status"); got != "submitted" {
			t.Errorf("profile.kyc_status = %q, want submitted", got)
		}
		if got := detail.str(t, "profile", "nida_masked"); !strings.HasPrefix(got, "••••") {
			t.Errorf("profile.nida_masked = %q, want a mask", got)
		}
		if strings.Contains(detail.Raw, validNIDA) {
			t.Error("the renter detail leaks the full NIDA number")
		}
		if got := len(arrayOf(t, detail, "link_requests")); got != 1 {
			t.Errorf("link_requests = %d, want 1", got)
		}
	})
}

// --------------------------------------------------------- isolation --

// TestPhase3OrgIsolation is the SPEC §8 promise on the Phase 3 surface: one
// org can neither see nor act on another's link requests and renters, and the
// answer is always 404 rather than 403.
func TestPhase3OrgIsolation(t *testing.T) {
	h := newHarness(t)
	a := h.newLinkFixture(t, "IsoA", "0712003400", "+255712003401")
	b := h.newLinkFixture(t, "IsoB", "0712003410", "+255712003411")

	created := a.renter.do(http.MethodPost, "/units/"+a.unitCode+"/link",
		linkBody(a.periodID, testTermDays)).
		mustStatus(t, http.StatusCreated, "org A request")
	requestID := created.str(t, "request", "id")

	t.Run("org B cannot see org A's request", func(t *testing.T) {
		inbox := b.owner.do(http.MethodGet, "/link-requests", nil).
			mustStatus(t, http.StatusOK, "org B inbox")
		if got := len(listOf(t, inbox)); got != 0 {
			t.Errorf("org B's inbox holds %d of org A's requests, want 0", got)
		}
		b.owner.do(http.MethodGet, "/link-requests/"+requestID, nil).
			mustStatus(t, http.StatusNotFound, "org B reading org A's request")
	})

	t.Run("org B cannot decide org A's request", func(t *testing.T) {
		b.owner.do(http.MethodPost, "/link-requests/"+requestID+"/approve", nil).
			mustStatus(t, http.StatusNotFound, "org B approving")
		b.owner.do(http.MethodPost, "/link-requests/"+requestID+"/reject",
			map[string]any{"reason": "not mine"}).
			mustStatus(t, http.StatusNotFound, "org B rejecting")

		// And the request is untouched.
		still := a.owner.do(http.MethodGet, "/link-requests/"+requestID, nil).
			mustStatus(t, http.StatusOK, "org A reading its own request")
		if got := still.str(t, "request", "status"); got != "pending" {
			t.Errorf("status = %q after org B's attempts, want pending", got)
		}
	})

	t.Run("org B cannot see org A's renter", func(t *testing.T) {
		b.owner.do(http.MethodGet, "/renters/"+a.renterID, nil).
			mustStatus(t, http.StatusNotFound, "org B reading org A's renter")
		b.owner.do(http.MethodGet, "/renters/"+a.renterID+"/kyc-doc", nil).
			mustStatus(t, http.StatusNotFound, "org B reading org A's renter's ID document")

		listed := b.owner.do(http.MethodGet, "/renters", nil).
			mustStatus(t, http.StatusOK, "org B directory")
		for _, item := range listOf(t, listed) {
			if item["user_id"] == a.renterID {
				t.Error("org B's directory includes org A's renter")
			}
		}
	})
}

// TestRenterCannotReadAnotherRentersRequests: the renter surface is scoped to
// the caller's own user id, not just to a session.
func TestRenterCannotReadAnotherRentersRequests(t *testing.T) {
	h := newHarness(t)
	fix := h.newLinkFixture(t, "Mine", "0712003500", "+255712003501")
	created := fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link",
		linkBody(fix.periodID, testTermDays)).
		mustStatus(t, http.StatusCreated, "first renter's request")
	requestID := created.str(t, "request", "id")

	intruder := h.registerRenter("+255712003502", "Intruder", defaultPIN)
	intruder.completeProfile(t, "Intruder", otherNIDA)

	mine := intruder.do(http.MethodGet, "/me/link-requests", nil).
		mustStatus(t, http.StatusOK, "intruder's own list")
	if got := len(listOf(t, mine)); got != 0 {
		t.Errorf("the intruder sees %d of someone else's requests, want 0", got)
	}
	intruder.do(http.MethodDelete, "/me/link-requests/"+requestID, nil).
		mustStatus(t, http.StatusNotFound, "intruder cancelling another renter's request")

	// The owner of the request still has it, untouched.
	still := fix.renter.do(http.MethodGet, "/me/link-requests", nil).
		mustStatus(t, http.StatusOK, "original renter's list")
	if got := listOf(t, still)[0]["status"]; got != "pending" {
		t.Errorf("status = %v after the intruder's attempt, want pending", got)
	}
}

// TestLandlordCannotReachRenterRoutes / vice versa: the audiences are separate
// cookies, and each surface admits only its own.
func TestPhase3AudienceSeparation(t *testing.T) {
	h := newHarness(t)
	fix := h.newLinkFixture(t, "Audience", "0712003600", "+255712003601")

	fix.owner.do(http.MethodGet, "/me/link-requests", nil).
		mustStatus(t, http.StatusUnauthorized, "org session on the renter list")
	fix.owner.do(http.MethodPost, "/units/"+fix.unitCode+"/link",
		linkBody(fix.periodID, testTermDays)).
		mustStatus(t, http.StatusUnauthorized, "org session applying for a unit")

	fix.renter.do(http.MethodGet, "/link-requests", nil).
		mustStatus(t, http.StatusUnauthorized, "renter session on the landlord inbox")
	fix.renter.do(http.MethodGet, "/renters", nil).
		mustStatus(t, http.StatusUnauthorized, "renter session on the directory")
	fix.renter.do(http.MethodGet, "/renters/"+fix.renterID+"/kyc-doc", nil).
		mustStatus(t, http.StatusUnauthorized, "renter session on the landlord KYC route")
}

// TestLinkRequestPagination checks the cursor on the landlord inbox.
func TestLinkRequestPagination(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Paging", "paging@jjne.test", "0712003700",
		[]string{"Room 1", "Room 2", "Room 3"}, testUnitAmount)
	periodID := fix.client.periodIDByDays(t, 30)

	renter := h.registerRenter("+255712003701", "Serial Applicant", defaultPIN)
	renter.completeProfile(t, "Serial Applicant", validNIDA)
	for _, code := range fix.unitCodes {
		renter.do(http.MethodPost, "/units/"+code+"/link", linkBody(periodID, testTermDays)).
			mustStatus(t, http.StatusCreated, "apply to "+code)
	}

	first := fix.client.do(http.MethodGet, "/link-requests?limit=2", nil).
		mustStatus(t, http.StatusOK, "first page")
	if got := len(listOf(t, first)); got != 2 {
		t.Fatalf("first page holds %d rows, want 2", got)
	}
	cursor, _ := first.Body["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("next_cursor is empty on a full first page")
	}
	second := fix.client.do(http.MethodGet, "/link-requests?limit=2&cursor="+cursor, nil).
		mustStatus(t, http.StatusOK, "second page")
	if got := len(listOf(t, second)); got != 1 {
		t.Fatalf("second page holds %d rows, want 1", got)
	}
	if second.Body["next_cursor"] != nil {
		t.Errorf("next_cursor = %v on the last page, want null", second.Body["next_cursor"])
	}

	fix.client.do(http.MethodGet, "/link-requests?limit=0", nil).
		mustStatus(t, http.StatusBadRequest, "limit 0")
	fix.client.do(http.MethodGet, "/link-requests?cursor=not-a-cursor", nil).
		mustStatus(t, http.StatusBadRequest, "malformed cursor")
	fix.client.do(http.MethodGet, "/link-requests?status=bogus", nil).
		mustStatus(t, http.StatusBadRequest, "bad status filter")
}

// TestSchedulePreviewMatchesTheContractRules ties the API's preview to the
// SPEC §4 worked example: a 100-day term at a 30-day cadence is 30/30/30/10.
func TestSchedulePreviewMatchesTheContractRules(t *testing.T) {
	h := newHarness(t)
	fix := h.newLinkFixture(t, "Preview", "0712003800", "+255712003801")

	created := fix.renter.do(http.MethodPost, "/units/"+fix.unitCode+"/link",
		linkBody(fix.periodID, 100)).
		mustStatus(t, http.StatusCreated, "100-day term")

	if got := num(t, created, "request", "schedule_preview", "count"); got != 4 {
		t.Errorf("count = %v, want 4 (30/30/30/10)", got)
	}
	if got := num(t, created, "request", "schedule_preview", "amount_first"); got != testUnitAmount {
		t.Errorf("amount_first = %v, want a full month at %d", got, testUnitAmount)
	}
	// The truncated last row is 10 days of a 30-day price.
	wantLast := float64(testUnitAmount) * 10 / 30
	if got := num(t, created, "request", "schedule_preview", "amount_last"); got != float64(int64(wantLast+0.5)) {
		t.Errorf("amount_last = %v, want %v (10 days prorated)", got, wantLast)
	}
	if got := num(t, created, "request", "schedule_preview", "total"); got != 3*testUnitAmount+float64(int64(wantLast+0.5)) {
		t.Errorf("total = %v", got)
	}
	if got := created.str(t, "request", "schedule_preview", "first_due"); got != time.Now().UTC().Format("2006-01-02") {
		t.Errorf("first_due = %q, want today (no due_day set)", got)
	}
}
