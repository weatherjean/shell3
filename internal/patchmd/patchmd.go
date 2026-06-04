// Package patchmd is a small ANSI markdown renderer optimised for streaming
// LLM output. It supports the markdown subset that LLMs actually produce:
// headers (# ## ###), **bold**, `inline code`, [links](url), `- *` lists,
// and `> blockquotes`. Italics, tables, and HTML are passed through
// unchanged. Fenced code blocks are NOT handled here — callers are expected
// to track fence state and emit code-block lines verbatim, since per-line
// rendering can't see block context anyway.
//
// Render is pure: pass it the full accumulated markdown text and it returns
// rendered lines. There is no streaming state, so callers can re-render
// after each chunk without inconsistency.
package patchmd

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiItalic = "\033[3m"
	ansiDim    = "\033[2m"

	// Attribute-specific SGR "off" codes. Inline spans close with the
	// off-code for only the attribute(s) they set, rather than a blanket
	// ansiReset, so a nested inner span does not clear an enclosing span's
	// style. E.g. for "**bold [link](u) end**" the link closes underline +
	// color but leaves bold active through " end".
	ansiBoldOff      = "\033[22m" // also clears dim
	ansiItalicOff    = "\033[23m"
	ansiUnderlineOff = "\033[24m"
	ansiStrikeOff    = "\033[29m"
	ansiFgDefault    = "\033[39m" // reset foreground color only
	ansiUnderline    = "\033[4m"
	ansiStrike       = "\033[9m"
)

func fgRGB(r, g, b int) string { return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b) }

// Theme colors. Tweak here to restyle the renderer.
var (
	colYellow = fgRGB(234, 179, 8)
	colCyan   = fgRGB(6, 182, 212)
	colDim    = fgRGB(156, 163, 175)
)

var (
	boldItalicRe = regexp.MustCompile(`\*\*\*([^*\n]+?)\*\*\*`)
	boldRe       = regexp.MustCompile(`\*\*([^*\n]+?)\*\*`)
	italicRe     = regexp.MustCompile(`\*([^*\n]+?)\*`)
	strikeRe     = regexp.MustCompile(`~~([^~\n]+?)~~`)
	codeRe       = regexp.MustCompile("`([^`\n]+?)`")
	linkRe       = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\n]+)\)`)
	listRe       = regexp.MustCompile(`^(\s*)([-*]|\d+\.)\s+(.*)$`)
	// Matches a stashed inline-code placeholder: codeToken, the index digits,
	// codeToken. Built from the constant so the sentinel never appears as an
	// invisible literal in the pattern string.
	codeTokenRe = regexp.MustCompile(string(codeToken) + `\d+` + string(codeToken))
)

// Render converts markdown text to a slice of ANSI-styled lines.
// width is used only to enforce a minimum sanity bound; line wrapping
// is the caller's responsibility (e.g. via the TUI's wrapToWidth).
func Render(text string, width int) []string {
	if width < 10 {
		width = 80
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")

		// Headers (# ## ###).
		if h := headerLevel(trimmed); h > 0 {
			content := trimmed[h+1:] // skip the # chars and trailing space
			style := ansiBold + colYellow
			if h >= 3 {
				style = ansiBold + colDim
			}
			out = append(out, style+strings.Repeat("#", h)+" "+applyInline(content)+ansiReset)
			continue
		}

		// Blockquote.
		if strings.HasPrefix(trimmed, "> ") {
			indent := line[:len(line)-len(trimmed)]
			content := trimmed[2:]
			out = append(out, indent+colDim+"│ "+ansiReset+applyInline(content))
			continue
		}

		// List item (- *  or  1.).
		if m := listRe.FindStringSubmatch(line); m != nil {
			indent := m[1]
			marker := m[2]
			content := m[3]
			bullet := "•"
			if marker != "-" && marker != "*" {
				// Numbered: keep the original marker.
				bullet = marker
			}
			out = append(out, indent+colYellow+bullet+ansiReset+" "+applyInline(content))
			continue
		}

		// Plain paragraph (or empty line).
		out = append(out, applyInline(line))
	}

	return out
}

// codeToken is a placeholder character (U+E000, BMP private use area) used
// to stash inline-code spans during inline rendering. Without this, the
// ANSI escape that styles a code span (e.g. "\033[38;2;...m") contains a
// literal '[' that linkRe treats as the start of "[label](url)", which
// causes it to swallow the escape, the code span, and any link that
// follows. The byte is unlikely to appear in user input; if it does, it
// is treated as ordinary text and escapes the inline renderer unchanged.
const codeToken = ''

// applyInline applies inline formatting to a single line.
//
// Code spans are extracted to placeholder tokens FIRST, then inline
// formatters run over the placeholder-only text, then code spans are
// re-inserted with their styling. This isolation prevents the '['/'\033['
// collision between regex-based link parsing and ANSI-escaped code spans.
func applyInline(s string) string {
	// Stash code-span contents as placeholders so subsequent regexes
	// don't see escape sequences containing '['. We keep order via a
	// counter; the placeholder is "<token><index><token>".
	var stash []string
	s = codeRe.ReplaceAllStringFunc(s, func(m string) string {
		idx := len(stash)
		stash = append(stash, m[1:len(m)-1]) // inner, no backticks
		return fmt.Sprintf("%c%d%c", codeToken, idx, codeToken)
	})

	// Links — render as cyan underlined text, drop URL. Run FIRST, before
	// the bold/italic/strike formatters introduce ANSI escapes: those
	// escapes contain a literal '[' (e.g. "\033[1m") that linkRe's
	// `\[...\]\(...\)` pattern would otherwise mis-parse, swallowing the
	// escape and corrupting the preceding span. With links resolved first,
	// no escape '[' exists when linkRe runs. The link span closes only its
	// own attributes (underline + foreground color) so an enclosing bold or
	// italic survives the nested link.
	s = linkRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := linkRe.FindStringSubmatch(m)
		return ansiUnderline + colCyan + sub[1] + ansiUnderlineOff + ansiFgDefault
	})
	// Bold italic ***text***.
	s = boldItalicRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[3 : len(m)-3]
		return ansiBold + ansiItalic + inner + ansiItalicOff + ansiBoldOff
	})
	// Bold **text**.
	s = boldRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[2 : len(m)-2]
		return ansiBold + inner + ansiBoldOff
	})
	// Italic *text*.
	s = italicRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[1 : len(m)-1]
		return ansiItalic + inner + ansiItalicOff
	})
	// Strikethrough ~~text~~.
	s = strikeRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[2 : len(m)-2]
		return ansiStrike + inner + ansiStrikeOff
	})

	// Expand code-span placeholders.
	if len(stash) > 0 {
		s = codeTokenRe.ReplaceAllStringFunc(s, func(m string) string {
			// Strip the surrounding tokens to get the index digits.
			idx := 0
			for _, r := range m {
				if r == codeToken {
					continue
				}
				idx = idx*10 + int(r-'0')
			}
			if idx < 0 || idx >= len(stash) {
				return m
			}
			// Close only the foreground color so a code span nested inside
			// an enclosing bold/italic span does not clear that style.
			return colYellow + "`" + stash[idx] + "`" + ansiFgDefault
		})
	}
	return s
}

// headerLevel returns the header level (1-6) if line starts with "# " etc,
// or 0 if it isn't a header.
func headerLevel(line string) int {
	n := 0
	for n < 6 && n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n >= len(line) || line[n] != ' ' {
		return 0
	}
	return n
}
