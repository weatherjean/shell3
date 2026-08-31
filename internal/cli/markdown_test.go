package cli

import (
	"strings"
	"testing"
)

func TestStyleFor(t *testing.T) {
	for _, tc := range []struct {
		tty, dark bool
		want      string
	}{
		{false, false, "notty"},
		{false, true, "notty"},
		{true, true, "dark"},
		{true, false, "light"},
	} {
		if got := styleFor(tc.tty, tc.dark); got != tc.want {
			t.Errorf("styleFor(tty=%v, dark=%v) = %q, want %q", tc.tty, tc.dark, got, tc.want)
		}
	}
}

func TestRenderMarkdown_RendersStructure(t *testing.T) {
	src := "# Title\n\nBody with `code`.\n\n| k | v |\n|---|---|\n| wiring | /tmp/x.yaml |\n"
	out := RenderMarkdown(src)
	if strings.TrimSpace(out) == "" {
		t.Fatal("rendered markdown is empty")
	}
	for _, want := range []string{"Title", "code", "wiring", "/tmp/x.yaml"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q\n%s", want, out)
		}
	}
}
