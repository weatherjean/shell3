package cli

import (
	"os"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	lipgloss "charm.land/lipgloss/v2"
	"golang.org/x/term"
)

// styleFor picks the glamour style: plain text on a non-TTY, otherwise the
// stock matching the terminal's background — a light terminal must never get
// the dark palette (it renders as pale-on-pale).
func styleFor(tty, darkBackground bool) string {
	switch {
	case !tty:
		return styles.NoTTYStyle
	case darkBackground:
		return styles.DarkStyle
	default:
		return styles.LightStyle
	}
}

// RenderMarkdown renders md for the terminal via glamour, styled to the
// terminal's own background (plain text on a non-TTY). On any rendering
// error it returns the source unrendered — a pretty message must never break
// the flow that prints it.
func RenderMarkdown(md string) string {
	tty := term.IsTerminal(int(os.Stdout.Fd()))
	dark := true
	if tty {
		// Queries the terminal; falls back to dark when it can't answer.
		dark = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(styleFor(tty, dark)),
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
