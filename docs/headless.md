# Headless mode and `--out`

shell3 can run as a child process and stream a structured audit log of everything it did. This makes it composable: orchestrators (other shell3 agents, shell scripts, CI jobs) can spawn shell3 instances, wait for them to finish, and read what happened.

## Quick start

```bash
shell3 "summarise the README" --out /tmp/run.jsonl
tail -n1 /tmp/run.jsonl     # last line is {"kind":"end",...}
jq -r 'select(.kind=="text") | .text' < /tmp/run.jsonl
```

## What triggers headless mode

Either:
- The `--out <path>` flag is given, OR
- stdin is not a TTY *and* an input argument is provided (e.g. piped).

Both conditions export `SHELL3_HEADLESS=1` to all subprocess hooks. The `--out` case additionally exports `SHELL3_OUT=<path>`.

## What changes in headless mode

| Behavior | Interactive | Headless |
|----------|-------------|----------|
| `shell_interactive` tool exposed to the model | yes | no — stripped from tool schema |
| `<system-reminder>` about headless constraints injected | no | yes, once at turn start |
| `confirm-bash` hook policy on destructive commands | TUI picker | auto-block (unless `SHELL3_HEADLESS_TRUST=1`) |
| `confirm-bash` hook policy on safe commands | run silently | run silently |
| Hooks run at all | yes | yes — env tells them they're headless |
| TUI rendered | yes | no, plain stdout |
| `--out` audit log written | only if flag set | only if flag set |

## JSONL schema

One JSON object per line. Every event carries `ts` (RFC3339Nano UTC) and `kind`. Other fields depend on kind.

| Kind | Fields | Meaning |
|------|--------|---------|
| `start` | `input`, `persona`, `model`, `out`, `headless` | First line of every file. Identifies the run. |
| `text` | `text` | Assistant reply (coalesced — one event per message, not per token). |
| `reasoning` | `text` | Reasoning / thinking block (coalesced — one event per think block, not per token). Not part of saved history. |
| `tool` | `raw` | Pre-formatted tool call block (header + output). ANSI stripped. |
| `tty_exec_request` | `cmd`, `workdir` | Model asked for `shell_interactive`. Always denied in headless. |
| `usage` | `prompt`, `completion`, `total` | Intermediate token usage between LLM rounds. |
| `turn_done` | `prompt`, `completion`, `total` | A single turn completed successfully. |
| `error` | `error` | Turn failed; string from the underlying error. |
| `end` | `status` | Last line. `status` is `"ok"` or `"error"`. |

All `text` and `raw` fields have ANSI escape sequences removed.

Streaming text and reasoning deltas are accumulated and flushed as a single
event whenever a boundary event arrives (tool call, usage, turn_done, error,
tty_exec_request) or on `end`. This keeps the JSONL coherent — one event per
logical message — instead of one event per LLM token.

## Env vars exposed to hooks

| Var | Set when | Purpose |
|-----|----------|---------|
| `SHELL3_HEADLESS` | `1` in headless mode | Hooks branch on this to apply non-interactive policies. |
| `SHELL3_OUT` | absolute path when `--out` is set | Hooks can write supplemental logs alongside the audit. |
| `SHELL3_HEADLESS_TRUST` | not set by shell3; the orchestrator sets it | Opt-in: bypass the default safe-block policy in confirm-bash. |

## Writing your own headless-aware hook

Follow the pattern in `confirm-bash.sh`:

```bash
if [[ "$SHELL3_HEADLESS" == "1" && "$SHELL3_HEADLESS_TRUST" != "1" ]]; then
  # No human reachable. Pick a deterministic, safe action.
  echo '{"action":"block","reason":"headless: would have prompted"}'
  exit 0
fi
# Interactive fallback below — your existing logic here.
```

The hook contract is unchanged from interactive mode: stdin = on_tool_call JSON, stdout = action JSON (`allow`/`block`/`cancel`).

## Orchestrator example

A parent agent uses the `spawning-subagents` skill (shipped with shell3) to:

1. Spawn a sibling with `bash_bg`:
   ```bash
   shell3 "find every TODO in this repo" --out /tmp/find-todos.jsonl
   ```
2. `sleep 30` and check whether the JSONL ends with `{"kind":"end",...}`.
3. If yes: extract the final answer with `jq`. If no: sleep more.

See `internal/scaffold/defaults/skills/spawning-subagents.md` (also installed into `~/.shell3/skills/spawning-subagents.md`).

## Caveats

- Each `--out` invocation truncates the target file at open time. To collect history, use a unique path per run (e.g. include `$(date +%s)`).
- The JSONL is not strictly ordered with the stdout text stream — both are real-time but the file write may lag the terminal print by a few milliseconds. Treat the file as canonical.
- Orchestrators that need sub-second responsiveness should poll the file via `tail -f` or `fsnotify`, not `sleep` loops. For minute-scale tasks, polling with `sleep` is fine.
