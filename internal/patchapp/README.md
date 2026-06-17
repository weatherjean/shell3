# patchapp

App shell for inline chat-style terminal apps. Built on `patchtui`. Owns the input loop, terminal lifecycle, status bar, scrollback, slash command registry, and pause/resume for subprocesses.

## What it does

- **Input loop** (`loop.go`): self-pipe + `unix.Poll` over stdin, so [App.Pause] from another goroutine can interrupt the wait without stealing keystrokes from a subprocess.
- **Terminal lifecycle** (`lifecycle.go`): `Pause()` / `Resume()` / `WithReleasedTerminal(fn)` — restore cooked mode for `nvim`, `!cmd`, hooks; pair with `Pause`/`Resume` from any goroutine.
- **Slash commands** (`slash.go`): `RegisterSlash(cmd)` table-driven dispatch with aliases, auto `/help`, case-insensitive lookup.
- **Live frame**: idle frame is `input box + status bar`; busy frame is a single rainbow status line with spinner + tokens + cancel hint. Streaming text/reasoning commits to scrollback via `Print` (no live preview region). Mutators (`SetTokens`, `SetBusy`, `SetStatus`, `Print`, `PrintLine`) are goroutine-safe.
- **Editor** (`editor.go`): multi-line input with bracketed paste, alt+enter for newline, arrow nav, esc clear, ctrl+c cancel + double-tap quit.
- **`AppView` interface** (`view.go`): the narrow consumer-side contract for goroutines feeding events into the live frame. Implemented by `*App`.
- **Quit** (`App.Quit`): cleanly exits the input loop so callers' deferred teardown runs (instead of `os.Exit`).

## What it does NOT do

- No LLM logic, no conversation state — that's the caller's domain.
- No event types; the caller emits and drains its own events.
- No persistent storage, hooks, or auth.

## Usage

```go
app := patchapp.New(modeLabel, statusMsg, patchapp.WelcomeInfo{
    Persona: "assistant", // printed once on start
})

// Slash commands (auto /help built from the registry).
app.RegisterSlash(patchapp.SlashCommand{
    Name: "clear", Help: "reset state",
    Handler: func(args string) { /* … */ },
})

// Subscribe to user input.
app.SetSubmit(func(input string) {
    go runTurn(input, app) // caller's logic; uses AppView for output
})

// Hooks that need TTY access can satisfy hooks.TTYReleaser via *App
// directly (it has Pause/Resume).

if err := app.Run(ctx); err != nil { /* … */ }
```

For the renderer underneath, see `patchtui`. For markdown rendering, see `patchmd`.
