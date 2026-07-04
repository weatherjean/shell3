package strutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncate(t *testing.T) {
	for _, tc := range []struct {
		in   string
		max  int
		want string
	}{
		{"hello", 10, "hello"},       // under cap: unchanged
		{"hello", 5, "hello"},        // exactly at cap: unchanged
		{"hello world", 5, "hello…"}, // cut with ellipsis
		{"héllo", 2, "h…"},           // é spans bytes 1-2: cut backs up to the rune boundary
		{"", 5, ""},
	} {
		if got := Truncate(tc.in, tc.max); got != tc.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
}

func TestTail(t *testing.T) {
	for _, tc := range []struct {
		in   string
		max  int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "…world"},
		{"héllo", 4, "…llo"}, // cut lands mid-é: advances past it
		{"hello", 0, ""},
		{"hello", -1, ""},
	} {
		if got := Tail(tc.in, tc.max); got != tc.want {
			t.Errorf("Tail(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
}

func TestClipRunes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello!", 5, "hell…"}, // n-1 runes + ellipsis = n runes total
		{"héllo!", 3, "hé…"},   // rune-count, not bytes
		{"hi", 0, "…"},         // n clamped to 1 → 0 runes + ellipsis
	} {
		if got := ClipRunes(tc.in, tc.n); got != tc.want {
			t.Errorf("ClipRunes(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

// TestTail_RuneSafety pins that a byte-budget cut never lands mid-UTF-8
// sequence for any small budget.
func TestTail_RuneSafety(t *testing.T) {
	s := strings.Repeat("é", 100) // 2 bytes per rune
	for n := 1; n < 10; n++ {
		got := Tail(s, n)
		if !strings.HasPrefix(got, "…") || !utf8.ValidString(got) {
			t.Errorf("Tail(%d runes, %d) = %q, not rune-safe", 100, n, got)
		}
		if rest := strings.TrimPrefix(got, "…"); rest != "" && !strings.HasSuffix(rest, "é") {
			t.Errorf("Tail(%d runes, %d) = %q, kept a partial rune", 100, n, got)
		}
	}
}

// TestTruncate_RuneSafety pins the mid-rune back-off.
func TestTruncate_RuneSafety(t *testing.T) {
	s := strings.Repeat("é", 100)
	got := Truncate(s, 5) // 5 bytes lands mid-rune; must back off to 4
	if got != strings.Repeat("é", 2)+"…" {
		t.Errorf("Truncate = %q, want two é + ellipsis", got)
	}
}

func TestCutRunes(t *testing.T) {
	if got, cut := CutRunes("hello", 3); got != "hel" || !cut {
		t.Errorf("CutRunes(hello,3) = %q,%v", got, cut)
	}
	if got, cut := CutRunes("hi", 3); got != "hi" || cut {
		t.Errorf("CutRunes(hi,3) = %q,%v", got, cut)
	}
}
