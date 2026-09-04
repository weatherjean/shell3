# Workflows

`*.wrk.lisp` files are strict workflow definitions parsed as inert data.

Each file contains exactly one task:

```lisp
(task "checked-change"
  (root ".")
  (parallel 2)
  (timeout "45m")

  (agent inspect
    (using researcher)
    (access read)
    (prompt "Inspect the code and write findings.md.")
    (accept (file "findings.md")))

  (loop implement
    (using builder)
    (after inspect)
    (access write)
    (max 3)
    (prompt "Implement the requested change.")
    (until (sh "go test ./...")))

  (command package
    (after implement)
    (access read)
    (run "tar -czf \"$TASK_ARTIFACTS/result.tgz\" .")
    (accept (file "result.tgz"))))
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

File checks are confined to the run's artifact directory. Shell checks run
outside the agent, so the verifier—not the worker's claim—decides success.

A wait can include an operator-facing message:

```lisp
(wait approval
  (after draft)
  (for (event "approved"))
  (message "Review the draft, then signal approved."))
```

## Runners and agents

Wrkfile `using` names resolve through `shell3.lisp`:

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

The runner declaration is a typed argv protocol, not an interpolated shell
template. shell3 sends the prompt on standard input, runs in the task root, and
always retains stdout. The generated prompt identifies the process as a leaf
worker; nested workflow control is also rejected by the CLI.

Task instructions belong in node prompts. An agent form only selects a runner
and binds its declared parameters.

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

Each run snapshots the resolved config, wrkfile, hashes, task root, request,
and runner protocol. Later beats use that snapshot, not mutable source files.
State transitions are atomic. Interrupted running work is recovered
at-least-once.

Useful controls:

```sh
shell3 wrk status TASK/RUN
shell3 wrk beat TASK/RUN
shell3 wrk signal TASK/RUN approved 'review complete'
shell3 wrk cancel TASK/RUN
```

Signals are durable inbox events addressed to `wrk:TASK/RUN`. The router claims
them atomically, records them in the run ledger, and acknowledges only after
that write. A live host reduces latency; startup and periodic reconciliation
recover notices accepted while no host was running.

Run state, prompts, stdout, stderr, command output, verification logs, and
artifacts live under `.shell3_project/wrk/`. An invalid route is renamed with
an `.invalid` suffix and reported in `errors.jsonl`.

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

The wrkfile path is relative to `shell3.lisp`. `output` is relative to the run's
artifact directory. `overlap` is `skip` or `allow`; `request` is optional and
`notify` defaults to `main`.

Every admitted fire is an ordinary durable wrk run indexed in SQLite as
`running`, `done`, or `failed`. The timeout includes waiting. Completion
requires the declared output to exist as a regular file. See
[operations.md](operations.md) for host ownership and recovery.
