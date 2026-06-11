# shell3

Minimal Unix-composable coding agent written in Go.

**Bash-first.** The agent's verbs are `bash` and `edit_file`; everything else is
a file it reads or a command it runs (history is a read-only SQLite query, a
subagent is a backgrounded `shell3` subprocess). Background work and subagents
report completion through a per-session JSONL **sink** that the host watcher
turns into short pointer notifications. The shell is **unsafe by default** — the
only safety surface is the `shell3.wrap_bash(fn)` Lua hook (allow/block/rewrite;
no approval flow). Context is host-managed via a model `compact_at` token
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
internal/paths/        global + local path resolution
internal/ref/          project UUID (.shell3/.ref)
internal/store/        SQLite history + sessions (WAL; read-only-queryable via the `history` bash skill)
internal/edittool/     edit_file tool implementation (Go port of opencode's str-replace)
internal/bgjobs/       background job tracking (.shell3/bg.json); reaper writes a sink notification on exit
internal/sink/         per-session JSONL notification channel (bg_done / agent_done pointers)
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
