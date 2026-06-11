# Config hot reload (`/reload`)

Date: 2026-06-11
Status: implemented (2026-06-11)

## Goal

Let the always-on `shell3 telegram` host re-read its `shell3.lua` and apply the
new configuration **without restarting the process**, triggered by a `/reload`
bot command and an agent-callable `reload` tool. This is the foundation for a
**self-evolving agent**: the agent edits its own `shell3.lua` (adding a model,
agent, tool, skill, or cron job) and brings the change live by reloading —
without an operator restarting the binary.

Two non-negotiables shape the design:

- **Validate first; a bad edit can never brick the bot.** A reload that fails
  validation leaves the running configuration exactly as it was and reports the
  error. Self-editing is therefore safe: the worst case is `reload failed: …`.
- **Apply only at an idle boundary.** A reload never mutates config in the
  middle of a turn. It waits for the session to be idle, which eliminates every
  mid-turn race against in-flight tool calls, the Lua VM mutex, and MCP
  dispatch.

## Non-goals

- **fsnotify / auto-reload on file change.** Deliberately excluded. Reload is an
  explicit act (`/reload` or the `reload` tool), never an implicit side effect
  of saving the file. This keeps the trigger controlled and the mental model
  simple. (An earlier design considered fsnotify-deferred-to-idle and rejected
  it as more moving parts for no real gain in a single-user host.)
- **Selective / partial reload.** Reload is always a **full rebuild from the
  file**. There is no diffing of individual config sections to apply some and
  skip others; "reload" means "the running host now matches the file."
- **Zero-downtime MCP carry-over.** Reload cleanly restarts MCP servers and
  model/run proxies every time. Carrying a *running* MCP manager across the
  rebuild (to avoid a sub-second restart when MCP config is unchanged) is a
  named **future optimization** — see "Future work." It is intentionally out of
  scope: live-process handoff is exactly the kind of subtle lifecycle code we do
  not want to ship under time pressure.
- **Multi-host / distributed reload.** Single in-process host only.

## Architecture

```
   /reload  (operator types it)          reload tool  (agent calls it)
        │                                      │ sets pending-reload, returns
        │                                      ▼ (turn finishes normally)
        │                              host end-of-turn hook fires
        ▼                                      ▼
   host reload coordinator  ── waits for session idle (isBusy=false) ──┐
        │                                                              │
        ▼                                                              │
   Runtime.Reload(configPath)                                          │
     1. BuildParts(new)  → validate EVERYTHING; on error: discard, return err
     2. swap: old cleanup() ; rt.{sessionConfig,cleanup,store,cron,telegram} = new
     3. re-derive each live session in place (keep history), best-effort restore
     4. rebuild the cron scheduler from rt.Cron()
        │
        ▼
   reply: "✅ reloaded — N agents, M jobs, K MCP servers"  OR  "❌ reload failed: …"
```

The host keeps the **same `*Runtime` and `*Session` objects** across a reload —
only their internals are swapped. Because the bot, the cron scheduler, and the
dashboard server all hold those pointers, nothing needs re-wiring.

### Why a full rebuild at idle is safe (and the earlier "live swap" was not)

The hard part of live reconfiguration is swapping config **under an in-flight
turn**: two Lua VMs coexisting while a tool call is mid-dispatch, MCP servers
torn down under an active MCP tool, a session's `chat.Config` mutated while a
turn reads it. Gating on idle removes all of it. Between turns a session has no
in-flight engine state, so rebuilding its `chat.Config` and `handlers` from a
fresh `Parts` is a plain reassignment. The session's underlying `chat.Session`
(`s.sess`) — which owns the conversation history and the SQLite store id — is
**kept**, so history survives the rebuild.

## Components

### 1. `Runtime.Reload(configPath string) (ReloadResult, error)` — new engine method (`pkg/shell3`)

The one substantive engine addition. Steps:

1. **Build + validate a new `Parts` from the file.** Call
   `agentsetup.BuildParts(Options{ConfigPath, CWD, HomeDir})`. `BuildParts` →
   `luacfg.Load` already runs all cross-reference validation (models resolve,
   agents reference known subagents/models, cron agents valid, MCP server refs
   valid). **If it errors, discard the new parts and return the error — the
   running config is untouched.** This is the safety gate.
2. **Quiesce.** `Reload` requires the caller to invoke it only when the session
   is idle (the host guarantees this; see component 2). `Reload` itself takes
   `rt.mu`, so concurrent `Session()`/`Close()` calls serialize.
3. **Swap shared state.** Run the *old* `cleanup()` (closes old VM, MCP servers,
   proxies, store handle), then reassign the Runtime's swappable fields:
   `sessionConfig` (new closure over new parts), `cleanup`, `store`, `cron`,
   `telegram`. (These are already fields, not constants.)
4. **Re-derive each live session in place.** For every session in `rt.sessions`,
   rebuild `s.cfg = rt.sessionConfig(savedOpts)` and
   `s.handlers = chat.NewHandlers(s.cfg)`, keeping `s.sess` (history) untouched.
   Apply best-effort override restore (see component 4).
5. **Return a `ReloadResult`** summarizing what is now live (counts of agents,
   models, jobs, MCP servers; the list of changed-but-restart-bound items — here,
   always "MCP/proxies restarted") for the human-readable reply and the log.

`Reload` is the only method that mutates the Runtime's parts after
construction, so its locking discipline against `Session`/`Close` is part of its
contract and is tested.

The Runtime stores `configPath` (captured from `RuntimeSpec` at `NewRuntime`)
so the host need not thread it; `Reload()` (no arg) reloads from that path.

### 2. Host reload coordinator (`cmd/shell3/telegram.go`)

Owns *when* `Reload` runs. Exposes a single `reload()` entry that:

- checks `sess` idle; if a turn is in flight, defers (the agent path always
  defers — see component 3);
- calls `rt.Reload()`;
- on success, swaps the cron scheduler: `sched.Stop()`, build a new
  `cron.New(sess, rt.Cron())`, `sched.Start()`, and re-point `b.SetJobRunner`
  and `srv.SetCronSource`;
- formats and sends the reply.

The coordinator is wired into the bot as a hook (`b.SetReloader(func() ReloadResult)`)
mirroring the existing `SetJobRunner`/`SetUsageRecorder` pattern.

### 3. Triggers

- **`/reload` command** (`internal/telegram/commands.go`): operator-initiated.
  The session is idle when a command is handled (commands are not turns), so it
  runs immediately. Added to `BotCommands()`.
- **`reload` agent tool**: the self-evolution path. The agent edits `shell3.lua`
  (via its existing `edit_file`/`bash` tools), then calls `reload`. Because the
  agent is *mid-turn* when it calls the tool, the tool **records a pending
  reload and returns immediately** (e.g. "reload scheduled; applies when this
  turn ends"); it does **not** reload inline (that would saw off the branch it is
  sitting on). The host's existing end-of-turn hook fires the deferred reload at
  the next idle boundary. The agent's next turn runs on the new config.

### 4. Best-effort override restore

A full rebuild resets a session's `chat.Config` to the file's defaults. Two
in-memory runtime overrides are captured before the swap and re-applied after,
*if still valid* in the new config:

- **Active agent:** capture `s.ActiveAgent()`; after rebuild, if that agent
  still exists, `s.SwitchAgent(name)`; otherwise fall back to the configured
  default and note it in the reply.
- **`/set` params:** capture `s.Snapshot().Params` (name→value overrides); after
  rebuild, replay `s.SetParam(name, value)` for each that the new active agent
  still accepts; silently skip any the new model no longer exposes.

History (the conversation transcript in `s.sess`) is preserved by construction —
it is never rebuilt. Ad-hoc tools the agent registered at runtime (not in the
file) are dropped — they are not part of the declared config.

### 5. `self-evolve` skill (`shell3.skill{}` in scaffold)

A skill whose body teaches the agent the self-modification loop: the
`shell3.lua` block syntax for `model`/`agent`/`subagent`/custom tool/`skill`/
`cron`, the **edit → call `reload` → read the validation result → fix and retry**
loop, the fact that a failed reload keeps the old config (so it is safe to try),
and that MCP/proxy changes take effect once the reload's rebuild completes. Added
to the scaffold `shell3.lua.tmpl` and granted to the default `code` agent.

## Data flow

**Operator `/reload`:**
1. `/reload` handled (session idle) → `reload()` coordinator.
2. `rt.Reload()` builds+validates new parts. On error → `❌ reload failed: <err>`,
   nothing changed.
3. On success → old `cleanup()`, swap fields, re-derive the telegram session,
   restore overrides, rebuild scheduler.
4. `✅ reloaded — 4 agents, 2 jobs, 1 MCP server (overrides kept: agent=research)`.

**Agent self-evolution:**
1. Agent turn: edits `shell3.lua`, calls `reload` tool → tool records pending,
   returns "scheduled".
2. Turn finishes → end-of-turn hook sees pending reload, session now idle →
   `reload()` coordinator runs (same path as above).
3. Result is pushed to chat (so the agent/operator sees success or the
   validation error). Next agent turn runs on the new config.

## Error handling & edge cases

- **Invalid file:** `BuildParts`/`Load` returns an error → reload aborts before
  any teardown; running host unchanged; error reported with file/line where
  available. The core safety property; explicitly tested.
- **Reload requested mid-turn:** deferred to idle (agent path always defers;
  `/reload` is already at idle). Never applied mid-turn.
- **Active agent deleted in new file:** fall back to configured default, report
  it.
- **`/set` param invalid under new model:** skipped silently (best-effort).
- **MCP server fails to start under new config:** surfaced in the reload result
  as a warning (consistent with current startup behavior, which logs MCP
  discovery failures and continues); the reload still applies the rest.
- **Reload during a cron dispatch:** the cron subagent sessions are short-lived
  and separate; `Reload` re-derives only live sessions present at swap time.
  In-flight dispatched subagents continue on the old parts until they finish
  (their goroutines are already tracked/joined as today); new dispatches use the
  new config.
- **Concurrent `Session()`/`Close()`:** serialized by `rt.mu`, which `Reload`
  holds for the swap.
- **`Reload` failure after partial swap:** the swap (steps 3–4) is constructed
  to be non-failing once validation (step 1) passes — `BuildParts` is the only
  fallible step and runs first. If a later step can fail, it logs and leaves the
  session usable; this is asserted in tests.

## Testing

- **Validate-rejects-keeps-old (unit, `pkg/shell3`):** `Reload` with a malformed
  config returns an error and the Runtime still serves the old config (an old
  agent name still resolves; a removed-in-bad-file agent still works).
- **Successful reload swaps config (unit):** add an agent/model/cron job to the
  file, `Reload`, assert `rt.Cron()` and the session's agent list reflect the new
  file; history (`s.sess` turn count) is preserved.
- **Override restore (unit):** `/agent`-switch + `/set` a param, reload an
  unrelated change, assert active agent + param survive; reload a file that
  deletes the active agent, assert graceful fallback.
- **Deferred agent reload (unit/integration):** a `reload` tool call mid-turn
  records pending and does not reload inline; the end-of-turn hook applies it at
  idle (fakellm).
- **Idle gating / locking (unit):** `Reload` serializes against `Session`/`Close`
  (no race under `-race`).
- **Cron scheduler swap (unit/integration):** after reload the scheduler reflects
  the new jobs and `/run` fires a newly-added job.
- **Command + tool wiring (`internal/telegram`):** `/reload` calls the reloader
  and replies; the `reload` tool schedules and returns.
- `go test ./... && go test -race ./...` green before done.

## Implementation approach

Build with **Sonnet subagents in parallel where files are disjoint, then
verify/fix** (the same workflow as the cron feature). The engine method
`Runtime.Reload` gates everything (it is the only new public surface) and is
built and tested first. Then: the host coordinator + `/reload` command + cron
scheduler swap; the `reload` agent tool + deferred-apply hook; the `self-evolve`
skill + scaffold + docs. The orchestrator verifies with
`go build ./... && go vet ./... && gofmt -l . && go test -race ./...` after each
batch and fixes integration seams.

## Future work (explicitly out of scope for this release)

- **MCP/proxy carry-over.** Diff the MCP server set and proxy commands between
  old and new config; when unchanged (the common case), carry the *running* MCP
  manager and proxies into the new parts instead of restarting them, eliminating
  the sub-second reload pause. Requires restructuring `BuildParts`/`cleanup` so
  those resources can be detached from the old parts and re-attached to the new.
  Deferred because live-process handoff is a disproportionate source of subtle
  lifecycle bugs relative to the gain.
- **fsnotify auto-reload** (deferred-to-idle), if explicit `/reload` ever proves
  too manual in practice.
