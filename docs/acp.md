# ACP front-end — editor and bridge integration

shell3 implements the [Agent Client Protocol (ACP)](https://agentclientprotocol.com), a JSON-RPC 2.0 protocol that lets editors and chat bridges talk to an agent over stdio. Run `shell3 acp` and any ACP-capable client — Zed, OpenACP, or a custom bridge — can open sessions, stream responses, switch agents, and handle permission requests, without knowing anything about shell3 internals.

## Quickstart

`shell3 acp` runs as a long-lived ACP server: it reads newline-delimited JSON-RPC messages from stdin and writes responses to stdout, for the entire connection lifetime. All shell3 logs go to the app log (`~/.shell3/shell3.log`); stdout carries only protocol messages.

### Zed

Add to `~/.config/zed/settings.json` under `agent_servers`. The exact key shape depends on your Zed version — check the [Zed ACP documentation](https://zed.dev/docs/ai/agents) for the current format. A typical entry looks like:

```json
{
  "agent_servers": {
    "shell3": {
      "command": "shell3",
      "args": ["acp"]
    }
  }
}
```

To start on a specific agent: `"args": ["acp", "--agent", "plan"]`.

### OpenACP

Add an entry to your OpenACP `agents.json` (see the [OpenACP documentation](https://github.com/coder/openacpbridge) for the exact path and format). A typical entry:

```json
{
  "agents": {
    "shell3": {
      "command": "shell3",
      "args": ["acp"]
    }
  }
}
```

Both clients hold the stdio connection open for the full session lifetime. shell3 sends streaming responses as newline-delimited ACP notifications while the client keeps stdin open.

## ACP methods: supported and not supported

| Method | Supported | Notes |
|--------|-----------|-------|
| `initialize` | Yes | Protocol version negotiation; advertises `load_session`, `session/list`, `session/close`, `session/resume`, and image/audio/embedded-context prompt capabilities |
| `session/new` | Yes | Creates a shell3 session; returns session ID and available modes |
| `session/prompt` | Yes | Streams text, thought (reasoning), and tool-call lifecycle events |
| `session/cancel` | Yes | Cancels the in-flight turn |
| `session/load` | Yes | Replays full history to the client as `session/update` notifications, then opens the session |
| `session/list` | Yes | Returns stored sessions from the runs-store (up to 100) |
| `session/resume` | Yes | Reopens a stored session by ID without history replay |
| `session/close` | Yes | Cancels any in-flight turn and closes the session |
| `session/set_mode` | Yes | Switches the active agent; emits a `current_mode_update` notification. The available modes come from the `session/new` response rather than from `initialize` capabilities |
| `session/request_permission` | Yes | Sent agent→client when `on_tool_call` returns `{ask=...}`; see Permissions below |
| `fs/read_text_file` | Yes | Used agent→client when the client advertises the `fs` capability; `read` content comes from the editor's buffer (sees unsaved edits); falls back to direct disk I/O otherwise |
| `fs/write_text_file` | Yes | Used agent→client when the client advertises the `fs` capability; `edit_file` writes flow through the editor; falls back to direct disk I/O otherwise |
| `terminal/*` | No | Out of scope |
| `session/fork` | No | Not implemented |
| `session/set_model` | No | Models are configured in `shell3.lua`; use `session/set_mode` to switch agents (each agent has its own model) |
| MCP-over-ACP passthrough | No | shell3 has no MCP client |

### Editor filesystem (`fs` capability)

When the connected client advertises both `fs.readTextFile` and `fs.writeTextFile` capabilities, shell3's `read` and `edit_file` tools route file I/O through the editor's buffers via `fs/read_text_file` and `fs/write_text_file` agent→client calls — reads see the editor's unsaved content, and writes flow through the editor. If the client does not advertise the `fs` capability, shell3 falls back to direct disk I/O. `bash` is unaffected and always operates on disk directly, so an editor buffer and disk can diverge for `bash` commands. Background subagent sessions (spawned via the `task` tool) do not inherit the editor FS backend — their `read` and `edit_file` calls use direct disk I/O, the same as `bash`.

## Permissions (`on_tool_call` → `session/request_permission`)

When an `on_tool_call` handler in `shell3.lua` returns `{ ask = "prompt", reason = "..." }`, shell3 pauses the tool call and sends a `session/request_permission` request to the ACP client. The client presents two options:

| Option ID | Name | Kind |
|-----------|------|------|
| `allow` | Allow | `allow_once` |
| `reject` | Reject | `reject_once` |

Selecting **Allow** lets the command run; **Reject** (or any other outcome, including timeout or error) blocks it. `allow_always` and `reject_always` are intentionally not offered — shell3's Asker interface is a boolean (allow/deny this one call), and persistent allow/deny policy belongs in the Lua `on_tool_call` handler, not in a per-request button.

A headless in-process subagent (spawned via the `task` tool) has no attached human, so an `{ask=...}` verdict is auto-denied; the block reason flows back to the parent session, where the human can decide how to proceed.

See [configuration.md](configuration.md#opt-in-command-gate--on_tool_call) for how to write `on_tool_call` handlers.

## Turn errors

A failed turn surfaces as a JSON-RPC internal error on `session/prompt`. When
the error looks recoverable by undoing the last turn (a provider HTTP 400 —
usually a conversation state the model rejects), the message includes a
rollback hint, matching the other front-ends.

## Out-of-band events

Events can arrive while no `session/prompt` is in flight:

- **Async subagent and `bash_bg` completions.** When a background job finishes while the session is idle, shell3 wakes the parent session and drains the queued turn, streaming the result as out-of-turn `agent_message_chunk` updates.

### Live job-progress cards

While a background job is running, shell3 also streams its live progress as its **own synthetic tool-call card** — separate from the completion-wake `agent_message_chunk` described above. The sequence: a `tool_call` notification (status `in_progress`) whose `toolCallId` is the job id, followed by incremental `tool_call_update` notifications carrying rendered text chunks, and a final `tool_call_update` with status `completed` (the job summary as content, if any). This applies to both `task` subagents and `bash_bg` commands. These updates are out-of-turn — the same caveat below applies: strict clients (e.g. Zed) may drop them silently while bridges (OpenACP) render them.

Out-of-turn `session/update` notifications are valid ACP. OpenACP and passthrough bridges render them. Strict clients that gate updates on an active prompt may drop them silently — that is a client limitation, not a shell3 bug.

## Sessions and the runs-store

The ACP `sessionId` is the shell3 runs-store session ID. This identity is stable and bidirectional:

- `session/list` enumerates past stored sessions from `.shell3_project/runs/`.
- `session/load` and `session/resume` reopen a stored session by that ID.
- After a shell3 ACP server restart, a client can call `session/list`, pick an ID, and call `session/resume` to reconnect — the conversation history is preserved.

History lives as plain JSONL under `.shell3_project/runs/<id>/messages.jsonl` and is always readable outside ACP (see [cli.md](cli.md#reading-your-history)).

## `--agent` flag and modes

```sh
shell3 acp                        # default agent (first declared in shell3.lua)
shell3 acp --agent plan           # start new sessions on the "plan" agent
shell3 acp -c work                # use ~/.shell3/work.lua (or --config <path>)
```

shell3's named agents **are** the ACP modes. `session/new` returns the available modes and the current mode; `session/set_mode` switches the active agent mid-session (without resetting conversation history, same as pressing `Tab` in the TUI). Each mode ID is the agent's name as declared in `shell3.lua`.

## Known limitations

### One-shot pipe (stdin EOF before response)

```sh
# This will NOT print a response:
printf '{"jsonrpc":"2.0","method":"initialize",...}\n' | shell3 acp
```

When stdin reaches EOF, the underlying `acp-go-sdk` treats the connection as closed and tears down the session before the response is flushed. This affects one-shot pipes only — real ACP clients (Zed, OpenACP) hold stdin open for the entire session lifetime and are not affected. This is an upstream SDK behavior, not a shell3 bug.

### Not supported

The following are explicitly out of scope for the `shell3 acp` front-end:

- **`terminal/*`** — no terminal muxing or PTY management over ACP.
- **`session/fork`** — not implemented.
- **`session/set_model`** — model selection is in `shell3.lua`; switch agents (modes) to change models.
- **MCP-over-ACP passthrough** — shell3 has no MCP client.
