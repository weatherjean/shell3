package tui

import (
	"fmt"
	"strings"
)

// renderStatusBar returns the bottom status bar line, padded to terminal width.
// When busy, the spinner + "thinking" label replaces the model/usage info and
// the right hint becomes "ctrl+c cancel".
func renderStatusBar(width int, st statusInfo) string {
	yellow := fgRGB(rPrimary, gPrimary, bPrimary)
	black := fgRGB(0, 0, 0)
	white := fgRGB(255, 255, 255)
	gray4 := fgRGB(rFgDim, gFgDim, bFgDim)
	gray7 := bgRGB(rSubtle, gSubtle, bSubtle)
	dark := bgRGB(rDark, gDark, bDark)
	redBg := bgRGB(rRedBadge, gRedBadge, bRedBadge)
	greenBg := bgRGB(22, 101, 52) // gray-700 swap when streaming

	// Pick the bar's main background based on state.
	var barBg, badgeBg, badgeFg string
	switch {
	case st.ctrlCHint:
		barBg = redBg
		badgeBg = bgRGB(rPrimary, gPrimary, bPrimary)
		badgeFg = black
	case st.busy:
		barBg = greenBg
		badgeBg = greenBg
		badgeFg = white
	default:
		barBg = gray7
		badgeBg = bgRGB(rPrimary, gPrimary, bPrimary)
		badgeFg = black
	}

	left := styled(" shell3 ", badgeFg+badgeBg, "", true)
	mode := styled(" "+st.mode+" ", white, redBg, true)

	var mid string
	switch {
	case st.ctrlCHint:
		mid = styled(" press ctrl+c again to exit ", white, redBg, true)
	case st.busy:
		text := fmt.Sprintf(" %s  thinking  %d toks ", spinnerGlyph(), st.tokens)
		mid = styled(text, white, greenBg, false)
	default:
		text := " " + st.statusMsg + " "
		if st.tokens > 0 {
			text += fmt.Sprintf("│ %d toks ", st.tokens)
		}
		mid = styled(text, gray4, gray7, false)
	}

	var right string
	if st.busy {
		right = styled("  ", white, dark, false) +
			styled("ctrl+c", yellow, dark, true) +
			styled(" cancel  ", white, dark, false) + mode
	} else {
		right = styled("  ", gray4, dark, false) +
			styled("/h", yellow, dark, true) +
			styled(" help  ", gray4, dark, false) + mode
	}

	pad := width - visibleLen(left) - visibleLen(mid) - visibleLen(right)
	if pad < 0 {
		pad = 0
	}
	return left + mid + styled(strings.Repeat(" ", pad), white, barBg, false) + right
}

// statusInfo carries everything renderStatusBar needs.
type statusInfo struct {
	mode      string // mode badge text (persona name)
	statusMsg string // model/provider line when idle
	tokens    int
	busy      bool
	ctrlCHint bool
}
