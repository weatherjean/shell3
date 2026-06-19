package tui

import (
	"fmt"
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

	// ":disable_safety" indicator — green "!" pill next to the model.
	stYolo = lipgloss.NewStyle().Foreground(cBlack).Background(cGreen).Bold(true)

	// Active-agent badge, right side of the footer (Tab cycles it).
	stAgent = lipgloss.NewStyle().Foreground(cUser).Background(cRed).Bold(true)

	// Decorative "/" field behind the welcome card and modals — kept very dim.
	stSlashBg = lipgloss.NewStyle().Foreground(lipgloss.Color("#2E2E33"))

	// edit_file diff colors (git-diff-style preview).
	stDiffAdd  = lipgloss.NewStyle().Foreground(lipgloss.Color("#B4E6B4")).Background(lipgloss.Color("#143C14"))
	stDiffDel  = lipgloss.NewStyle().Foreground(lipgloss.Color("#F0B4B4")).Background(lipgloss.Color("#461414"))
	stDiffMeta = lipgloss.NewStyle().Foreground(cFgDim).Background(lipgloss.Color("#4A4018"))
)

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
