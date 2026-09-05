package notify

import "strings"

// Notification kinds Phase 4 sends. Each announces a contract event the renter
// must act on or record: the document is ready to sign, the tenancy has
// started, or it has been ended early.
const (
	KindContractReady      = "contract_ready"
	KindWelcome            = "welcome"
	KindContractTerminated = "contract_terminated"
)

// ContractVars are the substitutions a contract message needs. Unused fields
// are simply absent from the template that does not name them.
type ContractVars struct {
	Unit      string // "Room 2"
	Org       string // the org's display name
	Link      string // "{APP_BASE_URL}/enduser/contract/{id}"
	StartDate string // YYYY-MM-DD
	Amount    string // formatted first instalment, e.g. "TZS 250,000"
	DueDate   string // YYYY-MM-DD
	Reason    string // termination only
}

//nolint:gochecknoglobals // fixed message catalogue, read-only.
var contractTemplates = map[string]map[string]string{
	KindContractReady: {
		LangEnglish: "Your contract for {unit} at {org} is ready to sign. Open {link}",
		LangSwahili: "Mkataba wako wa {unit} katika {org} uko tayari kusainiwa. Fungua {link}",
	},
	KindWelcome: {
		LangEnglish: "Welcome to {org}. Your tenancy at {unit} starts {start_date}. First payment {amount} due {due_date}.",
		LangSwahili: "Karibu {org}. Upangaji wako wa {unit} unaanza {start_date}. Malipo ya kwanza {amount} yanatakiwa {due_date}.",
	},
	KindContractTerminated: {
		LangEnglish: "Your tenancy of {unit} at {org} has been ended: {reason}",
		LangSwahili: "Upangaji wako wa {unit} katika {org} umesitishwa: {reason}",
	},
}

// RenderContract builds the SMS body for a contract event in the org's
// language, falling back to Swahili for an unrecognised setting.
func RenderContract(kind, lang string, v ContractVars) string {
	byLang, ok := contractTemplates[kind]
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
		"{link}", v.Link,
		"{start_date}", v.StartDate,
		"{amount}", v.Amount,
		"{due_date}", v.DueDate,
		"{reason}", v.Reason,
	).Replace(body)
}
