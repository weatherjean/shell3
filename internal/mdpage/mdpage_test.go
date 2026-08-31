package mdpage

import (
	"strings"
	"testing"
)

func TestRenderProducesRealTables(t *testing.T) {
	out := string(Render("Report", "| theme | n |\n|---|---|\n| schema | 21 |\n"))
	for _, want := range []string{"<table>", "<th>theme</th>", "<td>21</td>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

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

func TestRenderDoesNotPassThroughRawHTML(t *testing.T) {
	out := string(Render("x", "Hello <script>alert(1)</script> there\n"))
	if strings.Contains(out, "<script>alert") {
		t.Fatalf("raw HTML survived:\n%s", out)
	}
}

func TestRenderEscapesTitle(t *testing.T) {
	out := string(Render(`</title><script>x`, "hi"))
	if strings.Contains(out, "<script>x") {
		t.Fatalf("title escaped the head:\n%s", out)
	}
}

func TestRenderNeverReturnsEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\x00\x01 weird"} {
		if out := Render("t", in); len(out) == 0 {
			t.Fatalf("empty page for %q", in)
		}
	}
}
