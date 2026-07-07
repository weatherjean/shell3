# CLI, headless mode, and scripting

shell3 is a Unix tool first. It reads stdin, writes stdout, exits with a status,
and pipes like anything else in your shell. This page covers running it
non-interactively, the audit log, the read-only query commands, and the
in-session slash commands.

## Interactive vs. headless

The bare `shell3` command opens an interactive session (the TUI):

```sh
shell3                       # start a session
shell3 --resume 20060102T150405.000000000  # resume a stored session by id
shell3 -c plan               # use ~/.shell3/plan.lua
shell3 --agent review        # start on a specific agent
```

A resume without `--config` runs under the config the session was originally
created with (recorded in its metadata), so a conversation started under
`~/.shell3/plan.lua` resumes under it too. An explicit `--config` always wins.

The `run` subcommand is the headless mode: it runs exactly one turn, prints the
result, and exits. The prompt comes from `--prompt`, positional arguments, or
stdin — the first non-empty source wins:

```sh
shell3 run "explain the failing test"                 # one-shot
git log -3 | shell3 run                               # stdin is the prompt
{ echo "write a commit msg:"; git diff; } | shell3 run  # compose a prompt in the pipe
```

In headless mode shell3 strips the interactive-shell tool from the model's
schema and tells the model that no human is present, so it decides and proceeds
rather than asking questions it can't get answered.

### `shell3 run` flags

| Flag | Effect |
|------|--------|
| `--prompt <text>` | The prompt for this run (alternative to positional args / stdin) |
| `-c`, `--config <name\|path>` | Config name (→ `~/.shell3/<name>.lua`) or path to a `*.lua` file (default `~/.shell3/shell3.lua`) |
| `--agent <name>` | Select the active agent (default: first declared). May also name a registered subagent |
| `--resume <id>` | Resume a stored session by id: reload its messages and continue the conversation (without `--config`, runs under the session's recorded config) |
| `--out <path>` | Stream a JSONL audit log of this run to `<path>` (see below) |

## The audit log (`--out`)

Add `--out <file>` and shell3 writes a lossless JSONL record of the run — one
line per event:

```sh
shell3 run "audit deps" --out audit.jsonl
```

Every assistant token, every tool call **with its raw arguments**, every tool
result, the usage counts, and the terminal status are all there. It's meant to
be consumed by downstream tooling — grep it, pipe it into `jq`, replay it,
diff two runs. Nothing is summarized or lossy.

## Reading your history

Conversation history lives as plain JSONL files under `.shell3_project/runs/`
in the project directory. Use standard Unix tools to query it:

```sh
# Full-text search across all sessions
rg -n "JWT|expiry" .shell3_project/runs

# List sessions (newest first)
ls -lt .shell3_project/runs/

# Read a session's metadata
cat .shell3_project/runs/<id>/meta.json

# Dump a session in formatted form
shell3 read-session <id>
```

The agent can search its own past conversations the same way (via the
`history` skill), using `bash` with `rg` — no special tool needed.

## First-run setup

```sh
shell3 boot     # interactive: endpoint, model, name, API key → writes config
```

`boot` writes `~/.shell3/shell3.lua`, the `lib/` modules, and `~/.shell3/.env`.
See [configuration.md](configuration.md) for what it produces and how to extend it.

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
