package patchwidgets

import (
	"strings"

	"github.com/weatherjean/shell3/internal/patchtui"
)

// Color palette. Mirrors the shell3 app palette but is self-contained so
// the package can be lifted out without external constants.
const (
	rPrimary, gPrimary, bPrimary = 234, 179, 8   // yellow
	rDim, gDim, bDim             = 156, 163, 175 // gray-400
	rMuted, gMuted, bMuted       = 107, 114, 128 // gray-500
)

func dim(s string) string   { return patchtui.FgRGB(rDim, gDim, bDim) + s + patchtui.Reset }
func muted(s string) string { return patchtui.FgRGB(rMuted, gMuted, bMuted) + s + patchtui.Reset }
func boldP(s string) string {
	return patchtui.Bold + patchtui.FgRGB(rPrimary, gPrimary, bPrimary) + s + patchtui.Reset
}

// titleLine returns the prompt header line: a bold "?" badge, the input
// question, and (optionally) a trailing dimmed hint.
func titleLine(input, hint string) string {
	var b strings.Builder
	b.WriteString(boldP("?"))
	b.WriteString(" ")
	b.WriteString(input)
	if hint != "" {
		b.WriteString("  ")
		b.WriteString(muted(hint))
	}
	return b.String()
}

// hintLine returns a dimmed footer of key bindings.
func hintLine(parts ...string) string {
	return muted(strings.Join(parts, "  "))
}
