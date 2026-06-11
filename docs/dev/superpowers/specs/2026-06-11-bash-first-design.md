# bash-first — collapse the tool surface onto bash + a sink

Status: design (branch `feat/bash-first`)
Date: 2026-06-11

## Vision

shell3 drifted toward a Claude-Code-shaped agent: a dozen bespoke Go tools
(`spawn_agent`, `list_agents`, `history_get`, `history_search`,
`prune_tool_result`, `compact_history`, …) each with its own schema, handler,
and wiring. This branch returns the core to its stated philosophy — **Unix-
composable, Lua-is-king, bash-first**: the agent acts through `bash` and
`edit_file`, and everything else is a *file the agent reads* or a *command it
runs*, coordinated through one small notification channel (the **sink**).

Guiding cuts:

- The agent's verbs are `bash` and `edit_file`. Other capabilities are
  expressed as on-disk artifacts (a SQLite DB it queries, a transcript it
  `cat`s) or backgrounded commands — not as new tool schemas.
- Internal Go machinery exists only where bash genuinely cannot reach: live
  context window, media injection, PTY handoff, and the fiddly process-group /
  zombie-reaping detail of backgrounding.
- Policy lives in Lua; the engine provides mechanism. Default config is
  **unsafe** (full shell, no guards) by deliberate choice, with a loud opt-in
  safety hook.

## Tool inventory

| Tool | Disposition | Why |
|------|-------------|-----|
| `bash` | **keep** | the primitive |
| `edit_file` | **keep** | structured edit args avoid shell-escaping failure; apply logic already ported |
| `bash_bg` | **keep + enhance** | THE background primitive; now writes a sink notification on exit |
| `read_media` | **keep** | injects image/audio bytes into the next message — no bash equivalent |
| `shell_interactive` | **keep** | PTY handoff in the TUI — no bash equivalent |
| `history_get` / `history_search` | **delete** | → ro-SQLite `history` skill (bash) |
| `prune_tool_result` / `compact_history` | **delete** | → host-enforced auto-compaction (`compact_at`) |
| `spawn_agent` / `list_agents` | **delete** | → subagent = `bash_bg` running `shell3`, results via the sink |

Net: 5 internal tools (was 11), the `subRegistry`, `StoreHandler`, and the
session-scoped subagent-cancellation machinery all removed.

## 1. The sink — one notification channel

A **sink** is a per-session append-only JSONL file (e.g.
`.shell3/sink/<session>.jsonl`). It carries short *pointer* notifications, never
full payloads. Heavy output stays in files the agent reads itself.

```
{"ts":"…","kind":"agent_done","id":"a3f","status":"ok","transcript":".shell3/agents/a3f.jsonl","preview":"Found 3 call sites…"}
{"ts":"…","kind":"bg_done","id":"bg_9c","exit":0,"log":"/tmp/shell3/runs/bg_9c.log","cmd":"npx tsc --watch"}
```

**Producers** append a line (`O_APPEND`, one `write` per line — lines are small
pointers, always < PIPE_BUF, so appends are atomic; `flock` is a belt-and-braces
addition only if a producer ever writes a large line). Two producers:

1. `bash_bg`'s reaper goroutine — generic `bg_done` for any command, on exit.
2. A child `shell3 --append-sinkfile <path>` — rich self-reported lifecycle
   (`agent_done` with a result preview), for subagents specifically.

**Consumer** is the host (`pkg/shell3.Session`). On session start it launches a
watcher (poll the file size / tail by byte offset; no mid-file deletion — track
a read offset, remove the file on `Close`). For each new line it calls
`sess.Interject(formatNotification(line))` and `Wake`s the session if idle —
reusing the exact path `deliverSubagentResult` uses today
(`pkg/shell3/subagents.go:192`). The notification injected into the agent's next
turn is a pointer, e.g.:

> `<system-reminder>subagent a3f finished (ok). Transcript: .shell3/agents/a3f.jsonl — read it if you need the detail.</system-reminder>`

This is the crux refinement: the agent is *notified*, not *flooded*. Context
stays small; the agent `cat`s the transcript on demand.

### Sink scope

Per-session. The parent owns its sink path and passes it to children via
`--append-sinkfile`. Concurrent sessions / Telegram chats never cross-talk;
cleanup is trivial on `Close`.

## 2. `bash_bg` as the unified background primitive

`bash_bg` stays (it earns its place — `bgjobs.go` already handles `Setpgid`,
zombie reaping, atomic registry writes — exactly what bash does badly). Two
additions:

- `bgjobs.Start(cmd, workdir, sinkPath)` — the reaper appends a `bg_done`
  notification to `sinkPath` on child exit.
- The sink path is threaded through `ToolConfig` → `BashBgHandler`.

`/stop` already kills tracked bg groups (`bgjobs.KillAll`) — so cancelling
subagents falls out for free once subagents *are* bg jobs.

## 3. Subagents = a backgrounded `shell3`

A "subagent" stops being a subsystem and becomes a convention: background a
`shell3` invocation that self-reports to the sink.

New `shell3 run` flags (`cmd/shell3/run.go`):

- `--agent <name>` — select the active agent (today only the TUI picks it).
- `--append-sinkfile <path>` — append lifecycle notifications to the sink.
- `--id <id>` — caller-chosen id stamped into notifications and used for the
  transcript filename.

The parent delegates by calling `bash_bg`:

```
shell3 --config <cfg> --agent explorer \
  --out .shell3/agents/<id>.jsonl \
  --append-sinkfile <session-sink> --id <id> \
  "<self-contained task>"
```

`--out` already streams the full transcript (the file the parent reads). On
exit the child writes `agent_done` (with a preview) to the sink; the host
injects the pointer.

**Ergonomics.** The agent must know which subagents exist and the exact command
(with runtime paths filled in). The host injects a small prompt fragment at
session start listing declared subagents and the templated `bash_bg` command
with `<session-sink>`/`<cfg>` already substituted. (This is where command-backed
prompt content from §7 pays off.)

**Depth limit.** A child `shell3` is launched without the delegation prompt
fragment (and/or a `--no-subagents` flag), so it cannot recurse. `shell3.subagent{}`
declarations are kept as named headless-agent configs; only the *spawn
mechanism* changes from an internal tool to `bash_bg`.

Deleted: `SpawnToolDefs`/`listAgentsTool` (tooldefs.go), `SpawnRequest`/
`AgentSnapshot`/`Spawn`/`ListAgents` (toolhandler.go + turn.go), all of
`pkg/shell3/subagents.go`, the `subCtx`/`CancelSubagents` plumbing.

Trade-off accepted: each spawn is a full process start (reload Lua, open store)
rather than an in-process child Session — heavier, but maximally extensible and
it unifies cancellation, isolation, and delivery onto one mechanism.

### Resolved decisions (Phase 2 implementation)

- **Single notification per spawn.** `bash_bg` gains `notify_on_exit` (bool,
  default `true` → emits `bg_done`). A subagent spawn passes `notify_on_exit=false`
  so the ONLY notification is the child's own `agent_done` (id / status /
  transcript / preview ≤200 chars) written via `--append-sinkfile`. Plain bg
  jobs (servers, watchers) keep `bg_done`. No double-injection.
- **Delegation context comes from pkg/shell3, not agentsetup.** The per-session
  values (concrete sink path, resolved config path, `shell3` binary path via
  `os.Executable`, the active agent's allowed subagents + descriptions, the exact
  templated `bash_bg` spawn command) are injected as a first-turn system context
  by the Session — agentsetup can't see session-level paths. The agent picks a
  per-spawn id; the transcript is `.shell3/agents/<id>.jsonl`.
- **Depth-1 via `--no-subagents`.** Spawned children run with `--no-subagents`,
  which suppresses the delegation context so they cannot recurse. `shell3.subagent{}`
  declarations are KEPT (they name the headless agent configs the parent may
  delegate to); only the spawn *mechanism* moves from an internal tool to `bash_bg`.
- **Cron `Dispatch` → subprocess + Notice (NOT the sink).** Host-initiated cron
  keeps its operator-notification semantics: exec `shell3 --agent X --out <t>
  "<prompt>"` as a tracked subprocess, wait, read the final assistant text from
  the transcript, emit `Notice` iff `notify || failed`. It does not route through
  the sink watcher (which inject+wakes — wrong for an operator notice). The
  `cron.Dispatcher` interface is preserved; only `Dispatch`'s body changes.
- **`/stop`** drops `CancelSubagents()`: model-spawned subagents are now bg jobs,
  already killed by the existing `bgjobs.KillAll`; cron subprocesses are joined
  by `Runtime.Close`.
- **Deletes:** `spawn_agent`/`list_agents` (SpawnToolDefs, listAgentsTool, the
  turn.go dispatch + chat SpawnRequest/AgentSnapshot/Spawn/ListAgents), the whole
  `subRegistry` + `Session.spawn`/`deliverSubagentResult`/`CancelSubagents`/`subCtx`,
  and the agentsetup spawn-tool injection.

## 4. Auto-compaction (`compact_at`)

`prune_tool_result` and `compact_history` (model-driven) are deleted in favor of
a host-enforced threshold.

- New `compact_at` on the model config (`luacfg.Model` + `modelKeys` in
  register.go): an integer token threshold (or fraction of `context_window`).
- In `turn.go`, before running a user turn, if `sess.lastPromptTokens >=
  compact_at`: **interrupt → compact → continue**.
  1. Run one synthetic LLM call with a compaction system prompt that returns the
     existing structured summary (summary / important_files / references /
     skills / next_steps).
  2. Apply the history rewrite — extract the reusable core of
     `handleCompactHistory` (tools.go:69) into a `compactInto(...)` helper,
     dropping the tool-call-entry specifics.
  3. Proceed with the user's turn against the compacted history.

Keeps the structured summary; removes the two tools, `handler_prune.go`, the
turn-scoped `compact_history` handler, and `ToolGates.Prune`/`Compact`.

Delicate bits (own commit, careful tests): the extra LLM round-trip, prompt-cache
invalidation on rewrite, and making sure a compaction that itself overflows
fails safe (cap input, fall back to truncation).

## 5. History → a bash skill over read-only SQLite

The conversation store stays (it still persists history); only the *tools* go.
The agent reads it directly:

```
sqlite3 'file:<db>?mode=ro' "SELECT created_at, role, content FROM … WHERE … LIMIT 50"
```

- Delete `history_get`/`history_search` (tooldefs.go), `StoreHandler`
  (handler_store.go), `ToolGates.History`.
- Ship a `history` skill documenting the schema + canonical queries (recent
  session, full-text search via the existing FTS5 table). The DB path is a
  runtime value — inject it via §7 command-backed body or a host-templated
  fragment.
- **Enable WAL for file-backed DBs** (`store.Open`, store.go:24). Today WAL is
  off; a cross-process `sqlite3` reader would contend with the writer (up to the
  5s busy_timeout). WAL gives lock-free concurrent readers; keep it off only for
  `:memory:` as the current comment notes.

## 6. Guard → `shell3.wrap_bash`, unsafe by default (FULL REMOVAL)

Decision: **full removal of the guard engine.** Unsafe by default; the only
safety surface is a Lua bash wrapper. This deletes the human-in-the-loop
approval flow with it (accepted).

Remove:

- The guard engine: `OnToolCallFor`, `runLuaGuard`, `parseAction`, the
  `Decision`/`guardDecision` types, `on_tool_call` parsing in register.go,
  `GuardEntry`/`Guard` fields on `Agent`/`Subagent`.
- The approval flow end-to-end: `DecisionAsk`/`guardAsk`, `ApprovalRequest`,
  `Approve`/`SetApprover` (pkg/shell3 + chat `TurnConfig`), the TUI y/N approval
  (`patchapp/approval*`, busy-gate approval bits), and the Telegram approver
  (`internal/telegram/approval.go`, the `ap:` callback in the bot).
- The scaffold `guards.lua` and all `on_tool_call = { … }` wiring.

Add `shell3.wrap_bash(fn)`: a single Lua hook the bash tool's command passes
through before execution. `fn(cmd)` returns either a string (the command to run,
possibly rewritten) or `nil`/`false` + reason to reject. Pure allow / block /
rewrite — **no `ask`** (nothing to ask; there is no approver anymore). Applies
only to `bash`/`bash_bg`. The default scaffold ships a **loud no-op**
`wrap_bash` whose comment states the shell is unguarded and shows how to lock it
down.

Mechanism: `wrap_bash` is invoked inside the bash handler path (or just before
dispatch) via a `luacfg` binding, not through the deleted guard chain.

## 7. Command-backed prompt / skill content (IMPLEMENTED)

A skill/prompt body may come from a command instead of a literal, resolved
**once per load/reload** (so prompt caching is preserved within a session):

```lua
shell3.skill({ name = "history", description = "…",
  body_cmd = "cat lib/skills/history.md" })
shell3.agent({ name = "build", model = "…", prompt_cmd = "cat lib/prompts/build.md" })
shell3.subagent({ name = "explorer", description = "…", prompt_cmd = "…" })
```

Used to fill runtime values (DB path, sink path, subagent list) into the
history skill and the delegation fragment from §3/§5. Auto-run at load; never
per-turn.

Implemented in `internal/luacfg`: `body_cmd` on skills and `prompt_cmd` on
agents/subagents (`register.go`); a resolution pass in `Load` (`luacfg.go`) runs
each command via `runBodyCmd` (`cmdbody.go`) with **cwd = the config dir** (same
dir as `.env`/`lib`), so relative paths resolve next to `shell3.lua`. Exactly
one of `body`/`body_cmd` (skills) and `prompt`/`prompt_cmd` (agents/subagents) —
setting both raises a Lua error. **Fail-closed**: a command that errors or
returns empty output fails the whole `Load` (and reload goes through `Load`, so
a broken prompt file can never silently swap in an empty prompt at runtime).

## 8. Stub-tools for hallucinated tool names

Models trained on other harnesses reflexively call `read_file`, `grep`,
`write`. Register name-only stubs that return a redirect string instead of
erroring:

```lua
shell3.stub_tools({
  read_file = "Use bash: cat <path>",
  grep      = "Use bash: rg <pattern>",
  write_file= "Use edit_file (empty old_string creates/overwrites).",
})
```

Implemented as a thin custom-tool variant (tooldefs + `CallTool`): a tool def
whose handler returns the fixed message. Self-correcting UX, near-zero code.

## 9. Color forwarding (TUI)

`tui/render.go:179` `renderToolResultBody` dims every bash line, flattening any
ANSI. Change: for `bash`/`bash_bg`, pass output through unstyled (no `dimLines`,
no strip) so SGR colors survive; keep truncation. The agent opts into color with
`CLICOLOR_FORCE=1` / `--color=always` (piped output disables color by default).
Display-only; model-facing bytes unchanged.

## CLI / config surface changes

- `shell3 run`: `--agent`, `--append-sinkfile`, `--id`, (maybe `--no-subagents`).
- `luacfg.Model`: `compact_at`.
- New Lua API: `shell3.wrap_bash(fn)`, `shell3.stub_tools(map)`, optional
  `body_cmd`/`prompt_cmd`.
- Scaffold `shell3.lua.tmpl`: drop guard wiring, add loud-no-op `wrap_bash`,
  add `compact_at`, ship `history` skill, replace the subagent tool prose with
  the `bash_bg`-delegation fragment.

## Deletion list (concrete)

- `internal/chat/handler_prune.go`, `handler_store.go` (+ tests)
- `internal/chat/tools.go`: `handleCompactHistory` → refactor to `compactInto`
- `internal/luacfg/tooldefs.go`: prune/compact/history/spawn/list defs
- `pkg/shell3/subagents.go` (+ `dispatch.go` subagent bits, `subCtx` plumbing)
- `internal/chat/toolhandler.go`: `SpawnRequest`/`AgentSnapshot`/`Spawn`/`ListAgents`
- `ToolGates`: `Prune`, `Compact`, `History` (register.go, gate keys)
- subagent-as-tool wiring in `agentsetup.go` (`SpawnToolDefs` injection)

## Risks & open questions

1. **Guard removal scope (§6).** DECIDED: full removal. The approval flow
   (Telegram Approve/Deny, TUI y/N) is deleted with the engine. Touches
   telegram/approval.go, patchapp approval, pkg/shell3 Approve/SetApprover, and
   chat guard/Approve plumbing — the widest-blast-radius phase.
2. **Subprocess subagent cost.** Process-per-spawn startup (Lua reload, store
   open). Fine for interactive/Telegram; could matter for fan-out. Mitigation
   later: a `shell3 serve` warm pool — out of scope here.
3. **Auto-compaction correctness.** Extra round-trip, cache invalidation,
   overflow-during-compaction. Most delicate piece; own commit + tests.
4. **WAL flip.** Verify no regression for the in-process writer and the reload
   path; gate to file-backed DBs only.
5. **Sink durability.** If the host process dies mid-run, undrained
   notifications are lost (transcripts/logs survive on disk). Acceptable —
   notifications are ephemeral pointers.

## Commit sequence within the branch (one branch, ordered commits)

1. Sink primitive + host watcher + `bash_bg` `bg_done` notification.
2. `--agent`/`--append-sinkfile`/`--id` flags + `agent_done` self-report;
   delete `spawn_agent`/`list_agents`/`subRegistry`; delegation prompt fragment.
3. Auto-compaction (`compact_at`); delete prune/compact tools.
4. History → ro-SQLite skill + WAL flip; delete history tools.
5. `wrap_bash` + unsafe-default scaffold; rework guard wiring.
6. Stub-tools.
7. Color forwarding.
8. Docs/CHANGELOG/CLAUDE.md sweep.
</content>
</invoke>
