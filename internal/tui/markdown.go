package tui

import (
	"regexp"
	"strings"
	"sync"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
)

// trailingPad matches glamour's run of (SGR-wrapped) trailing spaces that pads
// every line to the wrap width. \s excludes the non-breaking space glamour uses
// for inline-code padding, so code spans survive.
var trailingPad = regexp.MustCompile(`(?:\x1b\[[0-9;]*m| )+$`)

// shell3's markdown palette as hex strings (glamour wants string pointers).
const (
	mdYellow = "#EAB308" // headings — cPrimary
	mdSage   = "#87A58C" // code — cSage
	mdCyan   = "#5BB6C9" // links — cCyan
	mdDim    = "#9CA3AF" // blockquote / hr — cFgDim
	mdUser   = "#E5E7EB" // body / strong — cUser
)

func sptr(s string) *string { return &s }
func bptr(b bool) *bool     { return &b }

// shell3MarkdownStyle is glamour's dark theme recolored to shell3's palette,
// with the document margin removed so blocks hug the transcript's gutter.
func shell3MarkdownStyle() ansi.StyleConfig {
	c := styles.DarkStyleConfig
	c.Document.Margin = func() *uint { z := uint(0); return &z }()
	c.Document.Color = sptr(mdUser)

	// Headings: flat yellow, no filled background block.
	c.Heading.Color, c.Heading.Bold = sptr(mdYellow), bptr(true)
	c.Heading.BackgroundColor = nil
	for _, h := range []*ansi.StyleBlock{&c.H1, &c.H2, &c.H3, &c.H4, &c.H5, &c.H6} {
		h.Color, h.Bold, h.BackgroundColor = sptr(mdYellow), bptr(true), nil
	}
	c.H1.Prefix, c.H1.Suffix = "", ""

	c.Strong.Color, c.Strong.Bold = sptr(mdUser), bptr(true)
	c.Emph.Italic = bptr(true)
	c.Link.Color, c.Link.Underline = sptr(mdCyan), bptr(true)
	c.LinkText.Color = sptr(mdCyan)
	c.Code.Color = sptr(mdSage)
	c.BlockQuote.Color = sptr(mdDim)
	c.HorizontalRule.Color = sptr(mdDim)
	c.Item.Color = sptr(mdUser)
	return c
}

// Markdown renderers are width-bound and stateful (goldmark carries block state
// across Render), so we memoize one per width. The Update loop is
// single-threaded; the mutex only guards the cache map itself.
var (
	mdMu    sync.Mutex
	mdCache = map[int]*glamour.TermRenderer{}
)

// renderMarkdown renders assistant text as terminal markdown wrapped to width.
// On any error (or empty input) it returns the raw text unchanged, so a bad
// renderer never blanks the transcript.
func renderMarkdown(text string, width int) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	if width < 20 {
		width = 20
	}
	mdMu.Lock()
	r, ok := mdCache[width]
	if !ok {
		var err error
		r, err = glamour.NewTermRenderer(
			glamour.WithStyles(shell3MarkdownStyle()),
			glamour.WithWordWrap(width),
			glamour.WithEmoji(),
		)
		if err != nil {
			mdMu.Unlock()
			return text
		}
		// Bound the cache so a long run of unique widths (window drags, tmux
		// resizes) can't grow it without limit — only a few are ever in use.
		if len(mdCache) >= 8 {
			mdCache = map[int]*glamour.TermRenderer{}
		}
		mdCache[width] = r
	}
	mdMu.Unlock()

	out, err := r.Render(text)
	if err != nil {
		return text
	}
	// Strip glamour's full-width trailing padding per line (re-adding a reset so
	// a stripped closing SGR can't bleed color into the next line's gutter).
	lines := strings.Split(strings.Trim(out, "\n"), "\n")
	for i, l := range lines {
		stripped := trailingPad.ReplaceAllString(l, "")
		if stripped != l && stripped != "" {
			stripped += "\x1b[0m"
		}
		lines[i] = stripped
	}
	return strings.Join(lines, "\n")
}
