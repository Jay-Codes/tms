package notify_test

import (
	"strings"
	"testing"

	"tms/backend/internal/notify"
)

// fullVars carries every substitution, so a template that names a variable the
// test forgot shows up as a leftover placeholder rather than passing quietly.
func fullVars() notify.Vars {
	return notify.Vars{
		Name: "Asha Mrisho", Amount: "TZS 250,000", DueDate: "2026-09-12",
		Property: "Block A", Unit: "Room 2", Org: "JJnE Rentals",
		NextDueDate: "2026-10-12", Link: "https://tms.test/enduser/contract/abc",
		Reason: "unit already promised", StartDate: "2026-09-05", NextAmount: "TZS 250,000",
	}
}

// TestRenderPlatformTemplates pins every kind in both languages: each renders
// something, names the unit and the org, and leaves no placeholder behind.
func TestRenderPlatformTemplates(t *testing.T) {
	for _, kind := range notify.TemplateKinds() {
		for _, lang := range []string{notify.LangSwahili, notify.LangEnglish} {
			t.Run(kind+"/"+lang, func(t *testing.T) {
				body := notify.Render(kind, lang, fullVars(), nil)
				if body == "" {
					t.Fatal("rendered nothing")
				}
				if strings.Contains(body, "{{") {
					t.Errorf("body %q still holds an unsubstituted placeholder", body)
				}
				for _, want := range []string{"Room 2", "JJnE Rentals"} {
					if !strings.Contains(body, want) {
						t.Errorf("body %q does not name %q", body, want)
					}
				}
			})
		}
	}
}

// TestRenderLanguageFallback: an unrecognised language renders Swahili, the
// default for the first client's renters.
func TestRenderLanguageFallback(t *testing.T) {
	sw := notify.Render(notify.KindLinkApproved, notify.LangSwahili, fullVars(), nil)
	if got := notify.Render(notify.KindLinkApproved, "fr", fullVars(), nil); got != sw {
		t.Errorf("body for an unknown language = %q, want the Swahili %q", got, sw)
	}
}

// TestRenderUnknownKind: nothing to render is the empty string, which every
// caller treats as "nothing to send" rather than sending a blank SMS.
func TestRenderUnknownKind(t *testing.T) {
	if body := notify.Render("nonsense", notify.LangEnglish, fullVars(), nil); body != "" {
		t.Errorf("body = %q, want empty", body)
	}
}

// TestRenderThankYouSettledVariant: with nothing left to pay the message says
// so rather than naming a blank date.
func TestRenderThankYouSettledVariant(t *testing.T) {
	v := fullVars()
	v.NextDueDate = ""
	body := notify.Render(notify.KindThankYou, notify.LangEnglish, v, nil)
	if !strings.Contains(body, "up to date") {
		t.Errorf("settled thank-you = %q, want the all-paid wording", body)
	}
	if strings.Contains(body, "due ") {
		t.Errorf("settled thank-you = %q names a due date it does not have", body)
	}
}

// TestRenderOrgOverride: the org's wording wins, per kind and per language, and
// an override for one kind leaves the rest on the platform default.
func TestRenderOrgOverride(t *testing.T) {
	overrides := notify.Overrides{
		notify.KindReminderDue: {
			EN: "JJnE: {{name}}, {{amount}} for {{unit}} is due today ({{due_date}}).",
			SW: "JJnE: {{name}}, {{amount}} ya {{unit}} inatakiwa leo ({{due_date}}).",
		},
	}

	en := notify.Render(notify.KindReminderDue, notify.LangEnglish, fullVars(), overrides)
	if !strings.HasPrefix(en, "JJnE: Asha Mrisho, TZS 250,000 for Room 2 is due today") {
		t.Errorf("override not applied: %q", en)
	}
	sw := notify.Render(notify.KindReminderDue, notify.LangSwahili, fullVars(), overrides)
	if !strings.Contains(sw, "inatakiwa leo") {
		t.Errorf("Swahili override not applied: %q", sw)
	}

	// A kind with no override keeps the platform wording.
	other := notify.Render(notify.KindReminder7d, notify.LangEnglish, fullVars(), overrides)
	if strings.Contains(other, "JJnE:") {
		t.Errorf("an override for one kind leaked into another: %q", other)
	}
}

// TestRenderOverrideMissingLanguageFallsBack: an org that wrote only English
// still sends Swahili to a Swahili org — the platform default fills the gap.
func TestRenderOverrideMissingLanguageFallsBack(t *testing.T) {
	overrides := notify.Overrides{
		notify.KindReminderDue: {EN: "Only English here for {{unit}}."},
	}
	sw := notify.Render(notify.KindReminderDue, notify.LangSwahili, fullVars(), overrides)
	if strings.Contains(sw, "Only English") {
		t.Errorf("the English override was used for a Swahili send: %q", sw)
	}
	if !strings.Contains(sw, "Room 2") {
		t.Errorf("Swahili fallback did not render: %q", sw)
	}
}

// TestRenderCustomIsTheLandlordsOwnText: a broadcast's body is the template,
// and its variables still resolve per renter.
func TestRenderCustomIsTheLandlordsOwnText(t *testing.T) {
	v := fullVars()
	v.Body = "Hello {{name}}, water maintenance at {{property}} on Sunday 9am."
	body := notify.Render(notify.KindCustom, notify.LangSwahili, v, nil)
	want := "Hello Asha Mrisho, water maintenance at Block A on Sunday 9am."
	if body != want {
		t.Errorf("custom body = %q, want %q", body, want)
	}
}

// TestUnknownVariables is what stops a misspelt placeholder reaching a renter.
func TestUnknownVariables(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		allowed []string
		want    []string
	}{
		{"all known", "Hi {{name}}, {{amount}} due {{due_date}}", notify.OrgVariables, nil},
		{"one typo", "Hi {{nmae}}", notify.OrgVariables, []string{"nmae"}},
		{"sorted and deduped", "{{zzz}} {{aaa}} {{zzz}}", notify.OrgVariables, []string{"aaa", "zzz"}},
		{"custom is narrower", "{{amount}} for {{unit}}", notify.CustomVariables, []string{"amount"}},
		{"no variables at all", "Water is off on Sunday.", notify.CustomVariables, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := notify.UnknownVariables(tc.body, tc.allowed)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("UnknownVariables() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRenderDoesNotReinterpretValues: a variable's value is data. A renter who
// registers as `{{link}}` must not make the platform paste a signing link into
// the message — one pass of replacement, never a second.
func TestRenderDoesNotReinterpretValues(t *testing.T) {
	v := fullVars()
	v.Name = "{{link}} {{amount}}"
	body := notify.Render(notify.KindReminderDue, notify.LangEnglish, v, nil)
	if strings.Contains(body, "https://tms.test") {
		t.Errorf("a renter name expanded into the link variable: %q", body)
	}
	if !strings.Contains(body, "{{link}} {{amount}}") {
		t.Errorf("the name should survive verbatim: %q", body)
	}

	// The same holds for a broadcast, where the landlord's text is the template.
	v.Body = "Hello {{name}}."
	custom := notify.Render(notify.KindCustom, notify.LangEnglish, v, nil)
	if custom != "Hello {{link}} {{amount}}." {
		t.Errorf("custom body = %q, want the name left standing", custom)
	}
}

// TestRenderClampsLongValues: the template is capped at 320 characters, but the
// values are not — a body still cannot grow without bound.
func TestRenderClampsLongValues(t *testing.T) {
	v := fullVars()
	v.Name = strings.Repeat("a", 5000)
	body := notify.Render(notify.KindReminderDue, notify.LangEnglish, v, nil)
	if n := len([]rune(body)); n != notify.RenderedMaxLen {
		t.Errorf("rendered length = %d, want a clamp at %d", n, notify.RenderedMaxLen)
	}
}

// TestHasControlChars: what may travel to the provider, and what may not.
func TestHasControlChars(t *testing.T) {
	cases := map[string]bool{
		"Water is off on Sunday.": false,
		"Two\nlines and a\ttab":   false,
		"carriage\rreturn":        true,
		"nul\x00byte":             true,
		"escape\x1b[31m":          true,
		"Kiswahili: mvua kubwa":   false,
	}
	for body, want := range cases {
		if got := notify.HasControlChars(body); got != want {
			t.Errorf("HasControlChars(%q) = %v, want %v", body, got, want)
		}
	}
}
