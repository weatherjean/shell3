# CLI, headless mode, and scripting

shell3 is a Unix tool first. It reads stdin, writes stdout, exits with a status,
and pipes like anything else in your shell. This page covers running it
non-interactively, the audit log, the read-only query commands, and the
in-session slash commands.

## Interactive vs. headless

With no message, shell3 opens an interactive session (the TUI):

```sh
shell3                       # start a session
shell3 --resume 42           # resume stored session 42
shell3 -c plan               # use ~/.shell3/plan.lua
shell3 --agent review        # start on a specific agent
```

Give it a message — as an argument or on stdin — and it runs exactly one turn,
prints the result, and exits:

```sh
shell3 "explain the failing test"       # one-shot
git diff | shell3 "write a commit msg"  # reads stdin like any filter
echo "$LOG" | shell3 "what broke?"      # stdin as context
```

In headless mode shell3 strips the interactive-shell tool from the model's
schema and tells the model that no human is present, so it decides and proceeds
rather than asking questions it can't get answered.

## The audit log (`--out`)

Add `--out <file>` and shell3 writes a lossless JSONL record of the run — one
line per event:

```sh
shell3 "audit deps" --out audit.jsonl
```

Every assistant token, every tool call **with its raw arguments**, every tool
result, the usage counts, and the terminal status are all there. It's meant to
be consumed by downstream tooling — grep it, pipe it into `jq`, replay it,
diff two runs. Nothing is summarized or lossy.

## Reading your history (read-only commands)

Conversation history, sessions, and background jobs all live in one shared SQLite
database at `~/.shell3/data/shell3.db` (WAL mode). shell3 exposes a few
**read-only** commands over it — these only ever query, never mutate:

| Command | What it shows |
|---------|---------------|
| `shell3 fts <query>` | Full-text search across stored messages |
| `shell3 list-projects` | Projects shell3 has seen (by their UUID) |
| `shell3 list-sessions` | Stored sessions |
| `shell3 jobs` | Background jobs (self-pruning on list) |

These same queries are available to the agent from inside a session via the
`history` skill — so the model can search its own past conversations with `bash`,
without any special tool.

## First-run setup

```sh
shell3 boot              # interactive: endpoint, model, name, API key → writes config
shell3 boot --telegram   # scaffold a Telegram host config under ~/.shell3/telegram/
```

`boot` writes `~/.shell3/shell3.lua`, the `lib/` modules, and `~/.shell3/.env`.
See [configuration.md](configuration.md) for what it produces and how to extend
it, and [telegram.md](telegram.md) for the Telegram host.

## Slash commands (in-session)

Inside an interactive session, type `/help` for the full list. The common ones:

| Command | Effect |
|---------|--------|
| `/agent` | Switch the active agent (or `Tab` when idle) |
| `/clear` | Clear the conversation |
| `/rollback` | Undo back to an earlier point |
| `/prune <id>` | Drop a specific message from context |
| `/parameters` | Inspect the model parameters in effect |
| `/help` | List everything available |

## Platform support

shell3 targets Unix-like systems — Linux and macOS. Windows is **not** supported:
it leans on Unix process groups and TTY semantics that don't map cleanly to
Windows. WSL works.
