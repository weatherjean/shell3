package cli

import (
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
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

func styleConfigFor(tty, darkBackground bool) ansi.StyleConfig {
	var style ansi.StyleConfig
	switch styleFor(tty, darkBackground) {
	case styles.DarkStyle:
		style = styles.DarkStyleConfig
	case styles.LightStyle:
		style = styles.LightStyleConfig
	default:
		style = styles.NoTTYStyleConfig
	}
	zero := uint(0)
	style.Document.Margin = &zero
	return style
}

// RenderMarkdownFor renders with terminal characteristics already known by a
// caller. Interactive front ends use this to avoid querying the terminal more
// than once and to style the writer they actually own rather than os.Stdout.
func RenderMarkdownFor(md string, tty, darkBackground bool) string {
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(styleConfigFor(tty, darkBackground)),
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
