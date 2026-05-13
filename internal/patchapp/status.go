package patchapp

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/weatherjean/shell3/internal/patchtui"
)

// renderStatusBar returns the bottom status bar line for the idle frame,
// padded to terminal width. The busy state uses renderBusyLine instead.
func renderStatusBar(width int, st statusInfo) string {
	yellow := patchtui.FgRGB(rPrimary, gPrimary, bPrimary)
	black := patchtui.FgRGB(0, 0, 0)
	white := patchtui.FgRGB(255, 255, 255)
	gray4 := patchtui.FgRGB(rFgDim, gFgDim, bFgDim)
	gray7 := patchtui.BgRGB(rSubtle, gSubtle, bSubtle)
	dark := patchtui.BgRGB(rDark, gDark, bDark)
	redBg := patchtui.BgRGB(rRedBadge, gRedBadge, bRedBadge)

	var barBg, badgeBg, badgeFg string
	if st.ctrlCHint {
		barBg = redBg
		badgeBg = patchtui.BgRGB(rPrimary, gPrimary, bPrimary)
		badgeFg = black
	} else {
		barBg = gray7
		badgeBg = patchtui.BgRGB(rPrimary, gPrimary, bPrimary)
		badgeFg = black
	}

	left := styled(" shell3 ", badgeFg+badgeBg, "", true)
	mode := styled(" "+st.mode+" ", white, redBg, true)

	var mid string
	if st.ctrlCHint {
		mid = styled(" press ctrl+c again to exit ", white, redBg, true)
	} else {
		text := " " + st.statusMsg + " "
		if st.tokens > 0 {
			text += fmt.Sprintf("│ %s ", formatTokens(st.tokens, st.contextWindow))
		}
		mid = styled(text, gray4, gray7, false)
	}

	right := styled("  ", gray4, dark, false) +
		styled("/h", yellow, dark, true) +
		styled(" help  ", gray4, dark, false) + mode

	pad := width - patchtui.VisibleLen(left) - patchtui.VisibleLen(mid) - patchtui.VisibleLen(right)
	if pad < 0 {
		pad = 0
	}
	line := left + mid + styled(strings.Repeat(" ", pad), white, barBg, false) + right
	// Guarantee the status bar fits on one row regardless of terminal width.
	if width > 0 && patchtui.VisibleLen(line) > width {
		line = truncateANSIToWidth(line, width) + patchtui.Reset
	}
	return line
}

// truncateANSIToWidth returns the longest prefix of s whose visible
// width is at most width. ANSI SGR escape sequences (zero-width) are
// preserved verbatim; visible content is counted per rune using
// patchtui.RuneWidth so multi-byte UTF-8 characters (e.g. "│") count
// as one column, not three.
func truncateANSIToWidth(s string, width int) string {
	if patchtui.VisibleLen(s) <= width {
		return s
	}
	var b strings.Builder
	vis := 0
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			j := i + 1
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				j++
			}
			b.WriteString(s[i:j])
			i = j
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		w := patchtui.RuneWidth(r)
		if vis+w > width {
			break
		}
		b.WriteString(s[i : i+size])
		vis += w
		i += size
	}
	return b.String()
}

// renderBusyLine returns the single live bar shown while the app is busy.
// Rainbow gradient background spans the full width; spinner + "thinking" on
// the left, token count center-ish, ctrl+c hint on the right. Foreground
// glyphs are drawn over the gradient cell-by-cell.
func renderBusyLine(width int, st statusInfo) string {
	if width <= 0 {
		return ""
	}

	// Foreground content laid out left/middle/right onto a cell grid.
	cells := make([]rune, width)
	bold := make([]bool, width)
	for i := range cells {
		cells[i] = ' '
	}

	putRunes := func(start int, runes []rune, boldRun bool) {
		for i, r := range runes {
			col := start + i
			if col < 0 || col >= width {
				continue
			}
			cells[col] = r
			bold[col] = boldRun
		}
	}

	leftRunes := []rune(fmt.Sprintf(" %s thinking ", spinnerGlyph()))
	putRunes(0, leftRunes, false)

	if st.tokens > 0 {
		toks := []rune(fmt.Sprintf(" %s ", formatTokens(st.tokens, st.contextWindow)))
		putRunes(len(leftRunes)+1, toks, false)
	}

	// Right block: "  ctrl+c cancel "
	prefix := []rune("  ")
	ctrl := []rune("ctrl+c")
	suffix := []rune(" cancel ")
	rightLen := len(prefix) + len(ctrl) + len(suffix)
	rightStart := width - rightLen
	if rightStart < 0 {
		rightStart = 0
	}
	putRunes(rightStart, prefix, false)
	putRunes(rightStart+len(prefix), ctrl, true)
	putRunes(rightStart+len(prefix)+len(ctrl), suffix, false)

	// Rainbow background per column, white foreground (bold for ctrl+c).
	stops := [...][3]int{
		{180, 70, 70},   // red
		{200, 130, 70},  // orange
		{200, 180, 80},  // yellow
		{90, 170, 100},  // green
		{80, 140, 200},  // blue
		{150, 110, 200}, // violet
	}
	rainbow := func(col int) (r, g, b int) {
		t := float64(col) / float64(max(width-1, 1))
		pos := t * float64(len(stops)-1)
		i0 := int(pos)
		i1 := i0 + 1
		if i1 >= len(stops) {
			i1 = len(stops) - 1
		}
		frac := pos - float64(i0)
		lerp := func(a, b int) int { return a + int(float64(b-a)*frac+0.5) }
		return lerp(stops[i0][0], stops[i1][0]),
			lerp(stops[i0][1], stops[i1][1]),
			lerp(stops[i0][2], stops[i1][2])
	}

	var b strings.Builder
	for col, r := range cells {
		rr, gg, bb := rainbow(col)
		b.WriteString(patchtui.BgRGB(rr, gg, bb))
		b.WriteString(patchtui.FgRGB(255, 255, 255))
		if bold[col] {
			b.WriteString(patchtui.Bold)
		}
		b.WriteRune(r)
		b.WriteString(patchtui.Reset)
	}
	return b.String()
}

// statusInfo carries everything renderStatusBar / renderBusyLine need.
type statusInfo struct {
	mode          string // mode badge text (persona name)
	statusMsg     string // model/provider line when idle
	tokens        int
	contextWindow int // model context window size; 0 = unknown (no % shown)
	ctrlCHint     bool
}

// formatTokens returns "23% t:123k" (>=100k) or "23% t:1234" with context window,
// else "t:123k" / "t:1234" without.
func formatTokens(tokens, contextWindow int) string {
	var count string
	if tokens >= 100_000 {
		count = fmt.Sprintf("t:%dk", tokens/1000)
	} else {
		count = fmt.Sprintf("t:%d", tokens)
	}
	if contextWindow > 0 && tokens > 0 {
		pct := (tokens * 100) / contextWindow
		return fmt.Sprintf("%d%% %s", pct, count)
	}
	return count
}
