# Operations

## Process and state lifetime

shell3 persists state, not a mandatory daemon. By default the kit is
`~/.shell3/shell3.lisp` and the workdir is `~/.shell3/workdir`.
Conversation records, inbox and
outbox messages, wrk runs, and job logs live under the selected workdir's
`.shell3_project/` and survive process exit. Process-owned work does not:
`bash_bg` and live model turns require their owning shell3 process, and graceful
shutdown cancels their process groups while leaving honest recovery records.

Opening bare `shell3` starts a new in-process local runtime and conversation; it
does not attach to another shell3 process. Closing the console stops that
runtime. Stored local transcripts remain searchable, but the next console
launch currently starts a fresh local conversation rather than automatically
resuming the previous one.

`shell3 telegram` is one optional long-running case. It must remain alive to
poll Telegram and deliver remote input, and it resumes each Telegram room's
recorded conversation after restart. When the configuration declares
schedules, a persisted Telegram adapter also owns their clock.

`shell3 service` is the headless long-running alternative when Telegram should
not be persisted. It owns the same declared schedule clock and periodically
reconciles durable wrk routes, but opens no conversation or attached-model
client. launchd, systemd, or the host equivalent keeps the selected foreground
command alive across device restarts. The project schedule lock fails closed
if both persistent modes are started. A partially completed scheduled wrk run
remains recoverable from its immutable run directory. Version 1 does not replay
calendar occurrences missed while no owner process was running.

Infrastructure diagnostics are JSON records in
`.shell3_project/errors.jsonl`, not inbox work for the model. The logger is
active whenever an attached runtime is open, rotates the file at 10 MiB, and
retains `errors.jsonl.1` through `errors.jsonl.5`. The active file plus archives
use approximately 60 MiB at most. Invalid immutable workflow routes are
retained with an `.invalid` suffix and do not retry.

Interactive shells may export declared credential variables from `.zshrc`,
`.bashrc`, or their platform equivalent. Services should inject the same names
through their environment or secret manager. shell3 neither scans nor edits
shell startup files and does not load a kit-adjacent `.env`.

The `main` inbox is passive. No arrival starts an agent turn and neither notice
metadata nor bodies are injected into an unrelated prompt. Telegram posts a
human-only pending-count message when its persistent host receives a wake hint.
The local console prints the pending count at startup and before every ordinary
user turn. The user then explicitly asks the agent to check the inbox; the
embedded `shell3-inbox` skill supplies the bounded read-and-archive procedure.
Workflow-addressed messages remain automatic because the wrk router records
them in the destination run's durable event ledger.

Use `shell3 inbox list|read|archive` to inspect notice metadata and bodies.
These commands default to `~/.shell3/workdir/.shell3_project`, accept
`--here` or `--workdir`, and allow an advanced explicit `--state`; they do
not need `shell3.lisp` or `--config`. A notice stays pending while it is read
and moves atomically to the archive only after the complete body has been
exposed and handled. Archived main
notices remain JSON files rather than SQLite rows and have no automatic
retention cleanup. Remove them manually only when their historical value is no
longer needed.

One persistent host owns the project wake socket: Telegram or
`shell3 service`. A local console owns no wake socket and may use the same
default state concurrently. It opens its own conversation rather than attaching
to a Telegram room. Diagnostic log writes and rotation are coordinated across
these processes.

Validate before starting:

```sh
shell3 config check /path/to/shell3.lisp
shell3 wrk check /path/to/task.wrk.lisp
```

For default global operation, run bare `shell3`, `shell3 telegram`, or
`shell3 service`. Use `--here` for a directory-local instance. An explicit
`--config` requires its matching `--workdir`.
Use the host service manager for restart durability, not as a second calendar
source. Put schedules in `shell3.lisp`; each one invokes a wrkfile. Persist
either `shell3 telegram` or `shell3 service` for that project, then restart the
owner after a schedule edit. The binary does not install, enable, stop, or
replace host services itself.

Before changing the host, show and validate the exact service definition and
obtain explicit approval. Use absolute executable, config, and workdir paths.
Inject only the credential variables required by the chosen mode through a
protected host facility; never copy secret values into Lisp, argv, logs, or a
world-readable unit. A headless schedule service does not need the attached
model or Telegram secrets, though its external runner may have its own
authentication.

After installation, verify the loaded service, stop/start recovery, and single
schedule ownership before adding calendar work. Then validate and manually
fire one declaration:

```sh
shell3 config check /absolute/shell3.lisp
shell3 wrk check --config /absolute/shell3.lisp /absolute/job.wrk.lisp
shell3 schedule run --config /absolute/shell3.lisp \
  --workdir /absolute/project JOB
shell3 schedule history --workdir /absolute/project JOB
```

History is a SQLite index with `running`, `done`, and `failed` states plus the
durable run-directory and output-file pointers. Full workflow state and output
remain under `.shell3_project/wrk/`. `schedule.started`, `schedule.done`,
`schedule.failed`, and overlap-skip events are also written to the rotating
application JSONL log. A declared output must be a regular file beneath the
run's artifacts directory before the workflow can report success.

The recovery boundary is admission: a schedule run that reached the SQLite
`running` state is reconciled after process or device restart. Calendar
occurrences that happened entirely while the owner was offline are skipped;
there is no catch-up burst in version 1.

Give each experimental deployment its own kit, work directory,
Telegram bot token, and process. Persist `.shell3_project/` if conversation,
inbox, workflow, and completion recovery must survive restarts. Back up the kit
and state together, while keeping credential values in the host's secret
facility.

## Harness debugging and acceptance

Run `make acceptance` before testing a paid or authenticated harness. It builds
shell3, strictly checks the fixture kit and wrkfile, compiles and shell-validates
the plan, drives a two-attempt fake-runner loop, inspects durable status, and
checks its accepted artifact. It also manually fires the fixture schedule and
checks its SQLite history, required output, completion notice, and structured
lifecycle log. The fixture uses an isolated temporary state root and no network
or credentials, and CI runs the same target.

For a real runner such as Codex, first inspect the installed executable's
version and non-interactive help, then declare only the argv contract actually
observed. Use a disposable project and a minimal wrkfile whose single agent node
creates a harmless file in `$TASK_ARTIFACTS`; accept that exact file. Keep the
task instruction in the node `prompt`, not the agent profile. Validate each
boundary before incurring a model call:

```sh
shell3 config check /absolute/path/shell3.lisp
shell3 wrk check --config /absolute/path/shell3.lisp /absolute/path/smoke.wrk.lisp
shell3 wrk compile --config /absolute/path/shell3.lisp \
  --output /tmp/smoke-workflow.sh /absolute/path/smoke.wrk.lisp
sh -n /tmp/smoke-workflow.sh
shell3 wrk run --config /absolute/path/shell3.lisp \
  --state /tmp/shell3-smoke-state --notify-state /tmp/shell3-smoke-control \
  --run-id smoke /absolute/path/smoke.wrk.lisp 'Create only the requested probe artifact.'
shell3 wrk status --state /tmp/shell3-smoke-state smoke
```

On a runner failure, inspect that node's `prompt.md`, `stdout.log`,
`stderr.log`, `dispatch.err`, and verification log beneath the run directory.
These establish whether failure occurred in prompt assembly, argv resolution,
the harness, or acceptance. On an attached-model stream failure, start with the
latest records in `errors.jsonl`; the referenced
`runs/<session-id>/last_error.json` contains bounded request, response, and
message context for that session. Both files can contain user or model content,
so keep their owner-only permissions and do not paste them into reports without
reviewing and redacting them. Credential values are never intentional log
fields.

After the runner smoke passes, exercise `shell3 -p` in the disposable project,
then `shell3 telegram --console` for the full transport loop without Telegram
credentials. A release candidate's final networked check should use its real
service environment and cover one Telegram request plus file delivery; that
step is manual because it consumes external credentials and model capacity.

For a controlled release-candidate acceptance, keep every path absolute and
retain the disposable kit, workdir, run ids, history JSONL, and redacted log
records together. Perform these checks in order:

1. Boot a fresh standalone kit, run `shell3 config check`, and retrieve the
   embedded `wrk-scheduling` skill.
2. Exercise one console request, then persist a harmless notice with `shell3
   notify`, read it in bounded chunks, and archive it explicitly.
3. Confirm `/reload` accepts a non-schedule edit and rejects a schedule edit
   without activating any part of the edited generation.
4. Run a disposable single-agent external-harness workflow, followed by a
   bounded loop whose success is decided by an objective verifier.
5. Admit a scheduled run that waits or runs long enough to stop its owner,
   restart the same owner, and confirm the same run id reaches one terminal
   ledger state with one durable notice.
6. Exercise `telegram --console` with synthetic private input plus addressed
   and unaddressed group input. This validates transport behavior but still
   uses the configured model endpoint for actual model requests.
7. Only with explicit operator approval, inspect the selected live service's
   executable, help, environment-variable presence (never its value), and
   loaded definition; then send one real Telegram request and one harmless
   file.
8. Only with explicit operator approval, restart the selected persistent host,
   verify admitted-run recovery, and confirm the other host mode fails the
   project owner lock.

Do not treat the live steps as complete from deterministic test evidence. Save
the exact commands, exit statuses, run ids, and redacted service observations
so the promotion decision can distinguish reproducible checks from checks that
depend on the operator environment.

On shutdown shell3 stops schedule admission, cancels managed command process groups,
waits for their supervisors, and preserves running markers for honest recovery.
Use SIGINT or SIGTERM for normal service shutdown. `/superstop` is an operator
action for terminating live background commands without closing the service.
