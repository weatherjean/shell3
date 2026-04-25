package personality_test

import (
	"testing"

	"github.com/weatherjean/shell3/internal/personality"
)

func TestCodePersonalityHasBash(t *testing.T) {
	p := personality.Build(personality.TypeCode, nil, false, false)
	names := toolNames(p.Tools)
	if !contains(names, "bash") {
		t.Errorf("code personality missing bash tool; got %v", names)
	}
}

func TestAgentPersonalityHasBash(t *testing.T) {
	p := personality.Build(personality.TypeAgent, nil, false, false)
	names := toolNames(p.Tools)
	if !contains(names, "bash") {
		t.Errorf("agent personality missing bash tool; got %v", names)
	}
}

func TestStoreToolsIncludedWhenStorePresent(t *testing.T) {
	p := personality.Build(personality.TypeCode, nil, true, false)
	names := toolNames(p.Tools)
	for _, want := range []string{"memory_store", "memory_list", "memory_search", "memory_remove", "history_latest", "history_search"} {
		if !contains(names, want) {
			t.Errorf("personality with store missing tool %q; got %v", want, names)
		}
	}
}

func TestStoreToolsAbsentWithoutStore(t *testing.T) {
	p := personality.Build(personality.TypeCode, nil, false, false)
	names := toolNames(p.Tools)
	for _, unwanted := range []string{"memory_store", "memory_list"} {
		if contains(names, unwanted) {
			t.Errorf("personality without store has unexpected tool %q", unwanted)
		}
	}
}

func TestCodePromptNotEmpty(t *testing.T) {
	p := personality.Build(personality.TypeCode, nil, false, false)
	if p.SystemPrompt == "" {
		t.Error("code personality has empty system prompt")
	}
}

func toolNames(tools []personality.ToolDef) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
