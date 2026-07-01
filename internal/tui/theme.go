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

// shell3's color palette, expressed as lipgloss styles.
var (
	cPrimary = lipgloss.Color("#EAB308") // yellow
	cFgDim   = lipgloss.Color("#9CA3AF") // gray-400
	cMuted   = lipgloss.Color("#6B7280") // gray-500
	cGreen   = lipgloss.Color("#78AA78") // muted green — tool headers
	cSage    = lipgloss.Color("#87A58C") // muted sage — reasoning
	cAmber   = lipgloss.Color("#BFA94A") // muted yellow — system reminders
	cRed     = lipgloss.Color("#B91C1C") // errors / mode badge
	cUser    = lipgloss.Color("#E5E7EB") // near-white user text
	cCyan    = lipgloss.Color("#5BB6C9") // : command info
	cPink    = lipgloss.Color("#D98FB8") // misc tools
	cInputBg = lipgloss.Color("#1C1C22") // subtle background behind the input
	cBlack   = lipgloss.Color("#000000")

	// Shared bases so styles that are meant to look the same can't drift apart.
	stPrimaryBold = lipgloss.NewStyle().Foreground(cPrimary).Bold(true)
	stGreenBold   = lipgloss.NewStyle().Foreground(cGreen).Bold(true)

	stUserPrompt = stPrimaryBold // user "›" prompt
	stBar        = stPrimaryBold // NORMAL-mode cursor bar
	stBrand      = stPrimaryBold // "shell3" brand
	stTool       = stGreenBold   // generic tool accent (✓)

	stUserText = lipgloss.NewStyle().Foreground(cUser)
	stThinking = lipgloss.NewStyle().Foreground(cSage)
	stReminder = lipgloss.NewStyle().Foreground(cAmber)
	stDim      = lipgloss.NewStyle().Foreground(cMuted)
	stFgDim    = lipgloss.NewStyle().Foreground(cFgDim)
	stErr      = lipgloss.NewStyle().Foreground(cRed).Bold(true)
	stInfo     = lipgloss.NewStyle().Foreground(cCyan)
	stChevron  = lipgloss.NewStyle().Foreground(cMuted)

	// Per-tool header colors: bash green, edit_file yellow, bash_bg red, rest pink.
	stToolBash  = stGreenBold
	stToolEdit  = stPrimaryBold
	stToolBg    = lipgloss.NewStyle().Foreground(cRed).Bold(true)
	stToolOther = lipgloss.NewStyle().Foreground(cPink).Bold(true)

	stModeNormal  = lipgloss.NewStyle().Foreground(cBlack).Background(cPrimary).Bold(true)
	stModeInsert  = lipgloss.NewStyle().Foreground(cBlack).Background(cGreen).Bold(true)
	stModeCommand = stPrimaryBold

	// Ctrl+C "press again to quit" — red middle bar.
	stCtrlCArmed = lipgloss.NewStyle().Foreground(cUser).Background(cRed).Bold(true)

	// Danger "!" pill — shown when on_tool_call is off (runtime :disable_safety or
	// disabled in the lua config).
	stYolo = lipgloss.NewStyle().Foreground(cBlack).Background(cGreen).Bold(true)

	// Live subprocess count ("bg: N") — its own background so it reads apart from
	// the danger/agent pills around it on the footer.
	stBgCount = lipgloss.NewStyle().Foreground(cBlack).Background(cCyan).Bold(true)

	// Brand snail "๑ï", glued to the agent badge on the right of the footer:
	// dark text on the primary background.
	stSnail = lipgloss.NewStyle().Foreground(cBlack).Background(cPrimary).Bold(true)

	// Transient last-action notice on the footer (primary text, auto-hides).
	stNotice = lipgloss.NewStyle().Foreground(cPrimary)

	// edit_file diff colors (git-diff-style preview).
	stDiffAdd  = lipgloss.NewStyle().Foreground(lipgloss.Color("#B4E6B4")).Background(lipgloss.Color("#143C14"))
	stDiffDel  = lipgloss.NewStyle().Foreground(lipgloss.Color("#F0B4B4")).Background(lipgloss.Color("#461414"))
	stDiffMeta = lipgloss.NewStyle().Foreground(cFgDim).Background(lipgloss.Color("#4A4018"))
)

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

// readableOn returns black or near-white text for the higher-contrast pairing
// against bg. The crossover sits at relative luminance ≈ 0.179 — the point where
// black and white give equal WCAG contrast.
func readableOn(bg colorful.Color) color.Color {
	if relLuminance(bg) > 0.179 {
		return cBlack
	}
	return cUser
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
