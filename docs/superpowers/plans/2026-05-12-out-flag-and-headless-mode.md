# `--out` Flag and Headless Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `--out <path>` flag that streams a full JSONL audit log of a shell3 invocation (assistant text, reasoning, tool calls, tool output, errors, usage). Pair it with explicit headless-mode hardening so spawned-as-subprocess shell3 instances behave safely without a human at the keyboard, and ship a skill that teaches agents to spawn other agents this way.

**Architecture:** A new sink type in `internal/chat` wraps a writer and serializes existing `patchapp.Event` values into a JSONL schema with ANSI stripped. `RunOnce` and `drainTurn` tee events to the sink when `cfg.OutPath` is non-empty. Headless mode is a process-wide flag set in `runChat` when `--out` is provided (or when stdin is piped with no TTY); it exports `SHELL3_HEADLESS=1` / `SHELL3_OUT=<path>` to all child processes (which inherit shell3's env, so hooks see them), removes `shell_interactive` from the tool schema, and injects a one-shot system-reminder so the LLM knows its constraints. The default `confirm-bash.sh` hook is updated in both the bootstrap source (`internal/scaffold/defaults/hooks/`) and the user's installed copy (`~/.shell3/hooks/`) to branch on `SHELL3_HEADLESS` and apply a safe policy. A new skill `spawning-subagents.md` ships via scaffold and teaches the agent how to compose itself.

**Tech Stack:** Go 1.25, existing `patchapp.Event` types, `patchtui.StripANSI`, `encoding/json`. No new dependencies.

---

## Pre-Flight

- Branch from `main`: `git checkout -b feat/out-flag-headless` before starting.
- Baseline: `go test ./...` should pass on `main` before any edits.
- The plan is structured so each task ends with passing tests + a commit. Build green between tasks.

---

## File Structure

**New files:**
- `internal/chat/outsink.go` — JSONL event sink type + schema + ANSI stripper wrapper.
- `internal/chat/outsink_test.go` — unit tests for sink event serialization.
- `internal/scaffold/defaults/skills/spawning-subagents.md` — skill text shipped via bootstrap.
- `docs/headless.md` — user-facing reference for headless mode + JSONL schema.

**Modified files:**
- `internal/chat/chat.go` — add `OutPath string` to `Config`; wire sink into `RunOnce` and `drainTurn`; emit `start` / `end` events.
- `internal/chat/turn.go` — strip `shell_interactive` from `cfg.Personality.Tools` when `cfg.Headless` is true; inject headless system-reminder at turn start.
- `internal/chat/chat.go` (Config) — add `Headless bool`.
- `cmd/shell3/run.go` — add `--out` flag; detect headless; set env vars; pass `OutPath`/`Headless` into chat.Config.
- `internal/scaffold/defaults/hooks/confirm-bash.sh` — header comment + branch on `SHELL3_HEADLESS`.
- `~/.shell3/hooks/confirm-bash.sh` — same diff applied to the user's installed copy.

**Untouched but worth knowing:**
- `internal/hooks/hooks.go` — already uses `exec.CommandContext` which inherits the parent process env, so setting env in `runChat` is sufficient. No code change here.

---

## JSONL Schema (Reference)

This is the canonical schema. The engineer should keep this open while implementing the sink.

```jsonl
{"ts":"2026-05-12T15:04:05.123Z","kind":"start","input":"do something","persona":"default","model":"openai/gpt-5","out":"/tmp/out.jsonl","headless":true}
{"ts":"...","kind":"reasoning","text":"..."}
{"ts":"...","kind":"text","text":"streaming assistant chunk"}
{"ts":"...","kind":"tool","raw":"#1 → bash\n$ ls\nfoo.txt\n"}
{"ts":"...","kind":"usage","prompt":120,"completion":40,"total":160}
{"ts":"...","kind":"tty_exec_request","cmd":"vim foo.txt","workdir":"/tmp"}
{"ts":"...","kind":"error","error":"context canceled"}
{"ts":"...","kind":"turn_done","prompt":120,"completion":40,"total":160}
{"ts":"...","kind":"end","status":"ok"}
```

Rules:
- Exactly one JSON object per line, no trailing comma.
- `ts` is RFC3339Nano UTC for every event.
- `text` / `raw` fields have ANSI escape sequences stripped via `patchtui.StripANSI`.
- The `end` line is always the last line of the file. `status` is `"ok"` or `"error"`.
- Orchestrators poll the file and stop when they see the `end` line.

---

## Task 1: Add the JSONL sink type

**Files:**
- Create: `internal/chat/outsink.go`
- Create: `internal/chat/outsink_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/chat/outsink_test.go`:

```go
package chat

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/patchapp"
)

func TestSink_StartEndBracket(t *testing.T) {
	var buf bytes.Buffer
	s := newOutSink(&buf, time.Unix(0, 0).UTC)
	s.WriteStart("hi", "default", "gpt-5", "/tmp/out", true)
	s.WriteEnd("ok")
	out := buf.String()
	if !strings.Contains(out, `"kind":"start"`) || !strings.Contains(out, `"input":"hi"`) {
		t.Fatalf("missing start: %q", out)
	}
	if !strings.Contains(out, `"kind":"end"`) || !strings.Contains(out, `"status":"ok"`) {
		t.Fatalf("missing end: %q", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
}

func TestSink_StripsANSIFromText(t *testing.T) {
	var buf bytes.Buffer
	s := newOutSink(&buf, time.Unix(0, 0).UTC)
	s.WriteEvent(patchapp.ChunkEvent{Text: "\x1b[31mhello\x1b[0m world"})
	if strings.Contains(buf.String(), "\x1b[") {
		t.Fatalf("ANSI not stripped: %q", buf.String())
	}
	if !strings.Contains(buf.String(), `"text":"hello world"`) {
		t.Fatalf("expected stripped text, got %q", buf.String())
	}
}

func TestSink_AllEventKinds(t *testing.T) {
	var buf bytes.Buffer
	s := newOutSink(&buf, time.Unix(0, 0).UTC)
	s.WriteEvent(patchapp.ChunkEvent{Text: "a"})
	s.WriteEvent(patchapp.ReasoningChunkEvent{Text: "b"})
	s.WriteEvent(patchapp.AppendEvent{Text: "c"})
	s.WriteEvent(patchapp.UsageEvent{Usage: llm.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}})
	s.WriteEvent(patchapp.TurnDoneEvent{Usage: llm.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}})
	s.WriteEvent(patchapp.TurnErrEvent{Err: errString("boom")})
	s.WriteEvent(patchapp.TTYExecEvent{Cmd: "vim x", WorkDir: "/tmp"})

	want := []string{
		`"kind":"text"`, `"kind":"reasoning"`, `"kind":"tool"`,
		`"kind":"usage"`, `"kind":"turn_done"`,
		`"kind":"error"`, `"kind":"tty_exec_request"`,
	}
	for _, w := range want {
		if !strings.Contains(buf.String(), w) {
			t.Errorf("missing %q in %q", w, buf.String())
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chat/ -run TestSink -v`
Expected: FAIL — `newOutSink` / `WriteStart` / `WriteEnd` / `WriteEvent` undefined.

- [ ] **Step 3: Implement the sink**

Create `internal/chat/outsink.go`:

```go
package chat

import (
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/patchapp"
	"github.com/weatherjean/shell3/internal/patchtui"
)

// outSink writes one JSONL event per call to a writer. Safe for concurrent
// writes; serializes through an internal mutex. All text payloads have ANSI
// escape sequences stripped before serialization.
type outSink struct {
	mu  sync.Mutex
	w   io.Writer
	now func() time.Time
}

func newOutSink(w io.Writer, fixed time.Time) *outSink {
	now := func() time.Time { return time.Now().UTC() }
	if !fixed.IsZero() {
		now = func() time.Time { return fixed }
	}
	return &outSink{w: w, now: now}
}

type outEvent struct {
	TS         string     `json:"ts"`
	Kind       string     `json:"kind"`
	Text       string     `json:"text,omitempty"`
	Raw        string     `json:"raw,omitempty"`
	Input      string     `json:"input,omitempty"`
	Persona    string     `json:"persona,omitempty"`
	Model      string     `json:"model,omitempty"`
	Out        string     `json:"out,omitempty"`
	Headless   *bool      `json:"headless,omitempty"`
	Cmd        string     `json:"cmd,omitempty"`
	WorkDir    string     `json:"workdir,omitempty"`
	Error      string     `json:"error,omitempty"`
	Status     string     `json:"status,omitempty"`
	Prompt     int        `json:"prompt,omitempty"`
	Completion int        `json:"completion,omitempty"`
	Total      int        `json:"total,omitempty"`
}

func (s *outSink) write(e outEvent) {
	if s == nil {
		return
	}
	e.TS = s.now().Format(time.RFC3339Nano)
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = s.w.Write(data)
}

// WriteStart emits the first line of the JSONL stream.
func (s *outSink) WriteStart(input, persona, model, out string, headless bool) {
	h := headless
	s.write(outEvent{Kind: "start", Input: input, Persona: persona, Model: model, Out: out, Headless: &h})
}

// WriteEnd emits the final line. status should be "ok" or "error".
func (s *outSink) WriteEnd(status string) {
	s.write(outEvent{Kind: "end", Status: status})
}

// WriteEvent maps a patchapp.Event to its JSONL form.
func (s *outSink) WriteEvent(ev patchapp.Event) {
	switch v := ev.(type) {
	case patchapp.ChunkEvent:
		s.write(outEvent{Kind: "text", Text: patchtui.StripANSI(v.Text)})
	case patchapp.ReasoningChunkEvent:
		s.write(outEvent{Kind: "reasoning", Text: patchtui.StripANSI(v.Text)})
	case patchapp.AppendEvent:
		s.write(outEvent{Kind: "tool", Raw: patchtui.StripANSI(v.Text)})
	case patchapp.UsageEvent:
		s.write(usageEv("usage", v.Usage))
	case patchapp.TurnDoneEvent:
		s.write(usageEv("turn_done", v.Usage))
	case patchapp.TurnErrEvent:
		msg := ""
		if v.Err != nil {
			msg = v.Err.Error()
		}
		s.write(outEvent{Kind: "error", Error: msg})
	case patchapp.TTYExecEvent:
		s.write(outEvent{Kind: "tty_exec_request", Cmd: v.Cmd, WorkDir: v.WorkDir})
	}
}

func usageEv(kind string, u llm.Usage) outEvent {
	return outEvent{
		Kind:       kind,
		Prompt:     u.PromptTokens,
		Completion: u.CompletionTokens,
		Total:      u.TotalTokens,
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/chat/ -run TestSink -v`
Expected: PASS — all 3 tests green.

- [ ] **Step 5: Commit**

```bash
git add internal/chat/outsink.go internal/chat/outsink_test.go
git commit -m "feat(chat): add JSONL outSink for --out flag"
```

---

## Task 2: Wire OutPath and Headless into Config + RunOnce

**Files:**
- Modify: `internal/chat/chat.go` — add Config fields, open sink in RunOnce, write events.

- [ ] **Step 1: Add fields to Config**

In `internal/chat/chat.go`, locate the `Config struct` (around line 44) and add two fields at the end of the struct, before the closing brace:

```go
	// OutPath, when non-empty, opens a JSONL audit log at this path and
	// streams every turn event into it. Independent of stdout/TUI rendering.
	OutPath string
	// Headless flips on subprocess-friendly behaviors: strips
	// shell_interactive from the tool schema, injects a system-reminder
	// explaining the constraints, and signals hooks via SHELL3_HEADLESS=1.
	Headless bool
```

- [ ] **Step 2: Open sink in RunOnce**

In `internal/chat/chat.go`, replace the body of `RunOnce` (around line 691) with:

```go
// RunOnce executes a single turn and prints output to stdout. No TUI.
func RunOnce(ctx context.Context, cfg Config, input string) error {
	sess := &session{}
	ch := make(chan patchapp.Event, 256)

	sink, sinkCleanup, err := openSink(cfg.OutPath)
	if err != nil {
		return err
	}
	defer sinkCleanup()
	if sink != nil {
		_, model := splitStatus(cfg.StatusLine)
		sink.WriteStart(input, cfg.ModeLabel, model, cfg.OutPath, cfg.Headless)
	}

	tc := TurnConfig{
		LLM:         cfg.LLM,
		Hooks:       cfg.Hooks,
		Personality: cfg.Personality,
		StatusLine:  cfg.StatusLine,
		WorkDir:     cfg.WorkDir,
		Store:       cfg.Store,
		UserTools:   cfg.UserTools,
		Secrets:     cfg.Secrets,
		Truncate:    cfg.Truncate || cfg.OutPath != "", // full output when sink is active
		Handlers:    NewHandlers(cfg),
		Log:         logOrNoop(cfg.Log),
		Headless:    cfg.Headless,
	}
	go runTurn(ctx, tc, sess, llm.Message{Role: llm.RoleUser, Content: input}, ch)

	status := "ok"
	for ev := range ch {
		if sink != nil {
			sink.WriteEvent(ev)
		}
		switch v := ev.(type) {
		case patchapp.ChunkEvent:
			fmt.Print(v.Text)
		case patchapp.AppendEvent:
			fmt.Print(v.Text)
		case patchapp.TurnErrEvent:
			fmt.Fprintln(os.Stderr, "error:", v.Err)
			status = "error"
		case patchapp.TurnDoneEvent:
			fmt.Println()
		case patchapp.TTYExecEvent:
			// Headless one-shot: refuse, unblock the turn with an error.
			v.ReplyC <- "error: interactive TTY not available in headless mode"
		}
	}
	if sink != nil {
		sink.WriteEnd(status)
	}
	return nil
}

// openSink opens path for append+create+truncate (truncate on open so each
// run starts fresh) and returns the sink and a cleanup closure. Returns
// (nil, no-op, nil) when path is empty.
func openSink(path string) (*outSink, func(), error) {
	if path == "" {
		return nil, func() {}, nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open --out %s: %w", path, err)
	}
	return newOutSink(f, time.Time{}), func() { _ = f.Close() }, nil
}
```

Note: this references `Truncate`, `Headless` on `TurnConfig` — Task 3 adds those.

- [ ] **Step 3: Add Headless field to TurnConfig**

In `internal/chat/toolhandler.go`, add at the end of the `TurnConfig` struct (just before the closing brace):

```go
	// Headless is true when shell3 runs as a subprocess (no human at the
	// keyboard). turn.go drops shell_interactive and injects a system
	// reminder when this is set.
	Headless bool
```

- [ ] **Step 4: Add time import to chat.go if missing**

Verify `internal/chat/chat.go` imports `"time"`. The existing file imports it for other uses; if not, add it.

Run: `grep -q '"time"' internal/chat/chat.go && echo present || echo missing`
Expected: `present`. If `missing`, add `"time"` to the import block.

- [ ] **Step 5: Build to confirm no regressions**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chat/chat.go internal/chat/toolhandler.go
git commit -m "feat(chat): add OutPath + Headless to Config, wire sink into RunOnce"
```

---

## Task 3: Headless tool stripping + system-reminder injection in turn.go

**Files:**
- Modify: `internal/chat/turn.go`

- [ ] **Step 1: Write test**

Append to `internal/chat/turn_test.go` (the file already exists):

```go
func TestHeadless_StripsShellInteractiveTool(t *testing.T) {
	tools := []llm.ToolDefinition{
		{Name: "bash"},
		{Name: "shell_interactive"},
		{Name: "edit_file"},
	}
	out := filterHeadlessTools(tools, true)
	if len(out) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(out))
	}
	for _, td := range out {
		if td.Name == "shell_interactive" {
			t.Fatal("shell_interactive should have been stripped")
		}
	}
}

func TestHeadless_PassThroughWhenDisabled(t *testing.T) {
	tools := []llm.ToolDefinition{{Name: "bash"}, {Name: "shell_interactive"}}
	if got := filterHeadlessTools(tools, false); len(got) != 2 {
		t.Fatalf("expected pass-through, got %d", len(got))
	}
}
```

Add an import of `"github.com/weatherjean/shell3/internal/llm"` at the top of `turn_test.go` if not already present.

- [ ] **Step 2: Run failing test**

Run: `go test ./internal/chat/ -run TestHeadless -v`
Expected: FAIL — `filterHeadlessTools` undefined.

- [ ] **Step 3: Implement filter helper and inject reminder**

In `internal/chat/turn.go`, add a helper at the top of the file (after the imports, before `logStreamError`):

```go
// filterHeadlessTools returns tools with shell_interactive removed when
// headless is true. Other tools pass through unchanged.
func filterHeadlessTools(tools []llm.ToolDefinition, headless bool) []llm.ToolDefinition {
	if !headless {
		return tools
	}
	out := make([]llm.ToolDefinition, 0, len(tools))
	for _, td := range tools {
		if td.Name == "shell_interactive" {
			continue
		}
		out = append(out, td)
	}
	return out
}

// headlessReminder is injected once at the start of a headless turn so the
// model understands the environment. Adapters that block destructive tool
// calls also append their own reasons via the existing hook path.
const headlessReminder = "<system-reminder>\nheadless mode: no interactive shell, no human available to answer questions. Decide and proceed. Destructive commands may be blocked by host policy — if a block occurs, adapt rather than retry.\n</system-reminder>"
```

In the same file, find `runTurn` (around line 86). Locate the block that builds `allMsgs`:

```go
allMsgs := make([]llm.Message, 0, len(msgs)+1)
allMsgs = append(allMsgs, llm.Message{Role: llm.RoleSystem, Content: cfg.Personality.SystemPrompt})
allMsgs = append(allMsgs, msgs...)
```

Immediately after that block, add:

```go
toolList := filterHeadlessTools(cfg.Personality.Tools, cfg.Headless)
if cfg.Headless {
	allMsgs = injectReminder(allMsgs, headlessReminder)
}
```

Then find the call site of `streamOnce` (around line 125):

```go
text, reasoning, toolCalls, usage, err := streamOnce(ctx, cfg.LLM, allMsgs, cfg.Personality.Tools, ch)
```

Change `cfg.Personality.Tools` → `toolList`:

```go
text, reasoning, toolCalls, usage, err := streamOnce(ctx, cfg.LLM, allMsgs, toolList, ch)
```

Also find the schema-index builder a few lines above:

```go
toolSchemas := make(map[string]map[string]any, len(cfg.Personality.Tools))
for _, td := range cfg.Personality.Tools {
	toolSchemas[td.Name] = td.Parameters
}
```

Change `cfg.Personality.Tools` → `toolList` in both lines:

```go
toolSchemas := make(map[string]map[string]any, len(toolList))
for _, td := range toolList {
	toolSchemas[td.Name] = td.Parameters
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/chat/ -run TestHeadless -v
```
Expected: PASS — both tests green.

```bash
go test ./internal/chat/...
```
Expected: PASS — no other test regresses.

- [ ] **Step 5: Commit**

```bash
git add internal/chat/turn.go internal/chat/turn_test.go
git commit -m "feat(chat): headless mode strips shell_interactive + injects reminder"
```

---

## Task 4: Tee sink in drainTurn (interactive mode) so `--out` works there too

**Files:**
- Modify: `internal/chat/chat.go` — RunInteractive opens sink, passes to drainTurn.

- [ ] **Step 1: Update RunInteractive to open the sink**

In `internal/chat/chat.go`, find `RunInteractive` (around line 90). Just before the `app := patchapp.New(...)` call, add:

```go
sink, sinkCleanup, err := openSink(cfg.OutPath)
if err != nil {
	return err
}
defer sinkCleanup()
if sink != nil {
	_, model := splitStatus(cfg.StatusLine)
	sink.WriteStart("(interactive)", cfg.ModeLabel, model, cfg.OutPath, cfg.Headless)
	defer sink.WriteEnd("ok")
}
```

- [ ] **Step 2: Pass sink through launchTurn**

Find `launchTurn` (around line 134). The current line is:

```go
go drainTurn(ch, app, &lastUsage, &cfg)
```

Change it to:

```go
go drainTurn(ch, app, &lastUsage, &cfg, sink)
```

- [ ] **Step 3: Update drainTurn signature + tee writes**

Change the signature of `drainTurn` (around line 205):

```go
func drainTurn(ch <-chan patchapp.Event, app patchapp.AppView, lastUsage *llm.Usage, cfg *Config, sink *outSink) {
```

Inside the `for ev := range ch` loop, immediately before the switch on event type, add:

```go
if sink != nil {
	sink.WriteEvent(ev)
}
```

Place this before the `switch v := ev.(type) { ... }` line so every event reaches the sink regardless of what the TUI does with it.

- [ ] **Step 4: Pass Headless through TurnConfig in launchTurn**

In the `launchTurn` closure, the `tc := TurnConfig{...}` literal is missing the new `Headless` field. Add it:

```go
tc := TurnConfig{
	LLM:         cfg.LLM,
	Hooks:       cfg.Hooks,
	Personality: cfg.Personality,
	StatusLine:  cfg.StatusLine,
	WorkDir:     cfg.WorkDir,
	Store:       cfg.Store,
	UserTools:   cfg.UserTools,
	Secrets:     cfg.Secrets,
	Truncate:    cfg.Truncate || cfg.OutPath != "",
	Handlers:    handlers,
	Log:         logOrNoop(cfg.Log),
	Headless:    cfg.Headless,
}
```

- [ ] **Step 5: Run tests + build**

```bash
go build ./...
go test ./internal/chat/...
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chat/chat.go
git commit -m "feat(chat): tee --out sink in interactive drainTurn"
```

---

## Task 5: CLI plumbing — `--out` flag, headless detection, env vars

**Files:**
- Modify: `cmd/shell3/run.go`

- [ ] **Step 1: Add the flag**

In `cmd/shell3/run.go`, locate `type runFlags struct` (around line 29). Add a field:

```go
type runFlags struct {
	persona   string
	provider  string
	model     string
	noBash    bool
	noMemory  bool
	outPath   string
}
```

Then in `newRunCommand` (around line 36), after the existing `cmd.Flags()` calls, add:

```go
cmd.Flags().StringVar(&f.outPath, "out", "", "Stream a JSONL audit log of this run to <path>. Enables headless mode.")
```

- [ ] **Step 2: Detect headless mode**

In `runChat` (around line 63), after the initial cwd/homeDir setup but before any work is done with stores or personae, compute the headless flag:

```go
headless := f.outPath != "" || (!term.IsTerminal(int(os.Stdin.Fd())) && initialInput != "")
if headless {
	_ = os.Setenv("SHELL3_HEADLESS", "1")
	if f.outPath != "" {
		_ = os.Setenv("SHELL3_OUT", f.outPath)
	}
}
```

The `term` package is already imported in this file. Place this block right after the `homeDir, err := os.UserHomeDir()` check.

- [ ] **Step 3: Pass headless + outPath into chat.Config**

Find the `cfg := chat.Config{...}` literal (around line 263) and add two fields:

```go
cfg := chat.Config{
	// ... existing fields ...
	Params:        pers.Parameters,
	Log:           log,
	OutPath:       f.outPath,
	Headless:      headless,
}
```

- [ ] **Step 4: Build**

```bash
go build ./...
```
Expected: PASS.

- [ ] **Step 5: Manual smoke test the flag**

Run a tiny call to validate end-to-end (skip if no provider configured; this is opportunistic):

```bash
echo "echo hello world to stdout via bash" | ./shell3 --out /tmp/shell3-smoke.jsonl
echo "---file contents---"
cat /tmp/shell3-smoke.jsonl
```

Expected: first line is `{"kind":"start", ...}`, last line is `{"kind":"end", "status":"ok"}`. At least one `"kind":"tool"` line should be present if the agent ran bash. ANSI escapes (e.g. `[`) should not appear in any string field.

If no provider is configured, instead just verify the file is created with at least a start + end pair:

```bash
echo "" | ./shell3 --out /tmp/shell3-smoke.jsonl --persona default 2>/dev/null || true
wc -l /tmp/shell3-smoke.jsonl
```

Expected: at least 2 lines.

- [ ] **Step 6: Commit**

```bash
git add cmd/shell3/run.go
git commit -m "feat(cli): add --out flag, detect headless, export SHELL3_HEADLESS/SHELL3_OUT"
```

---

## Task 6: Update the default confirm-bash hook to branch on `SHELL3_HEADLESS`

**Files:**
- Modify: `internal/scaffold/defaults/hooks/confirm-bash.sh`

- [ ] **Step 1: Add headless branch to the hook**

Open `internal/scaffold/defaults/hooks/confirm-bash.sh`. After the line:

```bash
CMD=$(echo "$INPUT" | jq -r '.params.command // empty')
```

…and before the `DANGER_PATTERNS=(` block, insert this branch:

```bash
# Headless mode: no human at the keyboard to answer a prompt. Run the same
# danger-pattern check as interactive, but auto-deny destructive matches and
# allow safe commands. The orchestrator sees the block in --out and can adapt.
# Power-users who want to trust the agent in headless can set
# SHELL3_HEADLESS_TRUST=1 to bypass this branch (use with care).
if [[ "$SHELL3_HEADLESS" == "1" && "$SHELL3_HEADLESS_TRUST" != "1" ]]; then
  for pat in "${DANGER_PATTERNS[@]:-}"; do
    : # warm the array so the loop below has it; the actual block follows
  done
fi
```

Then, find the existing pattern loop:

```bash
HIT=""
for pat in "${DANGER_PATTERNS[@]}"; do
  if echo "$CMD" | grep -qE "$pat"; then
    HIT=1
    break
  fi
done
```

Immediately after this loop (before `if [[ -z "$HIT" ]]; then`), insert:

```bash
# Headless policy: dangerous → block (model adapts), safe → allow.
if [[ "$SHELL3_HEADLESS" == "1" && "$SHELL3_HEADLESS_TRUST" != "1" ]]; then
  if [[ -n "$HIT" ]]; then
    echo '{"action":"block","reason":"Headless mode: destructive command requires human approval. Try a non-destructive alternative or skip this step."}'
    exit 0
  fi
  echo '{"action":"allow"}'
  exit 0
fi
```

Also update the file header comment (lines 1–20) to document the env contract. Replace the existing header comment block with:

```bash
#!/usr/bin/env bash
# Default on_tool_call hook: prompts the user before destructive shell tool
# calls. Safe commands run unprompted.
#
# Coverage: bash, bash_bg, shell_interactive. All other tools allowed.
#
# Headless mode (set by shell3 when --out is given OR stdin is piped with no
# TTY): no widget prompt is possible, so this hook applies a safe policy:
#   - SHELL3_HEADLESS=1, SHELL3_HEADLESS_TRUST unset → block dangerous, allow safe.
#   - SHELL3_HEADLESS=1, SHELL3_HEADLESS_TRUST=1     → allow everything (orchestrator's risk).
#
# Match strategy: each pattern is an Extended Regular Expression run against
# the raw command string with `grep -E -q`. If ANY pattern matches in
# interactive mode, the user is prompted via the `shell3 widget pick` selector.
# In headless mode the same match drives the auto-block.
#
# To extend / override:
#   - Copy this file and edit the DANGER_PATTERNS array below.
#   - Point your persona's `on_tool_call:` at the copy.
#
# Hook contract: stdin = on_tool_call JSON, stdout = action JSON.
# Actions:
#   allow  — proceed
#   block  — deny THIS call only; model can pick a different approach
#   cancel — abort the entire turn
```

Remove the placeholder branch I asked you to insert first (the one with the no-op `for pat` warm loop) — it was only there to keep the diff readable while planning. The single insertion *after* the pattern loop is the real branch.

- [ ] **Step 2: Test the hook in isolation**

The hook is bash-only; test it directly without invoking shell3.

```bash
# Headless + dangerous → block
echo '{"tool":"bash","params":{"command":"rm -rf /"}}' \
  | SHELL3_HEADLESS=1 bash internal/scaffold/defaults/hooks/confirm-bash.sh
```
Expected output: `{"action":"block","reason":"Headless mode: ..."}`

```bash
# Headless + safe → allow
echo '{"tool":"bash","params":{"command":"ls"}}' \
  | SHELL3_HEADLESS=1 bash internal/scaffold/defaults/hooks/confirm-bash.sh
```
Expected output: `{"action":"allow"}`

```bash
# Headless + trust + dangerous → allow
echo '{"tool":"bash","params":{"command":"rm -rf /"}}' \
  | SHELL3_HEADLESS=1 SHELL3_HEADLESS_TRUST=1 bash internal/scaffold/defaults/hooks/confirm-bash.sh
```
Expected output: `{"action":"allow"}`

```bash
# Non-bash tool → allow (unchanged behavior)
echo '{"tool":"edit_file","params":{}}' \
  | SHELL3_HEADLESS=1 bash internal/scaffold/defaults/hooks/confirm-bash.sh
```
Expected output: `{"action":"allow"}`

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/defaults/hooks/confirm-bash.sh
git commit -m "feat(hooks): confirm-bash branches on SHELL3_HEADLESS"
```

---

## Task 7: Mirror the hook change in the user's installed copy

The scaffold ships a fresh copy on `EnsureGlobal`, but doesn't overwrite an existing file. Apply the same diff to the live copy so the running setup gets it.

**Files:**
- Modify: `~/.shell3/hooks/confirm-bash.sh`

- [ ] **Step 1: Apply the same edits**

Apply the exact same edits from Task 6 to `~/.shell3/hooks/confirm-bash.sh`. (Header comment block + branch insertion after the pattern loop.)

- [ ] **Step 2: Verify with the same test commands**

Run the four test invocations from Task 6 Step 2, but against the home copy:

```bash
echo '{"tool":"bash","params":{"command":"rm -rf /"}}' \
  | SHELL3_HEADLESS=1 bash ~/.shell3/hooks/confirm-bash.sh
```
Expected: `{"action":"block","reason":"Headless mode: ..."}`

Repeat the other three cases from Task 6 Step 2.

- [ ] **Step 3: No commit needed** — this file is outside the repo. Note in your followup commit message that the home copy was synced manually.

---

## Task 8: Ship the `spawning-subagents` skill via bootstrap

**Files:**
- Create: `internal/scaffold/defaults/skills/spawning-subagents.md`

- [ ] **Step 1: Write the skill**

Create `internal/scaffold/defaults/skills/spawning-subagents.md`:

```markdown
---
name: spawning-subagents
description: Use when delegating a sub-task to a fresh shell3 process so it runs in parallel, isolated from the current conversation. Covers spawning with bash_bg, polling the JSONL audit log, and timing the wait with sleep.
---

# Spawning subagents

When a task is independent enough that you want a fresh agent to work it without polluting the current context, spawn a sibling `shell3` process. Each spawned agent writes a JSONL audit log; you watch the log to know what it did and when it finished.

## Pattern

1. Pick a temp path for the audit log. Prefer `/tmp/shell3-<short-slug>-<timestamp>.jsonl` — temp dirs are cleaned by the OS and writable without permission worries.
2. Spawn with `bash_bg` so the call returns immediately:
   ```bash
   shell3 "your-task-description-here" --out /tmp/shell3-find-deps-1715537000.jsonl
   ```
3. Sleep, then read the log. The last line is always `{"kind":"end","status":"ok|error"}`. If absent, the agent is still working.

## When to use this

- The sub-task is **self-contained** (no back-and-forth with you).
- You'd rather not pay context cost for the sub-task's tool noise.
- You have other work to do in parallel.

## When NOT to use this

- The sub-task needs interactive input — spawned agents run headless and refuse `shell_interactive`.
- The sub-task can finish in a single bash call — just use bash directly.
- You need streaming feedback — JSONL polling is batch-style.

## Polling pattern

```bash
# Spawn
OUT=/tmp/shell3-task-$(date +%s).jsonl
shell3 "summarise the open PRs on this repo" --out $OUT  # via bash_bg

# Wait + check
sleep 30
if tail -n1 $OUT | grep -q '"kind":"end"'; then
  cat $OUT | jq -r 'select(.kind=="text").text' | head -50
else
  echo "still working, sleep more"
fi
```

For long-running work, sleep in increasing increments (30s, 60s, 120s) rather than a tight poll loop. The JSONL is append-only, so reading it at the end is fine.

## Reading the JSONL

Each line is one event. Useful filters:

- Final assistant text:
  ```bash
  jq -r 'select(.kind=="text") | .text' < $OUT
  ```
- Tool calls only:
  ```bash
  jq 'select(.kind=="tool")' < $OUT
  ```
- Final usage:
  ```bash
  jq 'select(.kind=="turn_done")' < $OUT
  ```
- Was it cancelled / did anything break?
  ```bash
  jq 'select(.kind=="error")' < $OUT
  ```

See `docs/headless.md` in the shell3 repo for the full schema reference.

## Headless caveats

A spawned agent runs with `SHELL3_HEADLESS=1` and the default `confirm-bash` hook will **block destructive commands automatically**. The blocked call appears in the JSONL as a tool result containing "Headless mode: destructive command requires human approval." If your sub-task legitimately needs destructive operations, either:

- Refactor the sub-task to avoid them, OR
- Spawn with `SHELL3_HEADLESS_TRUST=1 shell3 ...` to opt the child into "trust the agent" mode (only do this when you're sure the task is safe).

## Output location convention

- `/tmp/shell3-<slug>-<unix-timestamp>.jsonl` — default for ad-hoc spawns.
- `.shell3/agents/<slug>.jsonl` — when you want the log persisted alongside the project (commit-ignore via `.gitignore`).
```

- [ ] **Step 2: Verify scaffold picks it up**

The scaffold copies everything under `internal/scaffold/defaults/skills/` into `~/.shell3/skills/` on `EnsureGlobal`. To verify:

```bash
go test ./internal/scaffold/...
go test ./internal/bootstrap/...
```
Expected: PASS — no changes to the scaffold logic needed; it's content-only.

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/defaults/skills/spawning-subagents.md
git commit -m "feat(scaffold): ship spawning-subagents skill"
```

---

## Task 9: Write `docs/headless.md`

**Files:**
- Create: `docs/headless.md`

- [ ] **Step 1: Write the doc**

Create `docs/headless.md`:

````markdown
# Headless mode and `--out`

shell3 can run as a child process and stream a structured audit log of everything it did. This makes it composable: orchestrators (other shell3 agents, shell scripts, CI jobs) can spawn shell3 instances, wait for them to finish, and read what happened.

## Quick start

```bash
shell3 "summarise the README" --out /tmp/run.jsonl
tail -n1 /tmp/run.jsonl     # last line is {"kind":"end",...}
jq -r 'select(.kind=="text") | .text' < /tmp/run.jsonl
```

## What triggers headless mode

Either:
- The `--out <path>` flag is given, OR
- stdin is not a TTY *and* an input argument is provided (e.g. piped).

Both conditions export `SHELL3_HEADLESS=1` to all subprocess hooks. The `--out` case additionally exports `SHELL3_OUT=<path>`.

## What changes in headless mode

| Behavior | Interactive | Headless |
|----------|-------------|----------|
| `shell_interactive` tool exposed to the model | yes | no — stripped from tool schema |
| `<system-reminder>` about headless constraints injected | no | yes, once at turn start |
| `confirm-bash` hook policy on destructive commands | TUI picker | auto-block (unless `SHELL3_HEADLESS_TRUST=1`) |
| `confirm-bash` hook policy on safe commands | run silently | run silently |
| Hooks run at all | yes | yes — env tells them they're headless |
| TUI rendered | yes | no, plain stdout |
| `--out` audit log written | only if flag set | only if flag set |

## JSONL schema

One JSON object per line. Every event carries `ts` (RFC3339Nano UTC) and `kind`. Other fields depend on kind.

| Kind | Fields | Meaning |
|------|--------|---------|
| `start` | `input`, `persona`, `model`, `out`, `headless` | First line of every file. Identifies the run. |
| `text` | `text` | Assistant text token. Concatenate all `text` events for the full reply. |
| `reasoning` | `text` | Reasoning / thinking token (not part of saved history). |
| `tool` | `raw` | Pre-formatted tool call block (header + output). ANSI stripped. |
| `tty_exec_request` | `cmd`, `workdir` | Model asked for `shell_interactive`. Always denied in headless. |
| `usage` | `prompt`, `completion`, `total` | Intermediate token usage between LLM rounds. |
| `turn_done` | `prompt`, `completion`, `total` | A single turn completed successfully. |
| `error` | `error` | Turn failed; string from the underlying error. |
| `end` | `status` | Last line. `status` is `"ok"` or `"error"`. |

All `text` and `raw` fields have ANSI escape sequences removed.

## Env vars exposed to hooks

| Var | Set when | Purpose |
|-----|----------|---------|
| `SHELL3_HEADLESS` | `1` in headless mode | Hooks branch on this to apply non-interactive policies. |
| `SHELL3_OUT` | absolute path when `--out` is set | Hooks can write supplemental logs alongside the audit. |
| `SHELL3_HEADLESS_TRUST` | not set by shell3; the orchestrator sets it | Opt-in: bypass the default safe-block policy in confirm-bash. |

## Writing your own headless-aware hook

Follow the pattern in `confirm-bash.sh`:

```bash
if [[ "$SHELL3_HEADLESS" == "1" && "$SHELL3_HEADLESS_TRUST" != "1" ]]; then
  # No human reachable. Pick a deterministic, safe action.
  echo '{"action":"block","reason":"headless: would have prompted"}'
  exit 0
fi
# Interactive fallback below — your existing logic here.
```

The hook contract is unchanged from interactive mode: stdin = on_tool_call JSON, stdout = action JSON (`allow`/`block`/`cancel`).

## Orchestrator example

A parent agent uses the `spawning-subagents` skill (shipped with shell3) to:

1. Spawn a sibling with `bash_bg`:
   ```bash
   shell3 "find every TODO in this repo" --out /tmp/find-todos.jsonl
   ```
2. `sleep 30` and check whether the JSONL ends with `{"kind":"end",...}`.
3. If yes: extract the final answer with `jq`. If no: sleep more.

See `internal/scaffold/defaults/skills/spawning-subagents.md` (also installed into `~/.shell3/skills/spawning-subagents.md`).

## Caveats

- Each `--out` invocation truncates the target file at open time. To collect history, use a unique path per run (e.g. include `$(date +%s)`).
- The JSONL is not strictly ordered with the stdout text stream — both are real-time but the file write may lag the terminal print by a few milliseconds. Treat the file as canonical.
- Orchestrators that need sub-second responsiveness should poll the file via `tail -f` or `fsnotify`, not `sleep` loops. For minute-scale tasks, polling with `sleep` is fine.
````

- [ ] **Step 2: Commit**

```bash
git add docs/headless.md
git commit -m "docs: headless mode + JSONL schema reference"
```

---

## Task 10: End-to-end verification

- [ ] **Step 1: Run the full test suite**

```bash
go test ./...
```
Expected: PASS across the board.

- [ ] **Step 2: `go vet` clean**

```bash
go vet ./...
```
Expected: no output.

- [ ] **Step 3: gofmt clean**

```bash
gofmt -l .
```
Expected: no output.

- [ ] **Step 4: Smoke test end-to-end**

```bash
make install
# Interactive (sidecar mode): TUI still works, sidecar JSONL collected.
shell3 --out /tmp/interactive-smoke.jsonl --persona default
# Type 'hello' + Enter, then /quit. Inspect:
wc -l /tmp/interactive-smoke.jsonl
head -3 /tmp/interactive-smoke.jsonl
```

Expected: start line + at least a turn_done, possibly an end line on /quit.

```bash
# Headless one-shot:
echo "echo subagent works" | shell3 --out /tmp/headless-smoke.jsonl --persona default
tail -n1 /tmp/headless-smoke.jsonl
```

Expected: `{"ts":"...","kind":"end","status":"ok"}` on the last line.

```bash
# Verify shell_interactive was stripped from the model's tool schema:
grep -c '"shell_interactive"' /tmp/headless-smoke.jsonl
```

Expected: `0` (no occurrences — the tool was never offered, so the model never called it).

- [ ] **Step 5: Confirm hooks behavior**

```bash
echo "rm -rf /tmp/never-actually-created" | shell3 --out /tmp/danger.jsonl --persona default
```

Expected: the model's `bash` tool call is blocked. The `tool` event in `/tmp/danger.jsonl` should contain the text `Headless mode: destructive command requires human approval` somewhere in the tool result.

- [ ] **Step 6: Final commit**

If any smoke-test fix-ups land in this task, commit them:

```bash
git add -A
git commit -m "chore: end-to-end smoke fixes for --out + headless"
```

If nothing changed, skip this step.

---

## Self-Review

**Spec coverage:**

| Spec item | Task |
|-----------|------|
| `--out <path>` writes JSONL | 1, 2, 4, 5 |
| Full tool output captured (no truncation) | 2 (sets `Truncate=true` when sink active) |
| System reminders captured | 1 (AppendEvent path), 2 (RunOnce wires events into sink) |
| Slash command output (interactive) — explicit non-goal | n/a, documented in docs |
| Headless mode triggered by `--out` | 5 |
| Headless mode triggered by piped stdin | 5 |
| `SHELL3_HEADLESS=1` env to hooks | 5 |
| `SHELL3_OUT=<path>` env to hooks | 5 |
| `shell_interactive` removed from tool schema | 3 |
| System-reminder injection explaining headless | 3 |
| `confirm-bash` hook branches on env | 6 (scaffold copy) + 7 (live copy) |
| `SHELL3_HEADLESS_TRUST=1` escape hatch | 6 |
| `spawning-subagents` skill shipped via bootstrap | 8 |
| `docs/headless.md` reference | 9 |
| End-to-end smoke tested | 10 |

**Placeholder scan:** No TBDs, no "implement later", no abstract instructions; every step has either runnable code, an exact shell command, or a documented expected output.

**Type consistency:** `Config.OutPath`, `Config.Headless`, `TurnConfig.Headless`, `outSink`, `newOutSink`, `openSink`, `filterHeadlessTools`, `headlessReminder` — all names match across tasks. The sink type's methods (`WriteStart`, `WriteEvent`, `WriteEnd`) match between Task 1 (definition) and Tasks 2 + 4 (calls). Event-kind strings in the schema doc match the `outSink.WriteEvent` switch arms in Task 1.

**Risk hotspots:**

1. **Task 6's hook diff is a free-text patch in a bash file.** Verify the placement carefully — the `for pat in "${DANGER_PATTERNS[@]:-}"` warmup snippet was a planning artifact, not real code. Only the post-loop branch is the real edit.
2. **Task 2 forces `Truncate=true` when OutPath is set.** This changes terminal output verbosity for users who pipe with `--out` (they'll see full bash output in their stdout/TUI). Acceptable side-effect; users who don't want it can `> /dev/null` stdout.
3. **Smoke tests in Task 5 / Task 10 require a configured provider.** If no provider is available, those steps degrade to "file is created with start+end lines" rather than full validation. Mark them as opportunistic, not blocking.
