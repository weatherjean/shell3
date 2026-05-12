package patchapp

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
	"github.com/weatherjean/shell3/internal/patchtui"
)

// buildFrame composes the live render frame.
//
// Two modes:
//   - Idle: blank line + input box (multi-line, wrapped, with cursor marker)
//     + status bar.
//   - Busy: single line — the rainbow busy bar (see renderBusyLine).
//
// Streaming text and tool output are committed to scrollback via
// Renderer.Print by event handlers (see chat.drainTurn); they are not part
// of the live frame.
func buildFrame(width int, st frameState) []string {
	if st.busy {
		return []string{renderBusyLine(width, st.status)}
	}

	frame := make([]string, 0, 8)
	frame = append(frame, "")
	frame = append(frame, renderInputBox(st.input, st.cursor, width, true)...)
	frame = append(frame, renderStatusBar(width, st.status))
	return frame
}

// frameState is the snapshot of app state buildFrame needs.
type frameState struct {
	input  []rune
	cursor int
	busy   bool
	status statusInfo
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
		var wrapped []string
		switch {
		case strings.HasPrefix(trimmed, "```"):
			inFence = !inFence
			wrapped = hardWrapLine(line, width)
		case inFence || looksLikeTableRow(trimmed):
			wrapped = hardWrapLine(line, width)
		default:
			wrapped = smartWrapLine(line, width)
		}
		out = append(out, reapplyStyleEnvelope(line, wrapped)...)
	}
	return out
}

// reapplyStyleEnvelope ensures every wrapped continuation line carries the
// same leading SGR styling and trailing reset as the source line. Without
// this, frame renderers that emit explicit resets between lines render
// continuations unstyled until the next redraw.
//
// Only applies when the source line is uniformly styled — i.e. it starts
// with an SGR run, ends with one (containing a reset), and has no embedded
// resets in the middle. Lines with mid-line resets (e.g. multi-color
// content) are left to terminal default carry-over behavior.
func reapplyStyleEnvelope(src string, wrapped []string) []string {
	if len(wrapped) <= 1 {
		return wrapped
	}
	lead := leadingSGR(src)
	trail := trailingSGR(src)
	if lead == "" {
		return wrapped
	}
	// Guard against lead and trail overlapping (e.g. line is all SGR codes).
	if len(lead)+len(trail) > len(src) {
		return wrapped
	}
	// Reject if there's any reset between lead and trail (mid-line style break).
	body := src[len(lead) : len(src)-len(trail)]
	if strings.Contains(body, "\033[0m") {
		return wrapped
	}
	for i, w := range wrapped {
		if !strings.HasPrefix(w, lead) {
			w = lead + w
		}
		if trail != "" && !strings.HasSuffix(w, trail) {
			w = w + trail
		}
		wrapped[i] = w
	}
	return wrapped
}

// leadingSGR returns the contiguous run of SGR escape sequences at the
// start of s, or "" if none.
func leadingSGR(s string) string {
	end := 0
	for end < len(s) && s[end] == '\033' {
		j := end + 1
		if j >= len(s) || s[j] != '[' {
			break
		}
		j++
		for j < len(s) && s[j] != 'm' {
			j++
		}
		if j >= len(s) {
			break
		}
		end = j + 1
	}
	return s[:end]
}

// trailingSGR returns the contiguous run of SGR escape sequences at the
// end of s, or "" if none.
func trailingSGR(s string) string {
	start := len(s)
	for start > 0 {
		if s[start-1] != 'm' {
			break
		}
		j := start - 2
		for j >= 0 && s[j] != '\033' {
			j--
		}
		if j < 0 || j+1 >= len(s) || s[j+1] != '[' {
			break
		}
		start = j
	}
	return s[start:]
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
