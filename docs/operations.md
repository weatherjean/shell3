# Operations

## Runtime state

The working bubble names host actions and shows their immediate outcomes.
`shell3` action `restart` initially reports **queued**, not completed. The bot
receives authoritative system context with recent action receipts and the
current runtime instance on every model round, including after a restart.
Receipts live in `.shell3_project/host-actions.json` (the latest 12 events).

Internal restart preserves the PID and environment and does not rerun your
service launcher. To pick up credential-injection or launcher changes, restart
through the service manager. `/superstop` cancels work; it does not restart the
service. A matching PID alone is not evidence that internal restart failed.

If the model makes no progress for five minutes, shell3 cancels that provider
request, reports an inactivity error, and releases the conversation's turn.
This also covers requests that never begin responding. Active reasoning and
output keep the request alive; keepalive traffic alone does not. Ask to continue
after the error; shell3 does not automatically replay a timed-out response.

For a selected workdir, shell3 stores project state under `.shell3_project/`:

```text
shell3.db       conversations, front-end markers, running jobs, schedule runs
inbox/          durable notices and read progress
runs/           provider failure dumps and background-command logs
wrk/            workflow snapshots, state, logs, and artifacts
wrk-routes/     workflow inbox routing records
errors.jsonl    structured diagnostics with rotating archives
```

Locks, the wake socket, and SQLite WAL files may also appear while a process is
running. `.shell3_project/.gitignore` excludes the directory from an enclosing
repository. Do not commit it.

Stop processes using the workdir before copying it for backup. Conversation,
inbox, workflow, and schedule state must be restored together.

Telegram downloads attachments to `~/.shell3/media` or `SHELL3_MEDIA_DIR`, not
the project state directory. Files persist until the operator removes them.

## Process lifetime

Bare `shell3` owns its conversation and background commands. Graceful close
cancels managed jobs; they are not resumable. A new console starts a new
conversation.

`shell3 telegram` is a long-running remote adapter. It resumes each room from
its persisted marker. `shell3 service` is the headless alternative: it opens no
model conversation and exists only to host schedules and workflow routing.

Run exactly one persistent host per project:

```text
Telegram required   shell3 telegram
Telegram not needed shell3 service
```

The wake and schedule locks reject a second owner. Keep the command alive with
launchd, systemd, or another service manager. shell3 does not install services.

Use absolute executable, config, and workdir paths in service definitions.
Inject only required environment variables. A headless service does not resolve
the attached-model or Telegram credentials; inject only the credentials its
workflow nodes need.

## Reload and restart

`/reload` validates a complete configuration generation before activation.
Idle sessions and future turns adopt it immediately; a busy turn finishes with
its captured generation. Invalid input leaves the active generation unchanged.

Telegram `token-env`, `home-chat`, and all schedule declarations are
restart-only. Other valid Telegram configuration reloads between turns.
`service` has no reload command; restart it after any relevant config change.

The attached Telegram agent uses its bounded `shell3` tool for `status`,
`validate`, `reload`, and `restart`. The tool is pinned to the running host and
accepts no paths, commands, PIDs, or service-manager names. It is supplied by
the host rather than declared in `shell3.lisp`; `config_change` compares only
the active and on-disk parsed configuration. Results identify the tool and
interface version, name changed top-level sections, and include semantic
SHA-256 fingerprints plus the active generation's load time. The fingerprints
ignore source formatting and comments and never contain resolved secrets.
Restart validates first, gates new turns, drains active replies, and re-execs
the persistent host only after successful delivery. `/reload` remains the
operator shortcut over the same controller.

Graceful shutdown cancels live model work and managed background process groups.
A background command interrupted by shutdown keeps its SQLite marker so the
next runtime can report the loss. Normal completion writes its durable inbox
notice before removing the marker.

## Inbox

The `main` inbox is passive. The console prints a pending count; Telegram sends
a host-owned pending alert. Neither action starts a model turn or inserts the
notice into a prompt.

Agent-requested `bash_bg` progress checks are separate: `poll_in: "2m"` requests
one future model turn. A busy conversation defers it. If the command finishes
first, the requested check still runs when due to report its outcome.
The agent may re-arm a running job with `shell3` action `poll`. These checks
spend a model turn when due and do not survive host restart.
`/stop` clears the conversation's pending checks; `/superstop` also kills managed
commands. Completion notices still remain passive. The inbox skill permits
reading relevant notices as part of the user's ongoing task.

Inspect and archive notices explicitly:

```sh
shell3 inbox list --workdir /path/to/project
shell3 inbox read MESSAGE_ID --workdir /path/to/project
shell3 inbox archive MESSAGE_ID --workdir /path/to/project
```

Archived notices remain on disk until removed manually. Workflow destinations
are handled by the durable router and may be processed more than once after a
crash.

## Schedules

Schedules are declarations in `shell3.lisp`; each invokes a checked wrkfile.
Validate and manually fire a new schedule before relying on the clock:

```sh
shell3 config check /absolute/shell3.lisp
shell3 schedule list --config /absolute/shell3.lisp --workdir /absolute/project
shell3 schedule run --config /absolute/shell3.lisp --workdir /absolute/project NAME
shell3 schedule history --workdir /absolute/project NAME
```

An admitted run has a `running` SQLite row and is reconciled after restart.
Calendar occurrences missed while no host was running are not replayed. Success
requires a complete workflow and its declared symlink-free regular output file.

## Diagnostics

`errors.jsonl` rotates at 10 MiB and retains five archives. Provider failures go
to `runs/<session-id>/last_error.json`; background output goes to that session's
`jobs/` directory. These files can contain user, model, command, or provider
content. Review and redact them before sharing.

There is no automatic retention for conversations, archived notices, workflow
runs, artifacts, job logs, or Telegram media.

Run the deterministic local acceptance path before a paid or authenticated
test:

```sh
make acceptance
```

It exercises config validation, compilation, workflow execution, verification,
schedule admission, durable output, notification, and structured logging
without network access or credentials.
