// Package render turns runtime state into self-contained HTML views for the
// web dash (the index and runs fragments, and the run-replay page), plus the
// small formatting helpers they share. It is front-end-neutral: a caller
// serves the string over HTTP, sends it as a file, or writes it to disk.
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
	cut, trimmed := strutil.CutRunes(s, max)
	if trimmed {
		return cut + "…"
	}
	return cut
}
