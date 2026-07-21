package cli

import (
	"strings"
	"testing"
)

// TestRenderMarkdown_RendersStructure verifies glamour output carries the
// source's content (headers, code, table cells) and never comes back empty.
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
