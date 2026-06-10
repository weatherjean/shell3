package luacfg

import "testing"

func TestToolDefs_SubagentsGate(t *testing.T) {
	on := ToolDefs(ToolGates{Subagents: true}, nil, false)
	var sawSpawn, sawList bool
	for _, d := range on {
		if d.Name == "spawn_agent" {
			sawSpawn = true
		}
		if d.Name == "list_agents" {
			sawList = true
		}
	}
	if !sawSpawn || !sawList {
		t.Fatalf("Subagents=true should expose spawn_agent and list_agents; spawn=%v list=%v", sawSpawn, sawList)
	}

	off := ToolDefs(ToolGates{}, nil, false)
	for _, d := range off {
		if d.Name == "spawn_agent" || d.Name == "list_agents" {
			t.Fatalf("Subagents=false must not expose %s", d.Name)
		}
	}
}

func TestSpawnAgentTool_Schema(t *testing.T) {
	defs := ToolDefs(ToolGates{Subagents: true}, nil, false)
	for _, d := range defs {
		if d.Name == "spawn_agent" {
			props, _ := d.Parameters["properties"].(map[string]any)
			if _, ok := props["task"]; !ok {
				t.Fatalf("spawn_agent must declare a 'task' param; params=%+v", d.Parameters)
			}
			if _, ok := props["agent"]; !ok {
				t.Fatalf("spawn_agent must declare an optional 'agent' param")
			}
			if _, ok := props["workdir"]; !ok {
				t.Fatalf("spawn_agent must declare an optional 'workdir' param")
			}
			req, _ := d.Parameters["required"].([]string)
			if len(req) != 1 || req[0] != "task" {
				t.Fatalf("spawn_agent required must be exactly [task]; got %v", req)
			}
		}
	}
}
