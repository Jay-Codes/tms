package httpserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/httpserver"
	"tms/backend/internal/notify"
	"tms/backend/internal/tz"
)

// dateLayout is the wire form of a bare date.
const testDateLayout = "2006-01-02"

// ------------------------------------------------------------- fixtures --

// queries is the sqlc handle the scheduler runs against — the same one cmd/api
// builds from the pool.
func (h *harness) queries() *sqlc.Queries { return sqlc.New(h.pool) }

// schedulerToday is the calendar day the sweep is reading, which is the day on
// the platform's wall clock (Africa/Dar_es_Salaam) and not the UTC one. The two
// disagree for the three hours after 21:00 UTC, so a test that says
// `time.Now().UTC()` places its "due today" row on yesterday for a quarter of
// every evening and the sweep finds nothing.
func schedulerToday() time.Time { return tz.LocalDate(time.Now().In(tz.Zone())) }

// setDueDate moves one schedule's due date (and, optionally, its status) so a
// one-day test can watch a month-long timeline.
func (h *harness) setDueDate(t *testing.T, scheduleID string, due time.Time, status string) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE payment_schedules SET due_date = $2, status = $3 WHERE id = $1`,
		scheduleID, due.Format(testDateLayout), status); err != nil {
		t.Fatalf("set due date: %v", err)
	}
}

// parkSchedules pushes every schedule of a contract far into the future, so a
// test can then place exactly the rows it wants the sweep to find.
func (h *harness) parkSchedules(t *testing.T, contractID string) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE payment_schedules SET due_date = CURRENT_DATE + 400 WHERE contract_id = $1`,
		contractID); err != nil {
		t.Fatalf("park schedules: %v", err)
	}
}

// backdateContract ages a contract so the unsigned-reminder cushion has been
// crossed.
func (h *harness) backdateContract(t *testing.T, contractID string, days int) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE contracts SET created_at = now() - ($2::int * interval '1 day') WHERE id = $1`,
		contractID, days); err != nil {
		t.Fatalf("backdate contract: %v", err)
	}
}

// runScheduler runs one sweep against the test database, exactly as the ticker
// and POST /admin/jobs/notifications do.
func (h *harness) runScheduler(t *testing.T, opt notify.Options) notify.Result {
	t.Helper()
	if opt.Settings == nil {
		opt.Settings = httpserver.SchedulerSettings
	}
	res, err := notify.RunOnce(context.Background(), h.queries(), time.Now(), opt)
	if err != nil {
		t.Fatalf("scheduler sweep: %v", err)
	}
	return res
}

// dedupeKeys reads every dedupe key written so far, so a test asserts on the
// keys API.md fixes rather than on message bodies.
func (h *harness) dedupeKeys(t *testing.T, kind string) []string {
	t.Helper()
	rows, err := h.pool.Query(context.Background(),
		`SELECT dedupe_key FROM notification_log WHERE kind = $1 ORDER BY dedupe_key`, kind)
	if err != nil {
		t.Fatalf("read dedupe keys: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scan dedupe key: %v", err)
		}
		out = append(out, k)
	}
	return out
}

// ---------------------------------------------------- settings endpoint --

// TestNotificationSettingsDefaults: an org that has never opened the screen
// still has a full, sane configuration to show.
func TestNotificationSettingsDefaults(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Settings", "settings@jjne.test", "0715006000", []string{"Room 1"}, testUnitAmount)

	got := fix.client.do(http.MethodGet, "/org/notification-settings", nil).
		mustStatus(t, http.StatusOK, "get settings")

	if got.Body["sender_name"] != nil {
		t.Errorf("sender_name = %v, want null (the platform default)", got.Body["sender_name"])
	}
	if lang := got.str(t, "language"); lang != "sw" {
		t.Errorf("language = %q, want sw", lang)
	}
	if hour := num(t, got, "send_hour_local"); hour != 9 {
		t.Errorf("send_hour_local = %v, want 9", hour)
	}
	if offset := num(t, got, "kinds", "reminder_7d", "offset_days"); offset != 7 {
		t.Errorf("reminder_7d.offset_days = %v, want 7", offset)
	}
	if after := num(t, got, "kinds", "unsigned_reminder", "after_days"); after != 7 {
		t.Errorf("unsigned_reminder.after_days = %v, want 7", after)
	}
	templates, ok := got.Body["templates"].(map[string]any)
	if !ok {
		t.Fatalf("templates is not an object — body: %s", got.Raw)
	}
	// Every overridable kind is named, with null where the default applies.
	for _, kind := range notify.TemplateKinds() {
		v, present := templates[kind]
		if !present {
			t.Errorf("templates is missing %q", kind)
		}
		if v != nil {
			t.Errorf("templates[%q] = %v, want null before any override", kind, v)
		}
	}
}

// TestNotificationSettingsPartialMerge: saving one field leaves the rest alone
// (API.md: "partial merge").
func TestNotificationSettingsPartialMerge(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Merge", "merge@jjne.test", "0715006010", []string{"Room 1"}, testUnitAmount)

	fix.client.do(http.MethodPut, "/org/notification-settings", map[string]any{
		"sender_name": "JJNE", "language": "en", "send_hour_local": 7,
	}).mustStatus(t, http.StatusOK, "put settings")

	// A second save touching only one toggle must not reset the sender name.
	after := fix.client.do(http.MethodPut, "/org/notification-settings", map[string]any{
		"kinds": map[string]any{"overdue_daily": map[string]any{"enabled": false}},
	}).mustStatus(t, http.StatusOK, "second put")

	if got := after.str(t, "sender_name"); got != "JJNE" {
		t.Errorf("sender_name = %q after a partial save, want JJNE", got)
	}
	if got := after.str(t, "language"); got != "en" {
		t.Errorf("language = %q, want en", got)
	}
	if got := num(t, after, "send_hour_local"); got != 7 {
		t.Errorf("send_hour_local = %v, want 7", got)
	}
	if enabled, _ := after.Body["kinds"].(map[string]any)["overdue_daily"].(map[string]any)["enabled"].(bool); enabled {
		t.Error("overdue_daily is still enabled after being switched off")
	}

	// `language` is the org's one SMS language, not a second copy of it.
	org := fix.client.do(http.MethodGet, "/org", nil).mustStatus(t, http.StatusOK, "get org")
	if got := org.str(t, "settings", "sms_language"); got != "en" {
		t.Errorf("orgs.settings.sms_language = %q, want en", got)
	}
}

// TestNotificationSettingsValidation is the 400 table of API.md Phase 6.
func TestNotificationSettingsValidation(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Valid", "valid@jjne.test", "0715006020", []string{"Room 1"}, testUnitAmount)

	cases := []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"hour above 23", map[string]any{"send_hour_local": 24}, "send_hour_local"},
		{"hour below 0", map[string]any{"send_hour_local": -1}, "send_hour_local"},
		{"sender name too long", map[string]any{"sender_name": "TWELVECHARSX"}, "sender_name"},
		{"language not offered", map[string]any{"language": "fr"}, "language"},
		{"offset above 30", map[string]any{
			"kinds": map[string]any{"reminder_7d": map[string]any{"offset_days": 31}},
		}, "kinds.reminder_7d.offset_days"},
		{"after_days below 0", map[string]any{
			"kinds": map[string]any{"unsigned_reminder": map[string]any{"after_days": -1}},
		}, "kinds.unsigned_reminder.after_days"},
		{"unknown variable", map[string]any{
			"templates": map[string]any{"reminder_due": map[string]any{"en": "Hi {{nmae}}"}},
		}, "templates.reminder_due.en"},
		{"variable outside the org whitelist", map[string]any{
			"templates": map[string]any{"reminder_due": map[string]any{"en": "Because {{reason}}"}},
		}, "templates.reminder_due.en"},
		{"template too long", map[string]any{
			"templates": map[string]any{"reminder_due": map[string]any{"sw": strings.Repeat("a", 321)}},
		}, "templates.reminder_due.sw"},
		{"unknown kind", map[string]any{
			"templates": map[string]any{"nonsense": map[string]any{"en": "hi"}},
		}, "templates.nonsense"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := fix.client.do(http.MethodPut, "/org/notification-settings", tc.body).
				mustStatus(t, http.StatusBadRequest, "put settings")
			errs, ok := resp.Body["errors"].(map[string]any)
			if !ok {
				t.Fatalf("no errors object — body: %s", resp.Raw)
			}
			if _, named := errs[tc.field]; !named {
				t.Errorf("errors = %v, want one for %q", errs, tc.field)
			}
		})
	}
}

// TestNotificationTemplateOverrideReachesTheRenter: an org's own wording is
// what the renter actually receives, and clearing it restores the default.
func TestNotificationTemplateOverrideReachesTheRenter(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Override", "0715006100", "+255715006101")

	// The renter's locale decides which of the two wordings is rendered
	// (Phase 13), so the override is written in the language they read.
	fix.owner.do(http.MethodPut, "/org/notification-settings", map[string]any{
		"language": "sw",
		"templates": map[string]any{
			"thank_you": map[string]any{"sw": "JJnE thanks you {{name}} — {{amount}} received for {{unit}}."},
		},
	}).mustStatus(t, http.StatusOK, "override thank_you")

	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": fix.amounts[0], "method": "cash",
	}).mustStatus(t, http.StatusCreated, "record payment")

	notes := ofKind(h.notifications(t), "thank_you")
	if len(notes) != 1 {
		t.Fatalf("thank_you notifications = %+v, want 1", notes)
	}
	if !strings.HasPrefix(notes[0].Body, "JJnE thanks you Override Renter — TZS 250,000 received for Room 1.") {
		t.Errorf("body = %q, want the org's own wording with its variables filled in", notes[0].Body)
	}

	// A null template hands the kind back to the platform default.
	fix.owner.do(http.MethodPut, "/org/notification-settings", map[string]any{
		"templates": map[string]any{"thank_you": nil},
	}).mustStatus(t, http.StatusOK, "clear override")
	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": fix.amounts[1], "method": "cash",
	}).mustStatus(t, http.StatusCreated, "second payment")

	notes = ofKind(h.notifications(t), "thank_you")
	if len(notes) != 2 {
		t.Fatalf("thank_you notifications = %d, want 2", len(notes))
	}
	if strings.Contains(notes[1].Body, "JJnE thanks you") {
		t.Errorf("body = %q after clearing the override, want the platform wording", notes[1].Body)
	}
}

// ------------------------------------------------------------ scheduler --

// TestSchedulerTimeline is Flow 8 end to end against one org: a schedule due in
// a week, one due today, one overdue since yesterday, and a contract that has
// been waiting eight days for a signature. Each produces exactly the dedupe key
// API.md fixes, and a second run the same day produces nothing.
func TestSchedulerTimeline(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Timeline", "0715006200", "+255715006201")

	// Place the three schedules the sweep should find, and move the rest out
	// of the way so nothing else fires.
	h.parkSchedules(t, fix.contractID)
	today := schedulerToday()
	h.setDueDate(t, fix.scheduleIDs[0], today.AddDate(0, 0, 7), "pending")
	h.setDueDate(t, fix.scheduleIDs[1], today, "pending")
	h.setDueDate(t, fix.scheduleIDs[2], today.AddDate(0, 0, -1), "overdue")

	// A second renter whose contract has been waiting for a signature.
	unsigned := h.newContractFixture(t, "Unsigned", "0715006210", "+255715006211")
	h.parkSchedules(t, unsigned.contractID)
	h.backdateContract(t, unsigned.contractID, 8)

	res := h.runScheduler(t, notify.Options{ForceHour: true, BaseURL: "http://localhost:8080"})

	date := today.Format(testDateLayout)
	want := map[string][]string{
		notify.KindReminder7d:       {"reminder_7d:" + fix.scheduleIDs[0] + ":" + date},
		notify.KindReminderDue:      {"reminder_due:" + fix.scheduleIDs[1] + ":" + date},
		notify.KindOverdueDaily:     {"overdue_daily:" + fix.scheduleIDs[2] + ":" + date},
		notify.KindUnsignedReminder: {"unsigned:" + unsigned.contractID + ":" + date},
	}
	for kind, wantKeys := range want {
		if got := res.Queued[kind]; got != len(wantKeys) {
			t.Errorf("%s queued %d, want %d (whole result: %v)", kind, got, len(wantKeys), res.Queued)
		}
		got := h.dedupeKeys(t, kind)
		if strings.Join(got, ",") != strings.Join(wantKeys, ",") {
			t.Errorf("%s dedupe keys = %v, want %v", kind, got, wantKeys)
		}
	}

	// The reminder names the renter, the unit and the money (SPEC §6).
	body := ofKind(h.notifications(t), notify.KindReminderDue)[0].Body
	for _, want := range []string{"Timeline Renter", "Room 1", "250,000", date} {
		if !strings.Contains(body, want) {
			t.Errorf("reminder_due body %q does not name %q", body, want)
		}
	}
	// The unsigned nudge carries the link the renter has to open.
	nudge := ofKind(h.notifications(t), notify.KindUnsignedReminder)[0].Body
	if !strings.Contains(nudge, "http://localhost:8080/enduser/contract/"+unsigned.contractID) {
		t.Errorf("unsigned_reminder body %q carries no contract link", nudge)
	}

	// A second sweep on the same day queues nothing: the keys already exist.
	again := h.runScheduler(t, notify.Options{ForceHour: true, BaseURL: "http://localhost:8080"})
	if again.Total() != 0 {
		t.Errorf("a repeat sweep queued %v, want nothing", again.Queued)
	}
}

// TestSchedulerWaitsForTheSendHour: before the org's local send hour nothing
// goes out, however many times the ticker runs.
func TestSchedulerWaitsForTheSendHour(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "SendHour", "0715006300", "+255715006301")
	h.parkSchedules(t, fix.contractID)
	h.setDueDate(t, fix.scheduleIDs[0], schedulerToday(), "pending")

	// Pick the next whole hour so the gate is still ahead of the wall clock;
	// in the 23:00 hour there is no such hour, and the run is skipped rather
	// than made to fail.
	sendHour := time.Now().In(tz.Zone()).Hour() + 1
	if sendHour > 23 {
		t.Skip("no local hour left ahead of the clock to gate on")
	}
	fix.owner.do(http.MethodPut, "/org/notification-settings", map[string]any{
		"send_hour_local": sendHour,
	}).mustStatus(t, http.StatusOK, "set send hour")

	if res := h.runScheduler(t, notify.Options{}); res.Total() != 0 {
		t.Errorf("the sweep queued %v before the send hour, want nothing", res.Queued)
	}

	// Forcing the hour is what the admin job does, and it fires.
	if res := h.runScheduler(t, notify.Options{ForceHour: true}); res.Queued[notify.KindReminderDue] != 1 {
		t.Errorf("forced sweep queued %v, want one reminder_due", res.Queued)
	}
}

// TestSchedulerRespectsDisabledKinds: a landlord who has switched a kind off
// stops receiving it.
func TestSchedulerRespectsDisabledKinds(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Disabled", "0715006400", "+255715006401")
	h.parkSchedules(t, fix.contractID)
	h.setDueDate(t, fix.scheduleIDs[0], schedulerToday(), "pending")
	h.setDueDate(t, fix.scheduleIDs[1], schedulerToday().AddDate(0, 0, -3), "overdue")

	fix.owner.do(http.MethodPut, "/org/notification-settings", map[string]any{
		"kinds": map[string]any{
			"overdue_daily":     map[string]any{"enabled": false},
			"reminder_7d":       map[string]any{"enabled": false},
			"unsigned_reminder": map[string]any{"enabled": false},
		},
	}).mustStatus(t, http.StatusOK, "disable kinds")

	res := h.runScheduler(t, notify.Options{ForceHour: true})
	if res.Queued[notify.KindOverdueDaily] != 0 {
		t.Errorf("overdue_daily fired while disabled: %v", res.Queued)
	}
	if res.Queued[notify.KindReminderDue] != 1 {
		t.Errorf("reminder_due queued %v, want 1 — it is still enabled", res.Queued)
	}
}

// TestSchedulerOffsetIsTheOrgs: reminder_7d is a name, not a fixed seven days.
func TestSchedulerCustomOffset(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Offset", "0715006500", "+255715006501")
	h.parkSchedules(t, fix.contractID)
	h.setDueDate(t, fix.scheduleIDs[0], schedulerToday().AddDate(0, 0, 3), "pending")

	if res := h.runScheduler(t, notify.Options{ForceHour: true}); res.Queued[notify.KindReminder7d] != 0 {
		t.Fatalf("a schedule due in 3 days fired the 7-day reminder: %v", res.Queued)
	}
	fix.owner.do(http.MethodPut, "/org/notification-settings", map[string]any{
		"kinds": map[string]any{"reminder_7d": map[string]any{"offset_days": 3}},
	}).mustStatus(t, http.StatusOK, "set offset")

	if res := h.runScheduler(t, notify.Options{ForceHour: true}); res.Queued[notify.KindReminder7d] != 1 {
		t.Errorf("with offset_days 3 the sweep queued %v, want one reminder_7d", res.Queued)
	}
}

// TestAdminNotificationsJob is the on-demand sweep behind the platform-admin
// session, and it is audited like every other job.
func TestAdminNotificationsJob(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Job", "0715006600", "+255715006601")
	h.parkSchedules(t, fix.contractID)
	h.setDueDate(t, fix.scheduleIDs[0], schedulerToday(), "pending")

	admin := h.adminClient(t)
	res := admin.do(http.MethodPost, "/admin/jobs/notifications", map[string]any{"force_hour": true}).
		mustStatus(t, http.StatusOK, "run job")

	queued, ok := res.Body["queued"].(map[string]any)
	if !ok {
		t.Fatalf("no queued object — body: %s", res.Raw)
	}
	if queued[notify.KindReminderDue] != float64(1) {
		t.Errorf("queued = %v, want one reminder_due", queued)
	}
	if len(h.auditPayloads(t, "notification.scheduler_run")) != 1 {
		t.Error("the job wrote no notification.scheduler_run audit row")
	}

	// The org's own session cannot run a platform-wide job.
	fix.owner.do(http.MethodPost, "/admin/jobs/notifications", map[string]any{"force_hour": true}).
		mustStatus(t, http.StatusUnauthorized, "org running the admin job")
}

// -------------------------------------------------------- custom bulk SMS --

// TestCustomSMSToAllActive is FLOWS 8's water-outage notice: one message, one
// row per renter with a running contract, variables resolved per person.
func TestCustomSMSToAllActive(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Bulk", "0715006700", "+255715006701")

	// A second renter of the same org, whose contract is only pending: they
	// are not "active", so they are not in the broadcast.
	pending := h.newContractFixture(t, "Pending", "0715006710", "+255715006711")
	_ = pending

	res := fix.owner.do(http.MethodPost, "/notifications/custom", map[string]any{
		"recipients": "all_active",
		"body":       "Hello {{name}}, water maintenance at {{property}} on Sunday 9am.",
	}).mustStatus(t, http.StatusAccepted, "custom send")

	if got := num(t, res, "queued"); got != 1 {
		t.Errorf("queued = %v, want 1 (only the active contract)", got)
	}
	notes := ofKind(h.notifications(t), "custom")
	if len(notes) != 1 {
		t.Fatalf("custom notifications = %d, want 1", len(notes))
	}
	if !strings.HasPrefix(notes[0].Body, "Hello Bulk Renter, water maintenance at Bulk Block A on Sunday 9am.") {
		t.Errorf("body = %q, want the variables resolved for this renter", notes[0].Body)
	}
	if notes[0].ToPhone != "+255715006701" {
		t.Errorf("to_phone = %q, want the renter's number", notes[0].ToPhone)
	}
	batchID := res.str(t, "batch_id")
	if !strings.HasPrefix(notes[0].DedupeKey, "custom:"+batchID+":") {
		t.Errorf("dedupe_key = %q, want custom:{batch_id}:{user_id}", notes[0].DedupeKey)
	}

	// The trail records what was sent, and to how many (SPEC §8).
	payloads := h.auditPayloads(t, "notification.custom")
	if len(payloads) != 1 {
		t.Fatalf("notification.custom audit rows = %d, want 1", len(payloads))
	}
	if !strings.Contains(payloads[0], "water maintenance") {
		t.Errorf("audit payload %q does not record the message body", payloads[0])
	}
}

// TestCustomSMSSelectedSkipsStrangers: an id the org has no relationship with
// is counted as skipped, never sent to and never confirmed to exist.
func TestCustomSMSSelectedSkipsStrangers(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Selected", "0715006800", "+255715006801")
	other := h.newPaymentFixture(t, "Stranger", "0715006810", "+255715006811")

	res := fix.owner.do(http.MethodPost, "/notifications/custom", map[string]any{
		"recipients":      "selected",
		"renter_user_ids": []string{fix.renterID, other.renterID},
		"body":            "Hello {{name}}, the lift is out today.",
	}).mustStatus(t, http.StatusAccepted, "custom send")

	if got := num(t, res, "queued"); got != 1 {
		t.Errorf("queued = %v, want 1 — only this org's renter", got)
	}
	if got := num(t, res, "skipped"); got != 1 {
		t.Errorf("skipped = %v, want 1 — the other org's renter", got)
	}
	for _, n := range ofKind(h.notifications(t), "custom") {
		if n.ToPhone == "+255715006811" {
			t.Fatal("a broadcast reached a renter of another org")
		}
	}
}

// TestCustomSMSValidation covers the field bounds and the narrower variable
// whitelist a broadcast gets.
func TestCustomSMSValidation(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "CustomV", "0715006900", "+255715006901")

	cases := []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"no recipients selector", map[string]any{"body": "hi"}, "recipients"},
		{"empty body", map[string]any{"recipients": "all_active", "body": "   "}, "body"},
		{"body too long", map[string]any{
			"recipients": "all_active", "body": strings.Repeat("a", 321),
		}, "body"},
		{"variable outside the custom whitelist", map[string]any{
			"recipients": "all_active", "body": "You owe {{amount}}",
		}, "body"},
		{"selected with no ids", map[string]any{"recipients": "selected", "body": "hi"}, "renter_user_ids"},
		{"ids on all_active", map[string]any{
			"recipients": "all_active", "body": "hi", "renter_user_ids": []string{fix.renterID},
		}, "renter_user_ids"},
		{"malformed id", map[string]any{
			"recipients": "selected", "body": "hi", "renter_user_ids": []string{"not-a-uuid"},
		}, "renter_user_ids"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := fix.owner.do(http.MethodPost, "/notifications/custom", tc.body).
				mustStatus(t, http.StatusBadRequest, "custom send")
			errs, _ := resp.Body["errors"].(map[string]any)
			if _, named := errs[tc.field]; !named {
				t.Errorf("errors = %v, want one for %q", errs, tc.field)
			}
		})
	}
}

// TestCustomSMSRateLimit: ten batches an hour per org, then a 429.
func TestCustomSMSRateLimit(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Limit", "0715007000", "+255715007001")

	for i := 0; i < 10; i++ {
		fix.owner.do(http.MethodPost, "/notifications/custom", map[string]any{
			"recipients": "all_active", "body": "notice",
		}).mustStatus(t, http.StatusAccepted, "batch within the limit")
	}
	fix.owner.do(http.MethodPost, "/notifications/custom", map[string]any{
		"recipients": "all_active", "body": "one too many",
	}).mustStatus(t, http.StatusTooManyRequests, "eleventh batch")
}

// ------------------------------------------------------------------ log --

// TestNotificationLogAndRetry: the landlord reads their sends, filters them,
// and can put a failed one back on the queue — but only a failed one.
func TestNotificationLogAndRetry(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Log", "0715007100", "+255715007101")
	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": fix.amounts[0], "method": "cash",
	}).mustStatus(t, http.StatusCreated, "record payment")

	log := fix.owner.do(http.MethodGet, "/notifications/log", nil).
		mustStatus(t, http.StatusOK, "read log")
	items := listOf(t, log)
	if len(items) == 0 {
		t.Fatal("the log is empty after a contract and a payment")
	}
	// Full number and the renter's name (API.md: landlords see both).
	found := false
	for _, item := range items {
		if item["kind"] == "thank_you" {
			found = true
			if item["to_phone"] != "+255715007101" {
				t.Errorf("to_phone = %v, want the full number", item["to_phone"])
			}
			if item["renter_name"] != "Log Renter" {
				t.Errorf("renter_name = %v, want Log Renter", item["renter_name"])
			}
		}
	}
	if !found {
		t.Error("the log holds no thank_you row")
	}

	// Filtering by kind narrows the list to that kind alone.
	filtered := listOf(t, fix.owner.do(http.MethodGet, "/notifications/log?kind=thank_you", nil).
		mustStatus(t, http.StatusOK, "filter by kind"))
	for _, item := range filtered {
		if item["kind"] != "thank_you" {
			t.Errorf("kind filter returned a %v row", item["kind"])
		}
	}

	// A queued row is not retryable — it is already on its way.
	queuedID, _ := items[0]["id"].(string)
	fix.owner.do(http.MethodPost, "/notifications/log/"+queuedID+"/retry", nil).
		mustStatus(t, http.StatusConflict, "retry a queued row")

	// Mark it failed the way the worker would, and it retries.
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE notification_log SET status = 'failed', attempts = 3, error = 'provider unavailable' WHERE id = $1`,
		queuedID); err != nil {
		t.Fatalf("fail the row: %v", err)
	}
	retried := fix.owner.do(http.MethodPost, "/notifications/log/"+queuedID+"/retry", nil).
		mustStatus(t, http.StatusAccepted, "retry a failed row")
	if got := retried.str(t, "status"); got != "queued" {
		t.Errorf("status after a retry = %q, want queued", got)
	}
	if len(h.auditPayloads(t, "notification.retry")) != 1 {
		t.Error("a retry wrote no notification.retry audit row")
	}
}

// TestNotificationLogFilterValidation rejects filters the log cannot answer.
func TestNotificationLogFilterValidation(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("LogV", "logv@jjne.test", "0715007200", []string{"Room 1"}, testUnitAmount)

	for _, q := range []struct{ query, field string }{
		{"?status=exploded", "status"},
		{"?kind=nonsense", "kind"},
		{"?user_id=not-a-uuid", "user_id"},
		{"?from=yesterday", "from"},
		{"?limit=0", "limit"},
		{"?cursor=not-a-cursor", "cursor"},
	} {
		resp := fix.client.do(http.MethodGet, "/notifications/log"+q.query, nil).
			mustStatus(t, http.StatusBadRequest, "filter "+q.query)
		errs, _ := resp.Body["errors"].(map[string]any)
		if _, named := errs[q.field]; !named {
			t.Errorf("%s: errors = %v, want one for %q", q.query, errs, q.field)
		}
	}
}

// ------------------------------------------------------------ isolation --

// TestNotificationIsolation is the SPEC §8 promise on the Phase 6 surface: org
// B can neither read nor retry org A's messages, and a direct id is a 404
// rather than a 403 that would confirm the row exists.
func TestNotificationIsolation(t *testing.T) {
	h := newHarness(t)
	a := h.newPaymentFixture(t, "OrgA", "0715007300", "+255715007301")
	b := h.newPaymentFixture(t, "OrgB", "0715007310", "+255715007311")

	a.owner.recordPayment(map[string]any{
		"contract_id": a.contractID, "amount": a.amounts[0], "method": "cash",
	}).mustStatus(t, http.StatusCreated, "org A payment")

	// Org B's log holds none of org A's rows.
	for _, item := range listOf(t, b.owner.do(http.MethodGet, "/notifications/log", nil).
		mustStatus(t, http.StatusOK, "org B log")) {
		if item["to_phone"] == "+255715007301" {
			t.Fatal("org B can read a message org A sent")
		}
	}

	// A row of org A, addressed directly by org B, is a 404 — before and after
	// it has failed, so the answer never depends on the row's state.
	var rowID string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT id::text FROM notification_log WHERE to_phone = $1 LIMIT 1`,
		"+255715007301").Scan(&rowID); err != nil {
		t.Fatalf("find org A row: %v", err)
	}
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE notification_log SET status = 'failed' WHERE id = $1`, rowID); err != nil {
		t.Fatalf("fail org A row: %v", err)
	}
	b.owner.do(http.MethodPost, "/notifications/log/"+rowID+"/retry", nil).
		mustStatus(t, http.StatusNotFound, "org B retrying org A's row")

	// Its own owner can still retry it, so the 404 is scope and not state.
	a.owner.do(http.MethodPost, "/notifications/log/"+rowID+"/retry", nil).
		mustStatus(t, http.StatusAccepted, "org A retrying its own row")
}

// TestNotificationSettingsIsolation: each org's settings are its own.
func TestNotificationSettingsIsolation(t *testing.T) {
	h := newHarness(t)
	a := h.newOrgWithUnits("SetA", "seta@jjne.test", "0715007400", []string{"Room 1"}, testUnitAmount)
	b := h.newOrgWithUnits("SetB", "setb@jjne.test", "0715007410", []string{"Room 1"}, testUnitAmount)

	a.client.do(http.MethodPut, "/org/notification-settings", map[string]any{
		"sender_name": "ORGA", "send_hour_local": 6,
	}).mustStatus(t, http.StatusOK, "org A settings")

	got := b.client.do(http.MethodGet, "/org/notification-settings", nil).
		mustStatus(t, http.StatusOK, "org B settings")
	if got.Body["sender_name"] != nil {
		t.Errorf("org B sender_name = %v, want null", got.Body["sender_name"])
	}
	if hour := num(t, got, "send_hour_local"); hour != 9 {
		t.Errorf("org B send_hour_local = %v, want the default 9", hour)
	}
}

// TestSchedulerKeepsOrgsApart: two orgs, two timelines, and neither org's
// renter hears about the other's schedule.
func TestSchedulerKeepsOrgsApart(t *testing.T) {
	h := newHarness(t)
	a := h.newPaymentFixture(t, "SchedA", "0715007500", "+255715007501")
	b := h.newPaymentFixture(t, "SchedB", "0715007510", "+255715007511")
	h.parkSchedules(t, a.contractID)
	h.parkSchedules(t, b.contractID)
	h.setDueDate(t, a.scheduleIDs[0], schedulerToday(), "pending")

	// Org B switches the kind off; org A's reminder must still go out.
	b.owner.do(http.MethodPut, "/org/notification-settings", map[string]any{
		"kinds": map[string]any{"reminder_due": map[string]any{"enabled": false}},
	}).mustStatus(t, http.StatusOK, "org B disables reminders")

	res := h.runScheduler(t, notify.Options{ForceHour: true})
	if res.Queued[notify.KindReminderDue] != 1 {
		t.Fatalf("sweep queued %v, want exactly one reminder_due", res.Queued)
	}
	notes := ofKind(h.notifications(t), notify.KindReminderDue)
	if len(notes) != 1 || notes[0].ToPhone != "+255715007501" {
		t.Errorf("reminder went to %+v, want org A's renter only", notes)
	}
}

// TestSchedulerSkipsSuspendedOrgs: a suspended tenant sends nothing. The org is
// off the platform (SPEC §5.10), and a scheduler that kept texting its renters
// would be the one part of the product that had not noticed.
func TestSchedulerSkipsSuspendedOrgs(t *testing.T) {
	h := newHarness(t)
	live := h.newPaymentFixture(t, "Live", "0715007600", "+255715007601")
	dead := h.newPaymentFixture(t, "Dead", "0715007610", "+255715007611")
	h.parkSchedules(t, live.contractID)
	h.parkSchedules(t, dead.contractID)
	h.setDueDate(t, live.scheduleIDs[0], schedulerToday(), "pending")
	h.setDueDate(t, dead.scheduleIDs[0], schedulerToday(), "pending")

	if _, err := h.pool.Exec(context.Background(),
		`UPDATE orgs SET status = 'suspended' WHERE id = $1`, dead.orgID); err != nil {
		t.Fatalf("suspend org: %v", err)
	}

	res := h.runScheduler(t, notify.Options{ForceHour: true})
	if res.Queued[notify.KindReminderDue] != 1 {
		t.Fatalf("sweep queued %v, want exactly one reminder_due", res.Queued)
	}
	for _, n := range ofKind(h.notifications(t), notify.KindReminderDue) {
		if n.ToPhone == "+255715007611" {
			t.Error("a suspended org's renter was texted")
		}
	}
}

// TestOverdueDailyStopsOncePaid: the daily chase is "until paid or waived"
// (SPEC §6) — recording the money is what ends it, with no separate switch to
// remember to turn off.
func TestOverdueDailyStopsOncePaid(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Chase", "0715007700", "+255715007701")
	h.parkSchedules(t, fix.contractID)
	h.setDueDate(t, fix.scheduleIDs[0], schedulerToday().AddDate(0, 0, -5), "overdue")

	if res := h.runScheduler(t, notify.Options{ForceHour: true}); res.Queued[notify.KindOverdueDaily] != 1 {
		t.Fatalf("sweep queued %v, want one overdue_daily", res.Queued)
	}

	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "schedule_id": fix.scheduleIDs[0],
		"amount": fix.amounts[0], "method": "cash",
	}).mustStatus(t, http.StatusCreated, "settle the overdue instalment")

	// Tomorrow's key is a fresh one, so nothing here is the dedupe key's doing.
	tomorrow := time.Now().UTC().AddDate(0, 0, 1)
	if res := h.runScheduler(t, notify.Options{ForceHour: true, Date: tomorrow}); res.Queued[notify.KindOverdueDaily] != 0 {
		t.Errorf("the chase continued after payment: %v", res.Queued)
	}
}

// TestCustomSMSRejectsControlCharacters: the body travels verbatim to the
// provider, so what may not be in an SMS may not be in the request either.
func TestCustomSMSRejectsControlCharacters(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Ctrl", "0715007800", "+255715007801")

	fix.owner.do(http.MethodPost, "/notifications/custom", map[string]any{
		"recipients": "all_active", "body": "Water off\x00 on Sunday",
	}).mustStatus(t, http.StatusBadRequest, "control character in a broadcast")

	fix.owner.do(http.MethodPut, "/org/notification-settings", map[string]any{
		"sender_name": "JJnE Ltd!",
	}).mustStatus(t, http.StatusBadRequest, "non-alphanumeric sender id")
}

// TestThankYouToggleIsHonoured: `thank_you` is event-driven (Phase 5), but the
// switch on the notifications screen is the same switch — turning it off has to
// stop the SMS, not just change the screen.
func TestThankYouToggleIsHonoured(t *testing.T) {
	h := newHarness(t)
	fix := h.newPaymentFixture(t, "Thanks", "0715007900", "+255715007901")

	fix.owner.do(http.MethodPut, "/org/notification-settings", map[string]any{
		"kinds": map[string]any{"thank_you": map[string]any{"enabled": false}},
	}).mustStatus(t, http.StatusOK, "disable thank_you")

	fix.owner.recordPayment(map[string]any{
		"contract_id": fix.contractID, "amount": fix.amounts[0], "method": "cash",
	}).mustStatus(t, http.StatusCreated, "record payment")

	if got := ofKind(h.notifications(t), notify.KindThankYou); len(got) != 0 {
		t.Errorf("a thank-you went out with the kind disabled: %+v", got)
	}
}
