package cli

import (
	"strings"
	"testing"
)

// PrintHeader writes the two-line brand banner. It must contain the product
// name and the tagline, and end with a trailing blank line.
func TestPrintHeader(t *testing.T) {
	var b strings.Builder
	PrintHeader(&b)
	out := b.String()
	if !strings.Contains(out, "shell3") {
		t.Errorf("header missing product name; got %q", out)
	}
	if !strings.Contains(out, "minimal Unix-composable personal agent") {
		t.Errorf("header missing tagline; got %q", out)
	}
	if !strings.HasSuffix(out, "\n\n") {
		t.Errorf("header must end with a blank line; got %q", out)
	}
}
