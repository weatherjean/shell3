# shell3 wrk

shell3 is a minimal Unix-composable harness harness. Its main agent turns a
human intention into inspectable work, dispatches suitable agent harnesses,
observes their results, and keeps the human informed. The binary owns only the
parts an agent cannot reliably provide for itself: the turn, process and
filesystem integration, durable delivery, transport attachment, and strict
workflow execution.

shell3 does not preserve the old configuration language or carry compatibility
shims. Proven code belongs here when it protects a named invariant; the old
architecture is not itself an invariant. Promotion from an older installation
is an explicit replacement of configuration and runtime state, not an in-place
migration.

## Product shape

shell3 is primarily a harness harness: a small control plane for expressing,
dispatching, observing, and resuming work performed by other agent harnesses.
Its attached main agent is the operator of that control plane. It translates a
human outcome into direct local work or a checked wrk workflow, selects named
external harness profiles, and interprets their durable results. It can still
answer small questions, research unfamiliar subjects, and do ordinary local
work directly, but those abilities serve orchestration rather than turning
shell3 into a general-purpose assistant with an ever-growing tool surface.

The complete model-facing tool surface is:

```text
Console:   bash, bash_bg, edit_file
Telegram:  bash, bash_bg, edit_file, telegram
```

`telegram` sends a local file to the chat attached to the current main
session. Text leaves as the normal assistant reply. Headless workers never
receive the tool; they publish results and artifacts through notifications to
their owner. Configuration inspection, notice handling, and workflow control
are ordinary `shell3` commands invoked through Bash. Reload is a host-handled
console and Telegram command.

The console is the local control surface. Telegram is an optional remote
control adapter for the same orchestrator, not shell3's identity or the owner
of workflow behavior. Both drive the same session, turn, inbox, and workflow
contracts; adding another transport must not create another kind of agent.

Telegram configuration names a token secret, home chat, operator allowlist,
room turn limit, and group trigger policy. The adapter retains one conversation
per chat, host-side conversation controls, durable completion delivery, and
safe file sending. It adds exactly one model-facing tool, `telegram`, whose
only job is sending a local file to the attached chat; ordinary text uses the
assistant reply.

## Configuration and wrkfiles

One `shell3.lisp` is the root configuration. Files named `*.wrk.lisp` describe
workflows. Both are inert S-expression data parsed by the same strict reader.
They are not executable Lisp: there is no evaluator, mutation, recursion, or
macro system. Immutable constants and literal includes may be supported when
their resolution remains finite, deterministic, and cycle-checked.

The reader supports lists, symbols, strings, numbers, comments, and a small
literal multiline form suitable for prompts and shell bodies. Parsing retains
source positions. Configuration and wrk schemas reject unknown, duplicate,
misplaced, contradictory, and unresolved forms.

The root configuration defines:

- constants and secret references;
- the OpenAI-compatible endpoint for the attached main turn;
- external harness runners;
- named agent profiles specializing those runners;
- control-surface settings, including optional Telegram transport settings;
- named schedules that invoke checked wrkfiles;

Secret forms name inherited process-environment variables; values never live in
Lisp data. The operator supplies them through an interactive shell, service
manager, or secret manager. They are resolved only at point of use and model
credentials are removed from external runner environments.

A runner is a typed process protocol, not a shell command template. It declares
an argv vector, runtime argument slots, stderr handling, result extraction,
successful exit behavior, cancellation, and timeout. shell3 sends the node
prompt on standard input, runs the process in the task root, and always retains
stdout. Values are passed as distinct argv entries; structural configuration
never goes through `eval` or shell string interpolation. Complex adapters
remain normal executable scripts.

Dispatched agents are leaf workers: they perform their assigned node directly
and do not recursively launch another wrk workflow. The attached main agent
owns orchestration; the workflow owns dispatch and external verification. An
agent profile selects a runner and its parameters. Task-specific instructions
belong in the wrk node prompt.

A wrkfile resolves its `(using agent-name)` references through `shell3.lisp`.
It may declare one-shot agent nodes, Ralph-style fresh-agent loops,
deterministic commands, external waits, dependencies, verification, artifacts,
budgets, and concurrency. Independent runnable nodes may run concurrently.
Readers may share a wave up to the task's parallel limit; a writer runs alone.

The workflow vocabulary remains small:

```text
task agent loop command wait using after access max timeout
prompt run until accept on
```

The task definition is immutable during a run. Durable state, attempts,
events, logs, inbox messages, and artifacts live under a run directory. A run
records the hashes of its resolved configuration and wrkfile. Changed input
does not silently alter an active run.

## Ralph loop

A `loop` repeatedly starts a new configured agent process with the original
request, the node instructions, relevant durable artifacts, attempt metadata,
and the latest verifier result. Freshness is inherent rather than a
configurable field. After every turn, an external verifier decides whether the
node passed. The loop stops on verifier success, an explicit terminal failure,
cancellation, or a declared budget. An agent's claim that work is complete is
evidence, never the authoritative success condition when an external check is
available.

## Inbox, notify, and beat

`shell3 notify` is the single asynchronous ingress. It constructs a typed
message, persists it durably to a session or workflow inbox, commits that
write, and only then attempts a best-effort immediate wake of a live process.
Success means durable acceptance; wake delivery is reported separately. A
missing daemon never turns an accepted message into loss.

The persistent Telegram or headless host owns the advisory wake socket. A
`main` message remains passive durable state: its arrival may print or post a
human-facing pending count, but it never starts an orchestrator turn or enters
a prompt. The user explicitly asks the agent to use the inbox skill, read each
body in bounded chunks, handle it, and archive it. A local console may coexist
with Telegram because neither claims main notices automatically.

Notification bodies are information, not authorization. Every envelope has a
unique identifier, destination, origin classification, event kind, creation
time, optional correlation identifier, and body. Workflow consumers claim and
acknowledge messages durably. Main notices move atomically to the archive only
after explicit, complete handling. Processing is at-least-once.

The live process may accept wake hints over a local Unix socket. The hint names
the destination but carries no authoritative content; the receiver rereads the
durable inbox. The process resolves workflow destinations through a durable
route registry, drives each awakened run until it is terminal or waiting, and
periodically reconciles that registry so a dropped hint or restart cannot
strand accepted work. Independent runs are bounded and concurrent. A persisted
Telegram adapter or headless `shell3 service` owns the periodic router and
declared calendar clock; one project never has both schedule owners. One beat claims input,
reconciles running work, verifies completed nodes, dispatches newly runnable
nodes up to the concurrency limit, records transitions, and exits when
quiescent.

Starting a run snapshots the validated `shell3.lisp` and wrkfile, their hashes,
the resolved task root, original request, runner executable, and notification
route. A beat reads only that immutable snapshot. It recovers an interrupted
`running` marker as at-least-once work, advances one dependency wave (or one
fresh loop attempt), and writes node and run status atomically. `wrk run` is the
convenience driver that invokes successive beats until the run is terminal or
waiting; automation can invoke `wrk beat` directly instead.

`wrk signal` is a typed convenience over `notify`, not another ingress path. It
persists an event to the `wrk:<task>/<run-id>` inbox and attempts the same
best-effort wake. A live attached process advances it immediately; otherwise
the next beat claims those messages into the immutable run's event ledger,
acknowledges them only after that write, and releases every reachable wait node
naming the event. Signals may arrive before a wait becomes reachable.
`wrk status` renders the durable run and node state as JSON. `wrk cancel`
persists a terminal marker; an active beat observes it and cancels its child
process group, while later beats remain idempotently cancelled.

## Scheduling and service lifetime

A schedule is inert `shell3.lisp` data: a cron expression, explicit IANA
timezone, wrkfile reference, request, required artifact-relative output,
whole-run timeout, overlap policy, and notification destination. It cannot name
an agent or arbitrary shell body. The referenced wrkfile remains the complete
execution contract and is parsed before any schedule is armed.

`shell3 service` is the foreground headless host intended for persistence by
launchd, systemd, or an equivalent service manager when Telegram is not kept
alive. Telegram can instead own the same clock. A project lock fails closed on
two owners. Host installation and credential injection remain explicit
operator actions rather than cross-platform mutation built into the binary.
Admitted work recovers after restart; version 1 does not replay calendar
occurrences missed while every owner was offline.

Every actual fire is a normal durable wrk run. SQLite indexes its schedule,
task, trigger, timestamps, `running|done|failed` status, run directory, required
output path, and bounded failure. Full state, logs, and artifacts remain in the
run directory. Structured lifecycle events enter the rotating JSONL application
log. Timeouts and required-output validation occur before completion
notification; overlap skips are audit events, not failed runs.

## Kernel responsibilities

The target kernel owns:

- provider streaming and valid tool-call ordering for the main turn;
- serialized turns, steering, cancellation, and bounded context maintenance;
- synchronous and background Bash with process-group lifecycle management;
- reliable exact file editing;
- strict S-expression parsing, configuration resolution, and wrk validation;
- external runner invocation and normalized results;
- durable workflow state, inbox notices, running markers, logs, and restart reconciliation;
- exclusive calendar ownership and a durable scheduled-run ledger;
- local and remote control-surface attachment to the same turn contract;
- one safe Telegram file-delivery tool;
- transport authorization and bounded file-delivery checks.

It does not own MCP, provider-specific workflow logic, host service installation,
perception or generation choices, a general plugin system, or a growing set of
model-facing verbs. Those remain commands, scripts, runner adapters, skills,
or wrkfiles.

## Command surface

The intended operator and agent vocabulary is:

```text
shell3                         open the local orchestrator console
shell3 telegram               attach Telegram remote control
shell3 service                run the persistent headless schedule host
shell3 schedule run NAME      manually fire a declared schedule
shell3 schedule history       inspect scheduled-run history as JSONL
shell3 config check           parse and validate shell3.lisp
shell3 notify                 persist a message and attempt a wake
shell3 wrk check FILE          parse and validate a wrkfile
shell3 wrk compile FILE        render its fully resolved executable plan
shell3 wrk run FILE            start a durable run
shell3 wrk beat RUN            advance a run once
shell3 wrk status RUN          inspect state and results
shell3 wrk signal RUN EVENT    deliver an external event
shell3 wrk cancel RUN          cancel a run and its active processes
```

Every essential behavior must be exercisable without Telegram. CLI-level and
console tests must be able to use fake providers and fake harness executables,
drive notifications and beats, interrupt and restart processes, and inspect
all durable state. Telegram tests cover only the transport-specific boundary.

## Build discipline

shell3 has one configuration format, one workflow engine, one notification
path, and the minimal tool surface above. No removed syntax, database,
declaration, tool, or scheduler receives a fallback.

Implementation proceeds through end-to-end vertical slices. The decisive
release proof is a fresh process with its own configuration, state, and
Telegram bot that accepts a request, writes and validates a wrkfile, dispatches
multiple runner types, executes independent nodes concurrently, iterates a
fresh-agent loop until an external verifier passes, survives a restart without
losing accepted work, wakes its owner through `notify`, and delivers an
artifact through `telegram`.
