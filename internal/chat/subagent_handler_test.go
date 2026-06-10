package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

func TestRunTurn_SpawnAgent_InvokesSpawnAndReturnsID(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{ToolCall: &llm.ToolCall{ID: "c", Name: "spawn_agent", RawArgs: `{"task":"check the logs","subagent":"researcher","workdir":"/tmp/x"}`}}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "spawned"}}},
	)
	sess, c := newCollectorSession(SessionOpts{})
	var got SpawnRequest
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "t", Tools: []llm.ToolDefinition{{Name: "spawn_agent"}}},
		Log:         LogOrNoop(nil),
		Subagents:   []string{"researcher"},
		Spawn: func(_ context.Context, req SpawnRequest) (string, error) {
			got = req
			return "a1b2", nil
		},
	}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "go"}, nil)
	if got.Task != "check the logs" || got.Subagent != "researcher" || got.WorkDir != "/tmp/x" {
		t.Fatalf("Spawn got %+v, want task/subagent/workdir from args", got)
	}
	var sawResult bool
	for _, ev := range c.all() {
		if ev.Kind == EventToolResult && strings.Contains(ev.ToolOutput, "a1b2") {
			sawResult = true
		}
	}
	if !sawResult {
		t.Fatalf("spawn_agent tool result should carry the spawned id; events=%+v", c.all())
	}
}

func TestRunTurn_SpawnAgent_MissingSubagentReturnsError(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{ToolCall: &llm.ToolCall{ID: "c", Name: "spawn_agent", RawArgs: `{"task":"do something"}`}}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}},
	)
	sess, c := newCollectorSession(SessionOpts{})
	spawnCalled := false
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "t", Tools: []llm.ToolDefinition{{Name: "spawn_agent"}}},
		Log:         LogOrNoop(nil),
		Spawn: func(_ context.Context, _ SpawnRequest) (string, error) {
			spawnCalled = true
			return "should-not-reach", nil
		},
	}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "go"}, nil)
	if spawnCalled {
		t.Fatal("Spawn must NOT be called when subagent is missing")
	}
	var sawErr bool
	for _, ev := range c.all() {
		if ev.Kind == EventToolResult && strings.Contains(ev.ToolOutput, "requires a subagent") {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatalf("spawn_agent with missing subagent should return 'requires a subagent' error; events=%+v", c.all())
	}
}

func TestRunTurn_SpawnAgent_NoSpawnerDegrades(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{ToolCall: &llm.ToolCall{ID: "c", Name: "spawn_agent", RawArgs: `{"task":"x"}`}}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}},
	)
	sess, c := newCollectorSession(SessionOpts{})
	cfg := TurnConfig{LLM: fake, Personality: persona.Persona{SystemPrompt: "t", Tools: []llm.ToolDefinition{{Name: "spawn_agent"}}}, Log: LogOrNoop(nil)}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "go"}, nil)
	var sawErr bool
	for _, ev := range c.all() {
		if ev.Kind == EventToolResult && strings.Contains(strings.ToLower(ev.ToolOutput), "not available") {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatalf("spawn_agent with no spawner should return an 'unavailable' result; events=%+v", c.all())
	}
}

func TestRunTurn_SpawnAgent_RejectsUnlistedSubagent(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{ToolCall: &llm.ToolCall{ID: "c", Name: "spawn_agent", RawArgs: `{"task":"t","subagent":"ghost"}`}}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}},
	)
	sess, c := newCollectorSession(SessionOpts{})
	spawnCalled := false
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "t", Tools: []llm.ToolDefinition{{Name: "spawn_agent"}}},
		Log:         LogOrNoop(nil),
		Subagents:   []string{"researcher"},
		Spawn: func(_ context.Context, _ SpawnRequest) (string, error) {
			spawnCalled = true
			return "should-not-reach", nil
		},
	}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "go"}, nil)
	if spawnCalled {
		t.Fatal("Spawn must NOT be called when subagent is not in the allowlist")
	}
	var sawErr bool
	for _, ev := range c.all() {
		if ev.Kind == EventToolResult && strings.Contains(ev.ToolOutput, "not available to this agent") {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatalf("spawn_agent with unlisted subagent should return 'not available to this agent' error; events=%+v", c.all())
	}
}

func TestRunTurn_ListAgents_ReturnsSnapshot(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{ToolCall: &llm.ToolCall{ID: "c", Name: "list_agents", RawArgs: `{}`}}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "done"}}},
	)
	sess, c := newCollectorSession(SessionOpts{})
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "t", Tools: []llm.ToolDefinition{{Name: "list_agents"}}},
		Log:         LogOrNoop(nil),
		ListAgents: func() []AgentSnapshot {
			return []AgentSnapshot{{ID: "a1", Agent: "code", Task: "check logs", Status: "running"}}
		},
	}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "go"}, nil)
	var resultText string
	for _, ev := range c.all() {
		if ev.Kind == EventToolResult {
			resultText = ev.ToolOutput
		}
	}
	var snap []AgentSnapshot
	if err := json.Unmarshal([]byte(resultText), &snap); err != nil {
		t.Fatalf("list_agents result should be JSON array of snapshots; got %q err=%v", resultText, err)
	}
	if len(snap) != 1 || snap[0].ID != "a1" || snap[0].Status != "running" {
		t.Fatalf("snapshot round-trip wrong: %+v", snap)
	}
}
