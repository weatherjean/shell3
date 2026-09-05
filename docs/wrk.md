# Workflows

`*.wrk.lisp` files are strict workflow definitions parsed as inert data.

Each file contains exactly one task:

```lisp
(task "checked-change"
  (root ".")
  (parallel 2)
  (timeout "45m")

  (agent inspect
    (using builder)
    (access read)
    (prompt "Inspect the code. Write findings to $TASK_ARTIFACTS/findings.md.")
    (accept (file "findings.md")))

  (loop implement
    (using builder)
    (after inspect)
    (access write)
    (max 3)
    (prompt "Implement the requested change.")
    (until (sh "make test")))

  (command verify
    (after implement)
    (access read)
    (run "make test")))
```

`root` defaults to `.`, `parallel` defaults to `1`, and `timeout` is optional.
Paths are resolved from the wrkfile. Node names are symbols and must be unique.
Dependencies must exist and the graph must be acyclic.

## Nodes

| Form | Required fields | Purpose |
|---|---|---|
| `agent` | `using`, `prompt` | Run one configured external agent. |
| `loop` | `using`, `prompt`, `max`, `until` | Run a fresh agent for each attempt until the check passes. |
| `command` | `run` | Execute a deterministic shell command. |
| `wait` | `for (event NAME)` | Pause until a matching durable signal arrives. |

Common fields are `after`, `timeout`, and—except for waits—`access read|write`.
`read` is the default. Independent readers can share a wave up to the task's
parallel limit; a writer runs alone.

An `agent` or `command` may use `accept`. A loop uses `until`. Checks are:

```lisp
(accept (file "relative/artifact"))
(until (sh "test -s \"$TASK_ARTIFACTS/report.md\""))
```

File-check paths are relative to the run's artifact directory. Shell checks run
outside the agent, so the verifier—not the worker's claim—decides success.

A wait can include an operator-facing message:

```lisp
(wait approval
  (after draft)
  (for (event "approved"))
  (message "Review the draft, then signal approved."))
```

## Runners and agents

Wrkfile `using` names resolve to configured agent profiles:

```lisp
(runner codex
  (parameters (model string required))
  (command "codex" "exec")
  (arguments
    "--output-last-message" result-file
    "--cd" workdir
    "--model" model
    "-")
  (stderr log)
  (result (file result-file))
  (success (exit 0))
  (timeout "30m"))

(agent builder
  (using codex)
  (model "model-id"))
```

The runner declaration is typed argv, not a shell template. shell3 sends the
prompt on standard input, runs in the task root, and retains stdout. The prompt
identifies the process as a leaf worker; the CLI also rejects nested workflow
control.

Task instructions belong in node prompts. An agent form only selects a runner
and binds its parameters. See [configuration.md](configuration.md) for the full
runner schema and defaults.

## Running

Validate before execution:

```sh
shell3 config check shell3.lisp
shell3 wrk check --config shell3.lisp change.wrk.lisp
shell3 wrk compile --config shell3.lisp change.wrk.lisp
shell3 wrk run --config shell3.lisp change.wrk.lisp 'request'
```

`compile` emits inspectable Bash. `run` creates a durable run, then advances it
until it completes, fails, or waits. Foreground runs stream lifecycle and
runner output while retaining the same data in the run directory.

Each run snapshots the config and wrkfile sources, their hashes, the task root,
request, and executable path. Later beats use the snapshots. A run lock
serializes beats, and state files are replaced atomically. After interruption,
a node left `running` returns to `pending` and may execute again.

Useful controls:

```sh
shell3 wrk status TASK/RUN
shell3 wrk beat TASK/RUN
shell3 wrk signal TASK/RUN approved 'review complete'
shell3 wrk cancel TASK/RUN
```

These controls default to `./.shell3_project/wrk`; pass `--state` when the run
uses another state root.

Signals are durable inbox events addressed to `wrk:TASK/RUN`. The router claims
them atomically, records them in the run ledger, and acknowledges only after
that write. A live host reduces latency; startup and periodic reconciliation
recover notices accepted while no host was running.

Run state, prompts, stdout, stderr, command output, verification logs, and
artifacts live under `<state>/<task>/<run-id>/`. An invalid route record gains
an `.invalid` suffix and is reported in `errors.jsonl`.

## Scheduled workflows

A schedule in `shell3.lisp` names a wrkfile:

```lisp
(schedule daily-report
  (cron "0 8 * * *")
  (timezone "Europe/Ljubljana")
  (run (wrkfile "workflows/daily-report.wrk.lisp"))
  (request "Produce the daily report.")
  (output "report.md")
  (timeout "30m")
  (overlap skip)
  (notify "main"))
```

`cron`, `timezone`, `run`, `output`, and `timeout` are required. The wrkfile is
relative to `shell3.lisp`; `output` is relative to the run's artifact directory.
`overlap` is `skip` (default) or `allow`. `request` is optional and `notify`
defaults to `main`.

Every admitted fire is a durable wrk run indexed in SQLite as `running`, `done`,
or `failed`; a skipped overlap creates no run. The timeout includes waiting.
Success requires a symlink-free regular output file. See
[operations.md](operations.md) for host ownership and recovery.
