# Internals

This is the implementation contract for shell3. User-facing behavior belongs
in the README and focused guides; tests hold examples and edge cases. Keep this
file about boundaries and invariants, not implementation history.

## Architecture

shell3 is a small harness around an agent turn:

```text
kit + wiring -> Parts -> Runtime -> Session -> chat turn -> provider
                         |          |
                         |          +-> tools, gates, persistence
                         +-> jobs, cron, completion routing, front ends
```

The binary owns transport, filesystem integration, process execution,
persistence, and turn lifecycle. Decisions stay in the model turn. If an agent
can build or inspect a capability itself, prefer a command, script, skill, or
declared tool over another built-in.

`agentsetup.Parts` is one loaded generation: parsed configuration, providers,
MCP clients, store, hooks, and agent factories. `shell3.Runtime` owns the live
generation, sessions, jobs, and reload lifecycle. `chat.Session` owns one
conversation's messages, inbox, reminders, and turn serialization.

## Kit and configuration

A config directory contains exactly one `shell3.sh`. There is no fallback
format or migration shim. Its top level is definitions-only: declaration
comments, function definitions, and literal heredocs. Sourcing it must not run
work. The parser therefore rejects top-level statements, ambiguous declaration
kinds, malformed structure, and commands hidden after a function close.

Declaration scopes are positional:

- The first `agent:` is main; later agents are employees and need delegation
  descriptions.
- `tool:` and `test:` belong to the preceding `agent:` or `shared:` block.
- `gate:`, `note:`, and `event:` name their target agents.
- `command:` and `cron:` are global. A cron block names its target agent.
- `shell3:` contains strict YAML wiring for models, Telegram, MCP, storage,
  review, and runtime limits.

Unknown, removed, misplaced, duplicated, negative, or contradictory fields are
load errors. Every agent capability resolves during parsing. MCP opt-ins use
the agent's `mcp:` field; old spellings fail with a directed replacement.

Secrets are `env:KEY` references resolved from the config directory's `.env`.
Never print or inspect credential contents. Tools inherit only the small
environment allowlist unless their declaration explicitly maps more values.

Relative agent workdirs resolve from the config directory. Context files
resolve from the active agent's workdir, refresh every turn, and are capped at
64 KB with middle elision. Skills are Markdown files under `skills/` or an
employee's project skill directory; only their index enters the prompt, and the
agent reads a selected file with ordinary shell tools.

## Turns and history

One `shell3.Session` permits one active turn. `Send`, queued wake turns,
compaction, close, and cancellation share the same lifecycle so persistence and
teardown cannot race. Events stream synchronously from `chat.Session`; closing
the event channel is the authoritative end-of-turn signal.

The system prompt is rendered per turn. Context files, room briefs, reminders,
available tools, and active configuration can therefore refresh between turns.
Messages and tool calls are persisted in provider-valid order. Tool results
always retain their matching call, including cancellation and error paths.

Prompt usage is provider-reported when available and estimated otherwise.
Pruning removes old tool output before `prune_at`; compaction summarizes the
head and keeps a verbatim tail before `compact_at`. A successful compaction
rolls to a new stored session while preserving front-end attribution and the
current-session marker. Errors leave the original history intact.

Inbox notices are durable conversation input, not authorization. User steering
may enter an active turn at a round boundary; completion notices queue and can
wake an idle owner.

## Tools and policy

The core model-facing tools are `bash`, `bash_bg`, and `edit_file`. Optional
built-ins cover history and task management. Custom kit tools are Bash
functions with a strict object schema. Their arguments become validated
environment variables; stdout is the result. Cancellation sends `SIGTERM` to
the invocation's process group and bounds inherited-pipe shutdown.

MCP servers connect as part of a Parts generation. Their tools are namespaced
`mcp_<server>_<tool>`, filtered by each agent's opt-in, validated as JSON
objects, and retried once after reconnect. A failed MCP call returns a tool
error rather than killing the turn.

Gates run before every tool for explicitly named agents. There is no fallback
gate and no ask verdict. Hook input and output are strict JSON. Hook failure,
timeout, malformed output, unknown fields, and invalid rewrites fail closed.
Verdict precedence is block, review, argv, command. Rewrites apply only to Bash
tools. Notes may rewrite output but never refuse a call; events only observe.

Contextual review trusts only ephemeral human-origin content captured in an
interactive root session. Stored history, generated user carriers, subagent
prompts, cron input, and tool output are not approval. Critical risk denies;
high risk requires approval of the exact action and side effects. The public
verdict remains `{"review":true}`.

The shipped gate is a speed bump, not isolation. It must distinguish a hard
block from a routed refusal that names the permitted method. Operating-system
sandboxing is the security boundary.

## Runtime, jobs, and reload

Subagents and background commands are in-process jobs. Admission, concurrency,
and the positive `WaitGroup.Add` happen under the job-manager lock before work
is published; shutdown closes admission before waiting. Dispatch depth is at
most two levels and is checked when a task starts, not by hiding tools.

A subagent can outlive its first turn while its background commands finish.
Each completion may resume it for a bounded follow-up. Results climb through
the parent that requested them. `report:` is the sole reporting axis:

- `auto` lets the owner decide whether a user-facing update is warranted.
- `always` requires an update, with a raw fallback if the owner says nothing.
- `raw` posts the result without another model turn.

`NO_REPLY`, failures, owner mail, cron attribution, and report traces must stay
consistent across command and subagent completion paths.

Every completion is persisted to the outbox before routing and deleted only
after successful handoff. Delivery is intentionally at-least-once. Running
markers make work lost to a process crash observable at restart. Event rows,
markers, job logs, and transcript reads use the store generation belonging to
the session/job that created them.

Reload builds and validates a complete new Parts before swapping. Idle root
sessions move to it in place. Active child sessions and jobs retain their old
generation until they drain, so old cleanup is parked rather than closing live
handles. A busy front-end turn blocks reload; background work does not. Cron
shutdown closes fire admission and waits for scheduled and manual dispatch
calls before its store generation closes.

Graceful shutdown, ordinary cancellation, `/stop`, and `/superstop` have
different reporting rules. Manufactured shutdown cancellations must not become
misleading failure posts.

## Storage

`internal/runs` owns the SQLite store for sessions, messages, usage, search,
thread markers, cron status, and the outbox. Schema mismatch is explicit; never
silently discard history or completions. An incompatible database bundle is
moved aside and retained before a fresh schema is created; shell3 does not
migrate it or read through it. The store and filesystem artifacts form one
durability contract:

- session rows and messages preserve conversation history;
- thread markers map a front-end surface to its current session;
- outbox rows preserve running work and undelivered completions;
- job logs preserve bounded background output;
- janitors remove only data outside configured retention and reference rules.

Message and chat IDs are opaque strings across storage and transports. SQLite
operations that select the Runtime's current store hold the Runtime lock through
the short database call so reload cannot close the chosen handle.

## Telegram and console

Telegram is the primary front end. Each chat owns one long-lived
`conversation`; replies add context but do not select another session. `/new`
detaches the current conversation, `/stop` cancels its turn, `/superstop` also
cancels background work, and `/reload` applies a new generation. Host commands
never spend a model turn.

`Bot.mu` guards the room registry and process-wide wiring;
`conversation.mu` guards room state. When both are needed, lock the room first.
Turns run concurrently across rooms under a global cap and serialize within a
room. Busy-room messages queue; text may steer the active turn. Releasing any
global slot must rescan all rooms, because a queued room has no other wakeup.

Each room persists its marker under `<transport>:<chat-id>`. Group-to-supergroup
migration moves the same conversation, swaps identity under the room-then-bot
lock order, persists the new marker, clears the old one, and invalidates cached
metadata. Room descriptions are untrusted contextual text, capped and refreshed
without blocking normal turns.

Completions return to the room owning their session; orphans and cron output
fall back to the home chat. Progress edits are best-effort. Long replies become
a short message plus a self-contained HTML document. Attachment paths are
durable media files; perception remains a declared tool decision.

`telegram --console` uses the same bot loop over stdin/stdout with a synthetic
local room. It needs no Telegram credentials or `allow_from` authorization and
is the end-to-end development transport. EOF shuts it down cleanly.

## Status, media, and rendering

`/status` reads concurrency-safe runtime snapshots and sends one self-contained
HTML document without a model turn. Stored conversations and job logs are
rendered only after the agent selects an opaque, validated id and explicitly
sends the resulting document through the attached Telegram room.

`mediadir` provides durable attachment/generated-file paths and an age-based
janitor. Telegram media sending opens only regular files, refuses credential
and config-tree aliases (including hardlinks), and bounds reads. Media
perception and generation are bring-your-own declared tools.

Renderers must escape raw input before emitting Telegram or document HTML.
Telegram supports only its documented tag subset; a rejected formatted
send falls back to plain text.

## Package map

```text
cmd/shell3/              command tree and front-end wiring
internal/agentsetup/     Parts and per-session configuration
internal/adapter/openai/ OpenAI-compatible provider adapter
internal/applog/         rotating application log
internal/askui/          interactive terminal UI
internal/bootstrap/      runtime-directory initialization
internal/chat/           turns, tools, gates, events, compaction
internal/cli/            one-shot output helpers
internal/config/         wiring and hook execution
internal/cron/           scheduler and run status
internal/edittool/       direct-disk edit_file implementation
internal/kit/            parser, declarations, runner, test harness
internal/llm/            provider interfaces and message types
internal/mcp/            MCP connections and dispatch
internal/mdpage/         standalone HTML reply rendering
internal/mediadir/       media paths and cleanup
internal/modelproxy/     local provider proxy lifecycle
internal/notify/         completion notification types
internal/paths/          global and local path resolution
internal/persona/        prompt/tool/parameter carrier
internal/render/         status and stored-record HTML renderers
internal/review/         contextual gate reviewer
internal/runs/           SQLite history, outbox, markers, janitors
internal/scaffold/       embedded starter kit, skills, scripts
internal/shell3/         Runtime, Session, jobs, reload, routing
internal/strutil/        shared text safety helpers
internal/telegram/       bot loop, transports, rooms, host tools
```

## Development

```bash
make build
make lint
go test ./...
go test -race ./...
make deepcheck
```

Use focused success and failure tests while changing a subsystem, then broaden
verification in proportion to risk. Parsing, policy, storage, concurrency,
reload, and delivery changes require regressions for the rejected/error path as
well as success.

Do not commit build output, runtime state, secrets, `.shell3_project/`, or local
working artifacts. Plans and review ledgers belong under ignored `docs/dev/`;
shipped behavior belongs in the focused public guides.
