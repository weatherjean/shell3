# Agent runtime: pkg/shell3 as an always-on personal-agent surface

Date: 2026-06-10
Status: approved (design), pending implementation plan

## Goal

Make `pkg/shell3` a great runtime for an OpenClaw/Hermes-style always-on
personal agent. Front-ends (Telegram bot, webui) are separate binaries that
consume the package; nothing they need may require touching `internal/`.

The CLI/TUI remains a first-class consumer and should *gain* from each
change (mid-turn steering, per-call tool approval).

## Non-goals (v1)

- **Multi-user tenancy.** One user, one process, a few channels. Nothing in
  the design precludes tenancy later, but no isolation/quota work now.
- **Resume after restart.** Conversation rehydration is phase-2 material;
  the store's current history rows are display/search-shaped, not
  message-shaped. Schema choices made here must not paint us into a corner
  (note: the JSONL audit sink already captures message-shaped data).
- **Cross-session event streaming.** Subagent token streams stay in their
  own JSONL; the bus carries lifecycle only.

## Architecture

```
bot binary (telegram/webui — separate repo, consumes pkg/shell3)
   │
   ▼
Runtime              one per process: config (Lua state), store, MCP, log, proxy
 ├── Session "tg:1234"     main Telegram chat        ─┐ each: own history,
 ├── Session "web:main"    webui conversation         ├─ inbox, busy gate,
 └── Session "sub:a1b2"    subagent (spawned)        ─┘ active agent
```

- **`Runtime`** (new public type) is today's `agentsetup.Build` made
  long-lived: created once from a `Spec`-like config, owns the Lua state,
  store, MCP manager, proxy spawner, and app log. `Close()` tears down.
- **`Session`** becomes cheap: message history, inbox, busy gate, active
  agent. `Runtime.Session(opts)` creates/returns named sessions. The
  existing `Start`/`Run` become thin wrappers over a single-session Runtime
  (public API stays convenient for simple embedders).
- The Lua VM stays single and mutex-guarded; for a personal agent with a
  few channels, serializing Lua tool calls across sessions is acceptable.
  SQLite handles concurrent sessions; MCP servers are shared per-Runtime.

## Inbox, Interject, Wake

The **inbox** (per session) is the unification seam: user steering messages
and finished-subagent results are both inbox items.

- `Session.Interject(text string, parts ...Part)` is the chat-message path
  and never fails:
  - Turn in flight → item lands in the inbox; the turn loop drains it at the
    existing between-rounds injection point as a
    `<system-reminder>user interjected: …</system-reminder>` so the model
    course-corrects immediately.
  - Idle → item is queued and a `Wake` event is emitted; the host starts the
    next turn with the queued items.
- `Send` keeps strict `ErrBusy` semantics: overlap is a host programming
  error. `Interject` is what front-ends call for human messages.
- **Two event planes.** In-turn events keep today's contract (per-`Send`
  channel, synchronous sink, close = end of turn). Out-of-turn events go on
  `Runtime.Events() <-chan HostEvent` with `{Session, Kind, Payload}` —
  v1 kinds: `Wake` (inbox has items, no turn running). One bus for N
  sessions: a bot binary has exactly one select loop.

## Tool approval: guard verdict `ask`

- `luacfg.Decision` gains a fourth verdict: `allow | block | cancel | ask`.
- `ask` suspends the tool call and invokes a host-registered
  `Approve(ctx context.Context, req ApprovalRequest) bool` on the turn
  goroutine. Blocking is correct (the model is waiting) and ctx-cancellable.
  `ApprovalRequest` carries tool name, parsed args, agent, session.
- Policy stays in Lua (user-ownable, e.g. "ask for bash unless it matches
  this allowlist"); the host only renders the question:
  - Telegram: Approve/Deny buttons.
  - TUI: inline `[allow bash: rm -rf …? y/N]` prompt — per-call approval the
    TUI doesn't have today.
  - No approver registered → `ask` degrades to deny-with-reason (headless
    stays safe).
- Approval requests and verdicts are recorded in the audit JSONL.

## Inbound media

- `Session.SendParts(ctx, prompt string, parts []Part)` (and the same
  `parts` on `Interject`), with `Part{Kind: Image|Audio, Path string,
  Data []byte, MIME string}` — maps onto the existing `llm.ContentPart`
  plumbing that `read_media` already uses. Telegram voice notes and photos
  flow in without touching disk when the host has bytes in hand.

## Subagents as built-in tools

Replaces the scaffold's `spawning-subagents` skill (bash_bg + CLI + JSONL
polling) with in-process spawning on the shared Runtime:

- `spawn_agent(task, agent?)` → returns an id immediately; runs
  `rt.Session("sub:<id>")` headless with a fresh context on a goroutine,
  using the named agent from the same shell3.lua (default: the caller's
  agent).
- Completion posts a `subagent finished: <result>` item to the **parent's
  inbox** — injected mid-turn if the parent is still running, `Wake` if not
  (OpenClaw's announce flow).
- `list_agents()` → snapshot of running/finished subagents for polling.
- Depth-limited to 1: subagent sessions get the spawn tools stripped from
  their schema.
- Each subagent writes its own audit JSONL under `.shell3/agents/`.
- Implemented in Go on the Runtime (turn-scoped handlers, same pattern as
  compact_history), not in Lua.

## TUI impact (all gains)

- Runtime/Session split: mechanical port of `tui.RunOnce`/`RunInteractive`.
- Enter-while-busy currently dies at the `patchapp` busy-gate; it becomes
  `Interject` with a dim `[queued for the running turn]` echo — Claude-Code
  style steering. Esc/cancel unchanged.
- `ask` guards get an inline y/N approval prompt.
- Subagent completion renders as a dim notice; when idle the TUI auto-runs
  the wake turn.

## Bootstrap / scaffold updates

The base config written by `shell3 boot` is updated in the same release:

- Remove `lib/skills/subagents.lua` and its `require`/`skills` wiring from
  the template; the `spawning-subagents` skill is retired.
- Gate the new built-ins in the agent `tools` table (e.g. `subagents =
  true` on the `code` agent, `false` on `plan`).
- Prompt text: replace the "Skills → spawning-subagents" section with a
  short "Subagents" tools section (spawn for independent subtasks, results
  arrive automatically; don't poll in a tight loop).
- Scaffold ships an example `ask` guard (e.g. `guards.confirm_destructive`)
  so boot users see the approval flow working in the TUI out of the box.
- Cookbook: update `lib/guards.lua` recipe for `ask`, drop the subagent
  JSONL-polling recipe, add a note on the builtin.

## Testing

Fakellm-driven, race-enabled, hermetic (temp HOME) like the existing suite:

- Inbox: injection timing (mid-turn vs idle), ordering, wake emission.
- `ask` guard: scripted approver allow/deny, no-approver degrade,
  ctx-cancel during approval.
- Subagents: spawn/complete/inbox delivery, depth limit, parent crash
  isolation, `list_agents` snapshots.
- `SendParts` → `ContentPart` mapping (image + audio).
- Runtime: N concurrent sessions race tests; shared-Lua serialization.
- TUI: `fakeSession`-based tests for interject echo + approval prompt.

**Manual acceptance:** delete `~/.shell3`, run `shell3 boot`, and play —
the rebooted config must demonstrate subagent spawning and `ask` approval
in the TUI with no hand-editing.

## Phasing

Each phase lands independently green on the `agent-runtime` branch:

1. **Runtime/Session split** — pure refactor; `Start`/`Run` become
   wrappers; TUI port mechanical.
2. **Inbox + Interject + turn-loop drain** — TUI gains mid-turn steering.
3. **Guard `ask` + `Approve`** — TUI gains per-call approval prompts.
4. **`SendParts` media.**
5. **`spawn_agent`/`list_agents` + `Wake` bus.**
6. **Scaffold/bootstrap refresh** — retire the subagents skill, wire the
   new gates and example guard, update cookbook; manual acceptance run.
