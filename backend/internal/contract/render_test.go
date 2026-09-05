package contract_test

import (
	"os"
	"strings"
	"testing"

	"tms/backend/internal/contract"
)

// TestSanitizeKeepsTheAllowlistAndDropsEverythingElse pins the policy API.md
// names. A template body is authored in a rich-text editor and then rendered
// inside the renter's app, so anything that can execute or fetch a remote
// resource has to be gone before it is stored (SPEC §5.5).
func TestSanitizeKeepsTheAllowlistAndDropsEverythingElse(t *testing.T) {
	kept := []struct{ name, in, want string }{
		{"paragraph", "<p>Hello</p>", "<p>Hello</p>"},
		{"heading", "<h2>Rent</h2>", "<h2>Rent</h2>"},
		{"emphasis", "<strong>x</strong><em>y</em><u>z</u>", "<strong>x</strong><em>y</em><u>z</u>"},
		{"list", "<ul><li>one</li></ul>", "<ul><li>one</li></ul>"},
		{"table", "<table><tbody><tr><td>a</td></tr></tbody></table>",
			"<table><tbody><tr><td>a</td></tr></tbody></table>"},
		{"quote", "<blockquote>q</blockquote>", "<blockquote>q</blockquote>"},
		{"class survives", `<p class="lead">x</p>`, `<p class="lead">x</p>`},
	}
	for _, tc := range kept {
		t.Run(tc.name, func(t *testing.T) {
			if got := contract.SanitizeHTML(tc.in); got != tc.want {
				t.Errorf("SanitizeHTML(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	stripped := []struct{ name, in string }{
		{"script tag and its contents", `<script>alert(1)</script>`},
		{"style tag and its contents", `<style>body{display:none}</style>`},
		{"event handler", `<p onclick="steal()">x</p>`},
		{"inline style", `<p style="position:fixed">x</p>`},
		{"link", `<a href="https://evil.example">x</a>`},
		{"image", `<img src="https://evil.example/pixel.gif">`},
		{"iframe", `<iframe src="https://evil.example"></iframe>`},
		{"form", `<form action="/pay"><input name="pin"></form>`},
	}
	for _, tc := range stripped {
		t.Run(tc.name, func(t *testing.T) {
			got := contract.SanitizeHTML(tc.in)
			for _, banned := range []string{"script", "style=", "onclick", "href", "src", "iframe", "<form", "<input"} {
				if strings.Contains(got, banned) {
					t.Errorf("SanitizeHTML(%q) = %q, still carries %q", tc.in, got, banned)
				}
			}
			if strings.Contains(got, "alert(1)") || strings.Contains(got, "display:none") {
				t.Errorf("SanitizeHTML(%q) = %q, kept the contents of a stripped element", tc.in, got)
			}
		})
	}
}

// TestRenderEscapesValues is the injection boundary: the body is trusted (it is
// sanitized on the way in), the values never are. A renter whose name contains
// markup must appear as text in the document, not as markup.
func TestRenderEscapesValues(t *testing.T) {
	body := `<p>Tenant: {{renter_name}} at {{unit}}</p>`
	out := contract.Render(body, map[string]string{
		"renter_name": `Asha <script>alert(1)</script>`,
		"unit":        `Room "2" & 3`,
	})
	if strings.Contains(out, "<script>") {
		t.Errorf("a value's markup survived rendering: %s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("the value was not HTML-escaped: %s", out)
	}
	if !strings.Contains(out, `Room &#34;2&#34; &amp; 3`) {
		t.Errorf("quotes and ampersands were not escaped: %s", out)
	}
	// The body's own markup is left alone — it is the trusted half.
	if !strings.HasPrefix(out, "<p>Tenant:") {
		t.Errorf("the template's own markup was escaped: %s", out)
	}
}

// TestRenderResolvesUnknownVariablesToBlank: a blank where a value should be is
// obvious to the person reading the contract; `{{gibberish}}` reads as a broken
// system and invites a dispute.
func TestRenderResolvesUnknownVariablesToBlank(t *testing.T) {
	out := contract.Render(`<p>[{{nonesuch}}][{{ unit }}]</p>`, map[string]string{"unit": "Room 2"})
	if out != `<p>[][Room 2]</p>` {
		t.Errorf("Render = %q, want an empty unknown and a whitespace-tolerant known", out)
	}
}

// TestSampleVarsCoverEveryVariable keeps the preview honest: a variable the
// editor offers but the preview cannot fill would render as a blank and look
// like a bug.
func TestSampleVarsCoverEveryVariable(t *testing.T) {
	sample := contract.SampleVars("Mbezi Estates")
	for _, name := range contract.Variables {
		if sample[name] == "" {
			t.Errorf("SampleVars has no value for %q", name)
		}
	}
}

// TestDefaultTemplateUsesEveryVariable: the seeded template is what most orgs
// will ever issue, so it is also the working example of the vocabulary.
func TestDefaultTemplateUsesEveryVariable(t *testing.T) {
	for _, name := range contract.Variables {
		if !strings.Contains(contract.DefaultTemplateBody, "{{"+name+"}}") {
			t.Errorf("the default template body never uses {{%s}}", name)
		}
	}
	// The sanitizer entity-escapes bare quotes in text, so a body is not
	// byte-identical to its sanitized form; what matters is that no element is
	// dropped and that sanitizing twice changes nothing more.
	once := contract.SanitizeHTML(contract.DefaultTemplateBody)
	if twice := contract.SanitizeHTML(once); twice != once {
		t.Error("sanitizing the default template body twice is not the same as once")
	}
	for _, tag := range []string{"<h1>", "<h2>", "<p>", "<strong>"} {
		if !strings.Contains(once, tag) {
			t.Errorf("the sanitizer dropped %s from the default template body", tag)
		}
	}
}

// TestDefaultTemplateBodyMatchesMigration: org creation seeds this constant, and
// every migration that re-words the default carries a copy for the orgs that
// already exist (000005 seeded it, 000006 fixed the `{{due_day}}` phrase, 000012
// added `{{rent_basis}}`). The *newest* of those copies is the one that has to
// match the constant — the earlier ones are history, and each was superseded by
// the next. If the newest drifts, old and new orgs issue different terms.
func TestDefaultTemplateBodyMatchesMigration(t *testing.T) {
	for _, name := range []string{"000012_part2_foundations.up.sql"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile("../../migrations/" + name)
			if err != nil {
				t.Fatalf("read migration: %v", err)
			}
			const marker = "$tpl$"
			sql := string(raw)
			start := strings.Index(sql, marker)
			if start < 0 {
				t.Fatalf("migration %s carries no $tpl$-quoted template body", name)
			}
			rest := sql[start+len(marker):]
			end := strings.Index(rest, marker)
			if end < 0 {
				t.Fatalf("migration %s's template body is not closed", name)
			}
			if got := rest[:end]; got != contract.DefaultTemplateBody {
				t.Errorf("the migration's body and contract.DefaultTemplateBody have drifted\n--- migration ---\n%s\n--- constant ---\n%s",
					got, contract.DefaultTemplateBody)
			}
		})
	}
}

// TestDueDayPhraseReadsWithoutADueDay: `due_day` is optional (SPEC §4), and the
// default template says "on or before {{due_day}} of each payment period" — so
// the variable has to carry a phrase, not a bare number, or a contract without
// a due day reads "on or before  of each payment period".
func TestDueDayPhraseReadsWithoutADueDay(t *testing.T) {
	five := 5
	if got := contract.DueDayPhrase(&five); got != "day 5" {
		t.Errorf("DueDayPhrase(5) = %q, want \"day 5\"", got)
	}
	if got := contract.DueDayPhrase(nil); got != "the first day" {
		t.Errorf("DueDayPhrase(nil) = %q, want \"the first day\"", got)
	}

	// The default body has to read in both cases.
	body := contract.SanitizeHTML(contract.DefaultTemplateBody)
	for name, phrase := range map[string]string{"none": contract.DueDayPhrase(nil), "fifth": contract.DueDayPhrase(&five)} {
		out := contract.Render(body, map[string]string{"due_day": phrase})
		if strings.Contains(out, "before  ") || strings.Contains(out, "before of") {
			t.Errorf("the default template reads badly with due day %s: %s", name, out)
		}
		if !strings.Contains(out, "on or before "+phrase+" of each payment period") {
			t.Errorf("the default template's rent clause did not resolve with due day %s", name)
		}
	}
}

// TestDefaultTemplateBodySWMatchesMigration is the Swahili twin of
// TestDefaultTemplateBodyMatchesMigration (Phase 13). Migration 000015 seeds
// `body_html_sw` for the orgs that already existed and org creation seeds this
// constant for new ones; if the two drift, two orgs on the same platform issue
// different Swahili terms.
func TestDefaultTemplateBodySWMatchesMigration(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/000015_language.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	const marker = "$tplsw$"
	sql := string(raw)
	start := strings.Index(sql, marker)
	if start < 0 {
		t.Fatalf("migration carries no %s-quoted Swahili template body", marker)
	}
	rest := sql[start+len(marker):]
	end := strings.Index(rest, marker)
	if end < 0 {
		t.Fatalf("the migration's Swahili template body is not closed")
	}
	if got := rest[:end]; got != contract.DefaultTemplateBodySW {
		t.Errorf("the migration's body and contract.DefaultTemplateBodySW have drifted\n--- migration ---\n%s\n--- constant ---\n%s",
			got, contract.DefaultTemplateBodySW)
	}
}

// TestDefaultTemplateBodiesCarryTheSameVariables: a contract renders from
// whichever body its language names, so a placeholder present in one and
// missing from the other would make the same tenancy read differently
// depending on the renter's language.
func TestDefaultTemplateBodiesCarryTheSameVariables(t *testing.T) {
	for _, v := range contract.Variables {
		ph := "{{" + v + "}}"
		inEN := strings.Contains(contract.DefaultTemplateBody, ph)
		inSW := strings.Contains(contract.DefaultTemplateBodySW, ph)
		if inEN != inSW {
			t.Errorf("%s: english=%v swahili=%v — the two bodies disagree", ph, inEN, inSW)
		}
	}
	// And the Swahili body survives the sanitizer whole, like the English one.
	if clean := contract.SanitizeHTML(contract.DefaultTemplateBodySW); !strings.Contains(clean, "Mkataba wa Upangaji") {
		t.Errorf("the sanitizer gutted the Swahili default body: %q", clean)
	}
}

// TestBodyForPicksTheLanguageThatExists: Swahili when the org has written it,
// English otherwise — and the resolved language is the one actually used, so a
// contract never records a language its terms are not in.
func TestBodyForPicksTheLanguageThatExists(t *testing.T) {
	const en, sw = "<p>English</p>", "<p>Kiswahili</p>"
	cases := []struct {
		name, want, wantLang, lang, sw string
	}{
		{"swahili wanted and written", sw, contract.LangSwahili, "sw", sw},
		{"swahili wanted, none written", en, contract.LangEnglish, "sw", ""},
		{"swahili wanted, only blanks written", en, contract.LangEnglish, "sw", "   "},
		{"english wanted", en, contract.LangEnglish, "en", sw},
		{"nothing wanted", en, contract.LangEnglish, "", sw},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body, lang := contract.BodyFor(c.lang, en, c.sw)
			if body != c.want || lang != c.wantLang {
				t.Errorf("BodyFor(%q, en, %q) = (%q, %q), want (%q, %q)",
					c.lang, c.sw, body, lang, c.want, c.wantLang)
			}
		})
	}
}

// TestDueDayPhraseForSpeaksTheContractLanguage: a Swahili document with an
// English clause in the middle of it is not a Swahili document.
func TestDueDayPhraseForSpeaksTheContractLanguage(t *testing.T) {
	five := 5
	if got := contract.DueDayPhraseFor(contract.LangSwahili, &five); got != "siku ya 5" {
		t.Errorf("sw/5 = %q", got)
	}
	if got := contract.DueDayPhraseFor(contract.LangSwahili, nil); got != "siku ya kwanza" {
		t.Errorf("sw/nil = %q", got)
	}
	if got := contract.DueDayPhraseFor(contract.LangEnglish, &five); got != contract.DueDayPhrase(&five) {
		t.Errorf("en/5 = %q, want the English phrase", got)
	}
	if got := contract.RentBasisPhraseFor(contract.LangSwahili, "TZS 100,000", 30); got != "TZS 100,000 / siku 30" {
		t.Errorf("sw rent basis = %q", got)
	}
	if got := contract.RentBasisPhraseFor(contract.LangEnglish, "TZS 100,000", 30); got != "TZS 100,000 / 30 days" {
		t.Errorf("en rent basis = %q", got)
	}
}
