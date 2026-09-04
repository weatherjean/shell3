<p align="center">
  <img src="docs/assets/shell3-banner.svg" alt="shell3" width="100%">
</p>

shell3 is a small harness harness: a local orchestrator that can work directly
through Unix tools or dispatch durable workflows to other agent harnesses.
The terminal is its primary control surface. Telegram is an optional remote
control adapter over the same orchestrator, inbox, and workflow machinery.

One strict `shell3.lisp` is the complete portable kit: model declarations,
prompt, memory, skills, harness
protocols, agent profiles, schedules, and optional Telegram configuration. `*.wrk.lisp`
files remain task-specific workflows.

## Build and run

```sh
make build
./shell3 boot
./shell3 config check ~/.shell3/shell3.lisp
./shell3
```

The default runtime uses `~/.shell3/shell3.lisp` and
`~/.shell3/workdir`. Use `--here` for `./shell3.lisp` plus the current
directory, or provide `--config` and `--workdir` together. With no `-p`,
bare `shell3` opens a line-oriented conversation. It deliberately
does not take over the terminal: output remains normal scrollback, tool results
are bounded, and final responses render as Markdown. A one-shot turn is
explicit:

```sh
./shell3 --config /path/to/shell3.lisp \
  --workdir /path/to/project \
  -p 'Inspect this repository and report its test command.'
```

Use `/reload` between turns after editing the kit. The new generation is
validated before it replaces the current model, prompt, memory, skills, and
session factory.

Secrets are named in `shell3.lisp` and supplied through the process
environment. Export them from a shell profile for interactive use or inject
them with the host service/secret manager. Never put secret values in Lisp.

## Product shape

The attached orchestrator receives only:

- `bash`
- `bash_bg`
- `edit_file`

It uses those tools for local work, research, creating wrkfiles, and operating
the shell3 CLI. A Telegram-attached session receives one additional
`telegram` tool whose sole purpose is sending a local file to the current
chat. Text travels as the ordinary assistant response.

Skills are forms inside `shell3.lisp`. The orchestrator sees their names and
descriptions and reads one body lazily with `shell3 config skill` only when it
applies. Memory is another kit form and is included on every turn.

## Configuration

```lisp
(shell3
  (version 1)

  (memory "Prefer concise progress reports.")

  (model primary
    (base-url "https://provider.example/v1")
    (api-key-env SHELL3_API_KEY)
    (id "model-id")
    (reasoning medium)
    (max-tokens 16000)
    (context-window 128000))

  (orchestrator
    (model primary)
    (prompt """
You are the operator of shell3, a harness harness.
Use wrk when delegation or verification adds value.
"""))

  (skill example
    (description "Use when an example applies.")
    (instructions "Follow this reusable guidance."))

  (schedule daily-report
    (cron "0 8 * * *")
    (timezone "Europe/Ljubljana")
    (run (wrkfile "workflows/daily-report.wrk.lisp"))
    (request "Produce the daily report.")
    (output "report.md")
    (timeout "30m")
    (overlap skip)
    (notify "main")))
```

Runner and agent forms describe external harnesses as typed argv protocols,
not interpolated shell strings. See [Wrk workflows](docs/wrk.md) for the full
runnable format.

## Commands

| Command | Purpose |
|---|---|
| `shell3` | Open the local orchestrator conversation. |
| `shell3 boot [shell3.lisp]` | Write one complete kit; no argument uses `~/.shell3/shell3.lisp`. |
| `shell3 -p '…'` | Run one headless turn. |
| `shell3 telegram` | Attach Telegram remote control. |
| `shell3 telegram --console` | Exercise the Telegram bot contract locally. |
| `shell3 service` | Run the persistent headless schedule and workflow host. |
| `shell3 schedule list` | Inspect resolved schedule declarations as JSONL. |
| `shell3 schedule run <name>` | Fire one declared schedule immediately. |
| `shell3 schedule history [name]` | Inspect its SQLite run ledger as JSONL. |
| `shell3 config check <shell3.lisp>` | Strictly validate the complete kit. |
| `shell3 config skill <shell3.lisp> <name>` | Print one embedded skill body. |
| `shell3 notify --to <destination>` | Persist an inbox message and attempt an immediate host notification. |
| `shell3 inbox list|read|archive` | Explicitly inspect and archive durable inbox notices. |
| `shell3 wrk …` | Check, compile, run, inspect, signal, beat, or cancel a workflow. |

See the [CLI reference](docs/cli.md) for flags and the
[implementation contract](docs/internals.md) for architectural invariants.
Operational guidance is in [Operations](docs/operations.md), and the trust
boundary is summarized in [Safety](docs/safety.md).

## Durability and safety

Inbox delivery, including background command completion, is durable and
at-least-once.
Main inbox notices never start a model turn or enter a prompt automatically.
Telegram posts a host-owned `✉️` pending count with a short preview of the
latest notice, while the console prints the count at startup and whenever the
user sends a message. The user asks the agent to check the inbox when ready;
the embedded `shell3-inbox` skill owns the bounded read-and-archive procedure.
The real Telegram adapter also posts `๑ï shell3 started` after initialization
and `๑ï shell3 shutting down` on graceful exit. These lifecycle messages are
host-generated and never start a model turn. Startup arrives before pending
inbox alerts; lifecycle and inbox notifications are silent.
Workflow definitions and resolved configuration are hashed into each run so an
active run cannot silently change underneath its state. External agent workers
are leaves; the attached orchestrator owns delegation and verification.

Schedules always invoke wrkfiles. Exactly one persistent process owns a
project's schedule clock: either `shell3 telegram` or `shell3 service`. Keep
that foreground command alive with launchd, systemd, or the host equivalent;
the schedule lock prevents two owners from firing duplicate work. Schedule run
status and output pointers live in SQLite while full artifacts and logs remain
in the durable wrk run directory. An admitted run recovers after restart;
calendar occurrences missed while no owner is running are not replayed in
version 1.

The agent has a real shell. shell3 is a harness, not an operating-system
sandbox. Use a container or VM when hard isolation matters. Runtime state lives
under the selected workdir's `.shell3_project/`; do not commit it. Keep
secrets outside the kit.

## Development

```sh
make build
go test ./...
make lint
```

Linux, macOS, and WSL are supported. Native Windows is not. shell3 is
[MIT licensed](LICENSE).
