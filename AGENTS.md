# shell3

Minimal Unix-composable coding agent written in Go.

**Bash-first.** The agent's verbs are `bash` and `edit_file`; everything else is
a file it reads or a command it runs (history is searched with `rg` over
`.shell3_project/runs/**/*.jsonl`; a subagent is a fire-and-forget backgrounded
`shell3` subprocess). A finishing subagent appends one pointer line to
`.shell3_project/inbox.jsonl`; the live host tails it (fsnotify, offset-persisted,
exactly-once) and injects a short pointer notification. Nested subagents use
plain blocking bash — there is no dormant-parent revive. The shell is
**unsafe by default** — the only safety surface is the `shell3.wrap_bash(fn)`
Lua hook (allow/block/rewrite; no approval flow). Skills are `.md` files the
agent reads with `cat` (listed by absolute path in the prompt under `## Skills`
— there is no `skill` tool), and custom tools are declarative bash-command
templates (`shell3.tool{command=...}`, params injected as lowercase env vars
plus a `secrets` list; no Lua `handler`) — the `shell3.bash`/`http`/`urlencode`
helpers are gone. Context is host-managed via a model `compact_at` token
threshold (auto-compaction), not model-driven prune/compact tools.

## IMPORTANT: Do Not Read Credential Files

Secrets and credentials (provider API keys, tool tokens) live in a plain `.env` file beside the active `shell3.lua` (e.g. `~/.shell3/.env`), read from Lua via `shell3.env.secret("KEY")`. Never read, display, or include the contents of any credential file in a response. This applies to all agents, assistants, and automated tools.

- `.env` beside `shell3.lua` (e.g. `~/.shell3/.env`) — provider API keys, base URLs, tool secrets
- any legacy `ai-do-not-read.*` files, if present — treat the same way

## Project Layout

```
cmd/shell3/            entry point (run command)
internal/agentsetup/   shared config assembly (Build → chat.Config) used by every front-end
internal/luacfg/       Lua config loader (shell3.lua → models/agents/tools/skills, wrap_bash/stub_tools) + system-prompt assembly
internal/bootstrap/    first-run global + project setup
internal/scaffold/     embedded starter shell3.lua + .env template
internal/adapter/openai/  OpenAI-compatible LLM adapter
internal/modelproxy/   run_proxy spawner (starts a model's proxy command on activation)
internal/paths/        global (~/.shell3/) + local (.shell3_project/) path resolution; no DB fields
internal/runs/         file-native JSONL store: sessions at .shell3_project/runs/<id>/; inbox watcher (fsnotify)
internal/edittool/     edit_file tool implementation (Go port of opencode's str-replace)
internal/bgjobs/       background job tracking (file-based, fire-and-forget; logs under .shell3_project/runs/jobs/)
internal/notify/       Notification type (bg_done / agent_done pointers) shared by inbox watcher + chat
internal/tui/          terminal UI (interactive + headless once)
internal/patchapp,patchmd,patchtui/  patch-style TUI components
internal/chat/         conversation loop, tools, events, JSONL audit sink
internal/llm/          Provider/Streamer interfaces, request params, types (+ fakellm)
internal/persona/      runtime carrier for an agent's prompt/tools/params (data only)
internal/applog/       rotating app log

pkg/shell3/            embeddable library API (the only public package)
```

## Development

```bash
make build      # go build ./cmd/shell3
make install    # go install ./cmd/shell3
go test ./...   # run all tests
```
