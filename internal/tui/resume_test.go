package tui

import (
	"strings"
	"testing"
)

func TestResumeBanner(t *testing.T) {
	got := resumeBanner("20060102T150405.000000042", 17)
	if !strings.Contains(got, "20060102T150405.000000042") || !strings.Contains(got, "17") {
		t.Fatalf("banner missing id/count: %q", got)
	}
}
