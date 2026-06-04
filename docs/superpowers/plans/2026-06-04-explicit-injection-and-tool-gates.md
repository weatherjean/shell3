# Explicit prompt injection + gateable always-on tools — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `environment`, `core_memories`, `prune`, and `compact` opt-in agent flags (default off), so a `shell3.lua` can declare a pure-text agent (no engine-injected prompt blocks, no tools).

**Architecture:** Two engine-forced behaviors become gated. `luacfg.ToolGates` gains `Prune`/`Compact`; `luacfg.Agent` gains `Environment`/`CoreMemories`. `tooldefs.go` gates the two always-on tool schemas; `persona.go` gates the two prompt blocks. The scaffold's `base`/`plan` agents opt back in to preserve behavior. No agents exist in the wild, so flipping defaults now is safe.

**Tech Stack:** Go, gopher-lua.

**Ordering rule:** Each task leaves `go build ./...` and `go test ./...` green. Task 1 adds the fields (inert). Task 2 flips the tool gates and opts the scaffold back in together. Task 3 flips the prompt gates, opts the scaffold back in, and fixes the one affected test. Task 4 is docs + final verification.

---

## File Structure

- `internal/luacfg/luacfg.go` — `ToolGates` += `Prune`/`Compact`; `Agent` += `Environment`/`CoreMemories`.
- `internal/luacfg/register.go` — `agentKeys` += `environment`/`core_memories`; `toolGateKeys` += `prune`/`compact`; `luaAgent` parses them.
- `internal/luacfg/tooldefs.go` — `ToolDefs` gates prune/compact.
- `internal/luacfg/persona.go` — `BuildPersona` gates the Environment + Core-memories blocks.
- `internal/luacfg/injection_gates_test.go` — new: parsing + gating tests.
- `internal/luacfg/persona_test.go` — update the existing test agent to opt in.
- `internal/scaffold/defaults/shell3.lua` — `base`/`plan` agents opt in to all four flags.
- `internal/docs/shell3.md` — document the new keys.

---

## Task 1: luacfg data model + Lua parsing (inert)

Adds the fields and parses them. Nothing consumes them yet, so behavior is unchanged and everything stays green.

**Files:**
- Modify: `internal/luacfg/luacfg.go` (`ToolGates`, `Agent` structs)
- Modify: `internal/luacfg/register.go` (`agentKeys`, `toolGateKeys`, `luaAgent`)
- Test: create `internal/luacfg/injection_gates_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/luacfg/injection_gates_test.go`. It reuses `writeConfig` and `twoModelsHdr` already defined in `internal/luacfg/multiagent_test.go` (same package):

```go
package luacfg

import (
	"path/filepath"
	"testing"
)

func TestInjectionAndToolGatesParsed(t *testing.T) {
	p := writeConfig(t, twoModelsHdr+`
shell3.agent({
  name="full", model="opus", prompt="p",
  environment=true, core_memories=true,
  tools={ prune=true, compact=true },
})
shell3.agent({ name="bare", model="opus", prompt="p", tools={} })
`)
	c, err := Load(p, filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	agents := c.Agents()
	full, bare := agents[0], agents[1]

	if !full.Environment || !full.CoreMemories {
		t.Fatalf("full: Environment=%v CoreMemories=%v, want both true", full.Environment, full.CoreMemories)
	}
	if !full.Gates.Prune || !full.Gates.Compact {
		t.Fatalf("full: Prune=%v Compact=%v, want both true", full.Gates.Prune, full.Gates.Compact)
	}
	if bare.Environment || bare.CoreMemories || bare.Gates.Prune || bare.Gates.Compact {
		t.Fatalf("bare: expected all four flags false, got env=%v mem=%v prune=%v compact=%v",
			bare.Environment, bare.CoreMemories, bare.Gates.Prune, bare.Gates.Compact)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/luacfg/ -run TestInjectionAndToolGatesParsed`
Expected: FAIL — `Environment`/`CoreMemories`/`Gates.Prune`/`Gates.Compact` undefined, or `checkKeys` rejects the unknown keys.

- [ ] **Step 3: Add the struct fields**

In `internal/luacfg/luacfg.go`, extend `ToolGates`:

```go
type ToolGates struct {
	Bash, BashBg, ShellInteractive, Edit, Memory, History, Docs, Prune, Compact bool
}
```

And add two fields to `Agent` (place after `Prompt`):

```go
type Agent struct {
	Name, ModelName, Prompt string
	Environment             bool // inject the "## Environment" block
	CoreMemories            bool // inject the "## Core memories" block
	Gates                   ToolGates
	CustomTools             []string
	Skills                  []string
	SkillsDisabled          bool
	Guard                   []GuardEntry
}
```

- [ ] **Step 4: Register the new Lua keys and parse them**

In `internal/luacfg/register.go`, extend the key allowlists:

```go
var agentKeys = map[string]bool{
	"name": true, "model": true, "prompt": true, "tools": true, "skills": true,
	"on_tool_call": true, "environment": true, "core_memories": true,
}
```
```go
var toolGateKeys = map[string]bool{
	"bash": true, "bash_bg": true, "shell_interactive": true, "edit": true,
	"memory": true, "history": true, "docs": true, "custom": true, "skill": true,
	"prune": true, "compact": true,
}
```

In `luaAgent`, after the duplicate-name check and before the `skills` block, set the two prompt flags:

```go
	a.Environment = optBool(opts, "environment")
	a.CoreMemories = optBool(opts, "core_memories")
```

Inside the `if tt, ok := opts.RawGetString("tools")...` block, extend the `ToolGates` literal with the two new gates:

```go
		a.Gates = ToolGates{
			Bash:             optBool(tt, "bash"),
			BashBg:           optBool(tt, "bash_bg"),
			ShellInteractive: optBool(tt, "shell_interactive"),
			Edit:             optBool(tt, "edit"),
			Memory:           optBool(tt, "memory"),
			History:          optBool(tt, "history"),
			Docs:             optBool(tt, "docs"),
			Prune:            optBool(tt, "prune"),
			Compact:          optBool(tt, "compact"),
		}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/luacfg/ -run TestInjectionAndToolGatesParsed`
Expected: PASS

- [ ] **Step 6: Full build + test (nothing else should change)**

Run: `go build ./... && go test ./...`
Expected: PASS (the new fields are not consumed yet, so all existing behavior is unchanged).

- [ ] **Step 7: Commit**

```bash
git add internal/luacfg/luacfg.go internal/luacfg/register.go internal/luacfg/injection_gates_test.go
git commit -m "feat(luacfg): parse environment/core_memories agent flags and prune/compact tool gates"
```

---

## Task 2: Gate the always-on tools (prune/compact) + scaffold opt-in

Flip `prune_tool_result`/`compact_history` from hardcoded-on to gated. Update the scaffold's `base`/`plan` tool tables to opt back in, so the embedded config and integration tests keep those tools.

**Files:**
- Modify: `internal/luacfg/tooldefs.go` (`ToolDefs`)
- Modify: `internal/scaffold/defaults/shell3.lua` (base + plan `tools` tables)
- Test: add to `internal/luacfg/injection_gates_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/luacfg/injection_gates_test.go`:

```go
func TestToolDefsGatesPruneCompact(t *testing.T) {
	bare := ToolDefs(ToolGates{}, nil, false)
	if len(bare) != 0 {
		t.Fatalf("bare gates should yield 0 tool defs, got %d: %v", len(bare), bare)
	}

	with := ToolDefs(ToolGates{Prune: true, Compact: true}, nil, false)
	names := make(map[string]bool, len(with))
	for _, d := range with {
		names[d.Name] = true
	}
	if !names["prune_tool_result"] || !names["compact_history"] {
		t.Fatalf("Prune+Compact gates should expose both tools, got %v", names)
	}

	onlyPrune := ToolDefs(ToolGates{Prune: true}, nil, false)
	if len(onlyPrune) != 1 || onlyPrune[0].Name != "prune_tool_result" {
		t.Fatalf("Prune-only gate should yield exactly prune_tool_result, got %v", onlyPrune)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/luacfg/ -run TestToolDefsGatesPruneCompact`
Expected: FAIL — `ToolDefs(ToolGates{}, …)` currently returns 2 defs (prune+compact are hardcoded), so `len(bare) != 0`.

- [ ] **Step 3: Gate prune/compact in ToolDefs**

In `internal/luacfg/tooldefs.go`, change the start of `ToolDefs` from:

```go
func ToolDefs(g ToolGates, custom []CustomTool, hasSkills bool) []llm.ToolDefinition {
	defs := []llm.ToolDefinition{pruneToolResultTool, compactHistoryTool}
	if hasSkills {
		defs = append(defs, skillTool)
	}
```

to:

```go
func ToolDefs(g ToolGates, custom []CustomTool, hasSkills bool) []llm.ToolDefinition {
	defs := []llm.ToolDefinition{}
	if g.Prune {
		defs = append(defs, pruneToolResultTool)
	}
	if g.Compact {
		defs = append(defs, compactHistoryTool)
	}
	if hasSkills {
		defs = append(defs, skillTool)
	}
```

Leave the rest of the function unchanged. Also update the doc comment above `ToolDefs` to read: "the gated always-on tools (prune_tool_result, compact_history) when enabled, the skill tool when hasSkills is true, …".

- [ ] **Step 4: Opt the scaffold tools back in**

In `internal/scaffold/defaults/shell3.lua`, the **base** agent's `tools` table — add `prune` and `compact` right after `docs`:

```lua
  tools = {
    bash              = true,
    bash_bg           = true,
    shell_interactive = true,
    edit              = true,
    memory            = true,
    history           = true,
    docs              = true,
    prune             = true,
    compact           = true,
    custom            = { web_fetch, brave_search },
  },
```

And the **plan** agent's `tools` table — add the same two lines after `docs`:

```lua
  tools = {
    bash              = true,
    bash_bg           = false,
    shell_interactive = true,
    edit              = false,
    memory            = true,
    history           = true,
    docs              = true,
    prune             = true,
    compact           = true,
    custom            = { web_fetch, brave_search },
  },
```

- [ ] **Step 5: Run the gating test + full suite**

Run: `go test ./internal/luacfg/ -run TestToolDefsGatesPruneCompact`
Expected: PASS

Run: `go build ./... && go test ./...`
Expected: PASS. The existing `TestToolDefsIncludesSkill` (skill_tool_test.go) still passes — it only checks for the `skill` def by name, never asserts prune/compact. The chat/tui tests that mention `prune_tool_result`/`compact_history` test the *handler* and *renderer* by name, not the schema, so they are unaffected. If any test unexpectedly fails because it assumed prune/compact were always in the schema, fix it by passing `ToolGates{Prune: true, Compact: true}` in that test (do not weaken its real assertion).

- [ ] **Step 6: Commit**

```bash
git add internal/luacfg/tooldefs.go internal/scaffold/defaults/shell3.lua internal/luacfg/injection_gates_test.go
git commit -m "feat(luacfg): gate prune_tool_result/compact_history; scaffold opts in"
```

---

## Task 3: Gate the prompt blocks (environment/core_memories) + scaffold opt-in + fix test

Flip the `## Environment` and `## Core memories` blocks to gated. Opt the scaffold back in. Fix the one existing test that assumed the Environment block was unconditional.

**Files:**
- Modify: `internal/luacfg/persona.go` (`BuildPersona`)
- Modify: `internal/scaffold/defaults/shell3.lua` (base + plan agents: add `environment`/`core_memories`)
- Modify: `internal/luacfg/persona_test.go` (opt the test agent in)
- Test: add to `internal/luacfg/injection_gates_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/luacfg/injection_gates_test.go`:

```go
func TestBuildPersonaGatesBlocks(t *testing.T) {
	rd := RuntimeData{Time: "Mon", CWD: "/work", Model: "m-1",
		CoreMemories: []store.MemoryEntry{{Key: "k", Value: "v"}}}

	// Bare agent: no Environment block, no Core memories block.
	bare := &LoadedConfig{agents: []Agent{{Name: "bare", Prompt: "ONLY PROMPT"}}}
	got := bare.BuildPersona(rd)
	if got != "ONLY PROMPT" {
		t.Fatalf("bare persona should be the verbatim prompt only, got:\n%s", got)
	}

	// Opted-in agent: both blocks present.
	full := &LoadedConfig{agents: []Agent{{Name: "full", Prompt: "P", Environment: true, CoreMemories: true}}}
	gotFull := full.BuildPersona(rd)
	for _, want := range []string{"## Environment", "/work", "m-1", "## Core memories", "k: v"} {
		if !strings.Contains(gotFull, want) {
			t.Fatalf("opted-in persona missing %q:\n%s", want, gotFull)
		}
	}
}
```

This test references `store.MemoryEntry` and `strings`. Add the imports to the file's import block:

```go
import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/store"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/luacfg/ -run TestBuildPersonaGatesBlocks`
Expected: FAIL — `BuildPersona` currently always appends the Environment block, so the bare agent's prompt is not equal to "ONLY PROMPT".

- [ ] **Step 3: Gate the blocks in BuildPersona**

In `internal/luacfg/persona.go`, change `BuildPersona` so the two blocks are conditional:

```go
func (c *LoadedConfig) BuildPersona(rd RuntimeData) string {
	a := c.Active()
	var b strings.Builder
	b.WriteString(a.Prompt)
	if a.Environment {
		fmt.Fprintf(&b, "\n\n## Environment\n- Workdir: %s\n- Model: %s\n- Time: %s\n", rd.CWD, rd.Model, rd.Time)
	}
	if a.CoreMemories && len(rd.CoreMemories) > 0 {
		b.WriteString("\n## Core memories\n")
		for _, m := range rd.CoreMemories {
			fmt.Fprintf(&b, "- %s: %s\n", m.Key, m.Value)
		}
	}
	if a.SkillsActive() {
		b.WriteString("\n## Skills\nRead a skill body with the `skill` tool when it applies.\n")
		for _, name := range a.Skills {
			for _, s := range c.Skills {
				if s.Name == name {
					fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Description)
				}
			}
		}
	}
	return b.String()
}
```

(Only the two `if a.Environment` / `if a.CoreMemories && …` guards are new; the skills block is unchanged.)

- [ ] **Step 4: Fix the existing persona test**

In `internal/luacfg/persona_test.go`, the test agent must opt in to the Environment block (it asserts `/work` and `m-1` appear). Change the agent literal:

```go
	c := &LoadedConfig{
		agents: []Agent{{Name: "base", Prompt: "You are base.", Environment: true, Skills: []string{"web-search"}}},
		Skills: []Skill{{Name: "web-search", Description: "search the web", Body: "..."}},
	}
```

(Add `Environment: true`. The test uses no core memories, so `CoreMemories` is not needed here.)

- [ ] **Step 5: Opt the scaffold agents back in**

In `internal/scaffold/defaults/shell3.lua`, add `environment = true` and `core_memories = true` to BOTH the `base` and `plan` agents. For each, insert the two keys right after the `model = "main",` line (above `prompt =`):

base agent:
```lua
shell3.agent({
  name  = "base",
  model = "main",

  environment   = true,
  core_memories = true,

  prompt = base_prompt,
```

plan agent:
```lua
shell3.agent({
  name  = "plan",
  model = "main",

  environment   = true,
  core_memories = true,

  prompt = base_prompt,
```

- [ ] **Step 6: Run the new test, the fixed test, and the full suite**

Run: `go test ./internal/luacfg/ -run 'TestBuildPersonaGatesBlocks|TestBuildPersonaSystemPrompt'`
Expected: PASS

Run: `go build ./... && go test ./...`
Expected: PASS. If `internal/luacfg/skills_gate_test.go` fails because it asserted Environment content, opt its agent in the same way; if it only checks the skills block (it does), it needs no change.

- [ ] **Step 7: Commit**

```bash
git add internal/luacfg/persona.go internal/luacfg/persona_test.go internal/scaffold/defaults/shell3.lua internal/luacfg/injection_gates_test.go
git commit -m "feat(luacfg): gate Environment/Core-memories prompt blocks; scaffold opts in"
```

---

## Task 4: Docs + final verification

**Files:**
- Modify: `internal/docs/shell3.md` (`shell3.agent` reference)

- [ ] **Step 1: Document the new agent keys**

In `internal/docs/shell3.md`, find the `shell3.agent` key table (the rows for `name`, `model`, `prompt`, `tools`, `skills`, `on_tool_call`). Add two rows:

```
| `environment`   | bool   | inject the `## Environment` block (workdir/model/time); default off |
| `core_memories` | bool   | inject the `## Core memories` block; default off                    |
```

Then find the tool-gate documentation (the keys allowed inside `tools = { … }`) and add `prune` and `compact`. If there is a list/table of gate keys, add:

```
- `prune`   — expose `prune_tool_result` (default off)
- `compact` — expose `compact_history` (default off)
```

If the docs describe `prune_tool_result`/`compact_history` as "always-on", update that wording to "opt-in via the `prune` / `compact` tool gates". Add a one-line note near `shell3.agent`: "All injection and tool flags default off — declare a bare agent (`tools = {}`, no `environment`/`core_memories`) for pure text."

- [ ] **Step 2: Final verification**

Run:
```
go build ./...
go test ./...
go vet ./...
```
Expected: all PASS clean.

- [ ] **Step 3: Sanity-check a pure-text agent parses**

Run this throwaway check to confirm a bare agent yields no tools and a prompt-only system prompt:

```bash
cat > /tmp/pure.lua <<'LUA'
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="chat", model="m", prompt="PURE" })
LUA
go test ./internal/luacfg/ -run TestInjectionAndToolGatesParsed -count=1
```
(The unit tests already cover the behavior; this just confirms a minimal real file is valid Lua. Remove `/tmp/pure.lua` after.)

- [ ] **Step 4: Commit**

```bash
git add internal/docs/shell3.md
git commit -m "docs: document environment/core_memories agent keys and prune/compact tool gates"
```

---

## Self-review notes (author)

- **Spec coverage:** ToolGates Prune/Compact (T1) · Agent Environment/CoreMemories (T1) · Lua keys (T1) · ToolDefs gating (T2) · BuildPersona gating (T3) · scaffold opt-in (T2 tools, T3 prompt) · docs (T4) · tests for parsing + both gates (T1–T3). All spec sections covered.
- **Type consistency:** `ToolGates{…, Prune, Compact bool}`, `Agent.Environment`, `Agent.CoreMemories`, gate keys `prune`/`compact`, agent keys `environment`/`core_memories` used identically across tasks.
- **Greenness:** T1 fields inert → green. T2 flips tool gate + scaffold tools together → green. T3 flips prompt gate + scaffold prompt + fixes persona_test together → green. No task leaves a broken build.
