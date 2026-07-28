// Package render turns runtime state into markdown documents. It is
// front-end-neutral: a caller sends the string as a file, a message, or writes
// it to disk.
package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/strutil"
)

// maxEntry caps one rendered entry (a tool's arguments, a job's output, a
// system prompt) so a single 10 MB command output cannot dominate a document.
const maxEntry = 4 << 10

const truncMark = "\n… (truncated)"

// truncate cuts s to maxEntry bytes on a rune boundary, marking the cut.
func truncate(s string) string {
	if len(s) <= maxEntry {
		return s
	}
	// strutil.Truncate appends its own "…"; swap that for this package's marker,
	// which reads as prose in a document rather than as a glyph mid-line.
	return strings.TrimSuffix(strutil.Truncate(s, maxEntry), "…") + truncMark
}

// fence wraps body in a code fence long enough to survive backticks in the
// body itself, truncating it first.
func fence(b *strings.Builder, lang, body string) {
	body = truncate(strings.TrimRight(body, "\n"))
	ticks := "```"
	for strings.Contains(body, ticks) {
		ticks += "`"
	}
	fmt.Fprintf(b, "%s%s\n%s\n%s\n\n", ticks, lang, body, ticks)
}

// quote renders text as a markdown blockquote (used for reasoning).
func quote(b *strings.Builder, text string) {
	for _, line := range strings.Split(truncate(strings.TrimRight(text, "\n")), "\n") {
		b.WriteString("> " + line + "\n")
	}
	b.WriteString("\n")
}

// field writes one `- **name:** value` bullet, skipping empty values.
func field(b *strings.Builder, name, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, "- **%s:** %s\n", name, value)
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	// UTC, not local: these documents travel (a file sent to a phone in another
	// zone) and an unlabelled local stamp there is a wrong stamp.
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

// oneLine flattens text to a single line for list rows.
func oneLine(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " "))
	cut, trimmed := strutil.CutRunes(s, max)
	if trimmed {
		return cut + "…"
	}
	return cut
}
