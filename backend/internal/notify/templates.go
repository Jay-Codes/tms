package notify

import "strings"

// Notification kinds used in Phase 3. The scheduler kinds (reminder_7d,
// reminder_due, overdue_daily, thank_you) arrive with Phase 6.
const (
	KindLinkApproved = "link_approved"
	KindLinkRejected = "link_rejected"
)

// Languages an org may send in (orgs.settings.sms_language). Swahili is the
// default for the first client's renters.
const (
	LangSwahili = "sw"
	LangEnglish = "en"
)

// LinkVars are the substitutions a link-decision message needs.
type LinkVars struct {
	Unit   string // "Room 1" — the unit's name
	Org    string // the org's display name
	Reason string // rejection only
}

// linkTemplates holds both languages for both kinds. They are plain Go string
// replacement rather than text/template: an SMS body is one sentence, and a
// template parse error must not be able to stop a notification from going out.
var linkTemplates = map[string]map[string]string{
	KindLinkApproved: {
		LangEnglish: "Your request for {unit} at {org} was approved. Your contract will be ready to sign soon.",
		LangSwahili: "Ombi lako la {unit} katika {org} limekubaliwa. Mkataba wako utakuwa tayari kusainiwa hivi karibuni.",
	},
	KindLinkRejected: {
		LangEnglish: "Your request for {unit} at {org} was not approved: {reason}",
		LangSwahili: "Ombi lako la {unit} katika {org} halikukubaliwa: {reason}",
	},
}

// RenderLink builds the SMS body for a link approval or rejection in the org's
// language, falling back to Swahili for an unrecognised setting.
func RenderLink(kind, lang string, v LinkVars) string {
	byLang, ok := linkTemplates[kind]
	if !ok {
		return ""
	}
	body, ok := byLang[lang]
	if !ok {
		body = byLang[LangSwahili]
	}
	return strings.NewReplacer(
		"{unit}", v.Unit,
		"{org}", v.Org,
		"{reason}", v.Reason,
	).Replace(body)
}
