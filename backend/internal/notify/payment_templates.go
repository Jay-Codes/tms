package notify

import "strings"

// KindThankYou acknowledges a recorded payment and tells the renter what comes
// next (SPEC §6, FLOWS 7.3). It is the one Phase 5 kind; the reminder and
// overdue kinds arrive with the Phase 6 scheduler.
const KindThankYou = "thank_you"

// PaymentVars are the substitutions a thank-you message needs. NextDueDate and
// NextAmount are empty when the contract has nothing left to pay, which selects
// the "all paid" wording rather than leaving a blank date in the SMS.
type PaymentVars struct {
	Amount      string // the payment received, e.g. "TZS 250,000"
	Unit        string // "Room 2"
	Org         string // the org's display name
	NextDueDate string // YYYY-MM-DD, empty when everything is settled
	NextAmount  string // formatted next instalment, empty when everything is settled
}

//nolint:gochecknoglobals // fixed message catalogue, read-only.
var paymentTemplates = map[string]map[string]string{
	KindThankYou: {
		LangEnglish: "Payment of {amount} for {unit} at {org} received. Thank you. Next payment {next_amount} due {due_date}.",
		LangSwahili: "Malipo ya {amount} kwa {unit} katika {org} yamepokelewa. Asante. Malipo yajayo {next_amount} yanatakiwa {due_date}.",
	},
	// The settled variant: there is no next due date to name.
	thankYouSettled: {
		LangEnglish: "Payment of {amount} for {unit} at {org} received. Thank you. All payments are up to date.",
		LangSwahili: "Malipo ya {amount} kwa {unit} katika {org} yamepokelewa. Asante. Malipo yote yamekamilika.",
	},
}

// thankYouSettled is an internal template key, never a notification_log kind:
// both wordings are logged as `thank_you`.
const thankYouSettled = "thank_you_settled"

// RenderPayment builds the SMS body for a recorded payment in the org's
// language, falling back to Swahili for an unrecognised setting. With no next
// due date it renders the "all paid" wording instead.
func RenderPayment(kind, lang string, v PaymentVars) string {
	key := kind
	if kind == KindThankYou && v.NextDueDate == "" {
		key = thankYouSettled
	}
	byLang, ok := paymentTemplates[key]
	if !ok {
		return ""
	}
	body, ok := byLang[lang]
	if !ok {
		body = byLang[LangSwahili]
	}
	return strings.NewReplacer(
		"{amount}", v.Amount,
		"{unit}", v.Unit,
		"{org}", v.Org,
		"{next_amount}", v.NextAmount,
		"{due_date}", v.NextDueDate,
	).Replace(body)
}
