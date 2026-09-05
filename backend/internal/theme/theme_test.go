package theme_test

import (
	"math"
	"testing"

	"tms/backend/internal/theme"
)

func ptr[T any](v T) *T { return &v }

// ledger is the preset every other resolution falls back to.
func ledger(t *testing.T) theme.Preset {
	t.Helper()
	p, ok := theme.PresetByID(theme.DefaultPresetID)
	if !ok {
		t.Fatal("the ledger preset is missing")
	}
	return p
}

// ---------------------------------------------------------------- presets --

// TestPresetsShipped pins the catalogue: eight presets, the ids and display
// names the tenant UI is built against, exactly one dark one.
func TestPresetsShipped(t *testing.T) {
	want := []struct{ id, name string }{
		{"ledger", "Ledger"},
		{"night_ledger", "Night ledger"},
		{"warm_paper", "Warm paper"},
		{"cool_slate", "Cool slate"},
		{"forest", "Forest"},
		{"ocean", "Ocean"},
		{"high_contrast", "High contrast"},
		{"minimal_white", "Minimal white"},
	}
	got := theme.Presets()
	if len(got) != len(want) {
		t.Fatalf("presets: got %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].ID != w.id || got[i].Name != w.name {
			t.Errorf("preset %d: got %q/%q, want %q/%q", i, got[i].ID, got[i].Name, w.id, w.name)
		}
	}
}

// TestEveryPresetValidates is the guarantee behind shipping them: a preset a
// landlord picks in one click must never be a theme the same landlord could
// not have saved by hand.
func TestEveryPresetValidates(t *testing.T) {
	for _, p := range theme.Presets() {
		if fails := theme.Validate(p.Tokens, p.FontID); len(fails) > 0 {
			t.Errorf("preset %s fails validation: %+v", p.ID, fails)
		}
		if theme.IsDark(p.Tokens) != p.Dark {
			t.Errorf("preset %s: dark=%v but its paper says %v", p.ID, p.Dark, theme.IsDark(p.Tokens))
		}
	}
}

// TestLedgerMatchesTokensCSS pins the default preset to the values
// packages/ui/src/tokens.css ships, so the served theme and the CSS fallback
// cannot drift apart into two different "defaults".
func TestLedgerMatchesTokensCSS(t *testing.T) {
	want := theme.Tokens{
		Paper: "#fbfbf7", Surface: "#ffffff", Ink: "#1c2b5a", InkMuted: "#4a5680",
		Rule: "#cbd3e8", Primary: "#2b4fd0", Accent: "#2b4fd0",
	}
	p := ledger(t)
	if p.Tokens != want {
		t.Errorf("ledger tokens drifted from tokens.css:\n got %+v\nwant %+v", p.Tokens, want)
	}
	if p.FontID != "bricolage" {
		t.Errorf("ledger font: got %q, want bricolage", p.FontID)
	}
}

// ------------------------------------------------------------- contrast --

func TestContrast(t *testing.T) {
	cases := []struct {
		a, b string
		want float64
	}{
		{"#ffffff", "#000000", 21},
		{"#000000", "#ffffff", 21},
		{"#ffffff", "#ffffff", 1},
		{"#767676", "#ffffff", 4.54}, // the canonical AA-borderline grey
	}
	for _, c := range cases {
		got := theme.Contrast(c.a, c.b)
		if math.Abs(got-c.want) > 0.01 {
			t.Errorf("Contrast(%s,%s) = %.3f, want %.2f", c.a, c.b, got, c.want)
		}
	}
}

// TestOnPrimary checks the rule the UI uses to pick button-label colour: the
// validator must judge the pair the user will actually see.
func TestOnPrimary(t *testing.T) {
	cases := map[string]string{
		"#2b4fd0": theme.OnPrimaryLight, // dark blue → white label
		"#000000": theme.OnPrimaryLight,
		"#96b4ff": theme.OnPrimaryDark, // pale blue → dark label
		"#ffffff": theme.OnPrimaryDark,
		"#ffd400": theme.OnPrimaryDark,
	}
	for primary, want := range cases {
		if got := theme.OnPrimary(primary); got != want {
			t.Errorf("OnPrimary(%s) = %s, want %s", primary, got, want)
		}
	}
}

// ------------------------------------------------------------ validation --

// TestValidateTable walks one deliberately broken pair at a time, so a failure
// names the rule that regressed rather than "the theme is wrong".
func TestValidateTable(t *testing.T) {
	base := ledger(t).Tokens

	with := func(mutate func(*theme.Tokens)) theme.Tokens {
		out := base
		mutate(&out)
		return out
	}

	cases := []struct {
		name     string
		tokens   theme.Tokens
		fontID   string
		wantPair string // "" means: must pass
	}{
		{name: "the ledger preset passes", tokens: base, fontID: "bricolage"},
		{
			name:     "grey ink on white paper is unreadable",
			tokens:   with(func(t *theme.Tokens) { t.Ink = "#a0a0a0" }),
			fontID:   "bricolage",
			wantPair: "ink/paper",
		},
		{
			name:     "ink that only clears the page still fails on a sheet",
			tokens:   with(func(t *theme.Tokens) { t.Surface = "#1c2b5a"; t.Paper = "#ffffff" }),
			fontID:   "bricolage",
			wantPair: "ink/surface",
		},
		{
			name:     "muted ink one shade too light",
			tokens:   with(func(t *theme.Tokens) { t.InkMuted = "#9aa3bd" }),
			fontID:   "bricolage",
			wantPair: "ink_muted/paper",
		},
		{
			name:     "a pale primary cannot carry white text",
			tokens:   with(func(t *theme.Tokens) { t.Primary = "#7c9cff"; t.Accent = "#7c9cff" }),
			fontID:   "bricolage",
			wantPair: "on_primary/primary",
		},
		{
			name:     "a primary that disappears into the page",
			tokens:   with(func(t *theme.Tokens) { t.Primary = "#e8e8e2"; t.Accent = "#e8e8e2" }),
			fontID:   "bricolage",
			wantPair: "primary/paper",
		},
		{
			name:     "an invisible ledger rule",
			tokens:   with(func(t *theme.Tokens) { t.Rule = "#fbfbf6" }),
			fontID:   "bricolage",
			wantPair: "rule/paper",
		},
		{
			name:     "a colour that is not a colour",
			tokens:   with(func(t *theme.Tokens) { t.Rule = "rebeccapurple" }),
			fontID:   "bricolage",
			wantPair: "rule",
		},
		{
			name:     "shorthand hex is refused",
			tokens:   with(func(t *theme.Tokens) { t.Paper = "#fff" }),
			fontID:   "bricolage",
			wantPair: "paper",
		},
		{
			name:     "an unknown font",
			tokens:   base,
			fontID:   "comic-sans",
			wantPair: "font_id",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fails := theme.Validate(c.tokens, c.fontID)
			if c.wantPair == "" {
				if len(fails) > 0 {
					t.Fatalf("expected no failures, got %+v", fails)
				}
				return
			}
			var found *theme.Failure
			for i := range fails {
				if fails[i].Pair == c.wantPair {
					found = &fails[i]
				}
			}
			if found == nil {
				t.Fatalf("expected a %q failure, got %+v", c.wantPair, fails)
			}
			if found.Minimum > 0 && found.Ratio >= found.Minimum {
				t.Errorf("%s reported ratio %.2f which is not below the minimum %.2f",
					found.Pair, found.Ratio, found.Minimum)
			}
		})
	}
}

// TestValidateFormatShortCircuits: a ratio measured against a colour that does
// not parse is a meaningless number, so it is never reported beside a real one.
func TestValidateFormatShortCircuits(t *testing.T) {
	broken := ledger(t).Tokens
	broken.Paper = "not-a-colour"
	fails := theme.Validate(broken, "bricolage")
	if len(fails) != 1 || fails[0].Pair != "paper" {
		t.Fatalf("expected only the format failure, got %+v", fails)
	}
}

// ------------------------------------------------------------ resolution --

func TestResolvePrecedence(t *testing.T) {
	base := ledger(t)
	night, ok := theme.PresetByID("night_ledger")
	if !ok {
		t.Fatal("night_ledger is missing")
	}
	custom := theme.Tokens{
		Paper: "#ffffff", Surface: "#ffffff", Ink: "#101010", InkMuted: "#454545",
		Rule: "#d0d0d0", Primary: "#7a2e2e", Accent: "#7a2e2e",
	}

	t.Run("nothing at all resolves to ledger", func(t *testing.T) {
		got := theme.Resolve(nil, nil)
		if got.Tokens != base.Tokens || got.Source != theme.SourceDefault || got.Dark {
			t.Fatalf("got %+v", got)
		}
		if got.PresetID == nil || *got.PresetID != "ledger" {
			t.Fatalf("preset_id: got %v", got.PresetID)
		}
	})

	t.Run("a preset with no tokens resolves to the preset", func(t *testing.T) {
		got := theme.Resolve(&theme.Stored{PresetID: ptr("night_ledger")}, nil)
		if got.Tokens != night.Tokens || got.Source != theme.SourcePreset || !got.Dark {
			t.Fatalf("got %+v", got)
		}
		if got.FontID != night.FontID {
			t.Errorf("font: got %q, want %q", got.FontID, night.FontID)
		}
	})

	t.Run("tokens beat the preset but keep its label", func(t *testing.T) {
		got := theme.Resolve(&theme.Stored{PresetID: ptr("night_ledger"), Tokens: &custom}, nil)
		if got.Tokens != custom {
			t.Fatalf("tokens: got %+v", got.Tokens)
		}
		if got.Source != theme.SourceCustom {
			t.Errorf("source: got %q", got.Source)
		}
		if got.PresetID == nil || *got.PresetID != "night_ledger" {
			t.Errorf("the base label was lost: %v", got.PresetID)
		}
		if got.Dark {
			t.Errorf("a white-paper override of a dark preset is not dark")
		}
	})

	t.Run("tokens with no preset have no label", func(t *testing.T) {
		got := theme.Resolve(&theme.Stored{Tokens: &custom}, nil)
		if got.PresetID != nil || got.Source != theme.SourceCustom {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("an unknown preset id falls back to ledger", func(t *testing.T) {
		got := theme.Resolve(&theme.Stored{PresetID: ptr("chartreuse")}, nil)
		if got.Tokens != base.Tokens {
			t.Fatalf("got %+v", got.Tokens)
		}
	})

	t.Run("the font is orthogonal to the palette", func(t *testing.T) {
		got := theme.Resolve(&theme.Stored{PresetID: ptr("ledger"), FontID: ptr("hanken")}, nil)
		if got.FontID != "hanken" || got.Tokens != base.Tokens || got.Source != theme.SourcePreset {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("an unknown stored font keeps the preset's", func(t *testing.T) {
		got := theme.Resolve(&theme.Stored{PresetID: ptr("ledger"), FontID: ptr("papyrus")}, nil)
		if got.FontID != base.FontID {
			t.Fatalf("font: got %q", got.FontID)
		}
	})

	t.Run("a row beats a legacy colour", func(t *testing.T) {
		got := theme.Resolve(&theme.Stored{PresetID: ptr("night_ledger")},
			&theme.Legacy{PrimaryColor: "#aa0000", FontID: "archivo"})
		if got.Tokens != night.Tokens {
			t.Fatalf("the legacy colour won: %+v", got.Tokens)
		}
	})
}

// TestResolveLegacy: an org that set a brand colour in Phase 4 keeps it,
// without anybody re-saving anything.
func TestResolveLegacy(t *testing.T) {
	base := ledger(t)
	got := theme.Resolve(nil, &theme.Legacy{PrimaryColor: "#1B4DB1", FontID: "archivo"})

	if got.Source != theme.SourceLegacy {
		t.Errorf("source: got %q, want legacy", got.Source)
	}
	if got.Tokens.Primary != "#1b4db1" || got.Tokens.Accent != "#1b4db1" {
		t.Errorf("the legacy colour did not land: %+v", got.Tokens)
	}
	if got.Tokens.Paper != base.Tokens.Paper || got.Tokens.Ink != base.Tokens.Ink {
		t.Errorf("the rest of the palette should be ledger: %+v", got.Tokens)
	}
	if got.FontID != "archivo" {
		t.Errorf("font: got %q, want archivo", got.FontID)
	}

	t.Run("a junk legacy colour resolves to the default", func(t *testing.T) {
		got := theme.Resolve(nil, &theme.Legacy{PrimaryColor: "blue", FontID: ""})
		if got.Source != theme.SourceDefault || got.Tokens != base.Tokens {
			t.Fatalf("got %+v", got)
		}
	})
}
