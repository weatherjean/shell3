package tui

import (
	"strings"
	"testing"

	colorful "github.com/lucasb-eyer/go-colorful"
)

func TestAgentColorStableAndDistinct(t *testing.T) {
	if agentColor("code") != agentColor("code") {
		t.Fatal("agentColor must be deterministic for the same name")
	}
	// The two default agents must not collapse to the same badge color.
	if agentColor("code") == agentColor("plan") {
		t.Fatal("code and plan should map to distinct colors")
	}
}

func TestReadableOnPicksHigherContrast(t *testing.T) {
	white, _ := colorful.Hex("#FFFFFF")
	black, _ := colorful.Hex("#000000")
	if got := readableOn(white); got != cBlack {
		t.Fatalf("a light background should take black text, got %v", got)
	}
	if got := readableOn(black); got != cUser {
		t.Fatalf("a dark background should take light text, got %v", got)
	}
}

func TestAgentBadgeRendersNameWithColor(t *testing.T) {
	out := agentBadge("plan")
	if !strings.Contains(stripANSI(out), "plan") {
		t.Fatalf("badge should contain the agent name: %q", stripANSI(out))
	}
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("badge should carry ANSI color styling: %q", out)
	}
}
