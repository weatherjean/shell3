package patchapp

import (
	"strings"

	"github.com/weatherjean/shell3/internal/patchtui"
)

// styled wraps text in fg/bg/bold ANSI codes, terminated by reset.
func styled(text, fg, bg string, bold bool) string {
	var b strings.Builder
	b.WriteString(bg)
	b.WriteString(fg)
	if bold {
		b.WriteString(patchtui.Bold)
	}
	b.WriteString(text)
	b.WriteString(patchtui.Reset)
	return b.String()
}

func renderUserBubbleLine(isFirst bool, content string, contentVisible, width int) string {
	userBg := patchtui.BgRGB(rUserBg, gUserBg, bUserBg)
	userFg := patchtui.FgRGB(rUserFg, gUserFg, bUserFg)
	yellow := patchtui.FgRGB(rPrimary, gPrimary, bPrimary)

	var prefix string
	if isFirst {
		prefix = userBg + yellow + patchtui.Bold + "> " + patchtui.Reset + userBg + userFg
	} else {
		prefix = userBg + userFg + "  "
	}

	contentW := width - 2
	if contentW < 1 {
		contentW = 1
	}
	pad := contentW - contentVisible
	if pad < 0 {
		pad = 0
	}
	return prefix + content + strings.Repeat(" ", pad) + patchtui.Reset
}

// Color palette — true-color RGB values used throughout the UI.
const (
	rPrimary = 234 // yellow #EAB308
	gPrimary = 179
	bPrimary = 8

	rFgDim = 156 // gray-400 #9CA3AF
	gFgDim = 163
	bFgDim = 175

	rMuted = 107 // gray-500 #6B7280
	gMuted = 114
	bMuted = 128

	rSubtle = 55 // gray-700 #374151
	gSubtle = 65
	bSubtle = 81

	rDark = 31 // gray-800 #1F2937
	gDark = 41
	bDark = 55

	rUserBg = 40 // user-message background
	gUserBg = 44
	bUserBg = 52

	rUserFg = 229 // near-white
	gUserFg = 231
	bUserFg = 235

	rRedBadge = 185 // mode badge bg #B91C1C
	gRedBadge = 28
	bRedBadge = 28
)
