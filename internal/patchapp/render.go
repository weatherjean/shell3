package patchapp

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
	"github.com/weatherjean/shell3/internal/patchtui"
)

// reservedRenderLines is the number of terminal lines reserved for the input
// box and status bar so the streaming preview doesn't overwrite them.
const reservedRenderLines = 4

// buildFrame composes the live render frame: streaming preview (capped to
// terminal height), input box (multi-line, wrapped, with cursor marker),
// and status bar at the bottom.
//
// History (user messages, tool output, finalized streamed responses) is
// committed separately via Renderer.Print; it is not part of this frame.
func buildFrame(width, height int, st frameState) []string {
	frame := make([]string, 0, len(st.streamLines)+8)

	// Streaming preview, wrapped to width and capped to fit the screen.
	if len(st.streamLines) > 0 {
		wrapped := wrapToWidth(st.streamLines, width)
		max := height - reservedRenderLines
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

// wrapToWidth wraps each line so no rendered line exceeds width visual
// columns. For plain prose it prefers word boundaries, with hanging indents
// for bullet/numbered list continuations. Fallback is hard-wrap at columns.
func wrapToWidth(lines []string, width int) []string {
	if width <= 0 {
		return lines
	}
	var out []string
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			out = append(out, hardWrapLine(line, width)...)
			continue
		}
		if inFence || looksLikeTableRow(trimmed) {
			out = append(out, hardWrapLine(line, width)...)
			continue
		}
		out = append(out, smartWrapLine(line, width)...)
	}
	return out
}

func smartWrapLine(line string, width int) []string {
	if patchtui.VisibleLen(line) <= width {
		return []string{line}
	}
	firstPrefix, contPrefix, content := listPrefixes(line)
	if firstPrefix == "" && contPrefix == "" {
		firstPrefix = ""
		contPrefix = ""
		content = line
	}

	words := strings.Fields(content)
	if len(words) == 0 {
		return hardWrapLine(line, width)
	}

	firstAvail := width - patchtui.VisibleLen(firstPrefix)
	contAvail := width - patchtui.VisibleLen(contPrefix)
	if firstAvail < 1 || contAvail < 1 {
		return hardWrapLine(line, width)
	}

	var out []string
	curPrefix := firstPrefix
	avail := firstAvail
	cur := ""
	for _, w := range words {
		if patchtui.VisibleLen(w) > avail {
			if cur != "" {
				out = append(out, curPrefix+cur)
				curPrefix = contPrefix
				avail = contAvail
				cur = ""
			}
			chunks := hardWrapLine(w, avail)
			for i, c := range chunks {
				if i == len(chunks)-1 {
					cur = c
				} else {
					out = append(out, curPrefix+c)
					curPrefix = contPrefix
					avail = contAvail
				}
			}
			continue
		}
		candidate := w
		if cur != "" {
			candidate = cur + " " + w
		}
		if patchtui.VisibleLen(candidate) <= avail {
			cur = candidate
			continue
		}
		if cur == "" {
			out = append(out, curPrefix+w)
		} else {
			out = append(out, curPrefix+cur)
			cur = w
		}
		curPrefix = contPrefix
		avail = contAvail
	}
	if cur != "" {
		out = append(out, curPrefix+cur)
	}
	if len(out) == 0 {
		return hardWrapLine(line, width)
	}
	return out
}

func looksLikeTableRow(trimmed string) bool {
	if trimmed == "" || !strings.Contains(trimmed, "|") {
		return false
	}
	if strings.HasPrefix(trimmed, "|") || strings.HasSuffix(trimmed, "|") {
		return true
	}
	return false
}

func listPrefixes(line string) (firstPrefix, contPrefix, content string) {
	lead := len(line) - len(strings.TrimLeft(line, " "))
	indent := strings.Repeat(" ", lead)
	rest := line[lead:]

	for _, m := range []string{"- ", "* ", "• "} {
		if strings.HasPrefix(rest, m) {
			firstPrefix = indent + m
			contPrefix = strings.Repeat(" ", patchtui.VisibleLen(firstPrefix))
			return firstPrefix, contPrefix, strings.TrimSpace(rest[len(m):])
		}
	}

	i := 0
	for i < len(rest) && unicode.IsDigit(rune(rest[i])) {
		i++
	}
	if i > 0 && i+1 < len(rest) && (rest[i] == '.' || rest[i] == ')') && rest[i+1] == ' ' {
		if _, err := strconv.Atoi(rest[:i]); err == nil {
			marker := rest[:i+2]
			firstPrefix = indent + marker
			contPrefix = strings.Repeat(" ", patchtui.VisibleLen(firstPrefix))
			return firstPrefix, contPrefix, strings.TrimSpace(rest[i+2:])
		}
	}

	return "", "", line
}

func wrapCommittedLines(lines []string, width int) []string {
	if width <= 0 {
		return lines
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, wrapToWidth([]string{line}, width)...)
	}
	return out
}

// hardWrapLine splits line at width column boundaries, preserving ANSI SGR
// sequences and never breaking in the middle of a grapheme cluster.
func hardWrapLine(line string, width int) []string {
	if patchtui.VisibleLen(line) <= width {
		return []string{line}
	}
	var out []string
	visCount := 0
	chunkStart := 0 // byte index of current output chunk start
	i := 0          // current byte index

	for i < len(line) {
		// ANSI SGR escape: pass through with zero width.
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
		cw := uniseg.StringWidth(cluster)
		if visCount+cw > width {
			out = append(out, line[chunkStart:i])
			chunkStart = i
			visCount = 0
		}
		visCount += cw
		i += len(cluster)
	}
	if chunkStart < len(line) {
		out = append(out, line[chunkStart:])
	}
	return out
}
