# bash-first — handoff to finish the port

Paste this file's path (or its contents) into a fresh session to continue. It is
self-contained: a new session can pick up cold from here.

## TL;DR of state

Branch **`feat/bash-first`** off `main`. The "bash-first" refactor (design:
`docs/dev/superpowers/specs/2026-06-11-bash-first-design.md`) is **8/9 phases
done, suite fully green, NOT merged.** The one remaining build phase is **§7
(command-backed prompt/skill bodies)**, plus a human live-review gate and a few
loose ends. The branch build is already `make install`ed as the user's `shell3`.

Verify you're oriented:
```
git -C /Users/weatherjean/CODE/AGENTS/shell3 log --oneline main..feat/bash-first
go build ./... && go test ./...      # must be green
```

## What "bash-first" did (so you understand the world you're in)

shell3 is a minimal Unix-composable coding agent (Go). The branch collapsed a
12-tool agent down to **5 tools** (`bash`, `edit_file`, `bash_bg`, `read_media`,
`shell_interactive`) and pushed everything else onto bash + files + Lua:

- **Sink** (`internal/sink`, watcher in `pkg/shell3/sink.go`): a per-session
  JSONL notification channel. Background jobs / subagents append short *pointer*
  lines; the host watcher injects them as system-reminders + wakes the session.
  The agent reads the heavy output (transcripts, logs) itself with bash.
- **Subagents = backgrounded `shell3`**: spawned via `bash_bg` running
  `shell3 --agent X --out <transcript> --append-sinkfile <sink> --id <id>
  --no-subagents`. The child self-reports an `agent_done` notification. The
  in-process `spawn_agent`/`list_agents`/`subRegistry` are gone. Cron `Dispatch`
  execs a subprocess and emits an operator `Notice` (NOT via the sink).
- **History** = a read-only-SQLite `history` bash skill (WAL on); the store now
  always persists; `history_get`/`history_search` tools deleted.
- **Auto-compaction**: `compact_at` token threshold on the model config; host
  interrupts → summarizes → continues. `prune_tool_result`/`compact_history`
  tools deleted. Fail-safe: a failed compaction never blocks the turn.
- **Guard + approval REMOVED entirely** (engine, Telegram Approve/Deny, TUI y/N).
  Replaced by `shell3.wrap_bash(fn)` — allow/rewrite/block, **no ask**, **UNSAFE
  BY DEFAULT**. (edit_file is unguarded now — accepted.)
- **`shell3.stub_tools(map)`**: name→message redirects for hallucinated tools.
- **Bash ANSI colors** forwarded to the TUI.
- New `shell3 run` flags: `--agent`, `--append-sinkfile`, `--id`, `--no-subagents`.

Decisions made on the user's behalf (already in the design doc §3 "Resolved
decisions"; the user is reviewing these): single notification per spawn via
`bash_bg notify_on_exit=false`; delegation guidance injected as a `## Delegation`
system-prompt block by `pkg/shell3`; cron keeps operator-Notice semantics.

## REMAINING WORK

### 1. Phase 9 — command-backed prompt/skill bodies (§7). THE last build phase.

Today skills are `body = [[...]]` and agents/subagents are `prompt = [[...]]`
inline Lua strings. Add the ability to source a body/prompt from a shell command
(so prompts can live as plain `.md` files the user edits, not Lua heredocs).

**Lua API:**
- `shell3.skill({ name, description, body_cmd = "cat skills/history.md" })`
- `shell3.agent({ ..., prompt_cmd = "..." })` and `shell3.subagent({ ..., prompt_cmd = "..." })`
- Exactly one of `body`/`body_cmd` (skills) and `prompt`/`prompt_cmd`
  (agents/subagents) — raise a Lua error if both or neither.

**Implementation (suggested):**
- `internal/luacfg/register.go`: add `body_cmd` to `skillKeys`, `prompt_cmd` to
  `agentKeys` + `subagentKeys`; parse into new `BodyCmd`/`PromptCmd` fields on
  `Skill`/`Agent`/`Subagent` (`internal/luacfg/luacfg.go`).
- Resolve AFTER `DoFile`, in a dedicated pass in `internal/luacfg/luacfg.go`
  `Load` (and the reload path): for each skill/agent/subagent with a `*_cmd`, run
  it and use trimmed stdout as `Body`/`Prompt`. Reuse the existing bash exec
  machinery (see `luaBash` in `lua_bash.go` / `shell3.bash`).
- **cwd = the config file's directory** (Load already knows it —
  `filepath.Dir(configPath)`, same dir as `.env`/`lib`), so `cat skills/x.md`
  resolves relative to the config. Document this.
- **Fail CLOSED at load**: a `*_cmd` that errors or returns empty makes
  `Load`/reload FAIL with a clear error (a broken prompt must be caught at
  load/reload, like the existing cross-reference validation). Never silently run
  with an empty prompt.
- Resolved **once per load/reload** → prompt caching preserved. Unsafe-by-default
  already covers "running bash at load is a code-exec surface."
- Scaffold (`internal/scaffold/defaults/base/shell3.lua.tmpl`): add a commented
  `prompt_cmd`/`body_cmd` example.

**Tests:** skill `body_cmd` resolves from stdout; `prompt_cmd` on agent +
subagent; exactly-one-of validation (both → error, neither → error); failing
command → `Load` error; cwd-is-config-dir (a relative `cat` works); reload
re-resolves. Keep existing tests green.

**Done = ** `go build ./...` exit 0; `go test ./...` green; `gofmt -l .` empty;
`go vet ./...` clean. Commit on `feat/bash-first`:
`feat(bash-first): command-backed prompt/skill bodies (body_cmd/prompt_cmd)`
with trailer `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
Then update design doc §7 from "optional, later commit" to implemented.

### 2. Live review gate (with the user — do NOT merge before this)

The branch is build+unit-tested only; nothing exercised live. The user wants a
**TUI + Telegram run-through together** before merge:
- TUI: bash with color; `edit_file`; history-skill query; `bash_bg` job →
  watch the sink notification land; spawn a subagent end-to-end; auto-compaction
  at a low `compact_at`; confirm the `## Delegation` text + `wrap_bash`
  unsafe-by-default messaging read right.
- Telegram (`shell3 telegram`): `/stop` killing a running subagent (now a bg
  process); a cron Notice; confirm approval prompts are gone cleanly.
- Offer to write a step-by-step manual test script; the user drives, you fix.

### 3. Loose ends

- **User's personal skill files** `~/.shell3/lib/skills/*.lua` (e.g.
  `self_evolve`, `scheduling_jobs`) may still describe the OLD gates/guards to
  the agent — not load-breaking, but `self_evolve` could lead the agent to write
  now-invalid keys (`history`/`prune`/`compact`/`on_tool_call`). Offer to scan +
  update them. (These are OUTSIDE the repo, in `~/.shell3/`.)
- The user's live config was amended for the new schema; backup at
  `~/.shell3/shell3.lua.pre-bashfirst.bak`. The branch binary is installed.

### 4. Merge (only after the live review + user OK)

`finishing-a-development-branch` skill. Likely a fast-forward / PR to `main`,
then `make install` from `main`. Confirm with the user first.

## Conventions & gotchas (IMPORTANT)

- **Implementation via opus subagents.** The user wants build work done by
  subagents on **opus** (`Agent` tool with `model: "opus"`), then the orchestrator
  verifies. Each phase: precise spec → subagent implements + self-runs
  build/test → subagent commits → you verify ground truth before the next.
- **Stale-diagnostics gotcha:** the IDE/`<new-diagnostics>` blocks show mid-edit
  snapshots and routinely reference *deleted* files / intermediate broken states.
  **`go build ./... && go test ./...` at HEAD is the only ground truth** — always
  re-run it; don't trust the diagnostic panel.
- **Never read the `.env`** beside `shell3.lua` (CLAUDE.md rule). `shell3.lua`
  itself is fine to read/edit.
- **Commit, don't push/merge** unless the user asks. Trailer on every commit:
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
- Match surrounding code: thorough doc comments on exported symbols, "explain
  why" on tricky/concurrency bits.
- Key files: `internal/luacfg/` (config/Lua), `internal/chat/turn.go` (turn
  loop), `internal/agentsetup/agentsetup.go` (assembly — `environmentSection`
  injects runtime paths), `pkg/shell3/` (Runtime/Session, sink watcher,
  dispatch), `internal/sink/`, `cmd/shell3/run.go` (flags).
- Design doc: `docs/dev/superpowers/specs/2026-06-11-bash-first-design.md`.
- Branch commits so far: `git log --oneline main..feat/bash-first`.
</content>
