# Embedding shell3 (the Go library)

Everything the TUI does is available as a Go library. `pkg/shell3` is the only
public package — import it and you get the same engine, from a one-shot call up
to an always-on agent hosting many concurrent conversations.

There are three entry points, in increasing order of ambition:

- **`Run`** — one turn, then done.
- **`Start` → `Session`** — a persistent, multi-turn conversation.
- **`NewRuntime` → many `Session`s** — one shared build hosting many named
  sessions, for a long-lived bot.

Full API reference:
[pkg.go.dev/github.com/weatherjean/shell3/pkg/shell3](https://pkg.go.dev/github.com/weatherjean/shell3/pkg/shell3).

## One-shot: `Run`

`Run` executes a single turn and streams events back over a channel. Use it for
scripts, filters, and one-off automation:

```go
events, err := shell3.Run(ctx, shell3.Spec{Prompt: "what does this repo do?"})
if err != nil {
    log.Fatal(err)
}
for ev := range events {
    if ev.Kind == shell3.Token {
        fmt.Print(ev.Text)
    }
}
```

The event stream is the same one the TUI consumes: assistant tokens, tool calls
with their raw arguments, tool results, usage counts, and a terminal status. You
decide what to render and what to ignore.

## Persistent: `Start` and `Session`

`Start` gives you a `Session` that holds a conversation across many turns. On top
of `Send` it exposes agent switching, history introspection, pruning, and
parameter control — the programmatic equivalents of the slash commands you'd use
in the TUI.

The strict single-turn path is `Send` / `SendParts`: one call, one turn, one
result. `SendParts` lets you attach inbound images and audio as `Part`
attachments — either from disk or as in-memory bytes — so a multimodal model can
see them.

## Always-on: `NewRuntime`

For a personal agent that's always listening, `NewRuntime` owns one shared build
— config, store, and log — and hosts many named sessions. Each session is its
own conversation with its own agent, working directory, and audit log:

```go
rt, _ := shell3.NewRuntime(ctx, shell3.RuntimeSpec{WorkDir: home})
defer rt.Close()

chat, _ := rt.Session(shell3.SessionOpts{Name: "tg:1234", Headless: true})
```

A long-lived host runs a single select loop over `rt.Events()`. The key ideas:

- **Wake bus.** A session whose inbox gains an item while it's idle emits a
  `Wake`. The host answers with `Session.RunQueued` — it doesn't poll.
- **Interjection.** `Session.Interject` steers a running turn (or queues for the
  next one) from any goroutine, and never blocks. It's the soft, concurrent path;
  `Send` / `SendParts` remain the strict single-turn path.
- **Media.** Inbound images and audio ride along as `Part` attachments, exactly
  as with `SendParts`.

This is the machinery behind long-lived embedding hosts (e.g. an always-on chat
bot front-end).

## The job-progress stream: `JobEvents`

`Runtime.JobEvents()` and `Session.JobEvents()` expose one unified stream of
`JobProgress` events covering every background job — `task` subagents and
`bash_bg` commands alike. Each event carries the job id, kind, and title, plus
either an incremental rendered text `Chunk` (while the job runs) or `Done` with
the capped result `Summary` (subagent jobs only). The TUI `:background` modal
live-tails this stream — it's built on the same channel an embedder can consume
too.

## Pluggable file I/O: `SessionOpts.FS`

`SessionOpts.FS` (or `Spec.FS` for `Start`/`Run`) accepts a `FileSystem` — a
two-method interface defined in `pkg/shell3` (`ReadTextFile`/`WriteTextFile`
over absolute paths) — that backs the session's `read` and `edit_file` tools. The default is direct disk;
an embedder can swap in another backend (e.g. one backed by in-memory buffers
instead of the OS). `bash` is unaffected — it always hits disk directly.

## Session introspection and host tools

A `Session` exposes the programmatic equivalents of the slash commands:
`Snapshot` (a point-in-time view of state and context usage), `History` (the
conversation entries), `Prune(id)` (drop one message from context), `Clear`,
`Rollback`, and `SwitchAgent(name)`. `RegisterHostTool` adds a Go-implemented
tool (name, JSON-schema parameters, and a handler func) to the session's schema
before the first turn — it complements Lua custom tools and dispatches through
the same path. `SetSafetyOff(true)` is the host-side switch behind a
front-end's `disable_safety` command: while on, `on_tool_call` ask verdicts
run without prompting a human (block verdicts still block).

All of the state-mutating methods return `ErrBusy` while a turn is in flight —
drain the `Send` channel first. A `Send` on a session that was already closed
is rejected with `ErrClosed` (as an immediate `Error` event), and
`Runtime.Session` on a closed runtime returns `ErrRuntimeClosed`.

## Subagents

Subagents are **in-process background jobs**, not subprocess forks. Declare a
specialist with `shell3.subagent{ name, description, … }` and list it on an
agent via `tools = { subagents = { … } }` with `delegation = true`. shell3 then
advertises the `task`, `task_list`, `task_status`, and `task_cancel` tools to
that agent.

When the agent calls `task{ subagent_type, prompt, description }` the call
returns immediately; the runtime (`pkg/shell3` jobManager) runs the child as a
goroutine under a concurrency cap (`shell3.background{ max_concurrent = N }`,
default 8). On completion the jobManager **wakes the parent session with a capped
result summary** injected into its next turn — there is no subprocess, no
`.shell3_project/inbox.jsonl`, and no fsnotify watch. The parent acts on the
summary directly and never polls.

Delegation is **single-level**: a subagent is not given the `task` tool.
Recursion depth is capped by `shell3.subagents{ max_depth = N }` (default 3).
`task_list` / `task_status <id>` / `task_cancel <id>` manage running jobs (ids
look like `sub1`, `bg1`). The TUI `:background` modal shows all running and
recently finished jobs live; the footer `bg: N` pill counts only running ones.

## The TUI rides the same rails

None of this is library-only. In the interactive TUI you can type while the
agent is working and press Enter to steer mid-turn (that's an `Interject`), and a
finished subagent surfaces as a dim notice that auto-wakes the next turn. The
front-ends are thin; the engine is shared.
