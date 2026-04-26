package patchapp

import "github.com/weatherjean/shell3/internal/patchtui"

// buildFrame composes the live render frame: streaming preview (capped to
// terminal height), input box (multi-line, wrapped, with cursor marker),
// and status bar at the bottom.
//
// History (user messages, tool output, finalized streamed responses) is
// committed separately via Renderer.Print; it is not part of this frame.
func buildFrame(width, height int, st frameState) []string {
	frame := make([]string, 0, len(st.streamLines)+8)

	// Streaming preview, hard-wrapped to width and capped to fit the screen.
	if len(st.streamLines) > 0 {
		wrapped := wrapToWidth(st.streamLines, width)
		max := height - 4
		if max < 1 {
			max = 1
		}
		if len(wrapped) > max {
			wrapped = wrapped[len(wrapped)-max:]
		}
		frame = append(frame, wrapped...)
	}

	// One blank line of breathing room above the input box.
	frame = append(frame, "")

	// Input box (cursor visible only when not busy).
	frame = append(frame, renderInputBox(st.input, st.cursor, width, !st.busy)...)

	// Status bar.
	frame = append(frame, renderStatusBar(width, st.status))
	return frame
}

// frameState is the snapshot of app state buildFrame needs.
type frameState struct {
	streamLines []string
	input       []rune
	cursor      int
	busy        bool
	status      statusInfo
}

// wrapToWidth hard-wraps each line so no rendered line exceeds width visual
// columns. Splits at column boundaries accounting for double-width runes
// (emoji, CJK). ANSI SGR sequences are passed through and don't count.
func wrapToWidth(lines []string, width int) []string {
	if width <= 0 {
		return lines
	}
	var out []string
	for _, line := range lines {
		if patchtui.VisibleLen(line) <= width {
			out = append(out, line)
			continue
		}
		var cur []rune
		visCount := 0
		inEsc := false
		for _, r := range line {
			cur = append(cur, r)
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
			rw := patchtui.RuneWidth(r)
			// If this rune wouldn't fit, flush current first (without it).
			// This keeps trailing ANSI sequences (zero-width) attached to
			// the line they belong to instead of starting the next line.
			if visCount+rw > width {
				out = append(out, string(cur[:len(cur)-1]))
				cur = []rune{r}
				visCount = rw
				continue
			}
			visCount += rw
		}
		if len(cur) > 0 {
			out = append(out, string(cur))
		}
	}
	return out
}
