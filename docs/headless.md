# Headless mode

shell3 runs non-interactively for pipelines, scripts, and agent-to-agent
orchestration. In headless mode there is no TUI: the model's output streams to
stdout, and an optional structured **JSONL audit log** captures every event for
machine consumption.

This document covers how headless mode is triggered, what lands on
stdout/stderr, the full JSONL event schema, the environment variables involved,
and the pattern for spawning subagents.

## Triggering headless mode

A run is headless when **either** of these is true
([`cmd/shell3/run.go`](../cmd/shell3/run.go)):

1. The `--out <path>` flag is passed, or
2. stdin is **not** a TTY **and** an initial message is supplied (i.e. the
   message is piped or passed as an argument in a non-interactive shell).

The message can come from arguments or stdin:

```sh
# Argument form
shell3 "summarize the staged diff" --out run.jsonl

# Stdin form (stdin is not a TTY → headless)
git diff --staged | shell3 "summarize this diff"

# Audit log without arguments still requires a message; with a TTY you can
# combine --out with an argument:
shell3 "list the open TODOs" --out /tmp/todos.jsonl
```

When headless is active, shell3 exports two environment variables to the
process (and therefore to any tools/hooks it spawns) — see
[Environment variables](#environment-variables).

In headless mode the interactive shell tool is disabled: any tool that would
block on a TTY receives `error: interactive TTY not available in headless mode`.

## stdout, stderr, and exit code

Plain text streams to stdout/stderr regardless of whether `--out` is set
([`internal/tui/once.go`](../internal/tui/once.go)):

| Stream | Content |
| ------ | ------- |
| **stdout** | Assistant token deltas (streamed verbatim), and each tool result body (trimmed, surrounded by blank lines). A trailing blank line is printed when a turn completes. |
| **stderr** | `retry: <summary>` on transient request retries; `error: <message>` on non-fatal errors. |

Exit code: **0** on success, **1** if the turn ended with an error (an
`error` event was observed). The final JSONL `end` line's `status` mirrors this
(`"ok"` or `"error"`).

stdout is intended to be plain and pipeline-friendly. For structured,
parseable output, use the JSONL audit log via `--out`.

## The JSONL audit log (`--out`)

`--out <path>` streams one JSON object per line to the given file (truncated and
rewritten each run). Two layers of records appear:

1. An **envelope** — a `start` line first and an `end` line last
   ([`internal/chat/outsink.go`](../internal/chat/outsink.go)).
2. **Session events** in between — one line per chat event
   ([`internal/chat/event.go`](../internal/chat/event.go)).

Every line has a `ts` (RFC 3339 / `RFC3339Nano`, UTC) and a `kind`. All other
fields are omitted when empty, so consumers should treat missing fields as
absent rather than null.

### Envelope: `start` and `end`

The first and last lines of the stream:

```jsonc
// first line
{"ts":"2026-06-03T12:00:00.123456Z","kind":"start","input":"summarize the diff","persona":"default","model":"gpt-4o","out":"run.jsonl","headless":true}
// last line
{"ts":"2026-06-03T12:00:07.654321Z","kind":"end","status":"ok"}
```

| Field | Type | On | Meaning |
| ----- | ---- | -- | ------- |
| `ts` | string | both | RFC3339Nano UTC timestamp |
| `kind` | string | both | `"start"` or `"end"` |
| `input` | string | start | The user message for this run |
| `persona` | string | start | Agent / mode label |
| `model` | string | start | Model id |
| `out` | string | start | The `--out` path |
| `headless` | bool | start | Whether headless mode is active |
| `status` | string | end | `"ok"` or `"error"` |

### Session events

Between the envelope lines, each chat event is serialized with this shape. The
`kind` discriminates which optional fields are present.

| Field | Type | Present on | Meaning |
| ----- | ---- | ---------- | ------- |
| `ts` | string | all | RFC3339Nano UTC timestamp |
| `kind` | string | all | Event kind (table below) |
| `session_id` | number | when a store is configured (non-zero) | Persistent session id |
| `text` | string | token / message / reasoning / error / system_reminder / retry | Payload text |
| `role` | string | message events | `"user"` or `"assistant"` |
| `tool` | string | tool_call / tool_result | Tool name |
| `input` | string | tool_call | Raw JSON arguments |
| `output` | string | tool_result | Tool return string |
| `call_id` | string | tool_call / tool_result | Sequential id linking a call to its result |
| `tool_error` | bool | tool_result (only when true) | Result represents an error |
| `usage` | object | usage / turn_done | `{"prompt":int,"completion":int,"total":int}` |
| `meta` | object | session_start / session_end | Small string key/values, e.g. `{"status":"ok"}` |

#### Event kinds

In emission order over a typical turn:

| `kind` | When it fires | Key fields |
| ------ | ------------- | ---------- |
| `session_start` | Once, before any turn | `meta` |
| `user_message` | User input submitted | `role:"user"`, `text` |
| `assistant_reasoning` | Reasoning/thinking deltas (providers that surface them) | `text` |
| `assistant_token` | Each streamed assistant token (high volume) | `text` |
| `tool_call` | Assistant invokes a tool | `tool`, `input`, `call_id` |
| `tool_result` | A tool returns | `tool`, `output`, `call_id`, `tool_error?` |
| `usage` | Provider reports per-stream token usage | `usage` |
| `assistant_message` | Once per turn after streaming completes | `role:"assistant"`, `text` |
| `system_reminder` | A `<system-reminder>` is injected (model change, context-usage threshold) | `text` |
| `retry` | A transient request failure is about to be retried | `text` |
| `error` | A non-fatal error (stream failure, hook denial, …) | `text` |
| `turn_done` | A full user→assistant turn (incl. tool roundtrips) completes | `usage` (cumulative) |
| `session_end` | Once at teardown | `meta.status` |

Notes:

- `assistant_token` is the streaming delta; `assistant_message` is the full
  assembled message emitted once at the end of the turn. Consume one or the
  other depending on whether you want streaming or final text.
- `usage` reports per-stream counts; `turn_done` carries the cumulative totals
  for the whole turn including tool roundtrips.
- Event delivery is best-effort and non-blocking — under extreme backpressure
  an event may be dropped rather than block the turn loop. The JSONL sink writes
  synchronously per event, so the file is the authoritative record.

### Reading the log

```sh
# Final status of a run
tail -n1 run.jsonl | jq -r '.status'

# Just the assistant's final answer
jq -r 'select(.kind=="assistant_message") | .text' run.jsonl

# Every tool the run invoked, with its arguments
jq -r 'select(.kind=="tool_call") | "\(.tool) \(.input)"' run.jsonl

# Total tokens for the run
jq 'select(.kind=="turn_done") | .usage.total' run.jsonl
```

## Environment variables

shell3 sets these for the duration of a headless run so tools and hooks can
adapt ([`cmd/shell3/run.go`](../cmd/shell3/run.go)):

| Variable | Set when | Value | Purpose |
| -------- | -------- | ----- | ------- |
| `SHELL3_HEADLESS` | Headless run | `"1"` | Signals to tools/hooks that no interactive TTY is available — avoid prompting. |
| `SHELL3_OUT` | `--out` provided | The output path | Lets tools/hooks know where the JSONL audit log is being written. |

Secrets are unrelated to these and are **not** passed as process environment
variables: they live in a `.env` file beside `shell3.lua` and are read from Lua
via `shell3.env.secret("KEY")`. See the main docs (`shell3 docs`).

## Spawning subagents

shell3 has no nested-agent construct inside a single config — there is exactly
one `shell3.agent()` per `shell3.lua`. Instead, you spawn a **fresh shell3
process** per subagent and use the JSONL log as the result channel. This keeps
each subagent fully isolated (its own config resolution, its own audit log) and
composes with ordinary shell tooling.

The pattern (also documented in the scaffolded
[`internal/scaffold/defaults/shell3.lua`](../internal/scaffold/defaults/shell3.lua)):

1. Pick a unique log path, e.g. `/tmp/shell3-<slug>-<timestamp>.jsonl`.
2. Launch `shell3 "<task>" --out <path>` in the background (via the async
   `bash_bg` tool) so the call returns immediately.
3. Poll the log until the final `{"kind":"end",...}` line appears.
4. Parse the events you care about (usually the `assistant_message` text, or
   specific `tool_result` outputs).

```sh
# Fan out two subagents in parallel
shell3 "find all uses of the deprecated API" --out /tmp/shell3-find-1.jsonl &
shell3 "list packages missing tests"          --out /tmp/shell3-tests-2.jsonl &
wait

# Collect each subagent's final answer
for f in /tmp/shell3-find-1.jsonl /tmp/shell3-tests-2.jsonl; do
  jq -r 'select(.kind=="assistant_message") | .text' "$f"
done
```

Each subagent writes its **own** isolated log; child events are not merged into
or prefixed onto the parent's stream. A subagent resolves its own `shell3.lua`
from the usual discovery order (no config is inherited from the parent), and
inherits `SHELL3_HEADLESS` / `SHELL3_OUT` only insofar as the parent's
environment is passed through by the spawning shell.

To detect completion, watch for the terminal line rather than a fixed sleep:

```sh
# Block until a subagent log is finished
until tail -n1 "$LOG" 2>/dev/null | grep -q '"kind":"end"'; do sleep 0.2; done
status=$(tail -n1 "$LOG" | jq -r '.status')
```
