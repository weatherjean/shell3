# CLI reference

Bare `shell3` is the local orchestrator. The remaining commands configure or
operate the same harness. `shell3 service` is a deliberately headless process,
not a second agent or conversation runtime.

## Local orchestrator

```sh
shell3
shell3 --here
shell3 --config /path/to/shell3.lisp --workdir /path/to/workdir
shell3 -p 'one-shot request'
```

Without path flags, shell3 uses `~/.shell3/shell3.lisp` and
`~/.shell3/workdir`. `--here` selects `./shell3.lisp` and the current
directory. It cannot be combined with `--config` or `--workdir`; explicit
mode requires both flags so configuration and state cannot accidentally come
from different modes.

Without `-p`, shell3 starts a line-oriented attached conversation. Input sent
while a turn is running is ignored, so the console always presents one prompt
at a time; Telegram retains its mid-turn steering behavior. Press Escape to
cancel the active console turn. `/`, `/h`, and `/help` print command help;
`/exit` and `/quit` exit. The unadvertised `/test_output` diagnostic prints
deterministic sample rendering without calling the model.
`/reload` validates and activates a complete new kit generation. Idle and
future sessions switch immediately; a busy Telegram room switches after its
current turn. Invalid edits leave the current generation active.

The command starts an in-process runtime rather than attaching its conversation
to the Telegram process. Both may nevertheless use the same default workdir
and durable state concurrently. The console never consumes `main` inbox
notices automatically. It prints a human-only pending count at startup and
before each user-requested model turn; neither the count nor notice content is
added to the prompt. Ask the agent to check the inbox when ready.
See [Operations](operations.md) for process and state lifetime.

The console preserves normal terminal scrollback. It renders completed model
responses as Markdown, animates a foreground-only rainbow thinking marker, and
collapses Bash results to one bounded display line. Display truncation never
changes the full result returned to the model.

`-p` runs one headless turn and waits for background work spawned by that turn.
Positional prompts are rejected so a misspelled command cannot silently become
a model request.

## `shell3 telegram`

```sh
shell3 telegram
shell3 telegram --here
shell3 telegram --config /path/to/shell3.lisp --workdir /path/to/workdir
shell3 telegram --console
```

Telegram is remote control for the same Lisp-configured orchestrator. The
configuration must contain a strict `telegram` form naming its token variable,
home chat, optional operator allowlist, turn concurrency, and group trigger
policy. The token value is resolved from the inherited process environment.

The adapter answers `/ask`, `/help`, `/stop`, `/superstop`, `/new`, and
`/reload` on the host side. It gives attached main sessions one transport tool named `telegram`
for sending local files. `--console` runs the same bot contract over standard
input and output without credentials or network access.
Unlike the local console, a Telegram adapter must remain running to receive new
remote messages. A restarted adapter resumes its persisted per-room
conversations. A persisted Telegram adapter also owns declared schedules. A
schedule edit requires restarting it; `/reload` rejects that partial change.
The real adapter posts `๑ï shell3 started` to the home chat after startup and
`๑ï shell3 shutting down` on a graceful exit; neither message starts a model
turn, and `--console` emits neither one. Startup is delivered before any
pending-inbox alert. Lifecycle and inbox alerts are silent notifications.
When a `main` notice arrives, Telegram posts a `✉️` pending count and a
bounded preview of the latest notice to the home chat. This is a host message,
not a quiet model turn. A local console may run against the same state while
Telegram is active.

## `shell3 service` and `shell3 schedule`

```sh
shell3 service --config /absolute/shell3.lisp --workdir /absolute/project
shell3 schedule list --config /absolute/shell3.lisp --workdir /absolute/project
shell3 schedule run --config /absolute/shell3.lisp --workdir /absolute/project NAME
shell3 schedule history --workdir /absolute/project [NAME]
shell3 schedule history --workdir /absolute/project --status failed --limit 20
```

`service` is the foreground headless schedule and workflow host intended to be
kept alive by launchd, systemd, or an equivalent service manager when Telegram
is not the persistent host. It opens no model session and resolves no attached
model or Telegram credential. Exactly one service or Telegram process may own
one project's schedule lock.
`schedule list` emits one resolved declaration per JSONL record, including its
name, cron expression, timezone, wrkfile, task, required output, timeout,
overlap policy, and notification target. It reads no runtime state: existing
run directories are execution history and must not be used as a declaration
inventory.

These commands use the global defaults when path flags are omitted and accept
`--here`; explicit config mode requires both path flags.

`schedule run` manually fires one strict Lisp declaration through the same
durable path as the clock. `history` reads the SQLite ledger and emits one JSON
record per line, newest first. Actual runs have only `running`, `done`, and
`failed` statuses; timeout is a failed result, while an overlap skipped by
policy is recorded only in the rotating application log. Each row points to
the wrk run directory and required output file rather than copying artifact
contents into SQLite.

## `shell3 config check`

```sh
shell3 config check /path/to/shell3.lisp
```

Parses, resolves, and strictly validates the configuration. Unknown forms,
duplicate or misplaced fields, unresolved models, and invalid runner protocols
are errors. Every scheduled wrkfile is also loaded and validated.

## `shell3 boot` and embedded skills

```sh
shell3 boot
shell3 boot /path/to/shell3.lisp
shell3 config skill /path/to/shell3.lisp web-search
```

`boot` exclusively creates one complete kit and refuses to overwrite an
existing file. The generated Lisp contains its own header, memory, full base
prompt, skills, model declaration, extension sections, and optional Telegram
example. It creates no `.env`, skills directory, memory file, or helper script.
With no path, `boot` writes `~/.shell3/shell3.lisp`; `--here` writes
`./shell3.lisp`.

`config skill` validates the whole kit and prints one named embedded skill body.
It lets the orchestrator load guidance lazily without injecting every skill
body into every model turn.

## `shell3 notify`

```sh
shell3 notify --to main 'message'
shell3 notify --to main --event completed < result.txt
shell3 notify --to wrk:TASK/RUN --state .shell3_project 'resume input'
```

`notify` first persists a typed inbox message, then attempts to alert the
persistent host. A successful command means the message was durably accepted;
its JSON receipt separately reports whether the advisory datagram arrived.
For `main`, that datagram can produce a Telegram pending count plus latest
preview but never starts a model turn.

Options:

- `--to`: required destination: `main` or `wrk:<task>/<run>`.
- `--workdir`: workdir whose state receives the message, default
  `~/.shell3/workdir`.
- `--here`: use the current directory's state.
- `--state`: advanced direct state-root override.
- `--source`: machine-origin source label.
- `--event`: event kind, default `message`.
- `--correlation`: optional correlation identifier.

With no message argument, the body is read from standard input up to 1 MiB.

## `shell3 inbox`

```sh
shell3 inbox list
shell3 inbox list --status archived --offset 10 --limit 10
shell3 inbox read msg_0123456789abcdef0123456789abcdef
shell3 inbox read msg_0123456789abcdef0123456789abcdef --offset 8192
shell3 inbox archive msg_ID1,msg_ID2
```

`inbox` inspects the filesystem mailbox without loading its contents into a
model turn. It defaults to the global workdir, accepts `--here` or
`--workdir`, and never needs `--config`. Use `--state` only for a direct
state-root override and `--to` for a destination other than `main`.

`list` emits JSON metadata and short previews. It returns ten notices by
default, accepts `new`, `processing`, `pending`, `archived`, or `all`, and has a
hard limit of 100 entries. `read` emits one JSON body chunk, defaults to 8 KiB,
and has a hard limit of 32 KiB. Pending notices must be read sequentially from
offset zero; `next_offset` identifies the next UTF-8-safe chunk or page and
durable read progress prevents skipped content from counting as read.
`archive` accepts one or more arguments containing comma-separated IDs and
rejects the batch before moving anything unless every notice is pending and
fully read. The move into `archived` is atomic per notice. Archived notices
remain available indefinitely; cleanup is an explicit filesystem operation.

## `shell3 wrk`

```sh
shell3 wrk check task.wrk.lisp
shell3 wrk compile --config shell3.lisp task.wrk.lisp
shell3 wrk run --config shell3.lisp task.wrk.lisp 'request'
shell3 wrk beat TASK/RUN
shell3 wrk status TASK/RUN
shell3 wrk signal TASK/RUN EVENT 'message'
shell3 wrk cancel TASK/RUN
```

`check` validates workflow syntax and schema. `compile` emits inspectable Bash.
`run` creates durable state and advances until the workflow completes, fails,
or waits. Its foreground transcript streams node lifecycle and runner output
while retaining the durable logs. With no request argument it reads a pipe, or
starts with an empty request when standard input is a terminal. `beat` advances
one runnable wave with the same foreground transcript. `status` prints
the durable snapshot as JSON. `signal` records an external event and wakes an
attached router when possible. `cancel` records terminal cancellation.

Dispatched agents cannot start, beat, signal, or cancel workflows themselves;
they are leaf workers. See [Wrk workflows](wrk.md) for configuration, DSL, and
state semantics.

## Help and version

```sh
shell3 --help
shell3 <command> --help
shell3 --version
```

All paths default to the current directory where stated. The portable
`shell3.lisp` kit and project `.shell3_project/` runtime state remain separate.
Credential values come only from the inherited process environment.
