package patchapp

import (
	"fmt"
	"io"

	"github.com/weatherjean/shell3/internal/patchtui"
)

// PrintHeader writes the shell3 brand header to w. Used as a uniform top
// banner for non-interactive CLI commands. Two lines of styled text plus a
// trailing blank line.
func PrintHeader(w io.Writer) {
	yellow := patchtui.FgRGB(rPrimary, gPrimary, bPrimary)
	dim := patchtui.FgRGB(rMuted, gMuted, bMuted)
	sub := patchtui.FgRGB(rFgDim, gFgDim, bFgDim)
	_, _ = fmt.Fprintln(w, styled("◆ shell3", yellow, "", true)+"  "+styled("/'ʃɛli/", dim, "", false))
	_, _ = fmt.Fprintln(w, styled("AI-powered shell assistant", sub, "", false))
	_, _ = fmt.Fprintln(w)
}
