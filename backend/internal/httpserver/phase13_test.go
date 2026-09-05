package httpserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"tms/backend/internal/notify"
)

// Phase 13 — Swahili/English per user (SPEC §3.2, PLAN2 Phase 13).
//
// The rule the whole phase turns on: the language of a message is a fact about
// the person receiving it. `users.locale` decides; `orgs.settings.sms_language`
// is only what a renter who has never said gets.

// ------------------------------------------------------------- helpers --

// registerRenterWithLocale is registerRenter with the language the public
// SW/EN toggle was set to carried into the account.
func (h *harness) registerRenterWithLocale(t *testing.T, phone, name, locale string) *client {
	t.Helper()
	c := h.client()
	c.do(http.MethodPost, "/auth/otp/send",
		map[string]any{"phone": phone, "purpose": "register", "locale": locale}).
		mustStatus(t, http.StatusAccepted, "otp send")
	code := h.sms.LastOTP(phone)
	if code == "" {
		t.Fatalf("no OTP captured for %s", phone)
	}
	verify := c.do(http.MethodPost, "/auth/otp/verify",
		map[string]any{"phone": phone, "code": code, "purpose": "register"}).
		mustStatus(t, http.StatusOK, "otp verify")
	c.do(http.MethodPost, "/auth/register/renter", map[string]any{
		"phone": phone, "otp_token": verify.str(t, "otp_token"),
		"pin": defaultPIN, "full_name": name, "locale": locale,
	}).mustStatus(t, http.StatusCreated, "register renter")
	return c
}

// notificationLanguages reads the language column of every row of one kind,
// which is the only place the answer to "what did this renter get?" is written
// down as data rather than as prose.
func (h *harness) notificationLanguages(t *testing.T, kind string) map[string]int {
	t.Helper()
	rows, err := h.pool.Query(context.Background(),
		`SELECT language FROM notification_log WHERE kind = $1`, kind)
	if err != nil {
		t.Fatalf("read notification languages: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var lang string
		if err := rows.Scan(&lang); err != nil {
			t.Fatalf("scan language: %v", err)
		}
		out[lang]++
	}
	return out
}

// bodyTo returns the last message body sent to one phone number.
func (h *harness) bodyTo(t *testing.T, phone string) string {
	t.Helper()
	msgs := h.sms.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].To == phone {
			return msgs[i].Body
		}
	}
	t.Fatalf("no SMS captured for %s", phone)
	return ""
}

// ------------------------------------------------------ locale on users --

// TestRegistrationCarriesTheChosenLocale: the SW/EN toggle a visitor used
// before they had an account is the locale the account starts with, and
// `/auth/me` hands it straight back to the app.
func TestRegistrationCarriesTheChosenLocale(t *testing.T) {
	h := newHarness(t)

	en := h.registerRenterWithLocale(t, "+255719100101", "English Renter", "en")
	me := en.do(http.MethodGet, "/auth/me?audience=renter", nil).
		mustStatus(t, http.StatusOK, "auth/me")
	if got := me.str(t, "user", "locale"); got != "en" {
		t.Errorf("locale = %q, want en", got)
	}

	// Absent means Swahili — the platform default, not the browser's guess.
	sw := h.registerRenter("+255719100102", "Default Renter", defaultPIN)
	if got := sw.do(http.MethodGet, "/auth/me?audience=renter", nil).
		mustStatus(t, http.StatusOK, "auth/me").str(t, "user", "locale"); got != "sw" {
		t.Errorf("default locale = %q, want sw", got)
	}
}

// TestRegistrationRejectsAnUnknownLocale: the field is a closed set of two.
func TestRegistrationRejectsAnUnknownLocale(t *testing.T) {
	h := newHarness(t)
	const phone = "+255719100110"
	c := h.client()
	c.do(http.MethodPost, "/auth/otp/send", map[string]any{"phone": phone, "purpose": "register"}).
		mustStatus(t, http.StatusAccepted, "otp send")
	verify := c.do(http.MethodPost, "/auth/otp/verify", map[string]any{
		"phone": phone, "code": h.sms.LastOTP(phone), "purpose": "register",
	}).mustStatus(t, http.StatusOK, "otp verify")

	resp := c.do(http.MethodPost, "/auth/register/renter", map[string]any{
		"phone": phone, "otp_token": verify.str(t, "otp_token"),
		"pin": defaultPIN, "full_name": "Nonsense Locale", "locale": "fr",
	}).mustStatus(t, http.StatusBadRequest, "register with locale fr")
	if errs, _ := resp.Body["errors"].(map[string]any); errs["locale"] == nil {
		t.Errorf("errors = %v, want one for locale", resp.Body["errors"])
	}
}

// TestRenterChangesOwnLocale: PATCH /me is the renter Profile switch, and the
// change is audited like every other thing a session does.
func TestRenterChangesOwnLocale(t *testing.T) {
	h := newHarness(t)
	renter := h.registerRenter("+255719100120", "Switch Renter", defaultPIN)

	updated := renter.do(http.MethodPatch, "/me", map[string]any{"locale": "en"}).
		mustStatus(t, http.StatusOK, "switch to english")
	if got := updated.str(t, "user", "locale"); got != "en" {
		t.Errorf("locale = %q, want en", got)
	}
	if got := renter.do(http.MethodGet, "/auth/me?audience=renter", nil).
		mustStatus(t, http.StatusOK, "auth/me").str(t, "user", "locale"); got != "en" {
		t.Errorf("auth/me locale = %q, want en", got)
	}

	payloads := h.auditPayloads(t, "user.locale_update")
	if len(payloads) != 1 {
		t.Fatalf("audit rows for user.locale_update = %d, want 1", len(payloads))
	}
	if !strings.Contains(payloads[0], `"sw"`) || !strings.Contains(payloads[0], `"en"`) {
		t.Errorf("audit payload does not record the move: %s", payloads[0])
	}

	renter.do(http.MethodPatch, "/me", map[string]any{"locale": "de"}).
		mustStatus(t, http.StatusBadRequest, "unknown locale")
	renter.do(http.MethodPatch, "/me", map[string]any{}).
		mustStatus(t, http.StatusBadRequest, "missing locale")
}

// TestOrgUserChangesOwnLocale: the same switch for a landlord, and signup
// takes a locale the way registration does.
func TestOrgUserChangesOwnLocale(t *testing.T) {
	h := newHarness(t)
	owner, created := h.createOrg("Locale Org", "Locale Owner",
		"locale-owner@jjne.test", "0719100130", "password123")
	if got := created.str(t, "user", "locale"); got != "sw" {
		t.Errorf("signup locale = %q, want sw", got)
	}

	updated := owner.do(http.MethodPatch, "/org/members/me", map[string]any{"locale": "en"}).
		mustStatus(t, http.StatusOK, "switch to english")
	if got := updated.str(t, "user", "locale"); got != "en" {
		t.Errorf("locale = %q, want en", got)
	}
	if got := owner.do(http.MethodGet, "/auth/me?audience=org", nil).
		mustStatus(t, http.StatusOK, "auth/me").str(t, "user", "locale"); got != "en" {
		t.Errorf("auth/me locale = %q, want en", got)
	}

	// An invite carries the language the new member's screens open in.
	member := owner.do(http.MethodPost, "/org/members", map[string]any{
		"email": "locale-staff@jjne.test", "full_name": "Locale Staff",
		"role": "org_manager", "locale": "en",
	}).mustStatus(t, http.StatusCreated, "invite")
	if got := member.str(t, "member", "locale"); got != "en" {
		t.Errorf("member locale = %q, want en", got)
	}
}

// -------------------------------------------------------------- the SMS --

// TestOTPFollowsTheAccountLanguage: the code is the first message anybody ever
// gets, and it is renter-facing platform text like the rest.
func TestOTPFollowsTheAccountLanguage(t *testing.T) {
	h := newHarness(t)
	const phone = "+255719100140"
	renter := h.registerRenter(phone, "OTP Renter", defaultPIN)

	// Default locale: Swahili.
	renter.do(http.MethodPost, "/auth/otp/send", map[string]any{"phone": phone, "purpose": "login"}).
		mustStatus(t, http.StatusAccepted, "otp send")
	if body := h.bodyTo(t, phone); !strings.Contains(body, "msimbo") {
		t.Errorf("swahili OTP body = %q, want the Swahili wording", body)
	}

	renter.do(http.MethodPatch, "/me", map[string]any{"locale": "en"}).
		mustStatus(t, http.StatusOK, "switch to english")
	renter.do(http.MethodPost, "/auth/otp/send", map[string]any{"phone": phone, "purpose": "sign"}).
		mustStatus(t, http.StatusAccepted, "otp send")
	if body := h.bodyTo(t, phone); !strings.Contains(body, "verification code") {
		t.Errorf("english OTP body = %q, want the English wording", body)
	}

	// A number with no account yet takes the toggle's language as a hint.
	const stranger = "+255719100141"
	h.client().do(http.MethodPost, "/auth/otp/send",
		map[string]any{"phone": stranger, "purpose": "register", "locale": "en"}).
		mustStatus(t, http.StatusAccepted, "stranger otp send")
	if body := h.bodyTo(t, stranger); !strings.Contains(body, "verification code") {
		t.Errorf("hinted OTP body = %q, want the English wording", body)
	}
}

// TestSchedulerPrefersTheRenterLocale: the sweep reads the recipient's row, so
// an English-speaking landlord's Swahili renter still gets Swahili — and the
// language used is recorded on the notification.
func TestSchedulerPrefersTheRenterLocale(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "SchedLang", "0719100150", "+255719100151")
	fix.owner.do(http.MethodPatch, "/org", map[string]any{
		"settings": map[string]any{"sms_language": "en"},
	}).mustStatus(t, http.StatusOK, "org sends english by default")

	h.parkSchedules(t, fix.contractID)
	h.setDueDate(t, fix.scheduleIDs[0], schedulerToday(), "pending")

	if res := h.runScheduler(t, notify.Options{ForceHour: true}); res.Queued[notify.KindReminderDue] != 1 {
		t.Fatalf("sweep queued %v, want one reminder_due", res.Queued)
	}
	notes := ofKind(h.notifications(t), notify.KindReminderDue)
	if len(notes) != 1 {
		t.Fatalf("reminder_due rows = %+v, want 1", notes)
	}
	if !strings.Contains(notes[0].Body, "kodi yako") {
		t.Errorf("body = %q, want the Swahili reminder (the renter's locale)", notes[0].Body)
	}
	if got := h.notificationLanguages(t, notify.KindReminderDue); got["sw"] != 1 {
		t.Errorf("languages = %v, want one sw row", got)
	}
}

// ------------------------------------------------------------ bulk sends --

// bilingualOrg is an org with one Swahili renter and one English one, which is
// the whole point of the bulk fan-out.
type bilingualOrg struct {
	owner    *client
	swPhone  string
	enPhone  string
	renterEN *client
}

func (h *harness) newBilingualOrg(t *testing.T, tag, ownerPhone, swPhone, enPhone string) bilingualOrg {
	t.Helper()
	fix := h.newPaymentFixture(t, tag, ownerPhone, swPhone)
	renterEN, contractEN := h.tenancyOn(t, fix.owner, fix.unitCodes[1], fix.periodID, enPhone, tag+" English")
	renterEN.do(http.MethodPatch, "/me", map[string]any{"locale": "en"}).
		mustStatus(t, http.StatusOK, "second renter prefers english")
	// These fixtures are about the bulk send, not the timeline. The scheduler
	// sweep runs across every org in the shared test database, so their
	// instalments are pushed out of its way.
	h.parkSchedules(t, fix.contractID)
	h.parkSchedules(t, contractEN)
	return bilingualOrg{owner: fix.owner, swPhone: swPhone, enPhone: enPhone, renterEN: renterEN}
}

// TestBulkSMSFansOutPerLanguage: one send, two bodies, each renter in their own
// language — and the counts the response reports match the rows written.
func TestBulkSMSFansOutPerLanguage(t *testing.T) {
	h := newHarness(t)
	org := h.newBilingualOrg(t, "Bulk", "0719100160", "+255719100161", "+255719100162")

	resp := org.owner.do(http.MethodPost, "/notifications/custom", map[string]any{
		"recipients": "all_active",
		"body_sw":    "Habari {{name}}, maji yatakatika kesho.",
		"body_en":    "Hello {{name}}, the water will be off tomorrow.",
	}).mustStatus(t, http.StatusAccepted, "bulk send")

	if got := num(t, resp, "queued"); got != 2 {
		t.Fatalf("queued = %v, want 2 — body: %s", got, resp.Raw)
	}
	if got := num(t, resp, "by_language", "sw"); got != 1 {
		t.Errorf("by_language.sw = %v, want 1 — body: %s", got, resp.Raw)
	}
	if got := num(t, resp, "by_language", "en"); got != 1 {
		t.Errorf("by_language.en = %v, want 1 — body: %s", got, resp.Raw)
	}

	notes := ofKind(h.notifications(t), notify.KindCustom)
	byPhone := map[string]string{}
	for _, n := range notes {
		byPhone[n.ToPhone] = n.Body
	}
	if !strings.HasPrefix(byPhone[org.swPhone], "Habari ") {
		t.Errorf("the Swahili renter got %q", byPhone[org.swPhone])
	}
	if !strings.HasPrefix(byPhone[org.enPhone], "Hello ") {
		t.Errorf("the English renter got %q", byPhone[org.enPhone])
	}
	if got := h.notificationLanguages(t, notify.KindCustom); got["sw"] != 1 || got["en"] != 1 {
		t.Errorf("stored languages = %v, want one of each", got)
	}
}

// TestBulkSMSFallsBackToTheOnlyBodyGiven: a landlord who wrote one language
// reaches everybody in it. Silence is not the safer failure for a notice.
func TestBulkSMSFallsBackToTheOnlyBodyGiven(t *testing.T) {
	h := newHarness(t)
	org := h.newBilingualOrg(t, "Fallback", "0719100170", "+255719100171", "+255719100172")

	resp := org.owner.do(http.MethodPost, "/notifications/custom", map[string]any{
		"recipients": "all_active",
		"body_en":    "Hello {{name}}, the lift is under repair.",
	}).mustStatus(t, http.StatusAccepted, "english-only bulk send")

	if got := num(t, resp, "queued"); got != 2 {
		t.Fatalf("queued = %v, want 2 — body: %s", got, resp.Raw)
	}
	if got := num(t, resp, "by_language", "en"); got != 2 {
		t.Errorf("by_language.en = %v, want 2 (both renters got the only body) — body: %s", got, resp.Raw)
	}
	for _, n := range ofKind(h.notifications(t), notify.KindCustom) {
		if !strings.HasPrefix(n.Body, "Hello ") {
			t.Errorf("%s got %q, want the English body", n.ToPhone, n.Body)
		}
	}
}

// TestBulkSMSLegacyBodyStandsForBothLanguages: the Phase 6 field still works,
// and reaches everyone in the one wording it carries.
func TestBulkSMSLegacyBodyStandsForBothLanguages(t *testing.T) {
	h := newHarness(t)
	org := h.newBilingualOrg(t, "Legacy", "0719100180", "+255719100181", "+255719100182")

	resp := org.owner.do(http.MethodPost, "/notifications/custom", map[string]any{
		"recipients": "all_active", "body": "Habari {{name}}, karibu.",
	}).mustStatus(t, http.StatusAccepted, "legacy bulk send")
	if got := num(t, resp, "queued"); got != 2 {
		t.Fatalf("queued = %v, want 2 — body: %s", got, resp.Raw)
	}
	langs := h.notificationLanguages(t, notify.KindCustom)
	if langs["sw"] != 1 || langs["en"] != 1 {
		t.Errorf("languages = %v, want one row per recipient locale", langs)
	}
}

// TestBulkSMSValidatesEachBodySeparately: the error names the tab the landlord
// typed in.
func TestBulkSMSValidatesEachBodySeparately(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "BulkValid", "0719100190", "+255719100191")
	h.parkSchedules(t, fix.contractID)

	resp := fix.owner.do(http.MethodPost, "/notifications/custom", map[string]any{
		"recipients": "all_active",
		"body_sw":    "Habari",
		"body_en":    "You owe {{amount}}",
	}).mustStatus(t, http.StatusBadRequest, "unknown variable in the english body")
	errs, _ := resp.Body["errors"].(map[string]any)
	if errs["body_en"] == nil {
		t.Errorf("errors = %v, want one for body_en", errs)
	}
	if errs["body_sw"] != nil {
		t.Errorf("errors = %v, want nothing against the valid Swahili body", errs)
	}

	fix.owner.do(http.MethodPost, "/notifications/custom", map[string]any{
		"recipients": "all_active",
	}).mustStatus(t, http.StatusBadRequest, "no body at all")
}

// TestRecipientsPreviewCountsPerLanguage: the compose screen asks before it
// sends, with the same filters, and gets the same answer.
func TestRecipientsPreviewCountsPerLanguage(t *testing.T) {
	h := newHarness(t)
	org := h.newBilingualOrg(t, "Preview", "0719100200", "+255719100201", "+255719100202")

	preview := org.owner.do(http.MethodGet,
		"/notifications/custom/recipients-preview?recipients=all_active", nil).
		mustStatus(t, http.StatusOK, "preview")
	if got := num(t, preview, "count"); got != 2 {
		t.Errorf("count = %v, want 2 — body: %s", got, preview.Raw)
	}
	if got := num(t, preview, "by_language", "sw"); got != 1 {
		t.Errorf("by_language.sw = %v, want 1 — body: %s", got, preview.Raw)
	}
	if got := num(t, preview, "by_language", "en"); got != 1 {
		t.Errorf("by_language.en = %v, want 1 — body: %s", got, preview.Raw)
	}

	org.owner.do(http.MethodGet, "/notifications/custom/recipients-preview?recipients=nonsense", nil).
		mustStatus(t, http.StatusBadRequest, "unknown selector")
}

// ------------------------------------------------------------- contracts --

// TestContractRendersInTheRenterLanguage: the document a renter signs is in
// the language they read, the landlord can override it per contract, and the
// language is stored beside the terms.
func TestContractRendersInTheRenterLanguage(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "ContractLang", "0719100210", "+255719100211")

	// The fixture's renter has the default locale: the seeded Swahili body.
	got := fix.owner.do(http.MethodGet, "/contracts/"+fix.contractID, nil).
		mustStatus(t, http.StatusOK, "read contract")
	if lang := got.str(t, "contract", "language"); lang != "sw" {
		t.Errorf("language = %q, want sw", lang)
	}
	if terms := fix.renter.do(http.MethodGet, "/contracts/"+fix.contractID+"/document", nil).
		mustStatus(t, http.StatusOK, "document").str(t, "terms_html"); !strings.Contains(terms, "Mkataba wa Upangaji") {
		t.Errorf("the Swahili renter's document is not in Swahili: %s", terms)
	}

	// The landlord may say otherwise for one contract.
	english := fix.owner.do(http.MethodPost, "/contracts", map[string]any{
		"unit_id": fix.unitIDs[1], "renter_user_id": fix.renterID,
		"payment_period_id": fix.periodID, "term_days": testTermDays,
		"start_date": time.Now().UTC().Format(testDateLayout), "language": "en",
	}).mustStatus(t, http.StatusCreated, "english contract")
	if lang := english.str(t, "contract", "language"); lang != "en" {
		t.Errorf("overridden language = %q, want en", lang)
	}
	if terms := fix.owner.do(http.MethodGet,
		"/contracts/"+english.str(t, "contract", "id")+"/document", nil).
		mustStatus(t, http.StatusOK, "english document").str(t, "terms_html"); !strings.Contains(terms, "Tenancy Agreement") {
		t.Errorf("the overridden document is not in English: %s", terms)
	}

	fix.owner.do(http.MethodPost, "/contracts", map[string]any{
		"unit_id": fix.unitIDs[2], "renter_user_id": fix.renterID,
		"payment_period_id": fix.periodID, "term_days": testTermDays,
		"start_date": time.Now().UTC().Format(testDateLayout), "language": "de",
	}).mustStatus(t, http.StatusBadRequest, "unknown contract language")
}

// TestContractFallsBackToEnglishWithoutASwahiliBody: an org that cleared (or
// never wrote) its Swahili terms issues the English document rather than a
// blank one, and says so.
func TestContractFallsBackToEnglishWithoutASwahiliBody(t *testing.T) {
	h := newHarness(t)
	fix := h.newContractFixture(t, "NoSwBody", "0719100220", "+255719100221")

	templates := listOf(t, fix.owner.do(http.MethodGet, "/contract-templates", nil).
		mustStatus(t, http.StatusOK, "templates"))
	templateID, _ := templates[0]["id"].(string)
	fix.owner.do(http.MethodPatch, "/contract-templates/"+templateID,
		map[string]any{"body_html_sw": ""}).
		mustStatus(t, http.StatusOK, "clear the Swahili body")

	created := fix.owner.do(http.MethodPost, "/contracts", map[string]any{
		"unit_id": fix.unitIDs[1], "renter_user_id": fix.renterID,
		"payment_period_id": fix.periodID, "term_days": testTermDays,
		"start_date": time.Now().UTC().Format(testDateLayout),
	}).mustStatus(t, http.StatusCreated, "contract without a Swahili body")

	if lang := created.str(t, "contract", "language"); lang != "en" {
		t.Errorf("language = %q, want en (there is no Swahili body to render)", lang)
	}
	if terms := fix.owner.do(http.MethodGet,
		"/contracts/"+created.str(t, "contract", "id")+"/document", nil).
		mustStatus(t, http.StatusOK, "document").str(t, "terms_html"); !strings.Contains(terms, "Tenancy Agreement") {
		t.Errorf("the fallback document is not the English one: %s", terms)
	}
}

// TestTemplateSwahiliBodyIsSanitisedLikeTheEnglishOne: two bodies, one policy.
func TestTemplateSwahiliBodyIsSanitisedLikeTheEnglishOne(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("TplSW", "tplsw@jjne.test", "0719100230", []string{"Room 1"}, testUnitAmount)

	created := fix.client.do(http.MethodPost, "/contract-templates", map[string]any{
		"name":         "Bilingual terms",
		"body_html":    "<p>Rent is {{rent}}</p><script>alert(1)</script>",
		"body_html_sw": `<p onclick="x()">Kodi ni {{rent}}</p><script>alert(1)</script>`,
	}).mustStatus(t, http.StatusCreated, "create bilingual template")

	sw := created.str(t, "template", "body_html_sw")
	if strings.Contains(sw, "<script") || strings.Contains(sw, "onclick") {
		t.Errorf("the Swahili body was stored unsanitised: %q", sw)
	}
	if !strings.Contains(sw, "Kodi ni {{rent}}") {
		t.Errorf("the Swahili body lost its content: %q", sw)
	}

	// It comes back on the single-template read, and previews in its own
	// language.
	id := created.str(t, "template", "id")
	if got := fix.client.do(http.MethodGet, "/contract-templates/"+id, nil).
		mustStatus(t, http.StatusOK, "read template").str(t, "template", "body_html_sw"); got != sw {
		t.Errorf("body_html_sw = %q, want %q", got, sw)
	}
	preview := fix.client.do(http.MethodPost, "/contract-templates/"+id+"/preview",
		map[string]any{"language": "sw"}).
		mustStatus(t, http.StatusOK, "swahili preview")
	if got := preview.str(t, "language"); got != "sw" {
		t.Errorf("preview language = %q, want sw", got)
	}
	if got := preview.str(t, "html"); !strings.Contains(got, "Kodi ni ") {
		t.Errorf("the Swahili preview rendered the wrong body: %q", got)
	}
}
