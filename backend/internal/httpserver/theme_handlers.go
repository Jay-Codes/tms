package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/theme"
	"tms/backend/internal/validate"
)

// Theming v2 (PLAN2 Phase 12). The token set and the presets live in
// internal/theme; this file is the transport: one public catalogue endpoint,
// the resolution every branding response goes through, and the validation the
// tenant screen's save runs into.

// themeBlock is the `theme` object every branding endpoint returns. It carries
// the resolved token set *and* the two Phase 4 fields, because the enduser and
// tenant apps in the field still read `primary_color`/`font_id` and a theme
// that silently stops answering them is a theme that unbrands them.
type themeBlock struct {
	PresetID *string      `json:"preset_id"`
	Tokens   theme.Tokens `json:"tokens"`
	FontID   string       `json:"font_id"`
	Dark     bool         `json:"dark"`
	Source   string       `json:"source"`
	// PrimaryColor is tokens.primary under its old name (legacy clients).
	PrimaryColor string `json:"primary_color"`
}

func toThemeBlock(r theme.Resolved) themeBlock {
	return themeBlock{
		PresetID: r.PresetID, Tokens: r.Tokens, FontID: r.FontID, Dark: r.Dark,
		Source: r.Source, PrimaryColor: r.Tokens.Primary,
	}
}

// legacyDefaultPrimary is the colour migration 000002 gives every new org's
// `org_branding.theme`. An org still carrying it has not chosen anything, so
// it resolves to the ledger preset rather than to a "legacy" theme built
// around a colour nobody picked.
const legacyDefaultPrimary = "#1b4db1"

// storedTheme reads an org's `org_themes` row. A missing row is (nil, nil):
// the org has never opened the theme screen.
func (s *Server) storedTheme(ctx context.Context, orgID pgtype.UUID) *theme.Stored {
	row, err := s.q.GetOrgTheme(ctx, orgID)
	if err != nil {
		return nil
	}
	out := theme.Stored{PresetID: row.PresetID, FontID: row.FontID}
	if tokens, ok := decodeTokens(row.Tokens); ok {
		out.Tokens = &tokens
	}
	return &out
}

// decodeTokens reads the row's JSONB. `{}` — what a plain preset stores — and
// any partial object are "no override": a half-set of tokens could only paint
// half a screen.
func decodeTokens(raw []byte) (theme.Tokens, bool) {
	var t theme.Tokens
	if len(raw) == 0 {
		return t, false
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return theme.Tokens{}, false
	}
	for _, v := range []string{t.Paper, t.Surface, t.Ink, t.InkMuted, t.Rule, t.Primary, t.Accent} {
		if !theme.IsHex(theme.Normalize(v)) {
			return theme.Tokens{}, false
		}
	}
	return theme.NormalizeTokens(t), true
}

// legacyTheme reads the Phase 4 `org_branding.theme` blob as a legacy choice,
// or nil when it holds the untouched bootstrap default.
func legacyTheme(raw []byte) *theme.Legacy {
	old := parseTheme(raw)
	if theme.Normalize(old.PrimaryColor) == legacyDefaultPrimary {
		return nil
	}
	return &theme.Legacy{PrimaryColor: old.PrimaryColor, FontID: old.FontID}
}

// resolveTheme is the one place a stored theme becomes a rendered one: the
// `org_themes` row if there is one, the Phase 4 colour if there is not, the
// ledger preset if there is neither.
func (s *Server) resolveTheme(ctx context.Context, orgID pgtype.UUID, brandingTheme []byte) themeBlock {
	return toThemeBlock(theme.Resolve(s.storedTheme(ctx, orgID), legacyTheme(brandingTheme)))
}

// ---------------------------------------------------- GET /themes/presets --

// handleThemePresets serves the shipped catalogue. It is public and needs no
// org: a landlord picking a theme is often doing it on a login-adjacent
// screen, and the presets are the same eight for everyone.
func (s *Server) handleThemePresets(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]any{"presets": theme.Presets()})
}

// -------------------------------------------------------- PUT validation --

// themeInput is the `theme` object PUT /org/branding accepts. Every field is
// optional and each is a pointer, so "not mentioned" and "set to empty" stay
// distinguishable.
type themeInput struct {
	PresetID *string            `json:"preset_id"`
	Tokens   map[string]*string `json:"tokens"`
	FontID   *string            `json:"font_id"`
	// PrimaryColor is the Phase 4 field. On its own it still works: it maps
	// to the ledger palette with that brand colour, validated like any other
	// custom set.
	PrimaryColor *string `json:"primary_color"`
}

// themeDecision is what a valid input resolves to for storage.
type themeDecision struct {
	presetID *string
	tokens   *theme.Tokens
	fontID   *string
	resolved theme.Resolved
}

// parseThemeInput turns the submitted object into what will be stored, or
// returns the failures that stop it. Field-level problems (an unknown preset,
// a partial token set, a font nobody ships) go through validate.Fields as
// everywhere else, and every one of them is collected before answering — a
// form that reveals its second mistake only after you fix the first is a form
// people fill in twice. Contrast failures come back separately, as the
// structured list the tenant screen draws its badges from.
func parseThemeInput(f *validate.Fields, in themeInput, current theme.Resolved) (themeDecision, []theme.Failure) {
	out := themeDecision{}

	// 1. The base: a preset, if one is named.
	base := current
	if in.PresetID != nil {
		id := theme.Normalize(*in.PresetID)
		if p, ok := theme.PresetByID(id); ok {
			out.presetID = &p.ID
			base = theme.Resolved{PresetID: &p.ID, Tokens: p.Tokens, FontID: p.FontID, Dark: p.Dark}
		} else {
			f.Add("theme.preset_id", "unknown preset "+strconv.Quote(id))
		}
	} else if current.PresetID != nil {
		id := *current.PresetID
		out.presetID = &id
	}

	// 2. The palette: an explicit token set, a legacy primary colour, or the
	//    base's own.
	tokens := base.Tokens
	custom := false
	switch {
	case in.Tokens != nil:
		if parsed, ok := parseTokenSet(f, in.Tokens); ok {
			tokens, custom = parsed, true
		}
	case in.PrimaryColor != nil:
		colour := theme.Normalize(*in.PrimaryColor)
		if theme.IsHex(colour) {
			// A brand colour on its own keeps the rest of the base palette, so
			// a Phase 4 client can still recolour an org without knowing the
			// token model exists.
			tokens.Primary = colour
			tokens.Accent = colour
			custom = true
		} else {
			f.Add("theme.primary_color", "must be a hex colour like #1b4db1")
		}
	}

	// 3. The font. theme.Validate checks it too, but a wrong font is a field
	//    mistake, not a contrast one, and belongs beside the others in
	//    `errors`.
	font := base.FontID
	if in.FontID != nil {
		font = theme.Normalize(*in.FontID)
		if !slices.Contains(theme.Fonts, font) {
			f.Add("theme.font_id", "must be one of: "+strings.Join(theme.Fonts, ", "))
		}
	}

	if !f.Empty() {
		return out, nil
	}
	if fails := theme.Validate(tokens, font); len(fails) > 0 {
		return out, fails
	}

	if custom {
		norm := theme.NormalizeTokens(tokens)
		out.tokens = &norm
	}
	out.fontID = &font
	out.resolved = theme.Resolve(&theme.Stored{
		PresetID: out.presetID, Tokens: out.tokens, FontID: out.fontID,
	}, nil)
	return out, nil
}

// parseTokenSet requires the complete set: seven keys, no more, no fewer. A
// partial override would leave the missing tokens to whatever the app last
// had, which is a theme nobody can reason about.
func parseTokenSet(f *validate.Fields, raw map[string]*string) (theme.Tokens, bool) {
	var out theme.Tokens
	for key := range raw {
		if !contains(theme.TokenKeys, key) {
			f.Add("theme.tokens", "unknown token "+strconv.Quote(key)+
				"; the set is: "+strings.Join(theme.TokenKeys, ", "))
			return out, false
		}
	}
	var missing []string
	for _, key := range theme.TokenKeys {
		v, ok := raw[key]
		if !ok || v == nil || strings.TrimSpace(*v) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		f.Add("theme.tokens", "the whole set is required; missing: "+strings.Join(missing, ", "))
		return out, false
	}
	out = theme.Tokens{
		Paper: theme.Normalize(*raw["paper"]), Surface: theme.Normalize(*raw["surface"]),
		Ink: theme.Normalize(*raw["ink"]), InkMuted: theme.Normalize(*raw["ink_muted"]),
		Rule: theme.Normalize(*raw["rule"]), Primary: theme.Normalize(*raw["primary"]),
		Accent: theme.Normalize(*raw["accent"]),
	}
	return out, true
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// writeThemeFailures answers a rejected theme: an RFC 7807 document whose
// `errors` says which field is wrong in one sentence, plus the machine-
// readable `failures` list the advanced panel highlights the offending swatch
// from. Both, because the same response serves a form and a colour picker.
func writeThemeFailures(w http.ResponseWriter, fails []theme.Failure) {
	noun := "failures"
	if len(fails) == 1 {
		noun = "failure"
	}
	summary := strconv.Itoa(len(fails)) + " contrast " + noun
	field := "theme.tokens"
	for _, fail := range fails {
		if fail.Minimum == 0 {
			// A format or font failure, not a contrast one.
			summary = fails[0].Message
			if fails[0].Pair == "font_id" {
				field = "theme.font_id"
			}
			break
		}
	}
	body := map[string]any{
		"type":     "about:blank",
		"title":    "invalid theme",
		"status":   http.StatusBadRequest,
		"detail":   "the theme was rejected: " + summary,
		"errors":   map[string]string{field: summary},
		"failures": fails,
	}
	w.Header().Set("Content-Type", ProblemContentType)
	w.WriteHeader(http.StatusBadRequest)
	WriteRawJSON(w, body)
}
