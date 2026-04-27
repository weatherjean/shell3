# shell3 documentation

shell3 is a minimal, Unix-composable coding agent. It runs LLM-powered sessions in your terminal using any OpenAI-compatible provider.

---

## Commands

### shell3 init
Scaffold `.shell3/` in the current directory. Requires `~/.shell3/credentials.yaml` to exist first (run `shell3 auth`).

Creates:
- `.shell3/personas/base.md` — default persona with frontmatter config (model, provider, db, hooks, etc.)
- `.shell3/tools/brave_search.yaml` — disabled example user-defined tool
- `.shell3/.env.example` — template for `.shell3/.env` (secrets)
- `.shell3/.gitignore` — ignores db, .env
- `.shell3/shell3.db` — SQLite store (memory + history)
- empty `.shell3/skills/` and `.shell3/hooks/` dirs

```
shell3 init
```

### shell3 auth
Configure adapter credentials. With no flags, presents an adapter menu and prompts for any required fields. Credentials are stored at `~/.shell3/credentials.shell3` (XOR-obfuscated YAML).

```
shell3 auth                                          # interactive: pick adapter, configure instance
shell3 auth --provider=openai                        # configure an OpenAI-compatible instance
shell3 auth --provider=openai --instance=ollama-local
shell3 auth --provider=codex                         # OAuth browser flow (single-instance)

shell3 auth list                                     # list configured instances + their models
shell3 auth remove <instance>                        # delete one instance
shell3 auth models <instance>                        # show current default_model CSV
shell3 auth models <instance> "a,b,c"                # replace default_model CSV
shell3 auth models <instance> ""                     # clear; adapter built-in list applies
```

**Concepts:**
- **Adapter** — code path for talking to a backend (`openai`, `codex`).
- **Instance** — one user-configured set of credentials for an adapter. The OpenAI-compatible adapter supports many instances (e.g. `ollama-local`, `openrouter-prod`, `openai-prod`). Codex is single-instance (always named `codex`).
- **default_model** — comma-separated list of models the persona's `/model` picker cycles through. First entry is the default.

**Editing models without re-authenticating.** `shell3 auth models <instance>` prints the current CSV. `shell3 auth models <instance> "<csv>"` replaces it. Pass an empty string (`""`) to clear and fall back to the adapter's built-in list (codex only — openai has no built-in list). Works for every adapter.

**Removing an instance.** `shell3 auth remove <instance>` deletes the instance from `credentials.shell3`. To start over completely: `rm ~/.shell3/credentials.shell3`.

**Storage format.** The credential file is wrapped with a fixed XOR + base64 layer behind a magic header. **This is obfuscation, not encryption.** It defends against accidental disclosure to LLM tools that walk your home directory and read files verbatim. Anyone with shell access (or this source tree) can reverse it trivially. Store actual high-value secrets in your OS keyring or a dedicated secret manager.

If `~/.shell3/credentials.yaml` or `~/.shell3/codex_tokens.json` exists from an older shell3, the first run automatically migrates them into `credentials.shell3` and renames `credentials.yaml` to `credentials.yaml.bak`.

### Adapters

shell3 ships with two adapters; new adapters live under `internal/adapters/<name>` and self-register via `init()`:

| Adapter  | Instance count | Auth                                | Models                                                  |
|----------|----------------|-------------------------------------|---------------------------------------------------------|
| `openai` | many           | base URL + API key + default model  | per-instance `default_model` CSV                        |
| `codex`  | one            | OAuth (ChatGPT subscription)        | per-instance `default_model` CSV; falls back to builtin |

Pass an instance name via the persona's `provider:` field, or pick at runtime with `--persona`. For single-instance adapters, instance name equals the adapter name.

### shell3 (root command)
The root command runs the interactive chat agent. With a positional argument it runs once non-interactively and prints the response to stdout.

```
shell3                                           # interactive TUI
shell3 "summarise TODO.md"                       # one-shot, prints to stdout
shell3 --model gpt-4o
shell3 --model "gpt-4o,gpt-4o-mini"              # /model switches between them
shell3 --base-url http://localhost:11434/v1 --api-key "" --model llama3.2
shell3 --persona base                            # pick a persona from .shell3/personas/
shell3 --no-bash                                 # disable bash + shell_interactive tools
shell3 --no-memory-tools                        # disable memory/history tools and store
```

Flags: `--persona`, `--model`, `--base-url`, `--api-key`, `--no-bash`, `--no-memory-tools`.

**Tools available to the model:**

| Tool                | What it does                                                          |
|---------------------|-----------------------------------------------------------------------|
| `bash`              | Execute non-interactive shell commands in the project directory       |
| `shell_interactive` | Run a command that needs a TTY (vim, less, REPL); TUI yields and resumes |
| `memory_upsert`     | Insert/update/delete a memory entry; empty value deletes; `core=true` injects into every session prompt |
| `memory_list`       | List memories newest-first; `core_only=true` restricts to core memories |
| `memory_search`     | Full-text search memories; `terms[]` (one concept per element) + `match=any\|all` (default any) |
| `history_get`       | Fetch one chunk (25 turns) of one session by `session_id` + `chunk`; walk via `prev_session_id` / `next_session_id` |
| `history_search`    | Full-text search past conversations; same `terms[]` + `match` shape as `memory_search`; hits include `session_id`/`chunk` for follow-up `history_get` |
| `shell3_docs`       | Return this documentation (commands, config, slash commands, skills)  |
| `prune_tool_result` | Replace a prior successful tool result with a stub to free context; gated to ≥500 bytes and non-error output |

User-defined tools (see below) appear after the built-ins. Memory and history are stored in `.shell3/shell3.db` (SQLite, gitignored).

**Core memories.** Set `core=true` on `memory_upsert` to mark a fact important enough to be rendered into the system prompt at every session start (via the persona's `{{.CoreMemories}}` template variable). Use sparingly — every core memory inflates context.

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

| Command     | Action                                                          |
|-------------|-----------------------------------------------------------------|
| `/`         | browse and pick a command                                       |
| `/model`    | switch model: `/model <name>`, or no arg → picker (≥2 models)   |
| `/clear`    | reset conversation context                                      |
| `/rollback` | remove last turn from context                                   |
| `/prune`    | `/prune <id>` — replace tool result `<id>` with a stub          |
| `/usage`    | show token usage from last turn                                 |
| `/prompt`   | dump system prompt and active tools                             |
| `/truncate` | toggle truncated bash output                                    |
| `/parameters` | list current LLM params; `/parameters <name> <value>` to set  |
| `/exit`     | quit shell3 (alias: `/quit`)                                    |
| `/help`     | list available commands                                         |

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

### Persona config — `.shell3/personas/<name>.md`

There is no `.shell3/config.yaml`. All per-project configuration — model, provider, store path, tool gating, hooks — lives in the YAML frontmatter of the active persona file. The default persona is `base.md`; switch with `--persona <name>`.

```markdown
---
name: code                       # persona name (defaults to filename)
description: short summary       # shown in pickers
model: kimi-k2.6                 # starting model (or ~ to use credential default)
provider: opencode-go            # provider key from credentials.yaml (or ~ for alphabetically-first)
db: .shell3/shell3.db            # SQLite path for memory + history (or ~ for default)
no_bash: false                   # disable bash + shell_interactive tools
no_memory: false                 # disable memory/history tools and store

# LLM request parameters — adapter clamps unknown values; defaults are
# medium across the board so codex models execute eagerly.
parameters:
  reasoning_effort: medium       # none|minimal|low|medium|high|xhigh
  reasoning_summary: auto        # auto|concise|detailed|off
  verbosity: medium              # low|medium|high
  parallel_tool_calls: true
  temperature: ~                 # ~ to leave provider default

# Hooks — string for plain command, or mapping with needs_tty.
on_session_start: ~              # fire-and-forget at session start
on_session_end: ~                # fire-and-forget at session end
on_turn_start: ~                 # fire-and-forget before each LLM turn
on_turn_end: ~                   # fire-and-forget after each LLM turn (params.response)
on_tool_call: ~                  # blocking before each tool call (can return action:block)
on_tool_result: ~                # fire-and-forget after each tool call (params.result)
on_context_build: ~              # blocking before LLM call (can rewrite messages)
on_error: ~                      # fire-and-forget on LLM errors and panics
---
The body of the persona file is a Go template rendered into the system prompt.
Available template variables: {{.Time}}, {{.CWD}}, {{.Model}}, {{.Skills}}, {{.CoreMemories}}.
```

Each hook value is either a plain string (command) or a mapping with `needs_tty`:

```yaml
# plain string — no TTY, output discarded for fire-and-forget hooks
on_turn_end: "bash .shell3/hooks/log.sh"

# mapping — set needs_tty: true to release the TUI before running
on_tool_call:
  command: "bash .shell3/hooks/confirm.sh"
  needs_tty: true
```

**Provider resolution.** If the persona's `provider` is `~`, shell3 picks the alphabetically-first provider from `~/.shell3/credentials.yaml`. Set it explicitly to avoid surprises when multiple providers are configured. CLI flags (`--model`, `--base-url`, `--api-key`) override frontmatter.

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

Available models are defined globally in `~/.shell3/credentials.yaml` as a comma-separated `default_model`. The session starts on the first model in that list, unless the active persona's frontmatter sets a `model`. Use `/model` inside a session to switch.

```yaml
# ~/.shell3/credentials.yaml
providers:
  ollama cloud:
    default_model: "kimi-k2.6:cloud,glm-5.1:cloud,llama3.2"
```

```markdown
---
# .shell3/personas/base.md — preferred starting model + provider for this project
model: glm-5.1:cloud
provider: ollama cloud
---
```

`--model` flag overrides both:
```
./shell3 --model "gpt-4o,gpt-4o-mini"
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
