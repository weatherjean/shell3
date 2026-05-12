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

func TestRendererSplitsMultilineEntries(t *testing.T) {
	r := New()
	var out bytes.Buffer
	r.SetOutput(&out)

	r.Render([]string{"? bash: line1\nline2\nline3" + CursorMarker})

	got := out.String()
	// Each split row must be separated by CRLF, not bare LF.
	if strings.Contains(got, "line1\nline2") {
		t.Fatalf("bare LF between split rows: %q", got)
	}
	for _, want := range []string{"line1", "line2", "line3"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing row %q: %q", want, got)
		}
	}
	if !strings.Contains(got, "line1\r\n") || !strings.Contains(got, "line2\r\n") {
		t.Fatalf("rows not separated by CRLF: %q", got)
	}
}

func TestRendererStripsCarriageReturns(t *testing.T) {
	r := New()
	var out bytes.Buffer
	r.SetOutput(&out)

	r.Render([]string{"a\r\nb" + CursorMarker})
	got := out.String()
	if strings.Contains(got, "a\r\r\n") {
		t.Fatalf("double CR leaked through: %q", got)
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
