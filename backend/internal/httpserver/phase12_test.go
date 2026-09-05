package httpserver_test

import (
	"net/http"
	"testing"
)

// Phase 12 — theming v2 (SPEC §2.0, PLAN2 Phase 12).
//
// The backend owns the presets and the contrast guard, so these tests are
// about the three promises the frontends build on: the catalogue is the same
// eight themes for everyone, what an org saves is what every endpoint then
// resolves, and a theme nobody could read is refused with enough detail to fix
// it.

// ledgerTokens is the default palette, byte-for-byte packages/ui/src/tokens.css.
//
//nolint:gochecknoglobals // fixture data.
var ledgerTokens = map[string]string{
	"paper": "#fbfbf7", "surface": "#ffffff", "ink": "#1c2b5a", "ink_muted": "#4a5680",
	"rule": "#cbd3e8", "primary": "#2b4fd0", "accent": "#2b4fd0",
}

// nightTokens is the dark preset, for the "saved is served" test.
//
//nolint:gochecknoglobals // fixture data.
var nightTokens = map[string]string{
	"paper": "#14161c", "surface": "#1c1f27", "ink": "#e8eaf2", "ink_muted": "#aab1c7",
	"rule": "#343a4a", "primary": "#96b4ff", "accent": "#96b4ff",
}

// tokensOf reads the token object out of a `theme` block at the given path.
func tokensOf(t *testing.T, r response, path ...string) map[string]string {
	t.Helper()
	cur := any(r.Body)
	for _, key := range append(path, "tokens") {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("path %v: %q is not an object — body: %s", path, key, r.Raw)
		}
		cur = m[key]
	}
	raw, ok := cur.(map[string]any)
	if !ok {
		t.Fatalf("no token object at %v — body: %s", path, r.Raw)
	}
	out := map[string]string{}
	for k, v := range raw {
		s, isStr := v.(string)
		if !isStr {
			t.Fatalf("token %q is not a string — body: %s", k, r.Raw)
		}
		out[k] = s
	}
	return out
}

func assertTokens(t *testing.T, got, want map[string]string, what string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d tokens, want %d — %v", what, len(got), len(want), got)
	}
	for key, wantVal := range want {
		if got[key] != wantVal {
			t.Errorf("%s: %s = %q, want %q", what, key, got[key], wantVal)
		}
	}
}

// ------------------------------------------------------ GET /themes/presets --

// TestThemePresetsAreServedPublicly: the catalogue is platform data, so it
// answers without a session and answers the same for everyone.
func TestThemePresetsAreServedPublicly(t *testing.T) {
	h := newHarness(t)
	got := h.client().do(http.MethodGet, "/themes/presets", nil).
		mustStatus(t, http.StatusOK, "presets")

	list, ok := got.Body["presets"].([]any)
	if !ok {
		t.Fatalf("no presets array — body: %s", got.Raw)
	}
	if len(list) != 8 {
		t.Fatalf("presets = %d, want the 8 shipped", len(list))
	}

	wantIDs := []string{
		"ledger", "night_ledger", "warm_paper", "cool_slate",
		"forest", "ocean", "high_contrast", "minimal_white",
	}
	var dark int
	for i, item := range list {
		p, isObj := item.(map[string]any)
		if !isObj {
			t.Fatalf("preset %d is not an object", i)
		}
		if p["id"] != wantIDs[i] {
			t.Errorf("preset %d: id = %v, want %q", i, p["id"], wantIDs[i])
		}
		if name, _ := p["name"].(string); name == "" {
			t.Errorf("preset %s has no display name", wantIDs[i])
		}
		if font, _ := p["font_id"].(string); font == "" {
			t.Errorf("preset %s has no font", wantIDs[i])
		}
		tokens, isObj := p["tokens"].(map[string]any)
		if !isObj || len(tokens) != 7 {
			t.Errorf("preset %s: tokens = %v, want the 7-key set", wantIDs[i], p["tokens"])
		}
		if isDark, _ := p["dark"].(bool); isDark {
			dark++
		}
	}
	if dark != 1 {
		t.Errorf("dark presets = %d, want exactly night_ledger", dark)
	}

	if first, _ := list[0].(map[string]any); first != nil {
		tokens, _ := first["tokens"].(map[string]any)
		for key, want := range ledgerTokens {
			if tokens[key] != want {
				t.Errorf("ledger.%s = %v, want %q (it must match tokens.css)", key, tokens[key], want)
			}
		}
	}
}

// ---------------------------------------------------------- saving a theme --

// TestThemePresetSavedIsThemeServed walks the exit condition of the phase: a
// landlord picks a preset and every surface — their own settings, the public
// login branding, a renter's QR landing — resolves to it.
func TestThemePresetSavedIsThemeServed(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Theme Co", "theme@jjne.test", "0714000280", []string{"Room 1"}, 100_000)
	c := fix.client
	slug := c.do(http.MethodGet, "/org", nil).
		mustStatus(t, http.StatusOK, "read org").str(t, "slug")

	t.Run("an org that has chosen nothing is on the ledger preset", func(t *testing.T) {
		got := c.do(http.MethodGet, "/org/branding", nil).
			mustStatus(t, http.StatusOK, "branding before")
		if id := got.str(t, "theme", "preset_id"); id != "ledger" {
			t.Errorf("preset_id = %q, want ledger", id)
		}
		if src := got.str(t, "theme", "source"); src != "default" {
			t.Errorf("source = %q, want default", src)
		}
		assertTokens(t, tokensOf(t, got, "theme"), ledgerTokens, "the default theme")
	})

	saved := c.do(http.MethodPut, "/org/branding", map[string]any{
		"theme": map[string]any{"preset_id": "night_ledger"},
	}).mustStatus(t, http.StatusOK, "save the night ledger")
	assertTokens(t, tokensOf(t, saved, "branding", "theme"), nightTokens, "the saved theme")

	t.Run("the landlord's own read shows it", func(t *testing.T) {
		got := c.do(http.MethodGet, "/org/branding", nil).
			mustStatus(t, http.StatusOK, "branding after")
		if id := got.str(t, "theme", "preset_id"); id != "night_ledger" {
			t.Errorf("preset_id = %q", id)
		}
		if src := got.str(t, "theme", "source"); src != "preset" {
			t.Errorf("source = %q, want preset", src)
		}
		dark, _ := got.Body["theme"].(map[string]any)["dark"].(bool)
		if !dark {
			t.Error("the night ledger is not marked dark")
		}
		assertTokens(t, tokensOf(t, got, "theme"), nightTokens, "the stored theme")
	})

	t.Run("the public branding endpoint shows it", func(t *testing.T) {
		got := h.client().do(http.MethodGet, "/public/orgs/"+slug+"/branding", nil).
			mustStatus(t, http.StatusOK, "public branding")
		assertTokens(t, tokensOf(t, got, "theme"), nightTokens, "the public theme")
		// Phase 4 clients still read primary_color, so it must keep answering.
		if got.str(t, "theme", "primary_color") != nightTokens["primary"] {
			t.Errorf("primary_color = %q, want the resolved primary", got.str(t, "theme", "primary_color"))
		}
	})

	t.Run("the QR landing shows it", func(t *testing.T) {
		got := h.client().do(http.MethodGet, "/public/units/"+fix.unitCodes[0], nil).
			mustStatus(t, http.StatusOK, "public unit")
		assertTokens(t, tokensOf(t, got, "branding", "theme"), nightTokens, "the unit theme")
	})

	t.Run("the change is in the audit trail under its own action", func(t *testing.T) {
		got := c.do(http.MethodGet, "/audit-log", nil).
			mustStatus(t, http.StatusOK, "audit")
		found := false
		for _, row := range listOf(t, got) {
			if row["action"] == "branding.theme_update" {
				found = true
				after, _ := row["after"].(map[string]any)
				block, _ := after["theme"].(map[string]any)
				if block["preset_id"] != "night_ledger" {
					t.Errorf("the audited theme is not what was saved: %v", after)
				}
			}
		}
		if !found {
			t.Fatalf("no branding.theme_update row — body: %s", got.Raw)
		}
	})
}

// TestThemeAdvancedOverride: the advanced panel's job — a full token set on
// top of a preset, with the preset kept as the label it started from.
func TestThemeAdvancedOverride(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Advanced", "advanced@jjne.test", "0714000281", nil, 0)
	c := fix.client

	custom := map[string]any{}
	for k, v := range nightTokens {
		custom[k] = v
	}
	custom["primary"] = "#FFD166" // a warmer accent, still readable on the dark paper
	custom["accent"] = "#FFD166"

	saved := c.do(http.MethodPut, "/org/branding", map[string]any{
		"theme": map[string]any{"preset_id": "night_ledger", "tokens": custom, "font_id": "hanken"},
	}).mustStatus(t, http.StatusOK, "save an override")

	if src := saved.str(t, "branding", "theme", "source"); src != "custom" {
		t.Errorf("source = %q, want custom", src)
	}
	if id := saved.str(t, "branding", "theme", "preset_id"); id != "night_ledger" {
		t.Errorf("preset_id = %q — the base label must survive an override", id)
	}
	if font := saved.str(t, "branding", "theme", "font_id"); font != "hanken" {
		t.Errorf("font_id = %q", font)
	}
	// Colours are canonicalised: one form for a value that ends up in CSS.
	if got := tokensOf(t, saved, "branding", "theme")["primary"]; got != "#ffd166" {
		t.Errorf("primary = %q, want the lower-cased override", got)
	}

	t.Run("it survives a re-read", func(t *testing.T) {
		got := c.do(http.MethodGet, "/org/branding", nil).
			mustStatus(t, http.StatusOK, "re-read")
		if v := tokensOf(t, got, "theme")["primary"]; v != "#ffd166" {
			t.Errorf("primary = %q after re-read", v)
		}
	})

	t.Run("going back to a plain preset drops the override", func(t *testing.T) {
		got := c.do(http.MethodPut, "/org/branding", map[string]any{
			"theme": map[string]any{"preset_id": "ledger"},
		}).mustStatus(t, http.StatusOK, "back to ledger")
		assertTokens(t, tokensOf(t, got, "branding", "theme"), ledgerTokens, "after reverting")
		if src := got.str(t, "branding", "theme", "source"); src != "preset" {
			t.Errorf("source = %q, want preset", src)
		}
	})
}

// ------------------------------------------------------------- rejections --

// TestThemeRejectsUnreadableColours is the contrast guard at the edge: the
// client warns, but the server is what makes the warning true.
func TestThemeRejectsUnreadableColours(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Contrast", "contrast@jjne.test", "0714000282", nil, 0)
	c := fix.client

	bad := map[string]any{}
	for k, v := range ledgerTokens {
		bad[k] = v
	}
	bad["ink"] = "#c9c9c9"       // grey text on near-white paper
	bad["ink_muted"] = "#d5d5d5" // and a fainter grey under it

	resp := c.do(http.MethodPut, "/org/branding", map[string]any{
		"theme": map[string]any{"tokens": bad},
	}).mustStatus(t, http.StatusBadRequest, "unreadable theme")

	if ct := resp.Body["type"]; ct == nil {
		t.Errorf("not an RFC 7807 document — body: %s", resp.Raw)
	}
	errs, _ := resp.Body["errors"].(map[string]any)
	if errs["theme.tokens"] == nil {
		t.Errorf("errors = %v, want theme.tokens", resp.Body["errors"])
	}

	fails, ok := resp.Body["failures"].([]any)
	if !ok || len(fails) == 0 {
		t.Fatalf("no failures array — body: %s", resp.Raw)
	}
	seen := map[string]bool{}
	for _, item := range fails {
		fail, _ := item.(map[string]any)
		pair, _ := fail["pair"].(string)
		seen[pair] = true
		ratio, _ := fail["ratio"].(float64)
		minimum, _ := fail["minimum"].(float64)
		if minimum <= 0 || ratio <= 0 || ratio >= minimum {
			t.Errorf("failure %v does not describe a real shortfall", fail)
		}
	}
	for _, want := range []string{"ink/paper", "ink_muted/paper"} {
		if !seen[want] {
			t.Errorf("failures %v do not name %s", fails, want)
		}
	}

	t.Run("nothing was stored", func(t *testing.T) {
		got := c.do(http.MethodGet, "/org/branding", nil).
			mustStatus(t, http.StatusOK, "branding after the rejection")
		assertTokens(t, tokensOf(t, got, "theme"), ledgerTokens, "after a rejected save")
	})
}

// TestThemeInputValidation covers the field-level refusals: an unknown preset,
// a half-filled token set, an unknown font.
func TestThemeInputValidation(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Refusals", "refusals@jjne.test", "0714000283", nil, 0)
	c := fix.client

	cases := []struct {
		name  string
		theme map[string]any
		field string
	}{
		{
			name:  "an unknown preset",
			theme: map[string]any{"preset_id": "chartreuse"},
			field: "theme.preset_id",
		},
		{
			name:  "a partial token set",
			theme: map[string]any{"tokens": map[string]any{"paper": "#ffffff", "ink": "#000000"}},
			field: "theme.tokens",
		},
		{
			name: "an unknown token",
			theme: map[string]any{"tokens": func() map[string]any {
				out := map[string]any{"shadow": "#000000"}
				for k, v := range ledgerTokens {
					out[k] = v
				}
				return out
			}()},
			field: "theme.tokens",
		},
		{
			name:  "a font nobody ships",
			theme: map[string]any{"preset_id": "ledger", "font_id": "comic-sans"},
			field: "theme.font_id",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := c.do(http.MethodPut, "/org/branding", map[string]any{"theme": tc.theme}).
				mustStatus(t, http.StatusBadRequest, tc.name)
			errs, _ := resp.Body["errors"].(map[string]any)
			if errs[tc.field] == nil {
				t.Errorf("errors = %v, want %s", resp.Body["errors"], tc.field)
			}
		})
	}
}

// ----------------------------------------------------------------- legacy --

// TestThemeLegacyPrimaryColourStillWorks: a Phase 4 client that knows only
// `primary_color` keeps working, and gets a full token set back.
func TestThemeLegacyPrimaryColourStillWorks(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Legacy", "legacy@jjne.test", "0714000284", nil, 0)
	c := fix.client

	saved := c.do(http.MethodPut, "/org/branding", map[string]any{
		"theme": map[string]any{"primary_color": "#0A7C4A", "font_id": "archivo"},
	}).mustStatus(t, http.StatusOK, "legacy save")

	tokens := tokensOf(t, saved, "branding", "theme")
	if tokens["primary"] != "#0a7c4a" || tokens["accent"] != "#0a7c4a" {
		t.Errorf("the brand colour did not land: %v", tokens)
	}
	if tokens["paper"] != ledgerTokens["paper"] || tokens["ink"] != ledgerTokens["ink"] {
		t.Errorf("the rest of the palette should stay ledger: %v", tokens)
	}
	if saved.str(t, "branding", "theme", "primary_color") != "#0a7c4a" {
		t.Errorf("primary_color = %q", saved.str(t, "branding", "theme", "primary_color"))
	}
	if saved.str(t, "branding", "theme", "font_id") != "archivo" {
		t.Errorf("font_id = %q", saved.str(t, "branding", "theme", "font_id"))
	}

	t.Run("an unreadable legacy colour is refused too", func(t *testing.T) {
		resp := c.do(http.MethodPut, "/org/branding", map[string]any{
			"theme": map[string]any{"primary_color": "#f4f4ee"},
		}).mustStatus(t, http.StatusBadRequest, "a primary that vanishes into the paper")
		if resp.Body["failures"] == nil {
			t.Errorf("no failures array — body: %s", resp.Raw)
		}
	})
}

// -------------------------------------------------------------- isolation --

// TestThemeIsolation: the theme is written from the session's own org, so
// there is no cross-org write to attempt. What is checked instead is that
// org A repainting itself leaves org B exactly as it was — including on the
// public endpoints, where the only key is a slug anyone can guess.
func TestThemeIsolation(t *testing.T) {
	h := newHarness(t)
	a := h.newOrgWithUnits("Iso Theme A", "iso-theme-a@jjne.test", "0714000285", nil, 0)
	b := h.newOrgWithUnits("Iso Theme B", "iso-theme-b@jjne.test", "0714000286", nil, 0)

	slugB := b.client.do(http.MethodGet, "/org", nil).
		mustStatus(t, http.StatusOK, "org B").str(t, "slug")

	a.client.do(http.MethodPut, "/org/branding", map[string]any{
		"theme": map[string]any{"preset_id": "night_ledger"},
	}).mustStatus(t, http.StatusOK, "org A repaints")

	got := b.client.do(http.MethodGet, "/org/branding", nil).
		mustStatus(t, http.StatusOK, "org B branding")
	assertTokens(t, tokensOf(t, got, "theme"), ledgerTokens, "org B after org A's save")

	public := h.client().do(http.MethodGet, "/public/orgs/"+slugB+"/branding", nil).
		mustStatus(t, http.StatusOK, "org B public branding")
	assertTokens(t, tokensOf(t, public, "theme"), ledgerTokens, "org B's public theme")
}
