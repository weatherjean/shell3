package patchmd

import (
	"strings"
	"testing"
)

// stripANSI removes CSI SGR escape sequences so tests can assert on the
// human-visible text without baking in color choices.
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\033' {
			inEsc = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func renderInline(s string) string { return applyInline(s) }

// ── inline rendering ──────────────────────────────────────────────────────────

func TestInline_PlainText(t *testing.T) {
	got := stripANSI(renderInline("hello world"))
	if got != "hello world" {
		t.Errorf("got %q", got)
	}
}

func TestInline_Bold(t *testing.T) {
	got := stripANSI(renderInline("a **bold** b"))
	if got != "a bold b" {
		t.Errorf("got %q", got)
	}
}

func TestInline_Italic(t *testing.T) {
	got := stripANSI(renderInline("a *it* b"))
	if got != "a it b" {
		t.Errorf("got %q", got)
	}
}

func TestInline_Code(t *testing.T) {
	got := stripANSI(renderInline("a `code` b"))
	if got != "a `code` b" {
		t.Errorf("got %q", got)
	}
}

func TestInline_Link(t *testing.T) {
	got := stripANSI(renderInline("see [docs](https://x.io) here"))
	if got != "see docs here" {
		t.Errorf("got %q", got)
	}
}

func TestInline_Strike(t *testing.T) {
	got := stripANSI(renderInline("a ~~gone~~ b"))
	if got != "a gone b" {
		t.Errorf("got %q", got)
	}
}

// ── regression: code span + link in same line must not leak ANSI ─────────────
//
// Previously, codeRe wrapped its match with "\033[...m" before linkRe ran.
// linkRe = `\[([^\]\n]+)\]\(([^)\n]+)\)` then matched the '[' inside the
// escape, swallowing the escape sequence, the code span, and the link in
// one bogus match. The escape's body (e.g. "38;2;234;179;8m") became
// visible text. Tokenizing code spans before other inline regexes fixes
// this.

func TestInline_CodeAndLink_NoLeak(t *testing.T) {
	in := "Here's a paragraph with a `code span`, a [link](https://x.io), and some **bold**."
	out := renderInline(in)
	visible := stripANSI(out)
	want := "Here's a paragraph with a `code span`, a link, and some bold."
	if visible != want {
		t.Errorf("visible = %q\nwant = %q", visible, want)
	}
	// Belt-and-suspenders: ensure no SGR body bytes leaked as plain text.
	for _, leak := range []string{"38;2;234;179;8m", "38;2;6;182;212m", "[0m", "[1m"} {
		if strings.Contains(visible, leak) {
			t.Errorf("ANSI body leaked into visible text: %q in %q", leak, visible)
		}
	}
}

func TestInline_LinkBeforeCode(t *testing.T) {
	in := "see [docs](https://x.io) and call `foo()`"
	visible := stripANSI(renderInline(in))
	if visible != "see docs and call `foo()`" {
		t.Errorf("got %q", visible)
	}
}

func TestInline_MultipleCodeSpans(t *testing.T) {
	in := "use `a` then `b` then [link](u)"
	visible := stripANSI(renderInline(in))
	if visible != "use `a` then `b` then link" {
		t.Errorf("got %q", visible)
	}
}

func TestInline_CodeWithBracketsInside(t *testing.T) {
	// Bracket inside a code span must not start a link match.
	in := "call `arr[0]` and [docs](u)"
	visible := stripANSI(renderInline(in))
	if visible != "call `arr[0]` and docs" {
		t.Errorf("got %q", visible)
	}
}

func TestInline_BoldAroundCode(t *testing.T) {
	// Bold delimiters outside a code span should still apply.
	in := "**use `x` carefully**"
	visible := stripANSI(renderInline(in))
	if visible != "use `x` carefully" {
		t.Errorf("got %q", visible)
	}
}

// ── full Render() smoke ──────────────────────────────────────────────────────

func TestRender_HeaderListBlockquote(t *testing.T) {
	in := "# Title\n\n- item one\n- item two\n\n> quoted line\n"
	lines := Render(in, 80)
	joined := stripANSI(strings.Join(lines, "\n"))
	for _, want := range []string{"# Title", "• item one", "• item two", "│ quoted line"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestRender_FencedCode(t *testing.T) {
	in := "```go\nfunc x() {}\n```\n"
	lines := Render(in, 80)
	joined := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "func x() {}") {
		t.Errorf("code body missing:\n%s", joined)
	}
}

func TestRender_MixedContentLine(t *testing.T) {
	// The exact failure mode from the bug report.
	in := "Here's a paragraph with a `code span`, a [link](https://x.io), and some **emphasis**. Below is a nested list:"
	lines := Render(in, 80)
	joined := stripANSI(strings.Join(lines, "\n"))
	want := "Here's a paragraph with a `code span`, a link, and some emphasis. Below is a nested list:"
	if joined != want {
		t.Errorf("mixed content:\ngot  %q\nwant %q", joined, want)
	}
}
