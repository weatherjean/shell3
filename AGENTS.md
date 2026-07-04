# shell3

Minimal Unix-composable coding agent written in Go.

**Bash-first.** The agent's verbs are `bash`, `read`, `list_files`, and
`edit_file` (plus `read_media` — attach an image/audio file so a multimodal
model can perceive it — when `tools = { media = true }`); text files are read
with the `read` tool (paged, capped at 2000
lines / 50 KB) and directories with `list_files` (an indented tree; `path`,
`depth` default 2, `ignore` globs, no auto-filtering, 1000-entry cap) — `read` +
`list_files` alone make a no-bash read-only agent; everything else is a command
it runs (history is searched with `rg` over
`.shell3_project/runs/**/*.jsonl`). A **subagent** is an **in-process background
job** spawned via the `task` tool (`{subagent_type, prompt, description}`; returns
immediately); the runtime (`pkg/shell3` jobManager) runs it as a child-session
goroutine under a concurrency cap (`shell3.background{max_concurrent=N}`, default
8) and, on completion, **wakes the parent with a capped result summary** injected
into context — no subprocess, no inbox file, no fsnotify. `bash_bg` is a background
shell command on the same runtime (the agent is notified on a later turn; there is
no pid / log path to poll). Delegation is **single-level** (a subagent is not
given the `task` tool); max nesting depth is `shell3.subagents{max_depth=N}`
(default 3). Active tasks are managed with `task_list`, `task_status <id>`, and
`task_cancel <id>` (ids like `sub1`/`bg1`); these three plus `task` itself — four
tools in all — are only advertised when the agent sets `delegation = true` and
`tools.subagents = { … }` (`bash_bg` is gated separately, by
`tools = { bash_bg = true }`).
The TUI `:background` modal lists running + finished jobs live; the footer `bg: N`
pill counts running jobs. Both the TUI modal and the ACP front-end consume a single
unified job-progress stream (`rt.JobEvents()` / `Session.JobEvents()`): the TUI
live-tails it, and ACP renders each job as its own live-updating tool-call card
(incremental chunks → `completed`). The shell is
**unsafe by default**; the single opt-in hook that gates it is
`shell3.on_tool_call(fn)` — a chainable handler that runs before **every** tool with
the real `t.name` (`bash`/`bash_bg`/`shell_interactive`/`read`/`list_files`/`edit_file`/custom;
`t.command` is the bash text for the three bash tools, nil otherwise) and returns a
verdict: `nil` (run) / `{command=...}` (rewrite, continue chain — bash tools only) /
`{argv={...}}` (runner-swap, terminal — `bash`/`bash_bg` only; fails closed for
`shell_interactive` and non-bash) / `{block=true, reason=...}` (block) /
`{ask="prompt", reason=...}` (prompt a human; allow→run, decline/headless→block).
Denylists are written with `shell3.regex(pat):match(s)` (Go RE2; compiled at load;
use `(?s)` so `.*` spans newlines; match the whole command so chaining can't hide a
flagged fragment) — guard on `t.name` before matching `t.command` (nil for non-bash).
`shell3.on_tool_result(fn)` can rewrite a tool's output (e.g. redact secrets). File
I/O for `read` and `edit_file` goes through a pluggable `internal/fsx.FileSystem`
backend (`fsx.OS` by default — direct disk); the ACP front-end swaps in an
editor-buffer backend when the client advertises the `fs` capability (reads see
unsaved buffers, writes flow through the editor), falling back to OS otherwise —
`bash` always hits disk directly. The
scaffold's example gate ships **commented out** — a fresh config gates nothing —
and, once enabled, covers only the bash family, so `read`/`list_files` run
ungated (a config choice, not a hardcoded exemption). Skills
are `.md` files the agent reads with `cat` (listed by absolute path in the prompt
under `## Skills` — there is no `skill` tool), and custom tools are declarative
bash-command templates (`shell3.tool{command=...}`, params injected as lowercase
env vars plus a `secrets` list; no Lua `handler`) — the
`shell3.bash`/`http`/`urlencode` helpers are gone. Context is host-managed via
two token thresholds: `prune_at` cheaply stubs old tool outputs (no LLM call),
and `compact_at` triggers tail-preserving compaction — summarizing the head while
keeping recent turns verbatim. The `prune_at` and `keep_recent` knobs are
optional, defaulting to fractions of `compact_at`; no model-driven prune/compact
tools.

## IMPORTANT: Do Not Read Credential Files

Secrets and credentials (provider API keys, tool tokens) live in a plain `.env` file beside the active `shell3.lua` (e.g. `~/.shell3/.env`), read from Lua via `shell3.env.secret("KEY")`. Never read, display, or include the contents of any credential file in a response. This applies to all agents, assistants, and automated tools.

- `.env` beside `shell3.lua` (e.g. `~/.shell3/.env`) — provider API keys, base URLs, tool secrets
- any legacy `ai-do-not-read.*` files, if present — treat the same way

## Project Layout

```
cmd/shell3/            cobra command tree: root (interactive TUI) + run/boot/read-session/acp subcommands
internal/agentsetup/   shared config assembly (Build → chat.Config) used by every front-end
internal/luacfg/       Lua config loader (shell3.lua → models/agents/tools/skills, on_tool_call/stub_tools) + system-prompt assembly
internal/bootstrap/    first-run global + project setup
internal/scaffold/     embedded starter shell3.lua + .env template
internal/adapter/openai/  OpenAI-compatible LLM adapter
internal/modelproxy/   run_proxy spawner (starts a model's proxy command on activation)
internal/paths/        global (~/.shell3/) + local (.shell3_project/) path resolution; no DB fields
internal/runs/         file-native JSONL store: sessions at .shell3_project/runs/<id>/
internal/edittool/     edit_file tool implementation (Go port of opencode's str-replace)
internal/fsx/          pluggable FileSystem interface (fsx.OS = direct disk; ACP swaps in editor-buffer backend)
internal/acp/          ACP (Agent Client Protocol) front-end: stdio JSON-RPC server for editors + bridges
internal/notify/       Notification type (bg_done / agent_done) shared by job runtime + chat
internal/tui/          full-screen vim-modal terminal UI (interactive + headless once)
internal/chat/         conversation loop, tools, events, JSONL audit sink
internal/llm/          Provider/Streamer interfaces, request params, types (+ fakellm)
internal/persona/      runtime carrier for an agent's prompt/tools/params (data only)
internal/strutil/      rune-safe string truncation helpers (byte-cap + rune-count) shared by runtime and front-ends
internal/applog/       rotating app log

pkg/shell3/            embeddable library API (the only public package); jobs.go hosts the in-process job runtime (subagents + bash_bg)
```

## Development

```bash
make build      # go build ./cmd/shell3
make install    # go install ./cmd/shell3
go test ./...   # run all tests
```

## AI artifacts are not committed

Design specs, implementation plans, and other AI-generated working notes are
**gitignored, never committed** — `docs/dev/*` (except its `README.md`),
`docs/superpowers/`, `docs/dev/superpowers/`, and `ai-do-not-read.*`. Keep them
local; the repo carries only shipped documentation (top-level `README.md`,
`docs/`, `docs/cookbook/`). If you generate a design/plan doc, leave it in
`docs/dev/` where the ignore rule keeps it out of commits.
