package tui

import "testing"

// The markdown palette must stay identical to the theme's active colors —
// markdown derives its hexes from the same palette (via hexOf), so a palette
// change (light/dark sensing, Lua override) can never leave assistant markdown a
// different color than the rest of the TUI.
func TestMarkdownStyleUsesThemeTokens(t *testing.T) {
	c := shell3MarkdownStyle()
	cases := []struct {
		name string
		got  *string
		want string
	}{
		{"H1", c.H1.Color, hexOf(cPrimary)},
		{"Heading", c.Heading.Color, hexOf(cPrimary)},
		{"Code", c.Code.Color, hexOf(cReason)},
		{"Link", c.Link.Color, hexOf(cCyan)},
		{"Document", c.Document.Color, hexOf(cFg)},
		{"BlockQuote", c.BlockQuote.Color, hexOf(cFgDim)},
	}
	for _, tc := range cases {
		if tc.got == nil {
			t.Errorf("%s color is unset, want %s", tc.name, tc.want)
			continue
		}
		if *tc.got != tc.want {
			t.Errorf("%s color = %s, want %s", tc.name, *tc.got, tc.want)
		}
	}
}
