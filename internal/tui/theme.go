package tui

import (
	"fmt"
	"hash/fnv"
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
	colorful "github.com/lucasb-eyer/go-colorful"
)

// palette is shell3's full set of foreground-role colors. Backgrounds pass
// through to the terminal — shell3 never paints a canvas; instead these colors
// adapt to whether the terminal is light or dark (see applyPalette and the
// sensing in model.go), which is what keeps text legible on both. Pills reuse an
// accent as a *background* with readableOn() text, so one adaptive accent serves
// both the text and the pill role.
type palette struct {
	// light marks the light-terminal palette. It drives the markdown base theme
	// (glamour's light vs dark chrome) so that choice follows the sensed terminal,
	// not an overridable fg color. withOverrides copies it through unchanged.
	light bool

	fg, fgDim, muted color.Color // body / secondary / chrome text

	primary color.Color // brand yellow — prompt, edit_file, headings, palette input
	green   color.Color // success — bash, safety-off "!" pill
	red     color.Color // error / danger — bash_bg, errors, ctrl-c
	cyan    color.Color // info — palette commands, bg count
	pink    color.Color // misc tools
	reason  color.Color // reasoning / help section headers

	// edit_file diff preview (git-diff-style): foreground + background pairs.
	diffAddFg, diffAddBg   color.Color
	diffDelFg, diffDelBg   color.Color
	diffMetaFg, diffMetaBg color.Color
}

// darkPalette is the built-in for dark terminals (shell3's historical look).
var darkPalette = palette{
	fg: lipgloss.Color("#E5E7EB"), fgDim: lipgloss.Color("#9CA3AF"), muted: lipgloss.Color("#6B7280"),
	primary:   lipgloss.Color("#EAB308"),
	green:     lipgloss.Color("#78AA78"),
	red:       lipgloss.Color("#DC2626"),
	cyan:      lipgloss.Color("#5BB6C9"),
	pink:      lipgloss.Color("#D98FB8"),
	reason:    lipgloss.Color("#87A58C"),
	diffAddFg: lipgloss.Color("#B4E6B4"), diffAddBg: lipgloss.Color("#143C14"),
	diffDelFg: lipgloss.Color("#F0B4B4"), diffDelBg: lipgloss.Color("#461414"),
	diffMetaFg: lipgloss.Color("#9CA3AF"), diffMetaBg: lipgloss.Color("#4A4018"),
}

// lightPalette is the built-in for light terminals. PROVISIONAL — the accents are
// darkened/saturated so they read as text on a light background (and readableOn
// still picks legible pill text), but the exact hexes have not been eyeballed on a
// real light terminal yet; verify there before relying on them.
var lightPalette = palette{
	light: true,
	fg:    lipgloss.Color("#1F2328"), fgDim: lipgloss.Color("#57606A"), muted: lipgloss.Color("#6E7781"),
	primary:   lipgloss.Color("#9A6700"),
	green:     lipgloss.Color("#1A7F37"),
	red:       lipgloss.Color("#CF222E"),
	cyan:      lipgloss.Color("#0969DA"),
	pink:      lipgloss.Color("#BF3989"),
	reason:    lipgloss.Color("#4B7B58"),
	diffAddFg: lipgloss.Color("#116329"), diffAddBg: lipgloss.Color("#CCFFD8"),
	diffDelFg: lipgloss.Color("#82071E"), diffDelBg: lipgloss.Color("#FFD7D5"),
	diffMetaFg: lipgloss.Color("#57606A"), diffMetaBg: lipgloss.Color("#FFF8C5"),
}

// Active colors + styles. These are package globals that applyPalette rebuilds;
// the UI goroutine is single-threaded and applyPalette runs before render, so the
// mutation is safe. cBlack/cWhite are literals used for pill text via readableOn.
var (
	cBlack = lipgloss.Color("#000000")
	cWhite = lipgloss.Color("#FFFFFF")

	cFg, cFgDim, cMuted                  color.Color
	cPrimary, cGreen, cRed, cCyan, cPink color.Color
	cReason                              color.Color

	activeLight bool // true when the active palette is the light one (markdown base)

	stPrimaryBold, stGreenBold                                        lipgloss.Style
	stUserPrompt, stBar, stBrand, stTool                              lipgloss.Style
	stUserText, stThinking, stReminder, stDim, stFgDim, stErr, stInfo lipgloss.Style
	stChevron                                                         lipgloss.Style
	stToolBash, stToolEdit, stToolBg, stToolOther                     lipgloss.Style
	stPaletteInput                                                    lipgloss.Style
	stCtrlCArmed, stYolo, stBgCount, stSnail, stNotice                lipgloss.Style
	stDiffAdd, stDiffDel, stDiffMeta                                  lipgloss.Style
)

func init() { applyPalette(darkPalette) }

// applyPalette rebuilds every active color and style from p. It runs once at
// startup (dark default) and again when the terminal background is sensed
// (model) or a Lua override is applied. It also resets the markdown renderer
// cache so assistant text re-renders in the new palette.
func applyPalette(p palette) {
	activeLight = p.light
	cFg, cFgDim, cMuted = p.fg, p.fgDim, p.muted
	cPrimary, cGreen, cRed, cCyan, cPink, cReason = p.primary, p.green, p.red, p.cyan, p.pink, p.reason

	stPrimaryBold = lipgloss.NewStyle().Foreground(cPrimary).Bold(true)
	stGreenBold = lipgloss.NewStyle().Foreground(cGreen).Bold(true)
	stUserPrompt, stBar, stBrand, stTool = stPrimaryBold, stPrimaryBold, stPrimaryBold, stGreenBold

	stUserText = lipgloss.NewStyle().Foreground(cFg)
	stThinking = lipgloss.NewStyle().Foreground(cReason)
	stReminder = lipgloss.NewStyle().Foreground(cMuted)
	stDim = lipgloss.NewStyle().Foreground(cMuted)
	stFgDim = lipgloss.NewStyle().Foreground(cFgDim)
	stErr = lipgloss.NewStyle().Foreground(cRed).Bold(true)
	stInfo = lipgloss.NewStyle().Foreground(cCyan)
	stChevron = lipgloss.NewStyle().Foreground(cMuted)

	// Per-tool header colors: bash green, edit_file yellow, bash_bg red, rest pink.
	stToolBash = stGreenBold
	stToolEdit = stPrimaryBold
	stToolBg = lipgloss.NewStyle().Foreground(cRed).Bold(true)
	stToolOther = lipgloss.NewStyle().Foreground(cPink).Bold(true)

	// Pills: an accent as the background, with auto-contrast text so they read on
	// a light or dark terminal alike.
	stPaletteInput = stPrimaryBold // the ctrl+p palette's typed input line
	stCtrlCArmed = pill(cRed)      // Ctrl+C "press again to quit" bar
	stYolo = pill(cGreen)          // danger "!" pill (on_tool_call off)
	stBgCount = pill(cCyan)        // live subprocess count
	stSnail = pill(cPrimary)       // brand snail glued to the agent badge
	stNotice = lipgloss.NewStyle().Foreground(cPrimary)

	// edit_file diff colors (git-diff-style preview).
	stDiffAdd = lipgloss.NewStyle().Foreground(p.diffAddFg).Background(p.diffAddBg)
	stDiffDel = lipgloss.NewStyle().Foreground(p.diffDelFg).Background(p.diffDelBg)
	stDiffMeta = lipgloss.NewStyle().Foreground(p.diffMetaFg).Background(p.diffMetaBg)

	resetMarkdown()
}

// pill renders an accent as a badge background with legible auto-chosen text.
func pill(bg color.Color) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(readableOn(bg)).Background(bg).Bold(true)
}

// tokens maps each shell3.theme{} token (the snake_case names in the docs) to the
// palette field it overrides. It is the single source of truth for the theme
// vocabulary: withOverrides applies from it and parseThemeOverride validates
// against it, so adding a token here is the only edit needed to support one.
func (p *palette) tokens() map[string]*color.Color {
	return map[string]*color.Color{
		"fg":      &p.fg,
		"fg_dim":  &p.fgDim,
		"muted":   &p.muted,
		"primary": &p.primary,
		"green":   &p.green,
		"red":     &p.red,
		"cyan":    &p.cyan,
		"pink":    &p.pink,
		"reason":  &p.reason,
	}
}

// withOverrides returns p with any token present in ov replaced.
func (p palette) withOverrides(ov map[string]color.Color) palette {
	fields := p.tokens()
	for name, c := range ov {
		if dst := fields[name]; dst != nil {
			*dst = c
		}
	}
	return p
}

// parseThemeOverride converts a token→hex map (from shell3.theme{}; the hex
// format is already validated in luacfg) into colors, dropping any token this
// palette doesn't recognize. It returns the overrides plus one warning per
// unknown token, so a typo surfaces at startup instead of vanishing silently. The
// vocabulary comes from palette.tokens — the single source of truth.
func parseThemeOverride(m map[string]string) (map[string]color.Color, []string) {
	if len(m) == 0 {
		return nil, nil
	}
	valid := (&palette{}).tokens()
	out := make(map[string]color.Color, len(m))
	var warnings []string
	for k, hex := range m {
		if _, ok := valid[k]; !ok {
			warnings = append(warnings, fmt.Sprintf("theme: unknown color token %q ignored", k))
			continue
		}
		out[k] = lipgloss.Color(hex)
	}
	if len(out) == 0 {
		out = nil
	}
	return out, warnings
}

// hexOf renders a color as a "#rrggbb" string (glamour's markdown styling wants
// string pointers, so the markdown palette derives its hexes from the active
// colors through this).
func hexOf(c color.Color) string {
	cf, _ := colorful.MakeColor(c)
	return cf.Hex()
}

// agentBadge renders the active-agent pill for the footer. Its background color
// is derived deterministically from the agent name, and the text is black or
// white — whichever reads better on that background.
func agentBadge(name string) string {
	bg := agentColor(name)
	return lipgloss.NewStyle().Foreground(readableOn(bg)).Background(bg).Bold(true).
		Render(" " + name + " ")
}

// agentColor maps an agent name to a stable, muted background color without a
// config knob. The name hashes into one of 12 hue buckets 30° apart, rendered at
// the palette's saturation/value — so two agents are either the same color or
// clearly distinct, never a muddy near-match (raw hash%360 once put "code" and
// "plan" two degrees apart).
func agentColor(name string) colorful.Color {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	hue := float64(h.Sum32()%12) * 30
	return colorful.Hsv(hue, 0.55, 0.7)
}

// readableOn returns black or white text for the higher-contrast pairing against
// bg (a pill background). The crossover sits at relative luminance ≈ 0.179 — the
// point where black and white give equal WCAG contrast.
func readableOn(bg color.Color) color.Color {
	cf, _ := colorful.MakeColor(bg)
	if relLuminance(cf) > 0.179 {
		return cBlack
	}
	return cWhite
}

// relLuminance is the WCAG relative luminance of an sRGB color: each channel is
// linearized, then weighted by the eye's sensitivity.
func relLuminance(c colorful.Color) float64 {
	lin := func(v float64) float64 {
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// rainbowBg renders s as white text over an animated rainbow background — the
// thinking indicator. shift flows the colors per frame.
func rainbowBg(s string, shift int) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return ""
	}
	var b strings.Builder
	for i, r := range runes {
		hue := math.Mod(float64(i)/float64(len(runes))*360+float64(shift)*15, 360)
		rr, gg, bb := colorful.Hsv(hue, 0.55, 0.7).RGB255() // soft bg under white text
		fmt.Fprintf(&b, "\x1b[48;2;%d;%d;%dm\x1b[1;38;2;255;255;255m%c", rr, gg, bb, r)
	}
	b.WriteString("\x1b[0m")
	return b.String()
}
