package httpserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"tms/backend/internal/notify"
)

// Phase 14: prepaid SMS credits per org, and the platform SMS catalogue an
// admin edits (PLAN2 Phase 14, API.md "Phase 14").

// ------------------------------------------------------------- fixtures --

// creditFixture is one org with two active tenancies — so a broadcast to
// `all_active` has two recipients — and a balance the test sets.
type creditFixture struct {
	owner *client
	orgID string
	admin *client
}

func (h *harness) newCreditFixture(t *testing.T, tag, ownerPhone, phoneA, phoneB string) creditFixture {
	t.Helper()
	bi := h.newBilingualOrg(t, tag, ownerPhone, phoneA, phoneB)
	orgID := bi.owner.do(http.MethodGet, "/org", nil).
		mustStatus(t, http.StatusOK, "read own org").str(t, "id")
	return creditFixture{owner: bi.owner, orgID: orgID, admin: h.adminClient(t)}
}

// setBalance puts an exact number of credits on the org, without a ledger row:
// it is a fixture, not a movement anybody made.
func (h *harness) setBalance(t *testing.T, orgID string, balance int) {
	t.Helper()
	h.grantCredits(orgID, balance)
}

func (h *harness) balance(t *testing.T, orgID string) int {
	t.Helper()
	var n int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT balance FROM org_sms_credits WHERE org_id = $1`, orgID).Scan(&n); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return n
}

// --------------------------------------------------- the bulk pre-check --

// TestBulkSendRefusedWithoutCredit is the exit criterion PLAN2 names: a
// landlord is told about the shortfall *before* the rows are queued, with the
// exact number of credits the send needs.
func TestBulkSendRefusedWithoutCredit(t *testing.T) {
	h := newHarness(t)
	fix := h.newCreditFixture(t, "Shortfall", "0712140001", "+255714140002", "+255714140003")
	h.setBalance(t, fix.orgID, 0)

	resp := fix.owner.do(http.MethodPost, "/notifications/custom", map[string]any{
		"recipients": "all_active",
		"body_sw":    "Maji yatakatika kesho.",
		"body_en":    "Water will be off tomorrow.",
	}).mustStatus(t, http.StatusConflict, "bulk send with no credit")

	if got := resp.str(t, "type"); got != "insufficient_sms_credits" {
		t.Fatalf("problem type = %q, want insufficient_sms_credits", got)
	}
	// Two recipients, one segment each: the shortfall is quoted in the same
	// unit the worker will actually debit.
	if got := num(t, resp, "needed"); got != 2 {
		t.Errorf("needed = %v, want 2 (one segment per recipient)", got)
	}
	if got := num(t, resp, "balance"); got != 0 {
		t.Errorf("balance = %v, want 0", got)
	}

	// Nothing was queued: the point of a pre-check is that the landlord is not
	// left with forty held rows to explain.
	log := fix.owner.do(http.MethodGet, "/notifications/log?kind=custom", nil).
		mustStatus(t, http.StatusOK, "notification log")
	if items := arrayOf(t, log, "items"); len(items) != 0 {
		t.Errorf("%d custom messages were queued despite the refusal, want 0", len(items))
	}
}

// TestBulkSendPassesWithEnoughCredit: the same send goes through once the
// balance covers it, and the pre-check is exact rather than conservative — a
// balance of precisely the needed amount is enough.
func TestBulkSendPassesWithEnoughCredit(t *testing.T) {
	h := newHarness(t)
	fix := h.newCreditFixture(t, "Exact", "0712140011", "+255714140012", "+255714140013")
	h.setBalance(t, fix.orgID, 2)

	fix.owner.do(http.MethodPost, "/notifications/custom", map[string]any{
		"recipients": "all_active",
		"body_sw":    "Maji yatakatika kesho.",
		"body_en":    "Water will be off tomorrow.",
	}).mustStatus(t, http.StatusAccepted, "bulk send with exactly enough credit")
}

// TestBulkPreCheckCountsSegmentsNotMessages: a long notice costs more than a
// short one, and the landlord is quoted the real figure.
func TestBulkPreCheckCountsSegmentsNotMessages(t *testing.T) {
	h := newHarness(t)
	fix := h.newCreditFixture(t, "Long", "0712140021", "+255714140022", "+255714140023")
	h.setBalance(t, fix.orgID, 0)

	long := strings.Repeat("a", 200) // two GSM segments
	resp := fix.owner.do(http.MethodPost, "/notifications/custom", map[string]any{
		"recipients": "all_active", "body_sw": long, "body_en": long,
	}).mustStatus(t, http.StatusConflict, "long bulk send with no credit")
	if got := num(t, resp, "needed"); got != 4 {
		t.Errorf("needed = %v, want 4 (two recipients × two segments)", got)
	}
}

// ------------------------------------------------------ admin: the credits --

// TestAdminTopupReleasesHeldMessages walks the whole credit story: an org at
// zero holds its messages, a top-up records a ledger row and puts the backlog
// back on the queue, and the landlord's own view reports the new balance.
func TestAdminTopupReleasesHeldMessages(t *testing.T) {
	h := newHarness(t)
	fix := h.newCreditFixture(t, "Topup", "0712140031", "+255714140032", "+255714140033")
	h.setBalance(t, fix.orgID, 5)

	// Queue two messages, then hold them the way the worker would.
	fix.owner.do(http.MethodPost, "/notifications/custom", map[string]any{
		"recipients": "all_active", "body_sw": "Habari", "body_en": "Hello",
	}).mustStatus(t, http.StatusAccepted, "bulk send")
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE notification_log SET status = 'held_no_credit'
		 WHERE org_id = $1 AND kind = 'custom'`, fix.orgID); err != nil {
		t.Fatalf("hold messages: %v", err)
	}
	h.setBalance(t, fix.orgID, 0)

	before := fix.admin.do(http.MethodGet, "/admin/orgs/"+fix.orgID+"/sms", nil).
		mustStatus(t, http.StatusOK, "admin sms view")
	if got := num(t, before, "balance"); got != 0 {
		t.Errorf("balance = %v, want 0", got)
	}
	if got := num(t, before, "held_count"); got != 2 {
		t.Errorf("held_count = %v, want 2", got)
	}

	top := fix.admin.do(http.MethodPost, "/admin/orgs/"+fix.orgID+"/sms/topup",
		map[string]any{"credits": 100, "note": "September prepayment"}).
		mustStatus(t, http.StatusOK, "topup")
	if got := num(t, top, "balance"); got != 100 {
		t.Errorf("balance after topup = %v, want 100", got)
	}
	if got := num(t, top, "released"); got != 2 {
		t.Errorf("released = %v, want the 2 held messages", got)
	}

	after := fix.admin.do(http.MethodGet, "/admin/orgs/"+fix.orgID+"/sms", nil).
		mustStatus(t, http.StatusOK, "admin sms view after topup")
	if got := num(t, after, "held_count"); got != 0 {
		t.Errorf("held_count after topup = %v, want 0", got)
	}
	ledger := arrayOf(t, after, "ledger")
	if len(ledger) != 1 {
		t.Fatalf("%d ledger rows, want 1", len(ledger))
	}
	row := ledger[0]
	if row["reason"] != notify.ReasonTopup {
		t.Errorf("ledger reason = %v, want topup", row["reason"])
	}
	if row["note"] != "September prepayment" {
		t.Errorf("ledger note = %v, want the admin's note", row["note"])
	}
	if row["balance_after"] != float64(100) {
		t.Errorf("ledger balance_after = %v, want 100", row["balance_after"])
	}

	// The landlord sees the same number, and the platform's movement is in
	// their own audit trail — "credits added by platform".
	own := fix.owner.do(http.MethodGet, "/org/sms-credits", nil).
		mustStatus(t, http.StatusOK, "landlord credit view")
	if got := num(t, own, "balance"); got != 100 {
		t.Errorf("landlord balance = %v, want 100", got)
	}
	if own.Body["low"] != false {
		t.Errorf("low = %v, want false at 100 credits over a 50 watermark", own.Body["low"])
	}
	if len(h.auditPayloads(t, "sms_credits.topup")) == 0 {
		t.Error("the top-up wrote no audit row")
	}
}

// TestAdminAdjustRefusesToGoNegative: a prepaid balance has no overdraft.
func TestAdminAdjustRefusesToGoNegative(t *testing.T) {
	h := newHarness(t)
	fix := h.newCreditFixture(t, "Adjust", "0712140041", "+255714140042", "+255714140043")
	h.setBalance(t, fix.orgID, 10)

	fix.admin.do(http.MethodPost, "/admin/orgs/"+fix.orgID+"/sms/adjust",
		map[string]any{"delta": -11, "note": "clawback"}).
		mustStatus(t, http.StatusBadRequest, "adjust below zero")
	if got := h.balance(t, fix.orgID); got != 10 {
		t.Errorf("balance = %d, want 10 — a refused adjustment moves nothing", got)
	}

	// Zero is not a movement.
	fix.admin.do(http.MethodPost, "/admin/orgs/"+fix.orgID+"/sms/adjust",
		map[string]any{"delta": 0}).mustStatus(t, http.StatusBadRequest, "zero adjust")

	res := fix.admin.do(http.MethodPost, "/admin/orgs/"+fix.orgID+"/sms/adjust",
		map[string]any{"delta": -10, "note": "correcting a double top-up"}).
		mustStatus(t, http.StatusOK, "adjust to zero")
	if got := num(t, res, "balance"); got != 0 {
		t.Errorf("balance = %v, want 0", got)
	}
}

// TestAdminWatermarkAndLowFlag: the warning level is the admin's to set, and
// the landlord's banner condition is computed once, server-side.
func TestAdminWatermarkAndLowFlag(t *testing.T) {
	h := newHarness(t)
	fix := h.newCreditFixture(t, "Watermark", "0712140051", "+255714140052", "+255714140053")
	h.setBalance(t, fix.orgID, 100)

	fix.admin.do(http.MethodPatch, "/admin/orgs/"+fix.orgID+"/sms",
		map[string]any{"low_watermark": 200}).
		mustStatus(t, http.StatusOK, "raise the watermark")

	own := fix.owner.do(http.MethodGet, "/org/sms-credits", nil).
		mustStatus(t, http.StatusOK, "landlord credit view")
	if own.Body["low"] != true {
		t.Errorf("low = %v, want true at 100 credits under a 200 watermark", own.Body["low"])
	}
	if got := num(t, own, "low_watermark"); got != 200 {
		t.Errorf("low_watermark = %v, want 200", got)
	}

	fix.admin.do(http.MethodPatch, "/admin/orgs/"+fix.orgID+"/sms",
		map[string]any{"low_watermark": -1}).
		mustStatus(t, http.StatusBadRequest, "negative watermark")
}

// TestAdminCreditsOnAnUnknownOrgAre404: an org id nobody uses must not create
// a credit row, and must not be distinguishable from one that exists.
func TestAdminCreditsOnAnUnknownOrgAre404(t *testing.T) {
	h := newHarness(t)
	admin := h.adminClient(t)
	const ghost = "00000000-0000-0000-0000-0000000000ff"

	admin.do(http.MethodGet, "/admin/orgs/"+ghost+"/sms", nil).
		mustStatus(t, http.StatusNotFound, "sms view of a missing org")
	admin.do(http.MethodPost, "/admin/orgs/"+ghost+"/sms/topup",
		map[string]any{"credits": 10}).
		mustStatus(t, http.StatusNotFound, "topup of a missing org")
}

// TestAdminMetricsCarryTheCreditBlock pins the three fields the admin
// dashboard reads (API.md Phase 14).
func TestAdminMetricsCarryTheCreditBlock(t *testing.T) {
	h := newHarness(t)
	fix := h.newCreditFixture(t, "Metrics", "0712140061", "+255714140062", "+255714140063")
	h.setBalance(t, fix.orgID, 1)
	fix.admin.do(http.MethodPatch, "/admin/orgs/"+fix.orgID+"/sms",
		map[string]any{"low_watermark": 500}).
		mustStatus(t, http.StatusOK, "raise the watermark")

	m := fix.admin.do(http.MethodGet, "/admin/metrics", nil).
		mustStatus(t, http.StatusOK, "admin metrics")
	sms, ok := m.Body["sms"].(map[string]any)
	if !ok {
		t.Fatal("metrics carry no sms block")
	}
	for _, field := range []string{"credits_used_today", "orgs_under_watermark", "held_total"} {
		if _, present := sms[field]; !present {
			t.Errorf("metrics.sms is missing %q", field)
		}
	}
	if n, _ := sms["orgs_under_watermark"].(float64); n < 1 {
		t.Errorf("orgs_under_watermark = %v, want at least the org just put under one", n)
	}
}

// -------------------------------------------------- admin: the templates --

// TestAdminTemplatesListIsSeeded: migration 000016 and the startup seed make
// the table authoritative, so the catalogue is there before anybody edits it.
func TestAdminTemplatesListIsSeeded(t *testing.T) {
	h := newHarness(t)
	admin := h.adminClient(t)

	items := arrayOf(t, admin.do(http.MethodGet, "/admin/templates", nil).
		mustStatus(t, http.StatusOK, "template list"), "items")
	byKind := map[string]map[string]any{}
	for _, row := range items {
		kind, _ := row["kind"].(string)
		byKind[kind] = row
	}
	for _, kind := range notify.SeedKinds() {
		row, ok := byKind[kind]
		if !ok {
			t.Errorf("the catalogue is missing %q", kind)
			continue
		}
		if row["sw"] == "" || row["en"] == "" {
			t.Errorf("%s: a language is blank", kind)
		}
		if vars, _ := row["variables"].([]any); len(vars) == 0 {
			t.Errorf("%s: no variables listed", kind)
		}
	}
	if otp := byKind[notify.KindOTP]; otp == nil || otp["locked"] != true {
		t.Error("otp does not ship locked; a landlord could reword the sign-in code")
	}
}

// TestAdminPutTemplateValidates: both languages are required, an unknown
// placeholder is a 400, and a body past the ceiling is refused.
func TestAdminPutTemplateValidates(t *testing.T) {
	h := newHarness(t)
	admin := h.adminClient(t)
	const path = "/admin/templates/reminder_due"

	admin.do(http.MethodPut, path, map[string]any{"sw": "Habari {{name}}"}).
		mustStatus(t, http.StatusBadRequest, "swahili only")
	admin.do(http.MethodPut, path, map[string]any{"en": "Hello {{name}}"}).
		mustStatus(t, http.StatusBadRequest, "english only")
	admin.do(http.MethodPut, path, map[string]any{
		"sw": "Habari {{nmae}}", "en": "Hello {{name}}",
	}).mustStatus(t, http.StatusBadRequest, "misspelt variable")
	admin.do(http.MethodPut, path, map[string]any{
		"sw": strings.Repeat("a", 481), "en": "Hello",
	}).mustStatus(t, http.StatusBadRequest, "over the ceiling")

	admin.do(http.MethodPut, "/admin/templates/not_a_kind", map[string]any{
		"sw": "a", "en": "b",
	}).mustStatus(t, http.StatusNotFound, "unknown kind")
}

// TestAdminTemplateEditFlowsThroughToRender is the exit criterion: an admin
// edits the Swahili `reminder_due`, and the next reminder uses the new
// wording. It also pins the resolution order — an org's own override still
// wins over the platform's.
func TestAdminTemplateEditFlowsThroughToRender(t *testing.T) {
	h := newHarness(t)
	admin := h.adminClient(t)
	const path = "/admin/templates/reminder_due"

	saved := admin.do(http.MethodPut, path, map[string]any{
		"sw": "Salamu {{name}}, kodi ya {{amount}} inatakiwa leo.",
		"en": "Hi {{name}}, rent of {{amount}} is due today.",
	}).mustStatus(t, http.StatusOK, "save reminder_due")
	tpl, _ := saved.Body["template"].(map[string]any)
	if tpl["version"] != float64(2) {
		t.Errorf("version after the first edit = %v, want 2", tpl["version"])
	}
	if seg, _ := saved.Body["segments"].(map[string]any); seg["sw"] != float64(1) {
		t.Errorf("segments.sw = %v, want 1", seg["sw"])
	}

	// The cache is invalidated on save, so the very next render reads the new
	// wording rather than waiting out the five-minute TTL.
	body := notify.Render(notify.KindReminderDue, notify.LangSwahili,
		notify.Vars{Name: "Asha", Amount: "TZS 250,000"}, nil)
	if !strings.HasPrefix(body, "Salamu Asha") {
		t.Fatalf("render after the edit = %q, want the new platform wording", body)
	}

	// An org override still wins: the platform sets the default, not the
	// ceiling.
	over := notify.Overrides{notify.KindReminderDue: &notify.Template{
		SW: "Ndugu {{name}}, lipa {{amount}}.",
	}}
	if got := notify.Render(notify.KindReminderDue, notify.LangSwahili,
		notify.Vars{Name: "Asha", Amount: "TZS 250,000"}, over); !strings.HasPrefix(got, "Ndugu Asha") {
		t.Errorf("render with an org override = %q, want the org's wording", got)
	}

	// A kind nobody has edited still renders the code default, which is what
	// the table was seeded from.
	if got := notify.Render(notify.KindLinkApproved, notify.LangEnglish,
		notify.Vars{Unit: "A-12", Org: "JJnE"}, nil); !strings.Contains(got, "was approved") {
		t.Errorf("unedited kind rendered %q, want the seeded default", got)
	}
}

// TestAdminTemplateVersionsAndRevert: history keeps the body that was
// replaced, and a revert is a new version rather than a rewound counter.
func TestAdminTemplateVersionsAndRevert(t *testing.T) {
	h := newHarness(t)
	admin := h.adminClient(t)
	const path = "/admin/templates/overdue_daily"

	original := admin.do(http.MethodGet, "/admin/templates", nil).
		mustStatus(t, http.StatusOK, "template list")
	var originalSW string
	for _, row := range arrayOf(t, original, "items") {
		if row["kind"] == "overdue_daily" {
			originalSW, _ = row["sw"].(string)
		}
	}

	admin.do(http.MethodPut, path, map[string]any{
		"sw": "Toleo la pili {{name}}", "en": "Second version {{name}}",
	}).mustStatus(t, http.StatusOK, "second version")
	admin.do(http.MethodPut, path, map[string]any{
		"sw": "Toleo la tatu {{name}}", "en": "Third version {{name}}",
	}).mustStatus(t, http.StatusOK, "third version")

	list := arrayOf(t, admin.do(http.MethodGet, path+"/versions", nil).
		mustStatus(t, http.StatusOK, "versions"), "items")
	if len(list) != 2 {
		t.Fatalf("%d versions in history, want 2 (the bodies replaced)", len(list))
	}
	first := list[len(list)-1]
	if first["version"] != float64(1) || first["sw"] != originalSW {
		t.Errorf("version 1 in history = %v, want the seeded body", first["sw"])
	}

	rev := admin.do(http.MethodPost, path+"/revert", map[string]any{"version": 1}).
		mustStatus(t, http.StatusOK, "revert to version 1")
	tpl, _ := rev.Body["template"].(map[string]any)
	if tpl["version"] != float64(4) {
		t.Errorf("version after revert = %v, want 4 — a revert moves forward", tpl["version"])
	}
	if tpl["sw"] != originalSW {
		t.Errorf("sw after revert = %v, want the version-1 body", tpl["sw"])
	}

	admin.do(http.MethodPost, path+"/revert", map[string]any{"version": 99}).
		mustStatus(t, http.StatusNotFound, "revert to a version that does not exist")
}

// TestAdminTemplatePreview renders the wording with sample values, which is
// what the editor shows beside the two boxes.
func TestAdminTemplatePreview(t *testing.T) {
	h := newHarness(t)
	admin := h.adminClient(t)

	sw := admin.do(http.MethodPost, "/admin/templates/reminder_7d/preview",
		map[string]any{"language": "sw", "sample": map[string]any{"name": "Asha"}}).
		mustStatus(t, http.StatusOK, "preview sw")
	if !strings.Contains(sw.str(t, "body"), "Asha") {
		t.Errorf("preview body = %q, want the sample name substituted", sw.str(t, "body"))
	}
	if sw.str(t, "encoding") != notify.EncodingGSM {
		t.Errorf("encoding = %q, want gsm for a Swahili reminder", sw.str(t, "encoding"))
	}
	if num(t, sw, "segments") != 1 {
		t.Errorf("segments = %v, want 1", num(t, sw, "segments"))
	}

	// An unsaved body can be previewed, so the editor shows the sentence as it
	// is typed — including what an emoji does to the price.
	emoji := admin.do(http.MethodPost, "/admin/templates/reminder_7d/preview",
		map[string]any{"language": "sw", "sw": "Habari {{name}} \U0001F600"}).
		mustStatus(t, http.StatusOK, "preview an unsaved body")
	if emoji.str(t, "encoding") != notify.EncodingUCS2 {
		t.Errorf("encoding with an emoji = %q, want ucs2", emoji.str(t, "encoding"))
	}

	admin.do(http.MethodPost, "/admin/templates/reminder_7d/preview",
		map[string]any{"language": "fr"}).
		mustStatus(t, http.StatusBadRequest, "preview in an unsupported language")
}

// ------------------------------------------------------------ the lock --

// TestLockedTemplateRefusesOrgOverride is the confirmed decision: a locked
// kind is the platform's wording, and an org's attempt to override it is a 409
// naming the reason rather than a silent no-op.
func TestLockedTemplateRefusesOrgOverride(t *testing.T) {
	h := newHarness(t)
	fix := h.newCreditFixture(t, "Locked", "0712140071", "+255714140072", "+255714140073")

	// `welcome` starts unlocked, so the landlord may write their own.
	fix.owner.do(http.MethodPut, "/org/notification-settings", map[string]any{
		"templates": map[string]any{
			"welcome": map[string]any{"sw": "Karibu {{name}}", "en": "Welcome {{name}}"},
		},
	}).mustStatus(t, http.StatusOK, "override an unlocked kind")

	fix.admin.do(http.MethodPatch, "/admin/templates/welcome",
		map[string]any{"locked": true}).
		mustStatus(t, http.StatusOK, "lock welcome")

	resp := fix.owner.do(http.MethodPut, "/org/notification-settings", map[string]any{
		"templates": map[string]any{
			"welcome": map[string]any{"sw": "Nyingine", "en": "Another"},
		},
	}).mustStatus(t, http.StatusConflict, "override a locked kind")
	if got := resp.str(t, "type"); got != "template_locked" {
		t.Errorf("problem type = %q, want template_locked", got)
	}

	// Clearing an override is still allowed: it moves the org *back* to the
	// platform's wording, which is what the lock protects.
	fix.owner.do(http.MethodPut, "/org/notification-settings", map[string]any{
		"templates": map[string]any{"welcome": nil},
	}).mustStatus(t, http.StatusOK, "clear an override of a locked kind")

	// The settings screen tells the landlord which kinds are read-only, and
	// what the platform's wording for each kind is.
	got := fix.owner.do(http.MethodGet, "/org/notification-settings", nil).
		mustStatus(t, http.StatusOK, "notification settings")
	locked, _ := got.Body["locked_kinds"].([]any)
	found := false
	for _, k := range locked {
		if k == "welcome" {
			found = true
		}
	}
	if !found {
		t.Errorf("locked_kinds = %v, want it to name welcome", locked)
	}
	platform, ok := got.Body["platform_templates"].(map[string]any)
	if !ok || len(platform) == 0 {
		t.Fatal("the settings carry no platform_templates")
	}
	if _, present := platform[notify.KindOTP]; present {
		t.Error("platform_templates offers otp; it is not an org's to see an editor for")
	}

	fix.admin.do(http.MethodPatch, "/admin/templates/welcome",
		map[string]any{"locked": false}).
		mustStatus(t, http.StatusOK, "unlock welcome")
	fix.owner.do(http.MethodPut, "/org/notification-settings", map[string]any{
		"templates": map[string]any{
			"welcome": map[string]any{"sw": "Karibu tena {{name}}", "en": "Welcome back {{name}}"},
		},
	}).mustStatus(t, http.StatusOK, "override after unlocking")
}

// TestHeldStatusIsNotRetryable: a held message is not failed, so the retry
// route leaves it alone — it goes out on the next top-up, unchanged.
func TestHeldStatusIsNotRetryable(t *testing.T) {
	h := newHarness(t)
	fix := h.newCreditFixture(t, "Retry", "0712140081", "+255714140082", "+255714140083")
	h.setBalance(t, fix.orgID, 10)

	fix.owner.do(http.MethodPost, "/notifications/custom", map[string]any{
		"recipients": "all_active", "body_sw": "Habari", "body_en": "Hello",
	}).mustStatus(t, http.StatusAccepted, "bulk send")
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE notification_log SET status = 'held_no_credit'
		 WHERE org_id = $1 AND kind = 'custom'`, fix.orgID); err != nil {
		t.Fatalf("hold messages: %v", err)
	}

	log := fix.owner.do(http.MethodGet, "/notifications/log?status=held_no_credit", nil).
		mustStatus(t, http.StatusOK, "held messages in the log")
	items := arrayOf(t, log, "items")
	if len(items) != 2 {
		t.Fatalf("%d held rows in the log, want 2", len(items))
	}
	id, _ := items[0]["id"].(string)

	resp := fix.owner.do(http.MethodPost, "/notifications/log/"+id+"/retry", nil).
		mustStatus(t, http.StatusConflict, "retry a held message")
	if got := resp.str(t, "type"); got != "not_failed" {
		t.Errorf("problem type = %q, want not_failed", got)
	}
}
