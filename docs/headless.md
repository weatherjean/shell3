# Headless mode and `--out`

shell3 can run as a child process and stream a structured audit log of everything it did. This makes it composable: orchestrators (other shell3 agents, shell scripts, CI jobs) can spawn shell3 instances, wait for them to finish, and read what happened.

## Quick start

```bash
shell3 "summarise the README" --out /tmp/run.jsonl
tail -n1 /tmp/run.jsonl     # last line is {"kind":"end",...}
jq -r 'select(.kind=="assistant_message") | .text' < /tmp/run.jsonl
```

## What triggers headless mode

Either:
- The `--out <path>` flag is given, OR
- stdin is not a TTY *and* an input argument is provided (e.g. piped).

Both conditions export `SHELL3_HEADLESS=1` to the process environment (visible to any subprocess shell3 spawns, e.g. `bash` tool calls). The `--out` case additionally exports `SHELL3_OUT=<path>`.

## What changes in headless mode

| Behavior | Interactive | Headless |
|----------|-------------|----------|
| `shell_interactive` tool exposed to the model | yes | no — interactive TTY is unavailable, the tool returns an error |
| `<system-reminder>` about headless constraints injected | no | yes, once at turn start |
| `confirm_dangerous` guard on destructive commands | blocks the call | blocks the call (no human to prompt) |
| `confirm_dangerous` guard on safe commands | allows | allows |
| TUI rendered | yes | no, plain stdout |
| `--out` audit log written | only if flag set | only if flag set |

## JSONL schema

One JSON object per line. Every event carries `ts` (RFC3339Nano UTC) and `kind`. Other fields depend on kind.

| Kind | Fields | Meaning |
|------|--------|---------|
| `start` | `input`, `persona`, `model`, `out`, `headless` | First line of every file. Identifies the run. |
| `user_message` | `role`, `text` | The user input for the turn. |
| `assistant_token` | `text` | A streamed token delta of the assistant reply. |
| `assistant_message` | `role`, `text` | The completed assistant reply for a message. |
| `assistant_reasoning` | `text` | Reasoning / thinking block. Not part of saved history. |
| `tool_call` | `tool`, `input`, `call_id` | The model invoked a tool, with its JSON arguments. |
| `tool_result` | `tool`, `output`, `call_id`, `tool_error` | Result of a tool call (`tool_error: true` if it failed). |
| `usage` | `usage` (`{prompt, completion, total}`) | Intermediate token usage between LLM rounds. |
| `turn_done` | `usage` (`{prompt, completion, total}`) | A single turn completed successfully. |
| `system_reminder` | `text` | Injected system reminder (e.g. the headless-constraints notice). |
| `retry` | `text` | The SDK is retrying a failed LLM request. |
| `error` | `text` | Turn failed; string from the underlying error. |
| `end` | `status` | Last line. `status` is `"ok"` or `"error"`. |

`session_id` (the store session id) is attached to most events when a store is configured. All `text` and tool `output` fields have ANSI escape sequences removed.

The `start` and `end` lines are written directly by the JSONL sink; every other kind is the string form of an internal chat event (`assistant_message`, `tool_call`, …). Tool calls and results are emitted as separate `tool_call` / `tool_result` events rather than a single pre-formatted block.

## Environment variables

| Var | Set when | Purpose |
|-----|----------|---------|
| `SHELL3_HEADLESS` | `1` in headless mode | Set by shell3. Visible to subprocesses (e.g. `bash` tool calls) and to custom `on_tool_call` guards, which can branch on it via `os.getenv`. |
| `SHELL3_OUT` | absolute path when `--out` is set | Subprocesses can write supplemental logs alongside the audit file. |
| `SHELL3_HEADLESS_TRUST` | not set by shell3; an orchestrator may set it | Opt-in marker the orchestrator can read from a custom guard to relax its non-interactive policy. shell3's built-in `confirm_dangerous` guard does **not** read it. |

## Guarding tool calls

Tool-call gating is configured in `shell3.lua`, not via external hook scripts. Each agent's `on_tool_call` is a chain of:

- the built-in `shell3.guards.confirm_dangerous{}` handle, which blocks commands matching a denylist of destructive patterns. In headless mode there is no human to confirm, so a matched dangerous command is simply blocked (the `prompt` option is reserved and currently a no-op).
- and/or custom Lua functions `(call) -> { action = "allow" | "block", reason = "..." }`.

A custom guard can make a headless-aware decision by reading the environment:

```lua
on_tool_call = {
  function(call)
    if os.getenv("SHELL3_HEADLESS") == "1"
       and os.getenv("SHELL3_HEADLESS_TRUST") ~= "1" then
      -- No human reachable. Pick a deterministic, safe action.
      return { action = "block", reason = "headless: would have prompted" }
    end
    return { action = "allow" }
  end,
  shell3.guards.confirm_dangerous{},
}
```

See the canonical config at `internal/scaffold/defaults/shell3.lua` and `shell3 docs` for the full guard/tool API.

## Orchestrator example

A parent agent can spawn a sibling shell3 as a child process and watch its JSONL:

1. Spawn a sibling in the background (e.g. via the `bash_bg` tool):
   ```bash
   shell3 "find every TODO in this repo" --out /tmp/find-todos.jsonl
   ```
2. Poll until the JSONL ends with `{"kind":"end",...}`.
3. If done: extract the final answer with `jq` (e.g. the last `assistant_message`). If not: poll again.

## Caveats

- Each `--out` invocation truncates the target file at open time. To collect history, use a unique path per run (e.g. include `$(date +%s)`).
- The JSONL is not strictly ordered with the stdout text stream — both are real-time but the file write may lag the terminal print by a few milliseconds. Treat the file as canonical.
- Orchestrators that need sub-second responsiveness should poll the file via `tail -f` or `fsnotify`, not `sleep` loops. For minute-scale tasks, polling with `sleep` is fine.
