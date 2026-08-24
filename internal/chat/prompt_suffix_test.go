package chat

import (
	"fmt"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/runs"
)

// The suffix is a closure so a source edited mid-conversation lands on the
// NEXT turn. A cached string would freeze it at session creation — which is
// exactly the bug a per-room brief would hit the first time someone edited a
// group description.
func TestRenderSystemPromptCallsSuffixEveryTurn(t *testing.T) {
	n := 0
	cfg := TurnConfig{
		Personality:  persona.Persona{SystemPrompt: "base"},
		PromptSuffix: func() string { n++; return fmt.Sprintf("brief %d", n) },
	}
	if got := renderSystemPrompt(cfg); !strings.Contains(got, "brief 1") {
		t.Fatalf("first render = %q", got)
	}
	if got := renderSystemPrompt(cfg); !strings.Contains(got, "brief 2") {
		t.Fatalf("second render = %q, want the suffix re-evaluated", got)
	}
}

// The suffix appends to the refreshed prompt, never replaces it.
func TestRenderSystemPromptKeepsRefreshedPrompt(t *testing.T) {
	cfg := TurnConfig{
		Personality:   persona.Persona{SystemPrompt: "stale"},
		RefreshPrompt: func() string { return "fresh prompt" },
		PromptSuffix:  func() string { return "room brief" },
	}
	got := renderSystemPrompt(cfg)
	if !strings.Contains(got, "fresh prompt") || !strings.Contains(got, "room brief") {
		t.Fatalf("render = %q, want both the refreshed prompt and the suffix", got)
	}
	if strings.Contains(got, "stale") {
		t.Fatal("the refresher must still win over the construction-time prompt")
	}
}

// No suffix wired, or an empty one, changes nothing.
func TestRenderSystemPromptWithoutSuffix(t *testing.T) {
	base := TurnConfig{Personality: persona.Persona{SystemPrompt: "base"}}
	if got := renderSystemPrompt(base); got != "base" {
		t.Fatalf("render = %q, want the prompt untouched", got)
	}
	empty := base
	empty.PromptSuffix = func() string { return "   " }
	if got := renderSystemPrompt(empty); got != "base" {
		t.Fatalf("render = %q, want whitespace-only suffix ignored", got)
	}
}

// A turn must leave behind the prompt it ran with: the whole point is that a
// stored conversation records what the model was told, not only what it said.
func TestTurnRecordsItsSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	st, err := runs.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sess := &Session{id: "sess-1"}
	cfg := TurnConfig{}
	cfg.Store = st
	recordTurnPrompt(cfg, sess, "the prompt this turn ran with", 0)

	got := st.PromptsForSession("sess-1")
	if len(got) != 1 || got[0].Text != "the prompt this turn ran with" {
		t.Fatalf("stored prompts = %+v", got)
	}
}

// No store (library use, a test harness) must be a no-op, never a panic.
func TestTurnPromptRecordingIsOptional(t *testing.T) {
	recordTurnPrompt(TurnConfig{}, &Session{id: "s"}, "body", 0)
	recordTurnPrompt(TurnConfig{}, nil, "body", 0)
}
