# Operations

## Runtime state

For a selected workdir, shell3 stores local state under
`.shell3_project/`:

```text
shell3.db       conversations, room markers, running commands, schedule index
inbox/          durable notices and read progress
runs/           session diagnostics and background-command logs
wrk/            workflow snapshots, state, logs, and artifacts
errors.jsonl    structured diagnostics with rotating archives
```

Do not commit this directory. Back it up when conversation, inbox, or workflow
recovery matters.

The database is created automatically on first use. Workflow runs and inbox
files are stored separately.

## Process lifetime

Bare `shell3` owns its local conversation and any background commands it
starts. Closing it stops that runtime. A new console starts a new conversation.

`shell3 telegram` is a long-running remote adapter. It resumes each room from
its persisted marker. `shell3 service` is the headless alternative: it opens no
model conversation and exists only to host schedules and workflow routing.

Use exactly one persistent host per project:

```text
Telegram required   shell3 telegram
Telegram not needed shell3 service
```

The project schedule lock rejects a second owner. Keep the chosen foreground
command alive with launchd, systemd, or an equivalent service manager. shell3
does not install or edit host services.

Use absolute executable, config, and workdir paths in service definitions.
Inject only the environment variables required by that process. A headless
service does not need the attached model or Telegram credentials, though its
external runners may require their own credentials.

## Reload and restart

`/reload` validates a complete configuration generation before activation.
Idle sessions and future turns adopt it immediately; a busy turn finishes with
its captured generation. Invalid input leaves the active generation unchanged.

Telegram connection fields and schedule declarations are restart-only. Restart
the persistent host after changing them.

Graceful shutdown cancels live model work and managed background process groups.
A background command interrupted by shutdown keeps its SQLite marker so the
next runtime can report the loss. Normal completion writes its durable inbox
notice before removing the marker.

## Inbox

The `main` inbox is passive. The console prints a pending count; Telegram sends
a host-owned pending alert. Neither action starts a model turn or inserts the
notice into a prompt.

Inspect and archive notices explicitly:

```sh
shell3 inbox list --workdir /path/to/project
shell3 inbox read MESSAGE_ID --workdir /path/to/project
shell3 inbox archive MESSAGE_ID --workdir /path/to/project
```

Archived notices remain on disk until the operator removes them. Workflow
destinations are handled by the durable router and may be processed more than
once after a crash.

## Schedules

Schedules are declarations in `shell3.lisp`; each invokes a checked wrkfile.
Validate and manually fire a new schedule before relying on the clock:

```sh
shell3 config check /absolute/shell3.lisp
shell3 schedule list --config /absolute/shell3.lisp --workdir /absolute/project
shell3 schedule run --config /absolute/shell3.lisp --workdir /absolute/project NAME
shell3 schedule history --workdir /absolute/project NAME
```

An admitted schedule run has a `running` SQLite row and is reconciled after a
restart. Calendar occurrences missed while no host was running are not replayed.
A run succeeds only when its workflow is complete and its declared output is a
regular file under the run's artifacts directory.

## Diagnostics

`.shell3_project/errors.jsonl` rotates at 10 MiB and retains five archives.
Per-session provider failures are written to
`runs/<session-id>/last_error.json`. Background output is kept in the session's
job-log directory. These files can contain user, model, command, or provider
content; review and redact them before sharing.

Run the deterministic local acceptance path before a paid or authenticated
test:

```sh
make acceptance
```

It exercises config validation, compilation, workflow execution, verification,
schedule admission, durable output, notification, and structured logging
without network access or credentials.
