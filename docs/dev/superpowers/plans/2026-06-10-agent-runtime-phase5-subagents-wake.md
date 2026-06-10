# Agent Runtime Phase 5: `spawn_agent`/`list_agents` + `Wake` bus — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A turn can spawn an in-process subagent (`spawn_agent`) on the shared `Runtime`; the subagent runs headless on its own session/goroutine and its result lands in the **parent's inbox** (injected mid-turn if the parent is still running, delivered as a `Wake` event if idle). `list_agents()` returns a snapshot of running/finished subagents. A new `Runtime.Events() <-chan HostEvent` carries out-of-turn `Wake` events so a single host select-loop can drive N sessions.

**Architecture:**
- `internal/chat` stays decoupled from `pkg/shell3.Runtime` (it cannot import the package that imports it). The turn loop reaches spawning through **two closures on `TurnConfig`** — `Spawn(ctx, SpawnRequest) (string, error)` and `ListAgents() []AgentSnapshot` — wired by `pkg/shell3.Session.turnConfig`. Two new turn-scoped tool handlers (`spawn_agent`, `list_agents`) call them, mirroring the existing `compact_history`/`read_media` pattern.
- The actual spawning lives in `pkg/shell3`: a `Session` captures its `*Runtime`, creates a `sub:<id>` session via `rt.Session(...)` headless rooted at the requested workdir, runs it on a goroutine, and on completion posts a `subagent finished: <result>` item to the **parent session's** inbox. Depth is limited to 1 by stripping the spawn tools from the subagent's schema.
- The `Wake` bus is entirely in `pkg/shell3`: a buffered `chan HostEvent` on `Runtime`. A session emits `Wake` when an idle session's inbox gains an item (idle `Interject`, or a subagent finishing while its parent is idle). `Session.RunQueued(ctx)` is the host's entry point to run a turn seeded from the queued inbox items.
- Each subagent writes its own audit JSONL under `<runtime-root>/.shell3/agents/<id>.jsonl`.

**Tech Stack:** Go (this repo, branch `agent-runtime`). Tests: standard `testing` + `internal/llm/fakellm`, race-enabled, hermetic (temp HOME). Spec: `docs/dev/superpowers/specs/2026-06-10-agent-runtime-design.md` (sections "Inbox, Interject, Wake" and "Subagents as built-in tools").

**Conventions:**
- TDD: failing test first, then minimal implementation, then green.
- Verify every task with `go test -race -count=1 ./...` from the repo root before committing.
- Never read `.env` (beside any `shell3.lua`) or `ai-do-not-read.*` files.
- Commit bodies end with `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.

---

## File Structure

| File | Responsibility | Task |
|------|----------------|------|
| `internal/luacfg/register.go` | `ToolGates.Subagents` gate + `subagents` key | 1 |
| `internal/luacfg/luacfg.go` | `ToolGates` struct field | 1 |
| `internal/luacfg/tooldefs.go` | `spawnAgentTool` / `listAgentsTool` defs + gating | 1 |
| `internal/chat/toolhandler.go` | `SpawnRequest`, `AgentSnapshot`, `TurnConfig.Spawn`/`.ListAgents`; `NewTurnConfig` pass-through | 2 |
| `internal/chat/turn.go` | `spawn_agent`/`list_agents` turn-scoped handlers | 2 |
| `internal/agentsetup/agentsetup.go` | `SessionOptions.DisableSubagents` → strip subagent gate | 3 |
| `pkg/shell3/runtime.go` | `HostEvent`, `Runtime.Events()`, `emit`, `SessionOpts.DisableSubagents` plumbing | 3, 4 |
| `pkg/shell3/subagents.go` (new) | subagent registry, spawn goroutine, `list`, completion → parent inbox | 3 |
| `pkg/shell3/shell3.go` | `Session.turnConfig` wires `Spawn`/`ListAgents`; `Session.RunQueued`; idle-`Interject` Wake | 3, 4 |
| `internal/tui/interactive.go` | consume `Events()`, auto-run wake turn, render subagent notice | 5 |
| `CHANGELOG.md` | release note | 6 |

---

## Task 1: luacfg — `spawn_agent`/`list_agents` tool defs + `subagents` gate

**Files:**
- Modify: `internal/luacfg/luacfg.go:26-28` (add field to `ToolGates`)
- Modify: `internal/luacfg/register.go:90` (`toolGateKeys`) and `:178-187` (gate population)
- Modify: `internal/luacfg/tooldefs.go` (tool defs + gating in `ToolDefs`)
- Test: `internal/luacfg/subagent_tool_test.go` (new)

- [ ] **Step 1: Write the failing test** (`internal/luacfg/subagent_tool_test.go`)

```go
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
	var spawn *llmToolDef
	_ = spawn
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
```

> Note: delete the `llmToolDef` placeholder line — it exists only to remind you `ToolDefinition` is `llm.ToolDefinition`; the loop uses `d` directly. (Remove `var spawn *llmToolDef; _ = spawn` before running.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/luacfg -run 'Subagents|SpawnAgentTool' -v`
Expected: FAIL — `ToolGates has no field Subagents` / `spawn_agent` not found.

- [ ] **Step 3: Add the gate field** (`internal/luacfg/luacfg.go:27`)

```go
type ToolGates struct {
	Bash, BashBg, ShellInteractive, Edit, History, Prune, Compact, Media, Subagents bool
}
```

- [ ] **Step 4: Register the lua key + populate the gate** (`internal/luacfg/register.go`)

Add `"subagents": true,` to the `toolGateKeys` map at line 90 (match the existing entries' formatting). Then in `luaAgent` at the `a.Gates = ToolGates{...}` block (line 178), add the field:

```go
		a.Gates = ToolGates{
			Bash:             optBool(tt, "bash"),
			BashBg:           optBool(tt, "bash_bg"),
			ShellInteractive: optBool(tt, "shell_interactive"),
			Edit:             optBool(tt, "edit"),
			History:          optBool(tt, "history"),
			Prune:            optBool(tt, "prune"),
			Compact:          optBool(tt, "compact"),
			Media:            optBool(tt, "media"),
			Subagents:        optBool(tt, "subagents"),
		}
```

- [ ] **Step 5: Add the tool definitions** (`internal/luacfg/tooldefs.go`)

Add two package-level vars (place them after `readMediaTool` to keep related defs together):

```go
var spawnAgentTool = llm.ToolDefinition{
	Name: "spawn_agent",
	Description: "Spawn a subagent to work a focused, independent subtask in the background. " +
		"Returns an id immediately; the subagent runs on its own with a fresh context and reports back automatically — " +
		"its result is delivered to you as a system message when it finishes (mid-turn if you are still working, otherwise on your next turn). " +
		"Use for parallelizable work (e.g. investigate a file while you keep going). Do NOT poll in a tight loop; the result arrives on its own. " +
		"Subagents cannot themselves spawn subagents.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task":    map[string]any{"type": "string", "description": "The full task prompt for the subagent. Be self-contained: the subagent does not see this conversation."},
			"agent":   map[string]any{"type": "string", "description": "Name of a configured agent to run as. Omit to use your own agent."},
			"workdir": map[string]any{"type": "string", "description": "Working directory to root the subagent in (absolute, or relative to your workdir). Omit to use your workdir."},
		},
		"required": []string{"task"},
	},
}

var listAgentsTool = llm.ToolDefinition{
	Name: "list_agents",
	Description: "List the subagents you have spawned this session, with their id, status (running or finished), the task they were given, and (when finished) a short result preview. Use to check progress; results still arrive on their own.",
	Parameters: map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	},
}
```

Then gate them in `ToolDefs` (after the `g.Media` block, before the custom-tool loop):

```go
	if g.Subagents {
		defs = append(defs, spawnAgentTool, listAgentsTool)
	}
```

- [ ] **Step 6: Run the tests** — `go test ./internal/luacfg -run 'Subagents|SpawnAgentTool' -v` → PASS. Then `go test -race -count=1 ./internal/luacfg` → PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/luacfg && git commit -m "feat(luacfg): spawn_agent/list_agents tool defs gated by tools.subagents

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 2: internal/chat — `Spawn`/`ListAgents` on TurnConfig + turn-scoped handlers

The turn loop must reach the (package-external) spawner without importing `pkg/shell3`. We add two closures and two value types to `internal/chat`, then two turn-scoped handlers that call them. Handlers degrade gracefully when the closures are nil (e.g. a single-session embedder that never wired a runtime).

**Files:**
- Modify: `internal/chat/toolhandler.go` (`SpawnRequest`, `AgentSnapshot`, `TurnConfig` fields, `NewTurnConfig`)
- Modify: `internal/chat/turn.go:292-313` (`turnScopedHandlers`)
- Test: `internal/chat/subagent_handler_test.go` (new)

- [ ] **Step 1: Write the failing test** (`internal/chat/subagent_handler_test.go`)

```go
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

// A turn whose model calls spawn_agent should invoke cfg.Spawn with the parsed
// args and return the spawned id as the tool result.
func TestRunTurn_SpawnAgent_InvokesSpawnAndReturnsID(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{ToolCall: &llm.ToolCall{ID: "c", Name: "spawn_agent", RawArgs: `{"task":"check the logs","agent":"code","workdir":"/tmp/x"}`}}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "spawned"}}},
	)
	sess, c := newCollectorSession(SessionOpts{})

	var got SpawnRequest
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "t", Tools: []llm.ToolDefinition{{Name: "spawn_agent"}}},
		Log:         LogOrNoop(nil),
		Spawn: func(_ context.Context, req SpawnRequest) (string, error) {
			got = req
			return "a1b2", nil
		},
	}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "go"}, nil)

	if got.Task != "check the logs" || got.Agent != "code" || got.WorkDir != "/tmp/x" {
		t.Fatalf("Spawn got %+v, want task/agent/workdir from args", got)
	}
	var sawResult bool
	for _, ev := range c.all() {
		if ev.Kind == EventToolResult && strings.Contains(ev.Text, "a1b2") {
			sawResult = true
		}
	}
	if !sawResult {
		t.Fatalf("spawn_agent tool result should carry the spawned id; events=%+v", c.all())
	}
}

// With no Spawn wired, spawn_agent returns an explanatory error string (no panic).
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
		if ev.Kind == EventToolResult && strings.Contains(strings.ToLower(ev.Text), "not available") {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatalf("spawn_agent with no spawner should return an 'unavailable' result; events=%+v", c.all())
	}
}

// list_agents serializes the snapshot returned by cfg.ListAgents.
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
			resultText = ev.Text
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
```

> Check the real event-kind constant names against `internal/chat/event.go` (`EventToolResult`) and the collector's `Text` field before running; adjust if the codebase names them differently. The phase-2/3 tests in `internal/chat/inbox_test.go` are the reference for `newCollectorSession`/`fakellm` usage.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/chat -run 'SpawnAgent|ListAgents' -v`
Expected: FAIL — `TurnConfig has no field Spawn` / `undefined: SpawnRequest`.

- [ ] **Step 3: Add the types and TurnConfig fields** (`internal/chat/toolhandler.go`)

Add near the other public tool types:

```go
// SpawnRequest is a parsed spawn_agent call handed to the host's spawner.
type SpawnRequest struct {
	Task    string
	Agent   string // "" → caller's agent
	WorkDir string // "" → caller's workdir
}

// AgentSnapshot is one row of a list_agents result.
type AgentSnapshot struct {
	ID     string `json:"id"`
	Agent  string `json:"agent"`
	Task   string `json:"task"`
	Status string `json:"status"`           // "running" | "finished"
	Result string `json:"result,omitempty"` // short preview when finished
}
```

In the `TurnConfig` struct (after `Approve`):

```go
	// Spawn launches a subagent for the parsed spawn_agent call and returns its
	// id immediately. Nil → spawn_agent degrades to an "unavailable" result.
	Spawn func(ctx context.Context, req SpawnRequest) (string, error)
	// ListAgents returns a snapshot of subagents spawned by this session. Nil →
	// list_agents returns an empty array.
	ListAgents func() []AgentSnapshot
```

If `NewTurnConfig` builds `TurnConfig` from `chat.Config` (check its body), copy `cfg.Spawn`/`cfg.ListAgents` through — i.e. add matching `Spawn`/`ListAgents` fields to `chat.Config` and assign them in `NewTurnConfig`. (Grep `func NewTurnConfig` and `type Config struct` to confirm the field list to extend; mirror how `Approve`/`ShellInteractive` flow through.)

- [ ] **Step 4: Add the handlers** (`internal/chat/turn.go`, inside `turnScopedHandlers`'s returned map)

```go
		"spawn_agent": funcHandler{name: "spawn_agent", fn: func(ctx context.Context, _ string, args json.RawMessage, _ ToolConfig) (string, error) {
			if cfg.Spawn == nil {
				return "error: subagent spawning is not available in this runtime", nil
			}
			var a struct {
				Task    string `json:"task"`
				Agent   string `json:"agent"`
				Workdir string `json:"workdir"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "error: invalid spawn_agent arguments: " + err.Error(), nil
			}
			if strings.TrimSpace(a.Task) == "" {
				return "error: spawn_agent requires a non-empty task", nil
			}
			id, err := cfg.Spawn(ctx, SpawnRequest{Task: a.Task, Agent: a.Agent, WorkDir: a.Workdir})
			if err != nil {
				return "error: spawn failed: " + err.Error(), nil
			}
			return "spawned subagent " + id + "; its result will arrive automatically when it finishes. Do not poll in a tight loop.", nil
		}},
		"list_agents": funcHandler{name: "list_agents", fn: func(_ context.Context, _ string, _ json.RawMessage, _ ToolConfig) (string, error) {
			var snap []AgentSnapshot
			if cfg.ListAgents != nil {
				snap = cfg.ListAgents()
			}
			if snap == nil {
				snap = []AgentSnapshot{}
			}
			b, err := json.Marshal(snap)
			if err != nil {
				return "error: " + err.Error(), nil
			}
			return string(b), nil
		}},
```

Confirm `turn.go` already imports `strings` and `encoding/json` (it uses both elsewhere).

- [ ] **Step 5: Run the tests** — `go test ./internal/chat -run 'SpawnAgent|ListAgents' -v` → PASS, then `go test -race -count=1 ./internal/chat` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chat && git commit -m "feat(chat): TurnConfig.Spawn/ListAgents + spawn_agent/list_agents handlers

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 3: pkg/shell3 — subagent registry, spawning, completion → parent inbox

This is the core. A `Session` already holds `runtime *Runtime` and `name string`. We wire `Spawn`/`ListAgents` closures in `turnConfig`, back them with a per-session registry, run each subagent on a goroutine, and on completion post its result to the parent's inbox. Depth limit: the runtime spawns the subagent with `DisableSubagents: true`, which strips the spawn tools from its schema.

**Files:**
- Modify: `internal/agentsetup/agentsetup.go` (`SessionOptions.DisableSubagents`; strip gate in `SessionConfig`)
- Modify: `pkg/shell3/runtime.go` (`SessionOpts.DisableSubagents` → through to `agentsetup.SessionOptions`)
- Create: `pkg/shell3/subagents.go` (registry + spawn + list + completion)
- Modify: `pkg/shell3/shell3.go` (`Session.turnConfig` wires `Spawn`/`ListAgents`; `Session` gets a `subagents` registry field)
- Test: `pkg/shell3/subagents_test.go` (new)

- [ ] **Step 1: Add the depth-limit strip in agentsetup** (`internal/agentsetup/agentsetup.go`)

Add to `SessionOptions` (line 193):

```go
	// DisableSubagents force-strips spawn_agent/list_agents from this session's
	// schema regardless of the agent's tools.subagents gate. The runtime sets it
	// for spawned subagents to enforce depth-limit 1.
	DisableSubagents bool
```

`SessionConfig` calls `p.AgentRuntime(so.Agent)` which builds the persona (with `toolDefs` already gated). The cleanest strip point is right after that call, filtering the persona's `Tools` and `ActiveTools`. Add a helper and apply it:

```go
// stripSubagentTools removes spawn_agent/list_agents from an agent's schema,
// enforcing the depth-limit-1 rule for spawned subagents.
func stripSubagentTools(a chat.ActiveAgent) chat.ActiveAgent {
	keep := a.Personality.Tools[:0:0]
	for _, t := range a.Personality.Tools {
		if t.Name == "spawn_agent" || t.Name == "list_agents" {
			continue
		}
		keep = append(keep, t)
	}
	a.Personality.Tools = keep
	names := a.ActiveTools[:0:0]
	for _, n := range a.ActiveTools {
		if n == "spawn_agent" || n == "list_agents" {
			continue
		}
		names = append(names, n)
	}
	a.ActiveTools = names
	return a
}
```

In `SessionConfig`, after `rt, err := p.AgentRuntime(so.Agent)` (line 208) and its error check:

```go
	if so.DisableSubagents {
		rt = stripSubagentTools(rt)
	}
```

> Verify `AgentRuntime` returns `chat.ActiveAgent` by value (the report shows it returns a value); if it returns a pointer, adapt the helper signature. Use a fresh backing array (`[:0:0]`) so the strip never aliases the shared persona slice from the config cache.

- [ ] **Step 2: Thread `DisableSubagents` through `RuntimeSpec`→`SessionConfig`** (`pkg/shell3/runtime.go`)

Add to `SessionOpts` (line 21):

```go
	// DisableSubagents strips the spawn tools from this session (used for
	// spawned subagents; depth limit 1).
	DisableSubagents bool
```

In `NewRuntime`'s `sessionConfig` closure (line 79), pass it through:

```go
		sessionConfig: func(o SessionOpts) (chat.Config, error) {
			return parts.SessionConfig(agentsetup.SessionOptions{
				Agent: o.Agent, WorkDir: o.WorkDir, Headless: o.Headless, OutPath: o.OutPath,
				DisableSubagents: o.DisableSubagents,
			})
		},
```

- [ ] **Step 3: Write the failing test** (`pkg/shell3/subagents_test.go`)

These tests use the fake `sessionConfig` injection that the existing `pkg/shell3/runtime_test.go` uses. **Read `runtime_test.go` first** to copy its exact fake-runtime construction (how it builds a `*Runtime` with a stub `sessionConfig` returning a `chat.Config` wired to a `fakellm` client). The skeleton below assumes a helper `newFakeRuntime(t, script...)`; if `runtime_test.go` names it differently, reuse that.

```go
package shell3

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

// spawn_agent on a parent turn runs a sub:<id> session and posts its result to
// the parent's inbox; an idle parent gets a Wake on the runtime bus.
func TestSpawn_SubagentResultWakesIdleParent(t *testing.T) {
	rt := newFakeRuntime(t, map[string]fakellm.Provider{
		// parent: one tool call to spawn, then end
		"parent": fakellm.New(
			fakellm.Script{Events: []llm.StreamEvent{{ToolCall: &llm.ToolCall{ID: "c", Name: "spawn_agent", RawArgs: `{"task":"do the thing"}`}}}},
			fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "kicked off"}}},
		),
		// subagent: just answers
		"sub": fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "subagent result here"}}}),
	})
	defer rt.Close()

	parent, err := rt.Session(SessionOpts{Name: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	drain(parent.Send(context.Background(), "go"))

	// The parent turn ended; the subagent finishing while idle must emit a Wake.
	select {
	case ev := <-rt.Events():
		if ev.Kind != Wake || ev.Session != "parent" {
			t.Fatalf("want Wake for parent, got %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no Wake event after subagent finished")
	}

	// RunQueued drains the inbox: the subagent result is the seed of the wake turn.
	parent.cfg.Personality = parent.cfg.Personality // no-op; ensures cfg accessible
	// The model for the wake turn must be scripted; see newFakeRuntime contract.
}

// Subagents cannot spawn (depth limit 1): the sub session is created with
// DisableSubagents, so its schema omits spawn_agent.
func TestSpawn_DepthLimitStripsToolsFromSubagent(t *testing.T) {
	// Assert via the recorded SessionOpts the runtime used to create "sub:*".
	// newFakeRuntime records every SessionOpts it was asked to build.
	rt := newFakeRuntime(t, nil)
	defer rt.Close()
	parent, _ := rt.Session(SessionOpts{Name: "parent"})
	id, err := parent.spawn(context.Background(), chat.SpawnRequest{Task: "x"})
	if err != nil {
		t.Fatal(err)
	}
	opts := rt.recordedOpts("sub:" + id)
	if !opts.DisableSubagents {
		t.Fatalf("sub session must be created with DisableSubagents=true; got %+v", opts)
	}
}

// list_agents reflects running then finished state.
func TestListAgents_Snapshot(t *testing.T) {
	// ... drive a spawn, assert parent.listAgents() shows status running, then
	// after the subagent goroutine joins, status finished with a result preview.
}

func drain(ch <-chan Event) {
	for range ch {
	}
}
```

> The exact fake-runtime harness is the part to get right; model it on `runtime_test.go`. The three behaviors to pin are: (a) spawn creates a `sub:<id>` session and runs it; (b) completion posts to the **parent** inbox and Wakes an idle parent; (c) the sub session is created with `DisableSubagents`. Fill in `TestListAgents_Snapshot` and the wake-turn assertion in `TestSpawn_...IdleParent` once the harness shape is settled. Keep all timing assertions behind a generous `time.After` to stay non-flaky under `-race`.

- [ ] **Step 4: Run to verify failure**

Run: `go test ./pkg/shell3 -run 'Spawn|ListAgents' -v`
Expected: FAIL — `parent.spawn undefined`, `rt.Events undefined`, etc.

- [ ] **Step 5: Implement the registry + spawn** (`pkg/shell3/subagents.go`)

```go
package shell3

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/weatherjean/shell3/internal/chat"
)

// subagent tracks one spawned child of a session.
type subagent struct {
	id     string
	agent  string
	task   string
	status string // "running" | "finished"
	result string
}

// subRegistry holds a session's spawned subagents. Guarded by its own mutex so
// list_agents (turn goroutine) and completion (subagent goroutine) don't race.
type subRegistry struct {
	mu   sync.Mutex
	subs []*subagent
	seq  int
}

func (r *subRegistry) add(agent, task string) *subagent {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	// Deterministic, race-free id: sequence-based, no global RNG (matches the
	// runtime's "s%d" session-naming style and keeps tests stable).
	sa := &subagent{id: fmt.Sprintf("%x%d", len(r.subs)+1, r.seq), agent: agent, task: task, status: "running"}
	r.subs = append(r.subs, sa)
	return sa
}

func (r *subRegistry) finish(sa *subagent, result string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sa.status = "finished"
	sa.result = result
}

func (r *subRegistry) snapshot() []chat.AgentSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]chat.AgentSnapshot, 0, len(r.subs))
	for _, sa := range r.subs {
		preview := sa.result
		if len(preview) > 200 {
			preview = preview[:200] + "…"
		}
		out = append(out, chat.AgentSnapshot{ID: sa.id, Agent: sa.agent, Task: sa.task, Status: sa.status, Result: preview})
	}
	return out
}

// spawn creates a headless sub:<id> session on the parent's runtime, runs the
// task on a goroutine, and posts the result to the parent's inbox on
// completion. Returns the id immediately. Depth-limited: the sub session is
// created with DisableSubagents.
func (s *Session) spawn(ctx context.Context, req chat.SpawnRequest) (string, error) {
	if s.runtime == nil {
		return "", fmt.Errorf("shell3: session has no runtime; cannot spawn subagents")
	}
	agent := req.Agent
	if agent == "" {
		agent = s.cfg.Personality.Name
	}
	workdir := req.WorkDir
	if workdir == "" {
		workdir = s.cfg.WorkDir
	} else if !filepath.IsAbs(workdir) {
		workdir = filepath.Join(s.cfg.WorkDir, workdir)
	}
	sa := s.subs.add(agent, req.Task)
	auditPath := filepath.Join(s.runtime.root(), ".shell3", "agents", sa.id+".jsonl")
	if err := ensureDir(filepath.Dir(auditPath)); err != nil {
		return "", err
	}
	child, err := s.runtime.Session(SessionOpts{
		Name: "sub:" + sa.id, Agent: agent, WorkDir: workdir,
		Headless: true, OutPath: auditPath, DisableSubagents: true,
	})
	if err != nil {
		return "", err
	}
	// Run on a fresh context (NOT the parent turn's ctx — the subagent must
	// outlive the spawning turn). Tie its lifetime to the runtime instead.
	go func() {
		runCtx := s.runtime.baseContext()
		var b strings.Builder
		for ev := range child.Send(runCtx, req.Task) {
			if ev.Kind == AssistantMessage {
				b.WriteString(ev.Text)
			}
		}
		result := strings.TrimSpace(b.String())
		s.subs.finish(sa, result)
		_ = child.Close()
		s.deliverSubagentResult(sa.id, result)
	}()
	return sa.id, nil
}

// deliverSubagentResult posts a finished subagent's result to the parent inbox,
// then Wakes the parent if it is idle (so the host runs a turn to react).
func (s *Session) deliverSubagentResult(id, result string) {
	msg := fmt.Sprintf("subagent %s finished: %s", id, result)
	s.sess.Interject(msg)
	if !s.isBusy() {
		s.wake()
	}
}
```

You must also add, in `runtime.go`:
- `root() string` (returns the runtime's workdir root — store it on `Runtime` at `NewRuntime`; `agentsetup.Parts` already has `p.root`, so capture `workDir` into a `Runtime.workDir` field).
- `baseContext() context.Context` (a `context.Background()`-derived ctx cancelled by `Close`; store a `ctx`/`cancel` pair on `Runtime`, cancel in `Close`).
- `ensureDir` helper (`os.MkdirAll(dir, 0o755)`).

And in `shell3.go`, add to the `Session` struct: `subs subRegistry`. Wire `Session.turnConfig` to set the closures:

```go
	cfg := s.cfg
	cfg.Spawn = func(ctx context.Context, req chat.SpawnRequest) (string, error) {
		return s.spawn(ctx, req)
	}
	cfg.ListAgents = func() []chat.AgentSnapshot { return s.subs.snapshot() }
	return chat.NewTurnConfig(cfg, s.handlers, shellInteractive)
```

> `s.cfg` is `chat.Config`; this requires the `Spawn`/`ListAgents` fields added to `chat.Config` in Task 2 Step 3. `turnConfig` currently passes `s.cfg` straight into `NewTurnConfig`; copy it to a local `cfg` first so the closures are set per-turn without mutating the stored config.

- [ ] **Step 6: Run the tests** — iterate until `go test ./pkg/shell3 -run 'Spawn|ListAgents' -v` passes, then `go test -race -count=1 ./pkg/shell3 ./internal/agentsetup` → PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/shell3 internal/agentsetup && git commit -m "feat(pkg): in-process subagents — spawn on runtime, result to parent inbox, depth limit 1

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 4: pkg/shell3 — `HostEvent` Wake bus + `RunQueued` wake-turn entry

The bus carries out-of-turn lifecycle. v1 has one kind: `Wake`. `Session.RunQueued(ctx)` is the host's response to a Wake — it runs a turn seeded from the queued inbox items.

**Files:**
- Modify: `pkg/shell3/runtime.go` (`HostEvent`, `HostEventKind`, `Wake`, `events` chan, `Events()`, `emit`)
- Modify: `pkg/shell3/shell3.go` (`Session.wake`, `Session.RunQueued`, idle-`Interject` Wake)
- Test: `pkg/shell3/wake_test.go` (new)

- [ ] **Step 1: Write the failing test** (`pkg/shell3/wake_test.go`)

```go
package shell3

import (
	"context"
	"testing"
	"time"
)

// Interject on an idle session emits a Wake; on a busy session it does not
// (it rides the running turn's inbox drain instead).
func TestInterject_IdleEmitsWake(t *testing.T) {
	rt := newFakeRuntime(t, nil)
	defer rt.Close()
	s, _ := rt.Session(SessionOpts{Name: "tg:1"})

	s.Interject("ping while idle")
	select {
	case ev := <-rt.Events():
		if ev.Kind != Wake || ev.Session != "tg:1" {
			t.Fatalf("want Wake for tg:1, got %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("idle Interject should emit Wake")
	}
}

// RunQueued runs a turn seeded from queued inbox items; with an empty inbox it
// returns a closed channel without starting a turn.
func TestRunQueued_EmptyInboxNoTurn(t *testing.T) {
	rt := newFakeRuntime(t, nil)
	defer rt.Close()
	s, _ := rt.Session(SessionOpts{Name: "tg:1"})
	ch := s.RunQueued(context.Background())
	for range ch { // must be already closed / immediately drains
	}
	if s.isBusy() {
		t.Fatal("RunQueued with empty inbox must not start a turn")
	}
}

// A queued item becomes the seed of the RunQueued turn (surfaces in the model's
// view as user input).
func TestRunQueued_RunsTurnFromQueuedItems(t *testing.T) {
	// scripted parent model echoes; assert the turn ran (got a terminal event)
	// and the inbox was drained (subsequent RunQueued is a no-op).
}
```

- [ ] **Step 2: Run to verify failure** — `go test ./pkg/shell3 -run 'Wake|RunQueued' -v` → FAIL (`rt.Events undefined`).

- [ ] **Step 3: Implement the bus** (`pkg/shell3/runtime.go`)

```go
// HostEventKind enumerates out-of-turn runtime events. v1: Wake only.
type HostEventKind int

const (
	// Wake signals a session's inbox gained an item while no turn was running.
	// The host should call Session.RunQueued to react.
	Wake HostEventKind = iota
)

// HostEvent is one out-of-turn event for a session. Payload is reserved for
// future kinds; Wake carries none.
type HostEvent struct {
	Session string
	Kind    HostEventKind
	Payload any
}
```

Add to `Runtime`: `events chan HostEvent`, `workDir string`, `ctx context.Context`, `cancel context.CancelFunc`. In `NewRuntime`, initialize `events: make(chan HostEvent, 64)`, `workDir: workDir`, and a `context.WithCancel(context.Background())` pair. Add:

```go
// Events returns the out-of-turn event bus. One receiver drives N sessions.
// Buffered; if the host is not draining, Wake events coalesce (drop on full —
// the host re-checks inboxes on its next turn anyway).
func (rt *Runtime) Events() <-chan HostEvent { return rt.events }

func (rt *Runtime) root() string                 { return rt.workDir }
func (rt *Runtime) baseContext() context.Context { return rt.ctx }

func (rt *Runtime) emit(ev HostEvent) {
	select {
	case rt.events <- ev:
	default: // bus full: drop (Wake is a hint, not a queue)
	}
}
```

In `Close`, call `rt.cancel()` (before or after closing sessions — after is safest so a subagent's `baseContext` is cancelled as its parent session closes). Do **not** close `rt.events` (a late `emit` from a finishing subagent goroutine must not panic on a closed channel; the `default` branch already protects against a stuck send).

- [ ] **Step 4: Implement `wake`, `RunQueued`, and idle-Interject Wake** (`pkg/shell3/shell3.go`)

```go
// wake emits a Wake for this session on the runtime bus (no-op without a runtime).
func (s *Session) wake() {
	if s.runtime != nil {
		s.runtime.emit(HostEvent{Session: s.name, Kind: Wake})
	}
}
```

Extend the public `Interject` (currently in shell3.go ~368) so that after enqueuing, an idle session wakes:

```go
func (s *Session) Interject(text string, parts ...Part) {
	// ... existing part-loading + s.sess.Interject(text, cps...) ...
	if !s.isBusy() {
		s.wake()
	}
}
```

> A mid-turn `Interject` (busy) must NOT wake — its item is drained by the running turn's between-rounds drain (turn.go:128/218). Only the idle case needs the host to start a turn. This mirrors the spec's "Idle → queued + Wake; in-flight → inbox drain."

Add `RunQueued`:

```go
// RunQueued runs one turn seeded from the session's queued inbox items — the
// host's response to a Wake event. With an empty inbox it returns an already-
// closed channel and starts no turn. Same ErrBusy contract as Send: a turn
// already in flight will itself drain the inbox, so RunQueued is a no-op then.
func (s *Session) RunQueued(ctx context.Context) <-chan Event {
	// The turn loop drains the inbox at its top (the reminder + attachments
	// injection point), so an empty-prompt Send consumes the queued items as the
	// turn's initiating input. Guard the empty/busy cases to avoid a no-op turn.
	if s.isBusy() || !s.sess.HasInbox() {
		closed := make(chan Event)
		close(closed)
		return closed
	}
	return s.Send(ctx, "")
}
```

This needs `chat.Session.HasInbox() bool` — add it in `internal/chat/session.go`:

```go
// HasInbox reports whether any interjected items are queued. Safe to call from
// any goroutine.
func (s *Session) HasInbox() bool {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()
	return len(s.inbox) > 0
}
```

> Decision: `RunQueued` uses `Send(ctx, "")`. Verify the turn loop tolerates an empty user prompt — `turn.go:105` appends `userMsg` (empty content) then drains the inbox into a reminder (`:128`) and attachments message (`:136`). An empty user message with a following reminder is benign on the wire (the adapter sends `content: ""`). If a green test shows the empty user message is undesirable, the fallback is a dedicated `RunInbox` path that builds the user message from the drained items directly — but prefer the simple reuse first and only escalate if a test demands it.

- [ ] **Step 5: Run the tests** — `go test ./pkg/shell3 ./internal/chat -run 'Wake|RunQueued|HasInbox' -v` → PASS, then `go test -race -count=1 ./...` → PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/shell3 internal/chat && git commit -m "feat(pkg): Runtime.Events() Wake bus + Session.RunQueued wake-turn entry

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 5: TUI — consume `Events()`, auto-run wake turn, render subagent notice

The CLI/TUI must *gain* from the change (spec "TUI impact"). When idle and a `Wake` arrives (a subagent finished, or a queued interjection), the TUI auto-runs the wake turn; a finished subagent renders as a dim notice.

**Files:**
- Modify: `internal/tui/interactive.go` (select on `runtime.Events()` alongside input; auto-run `RunQueued`; dim subagent-finished notice)
- Test: `internal/tui/interactive_test.go` (extend with a fake-session/bus test)

- [ ] **Step 1: Read the current TUI event loop.** `internal/tui/interactive.go` already selects on input and the per-turn event channel (phases 1–3 wired `Interject` and the approval prompt here). Identify the main select loop and how it obtains the `*shell3.Session` / `*shell3.Runtime`.

- [ ] **Step 2: Write the failing test** (`internal/tui/interactive_test.go`)

Mirror the existing TUI tests (they use a `fakeSession`-style harness per the spec's "TUI: fakeSession-based tests"). Add a test asserting that: given an idle TUI and a `Wake` delivered on the bus, the TUI calls `RunQueued` and renders the resulting turn; and that a `subagent ... finished:` reminder renders with the dim notice style. Match whatever fake/harness `interactive_test.go` already defines — **read it first** and extend its patterns rather than inventing a new harness.

- [ ] **Step 3: Run to verify failure** — `go test ./internal/tui -run Wake -v` → FAIL.

- [ ] **Step 4: Implement the bus consumption.** In the interactive loop's `select`, add a case receiving from `runtime.Events()`. On `Wake` for the current session when idle: start `session.RunQueued(ctx)` and stream its events through the same renderer used for `Send`. Render the auto-run with a dim `[woke: responding to queued input]`-style line so the user sees why a turn started unprompted. (Keep Esc/cancel behavior unchanged.) If the TUI is single-session, filter `ev.Session` to the active session name.

> Scope note: if `RunOnce` (headless) has no event loop to host the bus, this task touches only `RunInteractive`. The headless path still benefits — subagent results land in the inbox and surface on the next explicit `Send`.

- [ ] **Step 5: Run the tests** — `go test ./internal/tui -run Wake -v` → PASS, then `go test -race -count=1 ./internal/tui` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tui && git commit -m "feat(tui): consume Wake bus — auto-run wake turn, dim subagent-finished notice

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 6: Close-out — CHANGELOG + full verification

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1:** Add under `## [Unreleased]` / `### Added`, as the first new bullet:

```markdown
- Subagents: `spawn_agent(task, agent?, workdir?)` runs a focused subtask as a
  headless `sub:<id>` session on the shared `Runtime`; its result is posted to
  the spawning session's inbox — injected mid-turn if the parent is still
  working, or delivered as a `Wake` on the new `Runtime.Events() <-chan
  HostEvent` bus if the parent is idle. `list_agents()` returns a running/
  finished snapshot. Subagents are depth-limited to 1 (the spawn tools are
  stripped from their schema) and write their own audit JSONL under
  `.shell3/agents/`. Gated per agent by `tools = { subagents = true }`.
  `Session.RunQueued(ctx)` runs a turn seeded from queued inbox items — the
  host's response to a `Wake`. The TUI auto-runs the wake turn when idle and
  renders a finished subagent as a dim notice.
```

- [ ] **Step 2: Full verification** from repo root:

```bash
make lint && go test -race -count=1 ./... && make build
```
→ all green. Sanity-grep that the spawn tools are reachable and gated: `grep -rn "spawn_agent" internal pkg | grep -v _test` should show the tool def, the handler, and the strip helper — nothing orphaned.

- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "docs: changelog for subagents + Wake bus; phase 5 complete

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Self-review notes

- **Import-cycle avoidance:** `internal/chat` never imports `pkg/shell3`. The spawn capability crosses the boundary as plain closures + value types (`SpawnRequest`, `AgentSnapshot`) on `TurnConfig`/`Config`, exactly like `Approve`/`ShellInteractive` already do. Verified `chat.Config` → `NewTurnConfig` is the pass-through seam (Task 2 Step 3).
- **Subagent lifetime ≠ spawning turn's ctx:** the subagent runs on `rt.baseContext()`, not the parent turn's ctx, so it survives the parent turn ending (the whole point — the result arrives *after*). It is bounded by `Runtime.Close()` cancelling that base ctx. This is the one easy-to-get-wrong concurrency decision; the test `TestSpawn_SubagentResultWakesIdleParent` exercises exactly the "parent turn already ended" ordering.
- **Idle vs busy Interject (no double-injection):** idle `Interject` Wakes; busy `Interject` does not (the running turn's drain handles it). `deliverSubagentResult` follows the same rule via `isBusy()`. A small TOCTOU exists (parent finishes between the `isBusy()` check and the Wake) — harmless: the worst case is a spurious Wake on an inbox the next `Send` would have drained anyway, or the host calling `RunQueued` which is a no-op on an empty inbox. Both are safe by construction (`RunQueued` guards empty/busy).
- **Depth limit is schema-level:** the handlers are always present in `turnScopedHandlers`, but the model can't call a tool absent from its schema. `DisableSubagents` strips `spawn_agent`/`list_agents` from the sub session's `Personality.Tools` + `ActiveTools` with a fresh backing array (no aliasing of the cached persona). Pinned by `TestSpawn_DepthLimitStripsToolsFromSubagent`.
- **Wake bus is lossy by design:** buffered (64) with non-blocking `emit`; a full bus drops the Wake. Safe because Wake is a hint — the host re-checks/drains on its next turn, and `RunQueued` is idempotent on an empty inbox. The bus is never closed (a finishing subagent goroutine may `emit` after `Close` started; the `default` branch + never-close keeps that panic-free).
- **`RunQueued` reuses `Send(ctx, "")`:** simplest correct primitive given the turn loop already drains the inbox at the top. The empty-prompt wart is documented with a fallback (a dedicated inbox-seeded user message) gated on a test actually showing a problem — YAGNI until then.
- **Audit JSONL:** `<runtime-root>/.shell3/agents/<id>.jsonl` via the existing `chat.OpenSink` path already wired through `SessionOpts.OutPath`; `ensureDir` creates the dir. No new sink machinery.
- **Spec coverage check:** `spawn_agent(task, agent?, workdir?)` ✓ (Task 1 def, Task 3 impl); headless `sub:<id>` fresh-ctx goroutine ✓ (Task 3); result → parent inbox, mid-turn-inject-or-Wake ✓ (Task 3 `deliverSubagentResult`); `list_agents()` snapshot ✓ (Tasks 1–3); depth limit 1 via stripped schema ✓ (Task 3); per-subagent audit JSONL under `.shell3/agents/` ✓ (Task 3); `Runtime.Events() <-chan HostEvent{Session,Kind,Payload}` with v1 `Wake` ✓ (Task 4); turn-scoped Go handlers like `compact_history` ✓ (Task 2); TUI auto-run + notice ✓ (Task 5). Non-goal honored: subagent token streams stay in their own JSONL, the bus carries lifecycle only (no per-token events on `Events()`).
- **Open verification for the implementer:** confirm `AgentRuntime` returns `chat.ActiveAgent` by value (strip helper assumes it); confirm `NewTurnConfig`'s exact field list when adding `Spawn`/`ListAgents` to `chat.Config`; confirm `internal/tui/interactive.go` exposes the `*Runtime` to its select loop (Task 5). Each is a grep, noted at its step.
