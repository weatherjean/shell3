# shell3 documentation

shell3 is a minimal, Unix-composable coding agent. It runs LLM-powered sessions in your terminal using any OpenAI-compatible provider.

---

## Commands

### shell3 init
Scaffold `.shell3/config.yaml` in the current directory.

```
shell3 init
shell3 code --init   # also checks tool dependencies
```

### shell3 auth
Store provider credentials in `~/.shell3/credentials.yaml`.

```
shell3 auth
```

Prompts for: provider name, API key, base URL, default model.

### shell3 code
Interactive coding assistant with persistent memory and history.

```
shell3 code
shell3 code --model gpt-4o
shell3 code --model "gpt-4o,gpt-4o-mini"   # multiple models, switch with /model
shell3 code --base-url http://localhost:11434/v1 --api-key "" --model llama3.2
```

**Tools available to the model:**

| Tool             | What it does                                      |
|------------------|---------------------------------------------------|
| `bash`           | Execute shell commands in the project directory   |
| `memory_store`   | Persist a key-value fact across sessions          |
| `memory_list`    | List all stored memories                          |
| `memory_search`  | Full-text search memories                         |
| `memory_remove`  | Delete a memory entry by key                      |
| `history_latest` | Return the most recent conversation turns         |
| `history_search` | Full-text search past conversation turns          |

Memory and history are stored in `.shell3/shell3.db` (SQLite, gitignored).

### User-Defined Tools

Drop YAML files into `.shell3/tools/` (project) or `~/.shell3/tools/` (global). Project tools override global ones on name collision. Tools are loaded and merged into the model's tool list at startup.

```yaml
name: brave_search           # required, [a-z][a-z0-9_]*, must not shadow built-ins
description: Web search…     # required, shown to the model
enabled: false               # required; tools default off
secrets: [BRAVE_API_KEY]     # optional; loaded from .shell3/.env or OS env
parameters:                  # required; JSON Schema (type must be object)
  type: object
  properties:
    query: {type: string, description: Search query}
  required: [query]
command: |                   # required; bash -c
  curl -sG https://api.example.com/search \
    -H "Authorization: Bearer $BRAVE_API_KEY" \
    --data-urlencode "q=$QUERY"
timeout: 15s                 # optional; default 30s
cwd: ""                      # optional; default = project workdir
before: ""                   # optional; bash -c hook, stdin = args JSON
after: ""                    # optional; bash -c hook, stdin = command output
```

**How args reach your command:**
- Each scalar arg → upper-cased env var (`query` → `$QUERY`, `count` → `$COUNT`).
- The full args object is in `$ARGS_JSON` for `jq` consumers.
- Complex values (arrays, objects) are JSON-encoded into their env var.
- Args whose uppercased name collides with a declared secret are dropped (the secret wins).

**Secrets:** Put `KEY=value` lines in `.shell3/.env` (file mode must be 0600 — `chmod 600 .shell3/.env`). Only the secrets listed in a tool's `secrets:` field are exposed to that tool. Secret values are scrubbed from tool output and replaced with `***REDACTED***` before reaching the model. Secrets shorter than 4 characters are skipped (too likely to corrupt unrelated output).

**Hooks (per-tool, optional):**
- `before` — receives args JSON on stdin. Non-zero exit blocks the call (stderr becomes the block reason). Stdout, if valid JSON, replaces the args. Hooks do **not** receive declared secrets in env.
- `after` — receives command output on stdin. Stdout replaces the output on success; on failure the original output is kept and a `[after-hook failed: …]` sentinel is appended. Final output is redacted regardless of hook outcome.
- Order at runtime: `on_tool_call` (persona) → tool `before` → command → tool `after` → secret redaction → `on_tool_result` (persona).
- Each hook gets its own timeout budget equal to the full `tool.Timeout`.

**Validation at startup:** Invalid tools are skipped with a warning to stderr. Reasons include: missing required field, name shadowing a built-in (`bash`, `shell_interactive`, `memory_*`, `history_*`, `shell3_docs`), invalid name format, declared secret missing from environment, `parameters.type` not `object`.

**Example:** `shell3 init` drops a disabled `.shell3/tools/brave_search.yaml`. Add `BRAVE_API_KEY=…` to `.shell3/.env`, set `enabled: true`, restart.

**Slash commands inside a session:**

| Command   | Action                                        |
|-----------|-----------------------------------------------|
| `/`       | browse and pick a command                     |
| `/model`  | switch active model (if >1 configured)        |
| `/clear`  | reset conversation context                    |
| `/usage`  | show token usage from last turn               |
| `/prompt` | dump system prompt and active tools           |
| `/help`   | list available commands                       |

### shell3 run
One-shot agent run (non-interactive). Reads task from stdin or `--task`.

```
shell3 run --task "summarise TODO.md"
echo "fix lint errors" | shell3 run
```

### shell3 docs
Print this documentation.

```
shell3 docs
```

### shell3 destroy
Remove `.shell3/` from the current directory.

```
shell3 destroy
```

---

## Configuration

### Project config — `.shell3/config.yaml`

Created by `shell3 init` in the project directory.

```yaml
model: llama3.2          # preferred starting model for this project (single value)
provider: ollama         # preferred provider (must match a key in credentials.yaml)
store_db: .shell3/shell3.db   # SQLite DB for memory and history (gitignored)
hooks:
  on_session_start: ""  # fired once at session start (fire-and-forget)
  on_session_end: ""    # fired once at session end (fire-and-forget)
  on_turn_start: ""     # fired before each LLM turn (fire-and-forget)
  on_turn_end: ""       # fired after each LLM turn, params.response set (fire-and-forget)
  on_tool_call: ""      # fired before each tool call, can block with action:block (blocking)
  on_tool_result: ""    # fired after each tool call, params.result set (fire-and-forget)
  on_context_build: ""  # fired before LLM call, can rewrite messages array (blocking)
  on_error: ""          # fired on LLM errors and panics, params.error set (fire-and-forget)
```

Each hook value is either a plain string (command) or a mapping with `needs_tty`:

```yaml
hooks:
  # plain string — no TTY, output discarded for fire-and-forget hooks
  on_turn_end: "bash .shell3/hooks/log.sh"

  # mapping — set needs_tty: true to release the TUI before running
  on_tool_call:
    command: "bash .shell3/hooks/confirm.sh"
    needs_tty: true
```

**`needs_tty: true`** releases the TUI so the hook can read from the terminal (prompts, fzf, etc.). Without it, hooks run silently in the background — no TUI flash.

**Hook protocol:** shell3 writes JSON to the hook's stdin and reads JSON from stdout.

Stdin:
```json
{"hook": "on_tool_call", "tool": "bash", "params": {"command": "rm foo"}}
```

Stdout (blocking hooks only — `on_tool_call`, `on_context_build`):
```json
{"action": "allow"}
{"action": "block", "reason": "Denied by user"}
```

For `on_context_build`, return `{"messages": [...]}` to rewrite the message list sent to the LLM.

**Example — confirm before bash:**
```bash
#!/usr/bin/env bash
# .shell3/hooks/confirm-bash.sh
INPUT=$(cat)
TOOL=$(echo "$INPUT" | jq -r '.tool')
[[ "$TOOL" != "bash" ]] && echo '{"action":"allow"}' && exit 0
CMD=$(echo "$INPUT" | jq -r '.params.command // empty')
echo "Run: $CMD" >/dev/tty
read -r -p "Allow? [y/N] " ans </dev/tty
[[ "$ans" =~ ^[Yy]$ ]] && echo '{"action":"allow"}' || echo '{"action":"block","reason":"User denied"}'
```

```yaml
hooks:
  on_tool_call:
    command: "bash .shell3/hooks/confirm-bash.sh"
    needs_tty: true
```

### Global credentials — `~/.shell3/credentials.yaml`

```yaml
providers:
  ollama:
    api_key: ""
    base_url: "http://localhost:11434/v1"
    default_model: "llama3.2"
  openai:
    api_key: "sk-..."
    base_url: "https://api.openai.com/v1"
    default_model: "gpt-4o,gpt-4o-mini,o1-preview"  # comma-sep = switchable via /model
```

---

## Multiple models

Available models are defined globally in `~/.shell3/credentials.yaml` as a comma-separated `default_model`. The session starts on the first model (or the project's preferred model if set in `.shell3/config.yaml`). Use `/model` inside a session to switch.

```yaml
# ~/.shell3/credentials.yaml
providers:
  ollama cloud:
    default_model: "kimi-k2.6:cloud,glm-5.1:cloud,llama3.2"
```

```yaml
# .shell3/config.yaml — preferred starting model for this project
model: glm-5.1:cloud
provider: ollama cloud
```

`--model` flag overrides both:
```
./shell3 code --model "gpt-4o,gpt-4o-mini"
```

---

## Skills

Skills are persistent instruction sets injected into the system prompt at session start. Use them to encode workflows, conventions, or domain rules that should apply across sessions.

### How skills work

- Stored as `.md` files in `.shell3/skills/`
- At startup, skill name, description, and file path are injected into the system prompt under `# Skills`
- Full content is **not** eagerly loaded — model reads the file via `bash` when the skill applies to the task
- Take effect on next session start (restart required after adding/editing)
- Skills **without** valid frontmatter are silently ignored

### Skill format

Frontmatter is **required**. Skills without it are not loaded.

```markdown
---
name: skill-name
description: one-line summary of what this skill does
---

# Instructions

Write instructions here. Be direct and specific.
Rules, workflows, constraints, decision trees — whatever the agent should follow.

Use headings, bullet points, code blocks as needed.
```

### Creating a skill

To add a skill, write a `.md` file to `.shell3/skills/`:

```bash
# Example: create a git-workflow skill
cat > .shell3/skills/git-workflow.md << 'EOF'
---
name: git-workflow
description: Git conventions for this project
---

Always run tests before committing.
Use conventional commits: feat/fix/chore/docs/refactor.
Never force-push to main.
EOF
```

Then restart the session — the skill will be active.

### When to use skills vs memory

| Use skill | Use memory |
|-----------|------------|
| Rules that always apply | Facts discovered during session |
| Workflows to follow | Project-specific one-off context |
| Conventions for this project | Lookup values (keys, IDs, paths) |
| Instructions for the agent | Information for the agent to recall |

### Skill tips

- Keep each skill focused on one concern
- Skills stack — multiple files in `.shell3/skills/` all load
- Order is filesystem order (alphabetical by filename)
- Name the file after the skill: `git-workflow.md`, `testing.md`, `api-conventions.md`

---

## Providers

Any OpenAI-compatible endpoint works. Common setups:

| Provider  | base_url                          | api_key        |
|-----------|-----------------------------------|----------------|
| OpenAI    | https://api.openai.com/v1         | sk-...         |
| Ollama    | http://localhost:11434/v1         | (empty)        |
| Groq      | https://api.groq.com/openai/v1    | gsk_...        |
| LM Studio | http://localhost:1234/v1          | (empty)        |
