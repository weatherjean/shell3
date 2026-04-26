package tui

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
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runeWidth(c.r); got != c.want {
				t.Errorf("runeWidth(%q) = %d, want %d", c.r, got, c.want)
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
		{"emoji with text", "Hi 👋", 5}, // H=1, i=1, ' '=1, 👋=2
		{"cjk", "日本", 4},
		{"mixed", "Hello 🚀 world", 14}, // 5 + 1 + 2 + 1 + 5 = 14
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := visibleLen(c.s); got != c.want {
				t.Errorf("visibleLen(%q) = %d, want %d", c.s, got, c.want)
			}
		})
	}
}
