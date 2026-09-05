package theme

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestPresetsMatchUICopy guards against drift between presets.json (the
// source of truth served by GET /themes/presets) and the offline fallback
// copy bundled in packages/ui/src/theme.ts.
func TestPresetsMatchUICopy(t *testing.T) {
	path := filepath.Join("..", "..", "..", "packages", "ui", "src", "theme.ts")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("ui copy not reachable: %v", err)
	}
	src := string(raw)
	start := strings.Index(src, "export const PRESETS")
	if start < 0 {
		t.Fatal("PRESETS block not found in theme.ts")
	}
	src = src[start:]
	for _, p := range Presets() {
		i := strings.Index(src, "id: '"+p.ID+"'")
		if i < 0 {
			t.Errorf("preset %s missing from ui copy", p.ID)
			continue
		}
		block := src[i:]
		if j := strings.Index(block, "\n  },"); j > 0 {
			block = block[:j]
		}
		want := map[string]string{
			"paper": p.Tokens.Paper, "surface": p.Tokens.Surface, "ink": p.Tokens.Ink,
			"ink_muted": p.Tokens.InkMuted, "rule": p.Tokens.Rule, "primary": p.Tokens.Primary, "accent": p.Tokens.Accent,
		}
		for k, v := range want {
			re := regexp.MustCompile(k + `:\s*'(#[0-9a-f]{6})'`)
			m := re.FindStringSubmatch(block)
			if m == nil || m[1] != v {
				got := "missing"
				if m != nil {
					got = m[1]
				}
				t.Errorf("%s.%s: ui=%s server=%s", p.ID, k, got, v)
			}
		}
		if !strings.Contains(block, "font_id: '"+p.FontID+"'") {
			t.Errorf("%s.font_id: ui copy differs from server %s", p.ID, p.FontID)
		}
	}
}
