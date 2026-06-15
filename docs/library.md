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
rt, _ := shell3.NewRuntime(shell3.RuntimeSpec{WorkDir: home})
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

This is the machinery behind the built-in Telegram host — see
[telegram.md](telegram.md).

## Subagents

Subagents are a convention, not a subsystem. You declare a specialist with
`shell3.subagent{ name, description, … }` and list it on an agent via
`tools = { subagents = { … } }`. shell3 then injects a `## Delegation` fragment
into that agent's prompt containing the exact `bash_bg` command to spawn one.

When the agent delegates, a subagent is simply a **backgrounded `shell3`
subprocess** running the chosen agent on a self-contained task. When it finishes,
it self-reports completion up its parent pointer:

- over a **per-session Unix-domain socket** if the parent is still live, or
- via a **SQLite inbox + revive** if the parent has since gone dormant.

The host turns that report into a short pointer notification — a one-line notice
plus a transcript path the parent `cat`s on demand — and wakes the next turn.
The parent never polls; it gets told. This means delegation survives the parent
going idle and even multiple levels deep.

## The TUI rides the same rails

None of this is library-only. In the interactive TUI you can type while the
agent is working and press Enter to steer mid-turn (that's an `Interject`), and a
finished subagent surfaces as a dim notice that auto-wakes the next turn. The
front-ends are thin; the engine is shared.
