# Config Hot Reload (`/reload`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Re-read `shell3.lua` and apply the new config to the running `shell3 telegram` host without restarting the process — triggered by a `/reload` command and an agent-callable `reload` tool, validate-first (a bad edit never bricks the bot) and applied only at an idle boundary (full rebuild, no mid-turn swap).

**Architecture:** One new engine method, `pkg/shell3.Runtime.Reload`, rebuilds the shared `Parts` from the file (which runs all validation), swaps the Runtime's swappable fields, and re-derives each live `Session`'s `chat.Config` + handlers **in place** (keeping the same `*Runtime`/`*Session` objects and the session's history `s.sess`). The host (`cmd/shell3/telegram.go` + `internal/telegram`) owns *when* reload runs, re-decorates the session with its host tools/approver afterward, swaps the cron scheduler, and exposes the `/reload` command + `reload` tool. A `self-evolve` skill documents the edit→reload loop.

**Tech Stack:** Go, `pkg/shell3.Runtime` (existing), `internal/agentsetup` (existing), `gopher-lua` (existing). No new dependencies.

**Source of truth:** `docs/dev/superpowers/specs/2026-06-11-config-hot-reload-design.md`. Signatures below are copied verbatim from current code (verified 2026-06-11).

**Build approach:** Task 1 (`Runtime.Reload`, the only engine change) gates everything; do it first. Then Task 2 (host coordinator + `/reload` + scheduler swap) and Task 3 (`reload` tool + deferred apply) both touch `internal/telegram/bot.go` + `cmd/shell3/telegram.go`, so **serialize 2 then 3**. Task 4 (skill + docs) is disjoint and can run in parallel with 2/3. Task 5 is the integration test + verification sweep. After each batch the orchestrator verifies with `go build ./... && go vet ./... && gofmt -l . && go test -race ./...`.

**Key verified internals (do not guess these):**
- `RuntimeSpec{ConfigPath string, WorkDir string}` (runtime.go:47). `NewRuntime` captures `workDir` (`spec.WorkDir` or `os.Getwd()`) and `homeDir` (`os.UserHomeDir()`), then calls `agentsetup.BuildParts(agentsetup.Options{ConfigPath: spec.ConfigPath, CWD: workDir, HomeDir: homeDir})` → `(parts *agentsetup.Parts, cleanup func(), err error)` (runtime.go:159-172).
- `Runtime` fields (runtime.go ~100-130), all swappable: `sessionConfig func(SessionOpts) (chat.Config, error)`, `cleanup func()`, `store *store.Store`, `telegram TelegramConfig`, `cron []CronJob`, `workDir string`, `mu sync.Mutex`, `sessions map[string]*Session`, `closed bool`, `wg sync.WaitGroup`, `ctx context.Context`, `cancel context.CancelFunc`, `events chan HostEvent`.
- `parts.SessionConfig(agentsetup.SessionOptions{Agent, Subagent, WorkDir, Headless, OutPath, DisableSubagents})` builds a per-session `chat.Config`. `rt.sessionConfig` is the closure wrapping it (runtime.go:186-191). `parts.Cron() []luacfg.CronJob`, `parts.Telegram() luacfg.TelegramConfig`, `parts.Store() *store.Store`.
- `Runtime.Session(opts SessionOpts)` does `cfg, err := rt.sessionConfig(opts)` then `newSession(cfg, ...)` (runtime.go:347-357). The session does **not** currently store its opts — Task 1 adds an `opts SessionOpts` field.
- `Session` fields (shell3.go ~290-310): `cfg chat.Config`, `handlers` (built by `chat.NewHandlers(cfg)`), `sess *chat.Session` (owns history + store id), `name string`, `runtime *Runtime`, `subs subRegistry`.
- `newSession(cfg chat.Config, cleanup func()) *Session` builds `handlers: chat.NewHandlers(cfg)` and `s.sess = chat.NewSession(...)` keyed by `cfg.Store`'s `StartSession()` id (shell3.go:379-405). History lives in `s.sess`; keep it across reload.
- Session methods: `isBusy() bool` (shell3.go:690), `ActiveAgent() string` returns `s.cfg.ModeLabel` (shell3.go:939), `AgentNames() []string` returns `s.cfg.AgentNames` (shell3.go:936), `SwitchAgent(name string) error` (shell3.go:917), `Snapshot() Snapshot` (shell3.go:980, `.Params` is `[]ParamInfo{Name, Value, Default, Enum}`), `SetParam(name, value string) error` (shell3.go:1093), `RegisterHostTool(t HostTool) error` (shell3.go ~840), `SetApprover(fn) error`.
- **GOTCHA:** `RegisterHostTool` and `SetApprover` mutate `s.cfg` (append to `Personality.Tools`, set `CustomTool`/`Approve`). Re-deriving `s.cfg` on reload **drops them**. The host must re-apply them after `Reload` (Task 2's `decorateSession`). This mirrors today's order: `newSession` builds handlers, *then* `NewBot` calls `registerSendTool`/`SetApprover` — host tools registered after handler-build still take effect because each turn reads tools from `s.cfg` via `turnConfig()`.
- Host wiring (telegram.go): `rt`, `sess := rt.Session(SessionOpts{Name:"telegram", Agent: tg.Agent, WorkDir: tg.WorkDir})`, `b := telegram.NewBot(client, rt, sess, chatID, tg.Dashboard.URL)`, `sched` (cron), optional `srv := web.NewServer(...)`. `NewBot` calls `sess.SetApprover(b.approve)` + `b.registerSendTool()`.
- Bot: `handleMsg` runs a turn synchronously: `reply := b.drainTurn(b.sess.Send(turnCtx, text))` then `b.sendReply(ctx, reply)` (bot.go ~135-150). `handleCommand` switch (commands.go:26) + `BotCommands()` (commands.go:12). `b.sess.RegisterHostTool(shell3.HostTool{Name, Description, Parameters, Handler})` is the tool-registration pattern (sendtool.go:20).

---

## Task 1: `Runtime.Reload` — validate-first, idle-gated full rebuild (engine)

**Files:**
- Create: `pkg/shell3/reload.go`
- Create: `pkg/shell3/reload_test.go`
- Modify: `pkg/shell3/runtime.go` (add `configPath`/`homeDir` capture + `opts` field plumbing)
- Modify: `pkg/shell3/shell3.go` (add `opts SessionOpts` field to `Session`, set in `Session()`)

This is the only engine change. It rebuilds `Parts` from the file (which validates everything), swaps the Runtime's fields, and re-derives live sessions in place.

- [ ] **Step 1: Add the `opts` field to `Session` and capture it.** In `pkg/shell3/shell3.go`, add a field to the `Session` struct (near `name string`):

```go
	opts SessionOpts // the SessionOpts this session was built from (for reload re-derivation)
```

In `pkg/shell3/runtime.go`, in `Runtime.Session(opts SessionOpts)`, after `newSession(...)` returns the session `s` (and before it is stored/returned), set:

```go
	s.opts = opts
```

(If `Session()` returns an existing live session for a known name, leave its `opts` as originally captured — do not overwrite.)

- [ ] **Step 2: Capture reload inputs in `Runtime`.** In `runtime.go`, add fields to the `Runtime` struct:

```go
	configPath string // captured from RuntimeSpec for Reload
	homeDir    string // captured for Reload's BuildParts
```

In `NewRuntime`, set them in the returned `&Runtime{...}` literal:

```go
		configPath: spec.ConfigPath,
		homeDir:    homeDir,
```

- [ ] **Step 3: Write the failing tests** in `pkg/shell3/reload_test.go`. These use a real Lua config on disk (not the fake runtime) so `Reload` exercises the true `BuildParts` path.

```go
package shell3_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/pkg/shell3"
)

func writeCfg(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const baseCfg = `
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
local explorer = shell3.subagent({ name="explorer", model="main", description="d", prompt="p", tools={} })
shell3.agent({ name="code", model="main", prompt="hi", tools={ subagents={explorer} } })
`

func TestReload_AddAgentTakesEffect(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, baseCfg)
	rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: path, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, err := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	// Add a second agent + a cron job to the file, then reload.
	writeCfg(t, dir, baseCfg+`
shell3.agent({ name="research", model="main", prompt="research", tools={} })
shell3.cron({ jobs = { { name="n", schedule="@daily", agent="explorer", prompt="go" } } })
`)
	res, err := rt.Reload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	// New agent is now selectable on the SAME live session object.
	if err := sess.SwitchAgent("research"); err != nil {
		t.Fatalf("new agent not live after reload: %v", err)
	}
	// New cron job is visible via Runtime.Cron().
	if jobs := rt.Cron(); len(jobs) != 1 || jobs[0].Name != "n" {
		t.Fatalf("cron not reloaded: %+v", jobs)
	}
	if res.Agents < 2 || res.Jobs != 1 {
		t.Fatalf("bad reload result: %+v", res)
	}
}

func TestReload_InvalidKeepsOldConfig(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, baseCfg)
	rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: path, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, _ := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	// Write a config that fails cross-ref validation (cron references a ghost agent).
	writeCfg(t, dir, baseCfg+`
shell3.cron({ jobs = { { schedule="@daily", agent="ghost", prompt="x" } } })
`)
	if _, err := rt.Reload(); err == nil {
		t.Fatal("expected reload to reject the invalid config")
	}
	// Old config is intact: the original "code" agent still resolves.
	if err := sess.SwitchAgent("code"); err != nil {
		t.Fatalf("old config broken after failed reload: %v", err)
	}
	if jobs := rt.Cron(); len(jobs) != 0 {
		t.Fatalf("failed reload must not arm jobs: %+v", jobs)
	}
}

func TestReload_RestoresAgentAndParams(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, baseCfg+`
shell3.agent({ name="research", model="main", prompt="research", tools={} })
`)
	rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: path, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, _ := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err := sess.SwitchAgent("research"); err != nil {
		t.Fatal(err)
	}
	// Reload an unrelated change (no agent removed).
	writeCfg(t, dir, baseCfg+`
shell3.agent({ name="research", model="main", prompt="research v2", tools={} })
`)
	if _, err := rt.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := sess.ActiveAgent(); got != "research" {
		t.Fatalf("active agent not preserved across reload: got %q", got)
	}
}

func TestReload_DeletedActiveAgentFallsBack(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, baseCfg+`
shell3.agent({ name="research", model="main", prompt="research", tools={} })
`)
	rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: path, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, _ := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err := sess.SwitchAgent("research"); err != nil {
		t.Fatal(err)
	}
	// New file deletes "research"; reload must fall back gracefully (no error).
	writeCfg(t, dir, baseCfg)
	res, err := rt.Reload()
	if err != nil {
		t.Fatalf("reload should not error on deleted active agent: %v", err)
	}
	if got := sess.ActiveAgent(); got == "research" {
		t.Fatal("deleted agent should not remain active")
	}
	if !strings.Contains(strings.Join(res.Notes, " "), "research") {
		t.Fatalf("expected a note about the dropped agent, got %+v", res.Notes)
	}
}
```

- [ ] **Step 4: Run to verify failure**

Run: `go test ./pkg/shell3/ -run TestReload -v`
Expected: FAIL — `rt.Reload undefined`, `ReloadResult` fields undefined.

- [ ] **Step 5: Implement** `pkg/shell3/reload.go`

```go
package shell3

import (
	"fmt"
	"os"

	"github.com/weatherjean/shell3/internal/agentsetup"
)

// ReloadResult summarizes a successful reload, for the host's reply + log.
type ReloadResult struct {
	Agents int      // number of agents now live
	Models int      // number of models now live
	Jobs   int      // number of cron jobs now armed
	MCP    int      // number of MCP servers now configured
	Notes  []string // human-readable notes (e.g. dropped override, MCP restart)
}

// Reload re-reads the config file the Runtime was built from and applies it to
// the running Runtime WITHOUT restarting the process. It is the host-side entry
// for self-reconfiguration (the /reload command and the agent reload tool).
//
// Contract:
//   - Validate first: a new Parts is built from the file (BuildParts → luacfg
//     validation). On ANY error the new Parts is discarded and the running
//     config is left untouched — Reload returns the error and changes nothing.
//   - Idle only: the CALLER must ensure no live session has a turn in flight
//     (the host gates on Session.isBusy). Reload holds rt.mu so it serializes
//     against Session()/Close().
//   - Full rebuild: the old cleanup() runs (closing the old VM, MCP servers,
//     proxies, store handle) and every swappable Runtime field is replaced.
//   - In place: live sessions keep their identity and history (s.sess); only
//     s.cfg + s.handlers are rebuilt. Active agent + /set params are restored
//     best-effort. Host-registered tools/approver are NOT restored here — the
//     host re-applies them after Reload returns (they are not engine state).
func (rt *Runtime) Reload() (ReloadResult, error) {
	// 1. Build + validate the new parts BEFORE touching anything.
	homeDir := rt.homeDir
	if homeDir == "" {
		homeDir, _ = os.UserHomeDir()
	}
	newParts, newCleanup, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigPath: rt.configPath, CWD: rt.workDir, HomeDir: homeDir,
	})
	if err != nil {
		if newCleanup != nil {
			newCleanup() // release anything the partial build opened
		}
		return ReloadResult{}, fmt.Errorf("reload: %w", err)
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.closed {
		newCleanup()
		return ReloadResult{}, fmt.Errorf("reload: runtime is closed")
	}

	// 2. Capture per-session overrides to restore after the swap.
	type override struct {
		s      *Session
		agent  string
		params map[string]string
	}
	var ovs []override
	for _, s := range rt.sessions {
		ov := override{s: s, agent: s.ActiveAgent(), params: map[string]string{}}
		for _, p := range s.Snapshot().Params {
			if p.Value != "" { // only explicit /set overrides
				ov.params[p.Name] = p.Value
			}
		}
		ovs = append(ovs, ov)
	}

	// 3. Swap shared state: close the OLD parts, install the new.
	oldCleanup := rt.cleanup
	var cronJobs []CronJob
	for _, j := range newParts.Cron() {
		cronJobs = append(cronJobs, CronJob{
			Name: j.Name, Schedule: j.Schedule, Agent: j.Agent,
			Prompt: j.Prompt, WorkDir: j.WorkDir, Notify: j.Notify,
		})
	}
	tg := newParts.Telegram()
	rt.sessionConfig = func(o SessionOpts) (chat.Config, error) {
		return newParts.SessionConfig(agentsetup.SessionOptions{
			Agent: o.Agent, Subagent: o.Subagent, WorkDir: o.WorkDir,
			Headless: o.Headless, OutPath: o.OutPath, DisableSubagents: o.DisableSubagents,
		})
	}
	rt.cleanup = newCleanup
	rt.store = newParts.Store()
	rt.cron = cronJobs
	rt.telegram = TelegramConfig{
		Token: tg.Token, ChatID: tg.ChatID, Agent: tg.Agent, WorkDir: tg.WorkDir,
		Dashboard: DashboardConfig{Enabled: tg.Dashboard.Enabled, Addr: tg.Dashboard.Addr, URL: tg.Dashboard.URL},
	}
	oldCleanup() // closes old VM, MCP servers, proxies, old store handle

	// 4. Re-derive each live session in place (keep history s.sess), restore overrides.
	var notes []string
	for _, ov := range ovs {
		s := ov.s
		cfg, err := rt.sessionConfig(s.opts)
		if err != nil {
			notes = append(notes, fmt.Sprintf("session %q: re-derive failed: %v", s.name, err))
			continue
		}
		s.cfg = cfg
		s.handlers = chat.NewHandlers(cfg)
		// Restore active agent if it still exists, else fall back + note it.
		if ov.agent != "" && ov.agent != s.ActiveAgent() {
			if err := s.SwitchAgent(ov.agent); err != nil {
				notes = append(notes, fmt.Sprintf("agent %q no longer exists; using %q", ov.agent, s.ActiveAgent()))
			}
		}
		// Replay /set params best-effort.
		for name, val := range ov.params {
			_ = s.SetParam(name, val) // silently skip params the new model lacks
		}
	}

	res := ReloadResult{
		Agents: len(newParts.AgentNames()),
		Models: newParts.ModelCount(),
		Jobs:   len(cronJobs),
		MCP:    newParts.MCPServerCount(),
		Notes:  notes,
	}
	return res, nil
}
```

> This references three small read-only accessors on `agentsetup.Parts` — `AgentNames() []string`, `ModelCount() int`, `MCPServerCount() int` — for the result summary. Add whichever do not already exist (next step). `chat` is already imported by the package (runtime.go/shell3.go); if `pkg/shell3/reload.go` needs it, add `"github.com/weatherjean/shell3/internal/chat"` to its imports.

- [ ] **Step 6: Add the `Parts` accessors** in `internal/agentsetup/agentsetup.go` (only the ones missing — check first with `grep -n "func (p \*Parts) AgentNames\|ModelCount\|MCPServerCount" internal/agentsetup/agentsetup.go`). Mirror the existing `Cron()`/`Telegram()` accessors that delegate to `p.lc`:

```go
// AgentNames returns the declared agent names (for reload summaries).
func (p *Parts) AgentNames() []string { return p.lc.AgentNames() }

// ModelCount returns the number of declared models.
func (p *Parts) ModelCount() int { return p.lc.ModelCount() }

// MCPServerCount returns the number of configured MCP servers.
func (p *Parts) MCPServerCount() int { return p.lc.MCPServerCount() }
```

> Verify the underlying `LoadedConfig` methods exist (`grep -n "func (c \*LoadedConfig) AgentNames\|Agents()\|Models()\|MCPServer" internal/luacfg/luacfg.go`). If `LoadedConfig` exposes `Agents()`/`Models()` slices instead of count helpers, implement the `Parts` accessor in terms of what exists (e.g. `return len(p.lc.Agents())`). If there is no MCP-server accessor, add a trivial `func (c *LoadedConfig) MCPServerCount() int { return len(c.mcpServers) }` matching the real field name. Keep these read-only.

- [ ] **Step 7: Run to verify pass**

Run: `go test ./pkg/shell3/ -run TestReload -v`
Expected: PASS (all four). Then `go build ./... && go vet ./pkg/shell3/ ./internal/agentsetup/ && gofmt -l pkg/shell3/ internal/agentsetup/` (gofmt prints nothing) and `go test -race ./pkg/shell3/`.

> Known minor limitation to leave as a code comment in `reload.go` near step 4: the kept `s.sess` was built with a `ContextWindowFor` closure over the *old* `cfg.ContextWindow`, so a changed `context_window` for the live session is not picked up until restart (new sessions get it). Note it; do not rebuild `s.sess` (that would drop in-memory history).

- [ ] **Step 8: Commit**

```bash
git add pkg/shell3/reload.go pkg/shell3/reload_test.go pkg/shell3/runtime.go pkg/shell3/shell3.go internal/agentsetup/agentsetup.go
git commit -m "feat(shell3): Runtime.Reload — validate-first in-process config reload"
```

---

## Task 2: Host reload coordinator + `decorateSession` + `/reload` command + scheduler swap

**Files:**
- Modify: `internal/telegram/bot.go` (extract `decorateSession`, add `reload` hook field + setter)
- Modify: `internal/telegram/commands.go` (`/reload` command + `BotCommands()` entry)
- Modify: `internal/telegram/commands_test.go` (command test)
- Modify: `cmd/shell3/telegram.go` (reload coordinator wired as the bot's reloader)

The host decides *when* reload runs, re-applies its session decorations afterward, and swaps the cron scheduler.

- [ ] **Step 1: Extract `decorateSession`** in `internal/telegram/bot.go`. The session decorations currently in `NewBot` (`sess.SetApprover(b.approve)` + `b.registerSendTool()`) must be re-appliable after a reload re-derives `s.cfg`. Add a method and call it from `NewBot`:

```go
// decorateSession (re)applies the bot's host-level session customizations:
// the approval hook and host tools. Must be called after NewBot AND after every
// Runtime.Reload (which rebuilds s.cfg and drops these). Safe only when idle.
func (b *Bot) decorateSession() {
	_ = b.sess.SetApprover(b.approve)
	b.registerSendTool()
	b.registerReloadTool() // added in Task 3; if implementing Task 2 first, add an empty stub method that this calls
}
```

In `NewBot`, replace the existing two lines:

```go
	_ = sess.SetApprover(b.approve)
	b.registerSendTool() // gives the agent send_media_telegram
```

with:

```go
	b.decorateSession()
```

> If Task 3 is not yet implemented, add a temporary no-op `func (b *Bot) registerReloadTool() {}` in bot.go so this builds; Task 3 fills it in.

- [ ] **Step 2: Add the reload hook** to the `Bot` struct (beside `runJob func(name string) error`):

```go
	reload func() (shell3.ReloadResult, error) // performs a full config reload; nil if unset
```

Add the setter (mirror `SetJobRunner`):

```go
// SetReloader wires /reload (and the reload tool) to the host's reload coordinator.
func (b *Bot) SetReloader(fn func() (shell3.ReloadResult, error)) { b.reload = fn }
```

- [ ] **Step 3: Write the failing command test** in `internal/telegram/commands_test.go` (append):

```go
func TestCommand_Reload(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42, "")
	called := false
	b.SetReloader(func() (shell3.ReloadResult, error) {
		called = true
		return shell3.ReloadResult{Agents: 3, Jobs: 1}, nil
	})
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/reload"})
	if !called {
		t.Fatal("expected /reload to invoke the reloader")
	}
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "reloaded") {
		t.Fatalf("expected a success reply, got %v", fc.sentTexts())
	}
}

func TestCommand_ReloadNoReloader(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42, "")
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/reload"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "reload not available") {
		t.Fatalf("expected unavailable reply, got %v", fc.sentTexts())
	}
}
```

> Ensure `commands_test.go` imports `"github.com/weatherjean/shell3/pkg/shell3"` (for `shell3.ReloadResult`); add it if absent.

- [ ] **Step 4: Run to verify failure**

Run: `go test ./internal/telegram/ -run TestCommand_Reload -v`
Expected: FAIL — `b.SetReloader` / `/reload` undefined.

- [ ] **Step 5: Add the `/reload` command** in `internal/telegram/commands.go`. Add a `case "/reload":` before `default:`:

```go
	case "/reload":
		if b.reload == nil {
			b.sendReply(ctx, "reload not available")
			return
		}
		res, err := b.reload()
		if err != nil {
			b.sendReply(ctx, "❌ reload failed: "+err.Error())
			return
		}
		b.sendReply(ctx, formatReload(res))
```

Add the formatter at the bottom of `commands.go`:

```go
// formatReload renders a ReloadResult as a chat reply.
func formatReload(r shell3.ReloadResult) string {
	msg := fmt.Sprintf("✅ reloaded — %d agents, %d models, %d jobs, %d MCP", r.Agents, r.Models, r.Jobs, r.MCP)
	if len(r.Notes) > 0 {
		msg += "\n• " + strings.Join(r.Notes, "\n• ")
	}
	return msg
}
```

Add `"fmt"` and the `shell3` import to `commands.go` if not present. Add to `BotCommands()`:

```go
		{"reload", "Reload shell3.lua config without restarting"},
```

- [ ] **Step 6: Run to verify pass**

Run: `go test ./internal/telegram/ -run TestCommand_Reload -v && go build ./...`
Expected: PASS + clean.

- [ ] **Step 7: Wire the coordinator** in `cmd/shell3/telegram.go`. After the bot `b`, scheduler `sched`, and (optionally) dashboard `srv` are created — and after `b.decorateSession()` is implicitly done by `NewBot` — register a reloader that performs the full host-side reload. Add, after the `b.SetJobRunner(...)` block:

```go
			// /reload + reload tool: rebuild config in place, re-decorate the
			// session, and swap the cron scheduler. Runs only when the session
			// is idle (commands are handled between turns; the reload tool
			// defers to end-of-turn — see registerReloadTool).
			b.SetReloader(func() (shell3.ReloadResult, error) {
				res, err := rt.Reload()
				if err != nil {
					return res, err
				}
				b.decorateSession() // re-apply approver + host tools dropped by reload
				// Swap the cron scheduler to the reloaded jobs.
				if sched != nil {
					sched.Stop()
				}
				sched = nil
				if jobs := rt.Cron(); len(jobs) > 0 {
					ns, nerr := cron.New(sess, jobs)
					if nerr != nil {
						return res, nerr
					}
					ns.Start()
					sched = ns
					b.SetJobRunner(sched.Run)
					if srv != nil {
						srv.SetCronSource(cronSource(sched))
					}
				} else {
					b.SetJobRunner(nil)
					if srv != nil {
						srv.SetCronSource(nil)
					}
				}
				return res, nil
			})
```

This requires three small refactors in `telegram.go`:
1. `sched` must be declared in a scope visible to the closure and reassignable (it already is: `var sched *cron.Scheduler`). Ensure the closure captures the variable, not a copy.
2. `srv` must be visible to the closure. If `srv` is currently declared inside the `if tg.Dashboard.Enabled` block, hoist its declaration: `var srv *web.Server` before that block, assign inside, so the closure can reference it.
3. Extract the dashboard cron-source adapter into a helper so it is reusable here and at initial wiring:

```go
// cronSource adapts a scheduler to the dashboard's cron DTO provider.
func cronSource(sched *cron.Scheduler) func() []web.CronJob {
	return func() []web.CronJob {
		var out []web.CronJob
		for _, j := range sched.Jobs() {
			out = append(out, web.CronJob{
				Name: j.Name, Schedule: j.Schedule, Agent: j.Agent,
				Notify: j.Notify, LastRun: j.LastRun, LastSubID: j.LastSubID,
			})
		}
		return out
	}
}
```

Replace the inline `srv.SetCronSource(func() []web.CronJob { ... })` at initial wiring with `srv.SetCronSource(cronSource(sched))`.

- [ ] **Step 8: Build + vet + smoke**

Run: `go build ./... && go vet ./... && gofmt -l cmd/shell3/ internal/telegram/`
Expected: clean.

- [ ] **Step 9: Commit**

```bash
git add internal/telegram/bot.go internal/telegram/commands.go internal/telegram/commands_test.go cmd/shell3/telegram.go
git commit -m "feat(telegram): /reload command + host reload coordinator (re-decorate + scheduler swap)"
```

---

## Task 3: `reload` agent tool + deferred end-of-turn apply

**Files:**
- Modify: `internal/telegram/bot.go` (pending-reload flag, `registerReloadTool`, end-of-turn apply in `handleMsg`)
- Modify: `internal/telegram/reload_tool_test.go` (create)

The self-evolution path: the agent edits `shell3.lua` mid-turn, calls `reload`, the tool records a pending reload and returns immediately; the host applies it after the turn ends (idle).

- [ ] **Step 1: Add the pending flag** to the `Bot` struct (beside `reload`):

```go
	pendingReload bool // set by the reload tool mid-turn; applied at end-of-turn
```

- [ ] **Step 2: Write the failing test** in `internal/telegram/reload_tool_test.go`:

```go
//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/pkg/shell3"
)

func TestReloadTool_DefersToEndOfTurn(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42, "")
	reloads := 0
	b.SetReloader(func() (shell3.ReloadResult, error) {
		reloads++
		return shell3.ReloadResult{Agents: 1}, nil
	})
	// Calling the tool handler directly must NOT reload inline; it records pending.
	out, err := b.reloadToolHandler(context.Background(), "{}")
	if err != nil {
		t.Fatal(err)
	}
	if reloads != 0 {
		t.Fatal("reload tool must not reload inline (would saw off the running turn)")
	}
	if !b.pendingReload {
		t.Fatal("reload tool must set pendingReload")
	}
	if !strings.Contains(out, "scheduled") {
		t.Fatalf("tool should ack scheduling, got %q", out)
	}
	// Simulate end-of-turn: applyPendingReload fires it once and clears.
	b.applyPendingReload(context.Background())
	if reloads != 1 || b.pendingReload {
		t.Fatalf("end-of-turn should apply once and clear: reloads=%d pending=%v", reloads, b.pendingReload)
	}
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/telegram/ -run TestReloadTool -v`
Expected: FAIL — `b.reloadToolHandler` / `b.applyPendingReload` undefined.

- [ ] **Step 4: Implement** the reload tool + apply in `internal/telegram/bot.go` (or a new `internal/telegram/reloadtool.go` — match the `sendtool.go` split). Replace the temporary no-op `registerReloadTool` stub:

```go
// registerReloadTool gives the agent a `reload` tool to apply its own edits to
// shell3.lua. It records a pending reload and returns immediately; the host
// applies it at end-of-turn (a mid-turn reload would tear down the running turn).
func (b *Bot) registerReloadTool() {
	_ = b.sess.RegisterHostTool(shell3.HostTool{
		Name: "reload",
		Description: "Apply your edits to shell3.lua by reloading the config. " +
			"Edit the file first (add/modify a model, agent, tool, skill, or cron job), then call this. " +
			"The reload is validated and applied after this turn ends; if the file is invalid the old config keeps running and you'll see the error.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
		Handler:    b.reloadToolHandler,
	})
}

func (b *Bot) reloadToolHandler(ctx context.Context, argsJSON string) (string, error) {
	if b.reload == nil {
		return "error: reload is not available", nil
	}
	b.pendingReload = true
	return "reload scheduled; it will be validated and applied when this turn ends", nil
}

// applyPendingReload runs a deferred reload if one was requested during the turn
// that just finished. Called at end-of-turn (session idle). Pushes the result.
func (b *Bot) applyPendingReload(ctx context.Context) {
	if !b.pendingReload {
		return
	}
	b.pendingReload = false
	res, err := b.reload()
	if err != nil {
		b.sendReply(ctx, "❌ reload failed: "+err.Error())
		return
	}
	b.sendReply(ctx, formatReload(res))
}
```

- [ ] **Step 5: Hook end-of-turn** in `handleMsg` (bot.go). After the turn completes and the reply is sent, apply any pending reload. Change the tail of `handleMsg`:

```go
	reply := b.drainTurn(b.sess.Send(turnCtx, text))
	b.cancelTurn = nil
	cancel()
	stopTyping()
	b.sendReply(ctx, reply)
	b.applyPendingReload(ctx) // self-evolution: agent edited config + called reload this turn
```

- [ ] **Step 6: Run to verify pass**

Run: `go test ./internal/telegram/ -run TestReloadTool -v && go build ./...`
Expected: PASS + clean. Also run the whole telegram package: `go test ./internal/telegram/`.

- [ ] **Step 7: Commit**

```bash
git add internal/telegram/
git commit -m "feat(telegram): reload agent tool with deferred end-of-turn apply"
```

---

## Task 4: `self-evolve` skill + scaffold + docs

**Files:**
- Modify: `internal/scaffold/defaults/base/shell3.lua.tmpl` (a `self-evolve` skill + grant it to the `code` agent; commented `reload` note)
- Modify: `CHANGELOG.md`
- Modify: `docs/dev/superpowers/specs/2026-06-11-config-hot-reload-design.md` (status → implemented)
- Modify: `internal/scaffold/scaffold_test.go` only if it asserts rendered content

- [ ] **Step 1: Add the skill** to `shell3.lua.tmpl`. The template already declares skills via `shell3.skill{ name, description, body }` and grants them to an agent via `skills = { handle }` (see the existing `brainstorming` skill in the template). Add, near the other skill declarations:

```lua
local self_evolve = shell3.skill({
  name = "self-evolve",
  description = "How to safely change your own shell3.lua config and apply it with reload",
  body = [[
You can modify your own configuration and apply it live.

Loop:
1. Edit `shell3.lua` with your file tools — add or change a `shell3.model{}`,
   `shell3.agent{}`, `shell3.subagent{}`, custom tool, `shell3.skill{}`, or
   `shell3.cron{}` block. Follow the syntax already in the file.
2. Call the `reload` tool. It validates the whole file and applies it AFTER this
   turn ends.
3. If validation fails, the OLD config keeps running and you get the error — fix
   the file and call `reload` again. You cannot break the bot with a bad edit.

Notes:
- Cron `agent` must reference a declared subagent; agents/models must reference
  declared models — these are validated on reload.
- MCP servers and model proxies restart on reload (a brief pause); everything
  else (agents, models, tools, skills, cron) applies cleanly.
- Your active agent and any /set params are preserved across reload when they
  still exist in the new config.
]],
})
```

Grant it to the `code` agent by adding `self_evolve` to its `skills = { ... }` list (alongside `brainstorming`).

- [ ] **Step 2: Add a `CHANGELOG.md` entry** under `## [Unreleased] → ### Added` summarizing hot reload: `/reload` command + `reload` agent tool, validate-first (bad edit keeps old config), idle-gated full rebuild, history preserved, `/agent`+`/set` best-effort restored, MCP/proxies restart on reload. Note the future optimization (MCP carry-over) and that fsnotify is intentionally not included.

- [ ] **Step 3: Update the spec status** header in `docs/dev/superpowers/specs/2026-06-11-config-hot-reload-design.md` to `Status: implemented (2026-06-11)`.

- [ ] **Step 4: Run `go test ./internal/scaffold/...`**; fix any rendered-content assertion. The new skill is real Lua (not commented), so confirm the template still loads — the scaffold test loads the rendered config; if it fails because `self_evolve` is granted to an agent that the test inspects, adjust per the failure.

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/ CHANGELOG.md docs/dev/superpowers/specs/2026-06-11-config-hot-reload-design.md
git commit -m "docs(reload): self-evolve skill, scaffold, changelog, spec status"
```

---

## Task 5: Integration test + verification sweep

**Files:**
- Create: `pkg/shell3/reload_integration_test.go` (or add to `reload_test.go`)

- [ ] **Step 1: Write an end-to-end-ish test** proving a reloaded cron job is dispatchable and history survives. In `pkg/shell3` (real Lua config, fake LLM via the runtime's session):

```go
func TestReload_PreservesHistoryAndArmsNewJob(t *testing.T) {
	dir := t.TempDir()
	path := writeCfg(t, dir, baseCfg)
	rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: path, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, _ := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	before := sess.Snapshot() // capture turn/history count shape

	writeCfg(t, dir, baseCfg+`
shell3.cron({ jobs = { { name="nightly", schedule="@daily", agent="explorer", prompt="go", notify=false } } })
`)
	if _, err := rt.Reload(); err != nil {
		t.Fatal(err)
	}
	if jobs := rt.Cron(); len(jobs) != 1 || jobs[0].Name != "nightly" {
		t.Fatalf("new job not armed: %+v", jobs)
	}
	// Session object identity + history container preserved (same *Session).
	after := sess.Snapshot()
	_ = before
	_ = after // assert any stable field that proves the session wasn't recreated, e.g. session id if exposed
}
```

> Adjust the assertion to whatever stable identity/history field `Snapshot()` exposes (inspect the `Snapshot` struct). If none is suitable, assert that `sess.HasQueuedInput()` and `sess.ActiveAgent()` behave consistently before/after — the point is to prove the SAME session object survived the reload.

- [ ] **Step 2: Full verification sweep** (orchestrator):

Run: `go build ./... && go vet ./... && gofmt -l . && go test -race ./...`
Expected: all clean/green.

- [ ] **Step 3: Commit**

```bash
git add pkg/shell3/reload_integration_test.go
git commit -m "test(reload): integration — new job armed + session identity preserved"
```

---

## Self-Review

**Spec coverage:**
- `Runtime.Reload` validate-first / idle-gated / full rebuild / in-place session re-derivation / history kept → Task 1. ✓
- Safety gate (bad file keeps old config) → Task 1 `TestReload_InvalidKeepsOldConfig`. ✓
- Best-effort override restore (agent + /set; graceful fallback on deleted agent) → Task 1 (capture/restore) + tests. ✓
- Host re-decoration after reload (the `RegisterHostTool`/`SetApprover` GOTCHA) → Task 2 `decorateSession`. ✓
- `/reload` command + human-readable result/error → Task 2. ✓
- Cron scheduler swap on reload → Task 2 coordinator. ✓
- `reload` agent tool + deferred end-of-turn apply (no inline reload) → Task 3. ✓
- `self-evolve` skill + scaffold + docs → Task 4. ✓
- Integration (new job armed, session identity preserved) → Task 5. ✓
- No fsnotify, no selective reload, MCP carry-over documented as future → spec non-goals; nothing in the plan adds them. ✓

**Placeholder scan:** The three `Parts` accessors (`AgentNames`/`ModelCount`/`MCPServerCount`) carry an explicit "verify the underlying LoadedConfig method names / adapt to what exists" note (Task 1 Step 6) — real best-known code, not a guess. Task 5 Step 1's final assertion is explicitly parameterized on the real `Snapshot` shape (inspect-then-assert) rather than inventing a field. No TBD/TODO.

**Type consistency:** `ReloadResult{Agents,Models,Jobs,MCP int; Notes []string}` defined in Task 1, consumed identically by `formatReload` (Task 2) and the tests (Tasks 1–3). `SetReloader(func() (shell3.ReloadResult, error))` defined in Task 2, called by `/reload` (Task 2) and `applyPendingReload` (Task 3). `decorateSession` defined in Task 2, called in `NewBot` (Task 2) and the coordinator (Task 2); it calls `registerReloadTool` (stub in Task 2, filled in Task 3). `cronSource` helper defined in Task 2, used at initial wiring and in the coordinator. `s.opts` field added in Task 1 and read by `Reload`.

**Ordering note (integration seam):** `decorateSession` must run AFTER `rt.Reload()` re-derives `s.cfg` (Task 2 coordinator) and after any `SwitchAgent` restore inside `Reload` — because both `Reload`'s re-derivation and `SwitchAgent` rebuild `s.cfg` and would drop host tools. The coordinator calls `rt.Reload()` (which does the agent/param restore internally) then `b.decorateSession()`, so host tools are applied last. This ordering is the single most important thing for the implementer to preserve.
