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

func sptr(s string) *string { return &s }
func bptr(b bool) *bool     { return &b }

// shell3MarkdownStyle recolors glamour's base theme to shell3's active palette
// (theme.go), with the document margin removed so blocks hug the transcript's
// gutter. The colors derive from the live palette via hexOf, so markdown tracks
// the light/dark sensing. The base is glamour's light or dark theme, chosen by the
// sensed terminal (activeLight) so glamour's own defaults (e.g. code-block chrome)
// suit it — not by the fg color, which a shell3.theme override could flip.
func shell3MarkdownStyle() ansi.StyleConfig {
	var c ansi.StyleConfig
	if activeLight {
		c = styles.LightStyleConfig
	} else {
		c = styles.DarkStyleConfig
	}
	mdYellow, mdReason, mdCyan := hexOf(cPrimary), hexOf(cReason), hexOf(cCyan)
	mdDim, mdFg := hexOf(cFgDim), hexOf(cFg)

	c.Document.Margin = func() *uint { z := uint(0); return &z }()
	c.Document.Color = sptr(mdFg)

	// Headings: flat brand color, no filled background block.
	c.Heading.Color, c.Heading.Bold = sptr(mdYellow), bptr(true)
	c.Heading.BackgroundColor = nil
	for _, h := range []*ansi.StyleBlock{&c.H1, &c.H2, &c.H3, &c.H4, &c.H5, &c.H6} {
		h.Color, h.Bold, h.BackgroundColor = sptr(mdYellow), bptr(true), nil
	}
	c.H1.Prefix, c.H1.Suffix = "", ""

	c.Strong.Color, c.Strong.Bold = sptr(mdFg), bptr(true)
	c.Emph.Italic = bptr(true)
	c.Link.Color, c.Link.Underline = sptr(mdCyan), bptr(true)
	c.LinkText.Color = sptr(mdCyan)
	c.Code.Color = sptr(mdReason)
	c.BlockQuote.Color = sptr(mdDim)
	c.HorizontalRule.Color = sptr(mdDim)
	c.Item.Color = sptr(mdFg)
	return c
}

// mdEpoch counts palette changes. Rendered assistant markdown is memoized per
// item (see items.go, keyed on width+len); those keys don't change on a palette
// switch, so the epoch is also part of the item key — bumping it here forces
// already-rendered blocks to recolor, not just newly appended ones.
var mdEpoch uint64

// resetMarkdown drops the width-keyed renderer cache and bumps mdEpoch so the next
// render picks up a palette change — both for new items and for ones already
// rendered under the old palette (applyPalette calls this).
func resetMarkdown() {
	mdMu.Lock()
	mdCache = map[int]*glamour.TermRenderer{}
	mdMu.Unlock()
	mdEpoch++
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
