# Subagent Registry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the `tools = { subagents = true }` boolean with an explicit registry of named subagents (`shell3.subagent{...}`), each carrying a "when to use" description; an agent lists the subagents it may delegate to, and the model sees exactly those (name + description) on one `spawn_agent(task, subagent, workdir?)` tool whose `subagent` parameter is an enum.

**Architecture:** Subagents are a separate declaration registered into their own `LoadedConfig` registry (never in the Tab/`AgentNames` rotation) and referenced by handle in an agent's `tools.subagents` array (like `mcp`/`custom`). At schema-assembly time, an agent with a non-empty subagent list gets a `spawn_agent` tool whose `subagent` enum and description are built from its resolved subagents. Spawning resolves the chosen subagent's config from the subagent registry; depth-limit 1 is retained.

**Tech Stack:** Go (this repo). Tests: standard `testing` + `internal/llm/fakellm`, race-enabled, hermetic (temp HOME). Spec: `docs/dev/superpowers/specs/2026-06-10-subagent-registry-design.md`.

**Conventions:**
- TDD: failing test first, then implementation, then green.
- Verify each task with `go test -race -count=1 ./...` from the repo root.
- Never read `.env` (beside any `shell3.lua`) or `ai-do-not-read.*` files.
- Branch: `subagent-registry` (already created off `main`). Do NOT merge to main.
- Commit bodies end with `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.

---

## Starting context (verified against the code on `main`)

- `internal/luacfg/luacfg.go`: `type LoadedConfig struct { Models; Tools; MCPServers; Skills; Secrets; agents []Agent; L; mu; vmLockHeld }`. `Agents() []Agent` returns a copy of `c.agents`. The finalize/validate loop (~line 121) fills default models and validates each agent's model. `type Agent struct { Name, ModelName, Prompt string; Gates ToolGates; CustomTools []string; MCPServerNames []string; Skills []string; SkillsDisabled bool; Guard []GuardEntry }`. `type ToolGates struct { Bash, BashBg, ShellInteractive, Edit, History, Prune, Compact, Media, Subagents bool }`.
- `internal/luacfg/register.go`: handle pattern — `luaSkill`/`luaMCP`/`luaTool` return a table marked `__skill`/`__mcp`/`__tool` and `handleNames(tbl, marker)` collects names. `luaAgent` (line 154) parses `agentKeys = {name, model, prompt, tools, skills, on_tool_call}`, builds `ToolGates` (incl. `Subagents: optBool(tt,"subagents")`), and reads `tools.custom`/`tools.mcp` via `handleNames`. `toolGateKeys` (line 90) lists allowed `tools` keys incl. `"subagents"`. `luaAgent` appends to `c.agents` and returns 0 (no handle).
- `internal/luacfg/tooldefs.go`: `ToolDefs(g ToolGates, custom []CustomTool, hasSkills bool) []llm.ToolDefinition` appends `spawnAgentTool, listAgentsTool` when `g.Subagents`. `spawnAgentTool` has params `task`(required), `agent`, `workdir`; `listAgentsTool` has empty params.
- `internal/agentsetup/agentsetup.go`: `AgentRuntime(name)` (line 102) → `p.lc.AgentByName(name)` (or `FirstAgent`), builds `toolDefs := luacfg.ToolDefs(a.Gates, customDefs, hasSkills)` + `toolNames`, returns `chat.ActiveAgent`. `SessionOptions{Agent, WorkDir, Headless, OutPath, DisableSubagents}` (line ~193). `SessionConfig` (line ~203): `rt, err := p.AgentRuntime(so.Agent)`; `if so.DisableSubagents { rt = stripSubagentTools(rt) }`. `stripSubagentTools` (line 208) drops `spawn_agent`/`list_agents` from `Personality.Tools` + `ActiveTools`.
- `internal/chat/toolhandler.go`: `type SpawnRequest struct { Task, Agent, WorkDir string }`; `type AgentSnapshot struct { ID, Agent, Task, Status, Result string }`; `TurnConfig.Spawn func(ctx, SpawnRequest)(string,error)` + `ListAgents func()[]AgentSnapshot`, threaded via `chat.Config` → `NewTurnConfig`.
- `internal/chat/turn.go`: `turnScopedHandlers` has `spawn_agent` (parses `{task, agent, workdir}`, requires non-empty task, calls `cfg.Spawn`) and `list_agents`.
- `pkg/shell3/subagents.go`: `Session.spawn(_ ctx, req chat.SpawnRequest)` → `agent := req.Agent; if agent=="" { agent = s.cfg.Personality.Name }`; `rt.Session(SessionOpts{Name:"sub:"+id, Agent:agent, WorkDir, Headless:true, OutPath:auditPath, DisableSubagents:true})`. `pkg/shell3/runtime.go`: `SessionOpts{Name, Agent, WorkDir, Headless, OutPath, ShellInteractive, Approve, DisableSubagents}`; `NewRuntime`'s `sessionConfig` maps to `agentsetup.SessionOptions`.
- Scaffold: `internal/scaffold/defaults/base/shell3.lua.tmpl` — `code` agent has `subagents = true` in its `tools`; `internal/scaffold/scaffold_test.go` `TestRenderedConfigLoads` asserts agent count/names and `agents[0].Gates.Subagents`. `cmd/shell3/boot_test.go` enumerates rendered files.

## File Structure

| File | Change | Task |
|------|--------|------|
| `internal/luacfg/luacfg.go` | `Subagent` type, `c.subagents` registry + accessors, `Agent.Description`/`Agent.Subagents`, drop `ToolGates.Subagents`, validate subagent refs/collisions | 1 |
| `internal/luacfg/register.go` | `luaSubagent` + `__subagent` handle + `subagentKeys`; parse `tools.subagents` as handle array; nested-subagent + missing-description load errors | 1 |
| `internal/luacfg/dispatch.go`/wiring | register `shell3.subagent` in the Lua VM | 1 |
| `internal/luacfg/tooldefs.go` | `SubagentInfo` type + `SpawnToolDefs(infos)` builder (enum + description); remove `g.Subagents` emit | 2 |
| `internal/agentsetup/agentsetup.go` | resolve agent's subagents→infos & append spawn defs in `AgentRuntime`; `SubagentRuntime(name)` + `SessionOptions.Subagent` resolution | 3 |
| `internal/chat/toolhandler.go`, `internal/chat/turn.go` | `SpawnRequest.Subagent` (replace `Agent`); `spawn_agent` arg `subagent` (required enum) | 4 |
| `pkg/shell3/runtime.go`, `pkg/shell3/subagents.go` | `SessionOpts.Subagent`; spawn resolves via `Subagent`; validate non-empty | 4 |
| `internal/scaffold/defaults/base/shell3.lua.tmpl`, `scaffold_test.go`, `cmd/shell3/boot_test.go` | example subagent + wire into `code`; update tests | 5 |
| `pkg/shell3/shell3.go` (pkg doc), `README.md`, `CHANGELOG.md`, `docs/cookbook` | docs | 6 |

---

## Task 1: luacfg — `shell3.subagent` declaration + registry + agent opt-in

**Files:**
- Modify: `internal/luacfg/luacfg.go`
- Modify: `internal/luacfg/register.go`
- Modify: wherever Lua globals are registered (grep `RawSetString("agent"` / `SetGlobal` / where `luaAgent` is bound — likely `dispatch.go` or `luacfg.go`)
- Test: `internal/luacfg/subagent_decl_test.go` (new)

- [ ] **Step 1: Write the failing tests** (`internal/luacfg/subagent_decl_test.go`)

```go
package luacfg

import (
	"strings"
	"testing"
)

// loadLua is the existing test helper that writes src to a temp shell3.lua,
// loads it, and returns (*LoadedConfig, error). VERIFY its real name/signature
// in the existing luacfg tests (e.g. multiagent_test.go) and use that; the
// calls below assume loadString(t, src) returning (*LoadedConfig, error).

func TestSubagent_RegistersSeparateFromAgents(t *testing.T) {
	src := `
shell3.model({ name="m", base_url="x", model="y" })
local researcher = shell3.subagent({
  name = "researcher", description = "investigate the repo",
  model = "m", prompt = "you research",
  tools = { bash = true },
})
shell3.agent({
  name = "code", model = "m", prompt = "you code",
  tools = { bash = true, subagents = { researcher } },
})
`
	c := mustLoad(t, src)
	defer c.Close()
	// subagent is NOT a top-level agent
	for _, a := range c.Agents() {
		if a.Name == "researcher" {
			t.Fatal("researcher must not appear in Agents()/Tab rotation")
		}
	}
	sa, ok := c.SubagentByName("researcher")
	if !ok {
		t.Fatal("researcher not in subagent registry")
	}
	if sa.Description != "investigate the repo" {
		t.Fatalf("description = %q", sa.Description)
	}
	// the code agent references it by name
	code, ok := c.AgentByName("code")
	if !ok {
		t.Fatal("no code agent")
	}
	if len(code.Subagents) != 1 || code.Subagents[0] != "researcher" {
		t.Fatalf("code.Subagents = %v, want [researcher]", code.Subagents)
	}
}

func TestSubagent_MissingDescriptionErrors(t *testing.T) {
	src := `shell3.model({name="m",base_url="x",model="y"})
shell3.subagent({ name="r", model="m", prompt="p", tools={bash=true} })`
	if _, err := tryLoad(t, src); err == nil || !strings.Contains(err.Error(), "description") {
		t.Fatalf("want description-required error, got %v", err)
	}
}

func TestSubagent_NestedSubagentsErrors(t *testing.T) {
	src := `shell3.model({name="m",base_url="x",model="y"})
local x = shell3.subagent({name="x",description="d",model="m",prompt="p",tools={bash=true}})
shell3.subagent({ name="r", description="d", model="m", prompt="p",
  tools = { bash=true, subagents = { x } } })`
	if _, err := tryLoad(t, src); err == nil || !strings.Contains(err.Error(), "subagent") {
		t.Fatalf("want nested-subagents error, got %v", err)
	}
}

func TestSubagent_UnknownReferenceErrors(t *testing.T) {
	// referencing a non-handle / undeclared subagent in an agent's list fails at load
	src := `shell3.model({name="m",base_url="x",model="y"})
shell3.agent({ name="code", model="m", prompt="p",
  tools = { bash=true, subagents = { { __subagent = "ghost" } } } })`
	if _, err := tryLoad(t, src); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("want unknown-subagent error, got %v", err)
	}
}

func TestSubagent_NameCollisionWithAgentErrors(t *testing.T) {
	src := `shell3.model({name="m",base_url="x",model="y"})
shell3.agent({ name="dup", model="m", prompt="p", tools={bash=true} })
shell3.subagent({ name="dup", description="d", model="m", prompt="p", tools={bash=true} })`
	if _, err := tryLoad(t, src); err == nil || !strings.Contains(err.Error(), "dup") {
		t.Fatalf("want name-collision error, got %v", err)
	}
}
```

> Before running: open `internal/luacfg/multiagent_test.go` (and `*_test.go` siblings) and find the REAL load helpers. Replace `mustLoad`/`tryLoad`/`c.Close()` with whatever the suite uses (the repo's tests load a temp file via `luacfg.Load(path, dir)`; write a small local helper if none exists). Keep the assertions.

- [ ] **Step 2: Run to verify failure** — `go test ./internal/luacfg -run Subagent -v` → FAIL (`shell3.subagent` undefined / `SubagentByName` undefined / `Agent.Subagents` undefined).

- [ ] **Step 3: Add the `Subagent` type, registry, and `Agent` fields** (`internal/luacfg/luacfg.go`)

```go
// Subagent is a delegatable specialist: a non-interactive agent the model can
// spawn via spawn_agent. Registered separately from agents (never in the Tab
// rotation). Description is the model-facing "when to use".
type Subagent struct {
	Name, Description, ModelName, Prompt string
	Gates                                ToolGates
	CustomTools                          []string
	MCPServerNames                       []string
	Skills                               []string
	SkillsDisabled                       bool
	Guard                                []GuardEntry
}
```

Add `subagents []Subagent` to `LoadedConfig`. Add `Description string` and `Subagents []string` to `Agent`. Remove `Subagents bool` from `ToolGates`. Add accessors:

```go
// SubagentByName returns the registered subagent and whether it exists.
func (c *LoadedConfig) SubagentByName(name string) (Subagent, bool) {
	for _, s := range c.subagents {
		if s.Name == name {
			return s, true
		}
	}
	return Subagent{}, false
}

// Subagents returns a copy of the registered subagents.
func (c *LoadedConfig) Subagents() []Subagent {
	out := make([]Subagent, len(c.subagents))
	copy(out, c.subagents)
	return out
}
```

In the finalize/validate loop (~line 121, where agent models are defaulted/validated), ALSO: default+validate each subagent's model the same way; then validate cross-references — for every agent, each name in `a.Subagents` must resolve via `SubagentByName` (else `fmt.Errorf("config: agent %q references unknown subagent %q", a.Name, name)`); and reject a subagent whose name equals any agent name (`config: %q is declared as both an agent and a subagent`).

- [ ] **Step 4: Add `luaSubagent` + handle + parse `tools.subagents`** (`internal/luacfg/register.go`)

Add key set + registration (mirror `luaAgent`, require `description`, forbid nested `subagents`, return a `__subagent` handle):

```go
var subagentKeys = map[string]bool{
	"name": true, "description": true, "model": true, "prompt": true,
	"tools": true, "skills": true, "on_tool_call": true,
}

func (c *LoadedConfig) luaSubagent(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "subagent", subagentKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	s := Subagent{
		Name:        optStr(opts, "name"),
		Description: optStr(opts, "description"),
		ModelName:   optStr(opts, "model"),
		Prompt:      optStr(opts, "prompt"),
	}
	if s.Name == "" || s.Description == "" {
		L.RaiseError("subagent: name and description are required")
	}
	for _, ex := range c.subagents {
		if ex.Name == s.Name {
			L.RaiseError("subagent %q: already declared (subagent names must be unique)", s.Name)
		}
	}
	if sk, ok := opts.RawGetString("skills").(*lua.LTable); ok {
		s.Skills = handleNames(sk, "__skill")
	}
	if tt, ok := opts.RawGetString("tools").(*lua.LTable); ok {
		if err := checkKeys(tt, "subagent.tools", toolGateKeys); err != nil {
			L.RaiseError("%s", err.Error())
		}
		if tt.RawGetString("subagents") != lua.LNil {
			L.RaiseError("subagent %q: a subagent may not declare its own subagents (depth limit 1)", s.Name)
		}
		s.Gates = parseGates(tt) // see Step 5 refactor
		if cu, ok := tt.RawGetString("custom").(*lua.LTable); ok {
			s.CustomTools = handleNames(cu, "__tool")
		}
		if mc, ok := tt.RawGetString("mcp").(*lua.LTable); ok {
			s.MCPServerNames = handleNames(mc, "__mcp")
		}
		if tt.RawGetString("skill") == lua.LFalse {
			s.SkillsDisabled = true
		}
	}
	if g, ok := opts.RawGetString("on_tool_call").(*lua.LTable); ok {
		g.ForEach(func(_, v lua.LValue) {
			if fn, ok := v.(*lua.LFunction); ok {
				s.Guard = append(s.Guard, GuardEntry{fn: fn})
			}
		})
	}
	c.subagents = append(c.subagents, s)
	h := L.NewTable()
	h.RawSetString("__subagent", lua.LString(s.Name))
	L.Push(h)
	return 1
}
```

- [ ] **Step 5: Refactor gate parsing + change `luaAgent`'s `tools.subagents`** (`internal/luacfg/register.go`)

Extract the gate-literal into a helper so agent + subagent share it (DRY), now WITHOUT the `Subagents` bool:

```go
func parseGates(tt *lua.LTable) ToolGates {
	return ToolGates{
		Bash:             optBool(tt, "bash"),
		BashBg:           optBool(tt, "bash_bg"),
		ShellInteractive: optBool(tt, "shell_interactive"),
		Edit:             optBool(tt, "edit"),
		History:          optBool(tt, "history"),
		Prune:            optBool(tt, "prune"),
		Compact:          optBool(tt, "compact"),
		Media:            optBool(tt, "media"),
	}
}
```

In `luaAgent`'s `tools` block, replace `a.Gates = ToolGates{...}` with `a.Gates = parseGates(tt)`, and add subagent-array parsing:

```go
		if sg, ok := tt.RawGetString("subagents").(*lua.LTable); ok {
			a.Subagents = handleNames(sg, "__subagent")
		}
```

`toolGateKeys` already contains `"subagents"`, so `checkKeys` still accepts it (the value is now a table, not a bool — `checkKeys` only validates key names). Leave `toolGateKeys` as-is.

- [ ] **Step 6: Register `shell3.subagent` in the Lua VM.** Find where `luaAgent` is bound to the `shell3` table (grep `"agent"` near `RawSetString`/`SetFuncs`/`L.NewFunction` in register.go or dispatch.go). Add a sibling binding: `"subagent": L.NewFunction(c.luaSubagent)` (match the exact registration idiom used for `agent`/`tool`/`mcp`/`skill`).

- [ ] **Step 7: Fix compile fallout from removing `ToolGates.Subagents`.** `grep -rn "Gates.Subagents\|\.Subagents\b\|Subagents:" internal --include=*.go` — the `tooldefs.go` `if g.Subagents` emit (Task 2 removes it), the `register.go` literal (done), and any test referencing `Gates.Subagents` (e.g. `scaffold_test.go` — Task 5 updates it). For Task 1, just get `internal/luacfg` compiling: tooldefs still references `g.Subagents` → temporarily that's a compile error, so do Task 2's tooldefs change together or stub it. RECOMMENDED: do Task 1 + Task 2 in the same working session (they're one compile unit); commit them together.

- [ ] **Step 8: Run** — `go test -race -count=1 ./internal/luacfg -run Subagent -v` → PASS, then `go test -race -count=1 ./internal/luacfg` → PASS. Commit happens at end of Task 2.

---

## Task 2: luacfg/tooldefs — enum-based `spawn_agent` builder

**Files:**
- Modify: `internal/luacfg/tooldefs.go`
- Test: `internal/luacfg/subagent_tool_test.go` (the existing file from the earlier work — update it)

- [ ] **Step 1: Update/replace the failing tests** (`internal/luacfg/subagent_tool_test.go`)

The old `TestToolDefs_SubagentsGate`/`TestSpawnAgentTool_Schema` assert the gate-driven static def. Replace with:

```go
package luacfg

import "testing"

func TestSpawnToolDefs_EnumAndDescription(t *testing.T) {
	infos := []SubagentInfo{
		{Name: "researcher", Description: "investigate the repo"},
		{Name: "planner", Description: "make a plan"},
	}
	defs := SpawnToolDefs(infos)
	var spawn, list *llmDef
	_ = spawn
	_ = list
	var sawSpawn, sawList bool
	for _, d := range defs {
		switch d.Name {
		case "spawn_agent":
			sawSpawn = true
			props := d.Parameters["properties"].(map[string]any)
			sub := props["subagent"].(map[string]any)
			enum, _ := sub["enum"].([]string)
			if len(enum) != 2 || enum[0] != "researcher" || enum[1] != "planner" {
				t.Fatalf("subagent enum = %v, want [researcher planner]", enum)
			}
			if !contains(d.Description, "researcher") || !contains(d.Description, "investigate the repo") ||
				!contains(d.Description, "planner") {
				t.Fatalf("description must list each subagent + when-to-use; got %q", d.Description)
			}
			req := d.Parameters["required"].([]string)
			if !hasStr(req, "task") || !hasStr(req, "subagent") {
				t.Fatalf("task and subagent must be required; got %v", req)
			}
		case "list_agents":
			sawList = true
		}
	}
	if !sawSpawn || !sawList {
		t.Fatalf("want spawn_agent + list_agents; spawn=%v list=%v", sawSpawn, sawList)
	}
}

func TestSpawnToolDefs_EmptyIsNoTools(t *testing.T) {
	if defs := SpawnToolDefs(nil); len(defs) != 0 {
		t.Fatalf("no subagents → no spawn tools; got %v", defs)
	}
}
```

> Remove the `llmDef`/`contains`/`hasStr` placeholders — use the real `llm.ToolDefinition` type and small inline helpers or `strings.Contains`. Delete the now-obsolete `TestToolDefs_SubagentsGate` and `TestSpawnAgentTool_Schema`.

- [ ] **Step 2: Run to verify failure** — `go test ./internal/luacfg -run SpawnToolDefs -v` → FAIL (`SpawnToolDefs`/`SubagentInfo` undefined).

- [ ] **Step 3: Add `SubagentInfo` + `SpawnToolDefs`; remove the gate emit** (`internal/luacfg/tooldefs.go`)

```go
// SubagentInfo is the (name, when-to-use) pair surfaced to the model for one
// registered subagent.
type SubagentInfo struct{ Name, Description string }

// SpawnToolDefs returns the spawn_agent + list_agents tool defs for an agent
// that registered the given subagents. Returns nil when there are none (the
// agent then gets no spawn tooling). spawn_agent's `subagent` parameter is an
// enum of the registered names; the description lists each name + when-to-use.
func SpawnToolDefs(subs []SubagentInfo) []llm.ToolDefinition {
	if len(subs) == 0 {
		return nil
	}
	names := make([]string, len(subs))
	var b strings.Builder
	b.WriteString("Delegate a focused, independent subtask to a subagent that runs in the background; " +
		"its result comes back to you automatically when it finishes (you do not poll). " +
		"Choose the subagent best suited to the task:\n")
	for i, s := range subs {
		names[i] = s.Name
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Description)
	}
	spawn := llm.ToolDefinition{
		Name:        "spawn_agent",
		Description: b.String(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task":     map[string]any{"type": "string", "description": "The full, self-contained task prompt. The subagent does not see this conversation."},
				"subagent": map[string]any{"type": "string", "description": "Which subagent to run (see the list above).", "enum": names},
				"workdir":  map[string]any{"type": "string", "description": "Working directory to root the subagent in (absolute, or relative to your workdir). Omit to use your workdir."},
			},
			"required": []string{"task", "subagent"},
		},
	}
	return []llm.ToolDefinition{spawn, listAgentsTool}
}
```

Keep `listAgentsTool` as-is. DELETE `spawnAgentTool` (the old static var) and the `if g.Subagents { ... }` block in `ToolDefs`. Ensure `tooldefs.go` imports `fmt` and `strings` (add if missing).

- [ ] **Step 4: Run** — `go test -race -count=1 ./internal/luacfg` → PASS (both Task 1 and Task 2 tests; the package now compiles without `ToolGates.Subagents`).

- [ ] **Step 5: Commit (Tasks 1+2 together)**

```bash
git add internal/luacfg && git commit -m "feat(luacfg): subagent registry + enum-based spawn_agent tool def

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 3: agentsetup — resolve subagents into schema + spawn config

**Files:**
- Modify: `internal/agentsetup/agentsetup.go`
- Test: `internal/agentsetup/agentsetup_test.go` (extend)

- [ ] **Step 1: Write the failing test** (`internal/agentsetup/agentsetup_test.go`)

Mirror the existing tests in that file (they build `Parts` from a temp config — reuse the existing harness/helper). Assert:

```go
func TestAgentRuntime_ExposesRegisteredSubagents(t *testing.T) {
	// config: a "researcher" subagent + a "code" agent listing it.
	p := buildPartsFromLua(t, `
shell3.model({name="m",base_url="x",model="y"})
local r = shell3.subagent({name="researcher",description="investigate",model="m",prompt="p",tools={bash=true}})
shell3.agent({name="code",model="m",prompt="p",tools={bash=true,subagents={r}}})
`)
	rt, err := p.AgentRuntime("code")
	if err != nil { t.Fatal(err) }
	var spawn *llm.ToolDefinition
	for i := range rt.Personality.Tools {
		if rt.Personality.Tools[i].Name == "spawn_agent" { spawn = &rt.Personality.Tools[i] }
	}
	if spawn == nil { t.Fatal("code agent should expose spawn_agent") }
	enum := spawn.Parameters["properties"].(map[string]any)["subagent"].(map[string]any)["enum"].([]string)
	if len(enum) != 1 || enum[0] != "researcher" { t.Fatalf("enum=%v", enum) }
}

func TestSessionConfig_ResolvesSubagentConfig(t *testing.T) {
	p := buildPartsFromLua(t, /* same config */)
	cfg, err := p.SessionConfig(SessionOptions{Subagent: "researcher", DisableSubagents: true})
	if err != nil { t.Fatal(err) }
	if cfg.Personality.Name != "researcher" { t.Fatalf("subagent session should run as researcher, got %q", cfg.Personality.Name) }
	for _, td := range cfg.Personality.Tools {
		if td.Name == "spawn_agent" { t.Fatal("spawned subagent must not have spawn_agent (depth limit)") }
	}
}
```

> Use the file's real `Parts`-building helper (grep the test file). If none exists, write `buildPartsFromLua(t, src)` that writes a temp `shell3.lua` + empty `.env` and calls `BuildParts(Options{ConfigPath, CWD, HomeDir})`.

- [ ] **Step 2: Run to verify failure** — `go test ./internal/agentsetup -run 'Subagent|SessionConfig_ResolvesSubagent' -v` → FAIL.

- [ ] **Step 3: Append spawn defs in `AgentRuntime`** (`internal/agentsetup/agentsetup.go`, after `toolDefs := luacfg.ToolDefs(...)` ~line 123)

```go
	if len(a.Subagents) > 0 {
		infos := make([]luacfg.SubagentInfo, 0, len(a.Subagents))
		for _, name := range a.Subagents {
			sa, ok := p.lc.SubagentByName(name)
			if !ok {
				// Load-time validation already guarantees resolution; defensive.
				continue
			}
			infos = append(infos, luacfg.SubagentInfo{Name: sa.Name, Description: sa.Description})
		}
		spawnDefs := luacfg.SpawnToolDefs(infos)
		toolDefs = append(toolDefs, spawnDefs...)
		for _, d := range spawnDefs {
			toolNames = append(toolNames, d.Name)
		}
	}
```

- [ ] **Step 4: Add `SessionOptions.Subagent` + `SubagentRuntime` + resolution** (`internal/agentsetup/agentsetup.go`)

Add `Subagent string` to `SessionOptions` (doc: "when set, the session runs the named subagent's config instead of an agent; used by spawn_agent"). Add a `SubagentRuntime(name)` that builds a `chat.ActiveAgent` from a `Subagent` — factor the shared body out of `AgentRuntime` into a helper `runtimeFromAgentLike(name, model, prompt, gates, custom, mcp, skills, skillsDisabled, guard, subagentInfos)` so agents and subagents share it. A subagent passes NO subagentInfos (it can't spawn), so it never gets spawn tooling. In `SessionConfig`, branch at the top:

```go
	var rt chat.ActiveAgent
	var err error
	if so.Subagent != "" {
		rt, err = p.SubagentRuntime(so.Subagent)
	} else {
		rt, err = p.AgentRuntime(so.Agent)
	}
	if err != nil {
		return chat.Config{}, err
	}
	if so.DisableSubagents {
		rt = stripSubagentTools(rt)
	}
```

`SubagentRuntime` errors if the name isn't registered. Keep `stripSubagentTools` (defense-in-depth; a subagent has no spawn tools anyway). `ModeLabel`/`Personality.Name` for a subagent session = the subagent name.

- [ ] **Step 5: Run** — `go test -race -count=1 ./internal/agentsetup` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/agentsetup && git commit -m "feat(agentsetup): expose registered subagents in schema; resolve subagent spawn config

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 4: chat + pkg/shell3 — spawn the chosen subagent

**Files:**
- Modify: `internal/chat/toolhandler.go` (`SpawnRequest`)
- Modify: `internal/chat/turn.go` (`spawn_agent` handler arg)
- Modify: `pkg/shell3/runtime.go` (`SessionOpts.Subagent`)
- Modify: `pkg/shell3/subagents.go` (`spawn` resolves via Subagent)
- Test: update `internal/chat/subagent_handler_test.go`, `pkg/shell3/subagents_test.go`

- [ ] **Step 1: Update the failing tests.** In `internal/chat/subagent_handler_test.go`, change the spawn test so the model emits `{"task":"...","subagent":"researcher"}` and the captured `SpawnRequest` has `Subagent == "researcher"` (rename the field expectation from `Agent`). In `pkg/shell3/subagents_test.go`, change spawns to pass a `Subagent` and assert the `sub:<id>` session is created with `SessionOpts.Subagent == <name>` (the harness's `subOptsRecorder` already records `SessionOpts`). Add: an empty/unknown `subagent` returns an error result.

- [ ] **Step 2: Run to verify failure** — `go test ./internal/chat ./pkg/shell3 -run 'Spawn|Subagent' -v` → FAIL.

- [ ] **Step 3: Rename `SpawnRequest.Agent` → `Subagent`** (`internal/chat/toolhandler.go`)

```go
type SpawnRequest struct {
	Task     string
	Subagent string // which registered subagent to run (required)
	WorkDir  string
}
```

- [ ] **Step 4: Update the `spawn_agent` handler** (`internal/chat/turn.go`, in `turnScopedHandlers`)

```go
		"spawn_agent": funcHandler{name: "spawn_agent", fn: func(ctx context.Context, _ string, args json.RawMessage, _ ToolConfig) (string, error) {
			if cfg.Spawn == nil {
				return "error: subagent spawning is not available in this runtime", nil
			}
			var a struct {
				Task     string `json:"task"`
				Subagent string `json:"subagent"`
				Workdir  string `json:"workdir"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "error: invalid spawn_agent arguments: " + err.Error(), nil
			}
			if strings.TrimSpace(a.Task) == "" {
				return "error: spawn_agent requires a non-empty task", nil
			}
			if strings.TrimSpace(a.Subagent) == "" {
				return "error: spawn_agent requires a subagent (one of the registered names)", nil
			}
			id, err := cfg.Spawn(ctx, SpawnRequest{Task: a.Task, Subagent: a.Subagent, WorkDir: a.Workdir})
			if err != nil {
				return "error: spawn failed: " + err.Error(), nil
			}
			return "spawned subagent " + id + "; its result will arrive automatically when it finishes. Do not poll in a tight loop.", nil
		}},
```

- [ ] **Step 5: Thread `Subagent` through pkg/shell3** 

`pkg/shell3/runtime.go`: add `Subagent string` to `SessionOpts` (doc: "run this named subagent's config; set by spawn_agent"). In `NewRuntime`'s `sessionConfig` closure, pass `Subagent: o.Subagent` into `agentsetup.SessionOptions`.

`pkg/shell3/subagents.go` `spawn`: replace agent resolution with the subagent name (required; no fallback to the caller's agent):

```go
func (s *Session) spawn(_ context.Context, req chat.SpawnRequest) (string, error) {
	if s.runtime == nil {
		return "", fmt.Errorf("shell3: session has no runtime; cannot spawn subagents")
	}
	if req.Subagent == "" {
		return "", fmt.Errorf("shell3: spawn requires a subagent name")
	}
	workdir := req.WorkDir
	if workdir == "" {
		workdir = s.cfg.WorkDir
	} else if !filepath.IsAbs(workdir) {
		workdir = filepath.Join(s.cfg.WorkDir, workdir)
	}
	id := s.runtime.nextSubID()
	auditPath := filepath.Join(s.runtime.root(), ".shell3", "agents", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(auditPath), 0o755); err != nil {
		return "", err
	}
	child, err := s.runtime.Session(SessionOpts{
		Name: "sub:" + id, Subagent: req.Subagent, WorkDir: workdir,
		Headless: true, OutPath: auditPath, DisableSubagents: true,
	})
	if err != nil {
		return "", err // unknown subagent surfaces here → handler returns "spawn failed"
	}
	sa := s.subs.add(req.Subagent, req.Task) // registry: agent field now holds the subagent name
	// ... unchanged: trackSubagent goroutine, deliverSubagentResult ...
}
```

Update `subRegistry.add`'s first param name/usage if needed (it stored `agent`; now store the subagent name — same string field, just semantics). `AgentSnapshot.Agent` now carries the subagent name; that's fine for `list_agents`.

- [ ] **Step 6: Run** — `go test -race -count=1 ./internal/chat ./pkg/shell3 -v -run 'Spawn|Subagent'` → PASS, then `go test -race -count=1 ./...` → GREEN (catches any other caller of the renamed field).

- [ ] **Step 7: Commit**

```bash
git add internal/chat pkg/shell3 && git commit -m "feat(runtime): spawn_agent runs a chosen registered subagent (enum-validated)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 5: scaffold — example subagent in the boot config

**Files:**
- Modify: `internal/scaffold/defaults/base/shell3.lua.tmpl`
- Modify: `internal/scaffold/scaffold_test.go`
- Modify: `cmd/shell3/boot_test.go` (if it asserts agent/file specifics)

- [ ] **Step 1: Read the template + tests.** Confirm the `code` agent's `subagents = true` line and the `TestRenderedConfigLoads` assertions (`agents[0].Gates.Subagents`, agent counts).

- [ ] **Step 2: Update the template** (`shell3.lua.tmpl`): declare an example subagent before the `code` agent and reference it. Insert after the guards/skills requires:

```lua
local explorer = shell3.subagent({
  name        = "explorer",
  description = "Read-only investigation of the codebase — locate where/how something is implemented, summarize a subsystem, or gather context across many files. No edits.",
  model       = "{{.Name | luaesc}}",
  prompt = [[
You are a focused code explorer. Investigate the question using bash (rg, fd,
cat, git log) and report a concise, concrete answer with file:line references.
You cannot edit files. Decide and proceed; no human is available.
  ]],
  tools = { bash = true, history = true },
  on_tool_call = { guards.no_env_edit },
})
```

In the `code` agent's `tools`, replace `subagents = true,` with `subagents = { explorer },`.

- [ ] **Step 3: Update `scaffold_test.go`.** In `TestRenderedConfigLoads`: drop the `agents[0].Gates.Subagents` assertion (the field is gone). Add: the config has one registered subagent (`len(c.Subagents()) == 1`, name `explorer`), it is NOT in `c.Agents()`, and the `code` agent's `Subagents` lists `explorer`. Assert the code agent's resolved schema exposes `spawn_agent` with `explorer` in its enum — build via `agentsetup` (or assert at the luacfg level: `code.Subagents == ["explorer"]`). Keep the `confirm_destructive`/skills assertions. If `TestRenderBaseConfig` string-matches `subagents         = true`, change it to match `subagents = { explorer }` and add a check that the rendered config contains `shell3.subagent(`.

- [ ] **Step 4: Update `cmd/shell3/boot_test.go`** if it asserts the rendered agent set or `Gates.Subagents`. (Grep `Subagents`/`subagents` in that file.)

- [ ] **Step 5: Run** — `go test -race -count=1 ./internal/scaffold ./cmd/shell3` → PASS, then `go test -race -count=1 ./...` → GREEN.

- [ ] **Step 6: Commit**

```bash
git add internal/scaffold cmd/shell3 && git commit -m "feat(scaffold): ship an example subagent (explorer) wired into the code agent

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 6: docs — package doc, README, CHANGELOG, cookbook

**Files:**
- Modify: `pkg/shell3/shell3.go` (package doc, Subagents section)
- Modify: `README.md`, `CHANGELOG.md`
- Modify: `docs/cookbook` (if it mentions `subagents = true`)

- [ ] **Step 1: Update the `pkg/shell3` package doc Subagents section** (`pkg/shell3/shell3.go`, the `# Subagents` block). Replace the `tools = { subagents = true }` description with: subagents are declared via `shell3.subagent{name, description, ...}` and listed per-agent via `tools = { subagents = { handle, ... } }`; the model gets one `spawn_agent(task, subagent, workdir?)` tool whose `subagent` is an enum of the registered names (description = each name + its when-to-use); spawning runs the chosen subagent's config; depth-limit 1; results to the parent inbox as before. Verify every symbol named still exists.

- [ ] **Step 2: README** — update any `subagents = true` mention to the registry form; if the README has a config/agents example, show `shell3.subagent` + the array.

- [ ] **Step 3: CHANGELOG** — under `## [Unreleased] / ### Added` (or a new `### Changed`), note: "Subagents are now an explicit registry: declare `shell3.subagent{name, description, …}` and list them per-agent via `tools = { subagents = { … } }`; the model sees a `spawn_agent` tool whose `subagent` enum + description are the registered specialists. Replaces the `tools = { subagents = true }` boolean." Since the boolean shipped only on this branch's parent (now on main, unreleased), phrase as the current behavior, not a migration.

- [ ] **Step 4: Cookbook** — `grep -rn "subagents = true\|subagents=true" docs/cookbook` and update any hit to the registry form.

- [ ] **Step 5: Full verification + commit**

```bash
make lint && go test -race -count=1 ./... && make build
grep -rn "subagents = true\|Gates.Subagents\|ToolGates{.*Subagents\|spawnAgentTool\b" internal pkg --include=*.go   # expect: no hits (all migrated)
git add -A && git commit -m "docs: subagent registry (shell3.subagent + tools.subagents array)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Self-review notes

- **Spec coverage:** separate `shell3.subagent` registry (T1) ✓; required `description` (T1 Step 4) ✓; nested-subagents load error (T1) ✓; name-collision + unknown-ref load errors (T1 Step 3) ✓; `tools.subagents` handle array, drop bool gate (T1 Step 5) ✓; single `spawn_agent` with enum + description (T2) ✓; no-subagents → no tool (T2 `SpawnToolDefs(nil)` + T3 only appends when non-empty) ✓; required `subagent` param (T2 + T4) ✓; spawn resolves from subagent registry, names stay out of `AgentNames` (T3 `SubagentRuntime`/`SessionOptions.Subagent`) ✓; depth-limit 1 retained (T3 strip + T1 nested error) ✓; scaffold example (T5) ✓; docs (T6) ✓.
- **Type consistency:** `Subagent` (luacfg), `SubagentInfo{Name,Description}` (luacfg, used by `SpawnToolDefs` + agentsetup), `Agent.Subagents []string`, `SessionOptions.Subagent`/`SessionOpts.Subagent string`, `chat.SpawnRequest.Subagent` — all consistent across tasks. The JSON arg key is `subagent` everywhere (tool schema in T2, handler in T4).
- **Compile ordering:** removing `ToolGates.Subagents` (T1) breaks `tooldefs.go`'s `if g.Subagents` until T2 — that's why T1+T2 are one commit (Step noted in T1 Step 7). Likewise `scaffold_test.go`/`boot_test.go` reference `Gates.Subagents` and won't compile until T5; the executor should expect `./internal/scaffold` + `./cmd/shell3` to be red between T1 and T5 and only gate the whole-repo `go test ./...` green at T5/T6. Per-package tests (`./internal/luacfg`, `./internal/agentsetup`, `./internal/chat`, `./pkg/shell3`) stay green at each task.
- **Open verification for the implementer:** the real luacfg test load helper name; where `shell3.*` functions are bound to the Lua table (to add `subagent`); whether `AgentRuntime` returns `chat.ActiveAgent` by value (yes, per the existing `stripSubagentTools` signature) so the shared `runtimeFromAgentLike` helper returns a value; the `agentsetup` test harness for building `Parts` from inline Lua.
- **Decision recorded:** subagent↔agent name collisions are rejected at load (single human-facing namespace, two internal registries) — matches the spec.
