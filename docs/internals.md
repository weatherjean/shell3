# Internals

This is the implementation contract for shell3. User-facing behavior belongs
in the README and focused guides; tests hold examples and edge cases. Keep this
file about boundaries and invariants, not implementation history.

## Architecture

### Lisp orchestrator

`internal/lispconfig` strictly parses inert
`shell3.lisp`; `internal/orchestrator` resolves its attached model, opens the
project store, and supplies a `chat.Config` directly to
`shell3.NewConfiguredRuntime`. The console exposes exactly `bash`, `bash_bg`,
and `edit_file`. Prompt, memory, and skills are embedded in the kit; skill
descriptions enter each turn while `shell3 config skill` returns one selected
body. The runtime and chat loop are reused because their session, background
process, storage, and tool-execution invariants remain the
desired kernel.

The replacement product is a harness harness. The attached main agent is its
control-plane operator: it converts intent into direct work or checked wrk
workflows, dispatches configured external harnesses, and interprets durable
results. Console and Telegram are merely local and remote adapters to that same
orchestrator. Transport-specific code may deliver input, replies, and files,
but must not own workflow semantics or create a transport-specific agent role.
The Lisp Telegram adapter reuses the configured runtime, filesystem inbox, and
workflow router used by the console. It decorates non-headless room sessions
with one host tool named `telegram`; legacy status, reload, record, and media
tools are not registered. Its command menu is limited to conversation control.

The config parser records only model and transport secret names. Runtime
assembly reads only the secret required by the active surface from the process
environment and never prints its value. External runners do not receive the
configured model or Telegram secrets through shell3's runner environment
construction.
Bare `shell3` is a plain line-oriented local front end rather than a
full-screen TUI. It runs the same orchestrator that Telegram controls remotely.
Without path flags, runtime front ends resolve
`~/.shell3/shell3.lisp` and `~/.shell3/workdir`. `--here` resolves the
current directory and its `shell3.lisp`; it is mutually exclusive with
explicit paths. Explicit runtime selection requires both `--config` and
`--workdir`.
The REPL owns only presentation state: adaptive terminal colors, an in-place
activity marker, bounded tool summaries, and Markdown rendering at assistant
message boundaries. It never owns an alternate screen or scrollback. Bash
results have a separate, stricter one-line display budget; full results still
flow back to the model and only the human transcript is elided.
Console input is accepted only between turns; Escape cancels the active child
turn context without affecting the runtime. Mid-turn steering remains a
Telegram transport behavior. The host-handled, unadvertised `/test_output`
diagnostic renders deterministic sample content through the same presentation
functions without changing session history or calling the model.
External wrk agents are leaf processes. The runner prepends that universal
contract to the wrk node prompt; agent profiles contain only a runner choice
and typed invocation parameters, not task instructions. Their environment
carries `SHELL3_WRK_WORKER=1`, and `wrk run` refuses nested launches from that
environment. This prevents an implementation node from accidentally becoming a
second orchestrator while leaving ordinary subprocesses available to the worker.

`wrk run` creates a versioned run directory containing immutable Lisp
snapshots, source hashes, request, task root, runner executable, notification
route, optional whole-run deadline and required output, and initial node
statuses. The durable Go workflow scheduler advances this state
through one-shot beats; the agent process boundary remains `wrk _agent`, and
deterministic commands and checks remain Bash. A beat holds a non-blocking run
lock, fails closed on corrupt status or changed snapshots, converts a stranded
`running` marker back to pending at-least-once work, and executes at most one
dependency wave. Independent readers may share that wave up to `parallel`; a
writer runs alone. A loop advances by one new process attempt per beat;
freshness is unconditional and has no grammar field. `wrk run`
repeats beats only until completed, failed, or quiescent on a wait node.
Foreground `wrk run` and explicit `wrk beat` invocations mirror node lifecycle,
runner stdout and runner stderr while retaining the same per-node logs. Router
beats remain quiet. With no request argument, `wrk run` reads piped standard
input but treats an interactive terminal as an empty request instead of waiting
for EOF.
External workflow events enter through the shared filesystem inbox addressed
to `wrk:<task>/<run-id>`; `wrk signal` is its typed producer. Start registers
that destination and the absolute run directory under the shared control root,
so custom workflow state remains discoverable without scanning arbitrary
paths. The sole socket consumer exposes non-main destinations as advisory
hints. A bounded workflow router advances hinted runs and reconciles the route
registry at startup and periodically; durable inbox state, not the datagram,
is authoritative. A beat recovers and claims those messages, records them in
the run event ledger, then acknowledges the inbox copies. A matching event can
release an already-waiting node or remain durable until that node's
dependencies pass. Cancellation is a durable marker watched by active beats;
observing it cancels the beat context and therefore the configured process
group. Status inspection reads atomic run and node markers without mutating
them.

An incompatible or corrupt immutable run route is terminal. The router renames
its registry record with a `.invalid` suffix and sends one structured record to
the process-wide project logger; it does not attempt a version migration.
Infrastructure diagnostics never become actionable model inbox notices.

### Schedules and persistent hosts

`internal/schedule` owns the small calendar layer. A strict top-level schedule
form contains a five-field cron expression, IANA timezone, typed wrkfile
reference, request, artifact-relative required output, whole-run timeout,
overlap policy, and notification destination. Startup resolves and parses every
referenced wrkfile before arming any entry. Schedules cannot execute arbitrary
commands directly; command work remains a workflow node.

`schedule list` enumerates resolved declarations without opening runtime state.
Run directories are execution records and are never a declaration inventory.

Exactly one process holds `.shell3_project/schedule.lock`. A live Telegram
adapter owns that lock and clock when it is the persistent frontend; otherwise
the foreground `shell3 service` command owns it without opening a model client
or consuming main inbox notices. The latter also runs the workflow router's
durable periodic reconciliation with no wake-socket ownership. Schedule edits
are restart-only in version 1; Telegram reload rejects a changed schedule set
before replacing any runtime configuration.

Each admitted fire creates a `running` SQLite schedule row before constructing
its wrk run. The row records schedule, task, trigger, run directory, required
output path, and timestamps. `overlap=skip` performs admission and the existing
running-row check in one SQLite statement; a skipped clock event enters the
application log but creates no fake run. Restart reconciliation advances every
running row from the immutable wrk snapshot, including a schedule removed from
the current kit. A short creation grace distinguishes an admission still
building its run directory from a crash between those writes. The captured task
identity is checked again at wrk startup so a concurrent task rename cannot
create a run outside the admitted ledger path.

The recovery boundary is an admitted SQLite row. The cron clock does not
backfill occurrences that elapsed while no owner was alive in version 1.

The schedule timeout becomes a durable wrk manifest deadline and therefore
includes waiting time and survives restart. A completed graph becomes failed
before notification if its required output is absent, a symlink, or not a
regular file. A terminal wrk status is the durable decision point: later beats
retry its notice but never reinterpret that status from mutable filesystem
state. Only then does the schedule ledger move to `done` or `failed`.

shell3 is a small harness around an agent turn:

```text
shell3.lisp -> orchestrator -> Runtime -> Session -> chat turn -> provider
                                  |          |
                                  |          +-> core tools, persistence
                                  +-> background commands -> filesystem inbox
*.wrk.lisp -> durable workflow scheduler -> external runner processes
schedule -> persistent clock -> durable wrk run -> required output
```

The binary owns transport, filesystem integration, process execution,
persistence, and turn lifecycle. Decisions stay in the model turn. If an agent
can build or inspect a capability itself, prefer a command, script, skill, or
declared tool over another built-in.

`shell3.Runtime` owns sessions and managed background commands.
`chat.Session` owns one conversation's messages, inbox, reminders, and turn
serialization.
Resolved agent and model identities remain structured through assembly, turns,
and stored metadata; human-readable labels are produced only at rendering
boundaries.

## Configuration

A kit is exactly one `shell3.lisp`. It is inert S-expression data, not
executable Lisp. Unknown, duplicate, misplaced, contradictory, and unresolved
forms are load errors. The root declares memory, models, the complete attached
orchestrator prompt, embedded skills, typed external runners and agent profiles,
an optional Telegram adapter, and optional schedules.

Secrets are environment-variable names resolved only from the inherited
process environment. Secret values never enter Lisp data. Skill names and
descriptions enter the prompt; bodies stay in parsed configuration until the
agent deliberately requests one through the config CLI.

The model form configures the one supported OpenAI-compatible adapter directly;
there is no provider selector. A runner's prompt input, task-root working
directory, and stdout capture are fixed protocol behavior. Agent profiles bind
only runner parameters, while wrk node prompts own task instructions. Removed
spellings remain strict parse errors rather than ignored compatibility syntax.

`shell3 boot` creates the parent directory when necessary and exclusively
writes one `shell3.lisp`; it never overwrites. No argument selects
`~/.shell3/shell3.lisp`, while `--here` selects the current directory. Its generated file has a human
header and stable section dividers. A reload parses and resolves a complete
replacement generation before updating every idle live session and the factory
for future sessions. A busy session finishes its captured generation and adopts
the replacement before its next turn. Invalid configuration leaves the current
generation untouched. Telegram token variable or home-chat changes require
adapter restart because they define the active transport connection.

## Turns and history

One `shell3.Session` permits one active turn. `Send`, queued wake turns,
compaction, close, and cancellation share the same lifecycle so persistence and
teardown cannot race. Events stream synchronously from `chat.Session`; closing
the event channel is the authoritative end-of-turn signal.

The system prompt is rendered per turn. Context files, room briefs, reminders,
available tools, and active configuration can therefore refresh between turns.
Messages and tool calls are persisted in provider-valid order. Tool results
always retain their matching call, including cancellation and error paths.
Telegram direct replies preserve every non-empty assistant text segment across
tool rounds in order; a late tool call must not hide an answer already emitted.
Quiet wake turns expose only their final segment so background narration stays
silent.

Prompt usage is provider-reported when available and estimated otherwise.
Pruning removes old tool output before `prune_at`; compaction summarizes the
head and keeps a verbatim tail before `compact_at`. A successful compaction
rolls to a new stored session while preserving front-end attribution and the
current-session marker. Errors leave the original history intact.

For Lisp-configured sessions, a declared context window derives these internal
thresholds: compaction at 80% of the window, pruning at 60% of that threshold,
and the standard derived verbatim tail. They are policy, not separate public
configuration fields.

Each session owns its provider client so concurrent rooms cannot overwrite one
another's diagnostic traffic capture. A failed stream retains bounded request,
response, message, and tool-call context in that session's
`runs/<session-id>/last_error.json`; the project log records the error and dump
path. Successful and partial response bytes are captured as the stream is read.

Filesystem inbox notices are durable untrusted input, not authorization.
`main` is deliberately passive: an arrival never starts a model turn and
neither its metadata nor body enters a prompt automatically. The console
renders only the pending count at startup and before each ordinary user turn.
Telegram posts a host-owned `✉️` count and a bounded preview of the latest
notice to the home chat. The user explicitly asks the agent to load the inbox
skill before any notice is inspected.

The filesystem inbox uses `new`, `processing`, `archived`, and `reads`
directories per encoded destination. Workflow events are atomically claimed
into `processing`, recorded in their run event ledger, and acknowledged;
startup and periodic route reconciliation recover missed hints and abandoned
workflow claims. Main notices normally stay in `new` while bounded reads
durably advance a contiguous byte offset. Skipping content is rejected. The
agent explicitly archives a notice or prevalidated batch only after every
named notice is fully read; the per-notice archive is an atomic rename. Full
bodies remain JSON files and are not copied into the runs database.

`shell3 inbox list|read|archive` requires no model configuration. List output
defaults to ten rows and is capped at 100; read output defaults to 8 KiB and is
capped at 32 KiB with byte offsets constrained to UTF-8 boundaries. Notice
bodies remain untrusted machine-origin data even when fetched deliberately.

The persistent Telegram or headless service host owns the exclusive advisory
wake socket. Its listener never claims messages and fans non-main hints to the
workflow router. Telegram additionally turns a `main` hint into a direct
human notification; the headless service ignores it. The listener is a latency
optimization only. A local console owns no socket, can coexist against the
same state, and relies on the router's durable periodic reconciliation.

## Tools and policy

The core model-facing tools are `bash`, `bash_bg`, and `edit_file`. Telegram
adds one file-send tool named `telegram`. There is no MCP or custom-tool
registry in the orchestrator. Reusable capability belongs in shell commands,
scripts, or skills; multi-agent decisions belong in wrkfiles.

Foreground command cancellation and background shutdown terminate process
groups and bound inherited-pipe shutdown. The shipped harness is not an
operating-system sandbox.

## Runtime and background work

Background commands are in-process jobs. Admission, concurrency, and the
positive `WaitGroup.Add` happen under the job-manager lock before work is
published; shutdown closes admission before waiting. External agent work runs
through the durable wrk scheduler, and dispatched agents are leaf processes.

Every command completion is represented only by a `main` filesystem-inbox notice,
`bash_bg.completed` or `bash_bg.failed`. Completion never wakes a model turn and
never posts directly through a front end. The notice contains bounded command
and output context plus the full job-log path when available.

A typed SQLite `background_jobs` row marks each command while its owning
process may still be running. Normal completion persists the filesystem notice
before deleting that marker. Restart recovery converts a dead-process marker
to a failure notice before deleting it, so delivery is intentionally
at-least-once. `/superstop` suppresses its manufactured notice because the host
already answers the user; graceful shutdown leaves the marker for honest
restart recovery. Background work retains the configuration generation
captured when it was dispatched. A validated reload updates idle and future
sessions without changing already-running work.

Graceful shutdown, ordinary cancellation, `/stop`, and `/superstop` have
different reporting rules. Manufactured shutdown cancellations must not become
misleading failure posts.

## Storage

`internal/runs` owns the SQLite store for sessions, messages, usage, search,
thread markers, running command markers, and the schedule-run index. It has one
current base schema and no migration or compatibility reader. A version
mismatch deletes only `shell3.db`, `shell3.db-wal`, and `shell3.db-shm`, then
creates the current schema; no backup copy is created. Corrupt or unreadable
files still fail closed because shell3 cannot establish that they are merely
stale. The store and filesystem artifacts form one durability contract:

- session rows and messages preserve conversation history;
- thread markers map a front-end surface to its current session;
- `background_jobs` rows make command loss across a process crash observable;
- filesystem inbox notices preserve completed command and workflow delivery;
- job logs preserve bounded background output;
- schedule rows preserve running/done/failed status and pointers to the
  authoritative wrk run directory and required output;

Message and chat IDs are opaque strings across storage and transports.

## Diagnostics

Every attached runtime opens one private JSONL logger at
`.shell3_project/errors.jsonl` and shares it with turns, transports, and the wrk
router. Headless schedule service mode opens the same logger. Schedule start,
completion, failure, overlap skip, and recovery failures are structured records
there rather than model notices. It rotates during operation at 10 MiB, retaining five archives; the
active file plus archives are therefore bounded to approximately 60 MiB.
Writers sharing a workdir coordinate each append and rotation with a
cross-process lock and reopen the active path after another process rotates it.
The log and per-session provider traces use owner-only permissions. Diagnostic
failures never become model inbox input, and credentials must never be added as
fields.

## Control surfaces: Telegram and console

In the replacement architecture Telegram is an optional remote-control adapter
to the same orchestrator used by the local console. Each chat owns one long-lived
`conversation`; replies add context but do not select another session. `/new`
detaches the current conversation, `/stop` cancels its turn, `/superstop` also
cancels background work. The replacement menu exposes those conversation
controls plus `/ask`, `/help`, and `/reload`; legacy kit, status, aside,
and quiet commands are not advertised. Host commands never spend a model turn.

`Bot.mu` guards the room registry and process-wide wiring;
`conversation.mu` guards room state. When both are needed, lock the room first.
Turns run concurrently across rooms under a global cap and serialize within a
room. Busy-room messages queue; text may steer the active turn. Releasing any
global slot must rescan all rooms, because a queued room has no other wakeup.

Each room persists its marker under `<transport>:<chat-id>`. Groups require an
addressed message by default; `(group-messages all)` bypasses that
trigger only after sender authorization. Group-to-supergroup
migration moves the same conversation, swaps identity under the room-then-bot
lock order, persists the new marker, clears the old one, and invalidates cached
metadata. Room descriptions are untrusted contextual text, capped and refreshed
without blocking normal turns.

Completions return to the room owning their session; orphans fall back to the
home chat. Posted turns create their best-effort progress
bubble at turn start and add tool calls as they occur; quiet wake turns create
none. Long replies become
a short message plus a self-contained HTML document. Attachment paths are
durable media files; perception remains a declared tool decision.

Provider prompt usage drives silent Telegram context milestones at 50% and
75% once per growth cycle. The compaction event posts the new fullness and
re-baselines the cycle; `/new` clears it. These notices are host-rendered and
never become model input.

The real Telegram adapter posts `๑ï shell3 started` to the home chat after it
has initialized and `๑ï shell3 shutting down` during a graceful exit. These
are host-rendered lifecycle notices and do not create or resume an agent turn.
The console transport does not emit them.

`telegram --console` uses the same bot loop over stdin/stdout with a synthetic
local room. It needs no Telegram credentials or `allow_from` authorization and
is the end-to-end development transport. EOF shuts it down cleanly.

## Media and rendering

`mediadir` provides durable attachment paths. Telegram media sending opens
only regular files, refuses credential
and config-tree aliases (including hardlinks), and bounds reads. Media
perception and generation are bring-your-own declared tools.

Renderers must escape raw input before emitting Telegram or document HTML.
Telegram supports only its documented tag subset; a rejected formatted
send falls back to plain text.

## Package map

```text
cmd/shell3/              command tree and front-end wiring
internal/adapter/openai/ OpenAI-compatible provider adapter
internal/applog/         shared rotating project logger
internal/bootstrap/      runtime-directory initialization
internal/chat/           turns, core tools, events, compaction
internal/cli/            one-shot output helpers
internal/edittool/       direct-disk edit_file implementation
internal/inbox/          durable filesystem inbox and wake socket
internal/lispconfig/     strict shell3.lisp parser and resolver
internal/llm/            provider interfaces and message types
internal/mdpage/         standalone HTML reply rendering
internal/mediadir/       durable attachment paths
internal/orchestrator/   attached-model assembly and core schemas
internal/scaffold/       complete single-file starter kit
internal/paths/          project runtime and sensitive-path resolution
internal/runner/         typed external runner process boundary
internal/runs/           SQLite history and running markers
internal/schedule/       Cron clock, ownership, wrk dispatch, recovery
internal/sexpr/          inert S-expression reader
internal/shell3/         Runtime, Session, background jobs, inbox persistence
internal/strutil/        shared text safety helpers
internal/telegram/       bot loop, transports, rooms, host tools
internal/wrk/            workflow parser, compiler, scheduler, state
```

## Development

```bash
make build
make lint
go test ./...
go test -race ./...
make coverage
make deepcheck
```

Use focused success and failure tests while changing a subsystem, then broaden
verification in proportion to risk. Parsing, storage, concurrency, and
delivery changes require regressions for the rejected/error path as
well as success.

Do not commit build output, runtime state, secrets, `.shell3_project/`, or local
working artifacts. Plans and review ledgers belong under ignored `docs/dev/`;
shipped behavior belongs in the focused public guides.
