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

`compile` emits a Bash launcher that pins the config and workflow source hashes
and calls `shell3 wrk run`. Keep those source files at their original paths;
regenerate the launcher after edits. `run` creates a durable run, then advances it
until it completes, fails, is cancelled, or waits. Foreground runs stream lifecycle and
runner output while retaining the same data in the run directory.

From the attached agent, use `bash_bg` for `wrk run`, `wrk beat`, and
`schedule run`, optionally with `poll_in: "2m"`. The short-lived `bash` tool
marks its process environment as foreground; these execution commands reject
that context before admission. Direct terminal execution remains supported.
This inherited marker is an execution contract, not a security boundary.
Closing a progress consumer such as `head` does not stop execution or truncate
the durable logs. The driver still needs to remain alive.

Each run snapshots the config and wrkfile sources, their hashes, the task root,
request, and executable path. Later beats use the snapshots. A run lock
serializes beats, and state files are replaced atomically. Graceful driver
cancellation records `interrupted`; the original deadline is retained. A later
beat resets interrupted or abandoned running nodes to pending and may execute
them again. Inspect prior attempts and side effects before recovery.

`wrk status` checks the execution lock, rather than treating durable markers or
artifacts as evidence of a live worker:

| Status | Meaning |
| --- | --- |
| `running` | An execution owner holds the lock. This does not prove useful progress. |
| `ready` | No execution owner; a beat can advance the run. |
| `waiting` | No execution owner; waiting for an external event. |
| `interrupted` | No execution owner; unfinished execution needs recovery or cancellation. |
| `expired` | No execution owner and the deadline has elapsed; a beat records failure without launching work. |
| `completed`, `failed`, `cancelled` | Persisted terminal outcome. |

JSON includes `execution_active`, `persisted_status`, `observed_at`, `deadline`,
`deadline_exceeded`, and `recovery_required`, plus exact attempt/log/result paths.
Those paths may not exist yet. Inspection does not rewrite state or start work.
An active owner past its deadline remains visibly active with an explicit
deadline warning, so shutdown is not mistaken for completion.

The external-runner helper watches a driver-lifetime pipe. If the driver dies,
including by SIGKILL, the helper cancels and reaps its runner process group. It
retains the execution lock through cleanup, preventing a new beat from starting
the same work meanwhile. Liveness and output attribution are separate: shared
artifact files can be written by other processes. Put reproductions in their own
run or scratch directory; inspect the exact attempt logs for evidence.

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

Use `(notify-failure "main")` to override the destination for a failed or
cancelled workflow while retaining a quieter mailbox for successful runs:

```lisp
(notify "quiet")
(notify-failure "main")
```

This sends one notice to the selected destination, not to both. The override is
optional; omitting it preserves `notify` for every outcome. Routes are snapshotted
at admission and survive restart or removal of the schedule. A failed notice
write keeps the schedule row running until reconciliation persists it. As with
other workflow notices, a crash between inbox persistence and receipt persistence
can repeat delivery. Failures before a valid workflow snapshot exists are recorded
in the schedule ledger and application log, not through this terminal route.
`quiet` and `silent` are ordinary mailbox names, not discard policies. Only `main`
is reviewed automatically by an attached host.

Business outcomes such as “inserted” and “skipped” belong in the accepted report.
Use an external check to verify database effects, current-run receipts, and a
nonempty report. See the [verified-outcome example](../examples/verified-outcome/README.md).

Every admitted fire is a durable wrk run indexed in SQLite as `running`, `done`,
or `failed`; a skipped overlap creates no run. The timeout includes waiting.
Success requires a symlink-free regular output file. See
[operations.md](operations.md) for host ownership and recovery.
