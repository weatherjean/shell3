# shell3 documentation

shell3 is a minimal, Unix-composable coding agent. It runs LLM-powered sessions in your terminal using any OpenAI-compatible provider or Anthropic natively.

---

## Config layout

shell3 uses a two-tier directory structure.

**Global** (`~/.shell3/`) — shared across all projects:
```
~/.shell3/
├── ai-do-not-read.auth.yaml     # provider instances (plain YAML) — DO NOT READ
├── ai-do-not-read.secrets.yaml  # user-tool secrets (plain YAML) — DO NOT READ
├── personas/                    # global personas (e.g. base.md)
├── tools/                       # global user-defined tools
├── skills/                      # global skills
├── hooks/                       # global hook scripts
└── projects/
    └── <uuid>/          # per-project state
        ├── shell3.db    # SQLite (memory + history)
        └── meta.json    # project metadata
```

**Project** (`.shell3/` in your repo) — overrides and local additions:
```
.shell3/
├── .ref             # UUID pointer to ~/.shell3/projects/<uuid>/ (gitignored)
├── personas/        # project-local personas (override global by name)
├── tools/           # project-local tools (override global by name)
├── skills/          # project-local skills
├── hooks/           # project-local hook scripts
└── last_error.json  # last LLM error (gitignored)
```

`.ref` is auto-generated on first run and gitignored. Never commit it.

Project tools and personas override global ones when names collide.

The repository includes copyable example configurations under `examples/`. The canonical default bootstrap files shipped to new users live under `internal/scaffold/defaults/` and are written only when the target file is absent.

- `examples/tools/` — user-defined tools such as Brave Search and page fetching
- `examples/skills/` — reusable workflow skills, including web search guidance
- `examples/hooks/` — hook scripts such as terminal confirmation before `bash`

### Moving global config to a new machine

`~/.shell3/` is a plain directory — you can turn it into a git repo and sync your personas, tools, skills, and hooks across machines:

```bash
cd ~/.shell3
git init
# credential and secret files are sensitive — keep them out
echo -e "ai-do-not-read.*\nprojects/" > .gitignore
git add .
git commit -m "init global shell3 config"
git remote add origin <your-repo>
git push -u origin main
```

On the new machine:

```bash
# If ~/.shell3/ already exists (shell3 was run once), remove it first
rm -rf ~/.shell3
git clone <your-repo> ~/.shell3
shell3   # bootstraps fresh; personas/tools/skills arrive from git
```

`projects/` (SQLite DBs, session history) is machine-local and excluded. Auth and secrets must be set up fresh on each machine via `shell3 auth` and `shell3 secrets`.

---

## Commands

### shell3 (root command)

The root command runs the interactive chat agent. With a positional argument it runs once non-interactively.

On first run in a directory, shell3 auto-bootstraps: creates `.shell3/`, writes a `.ref` UUID, and writes default global personas, tools, skills, and hooks if absent.

```
shell3                                           # interactive TUI
shell3 "summarise TODO.md"                       # one-shot, prints to stdout
shell3 --model gpt-4o
shell3 --persona code                            # pick a persona from .shell3/personas/ or ~/.shell3/personas/
shell3 --no-bash                                 # disable bash + shell_interactive tools
shell3 --no-memory-tools                         # disable memory/history tools and store
```

Flags: `--persona`, `--provider`, `--model`, `--no-bash`, `--no-memory-tools`.

**Tools available to the model:**

| Tool                | What it does                                                          |
|---------------------|-----------------------------------------------------------------------|
| `bash`              | Execute non-interactive shell commands in the project directory       |
| `shell_interactive` | Run a command that needs a TTY (vim, less, REPL); TUI yields and resumes |
| `edit_file`         | Edit by exact string replacement; empty `old_string` creates or overwrites; atomic, diffs cleanly |
| `memory_upsert`     | Insert/update/delete a memory entry; empty value deletes; `core=true` injects into every session prompt |
| `memory_list`       | List memories newest-first; `core_only=true` restricts to core memories |
| `memory_search`     | Full-text search memories; `terms[]` (one concept per element) + `match=any\|all` (default any) |
| `history_get`       | Fetch one chunk (25 turns) of one session by `session_id` + `chunk`; walk via `prev_session_id` / `next_session_id` |
| `history_search`    | Full-text search past conversations; same `terms[]` + `match` shape as `memory_search`; hits include `session_id`/`chunk` for follow-up `history_get` |
| `shell3_docs`       | Return this documentation (commands, config, slash commands, skills)  |
| `prune_tool_result` | Replace a prior tool result with a stub to free context; any size or content |
| `compact_history`   | Compact full conversation into a structured summary (decisions, files, references, skills to re-read, next steps) and roll to a new session |

User-defined tools appear after the built-ins. Memory and history are stored per-project in `~/.shell3/projects/<uuid>/shell3.db`.

**Core memories.** Set `core=true` on `memory_upsert` to mark a fact important enough to be rendered into the system prompt at every session start (via the persona's `{{.CoreMemories}}` template variable). Use sparingly — every core memory inflates context.

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
| `/image`    | attach an image to the next turn: `/image "<path>" [prompt]`    |
| `/parameters` | list current LLM params; `/parameters <name> <value>` to set  |
| `/info`     | show session details: persona, project ref, skills, tools, hooks |
| `/exit`     | quit shell3 (alias: `/quit`)                                    |
| `/help`     | list available commands                                         |

**Image support.** `/image` resizes the image to 1000 px on the longest side, converts to JPEG, and sends it as a multimodal turn. Requires a vision-capable model. Supported formats: jpg, png, gif, webp. Quote paths that contain spaces:

```
/image "/tmp/Screenshot 2026-04-28 at 12.43.18.png" what is broken in this UI?
/image /tmp/shot.png describe the error
/image /tmp/chart.png                   # default prompt: "Describe this image."
```

### shell3 auth

Open `~/.shell3/ai-do-not-read.auth.yaml` in `$EDITOR` to configure provider instances. Each instance needs a `type` field: `openai` (any OpenAI-compatible endpoint) or `anthropic` (native Claude API).

```yaml
instances:
  - name: myinstance
    type: openai
    base_url: https://api.openai.com/v1
    api_key: sk-your-key-here
    models:
      - id: gpt-4o
        context_window: 128000
      - id: gpt-4o-mini
        context_window: 128000

  - name: anthropic
    type: anthropic
    api_key: ant-your-key-here
    models:
      - id: claude-sonnet-4-6
        context_window: 200000
```

```
shell3 auth        # open file in $EDITOR
```

Any OpenAI-compatible endpoint works: OpenAI, Ollama, Groq, LM Studio, OpenRouter, etc. The `name` field is what you pass to `--provider`. The first model in `models` is the default; add more for `/model` switching.

### shell3 secrets

Open `~/.shell3/ai-do-not-read.secrets.yaml` in `$EDITOR` to manage tool secrets. Format:

```yaml
secrets:
  BRAVE_API_KEY: sk-...
  MY_API_KEY: abc123
```

```
shell3 secrets        # open file in $EDITOR
shell3 secrets list   # list names (values masked)
```

Secrets are shared across all projects. Only the keys listed in a tool's `secrets:` field are exposed to that tool.

### shell3 doctor

Validate setup. Checks global dirs, credentials, secrets store, project `.ref`, meta.json, and default persona. Exit 0 when all checks pass.

```
shell3 doctor
```

### shell3 docs

Print this documentation.

```
shell3 docs
```

---

## Configuration

### Persona config — `~/.shell3/personas/<name>.md` or `.shell3/personas/<name>.md`

All per-project configuration — model, provider, store path, tool gating, hooks — lives in the YAML frontmatter of the active persona file. The default persona is `base.md`; switch with `--persona <name>`.

Global personas (`~/.shell3/personas/`) apply everywhere. Project personas (`.shell3/personas/`) override global ones by filename. On first run, a default `base.md` is written to `~/.shell3/personas/` if absent.

```markdown
---
name: base                       # persona name (defaults to filename)
description: short summary       # shown in pickers
model: ~                         # starting model (~ = credential default)
provider: ~                      # provider instance name (~ = alphabetically-first)
db: ~                            # SQLite path override (~ = ~/.shell3/projects/<uuid>/shell3.db)

# skills: [skill-name]           # allowlist; empty = load all from .shell3/skills/
# Built-in tools (always loaded — uncomment and edit to override user-tool allowlist):
# [bash, shell_interactive, edit_file, shell3_docs, prune_tool_result, compact_history, memory_upsert, memory_list, memory_search, history_get, history_search]
# tools: [tool-name]             # allowlist for user tools; empty = load all from .shell3/tools/

# LLM request parameters
parameters:
  reasoning_effort: medium       # none|minimal|low|medium|high|xhigh
  reasoning_summary: auto        # auto|concise|detailed|off
  verbosity: medium              # low|medium|high
  parallel_tool_calls: true
  temperature: ~                 # ~ to leave provider default
  max_tokens: 16000              # max output tokens (anthropic requires; openai sets MaxCompletionTokens)
  thinking_budget: 0             # anthropic extended-thinking budget tokens (0 = disabled)

# Hooks — string for plain command, or mapping with needs_tty.
on_session_start: ~              # fire-and-forget; runs once when session opens
on_session_end: ~                # fire-and-forget; runs once when session closes
on_turn_start: ~                 # fire-and-forget; runs before each LLM call
on_turn_end: ~                   # fire-and-forget; gets params.response after each LLM call
on_tool_call: ~                  # blocking; stdout {"action":"allow"|"block","reason":"..."} gates each tool call
on_tool_result: ~                # fire-and-forget; gets params.result after each tool call
on_context_build: ~              # blocking; stdout {"messages":[...]} can rewrite the message list sent to LLM
on_error: ~                      # fire-and-forget; runs on LLM errors and panics
---
The body is a Go template rendered into the system prompt.
Available variables: `{{.Time}}`, `{{.CWD}}`, `{{.Model}}`, `{{.Skills}}`, `{{.CoreMemories}}`, `{{.UserTools}}`.
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

**Provider resolution.** If the persona's `provider` is `~`, shell3 picks the first instance from `~/.shell3/ai-do-not-read.auth.yaml`. Set it explicitly to avoid surprises when multiple instances are configured.

**`needs_tty: true`** releases the TUI so the hook can read from the terminal (prompts, fzf, etc.). Without it, hooks run silently in the background — no TUI flash.

**Hook command quoting.** The command string is split on whitespace — quoted arguments with spaces are not supported. Use a script path instead of inline shell: `bash .shell3/hooks/my-hook.sh` rather than `bash -c "echo hello world"`.

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
# ~/.shell3/hooks/confirm-bash.sh
INPUT=$(cat)
TOOL=$(echo "$INPUT" | jq -r '.tool')
[[ "$TOOL" != "bash" ]] && echo '{"action":"allow"}' && exit 0
CMD=$(echo "$INPUT" | jq -r '.params.command // empty')
echo "Run: $CMD" >/dev/tty
read -r -p "Allow? [y/N] " ans </dev/tty
[[ "$ans" =~ ^[Yy]$ ]] && echo '{"action":"allow"}' || echo '{"action":"block","reason":"User denied"}'
```

```yaml
on_tool_call:
  command: "bash ~/.shell3/hooks/confirm-bash.sh"
  needs_tty: true
```

---

## User-Defined Tools

Drop YAML files into `~/.shell3/tools/` (global) or `.shell3/tools/` (project). Project tools override global ones on name collision. Tools are loaded at startup.

```yaml
name: brave_search           # required, [a-z][a-z0-9_]*, must not shadow built-ins
description: Web search…     # required, shown to the model
enabled: false               # required; tools default off
secrets: [BRAVE_API_KEY]     # optional; keys from ~/.shell3/ai-do-not-read.secrets.yaml
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

**Secrets:** Run `shell3 secrets` to open the secrets file in `$EDITOR`. Only the secrets listed in a tool's `secrets:` field are exposed to that tool. Secret values are scrubbed from tool output and replaced with `***REDACTED***` before reaching the model.

**Hooks (per-tool, optional):**
- `before` — receives args JSON on stdin. Non-zero exit blocks the call (stderr becomes the block reason). Stdout, if valid JSON, replaces the args. Hooks do **not** receive declared secrets in env.
- `after` — receives command output on stdin. Stdout replaces the output on success; on failure the original output is kept and a `[after-hook failed: …]` sentinel is appended. Final output is redacted regardless of hook outcome.
- Order at runtime: `on_tool_call` (persona) → tool `before` → command → tool `after` → secret redaction → `on_tool_result` (persona).
- Each hook gets its own timeout budget equal to the full `tool.Timeout`.

**Validation at startup:** Invalid tools are skipped with a warning to stderr. Reasons include: missing required field, name shadowing a built-in, invalid name format, declared secret missing from secrets store, `parameters.type` not `object`.

**Getting started:** On first run, a disabled `brave_search.yaml` and an enabled `web_fetch.yaml` are written to `~/.shell3/tools/` if absent. Run `shell3 secrets` to add `BRAVE_API_KEY`, set `enabled: true` in `brave_search.yaml`, and restart to use Brave Search. See `examples/tools/` for fuller copyable tool configs, including a Brave Search tool with concise search and LLM Context modes.

---

## Skills

Skills are persistent instruction sets injected into the system prompt at session start. Use them to encode workflows, conventions, or domain rules that should apply across sessions.

### How skills work

- Stored as `.md` files in `~/.shell3/skills/` (global) or `.shell3/skills/` (project)
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
```

### Creating a skill

```bash
cat > ~/.shell3/skills/git-workflow.md << 'EOF'
---
name: git-workflow
description: Git conventions for this project
---

Always run tests before committing.
Use conventional commits: feat/fix/chore/docs/refactor.
Never force-push to main.
EOF
```

Then restart the session.

See `examples/skills/` for copyable skill files, including `web-search.md` for low-limit Brave Search / LLM Context usage.

### When to use skills vs memory

| Use skill | Use memory |
|-----------|------------|
| Rules that always apply | Facts discovered during session |
| Workflows to follow | Project-specific one-off context |
| Conventions for this project | Lookup values (keys, IDs, paths) |
| Instructions for the agent | Information for the agent to recall |

---

## Multiple models

Models are defined per-instance in `~/.shell3/ai-do-not-read.auth.yaml` under `models:`. The session starts on the first model, unless the active persona's frontmatter sets a `model`. Use `/model` inside a session to switch.

```markdown
---
# ~/.shell3/personas/base.md — preferred starting model + provider for all projects
model: gpt-4o
provider: myinstance
---
```

`--model` flag overrides frontmatter:
```
shell3 --model gpt-4o-mini
```

---

## Providers

Two adapter types are built in: `openai` (any OpenAI-compatible endpoint) and `anthropic` (native Claude API). Common setups:

| Provider  | type      | base_url                          | api_key        |
|-----------|-----------|-----------------------------------|----------------|
| OpenAI    | openai    | https://api.openai.com/v1         | sk-...         |
| Ollama    | openai    | http://localhost:11434/v1         | (empty)        |
| Groq      | openai    | https://api.groq.com/openai/v1    | gsk_...        |
| LM Studio | openai    | http://localhost:1234/v1          | (empty)        |
| OpenRouter| openai    | https://openrouter.ai/api/v1      | sk-or-...      |
| Anthropic | anthropic | (omit; uses default)              | ant-...        |

### Codex (ChatGPT subscription)

OpenAI Codex uses OAuth, not a static API key, so shell3 does not support it natively. Run the third-party [openai-oauth](https://github.com/EvanZhouDev/openai-oauth) proxy locally — it exposes Codex as a standard OpenAI-compatible endpoint:

```bash
npx openai-oauth
```

Then add it to `auth.yaml` as a regular `openai` instance:

```yaml
  - name: codex
    type: openai
    base_url: http://localhost:3000/v1
    api_key: ""
    models:
      - id: codex-mini-latest
        context_window: 200000
```
