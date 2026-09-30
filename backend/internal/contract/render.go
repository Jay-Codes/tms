package contract

import (
	"html"
	"regexp"
	"strconv"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// DefaultTemplateName is the template seeded for every org (migration 000005
// for orgs that already existed, org creation for new ones).
const DefaultTemplateName = "Standard tenancy agreement"

// Variables are the substitutions a template body may use (API.md). They are
// listed on GET /contract-templates/{id} so the editor can offer them.
//
// `rent` is the amount due once per payment period; `rent_basis` is the unit's
// own price and the days it covers ("TZS 100,000 / 30 days"), for landlords who
// want both figures in the document (PLAN2 Phase 9).
//
//nolint:gochecknoglobals // a fixed vocabulary, read-only.
var Variables = []string{
	"renter_name", "unit", "property", "rent", "rent_basis", "start_date", "end_date",
	"payment_period", "org_name", "term_days", "due_day",
	// Phase 22 §22.2 — filled from the template's policy, blank without one.
	"deposit", "tenant_notice_days", "eviction_notice_days",
}

// PolicyVariables are the Variables that only a template with a policy fills
// (Phase 22). The seeded default template has no policy, so it does not use
// them — a blank "deposit of ." would be worse than no sentence.
var PolicyVariables = map[string]bool{
	"deposit": true, "tenant_notice_days": true, "eviction_notice_days": true,
}

// varPattern matches `{{name}}`, tolerating inner whitespace. Names are the
// closed vocabulary above; anything else resolves to blank.
var varPattern = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

// bodyPolicy is the allowlist from API.md: structural and inline-emphasis
// elements only, `class` the single surviving attribute.
//
// Everything else is stripped — every other attribute (so `onclick`, `style`,
// `href`, `src` cannot survive), and `<script>`/`<style>` along with their
// contents. A landlord's rich-text editor produces exactly this subset, and a
// contract document is rendered inside the renter's app: anything that can
// execute or load a remote resource has no business in it (SPEC §5.5).
//
//nolint:gochecknoglobals // one compiled policy, used read-only.
var bodyPolicy = newBodyPolicy()

func newBodyPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	elements := []string{
		"p", "br", "h1", "h2", "h3",
		"ul", "ol", "li",
		"strong", "em", "u",
		"table", "thead", "tbody", "tr", "td", "th",
		"blockquote",
	}
	p.AllowElements(elements...)
	p.AllowAttrs("class").OnElements(elements...)
	return p
}

// SanitizeHTML reduces a template body to the allowlisted subset. It is applied
// on write (a stored body is already safe) and again on render, so a body that
// predates a policy change cannot escape it.
func SanitizeHTML(in string) string { return bodyPolicy.Sanitize(in) }

// Render resolves `{{variable}}` placeholders in a template body.
//
// Values are HTML-escaped, so a renter called `Asha <script>` becomes text in
// the document rather than markup: the body is trusted (it is sanitized on the
// way in), the values are not. An unknown or missing variable resolves to the
// empty string — a contract with a blank where a value should be is obvious to
// the human reading it, where `{{gibberish}}` looks like a broken system.
func Render(body string, vars map[string]string) string {
	return varPattern.ReplaceAllStringFunc(body, func(match string) string {
		name := varPattern.FindStringSubmatch(match)[1]
		v, ok := vars[strings.ToLower(name)]
		if !ok {
			return ""
		}
		return html.EscapeString(v)
	})
}

// DueDayPhrase renders `{{due_day}}` as a phrase rather than a bare number, so
// a contract without a due day still reads as English.
//
// A contract may have no due day at all (SPEC §4: each row then falls due on
// the day its period starts), and "on or before day  of each payment period"
// is the sentence that produced. The phrase carries the word "day" with it —
// "day 5", or "the first day" when there is none — so both cases read.
func DueDayPhrase(dueDay *int) string {
	if dueDay == nil {
		return "the first day"
	}
	return "day " + strconv.Itoa(*dueDay)
}

// DueDayPhraseFor is DueDayPhrase in the language the contract is rendered in
// (Phase 13). A Swahili document that says "on or before day 5" is a Swahili
// document with an English sentence in the middle of it.
func DueDayPhraseFor(lang string, dueDay *int) string {
	if lang != LangSwahili {
		return DueDayPhrase(dueDay)
	}
	if dueDay == nil {
		return "siku ya kwanza"
	}
	return "siku ya " + strconv.Itoa(*dueDay)
}

// PaymentPeriodPhraseFor renders `{{payment_period}}`: the period's label and
// how long it is — "Monthly (30 days)", or for a calendar period "Monthly
// (every calendar month)" / "Quarterly (every 3 calendar months)".
func PaymentPeriodPhraseFor(lang, label string, days, months int) string {
	if lang == LangSwahili {
		switch {
		case months == 1:
			return label + " (kila mwezi wa kalenda)"
		case months > 1:
			return label + " (kila miezi " + strconv.Itoa(months) + " ya kalenda)"
		}
		return label + " (siku " + strconv.Itoa(days) + ")"
	}
	switch {
	case months == 1:
		return label + " (every calendar month)"
	case months > 1:
		return label + " (every " + strconv.Itoa(months) + " calendar months)"
	}
	return label + " (" + strconv.Itoa(days) + " days)"
}

// RentBasisPhrase renders `{{rent_basis}}`: the unit's own price and the span
// it covers, as "TZS 100,000 / 30 days".
//
// The amount arrives already formatted — money formatting lives in one place
// (notify.FormatTZS) and this package does not import it — so the phrase reads
// the same wherever it is built.
func RentBasisPhrase(formattedAmount string, rentPeriodDays int) string {
	return formattedAmount + " / " + strconv.Itoa(rentPeriodDays) + " days"
}

// RentBasisPhraseFor is RentBasisPhrase in the contract's language.
func RentBasisPhraseFor(lang, formattedAmount string, rentPeriodDays int) string {
	if lang != LangSwahili {
		return RentBasisPhrase(formattedAmount, rentPeriodDays)
	}
	return formattedAmount + " / siku " + strconv.Itoa(rentPeriodDays)
}

// Languages a contract may be rendered in (SPEC §3.2). They mirror
// notify.LangSwahili / notify.LangEnglish; this package does not import notify
// (the dependency runs the other way), so the two constants are stated here.
const (
	LangSwahili = "sw"
	LangEnglish = "en"
)

// BodyFor picks the body a contract is rendered from: the Swahili one when the
// contract is Swahili and the org has written one, else the English body.
//
// An org that has never touched its templates has both (the bootstrap seeds
// them); an org that added a template of its own may have only `body_html`,
// and a renter who prefers Swahili should still get the document rather than a
// blank page.
func BodyFor(lang, bodyHTML, bodyHTMLSW string) (body, resolvedLang string) {
	if lang == LangSwahili && strings.TrimSpace(bodyHTMLSW) != "" {
		return bodyHTMLSW, LangSwahili
	}
	return bodyHTML, LangEnglish
}

// SampleVars are the placeholder values POST /contract-templates/{id}/preview
// substitutes, so a landlord sees the shape of a real document while editing.
func SampleVars(orgName string) map[string]string {
	return map[string]string{
		"renter_name":    "Asha Mrisho",
		"unit":           "Room 2",
		"property":       "Mbezi Beach Block A",
		"rent":           "TZS 250,000",
		"rent_basis":     "TZS 250,000 / 30 days",
		"start_date":     "2026-10-01",
		"end_date":       "2027-03-29",
		"payment_period": "Monthly (30 days)",
		"org_name":       orgName,
		"term_days":      "180",
		"due_day":        "day 1",
		// Phase 22 — what a template with a policy fills in.
		"deposit":              "TZS 500,000",
		"tenant_notice_days":   "30",
		"eviction_notice_days": "30",
	}
}
