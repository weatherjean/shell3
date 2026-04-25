# shell3 BubbleTea Rework Design

**Date:** 2026-04-24  
**Status:** Approved

## Overview

Unify `shell3 run` and `shell3 code` into a single `shell3` chat command backed by a BubbleTea TUI. Add `!command` TTY passthrough for both user input and lifecycle hooks. Introduce a personality system (code / agent) configured at init.

---

## 1. CLI & Commands

`shell3 run` and `shell3 code` are removed. Replaced by:

- `shell3` — interactive TUI chat (reads personality from config)
- `shell3 "message"` — one-shot mode, raw stdout, no TUI
- `shell3 init` — gains personality prompt during setup (code / agent)
- `shell3 auth` — unchanged
- `shell3 destroy` — unchanged

---

## 2. Personality

Config field `personality: code | agent` in `.shell3/config.yaml`. Set at `shell3 init`. Editable directly in config.

| Personality | System prompt base | Tools |
|-------------|-------------------|-------|
| `code` | code-focused (current `CodeSystemPrompt`) | bash + memory/history + skills |
| `agent` | general agent (current skills-based prompt) | bash + memory/history + skills |

- Personality controls **only** base system prompt.
- Skills from `.shell3/skills/` are loaded for both personalities.
- Custom personalities via `.shell3/personalities/*.yaml` also receive skills appended.
- `internal/personality` package owns prompt construction and tool list per personality.

---

## 3. BubbleTea UI

New `internal/tui` package owns the `tea.Model`.

### Layout

```
┌─────────────────────────────┐
│  scrollable viewport        │
│  (chat history, tool calls, │
│   bash output)              │
│                             │
├─────────────────────────────┤
│ status: model | tokens      │
├─────────────────────────────┤
│ > input                     │
└─────────────────────────────┘
```

### Streaming

LLM stream runs in a goroutine. Chunks sent to the program via `program.Send(chunkMsg)`. Viewport appends chunks as they arrive. Full messages rendered with `glamour` before appending.

### `!command` TTY passthrough

If user input starts with `!`:
1. `program.ReleaseTerminal()`
2. `exec` subprocess with `stdio: "inherit"`
3. Wait for exit, show exit code inline if non-zero
4. `program.RestoreTerminal()`

### One-shot mode

`shell3 "message"` skips `tea.Program` entirely — raw stdout output, same behavior as today.

---

## 4. TTY Hooks

Hook runner receives a `TTYReleaser` interface:

```go
type TTYReleaser interface {
    Release() error
    Restore() error
}
```

| Hook | Behavior |
|------|----------|
| `on_session_start/end` | TTY release → subprocess with `stdio: inherit` → restore |
| `on_turn_start/end` | TTY release → subprocess with `stdio: inherit` → restore |
| `on_tool_result` | TTY release → subprocess with `stdio: inherit` → restore |
| `on_tool_call` | piped JSON (data hook, no TTY) |
| `on_context_build` | piped JSON (data hook, no TTY) |

Hook failure: non-fatal, dim warning rendered into chat buffer.

In one-shot mode: `TTYReleaser` is a no-op (stdout is already not a TUI).

---

## 5. Package Structure

| Old | New | Notes |
|-----|-----|-------|
| `internal/codeagent` | `internal/chat` | core turn loop, tool dispatch |
| `cmd/shell3/run.go` + `code.go` | single `runChat()` in cmd | merged entry point |
| _(new)_ | `internal/tui` | BubbleTea model, viewport, input |
| _(new)_ | `internal/personality` | prompt + tool list per personality |
| `internal/hooks` | `internal/hooks` | add `TTYReleaser`, split `callHook`/`callHookTTY` |
| `internal/agent` | deleted | superseded by `internal/chat` |

Unchanged: `internal/llm`, `internal/store`, `internal/memory`, `internal/config`, `internal/skills`, `internal/history`, `internal/output`.

---

## 6. Error Handling

- LLM/tool errors during turn: rendered inline in viewport (replaces current `colorRed` stderr)
- `!command` non-zero exit: show exit code in chat, no crash
- TTY hook failure: non-fatal, dim warning in chat buffer
- `tea.Program` crash: bubble to `main`, print error, exit 1

---

## 7. Testing

- Existing unit tests unchanged (`bash_test.go`, `memory_test.go`, etc.)
- `internal/tui`: no unit tests — manually tested
- `internal/personality`: test that correct tools returned per personality
- `internal/hooks`: test `callHookTTY` using a script that checks `isatty`
