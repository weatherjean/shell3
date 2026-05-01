package patchapp

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/patchtui"
)

func TestWrapToWidth(t *testing.T) {
	cases := []struct {
		name  string
		in    []string
		width int
		want  []string
	}{
		{
			name:  "fits",
			in:    []string{"hello"},
			width: 10,
			want:  []string{"hello"},
		},
		{
			name:  "splits on word boundary",
			in:    []string{"hello world"},
			width: 5,
			want:  []string{"hello", "world"},
		},
		{
			name:  "preserves multiple input lines",
			in:    []string{"abc", "def"},
			width: 10,
			want:  []string{"abc", "def"},
		},
		{
			name:  "emoji counts as 2 cols",
			in:    []string{"abc👋de"}, // visual width: 1+1+1+2+1+1 = 7
			width: 5,
			want:  []string{"abc👋", "de"},
		},
		{
			name:  "emoji at boundary doesn't split mid-rune",
			in:    []string{"abcd👋e"}, // a,b,c,d=4 cols + 👋 (2) would push to 6
			width: 5,
			want:  []string{"abcd", "👋e"},
		},
		{
			name:  "ansi escape passes through",
			in:    []string{"\033[31mhello\033[0m world"},
			width: 5,
			want:  []string{"\033[31mhello\033[0m", "world"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := wrapToWidth(c.in, c.width)
			if !equalStrings(got, c.want) {
				t.Errorf("wrapToWidth(%q, %d):\n  got:  %q\n  want: %q",
					c.in, c.width, got, c.want)
			}
			// Invariant: every output line's visible width must be <= width.
			for _, l := range got {
				if patchtui.VisibleLen(l) > c.width {
					t.Errorf("output line %q has visible width %d > %d",
						l, patchtui.VisibleLen(l), c.width)
				}
			}
		})
	}
}

func TestWrapToWidth_ListHangingIndent(t *testing.T) {
	got := wrapToWidth([]string{"• But some committed output paths likely still rely on terminal wrapping"}, 24)
	want := []string{
		"• But some committed",
		"  output paths likely",
		"  still rely on terminal",
		"  wrapping",
	}
	if !equalStrings(got, want) {
		t.Fatalf("wrapToWidth list hang:\n  got:  %q\n  want: %q", got, want)
	}
}

func TestWrapToWidth_TableAndFenceUseHardWrap(t *testing.T) {
	lines := []string{
		"| col1 | col2 | col3 |",
		"```",
		"this is a long code-ish line that should still be width-bounded",
		"```",
	}
	got := wrapToWidth(lines, 12)
	for i, l := range got {
		if patchtui.VisibleLen(l) > 12 {
			t.Fatalf("line %d visible=%d > 12: %q", i, patchtui.VisibleLen(l), l)
		}
	}
}

func TestWrapCommittedLines_UsesConservativeWrap(t *testing.T) {
	got := wrapCommittedLines([]string{"• But some committed output paths likely still rely on terminal wrapping"}, 24)
	want := []string{
		"• But some committed",
		"  output paths likely",
		"  still rely on terminal",
		"  wrapping",
	}
	if !equalStrings(got, want) {
		t.Fatalf("wrapCommittedLines:\n  got:  %q\n  want: %q", got, want)
	}
}

func TestWrapCommittedLines_PreservesStyledLines(t *testing.T) {
	styled := "\033[40m\033[38;2;200;200;200m  hello world\033[0m"
	got := wrapCommittedLines([]string{styled}, 8)
	want := []string{styled}
	if !equalStrings(got, want) {
		t.Fatalf("wrapCommittedLines styled:\n  got:  %q\n  want: %q", got, want)
	}
}

func TestWrapToWidthNoSoftWrap(t *testing.T) {
	// Long mixed-content line common in chat: emoji + ascii.
	// Wrapping it at typical terminal widths must never produce a line
	// whose visual width exceeds the target.
	long := "Hi there! 👋 Welcome to shell3. " +
		strings.Repeat("Lorem ipsum 🚀 dolor sit amet ", 5)

	for _, w := range []int{20, 40, 80, 120} {
		got := wrapToWidth([]string{long}, w)
		for i, l := range got {
			if patchtui.VisibleLen(l) > w {
				t.Fatalf("width=%d line %d visible=%d (>%d): %q",
					w, i, patchtui.VisibleLen(l), w, l)
			}
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBusySetTokensDefersUntilContentRender(t *testing.T) {
	var out strings.Builder
	app := New("test", "provider/model", WelcomeInfo{})
	app.r.SetOutput(&out)

	app.SetBusy(true, nil)
	out.Reset()

	app.SetTokens(42)
	if got := out.String(); got != "" {
		t.Fatalf("busy SetTokens should not render immediately, got %q", got)
	}

	app.mu.Lock()
	app.renderStatusOnly()
	app.mu.Unlock()
	if strings.Contains(out.String(), "t:42") {
		t.Fatalf("status-only render should not apply pending token update: %q", out.String())
	}
	out.Reset()

	app.Print([]string{"committed"})
	got := out.String()
	if !strings.Contains(got, "committed\r\n") || !strings.Contains(got, "t:42") {
		t.Fatalf("content render should commit output and apply pending tokens: %q", got)
	}
	if strings.Count(got, "\x1b[?2026h") != 1 || strings.Count(got, "\x1b[?2026l") != 1 {
		t.Fatalf("content render should be one synchronized update: %q", got)
	}
}
