package patchtui

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRendererWritesValidUTF8(t *testing.T) {
	r := New()
	var out bytes.Buffer
	r.SetOutput(&out)

	lines := []string{"punctuation: — ’ →", "prompt: ➜ ✗", "emoji: 👋"}
	r.Print(lines)
	r.Render([]string{"live: — ’ →" + CursorMarker})

	if !utf8.Valid(out.Bytes()) {
		t.Fatalf("renderer output is not valid UTF-8: %q", out.String())
	}
	for _, want := range []string{"—", "’", "→", "➜", "✗", "👋"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("renderer output missing %q: %q", want, out.String())
		}
	}
}
