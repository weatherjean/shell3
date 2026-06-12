package patchtui

import (
	"bytes"
	"fmt"
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

func TestRendererEmptyFrameTransitionStaysAtRowZero(t *testing.T) {
	r := New()
	var out bytes.Buffer
	r.SetOutput(&out)

	// Render a multi-line frame, then render an empty frame. The diff path
	// must not move the cursor above frame row 0 (into committed scrollback).
	r.Render([]string{"line one", "line two", "line three"})
	startRow := r.cursorRow // hardware cursor row relative to frame row 0
	out.Reset()
	r.Render([]string{})

	// Cursor must be parked at frame row 0, never -1 (which would mean a
	// cursor-up that overshoots the top of the frame into scrollback).
	if r.cursorRow != 0 {
		t.Fatalf("after empty Render, cursorRow = %d, want 0", r.cursorRow)
	}

	// Replay the emitted CSI up/down moves and CRLFs to track the cursor's
	// running row relative to frame row 0. It must never go negative — a
	// negative row means the cursor climbed above the frame into committed
	// scrollback, where it could erase history.
	if minRow := minCursorRow(out.String(), startRow); minRow < 0 {
		t.Fatalf("cursor reached row %d (above frame top): %q", minRow, out.String())
	}
}

// minCursorRow replays the cursor-affecting escapes in s starting from row
// and returns the lowest (most-negative) row reached. It accounts for
// CSI <n>A (up), CSI <n>B (down), and "\r\n" / "\n" (down one row).
func minCursorRow(s string, row int) int {
	lowest := row
	i := 0
	for i < len(s) {
		switch {
		case s[i] == '\n':
			row++
			i++
		case strings.HasPrefix(s[i:], "\x1b["):
			j := i + 2
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			n := 0
			if j > i+2 {
				fmt.Sscanf(s[i+2:j], "%d", &n)
			}
			if j < len(s) {
				switch s[j] {
				case 'A':
					row -= n
				case 'B':
					row += n
				}
			}
			i = j + 1
		default:
			i++
		}
		if row < lowest {
			lowest = row
		}
	}
	return lowest
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
