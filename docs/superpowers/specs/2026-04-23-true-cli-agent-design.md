# True CLI Agent — Design Spec

**Date:** 2026-04-23  
**Status:** Draft

---

## Overview

A minimal Go coding agent that follows Unix philosophy strictly. No TUI framework. No HTTP server. No extension SDK. Just a binary that reads input, calls an LLM, executes tools, and writes output. Everything else — UI, adapters, hooks — is external processes connected via pipes or `--out`.

Inspired by pi-mono but radically simpler: where pi embeds UI, adapters, and extension runtime inside the agent, this design pushes all of that outside.

---

## Goals

- Single Go binary, zero UI dependencies
- OpenAI-compatible API (works with Ollama, z.ai, Codex Plus, any compatible endpoint)
- Lifecycle hooks = shell commands called by the agent (JSON in/out via stdin/stdout)
- Output adapters = anything that reads JSONL from stdin
- Searchable long-term memory via SQLite FTS5
- Personalities = named YAML configs (system prompt + tools + skills)
- Skills = markdown files injected into system prompt (compatible with pi/Claude Code format)
- Per-project config in `.shell3/` — credentials only stored globally in `~/.shell3/`
- `agent init` scaffolds project; `agent auth` sets up credentials

---

## Architecture

```
stdin / --input  →  agent  →  stdout (plain text OR JSONL via --stream)
                      │
                      ├── bash tool
                      ├── memory_search / memory_store tools (SQLite)
                      ├── lifecycle hooks (shell commands, JSON via pipe)
                      └── --out CMD (agent spawns CMD, pipes output to its stdin)
```

Each conversation = one process. No persistent server. No shared state except `--memory-db` (SQLite) and `--history-md` (markdown file).

---

## CLI Interface

```bash
# One-shot
agent "fix the auth bug"

# Interactive (reads stdin lines, one turn per line)
agent

# With personality
agent "review this PR" --personality reviewer

# With persistent memory
agent "what did we decide about auth?" --memory-db ./proj.db

# With conversation history
agent "continue where we left off" --history-md ./session.md

# Both
agent "next task" --history-md ./session.md --memory-db ./proj.db

# Stream JSONL events to stdout (for TUI, web UI, etc.)
agent "fix bug" --stream

# Push final plain-text answer to a command
agent "fix bug" --out "notify-me"

# Stream JSONL events to a command (TUI adapter, Telegram adapter, etc.)
agent "fix bug" --stream --out "my-tui"

# Custom model/endpoint
agent "fix bug" --model llama3.2 --base-url http://localhost:11434/v1
```

### Flag Reference

| Flag | Description |
|------|-------------|
| `--personality NAME` | Load named personality from config |
| `--config PATH` | Load personality from YAML file |
| `--model MODEL` | Override model (e.g. `gpt-4o`, `llama3.2`) |
| `--base-url URL` | OpenAI-compatible endpoint base URL |
| `--api-key KEY` | Override API key for this run (bypasses ~/.shell3/credentials.yaml) |
| `--memory-db PATH` | SQLite memory database path |
| `--history-md PATH` | Markdown session history path |
| `--stream` | Emit JSONL events instead of buffered plain text |
| `--out CMD` | Spawn CMD and pipe output to its stdin |
| `--skills PATH,...` | Additional skill file paths |
| `--no-memory-tools` | Disable memory_search/memory_store tools |
| `--no-bash` | Disable bash tool (reasoning-only mode) |

---

## Output Modes

### Default (no flags)
Plain text to stdout. Human-readable. Thinking and tool calls printed inline.

```
thinking...
[bash] ls -la
total 32
drwxr-xr-x  5 user  staff  160 Apr 23 src
[bash done]
Here are your source files. The main entry point is src/main.go...
```

### `--stream`
JSONL event stream to stdout. One event per line. Consumed by adapters and TUIs.

```jsonl
{"type":"thinking","text":"Let me check the files first."}
{"type":"tool_call","tool":"bash","params":{"command":"ls -la"}}
{"type":"tool_result","tool":"bash","exit_code":0,"output":"total 32\ndrwxr-xr-x..."}
{"type":"token","text":"Here are your source files."}
{"type":"token","text":" The main entry point is src/main.go."}
{"type":"done","text":"Here are your source files. The main entry point is src/main.go."}
```

### `--out CMD`
Agent spawns CMD as a subprocess. Without `--stream`: pipes plain-text final answer to CMD stdin. With `--stream`: pipes JSONL event stream to CMD stdin.

```bash
# Telegram adapter: receives final answer, sends to Telegram
agent "deploy status?" --out "telegram-send --chat 123"

# TUI: receives event stream, renders it
agent "fix bug" --stream --out "agent-tui"
```

---

## JSONL Event Types

```go
type EventType string

const (
    EventThinking   EventType = "thinking"    // LLM reasoning block
    EventToken      EventType = "token"       // streaming text delta
    EventToolCall   EventType = "tool_call"   // tool invoked
    EventToolResult EventType = "tool_result" // tool completed
    EventDone       EventType = "done"        // final complete answer
    EventError      EventType = "error"       // fatal error
)
```

---

## Tools

### bash
Executes shell commands in the working directory.

```json
{"tool": "bash", "params": {"command": "go test ./..."}}
```

Output is streamed to the event stream as `tool_result`. Configurable timeout (default: 30s). Working directory = `$PWD` unless overridden.

### memory_search
Full-text search over SQLite FTS5 memory store. Only available when `--memory-db` is set.

```json
{"tool": "memory_search", "params": {"query": "auth decisions"}}
```

Returns top 5 matching entries by BM25 rank.

### memory_store
Upsert a key-value entry into SQLite memory. Only available when `--memory-db` is set.

```json
{"tool": "memory_store", "params": {"key": "auth-decision-2026-04", "value": "We use JWT with 1h expiry, refresh tokens in Redis."}}
```

---

## SQLite Memory Schema

```sql
CREATE VIRTUAL TABLE memories USING fts5(
    key,
    value,
    created_at UNINDEXED,
    updated_at UNINDEXED
);
```

FTS5 enables full-text search across both key and value. `memory_store` does upsert by key. `memory_search` runs FTS5 MATCH query, returns top 5 results by BM25 rank.

---

## Session History (`--history-md`)

Conversation history stored as human-readable markdown. Each turn is a section:

```markdown
# Session: 2026-04-23T12:34:56Z

## User
fix the auth bug

## Assistant
I'll look at the auth code first.

[bash] cat src/auth.go
...output...
[bash done]

Found the issue on line 42. The token expiry check uses `<` instead of `<=`...
```

On resume (`--history-md` points to existing file): agent loads prior turns into context, then appends new turns. File is written incrementally — each turn appended as it completes.

---

## Lifecycle Hooks

Configured in `.agent/config.yaml` or `~/.agent/config.yaml`. Agent calls hook commands synchronously at each lifecycle point, piping JSON to stdin, reading JSON response from stdout.

```yaml
hooks:
  on_session_start: "my-hooks session-start"
  on_session_end: "my-hooks session-end"
  on_turn_start: "my-hooks turn-start"
  on_turn_end: "my-hooks turn-end"
  on_tool_call: "my-hooks before-tool"
  on_tool_result: "my-hooks after-tool"
  on_context_build: "my-hooks inject-context"
  on_error: "my-hooks handle-error"
```

### Hook Protocol

Agent writes JSON to hook's stdin, reads JSON from hook's stdout. Hook must exit 0 on success.

**on_tool_call** — can block or modify:
```json
// stdin
{"hook": "on_tool_call", "tool": "bash", "params": {"command": "rm -rf /"}}

// stdout — allow
{"action": "allow"}

// stdout — block
{"action": "block", "reason": "dangerous command"}

// stdout — modify params
{"action": "modify", "params": {"command": "echo blocked"}}
```

**on_context_build** — can transform message array:
```json
// stdin
{"hook": "on_context_build", "messages": [...]}

// stdout
{"messages": [...]}  // return modified array
```

**on_turn_end** — informational, stdout ignored:
```json
// stdin
{"hook": "on_turn_end", "response": "Here are your files..."}
```

All other hooks are informational — agent ignores stdout, only checks exit code (non-zero = log warning, continue).

Hook timeout: 5s default. Configurable per hook via `timeout_ms` in config.

---

## Personalities

Named agent configurations. Ship built-ins, support custom YAML.

```yaml
# ~/.agent/personalities/coder.yaml
name: coder
model: gpt-4o
base_url: https://api.openai.com/v1
system_prompt: |
  You are an expert software engineer. You have access to bash and memory tools.
  Work methodically. Read before writing. Test after changing.
tools:
  - bash
  - memory_search
  - memory_store
skills:
  - ~/.agent/skills/git-workflow.md
  - ./.agent/skills/project.md
```

Built-in personalities:
- `coder` — full tools, engineering-focused prompt
- `reviewer` — read-only (no bash write ops), code review prompt  
- `minimal` — no memory tools, bare prompt

```bash
agent "review PR #42" --personality reviewer
agent "what is 2+2" --personality minimal
agent "deploy" --config ./deploy-agent.yaml
```

---

## Skills

Markdown files with optional YAML frontmatter. Appended to system prompt under `# Skills`. Compatible with pi and Claude Code skill format.

```markdown
---
name: git-workflow
description: Git conventions for this repo
---

Always squash commits before merging to main.
Use conventional commit format: feat/fix/chore/docs.
Never force-push to main.
```

Loaded from (in order, later overrides earlier):
1. `~/.agent/skills/`
2. `./.agent/skills/`
3. `--skills` flag paths

---

## Configuration & Project Scoping

### Global (`~/.shell3/`) — credentials only

```
~/.shell3/credentials.yaml    # API keys per provider. Never committed to git.
```

```yaml
# ~/.shell3/credentials.yaml
providers:
  openai:
    api_key: sk-...
  ollama:
    base_url: http://localhost:11434/v1
  z_ai:
    api_key: zai-...
    base_url: https://api.z.ai/v1
```

Nothing else lives globally. Model choice, hooks, skills, personalities — all per-project.

### Per-project (`.shell3/`) — everything else

```
.shell3/
  config.yaml         # model, hooks, default personality, memory/history paths
  personalities/      # named personalities for this project
  skills/             # project-specific skills
  prompts/            # prompt templates
```

```yaml
# .shell3/config.yaml
model: llama3.2
provider: ollama
default_personality: coder
memory_db: .shell3/memory.db
history_md: .shell3/history.md
hooks:
  on_tool_call: ".shell3/hooks/guard.sh"
  on_context_build: ".shell3/hooks/inject.sh"
```

`.shell3/memory.db` and `.shell3/history.md` are gitignored by default. Config files can be committed for team sharing.

### Init Commands

```bash
agent init                        # scaffold .shell3/ with sane defaults in current dir
agent init https://github.com/org/agent-config  # pull shared team config from git repo
agent auth                        # set up ~/.shell3/credentials.yaml interactively
```

`agent init` creates:
```
.shell3/
  config.yaml           # sane defaults, user fills in model/provider
  personalities/
    coder.yaml          # default coder personality
  skills/               # empty, ready for project skills
  .gitignore            # ignores memory.db, history.md
```

`agent init <git-url>` clones the repo into `.shell3/` (or merges if `.shell3/` exists). Enables shared team configs — commit your personalities, skills, hooks to a repo and teammates run `agent init <url>`.

### Startup Validation

On every run, agent checks in order:

1. `.shell3/config.yaml` exists → proceed
2. Missing → exit with:
   ```
   No .shell3/ config found. Run: agent init
   ```
3. Credentials for configured provider exist in `~/.shell3/credentials.yaml` → proceed
4. Missing → exit with:
   ```
   No credentials for provider "ollama". Run: agent auth
   ```

This means: many different agent setups on one machine, one per project directory. Global state is only credentials.

---

## Go Package Structure

```
cmd/agent/          # main binary (agent run, agent init, agent auth)
internal/
  agent/            # core loop (LLM call, tool dispatch, hook dispatch)
  tools/            # bash, memory_search, memory_store
  memory/           # SQLite FTS5 wrapper
  history/          # markdown session read/write
  hooks/            # hook runner (spawn process, pipe JSON)
  config/           # .shell3/ project config + ~/.shell3/ credentials loading
  skills/           # markdown skill loader
  output/           # event emitter (plain text vs JSONL)
  llm/              # OpenAI-compatible client (sashabaranov/go-openai)
  init/             # agent init scaffolding + git repo pull
```

---

## Core Agent Loop (pseudocode)

```go
func Run(cfg Config, input string) error {
    session := loadHistory(cfg.HistoryMD)
    memory := openMemory(cfg.MemoryDB)
    hooks := loadHooks(cfg.Hooks)
    tools := buildTools(cfg, memory)
    out := buildOutput(cfg)  // plain text or JSONL

    hooks.Call("on_session_start", session)

    for {
        userMsg := input or readStdin()
        session.Append(UserMessage(userMsg))

        hooks.Call("on_turn_start", session)

        msgs := hooks.TransformContext(session.Messages())  // on_context_build
        response := llm.Stream(cfg.Model, cfg.SystemPrompt, msgs, tools)

        for event := range response {
            out.Emit(event)
            if event.IsToolCall {
                if err := hooks.Call("on_tool_call", event); err != nil {
                    // blocked
                    continue
                }
                result := tools.Execute(event.ToolCall)
                hooks.Call("on_tool_result", event.ToolCall, result)
                out.Emit(ToolResultEvent(result))
            }
        }

        session.Append(AssistantMessage(response.Text))
        saveHistory(cfg.HistoryMD, session)

        hooks.Call("on_turn_end", response)

        if !cfg.Interactive {
            break
        }
    }

    hooks.Call("on_session_end", session)
}
```

---

## Adapter Pattern

Telegram adapter example (any language, ~30 lines):

```python
# telegram-adapter.py
import subprocess, sys, json
from telegram import Bot

bot = Bot(token=TOKEN)

def handle_message(chat_id, text):
    proc = subprocess.run(
        ["agent", text, "--stream", "--memory-db", f"./chats/{chat_id}.db"],
        capture_output=True, text=True
    )
    for line in proc.stdout.splitlines():
        event = json.loads(line)
        if event["type"] == "done":
            bot.send_message(chat_id, event["text"])
```

Or using `--out` for streaming:

```bash
agent "fix bug" --stream --out "my-tui-renderer"
```

The adapter owns nothing — no SDK, no wiring, just reads JSONL and does what it wants.

---

## What This Is Not

- **Not a TUI framework** — use any terminal renderer you like
- **Not a plugin system** — hooks are shell commands, not compiled extensions
- **Not a server** — one process per conversation, no persistent daemon
- **Not opinionated about UI** — plain text by default, JSONL for machines

---

## Non-Goals (v1)

- Multi-session server mode
- Built-in Telegram/web adapter
- Context compaction (memory_store handles long-term recall instead)
- Streaming tool results (tool output emitted as complete result)
- Parallel tool execution (sequential only, simpler)
