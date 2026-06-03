# shell3 Plugin API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `pkg/shell3`'s leaky `New()`/`chat.Config` surface with a single public `Run(ctx, Spec) (<-chan Event, error)` that loads a `shell3.lua` config, runs one turn, and streams a slim public `Event` channel.

**Architecture:** `Run` = unexported `buildConfig(spec)` (today's `New` body) + unexported `runConfig(ctx, cfg, prompt, cleanup)` core that spins a `chat.Session`, runs one turn in a goroutine, and translates `chat.Event` → public `Event` on a channel. Splitting `runConfig` out from `Run` gives a seam to inject a `fakellm`-backed `chat.Config` in tests without a real config file or network. Changes are isolated to `pkg/shell3`; the CLI (`internal/tui/once.go`, `cmd/shell3/run.go`) is untouched.

**Tech Stack:** Go, `pkg/chat` (Session/TurnConfig/Event), `pkg/llm/fakellm` (test double), `pkg/persona`, `internal/luacfg`, `internal/adapter/openai`.

---

## File Structure

- `pkg/shell3/shell3.go` — **rewritten.** Public `Run`, `Spec`, `Event`, `Kind`. Unexported `buildConfig` (the old `New` body) and `runConfig` (session driver + translation). Old `New`/`Options` removed.
- `pkg/shell3/shell3_test.go` — **rewritten.** `TestRun_BadConfig_Errors` (replaces `TestNew_NoAuth_Errors`), plus `runConfig` tests driven by `fakellm`.

No other files change. `internal/tui/once.go` and `cmd/shell3/run.go` keep their rich-config path.

---

## Task 1: Public types + event translation

Define the public surface (`Spec`, `Event`, `Kind`) and the pure function that maps an internal `chat.Event` to a public `Event`. Pure mapping is unit-testable with hand-built events — no LLM, no config, no goroutine.

**Files:**
- Modify: `pkg/shell3/shell3.go` (replace entire file contents in this task with the types + `translate`; `buildConfig`/`runConfig`/`Run` are added in later tasks)
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
		want *Event // nil means "dropped"
	}{
		{
			name: "token",
			in:   chat.Event{Kind: chat.EventAssistantToken, Text: "hello"},
			want: &Event{Kind: Token, Text: "hello"},
		},
		{
			name: "tool result",
			in:   chat.Event{Kind: chat.EventToolResult, ToolName: "bash", ToolOutput: "ok"},
			want: &Event{Kind: ToolResult, ToolName: "bash", ToolOutput: "ok"},
		},
		{
			name: "error",
			in:   chat.Event{Kind: chat.EventError, Text: "boom"},
			want: &Event{Kind: Error},
		},
		{
			name: "turn done",
			in:   chat.Event{Kind: chat.EventTurnDone},
			want: &Event{Kind: Done},
		},
		{
			name: "reasoning dropped",
			in:   chat.Event{Kind: chat.EventAssistantReasoning, Text: "thinking"},
			want: nil,
		},
		{
			name: "tool call dropped",
			in:   chat.Event{Kind: chat.EventToolCall, ToolName: "bash"},
			want: nil,
		},
		{
			name: "usage dropped",
			in:   chat.Event{Kind: chat.EventUsage},
			want: nil,
		},
		{
			name: "session start dropped",
			in:   chat.Event{Kind: chat.EventSessionStart},
			want: nil,
		},
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
				t.Fatal("expected an event, got drop")
			}
			if got.Kind != tc.want.Kind || got.Text != tc.want.Text ||
				got.ToolName != tc.want.ToolName || got.ToolOutput != tc.want.ToolOutput {
				t.Fatalf("translate(%+v) = %+v, want %+v", tc.in, got, *tc.want)
			}
			if tc.want.Kind == Error {
				if got.Err == nil || got.Err.Error() != tc.in.Text {
					t.Fatalf("error event: got Err=%v, want %q", got.Err, tc.in.Text)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/shell3/ -run TestTranslate -v`
Expected: FAIL — compile error, `undefined: translate` / `undefined: Event` / `undefined: Token`.

- [ ] **Step 3: Write minimal implementation**

Replace the entire contents of `pkg/shell3/shell3.go` with:

```go
// Package shell3 embeds the shell3 coding agent as a library. Run loads a
// shell3.lua config, executes one turn for a prompt, and streams structured
// events back to the caller. It is the entire public surface; pkg/chat,
// pkg/persona, and pkg/llm are internal details.
package shell3

import (
	"errors"

	"github.com/weatherjean/shell3/pkg/chat"
)

// Spec configures a single Run. Prompt is required; the rest default.
type Spec struct {
	// Prompt is the user input for the single turn. Required.
	Prompt string
	// ConfigPath is the path to shell3.lua. Defaults to
	// ~/.shell3/shell3.lua when empty.
	ConfigPath string
	// WorkDir is the working directory for tool execution. Defaults to
	// os.Getwd() when empty.
	WorkDir string
}

// Kind discriminates a streamed Event.
type Kind int

const (
	// Token is a chunk of streamed assistant text. Text is set.
	Token Kind = iota
	// ToolResult reports a completed tool call. ToolName and ToolOutput are set.
	ToolResult
	// Error is a non-fatal turn error. Err is set. The turn still drains to Done.
	Error
	// Done marks the end of the turn. The channel closes immediately after.
	Done
)

// Event is one item streamed on the Run channel. Only the fields named for a
// given Kind are populated.
type Event struct {
	Kind       Kind
	Text       string // Kind == Token
	ToolName   string // Kind == ToolResult
	ToolOutput string // Kind == ToolResult
	Err        error  // Kind == Error
}

// translate maps an internal chat.Event to a public Event. The second return
// is false when the internal event has no public equivalent and should be
// dropped (reasoning, tool-call, usage, session, user/assistant-message,
// system-reminder, retry).
func translate(ev chat.Event) (Event, bool) {
	switch ev.Kind {
	case chat.EventAssistantToken:
		return Event{Kind: Token, Text: ev.Text}, true
	case chat.EventToolResult:
		return Event{Kind: ToolResult, ToolName: ev.ToolName, ToolOutput: ev.ToolOutput}, true
	case chat.EventError:
		return Event{Kind: Error, Err: errors.New(ev.Text)}, true
	case chat.EventTurnDone:
		return Event{Kind: Done}, true
	default:
		return Event{}, false
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/shell3/ -run TestTranslate -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```bash
git add pkg/shell3/shell3.go pkg/shell3/shell3_test.go
git commit -m "feat(shell3): public Spec/Event/Kind types + event translation"
```

---

## Task 2: `runConfig` session driver

Add the unexported core that takes a fully-built `chat.Config`, runs one turn, and returns a public `Event` channel. Injectable config = testable with `fakellm`, no config file or network.

**Files:**
- Modify: `pkg/shell3/shell3.go` (add `runConfig`; keep Task 1 contents)
- Test: `pkg/shell3/shell3_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `pkg/shell3/shell3_test.go`:

```go
func TestRunConfig_StreamsToDone(t *testing.T) {
	client := fakellm.New(fakellm.Script{
		Events: []llm.StreamEvent{
			{TextDelta: "hello"},
			{TextDelta: " world"},
		},
	})
	cfg := chat.Config{
		LLM:         client,
		Personality: persona.Persona{Name: "test"},
		WorkDir:     t.TempDir(),
	}

	var calls int
	events := runConfig(context.Background(), cfg, "hi", func() { calls++ })

	var text string
	var sawDone bool
	for ev := range events {
		switch ev.Kind {
		case Token:
			text += ev.Text
		case Done:
			sawDone = true
		}
	}
	if text != "hello world" {
		t.Fatalf("text = %q, want %q", text, "hello world")
	}
	if !sawDone {
		t.Fatal("never saw Done before channel closed")
	}
	if calls != 1 {
		t.Fatalf("cleanup called %d times, want 1", calls)
	}
}

func TestRunConfig_MapsToolResult(t *testing.T) {
	// First model call invokes a custom tool; second call ends the turn.
	client := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "1", Name: "echo_tool", RawArgs: "{}"}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "done"},
		}},
	)
	cfg := chat.Config{
		LLM: client,
		Personality: persona.Persona{
			Name:  "test",
			Tools: []llm.ToolDefinition{{Name: "echo_tool", Description: "echo"}},
		},
		WorkDir:         t.TempDir(),
		CustomTool:      func(ctx context.Context, name, args string) (string, error) { return "echoed", nil },
		CustomToolNames: map[string]bool{"echo_tool": true},
	}

	events := runConfig(context.Background(), cfg, "hi", func() {})

	var tools []Event
	for ev := range events {
		if ev.Kind == ToolResult {
			tools = append(tools, ev)
		}
	}
	if len(tools) != 1 {
		t.Fatalf("got %d ToolResult events, want 1", len(tools))
	}
	if tools[0].ToolName != "echo_tool" || tools[0].ToolOutput != "echoed" {
		t.Fatalf("tool event = %+v, want name=echo_tool output=echoed", tools[0])
	}
}
```

Update the import block at the top of `pkg/shell3/shell3_test.go` to:

```go
import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/pkg/chat"
	"github.com/weatherjean/shell3/pkg/llm"
	"github.com/weatherjean/shell3/pkg/llm/fakellm"
	"github.com/weatherjean/shell3/pkg/persona"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/shell3/ -run TestRunConfig -v`
Expected: FAIL — `undefined: runConfig`.

- [ ] **Step 3: Write minimal implementation**

Add the `runConfig` function to `pkg/shell3/shell3.go`, and add `"context"` to its import block (final import block becomes `"context"`, `"errors"`, and the `pkg/chat` import):

```go
// runConfig runs one turn against an already-built chat.Config and streams
// translated public Events. The returned channel is closed exactly once after
// a final Done event; cleanup runs after teardown (used by Run to close the
// Lua state). cfg.LLM is injectable, which is what makes this testable with
// fakellm.
func runConfig(ctx context.Context, cfg chat.Config, prompt string, cleanup func()) <-chan Event {
	out := make(chan Event)

	sess := chat.NewSession(chat.SessionOpts{BufSize: 256})
	tc := chat.TurnConfig{
		LLM:             cfg.LLM,
		Personality:     cfg.Personality,
		StatusLine:      cfg.StatusLine,
		WorkDir:         cfg.WorkDir,
		Truncate:        cfg.Truncate,
		Handlers:        chat.NewHandlers(cfg),
		Log:             chat.LogOrNoop(cfg.Log),
		Headless:        true,
		CustomTool:      cfg.CustomTool,
		CustomToolNames: cfg.CustomToolNames,
		ToolGuard:       cfg.ToolGuard,
		ShellInteractive: func(ctx context.Context, cmd, workdir string) string {
			return "error: interactive TTY not available in headless mode"
		},
	}

	go func() {
		sess.Run(ctx, tc, prompt)
		sess.CloseEvents()
	}()

	go func() {
		defer close(out)
		defer cleanup()
		for ev := range sess.Events() {
			if pub, ok := translate(ev); ok {
				out <- pub
			}
		}
	}()

	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/shell3/ -run TestRunConfig -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add pkg/shell3/shell3.go pkg/shell3/shell3_test.go
git commit -m "feat(shell3): runConfig session driver streaming public events"
```

---

## Task 3: `buildConfig` + public `Run`; remove old `New`

Fold the old `New` body into an unexported `buildConfig(spec)` and expose `Run` = `buildConfig` + `runConfig`. Delete `New`/`Options`.

**Files:**
- Modify: `pkg/shell3/shell3.go` (add `buildConfig` + `Run`)
- Test: `pkg/shell3/shell3_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `pkg/shell3/shell3_test.go`:

```go
func TestRun_BadConfig_Errors(t *testing.T) {
	// Point at a temp dir with no shell3.lua — Run must fail to start:
	// non-nil error AND nil channel (nothing ran).
	tmp := t.TempDir()
	ch, err := Run(context.Background(), Spec{
		Prompt:     "hi",
		ConfigPath: tmp + "/shell3.lua",
		WorkDir:    tmp,
	})
	if err == nil {
		t.Fatal("expected error for missing config, got nil")
	}
	if ch != nil {
		t.Fatal("expected nil channel on start failure")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/shell3/ -run TestRun_BadConfig -v`
Expected: FAIL — `undefined: Run`.

- [ ] **Step 3: Write minimal implementation**

In `pkg/shell3/shell3.go`, extend the import block to:

```go
import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/weatherjean/shell3/internal/adapter/openai"
	"github.com/weatherjean/shell3/internal/luacfg"
	"github.com/weatherjean/shell3/pkg/chat"
	"github.com/weatherjean/shell3/pkg/llm"
	"github.com/weatherjean/shell3/pkg/persona"
)
```

Add `buildConfig` and `Run` (this is the old `New` body, unexported and reshaped to take a `Spec`, plus the public wrapper). The `closeLua` cleanup is threaded into `runConfig`:

```go
// Run loads the config at spec.ConfigPath, runs one turn for spec.Prompt in
// spec.WorkDir, and streams translated Events on the returned channel.
//
// A non-nil error means Run failed to START (missing/invalid config, unknown
// model, missing key): nothing ran and the channel is nil. A nil error means
// the turn is underway; per-turn failures arrive as Event{Kind: Error}. The
// channel is closed exactly once after a final Done event. The caller's only
// obligation is to drain the channel until it closes.
func Run(ctx context.Context, spec Spec) (<-chan Event, error) {
	cfg, closeLua, err := buildConfig(spec)
	if err != nil {
		return nil, err
	}
	return runConfig(ctx, cfg, spec.Prompt, closeLua), nil
}

// buildConfig loads shell3.lua and assembles the minimal chat.Config for an
// embedded single-turn run: OpenAI-compatible adapter, persona system prompt,
// tool defs, custom-tool dispatch, and the guard chain. The returned cleanup
// closes the Lua state and is invoked by runConfig after the turn.
func buildConfig(spec Spec) (chat.Config, func(), error) {
	noop := func() {}

	workDir := spec.WorkDir
	if workDir == "" {
		w, err := os.Getwd()
		if err != nil {
			return chat.Config{}, noop, fmt.Errorf("get working directory: %w", err)
		}
		workDir = w
	}

	configPath := spec.ConfigPath
	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return chat.Config{}, noop, fmt.Errorf("get home directory: %w", err)
		}
		configPath = filepath.Join(home, ".shell3", "shell3.lua")
	}

	lc, err := luacfg.Load(configPath, filepath.Dir(configPath))
	if err != nil {
		return chat.Config{}, noop, fmt.Errorf("load config: %w", err)
	}
	cleanup := func() { lc.Close() }

	m, ok := lc.Model(lc.Agent.ModelName)
	if !ok {
		cleanup()
		return chat.Config{}, noop, fmt.Errorf("agent references unknown model %q", lc.Agent.ModelName)
	}

	client := openai.NewClient(m.BaseURL, m.APIKey, m.ModelID)
	rp := llm.RequestParams{
		ReasoningEffort: m.Reasoning,
		MaxTokens:       m.MaxTokens,
		Temperature:     m.Temperature,
	}
	client.SetParams(rp)
	if m.Extra != nil {
		client.SetExtra(m.Extra)
	}

	sysPrompt := lc.BuildPersona(luacfg.RuntimeData{CWD: workDir, Model: m.ModelID})

	customDefs := lc.CustomToolsFor(lc.Agent.CustomTools)
	hasSkills := lc.Agent.SkillsActive()
	toolDefs := luacfg.ToolDefs(lc.Agent.Gates, customDefs, hasSkills)

	pers := persona.Persona{
		Name:         lc.Agent.Name,
		SystemPrompt: sysPrompt,
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

	cfg := chat.Config{
		LLM:             client,
		Personality:     pers,
		WorkDir:         workDir,
		StatusLine:      lc.Agent.Name + " │ " + m.ModelID,
		ModeLabel:       lc.Agent.Name,
		ContextWindow:   m.ContextWindow,
		ActiveSkills:    lc.Agent.Skills,
		CustomTool:      lc.CallTool,
		CustomToolNames: customNames,
		ToolGuard: func(ctx context.Context, t string, p map[string]any) (int, string, error) {
			d, r, e := lc.OnToolCall(ctx, t, p)
			return int(d), r, e
		},
		Headless: true,
		Params:   rp,
	}

	return cfg, cleanup, nil
}
```

Note: `chat.Config.Log` is left nil — `runConfig` wraps it via `chat.LogOrNoop`. The old `New`, `Options`, and the unused `applog` import are gone (this task's import block is the complete list).

- [ ] **Step 4: Run the full package test + build**

Run: `go test ./pkg/shell3/ -v && go build ./...`
Expected: PASS (TestTranslate, TestRunConfig_*, TestRun_BadConfig_Errors) and a clean build with no references to the removed `shell3.New`.

- [ ] **Step 5: Verify nothing else referenced the old API**

Run: `grep -rn "shell3.New\|shell3.Options" --include=*.go . || echo "no references — clean"`
Expected: `no references — clean` (the only caller was the old test, now replaced).

- [ ] **Step 6: Commit**

```bash
git add pkg/shell3/shell3.go pkg/shell3/shell3_test.go
git commit -m "feat(shell3): single-function Run entrypoint; remove New/Options"
```

---

## Task 4: Full regression sweep

Confirm the whole repo still builds and tests green — the public-surface change touched nothing outside `pkg/shell3`, but verify.

**Files:** none (verification only)

- [ ] **Step 1: Build everything**

Run: `make build`
Expected: builds with no errors.

- [ ] **Step 2: Run the full test suite**

Run: `go test ./...`
Expected: all packages PASS (or `ok` / `no test files`).

- [ ] **Step 3: Vet**

Run: `go vet ./...`
Expected: no diagnostics.

---

## Self-Review

**Spec coverage:**
- Public surface (`Run`/`Spec`/`Event`/`Kind`) → Tasks 1 & 3. ✓
- Event channel streaming → Task 2 (`runConfig`). ✓
- Slim `Event` translation + dropped kinds → Task 1 (`translate`, table test covers token/tool-result/error/done + dropped reasoning/tool-call/usage/session). ✓
- `Run` is single entrypoint; `New`/`Options`/`chat.Config` leak removed → Task 3 (Steps 3 & 5 verify no references). ✓
- Failed-to-start = `(nil, error)` → Task 3 `TestRun_BadConfig_Errors`. ✓
- Mid-turn error as `Event{Kind:Error}` → covered by `translate` error case (Task 1) and the streaming path (Task 2). ✓
- Lua state owned by `Run`, closed after teardown → Task 2 `cleanup` (asserted called once) + Task 3 wiring `closeLua` into `runConfig`. ✓
- Headless always on for embedders → `Headless: true` in both `buildConfig` and the `TurnConfig` in `runConfig`. ✓
- CLI untouched (spec adjustment) → no `internal/tui` or `cmd` files in the plan; Task 3 Step 5 + Task 4 confirm no breakage. ✓
- Tests: `TestRun_BadConfig_Errors`, `TestRunConfig_StreamsToDone` (≈ spec's StreamsToDone), `TestRunConfig_MapsToolResult` → Tasks 2 & 3. ✓

**Placeholder scan:** none — every code/test step shows complete content.

**Type consistency:** `translate(chat.Event) (Event, bool)`, `runConfig(ctx, chat.Config, string, func()) <-chan Event`, `buildConfig(Spec) (chat.Config, func(), error)`, `Run(ctx, Spec) (<-chan Event, error)` used consistently across tasks. `fakellm.New`, `fakellm.Script{Events}`, `llm.StreamEvent{TextDelta}`/`{ToolCall}`, `llm.ToolCall{ID,Name,RawArgs}`, `llm.ToolDefinition{Name,Description}`, `chat.NewSession`/`SessionOpts`/`TurnConfig`/`NewHandlers`/`LogOrNoop` all match verified source signatures.
