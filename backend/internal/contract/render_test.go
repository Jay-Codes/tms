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
