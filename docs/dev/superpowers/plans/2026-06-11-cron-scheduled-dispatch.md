# Cron / Scheduled Dispatch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run scheduled jobs in the always-on shell3 agent — on a cron schedule, dispatch work to an isolated subagent that reports its result back into the main session, with a per-job `notify` policy controlling whether results push to the chat, surfaced in the Telegram bot and the dashboard.

**Architecture:** Cron is a timer that injects work into machinery that already exists (Wake bus, subagent spawn, `deliverSubagentResult`, `RunQueued`, the bot's `consumeWakes`). The only engine change is one new public method, `pkg/shell3.Session.Dispatch` — a host-side trigger of the existing subagent path with `notify` gating and terminal-error detection. A `cron{...}` Lua block is parsed by `luacfg`, threaded through `agentsetup → Runtime.Cron()`, and run by a new `internal/cron.Scheduler` (over `github.com/robfig/cron/v3`) wired into the `shell3 telegram` host. A read-only dashboard tab and a `/run <job>` command round it out.

**Tech Stack:** Go, `github.com/robfig/cron/v3` (new), `gopher-lua` (existing), `pkg/shell3.Runtime` (existing), `net/http` dashboard (existing).

**Source of truth:** `docs/dev/superpowers/specs/2026-06-10-cron-scheduled-dispatch-design.md`. Signatures below are copied verbatim from current code (verified 2026-06-11).

**Build approach:** Tasks are mostly disjoint by file. Task 0 (dep) and Task 1 (`Dispatch`, the only engine change) gate the rest; do them first. Then {2,3} config threading, {4,5,6} scheduler + host + command, {7} dashboard, {8} docs. After each batch the orchestrator verifies with `go build ./... && go vet ./... && gofmt -l . && go test -race ./...`.

**Key verified internals (do not guess these):**
- `chat.SpawnRequest{Task, Subagent, WorkDir string}` (internal/chat/toolhandler.go:133).
- `Session.spawn` builds the child via `rt.nextSubID()` → `auditPath := filepath.Join(rt.root(), ".shell3", "agents", id+".jsonl")` → `rt.Session(SessionOpts{Name:"sub:"+id, Subagent, WorkDir, Headless:true, OutPath:auditPath, DisableSubagents:true})` → `s.subs.add(id, agent, task)` → `rt.trackSubagent(func(){ drain child.Send; s.subs.finish; child.Close; deliverSubagentResult })` (pkg/shell3/subagents.go).
- `Session.deliverSubagentResult(rt, id, result)` does `s.sess.Interject(...)` then `if !s.isBusy() { rt.emit(HostEvent{Session:s.name, Kind:Wake}) }`.
- Runtime helpers (unexported, same package): `rt.nextSubID() string`, `rt.root() string`, `rt.baseContext() context.Context`, `rt.trackSubagent(func()) bool`, `rt.emit(HostEvent)`.
- Public Event kinds: `shell3.Token`, `shell3.Error` (with `ev.Err`), `shell3.Retry` (distinct — intermediate, NOT terminal).
- luacfg load-time cross-ref validation lives in `Load` (internal/luacfg/luacfg.go ~168-190), pattern: `return nil, fmt.Errorf("config: agent %q references unknown subagent %q", a.Name, name)`. Helpers: `c.SubagentByName(name) (Subagent, bool)`, `checkKeys(tbl, ctx, allowed)`, `optStr/optBool(tbl, key)`.
- Config exposure pattern: `luacfg.LoadedConfig.Telegram()` → `agentsetup.Parts.Telegram()` → `Runtime.telegram` field + `Runtime.Telegram()`. Mirror for cron.
- Telegram subcommand: `cmd/shell3/telegram.go` builds `rt`, the `sess` (`SessionOpts{Name:"telegram", Agent:tg.Agent, WorkDir:tg.WorkDir}`), `b := telegram.NewBot(...)`, optional dashboard `web.NewServer(...)`, then `b.Run(ctx)`. `ctx` is `signal.NotifyContext(SIGINT,SIGTERM)`.
- Dashboard: `internal/telegram/web/server.go` `Handler()` registers `mux.HandleFunc("/api/...", s.auth(s.handleX))`; `internal/telegram/web/static/index.html` has a `<nav>` of `<button data-tab="...">` + matching `<section class="view">` + JS `refresh()`/render funcs.

---

## Task 0: Add the `robfig/cron` dependency

**Files:** Modify `go.mod`, `go.sum`.

- [ ] **Step 1:** Add the dependency.

Run: `go get github.com/robfig/cron/v3@latest`
Expected: `go.mod` gains `github.com/robfig/cron/v3 vX.Y.Z`.

- [ ] **Step 2:** Confirm the installed API (do not guess in later tasks).

Run: `go doc github.com/robfig/cron/v3.Cron` and `go doc github.com/robfig/cron/v3.New`
Expected to confirm: `cron.New(opts ...Option) *Cron`, `(*Cron).AddFunc(spec string, cmd func()) (EntryID, error)`, `(*Cron).Start()`, `(*Cron).Stop() context.Context`. If any signature differs from Task 4's code, adjust Task 4 to match the installed version — this is the one external boundary.

- [ ] **Step 3:** Commit.

```bash
git add go.mod go.sum
git commit -m "build: add github.com/robfig/cron/v3 for scheduled dispatch"
```

---

## Task 1: `Session.Dispatch` — host-initiated subagent with `notify` gating

**Files:**
- Create: `pkg/shell3/dispatch.go`
- Create: `pkg/shell3/dispatch_test.go`

This is the only engine change. It mirrors `spawn` but (a) is host-initiated (no model turn), (b) detects the **terminal** error (ignoring `Retry`), (c) labels the result, and (d) delivers to the parent inbox only when `Notify || failed` — so a quiet success is transcript-only, while a failure always pushes (loud on the last failure, not per retry).

- [ ] **Step 1: Write the failing tests** in `pkg/shell3/dispatch_test.go`

```go
package shell3

import (
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

// dispatchCfg builds a config whose subagent run streams the given text (and,
// when failText != "", a terminal Error). Mirrors fakeCfg in runtime_test.go.
func dispatchCfg(text string) func() chat.Config {
	return func() chat.Config {
		return chat.Config{
			LLM: fakellm.New(
				fakellm.Script{Events: []llm.StreamEvent{{TextDelta: text}}},
				fakellm.Script{Events: []llm.StreamEvent{{TextDelta: text}}},
			),
			ModeLabel: "code",
			// Subagent allowlist + name resolution is bypassed by the fake
			// sessionConfig (newTestRuntime ignores SessionOpts.Subagent); the
			// dispatch path only needs a runnable child session.
		}
	}
}

func TestDispatch_NotifyDeliversAndWakes(t *testing.T) {
	rt := newTestRuntime(t, dispatchCfg("job done"))
	main, err := rt.Session(SessionOpts{Name: "telegram"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := main.Dispatch("explorer", "do the thing", DispatchOpts{Label: "cron:nightly", Notify: true})
	if err != nil || id == "" {
		t.Fatalf("dispatch: id=%q err=%v", id, err)
	}
	// A Wake should fire once the subagent delivers into the idle main session.
	select {
	case ev := <-rt.Events():
		if ev.Kind != Wake || ev.Session != "telegram" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected a Wake from the notify=true dispatch")
	}
	// The queued inbox item carries the labeled result.
	reply := drainText(main.RunQueued(context()))
	if !strings.Contains(reply, "job done") {
		t.Fatalf("expected the labeled result in the wake turn, got %q", reply)
	}
}

func TestDispatch_QuietSuccessNoWake(t *testing.T) {
	rt := newTestRuntime(t, dispatchCfg("quiet result"))
	main, _ := rt.Session(SessionOpts{Name: "telegram"})
	if _, err := main.Dispatch("explorer", "bg job", DispatchOpts{Notify: false}); err != nil {
		t.Fatal(err)
	}
	// notify=false success must NOT wake the main session.
	select {
	case ev := <-rt.Events():
		t.Fatalf("quiet success should not wake, got %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
	if main.HasQueuedInput() {
		t.Fatal("quiet success should not queue input on the main session")
	}
}

func TestDispatch_RejectedFromSubagentSession(t *testing.T) {
	rt := newTestRuntime(t, dispatchCfg("x"))
	sub, _ := rt.Session(SessionOpts{Name: "sub:a1"})
	if _, err := sub.Dispatch("explorer", "nope", DispatchOpts{}); err == nil {
		t.Fatal("dispatch from a subagent session must be rejected (depth-1)")
	}
}
```

Add a tiny test helper at the bottom of `dispatch_test.go` (the package may already have `context()`/`drainText` — if so, delete these and use the existing ones; check `runtime_test.go`/`shell3_test.go` first):

```go
func context() context.Context { return contextpkg.Background() }
func drainText(ch <-chan Event) string {
	var b strings.Builder
	for ev := range ch {
		if ev.Kind == Token {
			b.WriteString(ev.Text)
		}
	}
	return b.String()
}
```
…and import `contextpkg "context"`. **Before writing these helpers, grep the test package** (`grep -rn "func drainText\|func newTestRuntime" pkg/shell3/*_test.go`) and reuse what exists rather than redefining (a redefinition is a compile error).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./pkg/shell3/ -run TestDispatch -v`
Expected: FAIL — `main.Dispatch undefined`, `DispatchOpts undefined`.

- [ ] **Step 3: Implement** `pkg/shell3/dispatch.go`

```go
package shell3

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DispatchOpts parameterizes a host-initiated subagent dispatch (e.g. cron).
type DispatchOpts struct {
	WorkDir string // "" → main session workdir; relative → joined to it
	Label   string // tags the delivered result, e.g. "cron:nightly" → "[cron:nightly] …"
	Notify  bool   // deliver the result to the parent on success; failures always deliver
}

// Dispatch runs a registered subagent from the host (not a model turn) and
// reports its result back into THIS (main) session's inbox, waking it. It is the
// cron/host-side trigger for the same path the model's spawn_agent tool uses,
// inheriting unique ids, depth-1, Close-joins-goroutines, and result-to-inbox.
//
// notify gating: on a successful run the result is delivered (and the session
// woken) only when Notify is true; otherwise it is recorded in the subagent
// transcript only. A run that ends in a terminal error ALWAYS delivers, so a
// quiet background job can never fail silently. The terminal Error event is the
// trigger — intermediate Retry events are ignored, so we go loud once on the
// final failure, not per retry. Returns the subagent id.
func (s *Session) Dispatch(agent, prompt string, opts DispatchOpts) (string, error) {
	if s.runtime == nil {
		return "", fmt.Errorf("shell3: session has no runtime; cannot dispatch")
	}
	if strings.HasPrefix(s.name, "sub:") {
		return "", fmt.Errorf("shell3: dispatch is not allowed from a subagent session (depth-1)")
	}
	if strings.TrimSpace(agent) == "" {
		return "", fmt.Errorf("shell3: dispatch requires an agent name")
	}
	workdir := opts.WorkDir
	if workdir == "" {
		workdir = s.cfg.WorkDir
	} else if !filepath.IsAbs(workdir) {
		workdir = filepath.Join(s.cfg.WorkDir, workdir)
	}
	rt := s.runtime
	id := rt.nextSubID()
	auditPath := filepath.Join(rt.root(), ".shell3", "agents", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(auditPath), 0o755); err != nil {
		return "", err
	}
	child, err := rt.Session(SessionOpts{
		Name: "sub:" + id, Subagent: agent, WorkDir: workdir,
		Headless: true, OutPath: auditPath, DisableSubagents: true,
	})
	if err != nil {
		return "", err
	}
	sa := s.subs.add(id, agent, prompt)
	label := opts.Label
	if label == "" {
		label = "dispatch"
	}
	notify := opts.Notify
	runCtx := rt.baseContext()
	started := rt.trackSubagent(func() {
		var b strings.Builder
		failed := false
		for ev := range child.Send(runCtx, prompt) {
			switch ev.Kind {
			case Token:
				b.WriteString(ev.Text)
			case Error:
				failed = true // terminal; Retry is a separate Kind and is ignored
				if ev.Err != nil {
					b.WriteString("\nerror: " + ev.Err.Error())
				}
			}
		}
		result := strings.TrimSpace(b.String())
		s.subs.finish(sa, result)
		_ = child.Close()
		if notify || failed {
			s.deliverDispatchResult(rt, fmt.Sprintf("[%s] %s", label, result))
		}
	})
	if !started {
		s.subs.remove(sa)
		_ = child.Close()
		return "", fmt.Errorf("shell3: runtime is closing; cannot dispatch")
	}
	return id, nil
}

// deliverDispatchResult injects an already-labeled host-dispatch result into
// this session's inbox and wakes it when idle (sibling of deliverSubagentResult).
func (s *Session) deliverDispatchResult(rt *Runtime, labeled string) {
	s.sess.Interject(labeled)
	if !s.isBusy() {
		rt.emit(HostEvent{Session: s.name, Kind: Wake})
	}
}
```
> Note: `newTestRuntime`'s fake `sessionConfig` ignores `SessionOpts.Subagent`, so the child runs the fake model regardless of agent name — the tests exercise the dispatch/notify/error plumbing, not real subagent resolution (that is covered by Task 2's config validation + Task 5's integration). Production resolution happens in `agentsetup.SubagentRuntime`, which errors on an unknown name; that error surfaces from `rt.Session(...)` above.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./pkg/shell3/ -run TestDispatch -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add pkg/shell3/dispatch.go pkg/shell3/dispatch_test.go
git commit -m "feat(shell3): Session.Dispatch — host-initiated subagent with notify gating"
```

---

## Task 2: `luacfg` — parse `cron{ jobs = {...} }` + validate agents

**Files:**
- Modify: `internal/luacfg/luacfg.go` (types + `Cron()` getter + load-time validation)
- Modify: `internal/luacfg/register.go` (`luaCron` parser + registration)
- Create: `internal/luacfg/cron_test.go`

- [ ] **Step 1: Write the failing tests** in `internal/luacfg/cron_test.go`

```go
package luacfg

import (
	"strings"
	"testing"
)

func TestLoadCron(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
shell3.subagent({ name="explorer", model="main", description="d", prompt="p", tools={} })
shell3.agent({ name="code", model="main", prompt="hi", tools={ subagents={"explorer"} } })
shell3.cron({ jobs = {
  { name="nightly", schedule="0 9 * * *", agent="explorer", prompt="summarize", notify=true },
  { schedule="@hourly", agent="explorer", prompt="check", workdir="/tmp" },
}})
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	jobs := c.Cron()
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(jobs))
	}
	if jobs[0].Name != "nightly" || jobs[0].Schedule != "0 9 * * *" || jobs[0].Agent != "explorer" || !jobs[0].Notify {
		t.Fatalf("bad job 0: %+v", jobs[0])
	}
	// notify defaults to true when omitted; name defaults to job-<n>.
	if !jobs[1].Notify || jobs[1].Name != "job-2" || jobs[1].WorkDir != "/tmp" {
		t.Fatalf("bad job 1 defaults: %+v", jobs[1])
	}
}

func TestLoadCronUnknownAgent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
shell3.agent({ name="code", model="main", prompt="hi", tools={} })
shell3.cron({ jobs = { { schedule="@daily", agent="ghost", prompt="x" } } })
`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil || !strings.Contains(err.Error(), `unknown subagent "ghost"`) {
		t.Fatalf("want unknown-subagent error, got %v", err)
	}
}

func TestLoadCronUnknownKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
shell3.agent({ name="code", model="main", prompt="hi", tools={} })
shell3.cron({ jobs = { { schedule="@daily", agent="code", prompt="x", nope=true } } })
`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil || !strings.Contains(err.Error(), `unknown key "nope"`) {
		t.Fatalf("want unknown-key error, got %v", err)
	}
}
```
> Confirm the exact `checkKeys` error format against `internal/luacfg/strict.go` and adjust the `unknown key` assertion if needed (it is `"%s: unknown key %q"`).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/luacfg/ -run TestLoadCron -v`
Expected: FAIL — `c.Cron undefined`.

- [ ] **Step 3: Add types + getter + validation** in `internal/luacfg/luacfg.go`

Add near `TelegramConfig`:
```go
// CronJob is one parsed shell3.cron job entry.
type CronJob struct {
	Name     string
	Schedule string
	Agent    string
	Prompt   string
	WorkDir  string
	Notify   bool
}
```
Add an unexported field to `LoadedConfig` (beside `telegram TelegramConfig`):
```go
	cron []CronJob
```
Add the getter (mirrors `Telegram`):
```go
// Cron returns the parsed shell3.cron jobs (nil if absent).
func (c *LoadedConfig) Cron() []CronJob { return c.cron }
```
In `Load`, after the existing agent→subagent cross-ref validation block (luacfg.go ~190), add cron agent validation:
```go
	for i := range c.cron {
		if c.cron[i].Schedule == "" {
			return nil, fmt.Errorf("config: cron job %q has no schedule", c.cron[i].Name)
		}
		if c.cron[i].Agent == "" {
			return nil, fmt.Errorf("config: cron job %q has no agent", c.cron[i].Name)
		}
		if _, ok := c.SubagentByName(c.cron[i].Agent); !ok {
			return nil, fmt.Errorf("config: cron job %q references unknown subagent %q", c.cron[i].Name, c.cron[i].Agent)
		}
	}
```

- [ ] **Step 4: Add the parser** in `internal/luacfg/register.go`

Register beside the telegram line in `registerShell3`:
```go
	L.SetField(tbl, "cron", L.NewFunction(c.luaCron))
```
Add the key allowlist + parser (mirror `luaTelegram`):
```go
var cronKeys = map[string]bool{"jobs": true}
var cronJobKeys = map[string]bool{
	"name": true, "schedule": true, "agent": true, "prompt": true, "workdir": true, "notify": true,
}

func (c *LoadedConfig) luaCron(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "cron", cronKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	jobsT, ok := opts.RawGetString("jobs").(*lua.LTable)
	if !ok {
		return 0 // no jobs
	}
	n := 0
	jobsT.ForEach(func(_, v lua.LValue) {
		jt, ok := v.(*lua.LTable)
		if !ok {
			return
		}
		n++
		if err := checkKeys(jt, "cron.job", cronJobKeys); err != nil {
			L.RaiseError("%s", err.Error())
		}
		job := CronJob{
			Name:     optStr(jt, "name"),
			Schedule: optStr(jt, "schedule"),
			Agent:    optStr(jt, "agent"),
			Prompt:   optStr(jt, "prompt"),
			WorkDir:  optStr(jt, "workdir"),
			Notify:   true, // default
		}
		if v := jt.RawGetString("notify"); v != lua.LNil {
			job.Notify = lua.LVAsBool(v)
		}
		if job.Name == "" {
			job.Name = fmt.Sprintf("job-%d", n)
		}
		c.cron = append(c.cron, job)
	})
	return 0
}
```
> `jobsT.ForEach` iteration order over a Lua array part is by integer key ascending in gopher-lua; the `job-%d` default uses the encounter count, which matches the array index. If a test proves ordering is unstable, sort `c.cron` by an explicit index captured during parse. Add `"fmt"` to register.go imports if not present.

- [ ] **Step 5: Run to verify pass**

Run: `go test ./internal/luacfg/ -run TestLoadCron -v`
Expected: PASS (all three).

- [ ] **Step 6: Commit**

```bash
git add internal/luacfg/luacfg.go internal/luacfg/register.go internal/luacfg/cron_test.go
git commit -m "feat(luacfg): parse shell3.cron{} jobs + validate agent references"
```

---

## Task 3: Thread cron config through `agentsetup` → `Runtime.Cron()`

**Files:**
- Modify: `internal/agentsetup/agentsetup.go` (`Parts.Cron()` accessor)
- Modify: `pkg/shell3/runtime.go` (re-exported `CronJob`, `cron` field, capture, `Cron()` getter)
- Create: `pkg/shell3/cron_config_test.go`

- [ ] **Step 1: Write the failing test** in `pkg/shell3/cron_config_test.go`

```go
package shell3_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/pkg/shell3"
)

func TestRuntime_CronConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := `
shell3.model("main", { base_url="https://api.x/v1", api_key="k", model="m-1", context_window=1000 })
shell3.subagent({ name="explorer", model="main", description="d", prompt="p", tools={} })
shell3.agent({ name="code", model="main", prompt="hi", tools={ subagents={"explorer"} } })
shell3.cron({ jobs = { { name="n", schedule="@daily", agent="explorer", prompt="go", notify=false } } })
`
	path := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: path, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	jobs := rt.Cron()
	if len(jobs) != 1 || jobs[0].Name != "n" || jobs[0].Agent != "explorer" || jobs[0].Notify {
		t.Fatalf("bad cron config: %+v", jobs)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./pkg/shell3/ -run TestRuntime_CronConfig -v`
Expected: FAIL — `rt.Cron undefined`.

- [ ] **Step 3: Add `Parts.Cron()`** in `internal/agentsetup/agentsetup.go` (beside `Telegram()`):
```go
// Cron returns the parsed shell3.cron jobs (nil if absent).
func (p *Parts) Cron() []luacfg.CronJob { return p.lc.Cron() }
```

- [ ] **Step 4: Re-export + capture + expose** in `pkg/shell3/runtime.go`

Add the public type (near `TelegramConfig`):
```go
// CronJob mirrors one parsed shell3.cron job.
type CronJob struct {
	Name     string
	Schedule string
	Agent    string
	Prompt   string
	WorkDir  string
	Notify   bool
}
```
Add field to `Runtime` (beside `telegram TelegramConfig`):
```go
	cron []CronJob
```
In `NewRuntime`, after `tg := parts.Telegram()` capture and convert:
```go
	var cronJobs []CronJob
	for _, j := range parts.Cron() {
		cronJobs = append(cronJobs, CronJob{
			Name: j.Name, Schedule: j.Schedule, Agent: j.Agent,
			Prompt: j.Prompt, WorkDir: j.WorkDir, Notify: j.Notify,
		})
	}
```
Set `cron: cronJobs,` in the returned `&Runtime{...}` literal. Add the getter:
```go
// Cron returns the parsed shell3.cron jobs (nil if absent).
func (rt *Runtime) Cron() []CronJob { return rt.cron }
```

- [ ] **Step 5: Run to verify pass**

Run: `go test ./pkg/shell3/ -run TestRuntime_CronConfig -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/agentsetup/agentsetup.go pkg/shell3/runtime.go pkg/shell3/cron_config_test.go
git commit -m "feat(pkg): surface cron jobs via Runtime.Cron()"
```

---

## Task 4: `internal/cron.Scheduler` over `robfig/cron`

**Files:**
- Create: `internal/cron/scheduler.go`
- Create: `internal/cron/scheduler_test.go`

The scheduler arms one entry per job, calls `Dispatch` on tick, and tracks per-job run status for the dashboard. It depends on a tiny `dispatcher` interface so tests inject a fake (no real subagents).

- [ ] **Step 1: Write the failing tests** in `internal/cron/scheduler_test.go`

```go
//go:build unix

package cron

import (
	"sync"
	"testing"
	"time"

	"github.com/weatherjean/shell3/pkg/shell3"
)

type fakeDispatcher struct {
	mu    sync.Mutex
	calls []shell3.CronJob
}

func (f *fakeDispatcher) Dispatch(agent, prompt string, opts shell3.DispatchOpts) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, shell3.CronJob{Agent: agent, Prompt: prompt, WorkDir: opts.WorkDir, Name: opts.Label, Notify: opts.Notify})
	return "subX", nil
}
func (f *fakeDispatcher) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

func TestScheduler_FireDispatches(t *testing.T) {
	fd := &fakeDispatcher{}
	jobs := []shell3.CronJob{{Name: "j1", Schedule: "@every 1s", Agent: "explorer", Prompt: "go", Notify: true}}
	s, err := New(fd, jobs)
	if err != nil {
		t.Fatal(err)
	}
	// Fire entry 0 directly (no real clock wait), the way the cron lib would.
	s.fire(jobs[0])
	if fd.count() != 1 {
		t.Fatalf("want 1 dispatch, got %d", fd.count())
	}
	got := fd.calls[0]
	if got.Agent != "explorer" || got.Prompt != "go" || got.Name != "cron:j1" || !got.Notify {
		t.Fatalf("bad dispatch args: %+v", got)
	}
	// Status is recorded.
	js := s.Jobs()
	if len(js) != 1 || js[0].Name != "j1" || js[0].LastSubID != "subX" {
		t.Fatalf("bad job status: %+v", js)
	}
}

func TestScheduler_BadScheduleRejected(t *testing.T) {
	if _, err := New(&fakeDispatcher{}, []shell3.CronJob{{Name: "x", Schedule: "not a cron", Agent: "a"}}); err == nil {
		t.Fatal("expected error for malformed schedule")
	}
}

func TestScheduler_StartStopClean(t *testing.T) {
	s, _ := New(&fakeDispatcher{}, []shell3.CronJob{{Name: "j", Schedule: "@every 1h", Agent: "explorer", Prompt: "p"}})
	s.Start()
	time.Sleep(20 * time.Millisecond)
	s.Stop() // must not hang
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/cron/ -v`
Expected: FAIL — package/`New` missing.

- [ ] **Step 3: Implement** `internal/cron/scheduler.go`

```go
//go:build unix

package cron

import (
	"fmt"
	"sync"
	"time"

	robcron "github.com/robfig/cron/v3"
	"github.com/weatherjean/shell3/pkg/shell3"
)

// Dispatcher is the subset of *shell3.Session the scheduler needs (faked in tests).
type Dispatcher interface {
	Dispatch(agent, prompt string, opts shell3.DispatchOpts) (string, error)
}

// JobStatus is a job plus its most recent run, for the dashboard.
type JobStatus struct {
	Name      string `json:"name"`
	Schedule  string `json:"schedule"`
	Agent     string `json:"agent"`
	Notify    bool   `json:"notify"`
	LastRun   string `json:"last_run,omitempty"` // RFC3339, "" if never
	LastSubID string `json:"last_sub_id,omitempty"`
}

// Scheduler arms one robfig/cron entry per job and dispatches on tick.
type Scheduler struct {
	disp Dispatcher
	c    *robcron.Cron
	mu   sync.Mutex
	jobs []shell3.CronJob
	last map[string]JobStatus // by job name
	now  func() time.Time     // injectable clock for tests
}

// New validates every schedule and arms an entry per job. Returns an error if
// any schedule is malformed (fail-fast at startup).
func New(disp Dispatcher, jobs []shell3.CronJob) (*Scheduler, error) {
	s := &Scheduler{
		disp: disp,
		c:    robcron.New(),
		jobs: jobs,
		last: map[string]JobStatus{},
		now:  time.Now,
	}
	for _, j := range jobs {
		job := j // capture
		s.last[job.Name] = JobStatus{Name: job.Name, Schedule: job.Schedule, Agent: job.Agent, Notify: job.Notify}
		if _, err := s.c.AddFunc(job.Schedule, func() { s.fire(job) }); err != nil {
			return nil, fmt.Errorf("cron: job %q bad schedule %q: %w", job.Name, job.Schedule, err)
		}
	}
	return s, nil
}

// fire dispatches one job and records its run status.
func (s *Scheduler) fire(j shell3.CronJob) {
	id, err := s.disp.Dispatch(j.Agent, j.Prompt, shell3.DispatchOpts{
		WorkDir: j.WorkDir, Label: "cron:" + j.Name, Notify: j.Notify,
	})
	s.mu.Lock()
	st := s.last[j.Name]
	st.LastRun = s.now().UTC().Format(time.RFC3339)
	if err == nil {
		st.LastSubID = id
	}
	s.last[j.Name] = st
	s.mu.Unlock()
}

// Start begins firing on schedule. Stop halts it (blocks until running jobs’
// dispatch calls return; in-flight subagents are joined by Runtime.Close).
func (s *Scheduler) Start() { s.c.Start() }
func (s *Scheduler) Stop()  { s.c.Stop() }

// Jobs returns each configured job with its last run, for the dashboard.
func (s *Scheduler) Jobs() []JobStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]JobStatus, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, s.last[j.Name])
	}
	return out
}
```
> **Verify the `robfig/cron/v3` API** (Task 0): `robcron.New()`, `(*Cron).AddFunc(spec, func) (EntryID, error)`, `Start()`, `Stop() context.Context`. If `New` needs `robcron.WithSeconds()` for second-granularity macros like `@every 1s`, add it — but note the standard 5-field cron is minute-granularity; the tests call `fire` directly so they do not depend on real tick timing.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/cron/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cron/scheduler.go internal/cron/scheduler_test.go go.mod go.sum
git commit -m "feat(cron): Scheduler over robfig/cron with per-job run status"
```

---

## Task 5: Wire the scheduler into the `shell3 telegram` host

**Files:** Modify `cmd/shell3/telegram.go`

The host builds a `Scheduler` from `rt.Cron()` on the main session, starts it, and stops it before `rt.Close()`.

- [ ] **Step 1:** In `cmd/shell3/telegram.go`, after the `sess` is created and before `b.Run(ctx)`, add:
```go
		// Scheduled jobs (shell3.cron{}): arm a scheduler on the main session.
		var sched *cron.Scheduler
		if jobs := rt.Cron(); len(jobs) > 0 {
			sched, err = cron.New(sess, jobs)
			if err != nil {
				return err // fail-fast on a bad schedule
			}
			sched.Start()
			defer sched.Stop() // stop before rt.Close()'s deferred run
			fmt.Printf("cron: %d job(s) scheduled\n", len(jobs))
		}
```
Add imports: `"github.com/weatherjean/shell3/internal/cron"`. Ensure `sched.Stop()` runs before `rt.Close()` — since `defer rt.Close()` is registered earlier, its deferred call runs *after* this later `defer sched.Stop()` (LIFO), which is correct (stop scheduling, then close runtime which joins in-flight subagents).

> `*shell3.Session` satisfies `cron.Dispatcher` because Task 1 added `Dispatch`. Confirm with `go build`.

- [ ] **Step 2: Build + manual smoke**

Run: `go build ./... && go vet ./...`
Expected: clean. (A live tick is covered by Task 4 unit tests + Task 8 integration; no model call here.)

- [ ] **Step 3: Commit**

```bash
git add cmd/shell3/telegram.go
git commit -m "feat(cmd): run shell3.cron jobs in the telegram host"
```

---

## Task 6: `/run <job>` bot command (manual fire)

**Files:**
- Modify: `internal/telegram/commands.go` (add `/run`, `BotCommands()` entry)
- Modify: `internal/telegram/bot.go` (hold a manual-fire hook)
- Modify: `cmd/shell3/telegram.go` (wire the hook to the scheduler)
- Create/Modify: `internal/telegram/commands_test.go`

`/run <job>` fires a configured job immediately via the scheduler (reusing `Dispatch`), so jobs are testable from the phone.

- [ ] **Step 1: Write the failing test** in `internal/telegram/commands_test.go`

```go
func TestCommand_Run(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42, "")
	fired := ""
	b.SetJobRunner(func(name string) error { fired = name; return nil })
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/run nightly"})
	if fired != "nightly" {
		t.Fatalf("expected /run to fire job 'nightly', fired %q", fired)
	}
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "nightly") {
		t.Fatalf("expected an ack mentioning the job, got %v", fc.sentTexts())
	}
}

func TestCommand_RunNoRunner(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42, "")
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/run x"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "no scheduled jobs") {
		t.Fatalf("expected a no-jobs reply, got %v", fc.sentTexts())
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/telegram/ -run TestCommand_Run -v`
Expected: FAIL — `b.SetJobRunner` / `/run` undefined.

- [ ] **Step 3: Add the hook** in `internal/telegram/bot.go`

Add a field to `Bot` (beside `onUsage`):
```go
	runJob func(name string) error // fires a cron job by name; nil if no scheduler
```
Add the setter:
```go
// SetJobRunner wires /run <job> to the scheduler's manual fire.
func (b *Bot) SetJobRunner(fn func(name string) error) { b.runJob = fn }
```

- [ ] **Step 4: Add the command** in `internal/telegram/commands.go`

In the `switch cmd` of `handleCommand`, add:
```go
	case "/run":
		if b.runJob == nil {
			b.sendReply(ctx, "no scheduled jobs configured")
			return
		}
		name := strings.TrimSpace(arg)
		if name == "" {
			b.sendReply(ctx, "usage: /run <job>")
			return
		}
		if err := b.runJob(name); err != nil {
			b.sendReply(ctx, "run failed: "+err.Error())
			return
		}
		b.sendReply(ctx, "▶️ fired job "+name)
```
Add to `BotCommands()`:
```go
		{"run", "Run a scheduled job now: /run <name>"},
```

- [ ] **Step 5: Add manual fire to the scheduler** in `internal/cron/scheduler.go`
```go
// Run fires a job by name immediately. Returns an error if the name is unknown.
func (s *Scheduler) Run(name string) error {
	for _, j := range s.jobs {
		if j.Name == name {
			s.fire(j)
			return nil
		}
	}
	return fmt.Errorf("no job named %q", name)
}
```

- [ ] **Step 6: Wire it** in `cmd/shell3/telegram.go` (after `sched.Start()`):
```go
			b.SetJobRunner(sched.Run)
```

- [ ] **Step 7: Run to verify pass + build**

Run: `go test ./internal/telegram/ -run TestCommand_Run -v && go build ./...`
Expected: PASS + clean.

- [ ] **Step 8: Commit**

```bash
git add internal/telegram/ internal/cron/scheduler.go cmd/shell3/telegram.go
git commit -m "feat(telegram): /run <job> fires a scheduled job on demand"
```

---

## Task 7: Dashboard — Cron tab

**Files:**
- Modify: `internal/telegram/web/server.go` (`/api/cron` + a cron source seam)
- Modify: `internal/telegram/web/static/index.html` (Cron tab)
- Modify: `internal/telegram/web/happy_path_test.go` (auth + shape test)
- Modify: `cmd/shell3/telegram.go` (wire the scheduler into the server)

- [ ] **Step 1: Add the endpoint + seam** in `internal/telegram/web/server.go`

Add a field to `Server`:
```go
	cron func() []cronJob // nil → no jobs
```
Add the JSON type + setter + handler + route:
```go
type cronJob struct {
	Name      string `json:"name"`
	Schedule  string `json:"schedule"`
	Agent     string `json:"agent"`
	Notify    bool   `json:"notify"`
	LastRun   string `json:"last_run,omitempty"`
	LastSubID string `json:"last_sub_id,omitempty"`
}

// SetCronSource attaches a provider of cron job statuses for /api/cron.
func (s *Server) SetCronSource(fn func() []cronJob) { s.cron = fn }

func (s *Server) handleCron(w http.ResponseWriter, r *http.Request) {
	var out []cronJob
	if s.cron != nil {
		out = s.cron()
	}
	if out == nil {
		out = []cronJob{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
```
Register in `Handler()`:
```go
	mux.HandleFunc("/api/cron", s.auth(s.handleCron))
```
> `cronJob` is the web layer's own DTO; the host (Task 7 Step 4) adapts `cron.Scheduler.Jobs()` (`[]cron.JobStatus`) into `[]web.cronJob`. Since the field names/JSON match, the adapter is a trivial copy loop — keep web independent of the `internal/cron` package.

- [ ] **Step 2: Write the test** in `internal/telegram/web/happy_path_test.go`

```go
func TestCron_AuthAndShape(t *testing.T) {
	const token = "test-bot-token"
	const chatID int64 = 8701499393
	rt := shell3.NewRuntimeForTest(t, "ok")
	sess, _ := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	srv := NewServer(rt, sess, token, chatID)
	srv.SetCronSource(func() []cronJob {
		return []cronJob{{Name: "nightly", Schedule: "0 9 * * *", Agent: "explorer", Notify: true}}
	})
	signed := signInitData(t, token, `{"id":8701499393,"first_name":"T"}`)

	// gated
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/cron", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
	// authed
	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/cron", nil)
	req.Header.Set("X-Init-Data", signed)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"nightly"`) {
		t.Fatalf("cron: got %d %q", rr.Code, rr.Body.String())
	}
}
```

- [ ] **Step 3: Run to verify fail → implement → pass**

Run: `go test ./internal/telegram/web/ -run TestCron -v`
Expected: FAIL then (after Step 1) PASS.

- [ ] **Step 4: Wire the scheduler into the server** in `cmd/shell3/telegram.go`

Where the dashboard `srv` is built (inside the `tg.Dashboard.Enabled` block), and when `sched != nil`, attach the source:
```go
				if sched != nil {
					srv.SetCronSource(func() []web.cronJob {
						var out []web.cronJob
						for _, j := range sched.Jobs() {
							out = append(out, web.cronJob{
								Name: j.Name, Schedule: j.Schedule, Agent: j.Agent,
								Notify: j.Notify, LastRun: j.LastRun, LastSubID: j.LastSubID,
							})
						}
						return out
					})
				}
```
> `web.cronJob` must be exported for the host to construct it. **Rename `cronJob` → `CronJob` in `server.go`** (and the setter signature to `func() []CronJob`) so `cmd/shell3` can build it. Update Step 1/Step 2 names accordingly. (Order the host code so `srv.SetCronSource` is set before `srv.Handler()` is served.)

- [ ] **Step 5: Add the Cron tab** in `internal/telegram/web/static/index.html`

Add the nav button after the Past button:
```html
  <button data-tab="cron">Cron</button>
```
Add the view section after the `past` section:
```html
  <section id="cron" class="view"><div class="empty">loading…</div></section>
```
Add `cron: document.getElementById("cron")` to the `views` map. Add a render function (static data — load on tab switch only, like Past):
```javascript
async function renderCron() {
  const jobs = await api("/api/cron");
  views.cron.innerHTML = jobs.length ? jobs.map(j => `
    <div class="card">
      <div class="row"><span class="agent">${esc(j.name)}</span><span class="badge ${j.notify ? "finished" : "running"}">${j.notify ? "notify" : "quiet"}</span></div>
      <div class="task">${esc(j.agent)} · <code>${esc(j.schedule)}</code></div>
      <div class="preview">${j.last_run ? "last run: " + esc(j.last_run) : "not run yet"}</div>
    </div>`).join("") : '<div class="empty">No scheduled jobs.</div>';
}
```
In `refresh(force)`, add: `else if (active === "cron" && force) await renderCron();` (load on switch, not every poll).

- [ ] **Step 6: Build + verify served**

Run: `go build ./internal/telegram/web/ && go test ./internal/telegram/web/`
Expected: clean + green. Manually: `curl -s localhost:8765/ | grep -c 'data-tab="cron"'` → 1 (when running).

- [ ] **Step 7: Commit**

```bash
git add internal/telegram/web/ cmd/shell3/telegram.go
git commit -m "feat(telegram/web): dashboard Cron tab (jobs + last run)"
```

---

## Task 8: Scaffold + documentation sweep

**Files:**
- Modify: `internal/scaffold/defaults/base/shell3.lua.tmpl` (commented `cron{}` example)
- Modify: `CHANGELOG.md`
- Modify: `docs/dev/superpowers/specs/2026-06-10-cron-scheduled-dispatch-design.md` (status → implemented)
- Modify: `internal/scaffold/scaffold_test.go` only if it asserts rendered content

- [ ] **Step 1:** Add a commented `cron` block to `shell3.lua.tmpl` (after the telegram block):
```lua
-- Scheduled jobs (run on the always-on `shell3 telegram` host). Each job
-- dispatches an isolated subagent; notify=false is a quiet background job whose
-- result shows only in the dashboard (errors still notify). Fire manually with
-- /run <name>.
-- shell3.cron({
--   jobs = {
--     { name="prs", schedule="0 9 * * *", agent="explorer", notify=true,
--       prompt="Summarize my open PRs and anything that needs review today." },
--     { name="tests", schedule="@hourly", agent="code", workdir="/path/to/repo", notify=false,
--       prompt="Run the test suite; if anything fails, summarize the failure." },
--   },
-- })
```

- [ ] **Step 2:** Add a `CHANGELOG.md` entry under `## [Unreleased] → ### Added` summarizing scheduled dispatch, explicitly noting v1 limits (in-process `robfig/cron`, no missed-while-down catch-up, jobs re-armed from config on restart, overlap allowed).

- [ ] **Step 3:** Update the cron spec header `Status:` to `implemented (2026-06-11)`.

- [ ] **Step 4:** Run `go test ./internal/scaffold/...`; fix any rendered-content assertion. The commented block won't break the loader (it's commented).

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/ CHANGELOG.md docs/dev/superpowers/specs/2026-06-10-cron-scheduled-dispatch-design.md
git commit -m "docs(cron): scaffold example, changelog, spec status"
```

---

## Self-Review

**Spec coverage:**
- `cron{jobs}` block + `[]CronJob{Name,Schedule,Agent,Prompt,WorkDir,Notify}` + agent cross-ref validation → Task 2. ✓
- `Session.Dispatch(agent, prompt, DispatchOpts{WorkDir,Label,Notify})` reusing the spawn path, depth-1, result→inbox→Wake → Task 1. ✓
- `notify` policy: true → deliver+wake; false success → transcript-only; false **error** → deliver+wake (loud on terminal failure, Retry ignored) → Task 1 (`notify || failed`, `Error` kind only). ✓
- `internal/cron.Scheduler` over `robfig/cron`, one entry per job, fail-fast on bad schedule, start/stop clean, per-job status → Task 4. ✓
- Host wiring + SIGINT stop-before-Close → Task 5. ✓
- `/run <job>` manual fire → Task 6. ✓
- Dashboard observability (quiet jobs visible) → Task 7 (Cron tab) + the existing Subagents-tab transcript reader already shows every dispatched subagent. ✓
- Documentation sweep (scaffold + CHANGELOG + spec status) → Task 8. ✓
- Config validation: unknown agent fails load, malformed schedule fails at `New` → Tasks 2 + 4. ✓

**Deviations made explicit:**
- Cron jobs gain an optional `name` (defaults `job-N`) not in the spec's example — needed for the `cron:<name>` label, `/run <name>`, and the dashboard. Documented in Task 2.
- `Dispatch` is a sibling of `spawn` (not a literal call to it) because `spawn` ignores `Error` events and always delivers; `Dispatch` needs terminal-error detection + conditional delivery. It reuses every helper (`nextSubID`, `Session`, `subs`, `trackSubagent`, `emit`) — no new lifecycle code. Documented in Task 1.
- Depth-1 from the host side is enforced by rejecting `Dispatch` on a `sub:`-named session (Task 1) since there is no model tool to strip.

**Placeholder scan:** External boundary (`robfig/cron/v3`) carries an explicit "verify with go doc" note (Task 0 + Task 4) with working best-known code, not a guess. No TODO/TBD. All referenced types (`CronJob`, `DispatchOpts`, `JobStatus`, `web.CronJob`, `Dispatcher`) are defined in a task before use.

**Type consistency:** `CronJob` fields `{Name,Schedule,Agent,Prompt,WorkDir,Notify}` are identical in luacfg (Task 2), pkg/shell3 (Task 3), and consumed unchanged by the scheduler (Task 4). `DispatchOpts{WorkDir,Label,Notify}` is defined in Task 1 and used identically in Tasks 4/5. The web DTO is `web.CronJob` (exported, Task 7 Step 4 rename).
