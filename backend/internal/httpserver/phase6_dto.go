package httpserver

import (
	"strings"
	"time"

	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/notify"
	"tms/backend/internal/validate"
)

// Bounds on the notification settings (API.md Phase 6).
const (
	senderNameMax     = 11 // GSM alphanumeric sender IDs are 11 characters
	sendHourMin       = 0
	sendHourMax       = 23
	reminderOffsetMin = 0
	reminderOffsetMax = 30
	customBodyMax     = notify.BodyMaxLen
	customRecipentMax = 500
)

// Custom-send rate limit: ten broadcasts an hour per org (API.md). It is a
// per-org limit rather than per-user because the cost is the org's.
const (
	customBatchLimit  = 10
	customBatchWindow = time.Hour
)

// Recipient selectors of POST /notifications/custom.
const (
	recipientsAllActive = "all_active"
	recipientsSelected  = "selected"
)

// notificationLogLimits page GET /notifications/log.
const (
	notifyLogDefaultLimit = 50
	notifyLogMaxLimit     = 200
)

// ------------------------------------------------------------- settings --

// kindToggle is a kind that is simply on or off.
type kindToggle struct {
	Enabled bool `json:"enabled"`
}

// kindWithOffset is `reminder_7d`: how many days ahead of the due date the
// first nudge goes out. The name keeps the API.md kind even when the offset is
// not seven — it is the kind's identity, not a description of its timing.
type kindWithOffset struct {
	Enabled    bool `json:"enabled"`
	OffsetDays int  `json:"offset_days"`
}

// kindWithAfter is `unsigned_reminder`: how long a contract may wait for the
// renter's signature before they are nudged.
type kindWithAfter struct {
	Enabled   bool `json:"enabled"`
	AfterDays int  `json:"after_days"`
}

// notificationKinds is the per-kind configuration block.
type notificationKinds struct {
	Reminder7d       kindWithOffset `json:"reminder_7d"`
	ReminderDue      kindToggle     `json:"reminder_due"`
	OverdueDaily     kindToggle     `json:"overdue_daily"`
	ThankYou         kindToggle     `json:"thank_you"`
	UnsignedReminder kindWithAfter  `json:"unsigned_reminder"`
}

// NotificationSettings is what is stored under `orgs.settings.notifications`.
//
// `language` is deliberately absent from the stored shape: the org already has
// one SMS language (`orgs.settings.sms_language`, Phase 1), and a second copy
// would be a second place for it to be wrong. The endpoint surfaces and writes
// that one field under the name API.md gives it.
type NotificationSettings struct {
	SenderName *string                     `json:"sender_name"`
	SendHour   int                         `json:"send_hour_local"`
	Kinds      notificationKinds           `json:"kinds"`
	Templates  map[string]*notify.Template `json:"templates"`
}

// DefaultNotificationSettings is what an org that has never opened the
// notifications screen behaves as. The two day counts come from the Phase 1
// org settings, so an org that set `unsigned_reminder_days` there keeps it.
func DefaultNotificationSettings(base OrgSettings) NotificationSettings {
	offset := notify.DefaultReminderOffset
	if len(base.ReminderOffsetsDays) > 0 && base.ReminderOffsetsDays[0] >= reminderOffsetMin {
		offset = base.ReminderOffsetsDays[0]
	}
	after := base.UnsignedReminderDays
	if after < 0 {
		after = notify.DefaultUnsignedAfterDay
	}
	return NotificationSettings{
		SenderName: nil,
		SendHour:   notify.DefaultSendHourLocal,
		Kinds: notificationKinds{
			Reminder7d:       kindWithOffset{Enabled: true, OffsetDays: offset},
			ReminderDue:      kindToggle{Enabled: true},
			OverdueDaily:     kindToggle{Enabled: true},
			ThankYou:         kindToggle{Enabled: true},
			UnsignedReminder: kindWithAfter{Enabled: true, AfterDays: after},
		},
		Templates: map[string]*notify.Template{},
	}
}

// notificationSettingsOf resolves an org's effective settings: what is stored,
// or the defaults when nothing has been written yet.
func notificationSettingsOf(base OrgSettings) NotificationSettings {
	out := DefaultNotificationSettings(base)
	if base.Notifications == nil {
		return out
	}
	stored := *base.Notifications
	out.SenderName = stored.SenderName
	if stored.SendHour >= sendHourMin && stored.SendHour <= sendHourMax {
		out.SendHour = stored.SendHour
	}
	out.Kinds = stored.Kinds
	if stored.Templates != nil {
		out.Templates = stored.Templates
	}
	return out
}

// notificationSettingsResponse is the wire shape of GET/PUT
// /org/notification-settings. `templates` always names every overridable kind,
// with null where the platform default applies, so a client can render the
// screen without knowing the catalogue.
type notificationSettingsResponse struct {
	SenderName *string                     `json:"sender_name"`
	Language   string                      `json:"language"`
	SendHour   int                         `json:"send_hour_local"`
	Kinds      notificationKinds           `json:"kinds"`
	Templates  map[string]*notify.Template `json:"templates"`
}

func toNotificationSettings(base OrgSettings) notificationSettingsResponse {
	s := notificationSettingsOf(base)
	templates := make(map[string]*notify.Template, len(notify.TemplateKinds()))
	for _, kind := range notify.TemplateKinds() {
		templates[kind] = s.Templates[kind]
	}
	return notificationSettingsResponse{
		SenderName: s.SenderName,
		Language:   base.SMSLanguage,
		SendHour:   s.SendHour,
		Kinds:      s.Kinds,
		Templates:  templates,
	}
}

// Overrides renders the org's templates in the form notify.Render consumes.
func (s NotificationSettings) Overrides() notify.Overrides {
	if len(s.Templates) == 0 {
		return nil
	}
	out := make(notify.Overrides, len(s.Templates))
	for kind, tpl := range s.Templates {
		out[kind] = tpl
	}
	return out
}

// notifyOverrides is the shorthand every queueing handler uses.
func (s OrgSettings) notifyOverrides() notify.Overrides {
	return notificationSettingsOf(s).Overrides()
}

// senderNameOf resolves the sender ID a live send would use for this org: its
// own approved name, or "" to let the provider fall back to the platform's.
func (s OrgSettings) senderNameOf() string {
	return db.StrVal(notificationSettingsOf(s).SenderName)
}

// ---------------------------------------------------------- patch + apply --

// notificationSettingsPatch is the PUT body. Every field is a pointer so an
// absent key is a partial merge rather than a reset (API.md: "partial merge").
type notificationSettingsPatch struct {
	SenderName *string                     `json:"sender_name"`
	Language   *string                     `json:"language"`
	SendHour   *int                        `json:"send_hour_local"`
	Kinds      *notificationKindsPatch     `json:"kinds"`
	Templates  map[string]*notify.Template `json:"templates"`
}

type kindTogglePatch struct {
	Enabled *bool `json:"enabled"`
}

type kindWithOffsetPatch struct {
	Enabled    *bool `json:"enabled"`
	OffsetDays *int  `json:"offset_days"`
}

type kindWithAfterPatch struct {
	Enabled   *bool `json:"enabled"`
	AfterDays *int  `json:"after_days"`
}

type notificationKindsPatch struct {
	Reminder7d       *kindWithOffsetPatch `json:"reminder_7d"`
	ReminderDue      *kindTogglePatch     `json:"reminder_due"`
	OverdueDaily     *kindTogglePatch     `json:"overdue_daily"`
	ThankYou         *kindTogglePatch     `json:"thank_you"`
	UnsignedReminder *kindWithAfterPatch  `json:"unsigned_reminder"`
}

// applyNotificationPatch merges the request onto the org's settings, recording
// every validation failure in f. It returns the whole OrgSettings because
// `language` lives beside the notifications block rather than inside it.
func applyNotificationPatch(cur OrgSettings, p notificationSettingsPatch, f validate.Fields) OrgSettings {
	ns := notificationSettingsOf(cur)

	if p.SenderName != nil {
		name := strings.TrimSpace(*p.SenderName)
		switch {
		case name == "":
			ns.SenderName = nil // back to the platform default
		case len([]rune(name)) > senderNameMax:
			f.Add("sender_name", "must be at most 11 characters")
		default:
			ns.SenderName = &name
		}
	}
	if p.Language != nil {
		cur.SMSLanguage = f.OneOf("language", strings.TrimSpace(*p.Language),
			notify.LangSwahili, notify.LangEnglish)
	}
	if p.SendHour != nil {
		if *p.SendHour < sendHourMin || *p.SendHour > sendHourMax {
			f.Add("send_hour_local", "must be between 0 and 23")
		} else {
			ns.SendHour = *p.SendHour
		}
	}
	if p.Kinds != nil {
		applyKindsPatch(&ns.Kinds, *p.Kinds, f)
	}
	if p.Templates != nil {
		applyTemplatesPatch(&ns, p.Templates, f)
	}

	cur.Notifications = &ns
	return cur
}

func applyKindsPatch(k *notificationKinds, p notificationKindsPatch, f validate.Fields) {
	if p.Reminder7d != nil {
		if p.Reminder7d.Enabled != nil {
			k.Reminder7d.Enabled = *p.Reminder7d.Enabled
		}
		if p.Reminder7d.OffsetDays != nil {
			if *p.Reminder7d.OffsetDays < reminderOffsetMin || *p.Reminder7d.OffsetDays > reminderOffsetMax {
				f.Add("kinds.reminder_7d.offset_days", "must be between 0 and 30")
			} else {
				k.Reminder7d.OffsetDays = *p.Reminder7d.OffsetDays
			}
		}
	}
	if p.ReminderDue != nil && p.ReminderDue.Enabled != nil {
		k.ReminderDue.Enabled = *p.ReminderDue.Enabled
	}
	if p.OverdueDaily != nil && p.OverdueDaily.Enabled != nil {
		k.OverdueDaily.Enabled = *p.OverdueDaily.Enabled
	}
	if p.ThankYou != nil && p.ThankYou.Enabled != nil {
		k.ThankYou.Enabled = *p.ThankYou.Enabled
	}
	if p.UnsignedReminder != nil {
		if p.UnsignedReminder.Enabled != nil {
			k.UnsignedReminder.Enabled = *p.UnsignedReminder.Enabled
		}
		if p.UnsignedReminder.AfterDays != nil {
			if *p.UnsignedReminder.AfterDays < reminderOffsetMin || *p.UnsignedReminder.AfterDays > reminderOffsetMax {
				f.Add("kinds.unsigned_reminder.after_days", "must be between 0 and 30")
			} else {
				k.UnsignedReminder.AfterDays = *p.UnsignedReminder.AfterDays
			}
		}
	}
}

// applyTemplatesPatch merges template overrides. A null value clears the
// override (back to the platform default); a body naming a variable outside
// the whitelist is a 400, because an SMS reading "Hello {{nmae}}" is worse than
// a rejected save.
func applyTemplatesPatch(ns *NotificationSettings, in map[string]*notify.Template, f validate.Fields) {
	if ns.Templates == nil {
		ns.Templates = map[string]*notify.Template{}
	}
	for kind, tpl := range in {
		field := "templates." + kind
		if !notify.KnownKind(kind) || kind == notify.KindCustom {
			f.Add(field, "unknown notification kind")
			continue
		}
		if tpl == nil {
			delete(ns.Templates, kind)
			continue
		}
		clean := notify.Template{SW: strings.TrimSpace(tpl.SW), EN: strings.TrimSpace(tpl.EN)}
		checkTemplateBody(f, field+".sw", clean.SW)
		checkTemplateBody(f, field+".en", clean.EN)
		if clean.SW == "" && clean.EN == "" {
			delete(ns.Templates, kind)
			continue
		}
		stored := clean
		ns.Templates[kind] = &stored
	}
}

func checkTemplateBody(f validate.Fields, field, body string) {
	if body == "" {
		return
	}
	if len([]rune(body)) > notify.BodyMaxLen {
		f.Add(field, "must be at most 320 characters")
	}
	if unknown := notify.UnknownVariables(body, notify.OrgVariables); len(unknown) > 0 {
		f.Add(field, "unknown variables: {{"+strings.Join(unknown, "}}, {{")+"}}")
	}
}

// -------------------------------------------------------- scheduler view --

// SchedulerSettings renders an org's stored settings in the shape the
// scheduler reads. It is the seam that keeps internal/notify free of the HTTP
// layer's DTOs: the sweep is handed a parser, not a package dependency, and
// cmd/api hands it the same one the on-demand admin job uses.
func SchedulerSettings(raw []byte) notify.SchedulerSettings {
	base := parseSettings(raw)
	ns := notificationSettingsOf(base)
	return notify.SchedulerSettings{
		Language:       base.SMSLanguage,
		SendHourLocal:  ns.SendHour,
		Overrides:      ns.Overrides(),
		Reminder7d:     ns.Kinds.Reminder7d.Enabled,
		ReminderOffset: ns.Kinds.Reminder7d.OffsetDays,
		ReminderDue:    ns.Kinds.ReminderDue.Enabled,
		OverdueDaily:   ns.Kinds.OverdueDaily.Enabled,
		Unsigned:       ns.Kinds.UnsignedReminder.Enabled,
		UnsignedAfter:  ns.Kinds.UnsignedReminder.AfterDays,
	}
}

// ------------------------------------------------------------------- log --

// notificationLogItem is one row of GET /notifications/log.
//
// `to_phone` is the full number: the landlord is the party who gave the renter
// their tenancy and already has it on the renter card, so masking it here
// would hide a delivery problem without hiding anything they cannot see.
type notificationLogItem struct {
	ID            string     `json:"id"`
	Kind          string     `json:"kind"`
	ToPhone       string     `json:"to_phone"`
	RenterName    string     `json:"renter_name"`
	Body          string     `json:"body"`
	Status        string     `json:"status"`
	ProviderMsgID *string    `json:"provider_msg_id"`
	Error         *string    `json:"error"`
	Attempts      int        `json:"attempts"`
	BatchID       *string    `json:"batch_id"`
	CreatedAt     time.Time  `json:"created_at"`
	SentAt        *time.Time `json:"sent_at"`
}

func toNotificationLogItem(r sqlc.ListOrgNotificationsRow) notificationLogItem {
	item := notificationLogItem{
		ID:            db.UUIDString(r.ID),
		Kind:          r.Kind,
		ToPhone:       r.ToPhone,
		RenterName:    r.RenterName,
		Body:          r.Body,
		Status:        r.Status,
		ProviderMsgID: r.ProviderMsgID,
		Error:         r.Error,
		Attempts:      int(r.Attempts),
		CreatedAt:     r.CreatedAt.Time,
	}
	if r.BatchID.Valid {
		id := db.UUIDString(r.BatchID)
		item.BatchID = &id
	}
	if r.SentAt.Valid {
		at := r.SentAt.Time
		item.SentAt = &at
	}
	return item
}

// notificationStatuses are the values `GET /notifications/log?status=` accepts.
//
//nolint:gochecknoglobals // fixed value set, read-only.
var notificationStatuses = map[string]bool{
	"queued": true, "sending": true, "sent": true, "failed": true,
}
