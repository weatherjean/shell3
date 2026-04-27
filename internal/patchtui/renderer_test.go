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

func TestRendererPrintAndRenderWritesOneSynchronizedUpdate(t *testing.T) {
	r := New()
	var out bytes.Buffer
	r.SetOutput(&out)

	r.Render([]string{"old frame"})
	out.Reset()
	r.PrintAndRender([]string{"committed"}, []string{"new frame" + CursorMarker})

	got := out.String()
	if strings.Count(got, "\x1b[?2026h") != 1 || strings.Count(got, "\x1b[?2026l") != 1 {
		t.Fatalf("PrintAndRender should use one synchronized update: %q", got)
	}
	if !strings.Contains(got, "committed\r\n") || !strings.Contains(got, "new frame") {
		t.Fatalf("PrintAndRender missing committed/frame output: %q", got)
	}
}
