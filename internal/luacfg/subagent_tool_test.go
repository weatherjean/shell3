package luacfg

import (
	"strings"
	"testing"
)

func TestSpawnToolDefs_EnumAndDescription(t *testing.T) {
	infos := []SubagentInfo{
		{"researcher", "investigate the repo"},
		{"planner", "make a plan"},
	}
	defs := SpawnToolDefs(infos)
	if len(defs) != 2 {
		t.Fatalf("SpawnToolDefs: want 2 defs, got %d", len(defs))
	}
	var spawnDef, listDef *struct {
		Name, Description string
		Parameters        map[string]any
	}
	for i := range defs {
		d := &struct {
			Name, Description string
			Parameters        map[string]any
		}{
			Name:        defs[i].Name,
			Description: defs[i].Description,
			Parameters:  defs[i].Parameters,
		}
		switch d.Name {
		case "spawn_agent":
			spawnDef = d
		case "list_agents":
			listDef = d
		}
	}
	if spawnDef == nil {
		t.Fatal("missing spawn_agent def")
	}
	if listDef == nil {
		t.Fatal("missing list_agents def")
	}

	// Check enum in subagent param
	props, ok := spawnDef.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("spawn_agent properties not map[string]any: %T", spawnDef.Parameters["properties"])
	}
	subagentProp, ok := props["subagent"].(map[string]any)
	if !ok {
		t.Fatalf("spawn_agent subagent prop not map[string]any: %T", props["subagent"])
	}
	enum, ok := subagentProp["enum"].([]string)
	if !ok {
		t.Fatalf("spawn_agent subagent enum not []string: %T", subagentProp["enum"])
	}
	if len(enum) != 2 || enum[0] != "researcher" || enum[1] != "planner" {
		t.Fatalf("spawn_agent subagent enum = %v, want [researcher planner]", enum)
	}

	// Check description mentions both subagents and their descriptions
	for _, want := range []string{"researcher", "investigate the repo", "planner"} {
		if !strings.Contains(spawnDef.Description, want) {
			t.Fatalf("spawn_agent description should contain %q; got: %s", want, spawnDef.Description)
		}
	}

	// Check required contains both "task" and "subagent"
	req, ok := spawnDef.Parameters["required"].([]string)
	if !ok {
		t.Fatalf("spawn_agent required not []string: %T", spawnDef.Parameters["required"])
	}
	hasTask, hasSubagent := false, false
	for _, r := range req {
		if r == "task" {
			hasTask = true
		}
		if r == "subagent" {
			hasSubagent = true
		}
	}
	if !hasTask || !hasSubagent {
		t.Fatalf("spawn_agent required must contain task and subagent; got %v", req)
	}
}

func TestSpawnToolDefs_EmptyIsNoTools(t *testing.T) {
	defs := SpawnToolDefs(nil)
	if len(defs) != 0 {
		t.Fatalf("SpawnToolDefs(nil): want 0 defs, got %d", len(defs))
	}
}
