// Package theme is the source of truth for org theming (PLAN2 Phase 12).
//
// The platform ships eight presets — complete, AA-validated token sets — and
// an org may either pick one or override the individual tokens on top of it.
// Both the shipped presets and the validator live here rather than in the
// frontend because three apps consume them: whichever copy the backend refuses
// to store is the copy that is true, so the frontends fetch this one.
//
// A theme is seven colours and a font. Everything derived from them —
// pressed/tinted primaries, the on-primary text colour, faint ink — is
// computed client-side from these values and is never stored: a derived value
// in the database is a value that can disagree with the thing it derives from.
package theme

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Fonts is the whitelist of font ids (the four faces packages/ui ships). It
// mirrors httpserver's brandingFonts; an id outside it is a name no frontend
// can resolve.
//
//nolint:gochecknoglobals // fixed vocabulary, read-only.
var Fonts = []string{"bricolage", "archivo", "instrument", "hanken"}

// DefaultPresetID is what an org with no theme of its own resolves to.
const DefaultPresetID = "ledger"

// Tokens is the themable set: seven colours, each a lower-case `#rrggbb`.
// The order of the fields is the order the advanced panel shows them in.
type Tokens struct {
	Paper    string `json:"paper"`     // page background
	Surface  string `json:"surface"`   // a separate sheet (dialog, panel)
	Ink      string `json:"ink"`       // body text
	InkMuted string `json:"ink_muted"` // secondary text
	Rule     string `json:"rule"`      // ledger rules
	Primary  string `json:"primary"`   // brand / actions
	Accent   string `json:"accent"`    // secondary emphasis
}

// TokenKeys is the canonical key list, used to validate a submitted object is
// the full set rather than a partial one.
//
//nolint:gochecknoglobals // fixed vocabulary, read-only.
var TokenKeys = []string{"paper", "surface", "ink", "ink_muted", "rule", "primary", "accent"}

// Preset is one shipped theme.
type Preset struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Dark   bool   `json:"dark"`
	Tokens Tokens `json:"tokens"`
	FontID string `json:"font_id"`
}

//go:embed presets.json
var presetsJSON []byte

//nolint:gochecknoglobals // loaded once from the embedded file, read-only.
var (
	presets    []Preset
	presetByID map[string]Preset
)

//nolint:gochecknoinits // the embedded presets are a constant; parse them once.
func init() {
	if err := json.Unmarshal(presetsJSON, &presets); err != nil {
		panic("theme: presets.json is not valid: " + err.Error())
	}
	presetByID = make(map[string]Preset, len(presets))
	for _, p := range presets {
		presetByID[p.ID] = p
	}
}

// Presets returns the shipped presets in display order.
func Presets() []Preset {
	out := make([]Preset, len(presets))
	copy(out, presets)
	return out
}

// PresetByID returns a preset and whether it exists.
func PresetByID(id string) (Preset, bool) {
	p, ok := presetByID[strings.ToLower(strings.TrimSpace(id))]
	return p, ok
}

// ------------------------------------------------------------- contrast --

var hexRe = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// Normalize lower-cases and trims a colour. It does not validate it.
func Normalize(c string) string { return strings.ToLower(strings.TrimSpace(c)) }

// IsHex reports whether c is a canonical 6-digit lower-case hex colour.
func IsHex(c string) bool { return hexRe.MatchString(c) }

// channel converts one sRGB byte to its linear-light value (WCAG 2.x).
func channel(v float64) float64 {
	c := v / 255
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// Luminance is the WCAG relative luminance of a `#rrggbb` colour. An
// unparsable colour is 0 — callers validate the format first.
func Luminance(hex string) float64 {
	hex = Normalize(hex)
	if !IsHex(hex) {
		return 0
	}
	n, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		return 0
	}
	r := float64((n >> 16) & 0xff)
	g := float64((n >> 8) & 0xff)
	b := float64(n & 0xff)
	return 0.2126*channel(r) + 0.7152*channel(g) + 0.0722*channel(b)
}

// Contrast is the WCAG contrast ratio between two colours, 1.0–21.0.
func Contrast(a, b string) float64 {
	la, lb := Luminance(a), Luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// OnPrimaryLight and OnPrimaryDark are the two candidates for text sitting on
// the primary colour. The UI picks between them with exactly the rule below,
// so the validator must judge the same pair the user will see.
const (
	OnPrimaryLight = "#ffffff"
	OnPrimaryDark  = "#1c1917"
	// onPrimaryCut is the luminance above which primary is "light enough" to
	// need dark text on it.
	onPrimaryCut = 0.4
)

// OnPrimary is the text colour the UI puts on `primary`.
func OnPrimary(primary string) string {
	if Luminance(primary) > onPrimaryCut {
		return OnPrimaryDark
	}
	return OnPrimaryLight
}

// IsDark reports whether a token set reads as a dark theme. The page
// background decides it: a dark theme is one whose paper is darker than its
// ink, whatever the accents do.
func IsDark(t Tokens) bool { return Luminance(t.Paper) < 0.5 }

// ------------------------------------------------------------ validation --

// Failure is one rule a candidate theme breaks. Contrast failures carry the
// measured ratio and the minimum; format failures carry a message instead.
type Failure struct {
	Pair    string  `json:"pair"`
	Ratio   float64 `json:"ratio"`
	Minimum float64 `json:"minimum"`
	Message string  `json:"message,omitempty"`
}

// contrastRule is one pair the theme must clear.
type contrastRule struct {
	pair    string
	fg      func(Tokens) string
	bg      func(Tokens) string
	minimum float64
	// why is the human sentence behind the number, for the message.
	why string
}

// Minimums (SPEC §2.0, PLAN2 Phase 12):
//
//   - 4.5 — WCAG AA for body text. Everything a renter or a landlord actually
//     reads sits at this bar, on both the page and a sheet, because a card is
//     a different background and a theme that only works on one of them is
//     half broken.
//   - 3.0 — AA for non-text UI: the primary must be findable against the page
//     even when nothing is written on it.
//   - 1.2 — not a WCAG number. A ledger rule is decoration until it is
//     visible, and below this it simply is not there.
const (
	minText      = 4.5
	minComponent = 3.0
	minRule      = 1.2
)

//nolint:gochecknoglobals // fixed rule table, read-only.
var contrastRules = []contrastRule{
	{"ink/paper", func(t Tokens) string { return t.Ink }, func(t Tokens) string { return t.Paper },
		minText, "body text on the page"},
	{"ink/surface", func(t Tokens) string { return t.Ink }, func(t Tokens) string { return t.Surface },
		minText, "body text on a sheet"},
	{"ink_muted/paper", func(t Tokens) string { return t.InkMuted }, func(t Tokens) string { return t.Paper },
		minText, "secondary text on the page"},
	{"ink_muted/surface", func(t Tokens) string { return t.InkMuted }, func(t Tokens) string { return t.Surface },
		minText, "secondary text on a sheet"},
	{"on_primary/primary", func(t Tokens) string { return OnPrimary(t.Primary) },
		func(t Tokens) string { return t.Primary }, minText, "button label on the brand colour"},
	{"primary/paper", func(t Tokens) string { return t.Primary }, func(t Tokens) string { return t.Paper },
		minComponent, "buttons and links against the page"},
	{"rule/paper", func(t Tokens) string { return t.Rule }, func(t Tokens) string { return t.Paper },
		minRule, "ledger rules against the page"},
}

// tokenFields pairs each key with its accessor, for format validation.
func tokenFields(t Tokens) []struct {
	key   string
	value string
} {
	return []struct {
		key   string
		value string
	}{
		{"paper", t.Paper}, {"surface", t.Surface}, {"ink", t.Ink}, {"ink_muted", t.InkMuted},
		{"rule", t.Rule}, {"primary", t.Primary}, {"accent", t.Accent},
	}
}

// Validate returns every rule a candidate theme breaks: bad hex first, then
// the unknown font, then the contrast pairs. An empty slice means the theme is
// storable.
//
// Format failures short-circuit the contrast pass: a ratio computed against a
// colour that does not parse is a number that means nothing, and reporting it
// beside a real ratio would teach the user to distrust both.
func Validate(t Tokens, fontID string) []Failure {
	var out []Failure
	for _, f := range tokenFields(t) {
		if !IsHex(Normalize(f.value)) {
			out = append(out, Failure{
				Pair:    f.key,
				Message: "must be a hex colour like #1b4db1",
			})
		}
	}
	if !slices.Contains(Fonts, Normalize(fontID)) {
		out = append(out, Failure{
			Pair:    "font_id",
			Message: "must be one of: " + strings.Join(Fonts, ", "),
		})
	}
	if len(out) > 0 {
		return out
	}
	norm := NormalizeTokens(t)
	for _, rule := range contrastRules {
		ratio := round2(Contrast(rule.fg(norm), rule.bg(norm)))
		if ratio < rule.minimum {
			out = append(out, Failure{
				Pair:    rule.pair,
				Ratio:   ratio,
				Minimum: rule.minimum,
				Message: fmt.Sprintf("%s: needs at least %s:1 contrast, this is %s:1",
					rule.why, trimFloat(rule.minimum), trimFloat(ratio)),
			})
		}
	}
	return out
}

// NormalizeTokens lower-cases and trims every colour in a set.
func NormalizeTokens(t Tokens) Tokens {
	return Tokens{
		Paper: Normalize(t.Paper), Surface: Normalize(t.Surface), Ink: Normalize(t.Ink),
		InkMuted: Normalize(t.InkMuted), Rule: Normalize(t.Rule),
		Primary: Normalize(t.Primary), Accent: Normalize(t.Accent),
	}
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func trimFloat(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// ------------------------------------------------------------- resolution --

// Stored is an `org_themes` row: a preset id, an explicit token set, a font —
// any of which may be absent.
type Stored struct {
	PresetID *string
	Tokens   *Tokens
	FontID   *string
}

// Legacy is the Phase 4 `org_branding.theme` blob: one brand colour and a
// font, from before the token model existed.
type Legacy struct {
	PrimaryColor string
	FontID       string
}

// Source names where a resolved theme came from, so the tenant UI can say
// "Night ledger" rather than "custom" when nothing has been overridden.
const (
	SourceDefault = "default" // no row, no legacy colour: the ledger preset
	SourcePreset  = "preset"  // a preset, unmodified
	SourceCustom  = "custom"  // an explicit token set
	SourceLegacy  = "legacy"  // a Phase 4 primary_color, mapped onto ledger
)

// Resolved is what every endpoint serves and every app applies.
type Resolved struct {
	PresetID *string `json:"preset_id"`
	Tokens   Tokens  `json:"tokens"`
	FontID   string  `json:"font_id"`
	Dark     bool    `json:"dark"`
	Source   string  `json:"source"`
}

// Default is the ledger preset, resolved.
func Default() Resolved {
	p := presetByID[DefaultPresetID]
	id := p.ID
	return Resolved{PresetID: &id, Tokens: p.Tokens, FontID: p.FontID, Dark: p.Dark, Source: SourceDefault}
}

// FromPrimary builds a token set from the ledger preset with `primary` and
// `accent` replaced. It is how a Phase 4 brand colour becomes a Phase 12 theme
// — and how the legacy `PUT {theme:{primary_color}}` still works.
func FromPrimary(primary string) Tokens {
	t := presetByID[DefaultPresetID].Tokens
	c := Normalize(primary)
	t.Primary = c
	t.Accent = c
	return t
}

// Resolve turns whatever an org has into the one theme its apps render.
//
// Precedence, highest first:
//
//  1. explicit tokens on the `org_themes` row — the advanced override;
//  2. the preset named by that row;
//  3. a legacy `org_branding.theme.primary_color` — ledger with that brand
//     colour, so an org that set one in Phase 4 keeps its colour without
//     anybody re-saving;
//  4. the ledger preset.
//
// `preset_id` survives an override: it is the label the tenant UI shows as the
// base the org started from ("Night ledger, edited"). The font is orthogonal
// to all of this — it never changes which of the four sources a theme has.
func Resolve(stored *Stored, legacy *Legacy) Resolved {
	switch {
	case stored != nil:
		out, hasPreset := Default(), false
		if stored.PresetID != nil {
			if p, ok := PresetByID(*stored.PresetID); ok {
				id := p.ID
				hasPreset = true
				out = Resolved{
					PresetID: &id, Tokens: p.Tokens, FontID: p.FontID,
					Dark: p.Dark, Source: SourcePreset,
				}
			}
		}
		if stored.Tokens != nil {
			out.Tokens = NormalizeTokens(*stored.Tokens)
			out.Dark = IsDark(out.Tokens)
			out.Source = SourceCustom
			if !hasPreset {
				// Nothing to label the override as a variation of.
				out.PresetID = nil
			}
		}
		if stored.FontID != nil {
			if f := Normalize(*stored.FontID); slices.Contains(Fonts, f) {
				out.FontID = f
			}
		}
		return out

	case legacy != nil && IsHex(Normalize(legacy.PrimaryColor)):
		out := Default()
		out.Tokens = FromPrimary(legacy.PrimaryColor)
		out.Dark = IsDark(out.Tokens)
		out.Source = SourceLegacy
		if f := Normalize(legacy.FontID); slices.Contains(Fonts, f) {
			out.FontID = f
		}
		return out

	default:
		return Default()
	}
}
