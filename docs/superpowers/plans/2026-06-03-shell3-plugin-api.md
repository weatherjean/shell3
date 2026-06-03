# shell3 Plugin API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `pkg/shell3` a full plugin front-end that behaves **identically to the TUI** — same config, store, memory, persona — exposing one persistent multi-turn `Session` (plus a one-shot `Run`) that streams structured events. Achieve this by extracting the CLI's config assembly into a shared `internal/agentsetup.Build` used by both the CLI and the plugin.

**Architecture:** Three front-ends over one shared core. `internal/agentsetup.Build(opts)` returns the full `chat.Config` (+ cleanup). `cmd/run.go` and `pkg/shell3` both call it. `pkg/shell3` mirrors `tui.RunInteractive`'s lifecycle (one `chat.Session`, one long-lived drain, turn boundaries via `TurnDone`/`Error`) but translates events onto a per-`Send` channel.

**Tech Stack:** Go; `internal/{paths,bootstrap,store,luacfg,adapter/openai,docs,agentsetup}`, `pkg/{chat,llm,llm/fakellm,persona,applog}`.

See the design spec: `docs/superpowers/specs/2026-06-03-shell3-plugin-api-design.md`.

---

## File Structure

- **Create** `internal/docs/docs.go` + move `cmd/shell3/shell3.md` → `internal/docs/shell3.md` — importable embedded docs.
- **Modify** `cmd/shell3/docs.go` — use `docs.Content`.
- **Create** `internal/agentsetup/agentsetup.go` — `Options` + `Build` + `resolveConfigPath` (the lifted assembly).
- **Modify** `cmd/shell3/run.go` — `runChat` shrinks to flags → `agentsetup.Build` → front-end dispatch.
- **Rewrite** `pkg/shell3/shell3.go` — `Spec`, `Event`, `Kind`, `translate`, `Session` (Start/Send/Close/ID/Clear/Rollback/SwitchModel), `Run`.
- **Rewrite** `pkg/shell3/shell3_test.go` — translate table + fakellm-driven Session tests.
- **Create** `internal/agentsetup/agentsetup_test.go` — Build happy/error paths.

The earlier minimal `buildConfig`/`runConfig` in `pkg/shell3/shell3.go` (commits c9ab242, 709c84c, 32983c5, 0c3ac47) are **superseded** — Task 4 rewrites the file.

---

## Task 1: Relocate embedded docs to `internal/docs`

**Files:**
- Create: `internal/docs/shell3.md` (moved from `cmd/shell3/shell3.md`)
- Create: `internal/docs/docs.go`
- Modify: `cmd/shell3/docs.go`

- [ ] **Step 1: Move the markdown and create the embed package**

```bash
git mv cmd/shell3/shell3.md internal/docs/shell3.md
```

Create `internal/docs/docs.go`:

```go
// Package docs embeds the shell3 documentation markdown so any package
// (the CLI's `docs` subcommand and the agentsetup builder) can read it
// without depending on package main.
package docs

import _ "embed"

//go:embed shell3.md
var Content string
```

- [ ] **Step 2: Point the docs subcommand at the new embed**

Replace the entire contents of `cmd/shell3/docs.go` with:

```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/docs"
)

func newDocsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "docs",
		Short: "Print shell3 documentation",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Print(docs.Content)
		},
	}
}
```

- [ ] **Step 3: Update run.go's reference**

In `cmd/shell3/run.go`, the field `Docs: docsContent,` (line ~238) currently reads a `package main` var. Change it to `Docs: docs.Content,` and add `"github.com/weatherjean/shell3/internal/docs"` to run.go's import block. (Task 2 moves this line into agentsetup; this interim change keeps the tree compiling between tasks.)

- [ ] **Step 4: Build + test**

Run: `go build ./... && go test ./cmd/... ./internal/docs/...`
Expected: clean build; `cmd` tests pass; `internal/docs` has no tests (`no test files` is fine).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor(docs): move embedded shell3.md to internal/docs"
```

---

## Task 2: Extract `internal/agentsetup.Build`

Lift the config assembly out of `runChat` into a shared builder. This is a near-verbatim move of `cmd/shell3/run.go` lines ~95–251 (the part between config-path resolution and the front-end dispatch), parameterized by `Options`.

**Files:**
- Create: `internal/agentsetup/agentsetup.go`
- Modify: `cmd/shell3/run.go`
- Test: `internal/agentsetup/agentsetup_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/agentsetup/agentsetup_test.go`:

```go
package agentsetup_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/agentsetup"
)

func TestBuild_MissingConfig_Errors(t *testing.T) {
	tmp := t.TempDir()
	_, _, err := agentsetup.Build(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    tmp,
	})
	if err == nil {
		t.Fatal("expected error for missing config, got nil")
	}
}

func TestBuild_LoadsConfig(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	writeMinimalConfig(t, tmp)

	cfg, cleanup, err := agentsetup.Build(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    home,
		Headless:   true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer cleanup()

	if cfg.LLM == nil {
		t.Error("cfg.LLM is nil")
	}
	if cfg.Personality.SystemPrompt == "" {
		t.Error("cfg.Personality.SystemPrompt is empty")
	}
	if cfg.WorkDir != tmp {
		t.Errorf("WorkDir = %q, want %q", cfg.WorkDir, tmp)
	}
}

// writeMinimalConfig writes a shell3.lua + .env that Build can load: one model
// referencing an env-injected key, and one agent selecting it.
func writeMinimalConfig(t *testing.T, dir string) {
	t.Helper()
	lua := `
shell3.model("main", {
  base_url = "https://example.test/v1",
  api_key = shell3.env.secret("TEST_KEY"),
  model_id = "test-model",
})
shell3.agent("tester", { model = "main" })
`
	if err := os.WriteFile(filepath.Join(dir, "shell3.lua"), []byte(lua), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TEST_KEY=sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
```

NOTE TO IMPLEMENTER: the exact Lua API (`shell3.model`, `shell3.agent`, field names) must match this project's `internal/luacfg` loader. Before writing the test, read one real example config (`internal/scaffold/` starter template, or an existing `internal/luacfg` test fixture) and adjust `writeMinimalConfig` to the actual schema. The test's intent — "a minimal valid config builds a Config with a non-nil LLM and non-empty system prompt" — is what matters; match the real Lua surface.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agentsetup/ -run TestBuild -v`
Expected: FAIL — package `agentsetup` does not exist.

- [ ] **Step 3: Write the builder**

Create `internal/agentsetup/agentsetup.go`. This is the lift of `runChat` lines ~95–251. Reproduce that code, replacing the locals `cwd`/`homeDir`/`f.outPath`/`headless` with `opts.CWD`/`opts.HomeDir`/`opts.OutPath`/`opts.Headless`, and `docsContent` with `docs.Content`. Return the assembled `chat.Config` and a cleanup that closes (in order) the store, the Lua state, and the log.

```go
// Package agentsetup is the shared config assembly used by every shell3
// front-end (the bubbletea TUI, the stdout one-shot, and the pkg/shell3 event
// stream). It resolves paths, ensures project dirs, opens the store and log,
// loads shell3.lua, and returns a fully-populated chat.Config — the single
// source of truth for "what the agent is", independent of how it's driven.
package agentsetup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/weatherjean/shell3/internal/adapter/openai"
	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/internal/docs"
	"github.com/weatherjean/shell3/internal/luacfg"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/store"
	"github.com/weatherjean/shell3/pkg/applog"
	"github.com/weatherjean/shell3/pkg/chat"
	"github.com/weatherjean/shell3/pkg/llm"
	"github.com/weatherjean/shell3/pkg/persona"
)

// Options parameterizes Build. CWD/HomeDir default via the caller (front-ends
// pass os.Getwd()/os.UserHomeDir()). ConfigPath "" triggers default resolution.
type Options struct {
	ConfigPath string
	CWD        string
	HomeDir    string
	Headless   bool
	OutPath    string
}

// Build assembles the full chat.Config. The returned cleanup closes the store,
// the Lua state, and the log; callers MUST invoke it.
func Build(opts Options) (chat.Config, func(), error) {
	noop := func() {}

	configPath, err := resolveConfigPath(opts.ConfigPath, opts.CWD, opts.HomeDir)
	if err != nil {
		return chat.Config{}, noop, err
	}

	g := paths.NewGlobal(opts.HomeDir)
	l := paths.NewLocal(opts.CWD)

	if err := bootstrap.EnsureGlobal(g); err != nil {
		return chat.Config{}, noop, err
	}
	uuid, err := bootstrap.EnsureProject(l, g, opts.CWD)
	if err != nil {
		return chat.Config{}, noop, err
	}

	const logMaxBytes = 2 * 1024 * 1024
	const logArchives = 3
	log, logCloser, err := applog.Open(g.LogFile, logMaxBytes, logArchives)
	if err != nil {
		log = applog.Noop{}
		logCloser = io.NopCloser(nil)
	}
	proj := paths.NewProject(g, uuid)

	lc, err := luacfg.Load(configPath, filepath.Dir(configPath))
	if err != nil {
		_ = logCloser.Close()
		return chat.Config{}, noop, err
	}

	buildClient := func(md luacfg.Model) (chat.LLMClient, llm.RequestParams) {
		cl := openai.NewClient(md.BaseURL, md.APIKey, md.ModelID)
		rp := llm.RequestParams{
			ReasoningEffort: md.Reasoning,
			MaxTokens:       md.MaxTokens,
			Temperature:     md.Temperature,
		}
		cl.SetParams(rp)
		if md.Extra != nil {
			cl.SetExtra(md.Extra)
		}
		return cl, rp
	}

	m, ok := lc.Model(lc.Agent.ModelName)
	if !ok {
		lc.Close()
		_ = logCloser.Close()
		return chat.Config{}, noop, fmt.Errorf("agent references unknown model %q", lc.Agent.ModelName)
	}
	client, rp := buildClient(m)

	var models []chat.ModelInfo
	for _, md := range lc.Models {
		models = append(models, chat.ModelInfo{
			Name:          md.Name,
			ModelID:       md.ModelID,
			ContextWindow: md.ContextWindow,
		})
	}
	switchModel := func(name string) (chat.ActiveModel, error) {
		md, ok := lc.Model(name)
		if !ok {
			return chat.ActiveModel{}, fmt.Errorf("unknown model %q", name)
		}
		cl, p := buildClient(md)
		return chat.ActiveModel{Client: cl, Params: p, ModelID: md.ModelID, ContextWindow: md.ContextWindow}, nil
	}

	var st *store.Store
	if lc.Agent.Gates.Memory || lc.Agent.Gates.History {
		if s, e := store.Open(proj.DB); e == nil {
			st = s
		} else {
			log.Warn("open store failed — memory and history unavailable", "error", e)
		}
	}

	var coreMemories []store.MemoryEntry
	if st != nil {
		if mems, e := st.MemoryQuery(true, 0); e != nil {
			log.Warn("load core memories failed", "error", e)
		} else {
			coreMemories = mems
		}
	}

	buildPrompt := func() string {
		return lc.BuildPersona(luacfg.RuntimeData{
			Time:         time.Now().Format("Mon Jan 2 2006, 15:04 MST"),
			CWD:          opts.CWD,
			Model:        m.ModelID,
			CoreMemories: coreMemories,
		})
	}

	customDefs := lc.CustomToolsFor(lc.Agent.CustomTools)
	hasSkills := lc.Agent.SkillsActive()
	toolDefs := luacfg.ToolDefs(lc.Agent.Gates, customDefs, hasSkills)

	pers := persona.Persona{
		Name:         lc.Agent.Name,
		SystemPrompt: buildPrompt(),
		Tools:        toolDefs,
		Parameters:   rp,
	}

	customNames := make(map[string]bool, len(lc.Agent.CustomTools))
	for _, n := range lc.Agent.CustomTools {
		customNames[n] = true
	}
	if hasSkills {
		customNames["skill"] = true
	}

	toolNames := make([]string, 0, len(toolDefs))
	for _, t := range toolDefs {
		toolNames = append(toolNames, t.Name)
	}

	cfg := chat.Config{
		LLM:             client,
		Store:           st,
		Personality:     pers,
		RefreshPrompt:   buildPrompt,
		WorkDir:         opts.CWD,
		StatusLine:      fmt.Sprintf("%s │ %s", lc.Agent.Name, m.ModelID),
		ModeLabel:       lc.Agent.Name,
		ProjectRef:      uuid,
		ActiveSkills:    lc.Agent.Skills,
		ActiveTools:     toolNames,
		ContextWindow:   m.ContextWindow,
		Docs:            docs.Content,
		CustomTool:      lc.CallTool,
		CustomToolNames: customNames,
		ToolGuard: func(ctx context.Context, t string, p map[string]any) (int, string, error) {
			d, r, e := lc.OnToolCall(ctx, t, p)
			return int(d), r, e
		},
		Params:   rp,
		Log:      log,
		OutPath:  opts.OutPath,
		Headless: opts.Headless,
		Models:   models,
		SwitchModel: switchModel,
	}

	cleanup := func() {
		if st != nil {
			_ = st.Close()
		}
		lc.Close()
		_ = logCloser.Close()
	}
	return cfg, cleanup, nil
}
```

Add `"context"` to the import block (used by the `ToolGuard` closure). Then move `resolveConfigPath` and its `fileExists` helper from `cmd/shell3/run.go` into this package (cut them from run.go):

```go
// resolveConfigPath returns the shell3.lua to load: explicit flag, else
// ./shell3.lua, else ~/.shell3/shell3.lua. Errors when nothing is found.
func resolveConfigPath(flag, cwd, homeDir string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	local := filepath.Join(cwd, "shell3.lua")
	if fileExists(local) {
		return local, nil
	}
	global := filepath.Join(homeDir, ".shell3", "shell3.lua")
	if fileExists(global) {
		return global, nil
	}
	return "", fmt.Errorf("no shell3.lua found — pass a config path or create ~/.shell3/shell3.lua")
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
```

(If `fileExists` exists elsewhere in `cmd` and is still used there, leave that copy; only ensure agentsetup has its own.)

- [ ] **Step 4: Refactor `cmd/shell3/run.go` to call Build**

In `runChat`, after computing `cwd`, `homeDir`, and `headless`, replace the entire assembly block (the old lines ~95–251, from `g := paths.NewGlobal(...)` through the `cfg := chat.Config{...}` literal) with:

```go
	cfg, cleanup, err := agentsetup.Build(agentsetup.Options{
		ConfigPath: f.configPath,
		CWD:        cwd,
		HomeDir:    homeDir,
		Headless:   headless,
		OutPath:    f.outPath,
	})
	if err != nil {
		return err
	}
	defer cleanup()
```

Keep the front-end dispatch:

```go
	if initialInput != "" {
		return tui.RunOnce(ctx, cfg, initialInput)
	}
	return tui.RunInteractive(ctx, cfg)
```

Delete the now-unused `resolveConfigPath` call site logic (config resolution now happens inside Build) — `runChat` no longer needs to call `resolveConfigPath` itself; pass `f.configPath` straight through. Remove now-unused imports from run.go (`paths`, `bootstrap`, `store`, `luacfg`, `openai`, `applog`, `persona`, `llm`, `time`, `docs`, and `resolveConfigPath`/`fileExists` if moved). Let the compiler/goimports tell you the exact final set. Keep `os`, `term`, `io`, `strings`, `fmt`, `cobra`, `context`, `tui`, `agentsetup`.

NOTE: `runChat` still needs `cwd`/`homeDir` for the `agentsetup.Options`, and the `headless` computation + `SHELL3_HEADLESS`/`SHELL3_OUT` env setting stays in run.go (front-end concern).

- [ ] **Step 5: Run tests**

Run: `go test ./internal/agentsetup/ ./cmd/... -v 2>&1 | tail -20 && go build ./...`
Expected: `agentsetup` tests pass; `cmd` tests pass (CLI behavior unchanged); clean build.

- [ ] **Step 6: Full regression (CLI must be unchanged)**

Run: `go test ./...`
Expected: all packages pass.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor(agentsetup): extract shared config builder from CLI runChat"
```

---

## Task 3: Expand the public Event + translate

Rewrite the event surface in `pkg/shell3/shell3.go` to the 8-kind design with the new `ToolInput` and token fields. (This replaces the current 4-kind version.)

**Files:**
- Modify: `pkg/shell3/shell3.go`
- Test: `pkg/shell3/shell3_test.go`

- [ ] **Step 1: Write the failing test**

Replace the entire contents of `pkg/shell3/shell3_test.go` with:

```go
package shell3

import (
	"testing"

	"github.com/weatherjean/shell3/pkg/chat"
)

func TestTranslate(t *testing.T) {
	cases := []struct {
		name string
		in   chat.Event
		want *Event // nil = dropped
	}{
		{"token", chat.Event{Kind: chat.EventAssistantToken, Text: "hi"}, &Event{Kind: Token, Text: "hi"}},
		{"reasoning", chat.Event{Kind: chat.EventAssistantReasoning, Text: "think"}, &Event{Kind: Reasoning, Text: "think"}},
		{"tool call", chat.Event{Kind: chat.EventToolCall, ToolName: "bash", ToolInput: `{"cmd":"ls"}`}, &Event{Kind: ToolCall, ToolName: "bash", ToolInput: `{"cmd":"ls"}`}},
		{"tool result", chat.Event{Kind: chat.EventToolResult, ToolName: "bash", ToolOutput: "ok"}, &Event{Kind: ToolResult, ToolName: "bash", ToolOutput: "ok"}},
		{"usage", chat.Event{Kind: chat.EventUsage, Usage: &chat.EventUsageData{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}, &Event{Kind: Usage, PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}},
		{"done", chat.Event{Kind: chat.EventTurnDone, Usage: &chat.EventUsageData{PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28}}, &Event{Kind: Done, PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28}},
		{"retry", chat.Event{Kind: chat.EventRetry, Text: "retrying"}, &Event{Kind: Retry, Text: "retrying"}},
		{"error", chat.Event{Kind: chat.EventError, Text: "boom"}, &Event{Kind: Error}},
		{"session start dropped", chat.Event{Kind: chat.EventSessionStart}, nil},
		{"user message dropped", chat.Event{Kind: chat.EventUserMessage}, nil},
		{"assistant message dropped", chat.Event{Kind: chat.EventAssistantMessage}, nil},
		{"system reminder dropped", chat.Event{Kind: chat.EventSystemReminder}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := translate(tc.in)
			if tc.want == nil {
				if ok {
					t.Fatalf("expected drop, got %+v", got)
				}
				return
			}
			if !ok {
				t.Fatal("expected event, got drop")
			}
			if got.Kind != tc.want.Kind || got.Text != tc.want.Text ||
				got.ToolName != tc.want.ToolName || got.ToolInput != tc.want.ToolInput ||
				got.ToolOutput != tc.want.ToolOutput || got.PromptTokens != tc.want.PromptTokens ||
				got.CompletionTokens != tc.want.CompletionTokens || got.TotalTokens != tc.want.TotalTokens {
				t.Fatalf("translate(%+v) = %+v, want %+v", tc.in, got, *tc.want)
			}
			if tc.want.Kind == Error && (got.Err == nil || got.Err.Error() != tc.in.Text) {
				t.Fatalf("error: got Err=%v want %q", got.Err, tc.in.Text)
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/shell3/ -run TestTranslate`
Expected: FAIL — `undefined: Reasoning` / `ToolInput` / `PromptTokens` etc.

- [ ] **Step 3: Implement the expanded types + translate**

In `pkg/shell3/shell3.go`, replace the `Kind` const block, the `Event` struct, and `translate` with:

```go
// Kind discriminates a streamed Event.
type Kind int

const (
	Token      Kind = iota // assistant text       → Text
	Reasoning              // thinking text         → Text
	ToolCall               // tool started          → ToolName, ToolInput
	ToolResult             // tool finished         → ToolName, ToolOutput
	Usage                  // per-roundtrip tokens  → PromptTokens/CompletionTokens/TotalTokens
	Retry                  // transient retry       → Text
	Error                  // turn error            → Err
	Done                   // turn end (normal)     → token fields (final totals)
)

// Event is one item streamed on a Send/Run channel. Only the fields named for a
// given Kind are populated.
type Event struct {
	Kind             Kind
	Text             string // Token, Reasoning, Retry
	ToolName         string // ToolCall, ToolResult
	ToolInput        string // ToolCall (raw JSON args)
	ToolOutput       string // ToolResult
	PromptTokens     int    // Usage, Done
	CompletionTokens int    // Usage, Done
	TotalTokens      int    // Usage, Done
	Err              error  // Error
}

// translate maps an internal chat.Event to a public Event. ok is false when the
// internal event has no public equivalent (session lifecycle, echoed user
// message, post-stream assistant message, injected reminders).
func translate(ev chat.Event) (Event, bool) {
	switch ev.Kind {
	case chat.EventAssistantToken:
		return Event{Kind: Token, Text: ev.Text}, true
	case chat.EventAssistantReasoning:
		return Event{Kind: Reasoning, Text: ev.Text}, true
	case chat.EventToolCall:
		return Event{Kind: ToolCall, ToolName: ev.ToolName, ToolInput: ev.ToolInput}, true
	case chat.EventToolResult:
		return Event{Kind: ToolResult, ToolName: ev.ToolName, ToolOutput: ev.ToolOutput}, true
	case chat.EventUsage:
		return usageEvent(Usage, ev), true
	case chat.EventTurnDone:
		return usageEvent(Done, ev), true
	case chat.EventRetry:
		return Event{Kind: Retry, Text: ev.Text}, true
	case chat.EventError:
		return Event{Kind: Error, Err: errors.New(ev.Text)}, true
	default:
		return Event{}, false
	}
}

func usageEvent(k Kind, ev chat.Event) Event {
	e := Event{Kind: k}
	if ev.Usage != nil {
		e.PromptTokens = ev.Usage.PromptTokens
		e.CompletionTokens = ev.Usage.CompletionTokens
		e.TotalTokens = ev.Usage.TotalTokens
	}
	return e
}
```

Ensure `"errors"` and `"github.com/weatherjean/shell3/pkg/chat"` are imported. (The `Spec` type and `Session`/`Start`/etc. land in Task 4; the file may not fully build until then — that's fine, but `translate` + types must compile. If the leftover Task-1/2 `buildConfig`/`runConfig`/`Run` reference removed symbols, comment them out or delete them now; Task 4 rewrites them anyway.)

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./pkg/shell3/ -run TestTranslate -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```bash
git add pkg/shell3/shell3.go pkg/shell3/shell3_test.go
git commit -m "feat(shell3): expand public Event to 8 kinds (reasoning, tool-call, usage, retry)"
```

---

## Task 4: Plugin front-end — `Session` (Start/Send/Close/ID) + `Run`

Rewrite `pkg/shell3/shell3.go` to add the persistent multi-turn `Session` over `agentsetup.Build`, mirroring `tui.RunInteractive`'s lifecycle.

**Files:**
- Modify: `pkg/shell3/shell3.go` (add Spec, Session, Start, Send, Close, ID, Run, turnConfig)
- Test: `pkg/shell3/shell3_test.go` (append)

- [ ] **Step 1: Write the failing tests**

Append to `pkg/shell3/shell3_test.go` (and extend the import block to add `context`, `os`, `path/filepath`, `llm`, `fakellm`):

```go
// newTestSession builds a Session backed by a fakellm client, bypassing
// agentsetup so the test needs no real config/network. It mirrors what Start
// produces: a persistent chat.Session + drain over a fake-LLM chat.Config.
func newTestSession(t *testing.T, client chat.LLMClient, cfg chat.Config) *Session {
	t.Helper()
	cfg.LLM = client
	if cfg.WorkDir == "" {
		cfg.WorkDir = t.TempDir()
	}
	if cfg.Personality.Name == "" {
		cfg.Personality.Name = "test"
	}
	return newSession(cfg, func() {})
}

func TestSession_MultiTurn_HistoryCarries(t *testing.T) {
	client := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "first"}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "second"}}},
	)
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	collect := func(ch <-chan Event) (text string, done bool) {
		for ev := range ch {
			switch ev.Kind {
			case Token:
				text += ev.Text
			case Done:
				done = true
			}
		}
		return
	}

	t1, d1 := collect(s.Send(context.Background(), "hello"))
	if t1 != "first" || !d1 {
		t.Fatalf("turn 1: text=%q done=%v", t1, d1)
	}
	t2, d2 := collect(s.Send(context.Background(), "again"))
	if t2 != "second" || !d2 {
		t.Fatalf("turn 2: text=%q done=%v", t2, d2)
	}
	// Two user turns + two assistant replies must be retained.
	if got := len(s.sess.Messages()); got < 4 {
		t.Fatalf("history has %d messages, want >= 4 (2 turns)", got)
	}
}

func TestSession_ErrorPath(t *testing.T) {
	client := fakellm.New(fakellm.Script{Err: errors.New("provider down")})
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	var sawError, sawDone bool
	for ev := range s.Send(context.Background(), "hi") {
		switch ev.Kind {
		case Error:
			sawError = true
		case Done:
			sawDone = true
		}
	}
	if !sawError {
		t.Fatal("expected Error event")
	}
	if sawDone {
		t.Fatal("did not expect Done on error path")
	}
}

func TestRun_BadConfig_Errors(t *testing.T) {
	tmp := t.TempDir()
	ch, err := Run(context.Background(), Spec{
		Prompt:     "hi",
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		WorkDir:    tmp,
	})
	if err == nil {
		t.Fatal("expected error for missing config")
	}
	if ch != nil {
		t.Fatal("expected nil channel on start failure")
	}
}
```

(The `os` import is for any future use; if goimports flags it unused, drop it.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/shell3/ -run 'TestSession|TestRun_BadConfig'`
Expected: FAIL — `undefined: newSession` / `Session` / `Spec` / `Run`.

- [ ] **Step 3: Implement Spec + Session + Start + Send + Close + ID + Run**

In `pkg/shell3/shell3.go`, remove any leftover `buildConfig`/`runConfig` from earlier tasks and add:

```go
// Spec configures Run / Start. Prompt is used by Run only.
type Spec struct {
	Prompt     string
	ConfigPath string // "" → ./shell3.lua then ~/.shell3/shell3.lua
	WorkDir    string // "" → os.Getwd()
}

// Session is a live, multi-turn conversation — the plugin equivalent of an open
// TUI. It wraps one persistent chat.Session and the full agent config, and
// streams a per-Send channel of translated Events. Drain a Send channel to
// completion before the next Send/Clear/Rollback/SwitchModel.
type Session struct {
	cfg       chat.Config
	sess      *chat.Session
	handlers  map[string]chat.ToolHandler
	cleanup   func()
	drainDone chan struct{}

	mu  sync.Mutex
	cur chan Event // current Send's channel; nil between turns
}

// Start loads the config (identically to the TUI), starts the store session,
// and launches the event drain. A non-nil error means startup failed and no
// Session was created.
func Start(ctx context.Context, spec Spec) (*Session, error) {
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

	cfg, cleanup, err := agentsetup.Build(agentsetup.Options{
		ConfigPath: spec.ConfigPath,
		CWD:        workDir,
		HomeDir:    homeDir,
		Headless:   true,
	})
	if err != nil {
		return nil, err
	}
	return newSession(cfg, cleanup), nil
}

// newSession wires a Session around an already-built chat.Config and starts the
// drain. Split out from Start so tests can inject a fakellm-backed config.
func newSession(cfg chat.Config, cleanup func()) *Session {
	var storeID int64
	if cfg.Store != nil {
		if id, err := cfg.Store.StartSession(); err == nil {
			storeID = id
		}
	}
	sess := chat.NewSession(chat.SessionOpts{
		BufSize:          256,
		StoreID:          storeID,
		ContextWindowFor: func(string) int { return cfg.ContextWindow },
	})
	s := &Session{
		cfg:       cfg,
		sess:      sess,
		handlers:  chat.NewHandlers(cfg),
		cleanup:   cleanup,
		drainDone: make(chan struct{}),
	}
	go s.drain()
	return s
}

// drain is the single long-lived consumer of sess.Events(), routing translated
// events to the current Send channel and closing it on Done/Error.
func (s *Session) drain() {
	defer close(s.drainDone)
	for ev := range s.sess.Events() {
		pub, ok := translate(ev)
		if !ok {
			continue
		}
		s.mu.Lock()
		cur := s.cur
		s.mu.Unlock()
		if cur == nil {
			continue
		}
		cur <- pub
		if pub.Kind == Done || pub.Kind == Error {
			s.mu.Lock()
			close(s.cur)
			s.cur = nil
			s.mu.Unlock()
		}
	}
}

// Send runs one turn for prompt and returns a channel of that turn's events,
// closed after the turn's Done (or Error). The caller MUST drain it before the
// next Send.
func (s *Session) Send(ctx context.Context, prompt string) <-chan Event {
	out := make(chan Event)
	s.mu.Lock()
	s.cur = out
	s.mu.Unlock()
	tc := s.turnConfig()
	go s.sess.Run(ctx, tc, prompt)
	return out
}

// ID returns the store session id (rolls on compaction; "0" with no store).
func (s *Session) ID() string {
	return fmt.Sprintf("%d", s.sess.ID())
}

// Close ends the conversation: stops the drain, ends the store session, and
// releases the config (store, Lua, log).
func (s *Session) Close() error {
	s.sess.End("ok")
	s.sess.CloseEvents()
	<-s.drainDone
	if s.cfg.Store != nil {
		_ = s.cfg.Store.EndSession(s.sess.ID())
	}
	s.cleanup()
	return nil
}

// turnConfig derives the per-turn config from the current cfg. Built fresh each
// turn so SwitchModel's mutations to cfg take effect on the next Send.
func (s *Session) turnConfig() chat.TurnConfig {
	return chat.TurnConfig{
		LLM:             s.cfg.LLM,
		Personality:     s.cfg.Personality,
		StatusLine:      s.cfg.StatusLine,
		WorkDir:         s.cfg.WorkDir,
		Store:           s.cfg.Store,
		Truncate:        s.cfg.Truncate,
		Handlers:        s.handlers,
		Log:             chat.LogOrNoop(s.cfg.Log),
		Headless:        true,
		CustomTool:      s.cfg.CustomTool,
		CustomToolNames: s.cfg.CustomToolNames,
		ToolGuard:       s.cfg.ToolGuard,
		ShellInteractive: func(ctx context.Context, cmd, workdir string) string {
			return "error: interactive TTY not available in plugin mode"
		},
	}
}

// Run is the one-shot convenience: Start, send spec.Prompt, stream the turn,
// and Close when it drains. A non-nil error means startup failed.
func Run(ctx context.Context, spec Spec) (<-chan Event, error) {
	s, err := Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	turn := s.Send(ctx, spec.Prompt)
	out := make(chan Event)
	go func() {
		defer close(out)
		defer s.Close()
		for ev := range turn {
			out <- ev
		}
	}()
	return out, nil
}
```

Update the import block to include: `context`, `errors`, `fmt`, `os`, `sync`, `github.com/weatherjean/shell3/internal/agentsetup`, `github.com/weatherjean/shell3/pkg/chat`. (`path/filepath` is no longer needed here — resolution lives in agentsetup.)

- [ ] **Step 4: Run the package tests**

Run: `go test ./pkg/shell3/ -race -v 2>&1 | tail -30`
Expected: PASS — `TestTranslate`, `TestSession_MultiTurn_HistoryCarries`, `TestSession_ErrorPath`, `TestRun_BadConfig_Errors`; no data races.

- [ ] **Step 5: Commit**

```bash
git add pkg/shell3/shell3.go pkg/shell3/shell3_test.go
git commit -m "feat(shell3): persistent multi-turn Session over agentsetup; one-shot Run"
```

---

## Task 5: Session hooks — Clear / Rollback / SwitchModel

Expose the TUI slash-command behaviors as `Session` methods.

**Files:**
- Modify: `pkg/shell3/shell3.go`
- Test: `pkg/shell3/shell3_test.go` (append)

- [ ] **Step 1: Write the failing tests**

Append to `pkg/shell3/shell3_test.go`:

```go
func TestSession_Clear_ResetsHistory(t *testing.T) {
	client := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "a"}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "b"}}},
	)
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	for range s.Send(context.Background(), "first") {
	}
	if len(s.sess.Messages()) == 0 {
		t.Fatal("expected history after first turn")
	}
	s.Clear()
	if got := len(s.sess.Messages()); got != 0 {
		t.Fatalf("after Clear: %d messages, want 0", got)
	}
}

func TestSession_Rollback(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "x"}}})
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	if s.Rollback() {
		t.Fatal("Rollback on empty history should return false")
	}
	for range s.Send(context.Background(), "hi") {
	}
	if !s.Rollback() {
		t.Fatal("Rollback after a turn should return true")
	}
	if got := len(s.sess.Messages()); got != 0 {
		t.Fatalf("after Rollback: %d messages, want 0", got)
	}
}

func TestSession_SwitchModel_Unknown(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "x"}}})
	cfg := chat.Config{
		SwitchModel: func(name string) (chat.ActiveModel, error) {
			return chat.ActiveModel{}, errors.New("unknown model " + name)
		},
	}
	s := newTestSession(t, client, cfg)
	defer s.Close()

	if err := s.SwitchModel("nope"); err == nil {
		t.Fatal("expected error for unknown model")
	}
}

func TestSession_SwitchModel_Applies(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "x"}}})
	newClient := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "y"}}})
	cfg := chat.Config{
		SwitchModel: func(name string) (chat.ActiveModel, error) {
			return chat.ActiveModel{Client: newClient, ModelID: "m2", ContextWindow: 1000}, nil
		},
	}
	s := newTestSession(t, client, cfg)
	defer s.Close()

	if err := s.SwitchModel("m2"); err != nil {
		t.Fatalf("SwitchModel: %v", err)
	}
	if s.cfg.LLM != chat.LLMClient(newClient) {
		t.Fatal("SwitchModel did not swap the active client")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/shell3/ -run TestSession_Clear -run TestSession_Rollback -run TestSession_SwitchModel`
Expected: FAIL — `undefined: (*Session).Clear` etc.

- [ ] **Step 3: Implement the hooks**

Add to `pkg/shell3/shell3.go`:

```go
// Clear resets the conversation context (= /clear): drops all history and
// re-stamps the system prompt with a fresh timestamp.
func (s *Session) Clear() {
	s.sess.SetMessages(nil)
	if s.cfg.RefreshPrompt != nil {
		s.cfg.Personality.SystemPrompt = s.cfg.RefreshPrompt()
	}
}

// Rollback drops the last turn from context (= /rollback). Returns false when
// there was nothing to remove.
func (s *Session) Rollback() bool {
	msgs := s.sess.Messages()
	pruned := chat.PruneLastTurn(msgs)
	if len(pruned) == len(msgs) {
		return false
	}
	s.sess.SetMessages(pruned)
	return true
}

// SwitchModel activates the configured model named name for subsequent Sends
// (= /model <name>). Returns an error for an unknown model or when the config
// declares no models.
func (s *Session) SwitchModel(name string) error {
	if s.cfg.SwitchModel == nil {
		return fmt.Errorf("no models configured")
	}
	am, err := s.cfg.SwitchModel(name)
	if err != nil {
		return err
	}
	s.cfg.LLM = am.Client
	s.cfg.Params = am.Params
	s.cfg.ContextWindow = am.ContextWindow
	s.cfg.StatusLine = fmt.Sprintf("%s │ %s", s.cfg.ModeLabel, am.ModelID)
	return nil
}
```

NOTE: these mutate `s.cfg` and must not race with an in-flight turn. They are documented to be called between turns (after draining a Send channel), same as the TUI runs slash commands between turns. No extra locking needed under that contract.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./pkg/shell3/ -race -v 2>&1 | tail -30`
Expected: PASS for all `TestSession_*`, `TestTranslate`, `TestRun_BadConfig_Errors`; no races.

- [ ] **Step 5: Commit**

```bash
git add pkg/shell3/shell3.go pkg/shell3/shell3_test.go
git commit -m "feat(shell3): expose Clear/Rollback/SwitchModel on Session"
```

---

## Task 6: Full regression sweep

**Files:** none (verification only)

- [ ] **Step 1: Build**

Run: `make build`
Expected: clean.

- [ ] **Step 2: Test everything (with race)**

Run: `go test -race ./...`
Expected: all packages pass (or `no test files`).

- [ ] **Step 3: Vet**

Run: `go vet ./...`
Expected: no diagnostics.

- [ ] **Step 4: Confirm the CLI still resolves config + runs**

Run: `go run ./cmd/shell3 --help 2>&1 | head -20`
Expected: help text prints (the binary builds and the command tree is intact).

---

## Self-Review

**Spec coverage:**
- Shared builder (paths, bootstrap, log, store, core memories, persona w/ timestamp, docs) → Task 2 `agentsetup.Build`. ✓
- CLI delegates to the builder, behavior unchanged → Task 2 Steps 4–6. ✓
- Docs importable by both → Task 1. ✓
- 8-kind Event (+ ToolInput, token fields) + translate → Task 3. ✓
- Persistent multi-turn Session mirroring RunInteractive (one chat.Session, one drain, TurnDone/Error boundaries) → Task 4. ✓
- ID() = store session id; Run one-shot; error contract; Lua/store/log lifecycle → Task 4. ✓
- Clear/Rollback/SwitchModel hooks → Task 5. ✓
- Tests: Build, translate table, multi-turn history, error path, one-shot Run, hooks → Tasks 2–5. ✓
- In-scope items (store/memory/persistence/compaction drill-back/core memories) follow automatically from using the real config. ✓

**Placeholder scan:** The only intentional implementer-judgment note is Task 2 Step 1's `writeMinimalConfig` (match the real Lua schema by reading a scaffold/fixture first) — flagged explicitly, not a silent gap. No "TBD"/"similar to"/empty handlers.

**Type consistency:** `translate(chat.Event) (Event, bool)`, `usageEvent(Kind, chat.Event) Event`, `agentsetup.Build(Options) (chat.Config, func(), error)`, `newSession(chat.Config, func()) *Session`, `Start/Run(context.Context, Spec)`, `(*Session).{Send,ID,Close,Clear,Rollback,SwitchModel,turnConfig,drain}` are consistent across tasks. Internal signatures used (`store.Open`, `StartSession`/`EndSession`/`MemoryQuery`, `bootstrap.EnsureGlobal/EnsureProject`, `paths.NewGlobal/NewLocal/NewProject`, `applog.Open`, `chat.NewSession/NewHandlers/PruneLastTurn/LogOrNoop`, `chat.ActiveModel{Client,Params,ModelID,ContextWindow}`) all match verified source.
