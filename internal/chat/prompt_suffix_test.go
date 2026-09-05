package chat

import (
	"fmt"
	"strings"
	"testing"
)

func TestRenderSystemPromptCallsSuffixEveryTurn(t *testing.T) {
	n := 0
	cfg := TurnConfig{
		Profile:      AgentProfile{SystemPrompt: "base"},
		PromptSuffix: func() string { n++; return fmt.Sprintf("brief %d", n) },
	}
	if got := renderSystemPrompt(cfg); !strings.Contains(got, "brief 1") {
		t.Fatalf("first render = %q", got)
	}
	if got := renderSystemPrompt(cfg); !strings.Contains(got, "brief 2") {
		t.Fatalf("second render = %q, want the suffix re-evaluated", got)
	}
}

func TestRenderSystemPromptWithoutSuffix(t *testing.T) {
	base := TurnConfig{Profile: AgentProfile{SystemPrompt: "base"}}
	if got := renderSystemPrompt(base); got != "base" {
		t.Fatalf("render = %q, want the prompt untouched", got)
	}
	empty := base
	empty.PromptSuffix = func() string { return "   " }
	if got := renderSystemPrompt(empty); got != "base" {
		t.Fatalf("render = %q, want whitespace-only suffix ignored", got)
	}
}
