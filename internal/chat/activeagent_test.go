package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/persona"
)

func TestApplyActiveAgentCopiesAllAgentFields(t *testing.T) {
	// Seed with agent-independent fields that must survive a switch unchanged.
	cfg := Config{
		WorkDir:    "/work",
		ConfigPath: "/cfg/shell3.lua",
		AgentNames: []string{"build", "plan"},
		Headless:   true,
	}

	rt := ActiveAgent{
		Personality:  persona.Persona{Name: "plan", SystemPrompt: "sp"},
		ModeLabel:    "plan",
		ActiveSkills: []string{"s1"},
		ActiveTools:  []string{"bash"},
		Params:       llm.RequestParams{ReasoningEffort: "high"},
		ModelID:      "gpt-x",
		AgentKnobs: AgentKnobs{
			HostToolNames: map[string]bool{"foo": true},
			ContextWindow: 128000,
		},
	}

	cfg.ApplyActiveAgent(rt)

	if cfg.ModeLabel != "plan" {
		t.Errorf("ModeLabel = %q, want plan", cfg.ModeLabel)
	}
	if cfg.Personality.Name != "plan" {
		t.Errorf("Personality not copied: %q", cfg.Personality.Name)
	}
	if cfg.Params.ReasoningEffort != "high" {
		t.Errorf("Params not copied: %+v", cfg.Params)
	}
	if cfg.ContextWindow != 128000 {
		t.Errorf("ContextWindow = %d, want 128000", cfg.ContextWindow)
	}
	if len(cfg.ActiveSkills) != 1 || cfg.ActiveSkills[0] != "s1" {
		t.Errorf("ActiveSkills not copied: %v", cfg.ActiveSkills)
	}
	if len(cfg.ActiveTools) != 1 || cfg.ActiveTools[0] != "bash" {
		t.Errorf("ActiveTools not copied: %v", cfg.ActiveTools)
	}
	if !cfg.HostToolNames["foo"] {
		t.Errorf("HostToolNames not copied: %v", cfg.HostToolNames)
	}
	if want := "plan │ gpt-x"; cfg.StatusLine != want {
		t.Errorf("StatusLine = %q, want %q", cfg.StatusLine, want)
	}

	// Agent-independent fields must be untouched by a switch.
	if cfg.WorkDir != "/work" || cfg.ConfigPath != "/cfg/shell3.lua" || !cfg.Headless {
		t.Errorf("switch clobbered agent-independent fields: %+v", cfg)
	}
	if len(cfg.AgentNames) != 2 {
		t.Errorf("AgentNames clobbered: %v", cfg.AgentNames)
	}
}

func TestConfigHasAgentSwitchingFields(t *testing.T) {
	cfg := Config{
		AgentNames: []string{"build", "plan"},
		SwitchAgent: func(name string) (ActiveAgent, error) {
			return ActiveAgent{ModeLabel: name, ModelID: "m"}, nil
		},
	}
	if len(cfg.AgentNames) != 2 {
		t.Fatalf("want 2 agent names, got %d", len(cfg.AgentNames))
	}
	rt, err := cfg.SwitchAgent("plan")
	if err != nil {
		t.Fatalf("SwitchAgent: %v", err)
	}
	if rt.ModeLabel != "plan" {
		t.Fatalf("want ModeLabel plan, got %q", rt.ModeLabel)
	}
}
