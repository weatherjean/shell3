package patchtui

import "testing"

func TestRuneWidth(t *testing.T) {
	cases := []struct {
		name string
		r    rune
		want int
	}{
		{"ascii lowercase", 'a', 1},
		{"ascii digit", '5', 1},
		{"ascii space", ' ', 1},
		{"latin extended", 'ñ', 1},
		{"em dash", '—', 1},
		{"emoji wave", '👋', 2},
		{"emoji rocket", '🚀', 2},
		{"emoji star", '🌟', 2},
		{"cjk han", '日', 2},
		{"hangul syllable", '한', 2},
		{"fullwidth digit", '５', 2},
		// Zero-width: must not inflate padding calculations.
		{"zwj", '‍', 0},
		{"variation selector 16", '️', 0},
		{"variation selector 15", '︎', 0},
		{"zero width space", '​', 0},
		// Emoji outside the 0x1F000 block.
		{"sparkles U+2728", '✨', 2},
		{"check mark button U+2705", '✅', 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RuneWidth(c.r); got != c.want {
				t.Errorf("RuneWidth(%q) = %d, want %d", c.r, got, c.want)
			}
		})
	}
}

func TestVisibleLen(t *testing.T) {
	cases := []struct {
		name string
		s    string
		want int
	}{
		{"empty", "", 0},
		{"plain ascii", "hello", 5},
		{"with ansi color", "\033[31mred\033[0m", 3},
		{"with bold + bg", "\033[1m\033[48;2;40;44;52mhi\033[0m", 2},
		{"emoji only", "👋", 2},
		{"emoji with text", "Hi 👋", 5},
		{"cjk", "日本", 4},
		{"mixed", "Hello 🚀 world", 14},
		// Grapheme clusters: the whole sequence is 2 columns, not the rune sum.
		{"zwj sequence woman+laptop", "👩‍💻", 2},
		{"variation selector emoji", "🖥️", 2},
		// Emoji outside 0x1F000 block.
		{"sparkles", "✨", 2},
		{"check mark button", "✅", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := VisibleLen(c.s); got != c.want {
				t.Errorf("VisibleLen(%q) = %d, want %d", c.s, got, c.want)
			}
		})
	}
}

func TestSplitLines(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a\n", []string{"a"}},
		{"a\nb", []string{"a", "b"}},
		{"a\nb\n", []string{"a", "b"}},
		{"\n", nil},
	}
	for _, tc := range cases {
		got := SplitLines(tc.in)
		if !equalStringSlices(got, tc.want) {
			t.Errorf("SplitLines(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func equalStringSlices(a, b []string) bool {
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
