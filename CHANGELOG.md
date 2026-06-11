# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Until v1.0.0, minor versions may contain breaking changes.

## [Unreleased]

### Bash-first refactor

A branch-wide collapse of the tool surface onto `bash` + `edit_file`. The agent
now acts through the shell; everything else is a file it reads or a command it
runs, coordinated through one small per-session notification channel (the
**sink**). See `docs/dev/superpowers/specs/2026-06-11-bash-first-design.md`.

#### Added

- **Sink notification channel** (`internal/sink`): a per-session append-only
  JSONL file carrying short *pointer* notifications (`bg_done`, `agent_done`),
  never full payloads. A host watcher in `pkg/shell3.Session` tails it by byte
  offset and, for each new line, injects a one-line pointer into the agent's
  next turn (and wakes the session if idle) — the agent `cat`s the referenced
  transcript/log itself, keeping context small. Cleaned up on `Close`.
- **`bash_bg` reports to the sink:** the reaper writes a `bg_done` notification
  (id / exit / log path / cmd) on child exit. New `notify_on_exit` arg (default
  `true`); subagent spawns pass `false` so the only notification is the child's
  own `agent_done`.
- **Subagents = a backgrounded `shell3`.** A subagent is now a `shell3`
  subprocess launched via `bash_bg` that self-reports an `agent_done` pointer
  (id / status / transcript / ≤200-char preview) to the parent's sink. The host
  injects a "## Delegation" prompt fragment at session start listing declared
  subagents and the exact templated `bash_bg` spawn command. New `shell3 run`
  flags: `--agent <name>`, `--append-sinkfile <path>`, `--id <id>`, and
  `--no-subagents` (suppresses the delegation fragment so children can't recurse
  — depth-1). `shell3.subagent{}` declarations are kept (they name the headless
  agent configs); only the spawn *mechanism* changed.
- **Host-enforced auto-compaction:** new `compact_at` token threshold on the
  model config. Before a user turn, if the last prompt crossed the threshold,
  shell3 interrupts → compacts the history into the structured summary
  (summary / important_files / references / skills / next_steps) → continues
  against the summary. No model-driven prune/compact tools.
- **History as a bash skill:** a `history` skill documenting the SQLite schema +
  canonical read-only queries (`sqlite3 'file:<db>?mode=ro' …`, FTS5 search). The
  store now enables **WAL** for file-backed DBs so a cross-process `sqlite3`
  reader doesn't contend with the writer. The store still always persists
  history; only the in-tool access changed.
- **`shell3.wrap_bash(fn)`:** the single Lua hook every `bash`/`bash_bg` command
  passes through before execution. `fn(cmd)` returns the command to run
  (optionally rewritten) or `nil`/`false` + reason to block. Pure
  allow/block/rewrite — no `ask`. The scaffold ships a loud no-op.
- **`shell3.stub_tools(map)`:** name-only stub tools that return a redirect
  string (e.g. `read_file` → "Use bash: cat <path>") instead of erroring, so
  models trained on other harnesses self-correct toward bash/edit_file.
- **Bash ANSI color forwarding:** `bash`/`bash_bg` output is passed to the TUI
  unstyled (no dim/strip) so SGR colors survive; model-facing bytes unchanged.

#### Changed (BREAKING)

- **Removed tools:** `spawn_agent`, `list_agents`, `history_get`,
  `history_search`, `prune_tool_result`, `compact_history`. The agent's
  built-in tools are now `bash`, `edit_file`, `bash_bg`, `read_media`,
  `shell_interactive` (5, down from 11).
- **Guard engine removed.** `on_tool_call` guard chains, the `Decision`/`ask`
  verdicts, and the `GuardEntry`/`Guard` fields on agents/subagents are gone.
  Replaced by `shell3.wrap_bash` — **the shell is now unsafe by default** (full,
  unrestricted bash) unless you wire up a `wrap_bash` hook.
- **Human approval flow removed end-to-end.** The TUI `[approve? y/N]` prompt,
  the Telegram Approve/Deny callback, and the `Approve`/`SetApprover` host
  callbacks (pkg/shell3 + chat `TurnConfig`) are deleted along with the guard
  engine. There is no `ask` verdict and no approver.
- **Config keys:** added model `compact_at`; removed the agent/subagent tool
  gates `history` / `prune` / `compact`; removed the `on_tool_call` agent key
  and the in-process subagent-tool injection. New top-level Lua API
  `shell3.wrap_bash` / `shell3.stub_tools`. Configs using the removed keys now
  fail to load (strict `checkKeys`).
- **Cron dispatch** now execs a tracked `shell3 --agent X --out <t>` subprocess
  (joined by `Runtime.Close`) and emits an operator `Notice` — it does not route
  through the sink watcher. The `cron.Dispatcher` interface is preserved.
- **`/stop`** no longer calls `CancelSubagents()`: model-spawned subagents are
  bg jobs, already killed by `bgjobs.KillAll`.

#### Removed

- `internal/chat/handler_prune.go`, `handler_store.go`, the in-process
  `subRegistry` / `Session.spawn` / `deliverSubagentResult` / `CancelSubagents` /
  `subCtx` machinery, `pkg/shell3/subagents.go`, the scaffold `guards.lua` and
  all `on_tool_call` wiring, the Telegram approver, and the patchapp approval UI.

### Added

- `browser` skill: drive a real, headed, cross-platform Chrome via `puppeteer-core`
  over `bash` (open/eval/click/type/wait/screenshot/pdf), shipped in the scaffold
  (`lib/browser/` + `lib/skills/browser.lua`). Each action is a bounded command —
  no long-lived server.
- Telegram-first setup: `shell3 boot --telegram` scaffolds a dedicated
  `~/.shell3/telegram/` host config (`shell3.lua` + its own `.env` + a
  Telegram-tuned agent prompt with Communication/Autonomy sections, the
  `explorer` subagent, and the self-evolve + scheduling-jobs skills). The bot
  token (`TELEGRAM_BOT_TOKEN`) is written to `.env` only (0600, never echoed);
  the chat id, dashboard, and workdir go in the lua. The Chrome DevTools MCP is
  opt-in (default off) via `--chrome` or a `[y/N]` bootstrap prompt — only then
  is the `chrome` MCP server declared and granted to the agent. `shell3 telegram`
  now resolves its config telegram-dir-first
  (`--config → ~/.shell3/telegram → ~/.shell3 → ./shell3.lua`), the opposite of
  the generic resolver, so an existing `~/.shell3/shell3.lua` keeps working until
  a telegram config is created. No migration code (fresh scaffold); the generic
  `boot` path is unchanged.
- Config hot reload (`/reload` bot command + `reload` agent tool): re-reads
  `shell3.lua` and applies the new configuration without restarting the process.
  Validate-first: a bad edit leaves the running config untouched and returns the
  error — the bot can never be bricked by a failed reload. Applied only at an
  idle boundary (full rebuild after the current turn ends). Conversation history
  is preserved across reload; active agent and `/set` params are best-effort
  restored when they still exist in the new config. MCP servers and model
  proxies restart on reload (brief pause); agents, models, tools, skills, and
  cron apply cleanly.

  **Intentional non-goals:** `fsnotify` auto-reload on file save is deliberately
  excluded — reload is an explicit act, never an implicit side-effect. Carrying
  live MCP tool-call state across reload is deferred as a future optimization.
- Scheduled dispatch (`shell3.cron{}`): cron-scheduled jobs that run on the
  always-on `shell3 telegram` host. Each job dispatches an isolated depth-1
  subagent (via the new `pkg/shell3.Session.Dispatch`) whose result reports
  back into the main session — a per-job `notify` policy (default `true`)
  governs the chat push: `notify=false` is a quiet background job whose
  success is transcript/dashboard-only, while errors always break the
  silence. Jobs are declared in `shell3.lua` (`{ name, schedule, agent,
  prompt, workdir, notify }`), schedules parsed by the maintained
  `github.com/robfig/cron/v3`; agent references are validated at config load.
  A dashboard Cron tab and a `/run <name>` bot command (manual fire) round it
  out.

  **v1 limitations:** in-process `robfig/cron` only (no distributed/persistent
  scheduling); no catch-up for fires missed while the host was down; jobs are
  re-armed from config on each restart (edit config + restart to change them);
  overlapping fires of the same job are allowed (each tick is a fresh
  subagent).
- Personal Telegram bot front-end (`shell3 telegram`): a single-chat bot over
  `pkg/shell3.Runtime` with a chat-id allowlist. Inbound text drives one
  persistent session (idle → `SendParts`, busy → `Interject`); replies are
  chunked to Telegram's 4096-byte limit. Image attachments become `shell3.Part`
  inputs. Tool "ask" guards surface as inline Approve/Deny buttons with a
  timeout that fails closed (deny). Subagent/cron results are pushed
  unprompted via the wake bus. Commands: `/clear /agent /agents /set /rollback
  /stop /dash`. Configured by a `shell3.telegram{ token, chat_id, dashboard }`
  block in `shell3.lua` (token from `.env` via `shell3.env.secret`); wraps the
  maintained `github.com/go-telegram/bot` library.
- Telegram Mini App dashboard: a read-only `net/http` server rendering the
  session's history, authenticated with Telegram `initData`
  (`github.com/telegram-mini-apps/init-data-golang`) bound to the configured
  chat id. Vanilla HTML/JS page (no build step), themed to the Telegram client.
  Intended to be exposed over Tailscale (`tailscale serve`); exposure is
  operator-configured.

  **v1 limitations:** Telegram voice messages (OGG/Opus) are dropped — the
  engine's audio loader accepts wav/mp3 only; OGG→wav transcoding is a future
  follow-up. The dashboard **polls** `/api/history` (every 4s) rather than
  streaming live updates; `/api/stream` is a heartbeat only, because
  `Runtime.Events()` is a single-consumer channel owned by the bot — true SSE
  fan-out is deferred. Tailscale exposure is not automated; the operator runs
  `tailscale serve`/`funnel` themselves.
- `shell3.telegram{}` Lua config block, parsed by `luacfg` and surfaced as
  `Runtime.Telegram()` (re-exported `TelegramConfig`/`DashboardConfig`).
- `pkg/shell3.Runtime`: one shared build (config, store, MCP, log) hosting
  multiple named sessions (`Runtime.Session(SessionOpts{Name: "tg:1234", …})`),
  each with its own agent, per-session workdir, headless flag, and audit log.
  `Start`/`Run` are now thin single-session wrappers over a Runtime.
- Mid-turn steering and the wake bus: `Session.Interject(text, parts…)` queues
  a message from any goroutine and never fails — injected into a running turn at
  the next round boundary as a system-reminder that the user sent input, or queued and woken
  when idle. An inbox gaining an item while idle emits a `Wake` on the new
  `Runtime.Events() <-chan HostEvent` bus (or `Session.WakeEvents()` for a
  single-session host); the host reacts with `Session.RunQueued(ctx)`, a turn
  seeded from the queued inbox items. `Send`/`SendParts` stay the strict
  single-turn path (`ErrBusy`). In the TUI you can type while the agent works
  and press Enter to steer.
- Tool approval: Lua guards can return `{ action = "ask" }` to suspend a tool
  call for human approval. Hosts answer via `Spec.Approve` /
  `SessionOpts.Approve` / `Session.SetApprover` with an `ApprovalRequest`
  (Telegram buttons, webui dialogs); the TUI shows an inline `[approve? y/N]`
  prompt. No approver registered → fail closed. Requests and verdicts are
  recorded in the audit JSONL.
- Inbound media: `Session.SendParts` starts a turn with image/audio
  attachments, and `Interject` accepts the same parts for mid-turn delivery.
  `Part{Kind, Path, Data, MIME}` loads from disk or straight from in-memory
  bytes (Telegram photos and voice notes never touch disk), riding the same
  multimodal plumbing and size caps as `read_media` (10 MB images, 25 MB
  audio). Invalid SendParts attachments reject the turn with a single Error
  event; invalid Interject attachments are dropped with a bracketed note.
- Subagents: an explicit registry of delegatable specialists. Declare one with
  `shell3.subagent{name, description, …}` (the `description` is the model-facing
  "when to use") and list them per-agent via `tools = { subagents = { … } }`.
  That agent gets one `spawn_agent(task, subagent, workdir?)` tool whose
  `subagent` parameter is an enum of the registered names; it runs the chosen
  subagent's own config as a headless `sub:<id>` session whose result posts to
  the spawning session's inbox — injected mid-turn if the parent is still
  working, or delivered as a `Wake` if it is idle. `list_agents()` returns a
  running/finished snapshot. Depth-limited to 1 (subagents get no spawn tools
  and may not declare their own); each writes its own audit JSONL under
  `.shell3/agents/`. The TUI auto-runs the wake turn when idle and renders a
  finished subagent as a dim notice.
- `shell3 boot` interactive onboarding: writes a split-file base config
  (`shell3.lua` + `lib/` modules) and merges secrets into `~/.shell3/.env`.
- Embeddable library API at `pkg/shell3`: one-shot `Run`, persistent
  multi-turn `Session` (Send/Clear/Rollback/SwitchAgent/Prune/Snapshot/
  History), streaming typed events.
- Multi-agent configs: declare several agents in `shell3.lua`, switch with
  Tab or `/agent` keeping conversation history.
- Lua-defined custom tools, skills, and `on_tool_call` guard chains.
- MCP server support (stdio transport) with per-agent tool selection.
- Headless mode with `--out` JSONL audit logs for pipelines.
- `run_proxy`: auto-start a local proxy/shim command on first model use.
- Runtime enforcement of the session single-turn contract (`ErrBusy`).
- CI (gofmt/vet/race tests on Linux+macOS) and goreleaser release builds.

### Removed

- MCP support, entirely: the `internal/mcp` package, the `shell3.mcp()` Lua
  builtin, `MCPServer`/`MCPServers`/`MCPServerNames`, MCP tool dispatch, the MCP
  manager in agent setup, the reload MCP-restart path, and the `boot --telegram
  --chrome` flag. Browser automation is now the `browser` skill (above). Configs
  that still call `shell3.mcp{}` fail loudly at load.

### Fixed

- `/stop` now cancels the in-flight turn, kills its synchronous `bash`/`node`
  children and `bash_bg` jobs, and cancels in-flight subagents — and works
  mid-turn: Telegram turns now run on their own goroutine so the message loop
  stays responsive and `/stop` lands while a turn is still running (previously
  the loop was wedged inside the turn and `/stop` was never read). Persistent
  browser windows and model proxies are intentionally left running.

[Unreleased]: https://github.com/weatherjean/shell3/commits/main
