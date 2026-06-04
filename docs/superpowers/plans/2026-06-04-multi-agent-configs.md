# Multi-agent configs with Tab switching — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let one `shell3.lua` register multiple agents (each = model + prompt + tools + guards + skills), switch the active one at runtime via **Tab** (when not busy) or **`/agent`**, show the active agent key in the status bar, and remove `/model` in favor of per-agent models.

**Architecture:** `luacfg.LoadedConfig` already holds shared `Models`/`Tools`/`Skills`; only `Agent` is singular. We turn it into an ordered `agents` collection with an active index and accessors. `agentsetup` gains a `buildActiveRuntime` that produces a `chat.ActiveAgent` bundle (persona + guard + model client), used at startup and on every switch. The TUI mutates `chat.Config` fields between turns (safe under the existing **busy-gate** invariant) and refreshes the status bar.

**Tech Stack:** Go, gopher-lua, custom terminal UI (`internal/patchapp`).

**Ordering rule:** Each task leaves `go build ./...` and `go test ./...` green. The engine change (luacfg + agentsetup) lands together in Task 2 because the two packages are compile-coupled; field removals are deferred to Task 5 so intermediate tasks stay green.

---

## File Structure

- `internal/chat/chat.go` — add `ActiveAgent` type + `Config.AgentNames`/`Config.SwitchAgent`; later remove `Config.Models`/`Config.SwitchModel`/`ModelInfo`.
- `internal/luacfg/luacfg.go` — `agents []Agent` + `activeIdx` + `Active`/`Agents`/`SwitchAgent`; `Load` validation + model fallback.
- `internal/luacfg/register.go` — `luaAgent` appends (dup-name error).
- `internal/luacfg/dispatch.go` — guard chain reads active agent.
- `internal/luacfg/persona.go` — prompt/skills read active agent.
- `internal/luacfg/multiagent_test.go` — new tests for the collection + accessors.
- `internal/agentsetup/agentsetup.go` — `buildActiveRuntime`, populate new fields, migrate `.Agent` → `.Active()`, store-open across all agents.
- `internal/patchapp/input.go` — parse Tab (`keyTab`).
- `internal/patchapp/app.go` — `onTab` field, `SetTab`, `SetMode`.
- `internal/patchapp/editor.go` — `keyTab` case, busy-gated.
- `internal/tui/interactive.go` — `/agent` command, Tab handler, remove `/model`.
- `internal/scaffold/defaults/shell3.lua` — add a second example agent.

---

## Task 1: chat — ActiveAgent type and Config fields (additive)

**Files:**
- Modify: `internal/chat/chat.go`
- Test: `internal/chat/chat_test.go` (or new `internal/chat/activeagent_test.go`)

- [ ] **Step 1: Write the failing test**

Create `internal/chat/activeagent_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chat/ -run TestConfigHasAgentSwitchingFields`
Expected: FAIL — `ActiveAgent`/`AgentNames`/`SwitchAgent` undefined.

- [ ] **Step 3: Add the type and fields**

In `internal/chat/chat.go`, add the `ActiveAgent` type next to `ActiveModel` (after line 45):

```go
// ActiveAgent is the full runtime bundle produced when switching agents:
// everything the chat loop needs to run the next turn under a different agent.
type ActiveAgent struct {
	Personality   persona.Persona
	ToolGuard     func(ctx context.Context, tool string, params map[string]any) (int, string, error)
	ModeLabel     string
	ActiveSkills  []string
	ActiveTools   []string
	LLM           LLMClient
	Params        llm.RequestParams
	ModelID       string
	ContextWindow int
}
```

In the `Config` struct, add these fields (after `SwitchModel`, line 118):

```go
	// AgentNames lists configured agents in declaration order, for /agent and
	// Tab cycling. Empty or single-element disables switching.
	AgentNames []string
	// SwitchAgent activates the agent with the given name and returns its full
	// runtime bundle. Nil disables agent switching.
	SwitchAgent func(name string) (ActiveAgent, error)
```

Confirm `internal/chat/chat.go` already imports `context`, `persona`, and `llm` (it does — `ToolGuard` and `Personality` already use them).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chat/ -run TestConfigHasAgentSwitchingFields`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/chat/chat.go internal/chat/activeagent_test.go
git commit -m "feat(chat): add ActiveAgent type and agent-switch Config fields"
```

---

## Task 2: luacfg + agentsetup — multi-agent engine

This is the core. luacfg turns `Agent` into an ordered collection; agentsetup builds a per-agent runtime and wires the switch closure. Both packages change together to stay compilable.

**Files:**
- Modify: `internal/luacfg/luacfg.go:55-110` (struct, Load, add accessors)
- Modify: `internal/luacfg/register.go:112-158` (`luaAgent` appends)
- Modify: `internal/luacfg/dispatch.go:21-22` (`OnToolCall` uses active)
- Modify: `internal/luacfg/persona.go:17-37` (`BuildPersona` uses active)
- Modify: `internal/agentsetup/agentsetup.go` (runtime builder + new fields)
- Test: `internal/luacfg/multiagent_test.go` (new)

### 2a. luacfg data model

- [ ] **Step 1: Write the failing test**

Create `internal/luacfg/multiagent_test.go`:

```go
package luacfg

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

const twoModelsHdr = `
shell3.model("opus",  { base_url="http://x", api_key="k", model="opus-id" })
shell3.model("haiku", { base_url="http://x", api_key="k", model="haiku-id" })
`

func TestMultipleAgentsAccumulateFirstActive(t *testing.T) {
	p := writeConfig(t, twoModelsHdr+`
shell3.agent({ name="build", model="opus",  prompt="b" })
shell3.agent({ name="plan",  model="haiku", prompt="p" })
`)
	c, err := Load(p, filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := c.Active().Name; got != "build" {
		t.Fatalf("active = %q, want build (first declared)", got)
	}
	names := []string{}
	for _, a := range c.Agents() {
		names = append(names, a.Name)
	}
	if len(names) != 2 || names[0] != "build" || names[1] != "plan" {
		t.Fatalf("agent order = %v, want [build plan]", names)
	}
}

func TestSwitchAgentByName(t *testing.T) {
	p := writeConfig(t, twoModelsHdr+`
shell3.agent({ name="build", model="opus",  prompt="b" })
shell3.agent({ name="plan",  model="haiku", prompt="p" })
`)
	c, _ := Load(p, filepath.Dir(p))
	defer c.Close()
	a, err := c.SwitchAgent("plan")
	if err != nil || a.Name != "plan" {
		t.Fatalf("SwitchAgent(plan) = %v, %v", a.Name, err)
	}
	if c.Active().Name != "plan" {
		t.Fatalf("active after switch = %q, want plan", c.Active().Name)
	}
	if _, err := c.SwitchAgent("nope"); err == nil {
		t.Fatal("SwitchAgent(nope) should error")
	}
	if c.Active().Name != "plan" {
		t.Fatal("failed switch must leave active unchanged")
	}
}

func TestDuplicateAgentNameErrors(t *testing.T) {
	p := writeConfig(t, twoModelsHdr+`
shell3.agent({ name="dup", model="opus", prompt="a" })
shell3.agent({ name="dup", model="opus", prompt="b" })
`)
	if _, err := Load(p, filepath.Dir(p)); err == nil {
		t.Fatal("duplicate agent name should error")
	}
}

func TestAgentModelDefaultsToFirstModel(t *testing.T) {
	p := writeConfig(t, twoModelsHdr+`
shell3.agent({ name="build", prompt="b" })
`)
	c, err := Load(p, filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := c.Active().ModelName; got != "opus" {
		t.Fatalf("model fallback = %q, want opus (first declared)", got)
	}
}

func TestSingleAgentBackCompat(t *testing.T) {
	p := writeConfig(t, twoModelsHdr+`
shell3.agent({ name="base", model="opus", prompt="x" })
`)
	c, err := Load(p, filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Active().Name != "base" || len(c.Agents()) != 1 {
		t.Fatalf("single-agent back-compat broken: %q / %d", c.Active().Name, len(c.Agents()))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/luacfg/ -run 'TestMultipleAgents|TestSwitchAgent|TestDuplicateAgent|TestAgentModelDefaults|TestSingleAgentBackCompat'`
Expected: FAIL — `Active`/`Agents`/`SwitchAgent` undefined; `Agent` field still singular.

- [ ] **Step 3: Change the struct and add accessors**

In `internal/luacfg/luacfg.go`, replace the `Agent Agent` field in `LoadedConfig` (line 58) with:

```go
	agents    []Agent
	activeIdx int
```

After the `Model` method (line 110), add:

```go
// Active returns the currently selected agent.
func (c *LoadedConfig) Active() Agent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.agents[c.activeIdx]
}

// Agents returns a copy of the registered agents in declaration order.
func (c *LoadedConfig) Agents() []Agent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Agent, len(c.agents))
	copy(out, c.agents)
	return out
}

// SwitchAgent sets the active agent by name. An unknown name returns an error
// and leaves the active agent unchanged.
func (c *LoadedConfig) SwitchAgent(name string) (Agent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, a := range c.agents {
		if a.Name == name {
			c.activeIdx = i
			return c.agents[i], nil
		}
	}
	return Agent{}, fmt.Errorf("unknown agent %q", name)
}
```

> Concurrency note: `Active`/`SwitchAgent` lock `c.mu` only around the slice/index access — they never touch the Lua VM, so they don't interact with the `vmLockHeld` invariant. No caller holds `c.mu` when calling them (the guard chain in `dispatch.go` acquires `c.mu` per-guard *after* reading the active agent — see step 5).

- [ ] **Step 4: Update `Load` validation and model fallback**

In `internal/luacfg/luacfg.go`, replace the post-`DoFile` validation block (lines 92-99) with:

```go
	if len(c.agents) == 0 {
		c.L.Close()
		return nil, fmt.Errorf("config: no shell3.agent declared")
	}
	for i := range c.agents {
		if c.agents[i].ModelName == "" {
			if len(c.Models) == 0 {
				c.L.Close()
				return nil, fmt.Errorf("config: agent %q has no model and no models are declared", c.agents[i].Name)
			}
			c.agents[i].ModelName = c.Models[0].Name
		}
		if _, ok := c.Model(c.agents[i].ModelName); !ok {
			c.L.Close()
			return nil, fmt.Errorf("config: agent %q references unknown model %q", c.agents[i].Name, c.agents[i].ModelName)
		}
	}
```

- [ ] **Step 5: Migrate `luaAgent`, `dispatch`, `persona` to the collection**

In `internal/luacfg/register.go`, replace the body of `luaAgent` (lines 117-157, i.e. everything between `checkKeys` and `return 0`) with:

```go
	a := Agent{
		Name:      optStr(opts, "name"),
		ModelName: optStr(opts, "model"),
		Prompt:    optStr(opts, "prompt"),
	}
	if a.Name == "" {
		L.RaiseError("agent: name is required")
	}
	for _, ex := range c.agents {
		if ex.Name == a.Name {
			L.RaiseError("agent %q: already declared (agent names must be unique)", a.Name)
		}
	}
	if sk, ok := opts.RawGetString("skills").(*lua.LTable); ok {
		a.Skills = handleNames(sk, "__skill")
	}
	if tt, ok := opts.RawGetString("tools").(*lua.LTable); ok {
		if err := checkKeys(tt, "agent.tools", toolGateKeys); err != nil {
			L.RaiseError("%s", err.Error())
		}
		a.Gates = ToolGates{
			Bash:             optBool(tt, "bash"),
			BashBg:           optBool(tt, "bash_bg"),
			ShellInteractive: optBool(tt, "shell_interactive"),
			Edit:             optBool(tt, "edit"),
			Memory:           optBool(tt, "memory"),
			History:          optBool(tt, "history"),
			Docs:             optBool(tt, "docs"),
		}
		if cu, ok := tt.RawGetString("custom").(*lua.LTable); ok {
			a.CustomTools = handleNames(cu, "__tool")
		}
		if tt.RawGetString("skill") == lua.LFalse {
			a.SkillsDisabled = true
		}
	}
	if g, ok := opts.RawGetString("on_tool_call").(*lua.LTable); ok {
		g.ForEach(func(_, v lua.LValue) {
			switch x := v.(type) {
			case *lua.LFunction:
				a.Guard = append(a.Guard, GuardEntry{fn: x})
			case *lua.LTable:
				if b, ok := x.RawGetString("__guard").(lua.LString); ok {
					a.Guard = append(a.Guard, GuardEntry{Builtin: string(b)})
				}
			}
		})
	}
	c.agents = append(c.agents, a)
	return 0
```

In `internal/luacfg/dispatch.go`, change `OnToolCall` (lines 21-22) so it reads the active agent's guards once at entry:

```go
func (c *LoadedConfig) OnToolCall(ctx context.Context, tool string, params map[string]any) (Decision, string, error) {
	for _, g := range c.Active().Guard {
```

In `internal/luacfg/persona.go`, replace the three `c.Agent` references (lines 19, 27, 29) by binding the active agent at the top of `BuildPersona`:

```go
func (c *LoadedConfig) BuildPersona(rd RuntimeData) string {
	a := c.Active()
	var b strings.Builder
	b.WriteString(a.Prompt)
```
and below, `if a.SkillsActive() {` and `for _, name := range a.Skills {`.

- [ ] **Step 6: Run luacfg tests**

Run: `go test ./internal/luacfg/`
Expected: PASS (new tests + existing). If existing tests reference `lc.Agent`, update them to `lc.Active()`.

Run: `git grep -n "\.Agent\b" -- 'internal/luacfg/*_test.go'` and fix any hits to `.Active()`.

### 2b. agentsetup runtime builder

- [ ] **Step 7: Migrate agentsetup to the active agent + runtime builder**

In `internal/agentsetup/agentsetup.go`:

Replace `resolveModel` (lines 143-158) — model enumeration and the initial client move into the runtime builder. Change it to validate-only (model existence is already validated in `Load`, but keep enumerating for the to-be-removed `/model`; we keep `cfg.Models` populated until Task 5 to stay green):

```go
// resolveModel enumerates models for the (soon-to-be-removed) /model command.
func (b *builder) resolveModel() error {
	for _, md := range b.lc.Models {
		b.models = append(b.models, chat.ModelInfo{
			Name:          md.Name,
			ModelID:       md.ModelID,
			ContextWindow: md.ContextWindow,
		})
	}
	return nil
}
```

Remove the now-unused `b.m`, `b.client`, `b.rp` fields from the `builder` struct (lines 55-57); keep `b.models`. (The compiler will flag remaining uses — they're all replaced below.)

In `openStore` (line 163), open the store if **any** agent gates memory/history:

```go
	needsStore := false
	for _, a := range b.lc.Agents() {
		if a.Gates.Memory || a.Gates.History {
			needsStore = true
		}
	}
	if needsStore {
		if s, e := store.Open(b.proj.DB); e == nil {
```

Add a runtime builder method (place after `assemble`):

```go
// buildActiveRuntime assembles the full chat runtime for the currently active
// agent: its model client, persona, tool defs, and guard closure. Called at
// startup and on every agent switch.
func (b *builder) buildActiveRuntime() (chat.ActiveAgent, error) {
	a := b.lc.Active()
	md, ok := b.lc.Model(a.ModelName)
	if !ok {
		return chat.ActiveAgent{}, fmt.Errorf("agent %q references unknown model %q", a.Name, a.ModelName)
	}
	client, rp := buildClient(md)

	customDefs := b.lc.CustomToolsFor(a.CustomTools)
	hasSkills := a.SkillsActive()
	toolDefs := luacfg.ToolDefs(a.Gates, customDefs, hasSkills)
	toolNames := make([]string, 0, len(toolDefs))
	for _, t := range toolDefs {
		toolNames = append(toolNames, t.Name)
	}

	prompt := b.lc.BuildPersona(luacfg.RuntimeData{
		Time:         time.Now().Format("Mon Jan 2 2006, 15:04 MST"),
		CWD:          b.opts.CWD,
		Model:        md.ModelID,
		CoreMemories: b.coreMemories,
	})

	return chat.ActiveAgent{
		Personality: persona.Persona{
			Name:         a.Name,
			SystemPrompt: prompt,
			Tools:        toolDefs,
			Parameters:   rp,
		},
		ToolGuard: func(ctx context.Context, t string, p map[string]any) (int, string, error) {
			d, r, e := b.lc.OnToolCall(ctx, t, p)
			return int(d), r, e
		},
		ModeLabel:     a.Name,
		ActiveSkills:  a.Skills,
		ActiveTools:   toolNames,
		LLM:           client,
		Params:        rp,
		ModelID:       md.ModelID,
		ContextWindow: md.ContextWindow,
	}, nil
}
```

Rewrite `assemble` (lines 182-259) to use the runtime builder, return an error, and populate the new fields. Replace the whole function with:

```go
// assemble renders the active agent's runtime and builds the final chat.Config,
// including the buildPrompt / switchAgent closures stored into it.
func (b *builder) assemble() (chat.Config, error) {
	// buildPrompt re-renders the active agent's system prompt with a fresh
	// timestamp. Used by /clear (cfg.RefreshPrompt) so a new conversation
	// re-stamps the clock against whatever agent is active at that moment.
	buildPrompt := func() string {
		a := b.lc.Active()
		md, _ := b.lc.Model(a.ModelName)
		return b.lc.BuildPersona(luacfg.RuntimeData{
			Time:         time.Now().Format("Mon Jan 2 2006, 15:04 MST"),
			CWD:          b.opts.CWD,
			Model:        md.ModelID,
			CoreMemories: b.coreMemories,
		})
	}
	switchModel := func(name string) (chat.ActiveModel, error) {
		md, ok := b.lc.Model(name)
		if !ok {
			return chat.ActiveModel{}, fmt.Errorf("unknown model %q", name)
		}
		cl, p := buildClient(md)
		return chat.ActiveModel{Client: cl, Params: p, ModelID: md.ModelID, ContextWindow: md.ContextWindow}, nil
	}
	switchAgent := func(name string) (chat.ActiveAgent, error) {
		if _, err := b.lc.SwitchAgent(name); err != nil {
			return chat.ActiveAgent{}, err
		}
		return b.buildActiveRuntime()
	}

	rt, err := b.buildActiveRuntime()
	if err != nil {
		return chat.Config{}, err
	}

	customNames := make(map[string]bool, len(b.lc.Active().CustomTools))
	for _, n := range b.lc.Active().CustomTools {
		customNames[n] = true
	}
	if b.lc.Active().SkillsActive() {
		customNames["skill"] = true
	}

	agentNames := make([]string, 0)
	for _, a := range b.lc.Agents() {
		agentNames = append(agentNames, a.Name)
	}

	return chat.Config{
		LLM:             rt.LLM,
		Store:           b.st,
		Personality:     rt.Personality,
		RefreshPrompt:   buildPrompt,
		WorkDir:         b.opts.CWD,
		StatusLine:      fmt.Sprintf("%s │ %s", rt.ModeLabel, rt.ModelID),
		ModeLabel:       rt.ModeLabel,
		ProjectRef:      b.uuid,
		ActiveSkills:    rt.ActiveSkills,
		ActiveTools:     rt.ActiveTools,
		ContextWindow:   rt.ContextWindow,
		Docs:            docs.Content,
		CustomTool:      b.lc.CallTool,
		CustomToolNames: customNames,
		ToolGuard:       rt.ToolGuard,
		Params:          rt.Params,
		Log:             b.log,
		OutPath:         b.opts.OutPath,
		Headless:        b.opts.Headless,
		Models:          b.models,
		SwitchModel:     switchModel,
		AgentNames:      agentNames,
		SwitchAgent:     switchAgent,
	}, nil
}
```

Update `Build` (line 82) to propagate the error:

```go
	cfg, err := b.assemble()
	if err != nil {
		b.closeAll()
		return chat.Config{}, noop, err
	}
	return cfg, b.closeAll, nil
```

(Remove the prior `return b.assemble(), b.closeAll, nil` line.)

- [ ] **Step 8: Build and test**

Run: `go build ./... && go test ./internal/luacfg/ ./internal/agentsetup/ ./internal/chat/`
Expected: PASS. Fix any remaining `b.m`/`b.client`/`b.rp` references in agentsetup (all are replaced by `buildActiveRuntime`).

- [ ] **Step 9: Commit**

```bash
git add internal/luacfg/ internal/agentsetup/agentsetup.go
git commit -m "feat(luacfg,agentsetup): register multiple agents with runtime switching"
```

---

## Task 3: patchapp — Tab key, SetMode, SetTab

**Files:**
- Modify: `internal/patchapp/input.go:34-83` (add `keyTab` + parse byte 9)
- Modify: `internal/patchapp/app.go` (field + setters)
- Modify: `internal/patchapp/editor.go:78` (case `keyTab`)
- Test: `internal/patchapp/input_test.go`, `internal/patchapp/app_test.go` (or new)

- [ ] **Step 1: Write the failing test**

Add to `internal/patchapp/input_test.go`:

```go
func TestParseTab(t *testing.T) {
	k, used := parseInput([]byte{9})
	if k.kind != keyTab || used != 1 {
		t.Fatalf("parseInput(Tab) = %v, %d; want keyTab, 1", k.kind, used)
	}
}
```

Create `internal/patchapp/setmode_test.go`:

```go
package patchapp

import "testing"

func TestSetModeUpdatesBadge(t *testing.T) {
	a := New("build", "status", WelcomeInfo{})
	a.SetMode("plan")
	a.mu.Lock()
	got := a.status.mode
	a.mu.Unlock()
	if got != "plan" {
		t.Fatalf("mode = %q, want plan", got)
	}
}

func TestSetTabRegistersHandler(t *testing.T) {
	a := New("build", "status", WelcomeInfo{})
	called := false
	a.SetTab(func() { called = true })
	if a.onTab == nil {
		t.Fatal("SetTab did not register handler")
	}
	a.onTab()
	if !called {
		t.Fatal("handler not invoked")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/patchapp/ -run 'TestParseTab|TestSetMode|TestSetTab'`
Expected: FAIL — `keyTab`, `SetMode`, `SetTab`, `onTab` undefined.

- [ ] **Step 3: Add `keyTab` and parse byte 9**

In `internal/patchapp/input.go`, add `keyTab` to the `keyKind` const block (after `keyEnter`, line 24):

```go
	keyEnter
	keyTab
```

In `parseInput`, after the backspace check (line 83), add:

```go
	if b == 9 {
		return parsedKey{kind: keyTab}, 1
	}
```

- [ ] **Step 4: Add the App field and setters**

In `internal/patchapp/app.go`, add to the `App` struct after `submit SubmitFunc` (line 109):

```go
	// onTab is fired when Tab is pressed while not busy. Nil = no-op.
	onTab func()
```

After `SetSubmit` (line 133), add:

```go
// SetTab registers the callback fired on Tab (ignored while busy).
func (a *App) SetTab(fn func()) { a.onTab = fn }

// SetMode updates the agent badge shown in the status bar. Goroutine-safe.
func (a *App) SetMode(name string) {
	a.mu.Lock()
	a.status.mode = name
	a.render()
	a.mu.Unlock()
}
```

- [ ] **Step 5: Handle `keyTab` in editor.go**

In `internal/patchapp/editor.go`, add a case in the `switch k.kind` block (after `case keyEnter:`, line 86). The handler is invoked **outside** `a.mu` (it calls `SetMode`/`SetStatus`, which lock), and only when not busy — preserving the busy-gate invariant that mutators don't run during a turn:

```go
		case keyTab:
			a.mu.Lock()
			busy := a.busy
			fn := a.onTab
			a.mu.Unlock()
			if !busy && fn != nil {
				fn()
			}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/patchapp/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/patchapp/
git commit -m "feat(patchapp): Tab key, SetTab handler, live SetMode badge"
```

---

## Task 4: TUI — /agent command, Tab handler, remove /model

**Files:**
- Modify: `internal/tui/interactive.go` (add `/agent`, Tab wiring, `applyAgent`; remove `/model` block at lines 630-666)

- [ ] **Step 1: Add the `applyAgent` helper and Tab wiring**

In `internal/tui/interactive.go`, just before `app.SetSubmit(...)` (line 207), add:

```go
	applyAgent := func(rt chat.ActiveAgent) {
		cfg.LLM = rt.LLM
		cfg.Personality = rt.Personality
		cfg.Params = rt.Params
		cfg.ToolGuard = rt.ToolGuard
		cfg.ModeLabel = rt.ModeLabel
		cfg.ActiveSkills = rt.ActiveSkills
		cfg.ActiveTools = rt.ActiveTools
		cfg.ContextWindow = rt.ContextWindow
		cfg.StatusLine = fmt.Sprintf("%s │ %s", rt.ModeLabel, rt.ModelID)
		app.SetMode(rt.ModeLabel)
		app.SetStatus(cfg.StatusLine)
		app.SetContextWindow(rt.ContextWindow)
	}

	app.SetTab(func() {
		if cfg.SwitchAgent == nil || len(cfg.AgentNames) < 2 {
			return
		}
		cur := 0
		for i, n := range cfg.AgentNames {
			if n == cfg.ModeLabel {
				cur = i
				break
			}
		}
		next := cfg.AgentNames[(cur+1)%len(cfg.AgentNames)]
		rt, err := cfg.SwitchAgent(next)
		if err != nil {
			return
		}
		applyAgent(rt)
		app.PrintLine(patchtui.Dim + "[agent: " + rt.ModeLabel + "]" + patchtui.Reset)
	})
```

> Safety: `SetTab`'s callback runs on the input-loop goroutine and only when `!busy` (gated in `editor.go`), exactly like slash handlers. It mutates `cfg` between turns, which is race-free under the documented busy-gate invariant (see `drainTurn` in this file).

- [ ] **Step 2: Replace the `/model` command with `/agent`**

In `internal/tui/interactive.go`, delete the entire `/model` `RegisterSlash` block (lines 630-666) and replace it with:

```go
	app.RegisterSlash(patchapp.SlashCommand{
		Name: "agent", Help: "/agent [name] — list agents or switch the active agent",
		Handler: func(args string) {
			if cfg.SwitchAgent == nil || len(cfg.AgentNames) == 0 {
				dim("[no agents configured]")
				return
			}
			name := strings.TrimSpace(args)
			if name == "" {
				lines := []string{patchtui.Bold + "agents:" + patchtui.Reset}
				for _, n := range cfg.AgentNames {
					marker := ""
					if n == cfg.ModeLabel {
						marker = patchtui.Dim + "  (active)" + patchtui.Reset
					}
					lines = append(lines, "  "+n+marker)
				}
				lines = append(lines, "", patchtui.Dim+"usage: /agent <name>"+patchtui.Reset)
				app.Print(lines)
				return
			}
			rt, err := cfg.SwitchAgent(name)
			if err != nil {
				dim(fmt.Sprintf("[%v]", err))
				return
			}
			applyAgent(rt)
			dim(fmt.Sprintf("[agent: %s]", rt.ModeLabel))
		},
	})
```

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: success. `cfg.Models`/`cfg.SwitchModel` are still populated by agentsetup but no longer read by the TUI — that's fine; they're removed in Task 5.

- [ ] **Step 4: Manual smoke test**

Create a scratch two-agent config and run the TUI:

```bash
cat > /tmp/ma-test.lua <<'LUA'
shell3.model("m", { base_url = shell3.env.secret("BASE_URL"), api_key = shell3.env.secret("API_KEY"), model = shell3.env.secret("MODEL_ID"), context_window = 200000 })
shell3.agent({ name = "build", model = "m", prompt = "You are build.", tools = { bash = true, edit = true } })
shell3.agent({ name = "plan",  model = "m", prompt = "You are plan. Propose, do not edit.", tools = { bash = true, edit = false } })
LUA
```

Run: `go run ./cmd/shell3 --config /tmp/ma-test.lua` (requires a real `.env` beside the config; if none available, skip the live run and rely on the integration test in Task 6).
Verify: status bar shows `build`; pressing **Tab** flips the badge to `plan` and prints `[agent: plan]`; `/agent` lists both with `(active)`; `/agent build` switches back; `/model` is gone.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/interactive.go
git commit -m "feat(tui): /agent command and Tab switching; remove /model"
```

---

## Task 5: Remove dead /model plumbing

Now that nothing reads them, delete the model-switch fields and type.

**Files:**
- Modify: `internal/chat/chat.go` (remove `Config.Models`, `Config.SwitchModel`, `ModelInfo`)
- Modify: `internal/agentsetup/agentsetup.go` (remove `resolveModel`, `b.models`, `switchModel`, `cfg.Models`, `cfg.SwitchModel`)

- [ ] **Step 1: Confirm no other readers**

Run: `git grep -n "SwitchModel\|\.Models\b\|ModelInfo" -- internal/ pkg/ cmd/`
Expected hits only in `chat.go` (definitions), `agentsetup.go` (population). If `pkg/shell3` or `cmd/` reads them, stop and migrate those readers first (out of plan scope — surface to the user).

- [ ] **Step 2: Remove from chat.Config**

In `internal/chat/chat.go`, delete the `Models []ModelInfo` field (line 115) and `SwitchModel func(...)` field (line 118), and delete the `ModelInfo` type definition (line 27 block).

- [ ] **Step 3: Remove from agentsetup**

In `internal/agentsetup/agentsetup.go`:
- Delete `resolveModel` entirely and its call in `Build` (line 77-80).
- Remove the `models []chat.ModelInfo` field from the `builder` struct.
- In `assemble`, delete the `switchModel` closure and the `Models:`/`SwitchModel:` entries in the returned `chat.Config`.

- [ ] **Step 4: Build and full test**

Run: `go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/chat/chat.go internal/agentsetup/agentsetup.go
git commit -m "refactor: remove /model plumbing now superseded by per-agent models"
```

---

## Task 6: Scaffold example + final verification

**Files:**
- Modify: `internal/scaffold/defaults/shell3.lua` (add a second agent)

- [ ] **Step 1: Inspect the scaffold's existing agent**

Run: `git grep -n "shell3.agent" internal/scaffold/defaults/shell3.lua`
Read the surrounding block (the single agent near lines 594-703) to learn the local names in scope (the prompt var, guard vars, model name).

- [ ] **Step 2: Add a second `plan` agent**

After the existing `shell3.agent({ ... })` call in `internal/scaffold/defaults/shell3.lua`, add a second agent reusing the in-scope locals (adjust names to match what step 1 found — e.g. if the model is `"base"` and the guard is `guard_dangerous`):

```lua
-- A read-only "plan" companion to the default agent. Switch with Tab or /agent.
-- Same model and prompt scaffolding, but edits are disabled so it investigates
-- and proposes rather than changing files.
shell3.agent({
  name  = "plan",
  model = "base",                 -- match the model name declared above
  prompt = base_prompt,           -- match the prompt local declared above
  tools = { bash = true, edit = false, memory = true, history = true, docs = true },
  on_tool_call = { guard_dangerous },  -- match the guard local declared above
})
```

If the existing agent uses inline (non-local) prompt/guard values, lift them into `local` variables first so both agents share them (DRY), then reference those locals from both `shell3.agent` calls.

- [ ] **Step 3: Verify the scaffold loads**

Run: `go test ./internal/scaffold/... ./internal/luacfg/...`
Expected: PASS. If there's a scaffold-load test, it now exercises two agents. If none exists, add one that loads the embedded default and asserts `len(c.Agents()) == 2`.

- [ ] **Step 4: Full build, test, vet**

Run: `go build ./... && go test ./... && go vet ./...`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/defaults/shell3.lua
git commit -m "feat(scaffold): ship a read-only plan agent alongside the default"
```

---

## Self-review notes (author)

- **Spec coverage:** additive `shell3.agent` (T2), first-wins active (T2 Load), per-agent model + fallback (T2), `/model` removal (T4/T5), Tab + `/agent` (T3/T4), keep-history (no history reset added — turns just read mutated `cfg`), status-bar badge (`SetMode`, T3/T4), duplicate-name error (T2), single-agent back-compat (T2 test), context-overflow-errors-naturally (no special handling — by omission). All covered.
- **Type consistency:** `chat.ActiveAgent` fields used identically in T2 (`buildActiveRuntime`) and T4 (`applyAgent`). `Active()`/`Agents()`/`SwitchAgent()` signatures consistent across luacfg, agentsetup, TUI.
- **Greenness:** `cfg.Models`/`SwitchModel` kept alive through T4, removed in T5 once unread — every task builds.
