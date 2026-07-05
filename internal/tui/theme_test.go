package tui

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	colorful "github.com/lucasb-eyer/go-colorful"
)

// setPalette applies p for the duration of the test and restores the default
// dark palette on cleanup — the styles are package globals, so a test that
// forgets the restore would poison every later test.
func setPalette(t *testing.T, p palette) {
	t.Helper()
	applyPalette(p)
	t.Cleanup(func() { applyPalette(darkPalette) })
}

// contrastRatio is the WCAG contrast ratio between two colors — (L1+0.05) /
// (L2+0.05) with L1 the lighter luminance. Ranges from 1 (identical) to 21
// (black on white). Test-only: these tests assert each accent stays legible
// against its terminal background; nothing enforces it at runtime.
func contrastRatio(a, b colorful.Color) float64 {
	la, lb := relLuminance(a), relLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func TestPaletteWithOverrides(t *testing.T) {
	red := lipgloss.Color("#FF0000")
	dim := lipgloss.Color("#111111")
	got := darkPalette.withOverrides(map[string]color.Color{"primary": red, "fg_dim": dim})
	if got.primary != red {
		t.Errorf("primary override not applied: got %v", got.primary)
	}
	if got.fgDim != dim {
		t.Errorf("fg_dim override not applied: got %v", got.fgDim)
	}
	if got.green != darkPalette.green {
		t.Errorf("an unspecified token (green) should be unchanged")
	}
	if darkPalette.withOverrides(nil).primary != darkPalette.primary {
		t.Errorf("nil overrides should leave the palette unchanged")
	}
}

// bgSeq returns the truecolor SGR background sequence lipgloss emits for c
// ("48;2;r;g;b"), so tests can assert a palette color is applied without
// freezing its literal RGB (which drifts when the palette is tuned).
func bgSeq(c color.Color) string {
	cf, _ := colorful.MakeColor(c)
	r, g, b := cf.RGB255()
	return fmt.Sprintf("48;2;%d;%d;%d", r, g, b)
}

func TestContrastRatio(t *testing.T) {
	white, _ := colorful.Hex("#FFFFFF")
	black, _ := colorful.Hex("#000000")
	// WCAG contrast of pure black on pure white is exactly 21:1.
	if got := contrastRatio(white, black); math.Abs(got-21) > 0.01 {
		t.Fatalf("white/black contrast: want 21, got %v", got)
	}
	// A color has no contrast against itself.
	if got := contrastRatio(white, white); math.Abs(got-1) > 0.01 {
		t.Fatalf("identical colors: want 1, got %v", got)
	}
}

func TestPaletteContrastIsLegible(t *testing.T) {
	// Backgrounds pass through to the terminal, so each palette's foreground
	// colors must be legible against a representative terminal background of its
	// mode: the dark palette against a dark bg, the light palette against white.
	// 3.0 is WCAG AA for large/bold UI text — the floor for muted chrome; accents
	// clear it easily.
	const minContrast = 3.0
	cases := []struct {
		name  string
		p     palette
		refBg string
	}{
		{"dark", darkPalette, "#1E1E1E"},
		{"light", lightPalette, "#FFFFFF"},
	}
	for _, tc := range cases {
		bg, _ := colorful.Hex(tc.refBg)
		tokens := map[string]color.Color{
			"fg": tc.p.fg, "fgDim": tc.p.fgDim, "muted": tc.p.muted,
			"primary": tc.p.primary, "green": tc.p.green, "red": tc.p.red,
			"cyan": tc.p.cyan, "pink": tc.p.pink, "reason": tc.p.reason,
		}
		for name, c := range tokens {
			cf, _ := colorful.MakeColor(c)
			if r := contrastRatio(cf, bg); r < minContrast {
				t.Errorf("%s palette: %s contrast on %s = %.2f, want >= %.2f",
					tc.name, name, tc.refBg, r, minContrast)
			}
		}
	}
}

func TestAgentColorStableAndDistinct(t *testing.T) {
	if agentColor("code") != agentColor("code") {
		t.Fatal("agentColor must be deterministic for the same name")
	}
	// The two default agents must not collapse to the same badge color.
	if agentColor("code") == agentColor("plan") {
		t.Fatal("code and plan should map to distinct colors")
	}
}

func TestReadableOnPicksHigherContrast(t *testing.T) {
	white, _ := colorful.Hex("#FFFFFF")
	black, _ := colorful.Hex("#000000")
	if got := readableOn(white); got != cBlack {
		t.Fatalf("a light background should take black text, got %v", got)
	}
	if got := readableOn(black); got != cWhite {
		t.Fatalf("a dark background should take white text, got %v", got)
	}
}

func TestAgentBadgeRendersNameWithColor(t *testing.T) {
	out := agentBadge("plan")
	if !strings.Contains(stripANSI(out), "plan") {
		t.Fatalf("badge should contain the agent name: %q", stripANSI(out))
	}
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("badge should carry ANSI color styling: %q", out)
	}
}

// TestDiffPairsLegible checks the edit_file diff colors. Unlike the accent tokens,
// they render as fixed foreground-on-background pairs (not against the terminal
// background) and aren't shell3.theme-overridable, so this is their only guard —
// TestPaletteContrastIsLegible deliberately doesn't touch them.
func TestDiffPairsLegible(t *testing.T) {
	const minContrast = 3.0 // WCAG AA for large/bold UI text
	for _, tc := range []struct {
		name string
		p    palette
	}{{"dark", darkPalette}, {"light", lightPalette}} {
		pairs := []struct {
			name   string
			fg, bg color.Color
		}{
			{"add", tc.p.diffAddFg, tc.p.diffAddBg},
			{"del", tc.p.diffDelFg, tc.p.diffDelBg},
			{"meta", tc.p.diffMetaFg, tc.p.diffMetaBg},
		}
		for _, pr := range pairs {
			fg, _ := colorful.MakeColor(pr.fg)
			bg, _ := colorful.MakeColor(pr.bg)
			if r := contrastRatio(fg, bg); r < minContrast {
				t.Errorf("%s palette: diff %s fg-on-bg contrast = %.2f, want >= %.2f",
					tc.name, pr.name, r, minContrast)
			}
		}
	}
}

// TestParseThemeOverride verifies the TUI is the single filter for the theme
// vocabulary: a known token converts to a color, an unknown one is dropped and
// reported as a warning (so a typo surfaces instead of vanishing), and empty input
// yields nothing.
func TestParseThemeOverride(t *testing.T) {
	ov, warns := parseThemeOverride(map[string]string{
		"primary": "#123456",
		"bogus":   "#654321",
	})
	if _, ok := ov["primary"]; !ok {
		t.Error("known token 'primary' should be applied")
	}
	if _, ok := ov["bogus"]; ok {
		t.Error("unknown token 'bogus' should be dropped, not applied")
	}
	warned := false
	for _, w := range warns {
		if strings.Contains(w, "bogus") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("unknown token should produce a warning, got: %v", warns)
	}
	if ov, warns := parseThemeOverride(nil); ov != nil || warns != nil {
		t.Errorf("empty input should yield (nil, nil), got (%v, %v)", ov, warns)
	}
}
