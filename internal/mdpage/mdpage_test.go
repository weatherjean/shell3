package mdpage

import (
	"strings"
	"testing"
)

// The page must render the structure the CHAT cannot. A table is the case
// that motivated the package: internal/telegram/mdhtml runs goldmark without
// the table extension, so a comparison arrives in Telegram as literal pipes.
func TestRenderProducesRealTables(t *testing.T) {
	out := string(Render("Report", "| theme | n |\n|---|---|\n| schema | 21 |\n"))
	for _, want := range []string{"<table>", "<th>theme</th>", "<td>21</td>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

// Telegram opens the attachment in its own webview, offline and sandboxed. A
// page that reaches for a stylesheet, font or script renders unstyled or not
// at all, so everything has to be inline.
func TestRenderIsSelfContained(t *testing.T) {
	out := string(Render("Plan", "# Plan\n\nSome **text**.\n"))
	for _, bad := range []string{"<link", "<script", "http://", "https://", "@import"} {
		if strings.Contains(out, bad) {
			t.Fatalf("page reaches outside itself (%q):\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "prefers-color-scheme") {
		t.Fatal("no dark variant: a white page at night is how a document goes unread")
	}
}

// The markdown is model output. Embedded HTML must not survive into a page the
// user opens in a webview.
func TestRenderDoesNotPassThroughRawHTML(t *testing.T) {
	out := string(Render("x", "Hello <script>alert(1)</script> there\n"))
	if strings.Contains(out, "<script>alert") {
		t.Fatalf("raw HTML survived:\n%s", out)
	}
}

// The title is attacker-adjacent too — it comes from a caller that may pass a
// job name or user text.
func TestRenderEscapesTitle(t *testing.T) {
	out := string(Render(`</title><script>x`, "hi"))
	if strings.Contains(out, "<script>x") {
		t.Fatalf("title escaped the head:\n%s", out)
	}
}

// A reply is being delivered when this runs. Unparseable input must still
// produce a readable page rather than failing the send.
func TestRenderNeverReturnsEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\x00\x01 weird"} {
		if out := Render("t", in); len(out) == 0 {
			t.Fatalf("empty page for %q", in)
		}
	}
}
