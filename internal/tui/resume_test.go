package tui

import (
	"strings"
	"testing"
)

func TestResumeBanner(t *testing.T) {
	got := resumeBanner(42, 17)
	if !strings.Contains(got, "42") || !strings.Contains(got, "17") {
		t.Fatalf("banner missing id/count: %q", got)
	}
}
