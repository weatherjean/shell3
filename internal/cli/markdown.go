package cli

import (
	"os"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"golang.org/x/term"
)

// RenderMarkdown renders md for the terminal via glamour, styled to match the
// brand banner's fixed dark palette (plain text on a non-TTY). On any
// rendering error it returns the source unrendered — a pretty message must
// never break the flow that prints it.
func RenderMarkdown(md string) string {
	style := styles.DarkStyle
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		style = styles.NoTTYStyle
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(style),
		glamour.WithWordWrap(78),
	)
	if err != nil {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return out
}
