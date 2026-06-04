package chat

import "testing"

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
