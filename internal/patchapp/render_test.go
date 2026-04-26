package patchapp

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/patchtui"
)

func TestWrapToWidth(t *testing.T) {
	cases := []struct {
		name  string
		in    []string
		width int
		want  []string
	}{
		{
			name:  "fits",
			in:    []string{"hello"},
			width: 10,
			want:  []string{"hello"},
		},
		{
			name:  "splits ascii",
			in:    []string{"hello world"},
			width: 5,
			want:  []string{"hello", " worl", "d"},
		},
		{
			name:  "preserves multiple input lines",
			in:    []string{"abc", "def"},
			width: 10,
			want:  []string{"abc", "def"},
		},
		{
			name:  "emoji counts as 2 cols",
			in:    []string{"abc👋de"}, // visual width: 1+1+1+2+1+1 = 7
			width: 5,
			want:  []string{"abc👋", "de"},
		},
		{
			name:  "emoji at boundary doesn't split mid-rune",
			in:    []string{"abcd👋e"}, // a,b,c,d=4 cols + 👋 (2) would push to 6
			width: 5,
			want:  []string{"abcd", "👋e"},
		},
		{
			name:  "ansi escape passes through",
			in:    []string{"\033[31mhello\033[0m world"},
			width: 5,
			want:  []string{"\033[31mhello\033[0m", " worl", "d"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := wrapToWidth(c.in, c.width)
			if !equalStrings(got, c.want) {
				t.Errorf("wrapToWidth(%q, %d):\n  got:  %q\n  want: %q",
					c.in, c.width, got, c.want)
			}
			// Invariant: every output line's visible width must be <= width.
			for _, l := range got {
				if patchtui.VisibleLen(l) > c.width {
					t.Errorf("output line %q has visible width %d > %d",
						l, patchtui.VisibleLen(l), c.width)
				}
			}
		})
	}
}

func TestWrapToWidthNoSoftWrap(t *testing.T) {
	// Long mixed-content line common in chat: emoji + ascii.
	// Wrapping it at typical terminal widths must never produce a line
	// whose visual width exceeds the target.
	long := "Hi there! 👋 Welcome to shell3. " +
		strings.Repeat("Lorem ipsum 🚀 dolor sit amet ", 5)

	for _, w := range []int{20, 40, 80, 120} {
		got := wrapToWidth([]string{long}, w)
		for i, l := range got {
			if patchtui.VisibleLen(l) > w {
				t.Fatalf("width=%d line %d visible=%d (>%d): %q",
					w, i, patchtui.VisibleLen(l), w, l)
			}
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
