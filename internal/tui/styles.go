package tui

import (
	"fmt"
	"strings"
)

// ANSI control sequences used throughout the TUI.
const (
	ansiReset = "\033[0m"
	ansiBold  = "\033[1m"
	ansiDim   = "\033[2m"
)

// fgRGB returns the ANSI SGR sequence to set foreground to an RGB color.
func fgRGB(r, g, b int) string { return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b) }

// bgRGB returns the ANSI SGR sequence to set background to an RGB color.
func bgRGB(r, g, b int) string { return fmt.Sprintf("\033[48;2;%d;%d;%dm", r, g, b) }

// styled wraps text in fg/bg/bold ANSI codes, terminated by reset.
func styled(text, fg, bg string, bold bool) string {
	var b strings.Builder
	b.WriteString(bg)
	b.WriteString(fg)
	if bold {
		b.WriteString(ansiBold)
	}
	b.WriteString(text)
	b.WriteString(ansiReset)
	return b.String()
}

// visibleLen returns the number of visible columns occupied by s, skipping
// ANSI SGR sequences. It approximates East Asian Wide and emoji ranges as
// 2 columns; everything else is 1 column. Good enough for chat content.
func visibleLen(s string) int {
	n := 0
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\033' {
			inEsc = true
			continue
		}
		n += runeWidth(r)
	}
	return n
}

// runeWidth returns 2 for runes in common East Asian Wide and emoji
// ranges, 1 otherwise. Approximate — doesn't cover every Unicode quirk
// but handles emoji and CJK reliably.
func runeWidth(r rune) int {
	switch {
	case r < 0x80:
		return 1
	case r >= 0x1100 && r <= 0x115F: // Hangul Jamo
		return 2
	case r >= 0x2E80 && r <= 0x9FFF: // CJK
		return 2
	case r >= 0xA000 && r <= 0xA4CF: // Yi
		return 2
	case r >= 0xAC00 && r <= 0xD7A3: // Hangul syllables
		return 2
	case r >= 0xF900 && r <= 0xFAFF: // CJK Compatibility
		return 2
	case r >= 0xFE30 && r <= 0xFE4F: // CJK Compatibility forms
		return 2
	case r >= 0xFF00 && r <= 0xFF60: // Fullwidth
		return 2
	case r >= 0xFFE0 && r <= 0xFFE6: // Fullwidth signs
		return 2
	case r >= 0x1F000 && r <= 0x1FAFF: // Emoji + symbols
		return 2
	case r >= 0x20000 && r <= 0x3FFFD: // CJK Extension B-G
		return 2
	}
	return 1
}

// Color palette — true-color RGB values used throughout the UI.
const (
	rPrimary = 234 // yellow #EAB308
	gPrimary = 179
	bPrimary = 8

	rRed = 239 // red #EF4444
	gRed = 68
	bRed = 68

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
