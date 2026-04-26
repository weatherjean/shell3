package tui

import (
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/patchtui"
)

// spinnerGlyph cycles · → ○ → ● every 500ms.
func spinnerGlyph() string {
	frames := []string{"·", "○", "●"}
	return frames[(time.Now().UnixMilli()/500)%int64(len(frames))]
}

// renderInputBox returns the frame lines for the multi-line input box,
// wrapping at width, with cursor marker placed at the right column.
//
// First logical line gets the "> " prefix; continuations and lines after
// alt+enter get a "  " indent. Each line has a subtle dark background that
// extends to the right edge so the input reads as a chat bubble.
func renderInputBox(input []rune, cursor, width int, showCursor bool) []string {
	userBg := bgRGB(rUserBg, gUserBg, bUserBg)
	userFg := fgRGB(rUserFg, gUserFg, bUserFg)
	yellow := fgRGB(rPrimary, gPrimary, bPrimary)

	prefixW := 2
	contW := 2

	// makeLine builds one full-width frame line: prefix + content + padding + reset.
	makeLine := func(isFirst bool, content string, contentVisible int) string {
		var prefix string
		if isFirst {
			prefix = userBg + yellow + ansiBold + "> " + ansiReset + userBg + userFg
		} else {
			prefix = userBg + userFg + "  "
		}
		pad := width - 2 - contentVisible
		if pad < 0 {
			pad = 0
		}
		return prefix + content + strings.Repeat(" ", pad) + ansiReset
	}

	// Split input on '\n' into logical lines.
	rawLines := splitRunes(input, '\n')
	cursorRow, cursorCol := 0, 0
	cursorFound := false
	inputPos := 0

	var lines []string
	for li, l := range rawLines {
		availW := width - contW
		if li == 0 {
			availW = width - prefixW
		}
		if availW < 1 {
			availW = 1
		}

		// Empty logical line: emit one frame line.
		if len(l) == 0 {
			lines = append(lines, makeLine(li == 0, "", 0))
			if !cursorFound && cursor == inputPos {
				cursorRow = len(lines) - 1
				if li == 0 {
					cursorCol = prefixW
				} else {
					cursorCol = contW
				}
				cursorFound = true
			}
			inputPos++
			continue
		}

		for chunkStart := 0; chunkStart < len(l); chunkStart += availW {
			chunkEnd := chunkStart + availW
			if chunkEnd > len(l) {
				chunkEnd = len(l)
			}
			chunk := l[chunkStart:chunkEnd]
			isFirstChunk := chunkStart == 0
			isLastChunk := chunkEnd == len(l)

			lines = append(lines, makeLine(li == 0 && isFirstChunk, string(chunk), len(chunk)))

			chunkMin := inputPos + chunkStart
			chunkMax := inputPos + chunkEnd
			if !cursorFound &&
				cursor >= chunkMin &&
				(cursor < chunkMax || (isLastChunk && cursor == chunkMax)) {
				cursorRow = len(lines) - 1
				rel := cursor - chunkMin
				if li == 0 && isFirstChunk {
					cursorCol = prefixW + rel
				} else {
					cursorCol = contW + rel
				}
				cursorFound = true
			}
		}
		// Cursor at exact width boundary at end of input — extra row.
		if !cursorFound && cursor == inputPos+len(l) && len(l)%availW == 0 {
			lines = append(lines, makeLine(false, "", 0))
			cursorRow = len(lines) - 1
			cursorCol = contW
			cursorFound = true
		}
		inputPos += len(l) + 1
	}

	if !cursorFound && len(lines) > 0 {
		cursorRow = len(lines) - 1
		cursorCol = visibleLen(lines[len(lines)-1])
	}

	if showCursor && len(lines) > 0 {
		lines[cursorRow] = insertAtVisibleCol(lines[cursorRow], cursorCol, patchtui.CursorMarker)
	}
	return lines
}

// splitRunes splits a rune slice on sep, returning sub-slices excluding
// the separator. Always returns at least one element.
func splitRunes(rs []rune, sep rune) [][]rune {
	var out [][]rune
	var cur []rune
	for _, r := range rs {
		if r == sep {
			out = append(out, cur)
			cur = nil
		} else {
			cur = append(cur, r)
		}
	}
	out = append(out, cur)
	return out
}

// insertAtVisibleCol inserts ins at visible column col in line, walking
// past any ANSI SGR sequences.
func insertAtVisibleCol(line string, col int, ins string) string {
	if col == 0 {
		return ins + line
	}
	visCol := 0
	inEsc := false
	for i, r := range line {
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
		if visCol == col {
			return line[:i] + ins + line[i:]
		}
		visCol++
	}
	return line + ins
}
