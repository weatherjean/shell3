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
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 5, "he…"},
		{"héllo", 4, "h…"},
		{"hello", 2, "he"},
		{"hello world", 0, ""},
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
		{"héllo", 4, "…llo"},
		{"hello", 0, ""},
		{"hello", -1, ""},
	} {
		if got := Tail(tc.in, tc.max); got != tc.want {
			t.Errorf("Tail(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
}

func TestTail_RuneSafety(t *testing.T) {
	s := strings.Repeat("é", 100)
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

func TestTruncate_RuneSafety(t *testing.T) {
	s := strings.Repeat("é", 100)
	got := Truncate(s, 6)
	if got != "é…" {
		t.Errorf("Truncate = %q, want one é + ellipsis", got)
	}
	if len(got) > 6 {
		t.Errorf("Truncate result %d bytes, exceeds max 6", len(got))
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
