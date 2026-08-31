// Package render turns runtime state and stored records into self-contained
// HTML documents suitable for sending as files or writing to disk.
package render

import (
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/strutil"
)

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
	return strutil.Ellipsize(s, max)
}

// stripToolIDPrefix drops the persistence-only tool-call id line before a
// stored result is shown to a person.
func stripToolIDPrefix(content string) string {
	if strings.HasPrefix(content, "[tool_call_id=") {
		if nl := strings.IndexByte(content, '\n'); nl >= 0 {
			return content[nl+1:]
		}
	}
	return content
}
