# Agent Runtime Phase 1: Runtime/Session Split — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split the heavyweight one-shot build into a long-lived `Runtime` (config/Lua/store/MCP/log, one per process) that hosts N cheap named `Session`s with per-session workdir, agent, headless flag, and audit sink — while `Start`/`Run` keep their exact public behavior as wrappers.

**Architecture:** Agent selection moves from process-global Lua state (`activeIdx`) to per-session data: `luacfg` gains name-parameterized lookups (`AgentByName`, `BuildPersonaFor`, `OnToolCallFor`) and loses `Active`/`SwitchAgent`. `agentsetup` splits into shared-parts assembly plus a per-agent runtime builder. `pkg/shell3.Runtime` holds a template `chat.Config` + closures; each `Session` copies the template and overrides its own fields, so existing Session internals (busy gate, route, sink) are untouched.

**Tech Stack:** Go 1.25, gopher-lua, fakellm for tests. Branch: `agent-runtime`. Spec: `docs/dev/superpowers/specs/2026-06-10-agent-runtime-design.md`.

**Conventions for every task:** run tests with `go test -race`, keep `gofmt`/`go vet` clean (`make lint`), one commit per task ending in a green suite. Existing behavior is pinned by the current tests — they must pass unmodified unless a task explicitly says it adapts them.

---

### Task 1: luacfg — agent selection becomes caller-supplied, not VM state

**Files:**
- Modify: `internal/luacfg/luacfg.go` (delete `activeIdx`, `Active()`, `SwitchAgent()`; add `AgentByName`)
- Modify: `internal/luacfg/persona.go` (`BuildPersona()` → `BuildPersonaFor(a Agent)`)
- Modify: `internal/luacfg/dispatch.go` (`OnToolCall` → `OnToolCallFor(a Agent, …)`)
- Modify: callers/tests that use the removed methods (find with the grep in Step 1)
- Test: `internal/luacfg/multiagent_test.go` (adapt), plus the new test below

- [ ] **Step 1: Survey current callers**

Run: `grep -rn "\.Active()\|\.SwitchAgent(\|BuildPersona()\|OnToolCall(" internal pkg --include="*.go"`
Expected callers: `internal/luacfg` (persona.go, dispatch.go, luacfg.go, tests), `internal/agentsetup/agentsetup.go` (builder), and luacfg tests. Note each — all are updated in this task or Task 2.

- [ ] **Step 2: Write the failing test**

Append to `internal/luacfg/multiagent_test.go`:

```go
// TestAgentByName_LookupAndMiss pins the name-parameterized agent lookup that
// replaces process-global active-agent state: sessions own their agent choice.
func TestAgentByName_LookupAndMiss(t *testing.T) {
	c := loadConfig(t, `
shell3.model("m", { base_url = "http://x", api_key = "k", model = "mm" })
shell3.agent({ name = "code", model = "m", prompt = "c" })
shell3.agent({ name = "plan", model = "m", prompt = "p" })
`)
	defer c.Close()

	a, ok := c.AgentByName("plan")
	if !ok || a.Name != "plan" || a.Prompt != "p" {
		t.Fatalf("AgentByName(plan) = %+v, %t", a, ok)
	}
	if _, ok := c.AgentByName("nope"); ok {
		t.Fatal("AgentByName(nope) should report ok=false")
	}
	// BuildPersonaFor renders the *given* agent, independent of any global.
	if got := c.BuildPersonaFor(a); got != "p" {
		t.Fatalf("BuildPersonaFor(plan) = %q, want %q", got, "p")
	}
}
```

(`loadConfig` is the existing test helper in this package that writes a temp shell3.lua and calls `Load`; if the helper has a different name in `helpers_test.go`, use that one — do not add a duplicate.)

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./internal/luacfg -run TestAgentByName -v`
Expected: FAIL — `c.AgentByName undefined`.

- [ ] **Step 4: Implement in luacfg.go**

In `internal/luacfg/luacfg.go`: delete the `activeIdx` field, the `Active()` and `SwitchAgent()` methods, and the comment block about Active/SwitchAgent busy-gating. Add:

```go
// AgentByName returns the declared agent with the given name. Agent selection
// is the caller's (per-session) state — the config holds only declarations.
func (c *LoadedConfig) AgentByName(name string) (Agent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range c.agents {
		if a.Name == name {
			return a, true
		}
	}
	return Agent{}, false
}

// FirstAgent returns the first declared agent (the default when a caller
// doesn't name one). Load guarantees at least one agent exists.
func (c *LoadedConfig) FirstAgent() Agent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.agents[0]
}
```

- [ ] **Step 5: Parameterize persona.go and dispatch.go**

`internal/luacfg/persona.go` — replace `BuildPersona`:

```go
// BuildPersonaFor renders the final system prompt for the given agent: the
// verbatim agent prompt followed by the engine-injected skills block (when
// skills are active).
func (c *LoadedConfig) BuildPersonaFor(a Agent) string {
	var b strings.Builder
	b.WriteString(a.Prompt)
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

`internal/luacfg/dispatch.go` — replace `OnToolCall`:

```go
// OnToolCallFor runs the given agent's guard chain in order; first non-allow
// short-circuits. The agent is passed in (not read from global state) so
// concurrent sessions with different active agents never race.
func (c *LoadedConfig) OnToolCallFor(a Agent, ctx context.Context, tool string, params map[string]any) (Decision, string, error) {
	for _, g := range a.Guard {
		d, reason, err := c.runLuaGuard(ctx, g.fn, tool, params)
		if err != nil {
			// Fail closed: a broken guard must block rather than silently
			// allow whatever it was meant to stop.
			return DecisionBlock, "guard execution error: " + err.Error(), nil
		}
		if d != DecisionAllow {
			return d, reason, nil
		}
	}
	return DecisionAllow, "", nil
}
```

- [ ] **Step 6: Fix luacfg-internal callers and tests**

Run: `go build ./internal/luacfg && go vet ./internal/luacfg`
Mechanical rewrites in luacfg tests (`multiagent_test.go`, `guard_test.go`, `persona_test.go`, `skills_gate_test.go`, others the compiler flags):
- `c.Active()` → `c.FirstAgent()` (or `c.AgentByName("x")` where a test switched first: `c.SwitchAgent("x")` followed by `c.Active()` becomes `a, _ := c.AgentByName("x")`).
- `c.BuildPersona()` → `c.BuildPersonaFor(a)` with the agent the test means.
- `c.OnToolCall(ctx, …)` → `c.OnToolCallFor(a, ctx, …)`.
Tests that asserted SwitchAgent error behavior ("unknown agent") move that assertion to `AgentByName` ok=false. Do NOT delete test intent — translate it.

`internal/agentsetup` will not compile until Task 2; that's expected. Verify only luacfg:

Run: `go test -race ./internal/luacfg`
Expected: PASS (including the new test).

- [ ] **Step 7: Commit**

```bash
git add internal/luacfg && git commit -m "refactor(luacfg): agent selection is caller state — AgentByName/BuildPersonaFor/OnToolCallFor replace global Active/SwitchAgent"
```
(The tree won't build whole-module until Task 2 lands; if you prefer atomic-green commits, fold this commit into Task 2's. Note which you did.)

---

### Task 2: agentsetup — shared Parts + per-agent runtime builder

**Files:**
- Modify: `internal/agentsetup/agentsetup.go`
- Test: `internal/agentsetup/agentsetup_test.go` (adapt + extend)

- [ ] **Step 1: Restructure the builder into Parts**

Replace the `builder`-returns-`chat.Config` flow with a two-level API. Keep `Options` and all existing stage methods (`resolvePaths`, `openLog`, `loadConfig`, `openStore`, `buildMCP`) — they become stages of `BuildParts`:

```go
// Parts is the session-independent runtime assembly: everything one process
// shares across N sessions. Front-ends derive per-session chat.Configs from it
// via SessionConfig.
type Parts struct {
	lc     *luacfg.LoadedConfig
	st     *store.Store
	mcpMgr *mcp.Manager
	proxy  *modelproxy.Spawner
	log    applog.Logger
	uuid   string
	root   string // runtime root workdir (Options.CWD)
}

// BuildParts assembles the shared runtime parts. The returned cleanup closes
// the store, Lua state, MCP servers, and log; callers MUST invoke it once.
func BuildParts(opts Options) (*Parts, func(), error) {
	b := &builder{opts: opts}
	noop := func() {}
	if err := b.resolvePaths(); err != nil {
		return nil, noop, err
	}
	b.openLog()
	b.proxy = modelproxy.New(b.l.Root, b.log)
	if err := b.loadConfig(); err != nil {
		b.closeAll()
		return nil, noop, err
	}
	b.openStore()
	b.buildMCP()
	p := &Parts{lc: b.lc, st: b.st, mcpMgr: b.mcpMgr, proxy: b.proxy,
		log: b.log, uuid: b.uuid, root: b.opts.CWD}
	return p, b.closeAll, nil
}
```

Accessors the pkg layer needs (keep them dumb):

```go
func (p *Parts) Store() *store.Store  { return p.st }
func (p *Parts) Log() applog.Logger   { return p.log }
func (p *Parts) ProjectRef() string   { return p.uuid }
func (p *Parts) Root() string         { return p.root }

// AgentNames returns declared agent names in declaration order.
func (p *Parts) AgentNames() []string {
	agents := p.lc.Agents()
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		names = append(names, a.Name)
	}
	return names
}

// CustomTool exposes the Lua custom-tool dispatcher.
func (p *Parts) CustomTool(ctx context.Context, name, args string) (string, error) {
	return p.lc.CallTool(ctx, name, args)
}

// MCPTool dispatches a prefixed MCP tool call; errors when no servers exist.
func (p *Parts) MCPTool(ctx context.Context, name, args string) (string, error) {
	if p.mcpMgr == nil {
		return "", fmt.Errorf("no MCP servers configured")
	}
	return p.mcpMgr.Dispatch(ctx, name, args)
}
```

- [ ] **Step 2: Per-agent runtime, parameterized by name**

Port `buildActiveRuntime` to Parts (same body, but the agent comes from the argument and persona/guards use the Task-1 APIs):

```go
// AgentRuntime assembles the full chat runtime bundle for the named agent
// ("" = first declared). Called by sessions at creation and on every switch;
// it reads only declared config, so concurrent sessions never contend.
func (p *Parts) AgentRuntime(name string) (chat.ActiveAgent, error) {
	var a luacfg.Agent
	if name == "" {
		a = p.lc.FirstAgent()
	} else {
		var ok bool
		if a, ok = p.lc.AgentByName(name); !ok {
			return chat.ActiveAgent{}, fmt.Errorf("unknown agent %q", name)
		}
	}
	md, ok := p.lc.Model(a.ModelName)
	if !ok {
		return chat.ActiveAgent{}, fmt.Errorf("agent %q references unknown model %q", a.Name, a.ModelName)
	}
	p.proxy.Ensure(md.Name, md.RunProxy)
	client, rp := buildClient(md)

	customDefs := p.lc.CustomToolsFor(a.CustomTools)
	hasSkills := a.SkillsActive()
	toolDefs := luacfg.ToolDefs(a.Gates, customDefs, hasSkills)
	toolNames := make([]string, 0, len(toolDefs))
	for _, t := range toolDefs {
		toolNames = append(toolNames, t.Name)
	}

	var mcpNames map[string]bool
	if p.mcpMgr != nil && len(a.MCPServerNames) > 0 {
		mcpDefs, err := p.mcpMgr.ToolDefinitionsFor(context.Background(), a.MCPServerNames)
		if err != nil {
			p.log.Warn("mcp: tool discovery failed; server tools unavailable", "error", err)
		} else {
			toolDefs = append(toolDefs, mcpDefs...)
			for _, d := range mcpDefs {
				toolNames = append(toolNames, d.Name)
			}
			mcpNames = p.mcpMgr.ToolNamesFor(a.MCPServerNames)
		}
	}

	customNames := make(map[string]bool, len(a.CustomTools))
	for _, n := range a.CustomTools {
		customNames[n] = true
	}
	if hasSkills {
		customNames["skill"] = true
	}

	agent := a // capture for the guard closure
	return chat.ActiveAgent{
		Personality: persona.Persona{
			Name:         a.Name,
			SystemPrompt: p.lc.BuildPersonaFor(a),
			Tools:        toolDefs,
			Parameters:   rp,
		},
		ToolGuard: func(ctx context.Context, t string, prm map[string]any) (int, string, error) {
			d, r, e := p.lc.OnToolCallFor(agent, ctx, t, prm)
			return int(d), r, e
		},
		ModeLabel:       a.Name,
		ActiveSkills:    a.Skills,
		ActiveTools:     toolNames,
		CustomToolNames: customNames,
		MCPToolNames:    mcpNames,
		LLM:             client,
		Params:          rp,
		ModelID:         md.ModelID,
		ContextWindow:   md.ContextWindow,
	}, nil
}

// RefreshPromptFor re-renders the named agent's system prompt (used by /clear).
func (p *Parts) RefreshPromptFor(name string) string {
	a, ok := p.lc.AgentByName(name)
	if !ok {
		a = p.lc.FirstAgent()
	}
	return p.lc.BuildPersonaFor(a)
}
```

- [ ] **Step 3: Rebuild Build as a wrapper (compat for tests/Start until Task 4 rewires)**

Replace the body of `Build` (keep its signature `Build(opts Options) (chat.Config, func(), error)`):

```go
// Build assembles a single-session chat.Config — the historical entry point,
// now a wrapper over BuildParts + SessionConfig. Multi-session hosts use
// BuildParts directly via pkg/shell3.Runtime.
func Build(opts Options) (chat.Config, func(), error) {
	parts, cleanup, err := BuildParts(opts)
	if err != nil {
		return chat.Config{}, cleanup, err
	}
	cfg, err := parts.SessionConfig(SessionOptions{
		Agent: opts.Agent, WorkDir: opts.CWD, Headless: opts.Headless, OutPath: opts.OutPath,
	})
	if err != nil {
		cleanup()
		return chat.Config{}, func() {}, err
	}
	return cfg, cleanup, nil
}
```

And the per-session derivation:

```go
// SessionOptions parameterizes one session derived from shared Parts.
type SessionOptions struct {
	Agent    string // "" → first declared
	WorkDir  string // "" → runtime root
	Headless bool
	OutPath  string
}

// SessionConfig derives a per-session chat.Config from the shared parts.
// The returned config embeds per-session closures (RefreshPrompt, SwitchAgent)
// that consult only declared config plus the session's own agent choice.
func (p *Parts) SessionConfig(so SessionOptions) (chat.Config, error) {
	workdir := so.WorkDir
	if workdir == "" {
		workdir = p.root
	}
	rt, err := p.AgentRuntime(so.Agent)
	if err != nil {
		return chat.Config{}, err
	}
	// activeName is the session's agent pointer, shared by the two closures
	// below; pkg/shell3.Session.SwitchAgent is documented single-threaded
	// (between turns), so a plain pointer is sufficient.
	activeName := rt.ModeLabel
	cfg := chat.Config{
		Store:      p.st,
		WorkDir:    workdir,
		ProjectRef: p.uuid,
		CustomTool: p.CustomTool,
		MCPTool:    p.MCPTool,
		Log:        p.log,
		OutPath:    so.OutPath,
		Headless:   so.Headless,
		AgentNames: p.AgentNames(),
		RefreshPrompt: func() string { return p.RefreshPromptFor(activeName) },
	}
	cfg.SwitchAgent = func(name string) (chat.ActiveAgent, error) {
		nrt, err := p.AgentRuntime(name)
		if err != nil {
			return chat.ActiveAgent{}, err
		}
		activeName = nrt.ModeLabel
		return nrt, nil
	}
	cfg.ApplyActiveAgent(rt)
	return cfg, nil
}
```

Delete the now-dead `builder.buildActiveRuntime` and `builder.assemble`.

- [ ] **Step 4: Write the multi-config independence test**

Append to `internal/agentsetup/agentsetup_test.go` (reuse the package's existing temp-config helper; it already writes a two-agent shell3.lua for the Agent-selection tests):

```go
// TestSessionConfigs_IndependentAgentSwitch pins the phase-1 invariant: two
// configs derived from one Parts hold independent agent state — switching one
// never changes the other (the old global activeIdx is gone).
func TestSessionConfigs_IndependentAgentSwitch(t *testing.T) {
	parts, cleanup, err := BuildParts(twoAgentOptions(t)) // helper: temp HOME + config declaring agents "code" and "plan"
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	a, err := parts.SessionConfig(SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := parts.SessionConfig(SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}

	rt, err := b.SwitchAgent("plan")
	if err != nil {
		t.Fatal(err)
	}
	b.ApplyActiveAgent(rt)

	if a.ModeLabel != "code" {
		t.Fatalf("config A's agent changed to %q when B switched", a.ModeLabel)
	}
	if b.ModeLabel != "plan" {
		t.Fatalf("config B should be plan, got %q", b.ModeLabel)
	}
	// RefreshPrompt follows each session's own agent.
	if a.RefreshPrompt() == b.RefreshPrompt() {
		t.Fatal("RefreshPrompt should render different prompts for different active agents")
	}
}
```

If no two-agent helper exists, write `twoAgentOptions(t)` in the test file: temp dir + `shell3.lua` with one model and agents `code` (prompt "c") / `plan` (prompt "p"), `Options{ConfigPath: path, CWD: dir, HomeDir: tempHome}` — copy the construction pattern from the existing `TestBuild…` tests in this file.

- [ ] **Step 5: Run agentsetup + luacfg tests**

Run: `go test -race ./internal/luacfg ./internal/agentsetup`
Expected: PASS. Then `go build ./...` — the whole module must compile again (pkg/shell3 and tui still use `Build`, untouched signature).

- [ ] **Step 6: Run the full suite**

Run: `make lint && go test -race ./...`
Expected: PASS everywhere — `Build`'s behavior is unchanged for single-session use.

- [ ] **Step 7: Commit**

```bash
git add -A && git commit -m "refactor(agentsetup): BuildParts + per-session SessionConfig — per-agent runtime no longer mutates global state"
```

---

### Task 3: pkg/shell3 — Runtime type hosting N sessions

**Files:**
- Create: `pkg/shell3/runtime.go`
- Modify: `pkg/shell3/shell3.go` (Session gains a back-pointer + ownsRuntime)
- Test: `pkg/shell3/runtime_test.go`

- [ ] **Step 1: Write the failing tests**

Create `pkg/shell3/runtime_test.go`:

```go
package shell3

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

// newTestRuntime builds a Runtime around fakellm-backed configs, bypassing
// agentsetup the same way newTestSession does for single sessions.
func newTestRuntime(t *testing.T, mk func() chat.Config) *Runtime {
	t.Helper()
	rt := &Runtime{
		sessionConfig: func(o SessionOpts) (chat.Config, error) {
			cfg := mk()
			cfg.Headless = o.Headless
			if o.WorkDir != "" {
				cfg.WorkDir = o.WorkDir
			}
			return cfg, nil
		},
		cleanup:  func() {},
		sessions: map[string]*Session{},
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func fakeCfg(text string) func() chat.Config {
	return func() chat.Config {
		return chat.Config{
			LLM: fakellm.New(
				fakellm.Script{Events: []llm.StreamEvent{{TextDelta: text}}},
				fakellm.Script{Events: []llm.StreamEvent{{TextDelta: text}}},
			),
			ModeLabel: "code",
		}
	}
}

// TestRuntime_SessionsAreIndependent pins the core phase-1 behavior: two named
// sessions on one runtime hold separate histories and separate busy gates.
func TestRuntime_SessionsAreIndependent(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("hi"))
	a, err := rt.Session(SessionOpts{Name: "tg:1"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := rt.Session(SessionOpts{Name: "web:1"})
	if err != nil {
		t.Fatal(err)
	}

	for range a.Send(context.Background(), "first") {
	}
	if len(a.History()) == 0 {
		t.Fatal("session a has no history after a turn")
	}
	if len(b.History()) != 0 {
		t.Fatalf("session b inherited a's history: %v", b.History())
	}
}

// TestRuntime_SessionNameReuseAndClose: same name returns the same session;
// closing a session removes it from the runtime without tearing it down.
func TestRuntime_SessionNameReuseAndClose(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	a, _ := rt.Session(SessionOpts{Name: "main"})
	again, _ := rt.Session(SessionOpts{Name: "main"})
	if a != again {
		t.Fatal("same name must return the same live session")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, _ := rt.Session(SessionOpts{Name: "main"})
	if fresh == a {
		t.Fatal("closed session must not be returned again")
	}
}

// TestRuntime_CloseClosesSessions: Runtime.Close closes remaining sessions
// then runs the shared cleanup exactly once.
func TestRuntime_CloseClosesSessions(t *testing.T) {
	cleanups := 0
	rt := newTestRuntime(t, fakeCfg("x"))
	rt.cleanup = func() { cleanups++ }
	_, _ = rt.Session(SessionOpts{Name: "a"})
	_, _ = rt.Session(SessionOpts{Name: "b"})
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if cleanups != 1 {
		t.Fatalf("shared cleanup ran %d times, want 1", cleanups)
	}
	if err := rt.Close(); err != nil {
		t.Fatal("second Close must be a no-op, not an error")
	}
	if cleanups != 1 {
		t.Fatalf("second Close re-ran cleanup (%d)", cleanups)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./pkg/shell3 -run TestRuntime -v`
Expected: FAIL — `undefined: Runtime`, `undefined: SessionOpts`.

- [ ] **Step 3: Implement Runtime**

Create `pkg/shell3/runtime.go`:

```go
package shell3

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/chat"
)

// RuntimeSpec configures a long-lived Runtime: the process-wide unit owning
// the config (Lua state), store, MCP servers, proxy spawner, and log.
type RuntimeSpec struct {
	ConfigPath string // "" → ./shell3.lua then ~/.shell3/shell3.lua
	WorkDir    string // runtime root; "" → os.Getwd(). Sessions default here.
}

// SessionOpts parameterizes one Session on a Runtime.
type SessionOpts struct {
	// Name keys the session on the runtime (e.g. "tg:1234"). "" gets a unique
	// generated name. Requesting an existing live name returns that session.
	Name string
	// Agent selects the initial agent ("" → first declared).
	Agent string
	// WorkDir roots tool execution for this session ("" → runtime root).
	WorkDir string
	// Headless strips shell_interactive and injects the headless reminder.
	Headless bool
	// OutPath, when non-empty, streams this session's JSONL audit log there.
	OutPath string
	// ShellInteractive runs an interactive shell command with TTY access.
	ShellInteractive func(ctx context.Context, cmd, workdir string) string
}

// Runtime hosts N sessions over one shared build. Create with NewRuntime,
// release with Close. Safe for concurrent Session calls.
type Runtime struct {
	// sessionConfig derives a per-session chat.Config; production wires
	// agentsetup.Parts.SessionConfig, tests inject fakes.
	sessionConfig func(SessionOpts) (chat.Config, error)
	cleanup       func()

	mu       sync.Mutex
	sessions map[string]*Session
	nextName int
	closed   bool
}

// NewRuntime loads the config and assembles the shared runtime parts.
// The Runtime must be Closed; sessions left open are closed by Close.
func NewRuntime(spec RuntimeSpec) (*Runtime, error) {
	workDir := spec.WorkDir
	if workDir == "" {
		w, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
		workDir = w
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}
	parts, cleanup, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigPath: spec.ConfigPath, CWD: workDir, HomeDir: homeDir,
	})
	if err != nil {
		return nil, err
	}
	return &Runtime{
		sessionConfig: func(o SessionOpts) (chat.Config, error) {
			return parts.SessionConfig(agentsetup.SessionOptions{
				Agent: o.Agent, WorkDir: o.WorkDir, Headless: o.Headless, OutPath: o.OutPath,
			})
		},
		cleanup:  cleanup,
		sessions: map[string]*Session{},
	}, nil
}

// Session returns the live session named opts.Name, or creates one.
func (rt *Runtime) Session(opts SessionOpts) (*Session, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.closed {
		return nil, fmt.Errorf("shell3: runtime is closed")
	}
	if opts.Name == "" {
		rt.nextName++
		opts.Name = fmt.Sprintf("s%d", rt.nextName)
	}
	if s, ok := rt.sessions[opts.Name]; ok {
		return s, nil
	}
	cfg, err := rt.sessionConfig(opts)
	if err != nil {
		return nil, err
	}
	s := newSession(cfg, func() {}) // shared parts are the runtime's to clean
	s.shellInteractive = opts.ShellInteractive
	s.runtime, s.name = rt, opts.Name

	sink, sinkCleanup, err := chat.OpenSink(opts.OutPath)
	if err != nil {
		return nil, err
	}
	s.sink, s.sinkCleanup = sink, sinkCleanup
	if sink != nil {
		_, model := chat.SplitStatus(cfg.StatusLine)
		sink.WriteStart("(session "+opts.Name+")", cfg.ModeLabel, model, cfg.OutPath, cfg.Headless)
	}
	rt.sessions[opts.Name] = s
	return s, nil
}

// forget removes a closed session from the registry (called by Session.Close).
func (rt *Runtime) forget(name string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	delete(rt.sessions, name)
}

// Close closes all live sessions, then the shared parts. Idempotent.
func (rt *Runtime) Close() error {
	rt.mu.Lock()
	if rt.closed {
		rt.mu.Unlock()
		return nil
	}
	rt.closed = true
	open := make([]*Session, 0, len(rt.sessions))
	for _, s := range rt.sessions {
		open = append(open, s)
	}
	rt.sessions = map[string]*Session{}
	rt.mu.Unlock()

	var firstErr error
	for _, s := range open {
		s.runtime = nil // already deregistered; avoid forget() on a held lock
		if err := s.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	rt.cleanup()
	return firstErr
}
```

In `pkg/shell3/shell3.go`, add to the `Session` struct (after `cleanup func()`):

```go
	// runtime/name link a runtime-hosted session back to its registry so
	// Close deregisters it; both are nil/"" for Start-created sessions.
	runtime *Runtime
	name    string
```

And at the end of `Session.Close`, just before `return endErr`:

```go
	if s.runtime != nil {
		s.runtime.forget(s.name)
	}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./pkg/shell3 -run TestRuntime -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Run the package + suite**

Run: `go test -race ./pkg/shell3 && make lint`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/shell3 && git commit -m "feat(pkg): Runtime hosts N named sessions over one shared build"
```

---

### Task 4: Start/Run become Runtime wrappers; per-session agent switching proven end-to-end

**Files:**
- Modify: `pkg/shell3/shell3.go` (`Start` body)
- Test: `pkg/shell3/runtime_test.go` (add switch-independence test), `test/lib_e2e_test.go` (extend if it exercises Start — check first)

- [ ] **Step 1: Write the failing test**

Append to `pkg/shell3/runtime_test.go`:

```go
// TestRuntime_AgentSwitchIsPerSession: switching agents in one session must
// not affect a sibling — the spec's core multi-session invariant.
func TestRuntime_AgentSwitchIsPerSession(t *testing.T) {
	mk := func() chat.Config {
		cfg := chat.Config{LLM: fakellm.New(), ModeLabel: "code", AgentNames: []string{"code", "plan"}}
		cfg.SwitchAgent = func(name string) (chat.ActiveAgent, error) {
			return chat.ActiveAgent{ModeLabel: name, LLM: fakellm.New()}, nil
		}
		return cfg
	}
	rt := newTestRuntime(t, mk)
	a, _ := rt.Session(SessionOpts{Name: "a"})
	b, _ := rt.Session(SessionOpts{Name: "b"})

	if err := b.SwitchAgent("plan"); err != nil {
		t.Fatal(err)
	}
	if got := a.ActiveAgent(); got != "code" {
		t.Fatalf("session a's agent changed to %q when b switched", got)
	}
	if got := b.ActiveAgent(); got != "plan" {
		t.Fatalf("session b should be plan, got %q", got)
	}
}
```

Run: `go test ./pkg/shell3 -run TestRuntime_AgentSwitch -v`
Expected: PASS already if Task 3 is correct (each session has its own cfg copy) — if it fails, the bug is a shared `chat.Config`; fix before proceeding. Either way the test now pins it.

- [ ] **Step 2: Rewire Start over NewRuntime**

Replace `Start`'s body in `pkg/shell3/shell3.go` (keep `Spec` and the signature; `Run` is untouched — it already calls `Start`):

```go
// Start loads the config, builds a single-session Runtime, and returns its one
// Session — the historical single-conversation entry point. Multi-session
// hosts use NewRuntime + Runtime.Session directly. Closing the returned
// Session also closes the underlying Runtime.
func Start(ctx context.Context, spec Spec) (*Session, error) {
	rt, err := NewRuntime(RuntimeSpec{ConfigPath: spec.ConfigPath, WorkDir: spec.WorkDir})
	if err != nil {
		return nil, err
	}
	s, err := rt.Session(SessionOpts{
		Name:             "main",
		Agent:            spec.Agent,
		Headless:         !spec.Interactive,
		OutPath:          spec.OutPath,
		ShellInteractive: spec.ShellInteractive,
	})
	if err != nil {
		rt.Close()
		return nil, err
	}
	s.ownsRuntime = true
	// Preserve the historical start-line label (prompt or "(interactive)").
	if s.sink != nil {
		label := spec.Prompt
		if label == "" {
			label = "(interactive)"
		}
		_, model := chat.SplitStatus(s.cfg.StatusLine)
		s.sink.WriteStart(label, s.cfg.ModeLabel, model, s.cfg.OutPath, s.cfg.Headless)
	}
	return s, nil
}
```

Add `ownsRuntime bool` to the Session struct next to `runtime`/`name`, and extend the Close hook added in Task 3:

```go
	if s.runtime != nil {
		rt := s.runtime
		s.runtime = nil
		rt.forget(s.name)
		if s.ownsRuntime {
			rt.cleanup() // session-owned runtime: shared parts die with it
		}
	}
```

Sink double-start: `Runtime.Session` already wrote a start line with the generic label. To keep the audit log byte-compatible for `Start` users, move the `WriteStart` out of `Runtime.Session` into a small method the wrapper controls:

```go
// in runtime.go, replace the inline sink-start block inside Session() with:
	s.sink, s.sinkCleanup = sink, sinkCleanup
	if sink != nil {
		s.writeStartLine("(session " + opts.Name + ")")
	}

// in shell3.go, on *Session:
func (s *Session) writeStartLine(label string) {
	_, model := chat.SplitStatus(s.cfg.StatusLine)
	s.sink.WriteStart(label, s.cfg.ModeLabel, model, s.cfg.OutPath, s.cfg.Headless)
}
```

…and in `Start`, instead of writing a second line, pass the label down: change `Runtime.Session` to accept the label via a package-private field on SessionOpts is over-engineering — simplest correct fix: `Start` opens the session with `OutPath: ""` and then opens the sink itself exactly as today (existing code block retained from the old Start). Choose this simplest path:

```go
	s, err := rt.Session(SessionOpts{
		Name: "main", Agent: spec.Agent, Headless: !spec.Interactive,
		ShellInteractive: spec.ShellInteractive,
		// OutPath deliberately empty: Start owns the sink for byte-compatible labels.
	})
	...
	sink, sinkCleanup, err := chat.OpenSink(spec.OutPath)
	if err != nil {
		rt.Close()
		return nil, err
	}
	s.sink, s.sinkCleanup = sink, sinkCleanup
	if sink != nil {
		label := spec.Prompt
		if label == "" {
			label = "(interactive)"
		}
		s.writeStartLine(label)
	}
```

(Then `Start` needs `cfg.OutPath` set for introspection: `s.cfg.OutPath = spec.OutPath` before opening the sink.)

- [ ] **Step 3: Run the full pkg + e2e suites**

Run: `go test -race ./pkg/shell3 ./test ./internal/tui ./cmd/shell3`
Expected: PASS — all existing Start/Run behavior tests (audit log lines, Close semantics, busy enforcement) green without modification. Any failure here means the wrapper broke compatibility; fix the wrapper, not the tests.

- [ ] **Step 4: Run everything**

Run: `make lint && go test -race ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "refactor(pkg): Start/Run are single-session wrappers over Runtime"
```

---

### Task 5: Per-session WorkDir, proven through tool execution

**Files:**
- Test: `pkg/shell3/runtime_test.go`

The plumbing already exists after Tasks 2–4 (`SessionOpts.WorkDir` → `SessionConfig` → `chat.Config.WorkDir` → `TurnConfig.WorkDir` → bash/edit handlers). This task pins it with a behavioral test so phase-5's `spawn_agent(workdir)` has a guaranteed substrate.

- [ ] **Step 1: Write the test**

Append to `pkg/shell3/runtime_test.go`:

```go
// TestRuntime_PerSessionWorkdir: a bash tool call runs in the session's own
// workdir, not the runtime root — the substrate for repo-rooted subagents.
func TestRuntime_PerSessionWorkdir(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	mk := func() chat.Config {
		return chat.Config{
			LLM: fakellm.New(
				fakellm.Script{Events: []llm.StreamEvent{
					{ToolCall: &llm.ToolCall{ID: "1", Name: "bash", RawArgs: `{"command":"pwd"}`}},
				}},
				fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "done"}}},
			),
			ModeLabel: "code",
			Personality: persona.Persona{Tools: []llm.ToolDefinition{{
				Name: "bash", Parameters: map[string]any{"type": "object"},
			}}},
		}
	}
	rt := newTestRuntime(t, mk)
	a, _ := rt.Session(SessionOpts{Name: "a", WorkDir: dirA})
	b, _ := rt.Session(SessionOpts{Name: "b", WorkDir: dirB})

	got := map[*Session]string{}
	for _, s := range []*Session{a, b} {
		for ev := range s.Send(context.Background(), "where am I?") {
			if ev.Kind == ToolResult && ev.ToolName == "bash" {
				got[s] = strings.TrimSpace(ev.ToolOutput)
			}
		}
	}
	// macOS tempdirs may resolve through /private; compare with EvalSymlinks.
	wantA, _ := filepath.EvalSymlinks(dirA)
	wantB, _ := filepath.EvalSymlinks(dirB)
	gotA, _ := filepath.EvalSymlinks(got[a])
	gotB, _ := filepath.EvalSymlinks(got[b])
	if gotA != wantA || gotB != wantB {
		t.Fatalf("bash cwd: a=%q (want %q) b=%q (want %q)", gotA, wantA, gotB, wantB)
	}
}
```

Add imports `path/filepath`, `strings`, and `github.com/weatherjean/shell3/internal/persona` to the test file.

- [ ] **Step 2: Run it**

Run: `go test -race ./pkg/shell3 -run TestRuntime_PerSessionWorkdir -v`
Expected: PASS via the newTestRuntime override (`cfg.WorkDir = o.WorkDir`). If it fails, the gap is in `newTestRuntime`'s config plumbing or `NewHandlers`/`TurnConfig` wiring — fix the production path, not the test.

- [ ] **Step 3: Commit**

```bash
git add pkg/shell3 && git commit -m "test(pkg): pin per-session workdir through real bash tool execution"
```

---

### Task 6: TUI/CLI port audit, docs, and phase close-out

**Files:**
- Modify: `pkg/shell3/example_test.go` (add a Runtime example)
- Modify: `CHANGELOG.md`
- Verify only: `internal/tui`, `cmd/shell3`

- [ ] **Step 1: Confirm the TUI needed zero changes**

Run: `grep -rn "shell3.Start\|shell3.Run(" internal/tui cmd/shell3 | grep -v _test`
Expected: only `tui.RunOnce` (via `shell3.Run`) and `tui.RunInteractive` (via `shell3.Start`) — both wrappers, behavior pinned by their tests in Task 4. If anything else turns up, port it the same way.

- [ ] **Step 2: Add the multi-session example**

Append to `pkg/shell3/example_test.go`:

```go
// ExampleNewRuntime shows the always-on host shape: one Runtime rooted at an
// agent home, multiple named sessions (e.g. one per Telegram chat), each with
// its own history, agent, and optional workdir.
func ExampleNewRuntime() {
	rt, err := shell3.NewRuntime(shell3.RuntimeSpec{WorkDir: "/home/me/assistant"})
	if err != nil {
		log.Fatal(err)
	}
	defer rt.Close()

	chat1, err := rt.Session(shell3.SessionOpts{Name: "tg:1234", Headless: true})
	if err != nil {
		log.Fatal(err)
	}
	for ev := range chat1.Send(context.Background(), "good morning") {
		if ev.Kind == shell3.Token {
			fmt.Print(ev.Text)
		}
	}

	// A second session rooted in a repo behaves like a normal coding session.
	coder, err := rt.Session(shell3.SessionOpts{Name: "job:fix-ci", WorkDir: "/home/me/src/myrepo", Headless: true})
	if err != nil {
		log.Fatal(err)
	}
	for range coder.Send(context.Background(), "make the tests pass") {
	}
}
```

- [ ] **Step 3: CHANGELOG entry**

Add under `## [Unreleased] / ### Added` in `CHANGELOG.md`:

```markdown
- `pkg/shell3.Runtime`: one shared build (config, store, MCP, log) hosting
  multiple named sessions with per-session agent, workdir, headless flag,
  and audit log. `Start`/`Run` are now thin single-session wrappers.
```

- [ ] **Step 4: Full verification**

Run: `make lint && go test -race ./... && make build && ./shell3 --version`
Expected: all green; version prints.

- [ ] **Step 5: Manual smoke (no config deletion yet — that's phase 6's acceptance)**

Run: `./shell3 "say hi" 2>&1 | head -5` against your existing `~/.shell3` config.
Expected: a normal one-shot reply; no behavior change.

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "docs(pkg): Runtime example + changelog; phase 1 complete"
```

---

## Self-review notes

- **Spec coverage (phase 1 scope only):** Runtime/Session split ✓ (Tasks 2–4), per-session workdir ✓ (Task 5), TUI unchanged ✓ (Task 4 Step 3, Task 6 Step 1), per-session agent state ✓ (Tasks 1–2, pinned in Task 4 Step 1). Inbox/ask/media/subagents/scaffold are phases 2–6 with their own plans.
- **Known judgment calls for the implementer:** (a) Task 1's commit may not build module-wide — folding into Task 2's commit is acceptable; note it. (b) `Start`'s sink ownership keeps audit logs byte-compatible by leaving `SessionOpts.OutPath` empty and owning the sink in the wrapper — don't "simplify" this, two start-lines is a regression. (c) If `agentsetup_test.go` lacks a two-agent helper, build one from the existing test patterns in that file rather than inventing new scaffolding.
