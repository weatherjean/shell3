package patchapp

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/patchtui"
)

func TestRenderUserMessage_WrapsBeforeStylingAndFillsWidth(t *testing.T) {
	got := renderUserMessage(strings.Repeat("a", 12), 10)
	if len(got) != 2 {
		t.Fatalf("lines = %d; want 2 (%q)", len(got), got)
	}
	for i, l := range got {
		if w := patchtui.VisibleLen(l); w != 10 {
			t.Fatalf("line %d visible width = %d; want 10; line=%q", i, w, l)
		}
	}
}

func TestRenderUserMessage_WideCharsFillWidth(t *testing.T) {
	got := renderUserMessage("你好世界こんにちは", 14)
	for i, l := range got {
		if w := patchtui.VisibleLen(l); w != 14 {
			t.Fatalf("line %d visible width = %d; want 14; line=%q", i, w, l)
		}
	}
}

func TestRenderUserMessage_MixedWidthAlwaysFillsLine(t *testing.T) {
	msg := "mixed width: abcdefghij 👩💻🚀✨ こんにちは世界 مرحبا بالعالم"
	got := renderUserMessage(msg, 24)
	if len(got) < 2 {
		t.Fatalf("expected wrapping, got %d line(s)", len(got))
	}
	for i, l := range got {
		if w := patchtui.VisibleLen(l); w != 24 {
			t.Fatalf("line %d visible width = %d; want 24; line=%q", i, w, l)
		}
	}
}
