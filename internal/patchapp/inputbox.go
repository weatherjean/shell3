package patchapp

// Input box rendering: grapheme-cluster-aware wrapping, cursor placement,
// and background fill for the user's chat bubble.
//
// Grapheme segmentation via rivo/uniseg (Rivo Huber, MIT License,
// https://github.com/rivo/uniseg) so that ZWJ sequences (👩‍💻),
// variation-selector emoji (🖥️), and East Asian wide characters are
// measured and wrapped correctly.

import (
	"time"

	"github.com/rivo/uniseg"
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
	prefixW := 2
	contW := 2

	makeLine := func(isFirst bool, content string, contentVisible int) string {
		return renderUserBubbleLine(isFirst, content, contentVisible, width)
	}

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

		chunks := splitRuneChunksByWidth(l, availW)
		for ci, ch := range chunks {
			chunk := l[ch.start:ch.end]
			isFirstChunk := ci == 0
			isLastChunk := ci == len(chunks)-1
			contentVis := uniseg.StringWidth(string(chunk))

			lines = append(lines, makeLine(li == 0 && isFirstChunk, string(chunk), contentVis))

			chunkMin := inputPos + ch.start
			chunkMax := inputPos + ch.end
			// Extend range to cover spaces dropped at word-wrap boundary after
			// this chunk (they belong visually at the end of this display line).
			extMax := chunkMax
			if !isLastChunk {
				extMax = chunkMax + chunks[ci+1].leadGap
			}
			if !cursorFound &&
				cursor >= chunkMin &&
				(cursor < extMax || (isLastChunk && cursor == chunkMax)) {
				rel := cursor - chunkMin
				if rel > ch.end-ch.start {
					rel = ch.end - ch.start
				}
				relCol := uniseg.StringWidth(string(chunk[:rel]))
				cursorRow = len(lines) - 1
				if li == 0 && isFirstChunk {
					cursorCol = prefixW + relCol
				} else {
					cursorCol = contW + relCol
				}
				cursorFound = true
			}
		}
		if !cursorFound && cursor == inputPos+len(l) && uniseg.StringWidth(string(l))%availW == 0 {
			lines = append(lines, makeLine(false, "", 0))
			cursorRow = len(lines) - 1
			cursorCol = contW
			cursorFound = true
		}
		inputPos += len(l) + 1
	}

	if !cursorFound && len(lines) > 0 {
		cursorRow = len(lines) - 1
		cursorCol = patchtui.VisibleLen(lines[len(lines)-1])
	}

	if showCursor && len(lines) > 0 {
		lines[cursorRow] = insertAtVisibleCol(lines[cursorRow], cursorCol, patchtui.CursorMarker)
	}
	return lines
}

// inputCursorPos computes the wrapped (row, col) for cursor offset.
func inputCursorPos(input []rune, cursor, width int) (row, col int) {
	prefixW, contW := 2, 2
	rawLines := splitRunes(input, '\n')
	inputPos := 0
	for li, l := range rawLines {
		availW := width - contW
		if li == 0 {
			availW = width - prefixW
		}
		if availW < 1 {
			availW = 1
		}
		if len(l) == 0 {
			if cursor == inputPos {
				if li == 0 {
					return row, prefixW
				}
				return row, contW
			}
			row++
			inputPos++
			continue
		}
		chunks := splitRuneChunksByWidth(l, availW)
		for ci, ch := range chunks {
			isLast := ci == len(chunks)-1
			min := inputPos + ch.start
			max := inputPos + ch.end
			extMax := max
			if !isLast {
				extMax = max + chunks[ci+1].leadGap
			}
			if cursor >= min && (cursor < extMax || (isLast && cursor == max)) {
				rel := cursor - min
				if rel > ch.end-ch.start {
					rel = ch.end - ch.start
				}
				relCol := uniseg.StringWidth(string(l[ch.start : ch.start+rel]))
				pw := contW
				if li == 0 && ci == 0 {
					pw = prefixW
				}
				return row, pw + relCol
			}
			row++
		}
		inputPos += len(l) + 1
	}
	return row, 0
}

// inputOffsetForRowCol is the inverse mapping from wrapped row/col to input offset.
func inputOffsetForRowCol(input []rune, width, targetRow, targetCol int) int {
	prefixW, contW := 2, 2
	rawLines := splitRunes(input, '\n')
	inputPos := 0
	row := 0
	for li, l := range rawLines {
		availW := width - contW
		if li == 0 {
			availW = width - prefixW
		}
		if availW < 1 {
			availW = 1
		}
		if len(l) == 0 {
			if row == targetRow {
				return inputPos
			}
			row++
			inputPos++
			continue
		}
		chunks := splitRuneChunksByWidth(l, availW)
		for ci, ch := range chunks {
			if row == targetRow {
				pw := contW
				if li == 0 && ci == 0 {
					pw = prefixW
				}
				relCol := targetCol - pw
				if relCol < 0 {
					relCol = 0
				}
				relRunes := runesForVisibleCols(l[ch.start:ch.end], relCol)
				return inputPos + ch.start + relRunes
			}
			row++
		}
		inputPos += len(l) + 1
	}
	return len(input)
}

// runeChunk is a half-open rune-index range [start, end) within a logical
// input line. leadGap is the number of space runes immediately before start
// that were dropped at a word-wrap boundary; callers use it to place the
// cursor when it sits on one of those dropped spaces.
type runeChunk struct {
	start, end int
	leadGap    int
}

// splitRuneChunksByWidth splits rs into display-line chunks with word-aware
// wrapping, falling back to grapheme-cluster hard-wrap for words wider than
// the available width. Spaces at wrap boundaries are dropped and tracked in
// leadGap for cursor placement.
//
// Word-boundary algorithm inspired by charmbracelet/bubbles textarea (MIT,
// https://github.com/charmbracelet/bubbles).
func splitRuneChunksByWidth(rs []rune, width int) []runeChunk {
	if len(rs) == 0 {
		return nil
	}
	if width < 1 {
		width = 1
	}
	n := len(rs)

	var out []runeChunk
	lineStart := 0 // rune index of the current line's first rune
	lineVis := 0   // visual columns used so far on the current line
	prevGap := 0   // spaces dropped before lineStart (for the current chunk)

	// newLine starts a fresh display line at rune index next, recording that
	// dropped spaces separated this line from the previous chunk.
	newLine := func(end, next, dropped int) {
		out = append(out, runeChunk{start: lineStart, end: end, leadGap: prevGap})
		lineStart = next
		lineVis = 0
		prevGap = dropped
	}

	i := 0
	for i < n {
		// Find the next word: scan non-space grapheme clusters.
		wStart := i
		wVis := 0
		s := string(rs[i:])
		state := -1
		for len(s) > 0 {
			cluster, rest, _, ns := uniseg.FirstGraphemeClusterInString(s, state)
			if cluster == " " {
				break
			}
			wVis += uniseg.StringWidth(cluster)
			i += len([]rune(cluster))
			s = rest
			state = ns
		}
		wEnd := i // exclusive

		if wEnd == wStart {
			// Only spaces remain (or i already at end from previous loop).
			if i < n {
				// Leading/consecutive space — include on current line if room.
				if lineVis < width {
					lineVis++
				}
				i++
			}
			continue
		}

		if lineVis > 0 && lineVis+wVis > width {
			// Word doesn't fit on current line. Break before the space(s)
			// that precede it; those spaces are dropped (not in either chunk).
			prevWordEnd := wStart
			for prevWordEnd > lineStart && rs[prevWordEnd-1] == ' ' {
				prevWordEnd--
			}
			dropped := wStart - prevWordEnd
			newLine(prevWordEnd, wStart, dropped)
		}

		if lineVis == 0 && wVis > width {
			// Word alone exceeds width — hard-wrap at grapheme boundaries.
			s := string(rs[wStart:wEnd])
			state := -1
			ri := wStart
			for len(s) > 0 {
				cluster, rest, _, ns := uniseg.FirstGraphemeClusterInString(s, state)
				clW := uniseg.StringWidth(cluster)
				clR := len([]rune(cluster))
				if lineVis > 0 && lineVis+clW > width {
					newLine(ri, ri, 0)
				}
				lineVis += clW
				ri += clR
				s = rest
				state = ns
			}
		} else {
			lineVis += wVis
		}

		// Consume trailing spaces after the word.
		sStart := wEnd
		for i < n && rs[i] == ' ' {
			i++
		}
		sEnd := i
		sVis := sEnd - sStart
		if sVis > 0 {
			if lineVis+sVis <= width {
				lineVis += sVis
			} else {
				// Trailing spaces overflow the line — emit now, drop spaces.
				newLine(sStart, sEnd, sVis)
			}
		}
	}

	if lineStart < n {
		out = append(out, runeChunk{start: lineStart, end: n, leadGap: prevGap})
	}
	if len(out) == 0 {
		out = append(out, runeChunk{start: 0, end: n})
	}
	return out
}

// runesForVisibleCols returns the number of runes in rs that fill cols
// visible terminal columns, stopping before exceeding the limit.
func runesForVisibleCols(rs []rune, cols int) int {
	if cols <= 0 {
		return 0
	}
	s := string(rs)
	vis := 0
	runeIdx := 0
	state := -1
	for len(s) > 0 {
		cluster, rest, _, newState := uniseg.FirstGraphemeClusterInString(s, state)
		w := uniseg.StringWidth(cluster)
		if vis+w > cols {
			return runeIdx
		}
		vis += w
		runeIdx += len([]rune(cluster))
		s = rest
		state = newState
	}
	return len(rs)
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

// insertAtVisibleCol inserts ins at visible column col in line, correctly
// handling ANSI SGR sequences (zero width) and grapheme clusters (which
// may occupy 1 or 2 columns each).
func insertAtVisibleCol(line string, col int, ins string) string {
	if col <= 0 {
		return ins + line
	}
	visCol := 0
	i := 0 // byte index
	for i < len(line) {
		// ANSI SGR escape: skip to closing 'm', zero width.
		if line[i] == '\033' {
			j := i + 1
			for j < len(line) && line[j] != 'm' {
				j++
			}
			if j < len(line) {
				j++
			}
			i = j
			continue
		}
		cluster, _, _, _ := uniseg.FirstGraphemeClusterInString(line[i:], -1)
		w := uniseg.StringWidth(cluster)
		if visCol >= col || visCol+w > col {
			return line[:i] + ins + line[i:]
		}
		visCol += w
		i += len(cluster)
	}
	return line + ins
}
