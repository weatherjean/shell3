package patchapp

import (
	"testing"

	"github.com/weatherjean/shell3/internal/patchtui"
)

func TestRenderInputBox_UsesVisibleWidthForWrapping(t *testing.T) {
	in := []rune("aaaa👋bbbb")
	lines := renderInputBox(in, len(in), 8, false) // avail content width = 6
	if len(lines) < 2 {
		t.Fatalf("expected wrapped lines, got %d", len(lines))
	}
	for i, l := range lines {
		if got := patchtui.VisibleLen(l); got != 8 {
			t.Fatalf("line %d visible len = %d; want 8; line=%q", i, got, l)
		}
	}
}

func TestInputCursorRoundTrip_WithWideRune(t *testing.T) {
	in := []rune("hello 👋 world")
	w := 10
	for off := 0; off <= len(in); off++ {
		row, col := inputCursorPos(in, off, w)
		got := inputOffsetForRowCol(in, w, row, col)
		if got != off {
			t.Fatalf("offset %d -> (%d,%d) -> %d", off, row, col, got)
		}
	}
}

func TestInsertAtVisibleCol_WideRunePositioning(t *testing.T) {
	line := "ab👋cd"
	got := insertAtVisibleCol(line, 4, "|")
	want := "ab👋|cd"
	if got != want {
		t.Fatalf("insertAtVisibleCol = %q; want %q", got, want)
	}
}

func TestInsertAtVisibleCol_PreservesANSIAndTail(t *testing.T) {
	line := patchtui.BgRGB(1, 2, 3) + "ok ✅" + patchtui.Reset
	got := insertAtVisibleCol(line, patchtui.VisibleLen("ok ✅"), patchtui.CursorMarker)
	if patchtui.VisibleLen(got) != patchtui.VisibleLen(line)+patchtui.VisibleLen(patchtui.CursorMarker) {
		t.Fatalf("visible width mismatch: got=%d want=%d", patchtui.VisibleLen(got), patchtui.VisibleLen(line)+patchtui.VisibleLen(patchtui.CursorMarker))
	}
	if len(got) <= len(line) {
		t.Fatalf("marker not inserted")
	}
}

func TestRenderInputBox_BubbleBackgroundFillsEveryWrappedLine(t *testing.T) {
	in := []rune("line1\n\n- bullet with wide 你好👋text")
	lines := renderInputBox(in, len(in), 24, false)
	if len(lines) < 3 {
		t.Fatalf("expected multiple lines, got %d", len(lines))
	}
	for i, l := range lines {
		if got := patchtui.VisibleLen(l); got != 24 {
			t.Fatalf("line %d visible len = %d; want 24; line=%q", i, got, l)
		}
	}
}

func TestRenderInputBox_ZWJAndVSBackgroundFill(t *testing.T) {
	// Paste content that triggered the original background gap: lines with ZWJ
	// sequences (👩‍💻), variation-selector emoji (🖥️), and out-of-range emoji
	// (✨ ✅) were overcounted by the old rune-by-rune width logic, making the
	// padding too small and leaving the right edge of the bubble transparent.
	const w = 60
	in := []rune("emoji: 👋👋👋🚀\nmixed: abcdefghij 👩‍💻🖥️✨ こんにちは\ndone ✅")
	lines := renderInputBox(in, 0, w, false)
	for i, l := range lines {
		got := patchtui.VisibleLen(l)
		if got != w {
			t.Fatalf("line %d: visible len = %d, want %d\n  line=%q", i, got, w, l)
		}
	}
}
