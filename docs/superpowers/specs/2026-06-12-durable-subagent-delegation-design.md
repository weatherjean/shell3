# Durable Subagent Delegation — Design

**Date:** 2026-06-12
**Status:** Approved design, pre-implementation
**Branch context:** `feat/bash-first` (subagent = backgrounded `shell3` subprocess)

## Problem

Today a subagent is a backgrounded `shell3` subprocess that reports completion by
appending an `agent_done` pointer to a **per-session sink file**, which the
spawning process's watcher tails and injects into the parent's next turn. This
has three structural limits:

1. **Depth-1 only.** Spawned children run `--no-subagents` (delegation context
   suppressed) plus a `SHELL3_NO_SUBAGENTS=1` env backstop. The limit is a
   *prompt-level* signal, not a hard gate — a subagent with full `bash` can shell
   out to the binary directly — and notifications are not designed to flow past
   one level.

2. **Silent drops.** Headless `once` mode exits as soon as the model's final
   turn ends; `Close()` stops the sink watcher and **deletes the sink file**
   before any longer-running `bash_bg` child finishes. The orphaned child's
   completion is appended to a now-deleted file and lost (error discarded).
   This is structural, not probabilistic: any bg job outliving the final turn is
   dropped.

3. **No reactivation.** A fire-and-forget delegator that has already exited
   cannot receive its child's result at all — there is no live process to notify,
   and conversations are not resumable (the store is an audit log, not a replay
   log; tool results are never persisted).

## Goals

- **Arbitrary-depth delegation** that is *solid*: every completion reaches a
  living consumer; the human at the root is the guaranteed backstop.
- **Durable delegation:** an agent can delegate, exit, and be **revived** to
  process its child's result when it returns.
- **Replayable conversations:** any session can be reconstructed from storage and
  continued.
- Clean rewire — pre-release, **no backward-compatibility or data migration**.

## Non-Goals

- Cross-machine delegation (single host; Unix-domain sockets).
- Preserving the existing sink-file mechanism (it is fully replaced).
- Hardening `bash` into a sandbox (shell3 stays unsafe-by-default; the only
  policy surface remains `shell3.wrap_bash`).

---

## Architecture Overview

Three durable facts live in **SQLite**; one live transport is a **Unix-domain
socket**. The division is strict:

| Concern | Where | Lifetime |
|---|---|---|
| Replayable conversation (`messages`) | SQLite | durable |
| Report pointer (`parent_session_id`) | SQLite | durable |
| Parked notifications for a dormant session (`inbox`) | SQLite | durable |
| Liveness registry (`session_id → pid, sock, status`) | SQLite, but treated as ephemeral | rewritten each boot/exit |
| Notification **payload** in flight | Unix-domain socket | ephemeral (live delivery only) |

**The pointer is always SQLite. The report goes to the socket if the target is
alive, and to the SQLite `inbox` if it is dormant.** The socket never carries
durable state; SQLite is never the live transport.

### Report algorithm (identical for original spawn and revived wake-turn)

At the end of *any* turn, a session reports as follows — nothing is threaded
through flags or env; everything derives from persisted state:

1. Read **my own** `parent_session_id` from my session row.
2. If null → I am the root; nothing to report.
3. Look up the parent in the liveness registry:
   - **`live`** → open the parent's socket, push the notification payload (one
     JSON line). The parent's running process `Interject`s it into its in-memory
     queue and processes it next turn. (Concurrent children → queue absorbs them.)
   - **`dormant`** → append the payload to the parent's SQLite `inbox`, then run
     the **revive claim** (below). The reviver drains the inbox on boot.
4. The revived parent, once booted, reads *its* `parent_session_id` and repeats
   these steps up to *its* parent. Recursion up the tree, each hop driven by a
   persisted pointer, terminating at the root TUI (always `live`).

Because the report walk is reconstructed from `parent_session_id` links at
completion time — not from an ephemeral chain captured at spawn — a revived
process reports correctly even though it never saw the original spawn
environment, and even if intermediate ancestors have died and been revived as
different pids.

---

## Section 1 — CLI surface

Today the bare root command *is* the run command (`main.go`:
`root.RunE = runCmd.RunE`), with `boot`/`telegram` as subcommands, and mode is
inferred from terminal detection (`run.go:61`). We make mode explicit:

- **`shell3`** (root) — interactive TUI only. Gains `--resume <session-id>`. No
  positional prompt.
- **`shell3 run`** — every non-interactive invocation: direct prompts, subagent
  spawns, headless audit runs. Flags:
  - `--prompt <text>` — unified input (replaces positional `[message]` + implicit
    stdin path).
  - `--resume <session-id>` — reload a conversation and continue (the machine
    reactivation primitive; also backs the TUI's `--resume`).
  - `--parent-session <id>` — record the report pointer on the new session row.
  - `--agent`, `--config`, `--out`, `--id` — as today.
  - **Removed:** `--append-sinkfile`, `--no-subagents` (sink mechanism and
    prompt-level depth gate both retired).
- `boot` / `telegram` — unchanged.

Net: `shell3` = human, `shell3 run` = machine. The terminal-detection inference
is deleted.

---

## Section 2 — Replayable persistence

Clean rewire of `internal/store`. Today `history` is an FTS5 audit table that
saves user/assistant text plus a one-line *summary* per tool call and **drops
tool results** (`internal/chat/turn.go:flushMessages`) — not replayable.

New shape:

- **`messages`** table: `(session_id, seq, role, content, tool_calls_json,
  tool_call_id, name, created_at)`. Stores the **full** `llm.Message` stream
  including `RoleTool` results, in order. Source of truth for replay.
- **`sessions`** table gains: `parent_session_id` (nullable; the report
  pointer), and the liveness columns `pid`, `sock`, `status`
  (`live` | `dormant`), plus `started_at` / `ended_at` / `summary` as today.
- **`inbox`** table: `(session_id, seq, payload_json, created_at)` — notifications
  parked for a dormant session.
- FTS5 `history` retained for search, derived from `messages` (or dropped — minor;
  default: keep for the `history` bash skill).

New store API:

- `Store.LoadSessionMessages(id) ([]llm.Message, error)` — reconstruct the slice.
- `Store.ResumeSession(id)` — re-open an existing session for continuation
  (clears `ended_at`, sets `status=live`, stamps `pid`/`sock`).
- `Store.AppendInbox(id, payload)` / `Store.DrainInbox(id) ([]Notification)`.
- `Store.ClaimRevive(id) (won bool)` — atomic `dormant → reviving` transition
  for leader election (see Section 5).
- `Store.SetLiveness(id, pid, sock, status)`.

Session seeding: `chat.Session` gains `SessionOpts.InitialMessages` (or
`SetMessages`) applied before turn one. `pkg/shell3.newSession` (`shell3.go:382`)
calls `ResumeSession(id)` + `LoadSessionMessages(id)` when `--resume` is set,
instead of `StartSession()`.

### Compaction fidelity (subtle, required)

shell3 auto-compacts at the model's `compact_at` token threshold. The store must
mirror the **compacted** context, not the raw pre-compaction blob — otherwise a
resumed session reloads an over-window history. When compaction rewrites the
in-memory message list, we mirror it in `messages`: delete the collapsed `seq`
range and insert the summary message row at that position. Invariant: the
persisted `messages` for a session is byte-equivalent to the in-memory context
the model currently sees.

---

## Section 3 — Socket transport (replaces sink files)

Each live session `listen()`s on a Unix-domain socket at `.shell3/sock/<id>.sock`
(short numeric session id — macOS caps UDS paths at ~104 bytes, a hard
constraint). On boot a session writes its `pid`, `sock`, and `status=live` into
the liveness registry; on `Close()` it sets `status=dormant` and removes the
socket file.

Delivery is push, not polled: a reporting child `connect()`s to the parent's
socket, writes one JSON `Notification` line, closes. The listening parent reads
it and injects via the existing `Interject` path (same formatting/wake behavior
the sink watcher had). The 250 ms sink poll is gone.

Liveness has two checks, both cheap: registry `status` (authoritative) and
`connect()` result (`ENOENT`/`ECONNREFUSED` ⇒ effectively dormant). `kill(pid,0)`
is available as a tiebreaker against a stale `live` row after an unclean exit.

---

## Section 4 — Report pointer + cascade

**The ephemeral report-chain idea is rejected.** A chain of `{pid, sock}`
captured at spawn is stale by revive time and invisible to a freshly-booted
revived process. Instead:

- At spawn, the parent passes `--parent-session <its-own-session-id>`; the child
  writes it to its `parent_session_id` column. **One pointer, persisted.**
- The ancestor chain is never stored as a list — it is **reconstructed by walking
  `parent_session_id` links** at report time.
- Liveness (`pid`/`sock`/`status`) is resolved fresh from the registry at report
  time, never persisted as part of the chain.

The report algorithm is the one in the Overview. The orphan/silent-drop problem
dissolves because **a subagent self-reports from its own process** by walking the
pointer — it does not depend on the spawning parent's reaper goroutine surviving.
A dead ancestor is simply skipped (its `status=dormant`).

Plain non-subagent `bash_bg` jobs keep today's best-effort parent-goroutine
reporting: a bare `sleep`'s completion is uninteresting once its spawning agent
is gone, so no durability is owed there. Durability applies to *agent*
completion / delegation, which always runs as a `shell3` process that can
self-report.

`bgjobs.Start` no longer injects `SHELL3_NO_SUBAGENTS=1`; the depth gate is
retired in favor of real multi-level support.

---

## Section 5 — Resume / reactivation semantics

`shell3 run --resume <session-id> --prompt "<text>"` = load messages, append the
prompt, run. This single primitive backs both human TUI resume and machine
reactivation.

When a child finishes and its parent is **dormant**, the child **revives** it
(chosen design: durable delegation, not escalate-to-root). Concurrency is handled
by separating the **queue** from the **reviver election**:

1. **Queue (no coordination):** every child that finishes while the parent is
   dormant appends its notification to the parent's SQLite `inbox`. N children →
   N atomic appends.
2. **Reviver election (one winner):** each such child calls
   `Store.ClaimRevive(parent_id)` — an atomic `dormant → reviving` transition.
   Exactly one wins. The winner spawns
   `shell3 run --resume <parent-id> --prompt <drained-inbox-summary>`. Losers
   already enqueued in step 1 and simply walk away.
3. **Drain on boot:** the revived process drains the *entire* inbox into its
   initial context — capturing notifications that arrived during the
   revive-startup window — then transitions `reviving → live` and registers its
   socket. Further deliveries use the normal live socket path.

If the parent is **live**, we socket-deliver instead of reviving (the liveness
check arbitrates: `live` → deliver, `dormant` → claim+revive). The in-memory
`Interject` queue absorbs concurrent live deliveries with no lock.

Revive failure (corrupt session, resume error, claim lost) is **not** silently
dropped: the notification remains in the inbox, and the child escalates one hop
up the pointer chain so the human at root still learns of it. (Revive is the
primary path; root-escalation is the safety net, satisfying "revive, fall back to
escalate" in practice while keeping revive the default.)

---

## Section 6 — TUI resume rendering

On `shell3 --resume <id>`: replay only the **tail** of the conversation — the
last ~3 user turns plus their following assistant/tool output — into the patch
TUI, then a `⟲ resumed conversation <id>` marker. If tail-replay proves fiddly
against the patch renderer, fall back to a bare
`⟲ resuming conversation <id> (<n> messages)` banner with no replay (acceptable
per design owner). The full message list is always loaded into model context
regardless of how much is *rendered*.

---

## Section 7 — Testing & migration

Pre-release: **no data migration**; schema is dropped and recreated.

Tests:

- **Persistence round-trip:** save a conversation containing tool-call and
  tool-result messages → `LoadSessionMessages` → identical `llm.Message` slice
  (roles, content, tool_call_id, tool results intact).
- **Compaction mirror:** after a compaction event, persisted `messages` equals
  the in-memory compacted context; resume loads within-window.
- **Socket deliver:** live parent receives a child's notification via socket and
  Interjects it; concurrent children all land via the queue.
- **Cascade skips dead ancestor:** parent dormant, grandparent live → grandparent
  receives, tagged with originating session.
- **Revive a dormant parent:** child finishes, parent dormant → exactly one
  reviver spawns (claim election), inbox fully drained on boot, parent processes
  the result and reports up its own pointer.
- **Concurrent revive:** N children finish against one dormant parent → one
  process revived, all N notifications delivered, no double-execution.
- **Revive failure → escalate:** forced resume error leaves inbox intact and
  escalates one hop; human at root is notified.
- **Arbitrary depth:** a 3-level spawn tree where every intermediate exits
  before its child finishes → the leaf's result propagates to root via
  successive revivals.

---

## Component summary (what changes)

| Component | Change |
|---|---|
| `cmd/shell3/run.go`, `main.go` | Split root (TUI + `--resume`) from `shell3 run` (`--prompt`/`--resume`/`--parent-session`); drop terminal-mode inference and `--append-sinkfile`/`--no-subagents`. |
| `internal/store` | Rewrite: `messages` (full replay), `sessions` (+`parent_session_id`, liveness cols), `inbox`; load/resume/inbox/claim/liveness API. |
| `internal/chat` | Persist full message stream incl. tool results; mirror compaction into store; session seeding from `InitialMessages`. |
| `pkg/shell3` (`shell3.go`, `runtime.go`, `delegation.go`, `sink.go`) | Replace sink-file watcher with socket listener + liveness registration; report via `parent_session_id` walk; revive-on-dormant; delegation context no longer suppressed for children. |
| `internal/bgjobs` | Drop `SHELL3_NO_SUBAGENTS=1`; subagent self-reports via socket/inbox instead of `--append-sinkfile`. |
| `internal/sink` | Replaced by socket transport + SQLite inbox (module retired or repurposed to the `Notification` type only). |
| `internal/tui` | `RunOnce`/`RunInteractive` accept resume; tail-render on resume; `run` vs interactive dispatch. |

## Open items deferred to the implementation plan

- Exact `Notification` payload schema carried over the socket and stored in
  `inbox` (origin session id, transcript path, preview, kind).
- Whether the liveness registry is a column-set on `sessions` or a separate
  `liveness` table (leaning columns on `sessions`).
- Heartbeat/staleness policy for detecting unclean exits (a `live` row whose pid
  is dead) — minimal `kill(pid,0)` check at report time vs. periodic sweep.
