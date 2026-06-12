package patchtui

import (
	"strings"
	"testing"
)

// TestRainbowPreservesText pins that Rainbow only adds SGR color codes: stripping
// the escapes returns the original string byte-for-byte, so the visible banner
// text is never mangled by the colorizer.
func TestRainbowPreservesText(t *testing.T) {
	for _, in := range []string{"", "x", "✦ conversation compacted ✦", "multi\nline\ttabs"} {
		got := Rainbow(in)
		if back := StripANSI(got); back != in {
			t.Errorf("Rainbow(%q): StripANSI = %q, want %q", in, back, in)
		}
	}
}

// TestRainbowClosesReset pins that a non-empty Rainbow string ends with Reset so
// the color never bleeds into subsequent terminal output.
func TestRainbowClosesReset(t *testing.T) {
	got := Rainbow("hi")
	if !strings.HasSuffix(got, Reset) {
		t.Errorf("Rainbow output must end with Reset; got %q", got)
	}
	if got := Rainbow(""); got != "" {
		t.Errorf("Rainbow(\"\") = %q, want empty", got)
	}
}
