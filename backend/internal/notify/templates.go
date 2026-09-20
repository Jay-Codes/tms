package notify

import (
	"sort"
	"strings"
)

// Notification kinds. Every message written to notification_log carries one of
// these, and every one of them renders through Render (SPEC §6, API.md Phase 6).
const (
	KindLinkApproved       = "link_approved"
	KindLinkRejected       = "link_rejected"
	KindContractReady      = "contract_ready"
	KindWelcome            = "welcome"
	KindContractTerminated = "contract_terminated"
	KindThankYou           = "thank_you"
	KindReminder7d         = "reminder_7d"
	KindReminderDue        = "reminder_due"
	KindOverdueDaily       = "overdue_daily"
	KindUnsignedReminder   = "unsigned_reminder"
	// KindProofRejected tells a renter their proof of payment was not
	// accepted, and why (PLAN2 §16.1). Acceptance raises no message of its
	// own: the allocator's existing `thank_you` is the acceptance.
	KindProofRejected = "proof_rejected"
	KindCustom        = "custom"
	KindOTP           = "otp"
)

// Languages a message may be written in. Every user carries one
// (`users.locale`, SPEC §3.2); `orgs.settings.sms_language` survives as the
// fallback for renters who have expressed no preference. Swahili is the
// default for the first client's renters.
const (
	LangSwahili = "sw"
	LangEnglish = "en"
)

// LanguageFor resolves the language one SMS is written in (SPEC §6, PLAN2
// Phase 13): the recipient's own locale, then the org's default for renters
// without a preference, then Swahili.
//
// It is total by design — every argument may be empty or nonsense (a user row
// that predates `users.locale`, an org whose settings blob has never been
// written) and a message still has to go out in a language somebody reads.
func LanguageFor(userLocale, orgLanguage string) string {
	switch userLocale {
	case LangSwahili, LangEnglish:
		return userLocale
	}
	switch orgLanguage {
	case LangSwahili, LangEnglish:
		return orgLanguage
	}
	return LangSwahili
}

// BodyMaxLen is the longest SMS body the product accepts, for a platform
// template, an org override or a landlord's custom message alike (API.md).
const BodyMaxLen = 320

// RenderedMaxLen bounds a body after substitution. A template is capped at
// BodyMaxLen, but its variables are not: a renter name, a unit name or an org
// display name is data, and eight of them expanded into one 320-character
// template is unbounded growth on the send path. Concatenated SMS is fine —
// this is only a ceiling, well above any real message.
const RenderedMaxLen = 1000

// thankYouSettled is an internal template key, never a notification_log kind:
// both wordings are logged as `thank_you`. It is selected when there is no next
// instalment to name, so the message says everything is up to date rather than
// leaving a blank date in the SMS.
const thankYouSettled = "thank_you_settled"

// Vars are the substitutions a template may name.
//
// The first eight are the variables an org may use in its own templates
// (API.md Phase 6: `{{name}} {{amount}} {{due_date}} {{property}} {{unit}}
// {{org}} {{next_due_date}} {{link}}`). The last three are platform-only: they
// carry facts that belong to one kind each — why a tenancy ended, when it
// starts, what the next instalment costs — and the validator does not offer
// them to org overrides.
type Vars struct {
	Name        string // the renter's name
	Amount      string // formatted money, e.g. "TZS 250,000"
	DueDate     string // YYYY-MM-DD
	Property    string
	Unit        string
	Org         string // the org's display name (branding, falling back to its name)
	NextDueDate string // YYYY-MM-DD, empty when everything is settled
	Link        string // an absolute link into the enduser app
	PayLink     string // the renter's Payments tab: {enduser base}/payments

	Reason     string // termination / rejection only
	StartDate  string // welcome only
	NextAmount string // thank-you only
	Code       string // OTP only: the six-digit verification code

	// Body is the landlord's own text for a `custom` broadcast. It is not a
	// placeholder: it IS the template, so `{{name}}` inside it still resolves
	// per renter (API.md: POST /notifications/custom).
	Body string
}

// OrgVariables are the placeholders an org's own template may use (API.md).
// Anything else in a submitted template is a 400.
//
//nolint:gochecknoglobals // fixed contract, read-only.
var OrgVariables = []string{
	"name", "amount", "due_date", "property", "unit", "org", "next_due_date", "link",
	// Phase 16 §16.4: the deep link to the renter's Payments tab, where the
	// pinned "How to pay" card and the proof sheet live. A reminder that says
	// money is owed and not where to send it is the complaint this variable
	// answers.
	"pay_link",
}

// CustomVariables are the placeholders a landlord's bulk message may use
// (API.md: `POST /notifications/custom`). It is a subset of OrgVariables:
// a broadcast is not about one schedule, so it names no money or dates.
//
//nolint:gochecknoglobals // fixed contract, read-only.
var CustomVariables = []string{"name", "unit", "property", "org"}

// Template is one kind's wording in both languages. An empty string in either
// field means "no override for this language": the platform default is used.
type Template struct {
	SW string `json:"sw"`
	EN string `json:"en"`
}

// pick returns the wording for a language, falling back to Swahili — the
// default for the first client's renters — for anything unrecognised.
func (t Template) pick(lang string) string {
	if lang == LangEnglish {
		return t.EN
	}
	return t.SW
}

// Overrides maps a notification kind to the org's own wording for it. A nil
// map, a missing kind or an empty string all fall through to the platform
// default, so an org that has customised one message keeps the standard
// wording for the rest.
type Overrides map[string]*Template

// lookup returns the override for a kind, or "" when there is none.
func (o Overrides) lookup(kind, lang string) string {
	if o == nil {
		return ""
	}
	t, ok := o[kind]
	if !ok || t == nil {
		return ""
	}
	return strings.TrimSpace(t.pick(lang))
}

// platformTemplates is the default catalogue: every kind, both languages.
//
// They are plain string replacement rather than text/template — an SMS body is
// one sentence, and a template parse error must not be able to stop a
// notification from going out.
//
//nolint:gochecknoglobals // fixed message catalogue, read-only.
var platformTemplates = map[string]Template{
	KindLinkApproved: {
		EN: "Your request for {{unit}} at {{org}} was approved. Your contract will be ready to sign soon.",
		SW: "Ombi lako la {{unit}} katika {{org}} limekubaliwa. Mkataba wako utakuwa tayari kusainiwa hivi karibuni.",
	},
	KindLinkRejected: {
		EN: "Your request for {{unit}} at {{org}} was not approved: {{reason}}",
		SW: "Ombi lako la {{unit}} katika {{org}} halikukubaliwa: {{reason}}",
	},
	KindContractReady: {
		EN: "Your contract for {{unit}} at {{org}} is ready to sign. Open {{link}}",
		SW: "Mkataba wako wa {{unit}} katika {{org}} uko tayari kusainiwa. Fungua {{link}}",
	},
	KindWelcome: {
		EN: "Welcome to {{org}}. Your tenancy at {{unit}} starts {{start_date}}. First payment {{amount}} due {{due_date}}.",
		SW: "Karibu {{org}}. Upangaji wako wa {{unit}} unaanza tarehe {{start_date}}. Malipo ya kwanza ya {{amount}} yanatakiwa ifikapo {{due_date}}.",
	},
	KindContractTerminated: {
		EN: "Your tenancy of {{unit}} at {{org}} has been ended: {{reason}}",
		SW: "Upangaji wako wa {{unit}} katika {{org}} umesitishwa: {{reason}}",
	},
	KindThankYou: {
		EN: "Payment of {{amount}} for {{unit}} at {{org}} received. Thank you. Next payment {{next_amount}} due {{next_due_date}}.",
		SW: "Tumepokea malipo ya {{amount}} kwa {{unit}} katika {{org}}. Asante. Malipo yajayo ya {{next_amount}} yanatakiwa ifikapo {{next_due_date}}.",
	},
	thankYouSettled: {
		EN: "Payment of {{amount}} for {{unit}} at {{org}} received. Thank you. All payments are up to date.",
		SW: "Tumepokea malipo ya {{amount}} kwa {{unit}} katika {{org}}. Asante. Malipo yako yote yapo sawa.",
	},
	// The three rent reminders end with the pay link (Phase 16 §16.4): the
	// renter is being asked for money, and the sentence now says where to send
	// it. The wording they replace lives in supersededDefaults below, which is
	// how an installation seeded before Phase 16 picks the new sentence up.
	KindReminder7d: {
		EN: "Hello {{name}}, your rent of {{amount}} for {{unit}} at {{org}} is due on {{due_date}}. Pay here: {{pay_link}}",
		SW: "Habari {{name}}, kodi yako ya {{amount}} kwa {{unit}} katika {{org}} inatakiwa ifikapo tarehe {{due_date}}. Lipa hapa: {{pay_link}}",
	},
	KindReminderDue: {
		EN: "Hello {{name}}, your rent of {{amount}} for {{unit}} at {{org}} is due today, {{due_date}}. Pay here: {{pay_link}}",
		SW: "Habari {{name}}, kodi yako ya {{amount}} kwa {{unit}} katika {{org}} inatakiwa leo, tarehe {{due_date}}. Lipa hapa: {{pay_link}}",
	},
	KindOverdueDaily: {
		EN: "Hello {{name}}, rent of {{amount}} for {{unit}} at {{org}} was due on {{due_date}} and is still unpaid. Pay: {{pay_link}}",
		SW: "Habari {{name}}, kodi ya {{amount}} kwa {{unit}} katika {{org}} ilitakiwa {{due_date}} na bado haijalipwa. Lipa: {{pay_link}}",
	},
	// The verification code is renter-facing platform text like the rest, so
	// it lives in this catalogue rather than as a format string in the auth
	// handler. It is excluded from TemplateKinds: an org cannot re-word the
	// message that lets someone into their account, and `{{code}}` is not a
	// variable the template validator offers.
	KindOTP: {
		EN: "TMS: your verification code is {{code}}. It expires in 5 minutes.",
		SW: "TMS: msimbo wako wa uthibitisho ni {{code}}. Utaisha baada ya dakika 5.",
	},
	KindUnsignedReminder: {
		EN: "Hello {{name}}, your contract for {{unit}} at {{org}} is still waiting for your signature. Open {{link}}",
		SW: "Habari {{name}}, mkataba wako wa {{unit}} katika {{org}} bado unasubiri saini yako. Fungua {{link}}",
	},
	// `{{reason}}` is platform-only here, as it is for a termination or a
	// rejected application: the landlord's words for why the claim did not
	// stand are the point of the message, and an org override that dropped
	// them would leave the renter with nothing to act on.
	KindProofRejected: {
		EN: "Your payment proof of {{amount}} for {{unit}} at {{org}} was not accepted: {{reason}}",
		SW: "Uthibitisho wako wa malipo ya {{amount}} kwa {{unit}} katika {{org}} haujakubaliwa: {{reason}}",
	},
}

// supersededDefaults is the wording a kind used to carry, kept so a changed
// platform default can reach a catalogue that was seeded with the old one.
//
// A platform_templates row is authoritative once it exists (migration 000016
// wrote them all), so editing the Go map alone would change nothing on a live
// install. SeedPlatformTemplates therefore re-applies the new sentence to any
// row that still holds *exactly* this text and has never been versioned — an
// admin's own wording is never touched, and re-running it is a no-op. That is a
// data migration expressed as an idempotent startup step rather than a numbered
// migration, which is what PLAN2 §16.4 asks for.
//
//nolint:gochecknoglobals // fixed historical record, read-only.
var supersededDefaults = map[string]Template{
	KindReminder7d: {
		EN: "Hello {{name}}, your rent of {{amount}} for {{unit}} at {{org}} is due on {{due_date}}.",
		SW: "Habari {{name}}, kodi yako ya {{amount}} kwa {{unit}} katika {{org}} inatakiwa kulipwa ifikapo tarehe {{due_date}}.",
	},
	KindReminderDue: {
		EN: "Hello {{name}}, your rent of {{amount}} for {{unit}} at {{org}} is due today, {{due_date}}.",
		SW: "Habari {{name}}, kodi yako ya {{amount}} kwa {{unit}} katika {{org}} inatakiwa kulipwa leo, tarehe {{due_date}}.",
	},
	KindOverdueDaily: {
		EN: "Hello {{name}}, your rent of {{amount}} for {{unit}} at {{org}} was due on {{due_date}} and is still outstanding.",
		SW: "Habari {{name}}, kodi yako ya {{amount}} kwa {{unit}} katika {{org}} ilitakiwa kulipwa tarehe {{due_date}} na bado haijalipwa.",
	},
}

// Render builds one SMS body.
//
// Resolution order is org override, then platform default, then — for an
// unknown kind — the empty string, which the callers treat as "nothing to
// send". Within a template the language is the org's; anything other than
// `en` renders Swahili.
//
// It is the single rendering path for every kind: the scheduler, the event
// handlers and the landlord's bulk send all arrive here, so an org's wording
// applies wherever the message is raised from.
func Render(kind, lang string, v Vars, overrides Overrides) string {
	// A broadcast carries its own wording, so there is nothing to look up:
	// the landlord's text is the template.
	if kind == KindCustom {
		return Clamp(Substitute(v.Body, v))
	}

	key := kind
	// The settled thank-you is a different sentence, not a blank date. An org
	// that has overridden `thank_you` gets its own wording for both cases —
	// there is one template slot per kind on the wire (API.md).
	if kind == KindThankYou && v.NextDueDate == "" && overrides.lookup(kind, lang) == "" {
		key = thankYouSettled
	}

	body := overrides.lookup(kind, lang)
	if body == "" {
		// Phase 14: the platform's own wording lives in platform_templates,
		// which an admin edits. The Go catalogue is the fallback beneath it —
		// what a fresh install is seeded from, and what renders when the table
		// cannot be read.
		body = platformBody(key, lang)
		if body == "" {
			return ""
		}
	}
	return Clamp(Substitute(body, v))
}

// Substitute replaces every `{{variable}}` a body names with its value.
//
// It is one pass of plain replacement, never a template evaluation: a value
// that itself contains `{{link}}` is left standing as those nine characters,
// because strings.Replacer does not rescan what it has written. A renter whose
// name is `{{link}}` therefore cannot make the platform paste a signing link
// into someone else's message.
func Substitute(body string, v Vars) string {
	return replacerFor(v).Replace(body)
}

// Clamp bounds a rendered body at RenderedMaxLen runes.
func Clamp(body string) string {
	r := []rune(body)
	if len(r) <= RenderedMaxLen {
		return body
	}
	return string(r[:RenderedMaxLen])
}

// HasControlChars reports whether s carries a control character.
//
// A body reaches the provider verbatim; a NUL, an escape or a stray carriage
// return in it is either a mistake or an attempt to confuse something
// downstream, and neither belongs in an SMS. Ordinary whitespace — space,
// newline, tab — is allowed, since a landlord's notice may be two lines.
func HasControlChars(s string) bool {
	for _, r := range s {
		if r == '\n' || r == '\t' || r == ' ' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return true
		}
	}
	return false
}

func replacerFor(v Vars) *strings.Replacer {
	return strings.NewReplacer(
		"{{name}}", v.Name,
		"{{amount}}", v.Amount,
		"{{due_date}}", v.DueDate,
		"{{property}}", v.Property,
		"{{unit}}", v.Unit,
		"{{org}}", v.Org,
		"{{next_due_date}}", v.NextDueDate,
		"{{link}}", v.Link,
		"{{pay_link}}", v.PayLink,
		"{{reason}}", v.Reason,
		"{{start_date}}", v.StartDate,
		"{{next_amount}}", v.NextAmount,
		"{{code}}", v.Code,
	)
}

// UnknownVariables returns, sorted, the `{{…}}` placeholders in body that are
// not in allowed. An empty result means the template is safe to store.
//
// A misspelt variable is worth a 400 rather than an SMS with `{{nmae}}` in it,
// which is why PUT /org/notification-settings rejects one (API.md).
func UnknownVariables(body string, allowed []string) []string {
	ok := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		ok[a] = true
	}
	seen := map[string]bool{}
	var out []string
	rest := body
	for {
		open := strings.Index(rest, "{{")
		if open < 0 {
			break
		}
		rest = rest[open+2:]
		end := strings.Index(rest, "}}")
		if end < 0 {
			// A dangling `{{` is not a variable, but it is not valid either.
			if !seen["{{"] {
				seen["{{"] = true
				out = append(out, strings.TrimSpace(rest))
			}
			break
		}
		name := strings.TrimSpace(rest[:end])
		rest = rest[end+2:]
		if ok[name] || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// KnownKind reports whether kind names a template the platform can render.
func KnownKind(kind string) bool {
	_, ok := platformTemplates[kind]
	return ok
}

// TemplateKinds lists, sorted, every kind an org may override. `custom` is
// excluded: its body is the landlord's own text, supplied per send.
func TemplateKinds() []string {
	out := make([]string, 0, len(platformTemplates))
	for k := range platformTemplates {
		// `thank_you_settled` is a second wording of `thank_you`, and `otp` is
		// the sign-in code: neither is an org's to re-word.
		if k == thankYouSettled || k == KindOTP {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Pick returns the wording for a language, falling back to Swahili. It is the
// exported form of the internal selector, used by the admin template editor's
// preview (Phase 14).
func (t Template) Pick(lang string) string { return t.pick(lang) }
