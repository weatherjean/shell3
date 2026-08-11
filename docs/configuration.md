# Configuration

Your config is a **directory** (default `~/.shell3/`), and it follows four
rules:

1. **YAML wires it** — connections and knobs live in `shell3.yaml`.
2. **Markdown prompts it** — anything with a prompt body is a `.md` file with
   frontmatter.
3. **Files enable it** — a feature is on because its file exists, off because
   it doesn't. No enable flags.
4. **One script gates it** — policy is a bash hook script, not a config
   language.

`shell3 boot` writes a working tree; this page is for going beyond it.

```
~/.shell3/
  shell3.yaml            # wiring: models, telegram, mcp, media, background, runs_keep_days
  .env                   # secrets — never commit this file
  agent.md               # THE agent: frontmatter (model, tools, context) + prompt body
  memory.md              # a context: file the scaffold wires in by default
  agents/<name>.md       # subagents; the file IS the registration
  projects/<name>/       # a project: project.md brief + manager.md subagent (+ skills/)
  projects.md            # the agent's standing project index (brief)
  skills/<name>.md       # skills; drop a file in, reload
  hooks/tool-call.sh     # command gate for the main agent
  hooks/<name>.tool-call.sh   # command gate for subagent <name>
  hooks/*.tool-result.sh # output rewriters (same per-agent split)
  cron/<name>.md         # scheduled jobs (checklists included — see below)
```

`--config`/`-c` takes a path to a config directory; omitted, it's `~/.shell3`.
The working directory is never consulted, so behavior doesn't depend on where
you launch from.

Secrets are referenced from YAML as `env:KEY` — resolved from the `.env`
beside `shell3.yaml`, anywhere inside a string value (`"Bearer env:LINEAR_KEY"`
works). An `env:` reference naming a missing key fails the load.

## Models

A model is an endpoint plus the parameters shell3 sends it. Any
OpenAI-compatible endpoint works:

```yaml
models:
  main:
    base_url: https://api.openai.com/v1
    api_key: env:MAIN_API_KEY      # read from .env
    model: gpt-5.2
    context_window: 128000         # the model's REAL token budget
    compact_at: 100000             # auto-compact threshold; 0 = off
    # reasoning: medium            # if the model supports reasoning effort
    # temperature: 0.7             # omitted = leave the provider default
    # max_tokens: 4096             # cap on a single reply; omitted = adapter default
```

Set `context_window` to the model's actual budget — a wrong number skews the
context-usage reminders and the compaction trigger.

### Context management

When a turn's prompt crosses `compact_at` tokens, shell3 summarizes the head
of the conversation and keeps a verbatim recent tail. Host-managed: there are
no model-driven prune/compact tools. Two optional knobs:

```yaml
    keep_recent: 33000   # verbatim tail (tokens); default compact_at * 0.33;
                         #   a value ≥ compact_at is clamped to compact_at / 2
    prune_at: 60000      # cheaper first tier: stub old tool outputs, no LLM call
                         #   default compact_at * 0.6; 0 (or ≥ compact_at) disables;
                         #   setting it without compact_at is a load error
```

The agent (and any subagent) can skip the prune tier individually with
`prune: false` in its frontmatter (the thresholds stay on the model;
omitted/`true` inherits).

Compaction is host-managed and there are no model-driven prune/compact tools.
The Telegram front-end runs ONE long-lived conversation, so its history
grows steadily; when it crosses `compact_at` it compacts on its own, keeping
the conversation viable indefinitely. It happens silently — `/status` shows the current
context usage, and `shell3 ask`'s verbose output narrates each compaction as
it runs.

### Provider-specific knobs — `extra`

Keys in `extra` are injected verbatim into the top-level request JSON:

```yaml
    extra: { reasoning_split: true }                 # MiniMax: thinking → reasoning_content
    extra: { verbosity: high }                       # gpt-5-style verbosity
    extra: { provider: { order: [anthropic] } }      # OpenRouter routing (nesting works)
```

Only set it when needed — strict endpoints reject unknown fields.

### Local proxies — `run_proxy`

If a model needs a shim in front of its endpoint (a Codex subscription via
`npx`, a litellm gateway), set `run_proxy`. shell3 starts the command
detached, fire-and-forget, on the model's first use; logs go to
`~/.shell3/proxy-<model>.log`. If a proxy is already listening, the spawn just
fails to bind and the first request proceeds against it.

```yaml
models:
  codex:
    run_proxy: "npx @some/codex-proxy --port 8787"
    base_url: http://localhost:8787/v1
    # ...
```

## The agent — `agent.md`

The agent is one markdown file: frontmatter for the wiring, body for the
system prompt. There is exactly one agent because there is exactly one
`agent.md` — specialists are [subagents](#subagents--delegation).

```markdown
---
model: main
tools: [bash, bash_bg, edit, media, history]
context: [memory.md]
---
You are a personal assistant running inside shell3…
```

Frontmatter keys: `model` (required), `tools` (any of `bash`, `bash_bg`,
`edit`, `media`, `read`, `list_files`, `history`), `mcp` (see [MCP](#mcp-servers)),
`prune`, `context` (see below).

### Giving the agent a memory — `context:`

A new thread starts with no conversation history — the only continuity is
staying in a thread. `context:` is how you give the agent a standing memory
instead: a YAML list of paths, relative to the config directory, globs
allowed:

```yaml
context: [memory.md, notes/*.md]
```

- Each file's contents are appended to the system prompt under a `## Context`
  heading, one `### <path>` sub-section per file — the agent knows exactly
  where to `edit_file` to update its own brain.
- Files are re-read **at every turn**, not at config load or session
  creation: edit `memory.md` (or have the agent edit it) and the very next
  message sees the change — even in the one long-lived Telegram
  conversation, no reload needed.
- A literal (non-glob) entry that doesn't exist fails config load, same as
  any other strict-decode error. A glob matching zero files is legal —
  `shell3 health` warns about it. A file that disappears between load and a
  session being built is skipped with a `(context file missing: <path>)`
  stub in the prompt, never a turn failure.
- List order is preserved; a glob's own matches are sorted lexically within
  its entry.
- Main agent only — subagent frontmatter rejects `context` (like `skills`,
  subagents carry none). `projects.md` keeps its own separate mechanism (the
  standing portfolio brief, read at config load) — no unification with
  `context:` in this release.

`shell3 boot` scaffolds `context: [memory.md]` plus a starter `memory.md`;
existing configs are untouched since the key is optional.

The main agent is **bash-first**: it reads with `cat`/`sed -n`, lists with
`ls`/`find`, searches with `rg` — all through `bash` — and a hallucinated
`read_file`/`grep` call gets an error redirecting it back to bash/edit_file.
The `read` and `list_files` tools exist as an opt-in for agents that do better
with structured file tools (typically a [subagent](#subagents--delegation) on
a smaller model) — list them in `tools` to turn them on; leave them out and
the bash-first redirect stands. A read-only agent is a policy, not a tool set:
gate `bash` in its [hook script](#the-command-gate--hookssh).

### Recalling past conversations — the `history` tool

`history` in `tools` (the scaffold puts it on the main agent) lets the agent
read its own past out of the [runs store](#the-runs-store--shell3db) instead
of only the current thread:

- `{"query": "certificate renewal"}` — ranked full-text search over what you
  and the agent said, across **every** stored session, chat threads and
  cron runs alike. FTS5 syntax: bare words AND together, `"quoted phrases"`
  match exactly, `OR`/`NOT`/`prefix*` work; a malformed query is retried as
  one quoted phrase rather than erroring. Tool output is not indexed — search
  for what was said *about* a thing, not for raw command output.
- `{"session": "<id>", "around": 41}` — read the transcript around a hit.

The tool is read-only, and it is the whole interface: the agent never writes
to the database. Leave `history` out of `tools` and the name hits the same
unknown-tool redirect as `read_file`.

## Subagents & delegation

A subagent is a delegatable specialist: one file in `agents/`. The filename is
its name; the file is the registration — the main agent can spawn every
subagent in the directory, and the `task` tools appear automatically as soon
as any subagent exists — a file in `agents/`, or a project's manager (no
toggle). `description` is required: it's what the main model reads when
deciding to delegate. `agent.md` is illegal here — the name is reserved.

```markdown
---
description: Use for substantial, self-contained work you can hand off whole.
tools: [bash]
---
You are a general-purpose assistant…
```

`model` is optional (defaults to the main agent's). With at least one
subagent, the agent gets four tools: `task` (spawn: `{subagent_type, prompt,
description}`; returns immediately), `task_list`, `task_status <id>`,
`task_cancel <id>`. The subagent names and descriptions are baked into the
`task` tool's schema (an enum on `subagent_type`), so no per-turn reminder is
spent.

A spawned subagent is an **in-process background job** (a child-session
goroutine, not a subprocess). Subagents run headless (their hook scripts see
`headless: true`), and delegation is single-level by construction — a
subagent never gets the `task` tool.

`bash_bg` runs on the same job runtime but is gated separately by `bash_bg`
in `tools`. **Completions arrive as task reports** (see
[Task reports](#task-reports)): each finished job — bash_bg, subagent,
or cron run — hands the spawning agent a report, and its reply
reaches you as an ✉️ update only when worth saying (failures always post; the
result is recorded in the runs store and the jobs list either way). Both
`task` and `bash_bg` accept two extra args:

- `direct: true` — the raw result posts straight to the chat (🔔), costing
  no agent turn; the spawning session gets the notice queued for its next
  turn instead of a wake. The right choice when you asked for the work and
  just want the output;
- `note: "…"` rides along in the report as context ("the user is
  waiting on this").

A bash_bg job's full output is persisted to
`.shell3_project/runs/<session>/jobs/<id>.log` (capped at 1 MiB, swept with
its run) so the agent and `task_status` can read past the in-memory tail.

A subagent's still-running `bash_bg` job keeps its session open past its
main turn; each completion resumes the subagent for a follow-up turn whose
summary arrives as a task report like any other — or, for a `direct` job,
posts raw (capped at 5 follow-ups per subagent — past the
cap, or after cancel, the raw job event is mailed instead, so no completion
is lost). `task_cancel <sub-id>` cascades to the jobs that subagent started.
One global knob caps it all:

```yaml
background:
  max_concurrent: 8    # concurrent background jobs (default 8)
```

## Projects — `projects/`

A **project** groups long-running work under a dedicated manager. It's a
`projects/<name>/` directory with two files (plus an optional `skills/`):

```
projects/site/
  project.md         # the brief: frontmatter (description, workdir) + body
  manager.md         # the manager: a subagent named after the project
  skills/<name>.md   # optional; reach only this manager
```

`project.md`'s frontmatter is strict — `description` and `workdir` are both
required, and `workdir` (a `~/` is expanded) must be an existing directory.
The body is the brief the manager reads when it opens the project:

```markdown
---
description: The marketing site
workdir: ~/code/site
---
# site
State the goal, the current status, and what's next. Keep it short — deep
memory goes in sibling files in this folder.
```

`manager.md` is a subagent, parsed exactly like an `agents/<name>.md` file but
**named after the project** and run with its shell in the project's `workdir`
(not the config dir). Managers join the same flat subagent namespace as
`agents/`, so a project name that collides with a subagent — or the reserved
name `agent` — is a load error. Per-project `skills/` reach only that manager;
global `skills/` stay main-agent-only.

Scaffold a project with [`shell3 project new`](cli.md#shell3-project-new--scaffold-a-project);
it also appends an index line to `projects.md`. That file is the agent's
standing project index — its body is injected into the main agent's system
prompt (after the skills index, before any `## Context` section) so, in every
new thread, the agent knows which projects exist
and which manager owns each. Register a new manager for dispatch with `/reload`
or a restart; `shell3 health` validates and lists every
project.

## Scripts & secrets

There is no custom-tool declaration: reusable glue is a **script** the agent
runs through `bash`, documented by a skill when it needs one. The scaffold
ships a `scripting` skill that teaches the pattern — reusable scripts live in
`~/.shell3/lib/bin/`, and a script that needs an API key reads it from
`~/.shell3/.env` itself, at point of use:

```bash
key="$(grep '^WEATHER_API_KEY=' ~/.shell3/.env | cut -d= -f2-)"
```

The secret enters exactly one process for exactly one call and never appears
in the conversation. Pair it with the hook example's `.env` deny (block
commands that read `.env` directly) and, if you like, a
[`tool-result.sh`](#output-rewriting--tool-resultsh) redaction as backstop.
More in [security.md](security.md).

## MCP servers

For tools that live behind the [Model Context Protocol](https://modelcontextprotocol.io),
shell3 ships a tools-only MCP client (official Go SDK): stdio and streamable
HTTP transports, no OAuth/resources/prompts (a remote server that needs auth
takes a bearer header from `.env`). Declare servers once in `shell3.yaml`;
each agent opts in via `mcp:` in its frontmatter:

```yaml
mcp:
  github:
    command: [github-mcp-server, stdio]        # stdio: argv list
    env: { GITHUB_TOKEN: env:GITHUB_TOKEN }
  linear:
    url: https://mcp.linear.app/mcp            # streamable HTTP
    headers: { Authorization: "Bearer env:LINEAR_KEY" }
    timeout: 30                    # seconds, connect + per call (default 10)
    allow: [search_issues, get_issue]   # or deny: [...] (not both)
```

```markdown
---
model: main
tools: [bash]
mcp: [github, linear]     # or mcp: all; omitted = NO MCP tools
---
```

Servers connect at startup (and on reload), in parallel, each under its
own timeout; their tools join the opted-in agents' tool lists as
`mcp_<server>_<tool>` (`mcp_github_search_issues`). A server that is down
loads as a **warning** — shell3 still starts, that server's tools are just
absent until the next reload — while `shell3 health` treats it as a failure
and reports each server's state. `/status` lists every server (up/down,
tool count, last error). At call time a dead server gets one
automatic reconnect; if that fails too the model sees the error as tool
output and adapts — a broken server never kills a turn.

MCP calls flow through the same [tool-call hook](#the-command-gate--hookssh)
as everything else: `name` is the prefixed tool name and `command` is null, so
gate them by name.

## The command gate — `hooks/*.sh`

shell3 gives the model a real shell, so the hook script is what limits it. A
scaffolded config **ships with the gate armed** (see below); an agent with no
hook file of its own runs ungated.
Hooks are per-agent, with no fallback or chaining — each agent is governed by
exactly one script per kind, or none:

- `hooks/tool-call.sh` — the main agent.
- `hooks/<name>.tool-call.sh` — subagent `<name>` (including when cron
  dispatches it). A subagent with no hook file runs **ungated**; the main
  hook never applies to it.

The split keeps each script trivial: a read-only subagent's gate can be a
short allowlist instead of one shared script branching on agent identity.
A hook file whose `<name>` matches no subagent is a warning
(`shell3 health` fails on it — it's usually a typo). `/status` states which
of the two it is, in as many words: **command gate armed**, or **command
gate off** when the main agent has no `hooks/tool-call.sh`.

Every tool call — `bash`, `bash_bg`, `edit_file`, `read_media`, host tools
like `image_generate`, and `mcp_*` — runs the governing script as
`bash hooks/….sh` with JSON on stdin:

```json
{"name": "bash", "command": "rm -rf /", "args": "{…}", "headless": false}
```

| Field | Description |
|-------|-------------|
| `name` | The real tool name: `"bash"`, `"bash_bg"`, `"edit_file"`, `"read_media"`, `"image_generate"`, `"mcp_…"`. |
| `command` | The bash command string — the two bash tools only; **null** for every other tool. |
| `args` | Raw arguments JSON (every tool). Gate non-bash tools by inspecting this. |
| `headless` | `true` when no human is attached (subagents, cron jobs, scripted `shell3 ask -p`). |

The script prints a verdict to stdout:

| Output | Effect |
|--------|--------|
| empty or `{}` | Run. |
| `{"block": true, "reason": "…"}` | Block; `reason` goes to the model. Any tool. |
| `{"command": "…"}` | Rewrite the bash command. Bash tools only — fails closed elsewhere. |
| `{"argv": ["…"]}` | Exec exactly this argv (runner swap). `bash`/`bash_bg` only. |

When several keys are set, precedence is block > argv > command. A script
that exits nonzero, prints malformed JSON, or runs past 10 s **fails
closed** (blocks, with the failure as the reason). A legacy `{"ask": …}`
verdict also fails closed, with a reason naming the removal — it never
silently allows. The script's cwd is the config directory. Compose
everything in the one script; there is no chain.

The scaffold ships `hooks/tool-call.sh` armed. Its shape, and the reasoning
behind it, in one line each:

- **Credentials** (`.env`, `~/.ssh`, `~/.aws`, `~/.config/gh`, …) — blocked for
  read and write, by every tool. A `lib/bin` script reads the one key it needs
  at point of use, so secrets never enter the conversation.
- **The gate itself** and `shell3.yaml` — readable, not writable. Otherwise
  "ask the operator to lift this" has an obvious shortcut.
- **The machine's plumbing** (`/etc`, `/usr/bin`, `/System`, `~/Library`) —
  writes blocked. `/usr/local` and `/opt` are not on the list: installing a
  tool is ordinary work.
- **Never** — `rm -rf /`, `mkfs`, fork bombs, and anything that stops shell3
  itself (an autonomous agent that kills its own runtime has nobody to restart
  it).
- **Unread remote code** — `curl … | sh`, `base64 -d | sh` and friends.
- **Public and permanent** — `npm publish`, `gh release create`, force-pushes.
  Ordinary `git push` is allowed: it is normal work and it is recoverable.
- **Everything else runs.** A denylist, deliberately: an allowlist means every
  new project is a refusal until someone edits this file, which is how gates
  get switched off entirely.

Every rule decides at once — there is no ask verdict or approval flow — and
each refusal tells the model not to route around it but to raise it with
the operator. `jq` makes the JSON handling clean:

```bash
in=$(cat)
name=$(printf '%s' "$in" | jq -r .name)
cmd=$(printf '%s' "$in" | jq -r '.command // empty')
if [ "$name" = "bash" ] || [ "$name" = "bash_bg" ]; then
  case "$cmd" in
    *'rm -rf /'*|*mkfs*|*'dd if='*)
      printf '{"block": true, "reason": "hard_deny"}'; exit 0 ;;
    *'git push --force'*)
      printf '{"block": true, "reason": "force-push refused; raise it with the operator"}'; exit 0 ;;
    *.env*)
      printf '{"block": true, "reason": "read secrets via a lib/bin script (scripting skill)"}'; exit 0 ;;
  esac
fi
exit 0
```

There's no allowlist by default: ordinary reads (`cat`, `rg`, `ls`) match
nothing and just run; only what you gate is affected. A hook is any program
bash can start — exec into Python if a gate outgrows shell.

### Runner swap (container, SSH, firejail)

`{"argv": […]}` chooses the program that runs the agent's command; the
command arrives as one argv element, so nothing re-parses or re-quotes it:

```bash
in=$(cat)
name=$(printf '%s' "$in" | jq -r .name)
cmd=$(printf '%s' "$in" | jq -r '.command // empty')
if [ "$name" = "bash" ] || [ "$name" = "bash_bg" ]; then
  jq -cn --arg cmd "$cmd" '{"argv": ["docker", "exec", "mycontainer", "bash", "-c", $cmd]}'
fi
```

A malformed argv (empty, or any empty element) fails **closed**. Recipes in
[cookbook/sandbox.md](cookbook/sandbox.md).

### Output rewriting — `tool-result.sh`

The symmetric post-execution hook: `hooks/tool-result.sh` (main agent) /
`hooks/<name>.tool-result.sh` (subagent) receives
`{"name": …, "args": …, "output": …}` on stdin; print `{"output": "…"}` to
replace what the model sees, `{}` or nothing to pass through. Primary use is
secret redaction:

```bash
in=$(cat)
printf '%s' "$in" | jq -c '{output: (.output | gsub("API_KEY=\\S+"; "API_KEY=[redacted]"))}'
```

A failing script here also fails **closed**: the tool output is replaced by an
error notice, never passed through unredacted. Background jobs (`bash_bg`)
are out of scope: the hook sees only the "started job…" pointer, not the
process's streamed output — redact at the source if a background command can
emit secrets.

## Telegram — `telegram:`

The bot's credentials, the one chat it answers, and where the agent's shell
runs.

```yaml
telegram:
  token: env:TELEGRAM_TOKEN         # TELEGRAM_TOKEN in .env, from @BotFather
  chat_id: "123456789"             # the single chat the bot answers
  workdir: /home/me/.shell3/workdir # optional; default = the config dir
```

`shell3 telegram` refuses to start without `token` and `chat_id`, and a
non-numeric `chat_id` fails at startup. Loading a config without the block
still succeeds — `shell3 ask` and `shell3 health` don't need it.

There is no listener, no login, no tunnel: shell3 long-polls Telegram
outbound, and access control is the token plus the one `chat_id` it answers.
Whoever controls that chat — or the token — controls a shell on this
machine. The threat model is in
[security.md](security.md#the-telegram-boundary).

### What `/reload` does and doesn't pick up

`/reload` (and the agent's own `reload` tool) re-reads the config directory
and applies it live: prompts, models, subagents, projects, skills, cron jobs,
MCP servers, and the `media:` blocks. It does **not** re-apply the
front-end's own wiring — a changed `telegram.chat_id` or `telegram.workdir`
takes effect at the next `shell3 telegram` start.

## Voice & images — `media:`

Four optional blocks under `media:`, each pointing at a model by name. All
speak the same OpenAI-compatible surface: `audio/transcriptions`,
`audio/speech`, chat completions with an image part, `images/generations`.
Everything below lands in the media dir, `<configDir>/media` —
`~/.shell3/media/` for the default config dir — overridable with
`$SHELL3_MEDIA_DIR` (see [The media janitor](#the-media-janitor--media_keep_days)
for that variable's precedence).

```yaml
media:
  stt: { model: groq-whisper, echo: true }        # voice notes → text
  tts: { model: groq-tts, voice: Fritz-PlayAI, mode: inbound }
  describe: { model: some-vision-model }          # for text-only main models
  imagegen: { model: some-image-model, size: 1024x1024 }
```

- **`stt: { model, language?, echo? }`** transcribes an inbound voice note
  before the turn and injects the transcript as the message. The recording is
  stored in the media dir (as `tg-*`) and its path goes into the prompt too.
  `echo: true` also posts the transcript back to the chat. A failure injects
  a could-not-transcribe marker and posts a ⚠️ notice; the turn still runs.
- **`tts: { model, voice?, format?, mode? }`** speaks the reply instead of
  posting it as text. `mode` is the default: `off`, `inbound` (speak only
  when the message came in as a voice note), or `always`; `/voice` overrides
  it at runtime and the override persists in `~/.shell3/voice_mode.json`.
  `voice` and `format` are passed to the model; an `opus`/`ogg` result is
  sent as a Telegram voice bubble, anything else as an audio file.
  Synthesized audio is cached in the media dir (as `tts-*`). Any failure
  falls back to text, so a reply is never lost.
- **`describe: { model, prompt? }`** captions an inbound photo before the
  turn, injecting `[image: <description>]`. Point it at a vision model when
  the main model is text-only, or at the main model itself (`shell3 boot`
  wires this when you say your model has vision). Every file you send is
  stored in the media dir and its path goes into the prompt, so the agent
  can re-open it later with `read_media` either way.
- **`imagegen: { model, size?, api? }`** adds an `image_generate{prompt,
  size?}` tool to **every** agent (main and subagents). `api: openai`
  (default) uses `images/generations`; `openrouter` POSTs a chat-completions
  request with `modalities=["image","text"]` — OpenRouter's image-output
  dialect — and reads the image off the reply (its dedicated `/api/v1/images`
  endpoint pre-authorizes worst-case cost, ~$2, and 402s low balances; the
  chat route charges actual usage, ~$0.03/image; `size` is ignored on this
  shape). Generated files land in the media dir and the tool returns the
  path; the main agent delivers it with `send_media_telegram` (kind
  `photo`), while a subagent reports the path for the parent to send. Gate
  it like any tool (`name == "image_generate"` in the hook payload).

**Media storage.** Everything you send the bot (`tg-*`), generated images
(`img-*`), and synthesized speech (`tts-*`) live in the media dir — stable
paths, re-readable with `read_media` and re-sendable with
`send_media_telegram` long after the message has scrolled away. The folder
grows until you prune it or set
[`media_keep_days`](#the-media-janitor--media_keep_days).

**`read_media` modalities** (needs `media` in the agent's `tools`): images
(`.jpg/.jpeg/.png/.gif/.webp`, vision models), audio
(`.wav/.mp3/.ogg/.opus/.oga`, audio models), PDFs (`.pdf` ≤ 20 MB, an
OpenAI-compatible `file` part — works on OpenAI and OpenRouter), and video
(`.mp4/.webm/.mov` ≤ 40 MB, a `video_url` part — an OpenRouter/Gemini
extension plain OpenAI endpoints reject; OpenRouter also wants at least $1.00
of balance on any request carrying video, whatever it actually costs).

Provider recipes — a one-key Groq quickstart for STT+TTS, the OpenRouter
variant — live in [cookbook/voice-images.md](cookbook/voice-images.md).

## Scheduled jobs — `cron/`

One file per job; the filename is the job name. Each fires a declared agent
on `schedule` (cron expression or `@daily`/`@hourly`/…), with the body as its
prompt. `agent` names either a subagent from `agents/` or a project's
`manager.md` — a project's cron job runs its manager in that project's
workdir, so a scheduled job can dispatch straight into a project's standing
context. The scheduler runs inside `shell3 telegram`, dispatching each job
from a hidden, pinned `cron` parent session. Interval schedules
(`@every 30m`) count from when the scheduler arms, and a `/reload` or
restart re-arms it — so the tick after one lands a full interval later,
which can look like a skipped run. Cron *expressions* (`*/30 * * * *`)
fire on wall-clock times and don't shift.

```markdown
---
schedule: "@daily"
agent: assistant
# direct: true          # optional; post the raw result (see below)
# workdir: /some/path   # optional; defaults to the config dir
---
Summarize anything noteworthy from the last day.
```

A cron run's result arrives as **mail to the main agent** (see
[Task reports](#task-reports)): a turn of the main conversation reads it, with the
job's prompt riding along as context so the agent knows what the job is
*for*, and its reply reaches you as an ✉️ update only when the run carries
something worth saying (NO_REPLY stays silent). A periodic checklist therefore only
speaks up when something needs attention: write its prompt to report
findings plainly, and the quiet runs stay quiet (no sentinel needed). A
failed run always surfaces as a ⚠️ alert and never spends an agent turn.

`direct: true` skips the agent: the raw result posts straight to the chat as
a ⏰ message, costing no agent turn — for jobs whose output should be
reported verbatim, not judged. `workdir` sets the job's working directory; the
default is the dispatched agent's own — a project manager runs in its
project's `workdir`, everything else in the config dir — and setting it
overrides even a manager's. A reload arms changed files.

`/cron` lists every job with its schedule, agent, workdir, `direct` flag,
full prompt, and last run; `/run <name>` fires one by hand — the result
travels the usual mail route, exactly as a scheduled firing would.

The scaffold ships a checklist example as `cron/checklist.md.example` —
rename it to `checklist.md` (drop the `.example`) and reload to activate it.

## Task reports

Every background completion — a `bash_bg` exit, a subagent's result, a
lingering follow-up, a cron run — is a **task report to the agent**, routed
deterministically by the host. No triage persona, no judging turn; three
rules:

- **Failures always surface.** A failed job posts `⚠️ <label> failed: …` to
  the chat, unconditionally. If the owning session is still live it also
  receives the report so the agent can react; an ownerless failure (a broken
  cron job, say) stops at the post — no agent turn is spent per broken tick.
- **`direct: true`** (bash_bg arg, task arg, cron frontmatter) posts the
  **raw result** straight to the chat — ⏰-prefixed for cron, 🔔 otherwise —
  costing no agent turn. The owning session gets the report queued, without
  a turn, so its next turn has it in context.
- **Everything else is a report to the agent.** The report queues into the
  main conversation and runs a turn over it (whichever session spawned the
  job — cron results and orphans land there too), carrying the spawner's
  `note:` — for cron runs, the job's own prompt, so the agent knows what the
  job is *for*. The report never reaches you raw; the agent's reply posts to
  the chat as an **✉️ update** — one channel, no separate tool — unless it
  replies `NO_REPLY`, which posts nothing. Silence is the expected answer
  for routine results, and for anything the conversation shows you were
  already told.

The ✉️ prefix marks an agent-initiated update, so bare chat text always
means a direct reply to something you sent. Updates are part of the one
conversation, and anything you type next continues it. ✉️ updates always
arrive without a notification ping — an update is not a page; `/quiet on`
extends that to ⏰/🔔 posts. Replies to your own messages and ⚠️ failures
always ring.

Report-handling turns are ordinary stored runs, so `/runs` shows exactly
what the agent did with each report. A leftover `notifier.md` from an older install
loads with a warning saying it is no longer used — delete the file.

## The runs store — `shell3.db`

Every session — chat threads, subagents, cron runs, `shell3 ask` — is
stored in one SQLite database beside `shell3.yaml`:
`.shell3_project/shell3.db`. It holds the sessions and their messages, each
front-end's thread→session index, and an FTS5 full-text index over user and
assistant text (the index the [`history` tool](#recalling-past-conversations--the-history-tool) searches;
tool output is deliberately not indexed). It is pure Go — no cgo, no
external SQLite. A background job's raw output stays a plain file under
`.shell3_project/runs/<session>/jobs/<id>.log`.

The database carries a schema version. If it doesn't match the binary you are
running, the file is **deleted and recreated empty**, with one line on stderr
saying so. shell3 data is disposable by design: there are no migrations, and
a version skew never leaves you on a half-understood schema. Keep anything
you actually care about outside the store. A corrupted version stamp that
happens to land within the valid range (e.g. a genuine `2` misread as `1`) is
indistinguishable from an actual older schema, so that database is recreated
empty too — the data is not recoverable.

## The runs janitor — `runs_keep_days`

Every thread — and every background job — is a stored session, so history
multiplies quickly. An optional top-level
`shell3.yaml` key bounds it:

```yaml
runs_keep_days: 30   # default 30; 0 = keep forever
```

Both `runs_keep_days` and `media_keep_days` are validated at config load:
negative values are a load error (use `0` for keep-forever, not a negative
number), and either key above 36500 (100 years) is also a load error — that
bound exists because the janitor's arithmetic is
`time.Duration(days) * 24 * time.Hour`, which overflows int64 nanoseconds
around 106751 days and can wrap into a small *positive* duration, silently
turning "keep basically forever" into "delete almost everything" on the next
sweep. 36500 is nowhere near that wraparound and is already an absurd
retention window.

At `shell3 telegram` startup — before the bot starts polling, never on
`shell3 ask` — a sweep deletes sessions whose last activity is older than the
cutoff, taking their messages, FTS entries, thread-index rows and job-log
directories with them. It also removes empty crash leftovers, thread entries
pointing at sessions that are gone by any other means, and orphaned
`runs/<id>/` directories left by older builds. It prints one line, `janitor:
removed N runs, M thread entries` (silent when both are zero). Fail-open: a
sweep error is reported as a `warning: janitor: …` line and the bot still
starts — stale rows are cosmetic hygiene, never a reason to refuse startup.
Start-time only — no daemon, no timers.

## The media janitor — `media_keep_days`

The media dir (`~/.shell3/media/` by default) accumulates everything you
send the bot, generated images, and cached TTS audio, and nothing
removes them on its own. An optional top-level `shell3.yaml` key bounds it:

```yaml
media_keep_days: 0   # default 0 = keep forever
```

Unlike `runs_keep_days`, the default is keep-forever: delivered files and
uploads are user data, so deletion is opt-in. When set, `shell3 telegram`
deletes files in the media dir whose mtime is older than N days at startup,
before the bot starts polling. It prints `janitor: removed N media files`
(silent when zero) and is fail-open like the runs janitor. Note that once a
file is swept, a `read_media` or `send_media_telegram` of its stored path in
an old transcript fails. Start-time only — no daemon, no timers.

The media dir this sweep points at is resolved by `internal/mediadir`:
normally it's derived from `--config`/the active config dir, but the
`SHELL3_MEDIA_DIR` environment variable, if set, overrides that unconditionally
— it outranks `--config`. This exists for tests, is not itself a
`shell3.yaml` key, and is undocumented anywhere else, but production code
reads it too. Since this is a *deleting* operation once `media_keep_days` is
set, be deliberate about that variable: an errant `SHELL3_MEDIA_DIR` pointed
at an unrelated directory (or a symlinked media dir, which the sweep follows)
means `media_keep_days` deletes old files there instead of in your actual
media store.

## Skills — `skills/`

A skill is a plain `.md` file the agent reads with `cat` when relevant — no
`skill` tool, no declaration. Every `*.md` in `skills/` (non-recursive)
becomes one skill for the main agent. Frontmatter needs a `description` (the
one-liner the agent uses to decide whether to read the body); `name` defaults
to the filename:

```markdown
---
description: Planning + approval gate before any non-trivial change.
---
When asked for a non-trivial change, first...
```

Adding a skill = drop a file in `skills/` + a reload. An unusable file (no
frontmatter/description, empty body, duplicate name) is skipped with a
warning — `shell3 health` hardens those into errors. Granted skills are
indexed by absolute path in the system prompt under `## Skills`. Subagents
carry no skills; put a subagent's standing instructions in its prompt body.

## Putting it together

Read the tree `boot` writes (`~/.shell3/`) for a full example; the
[cookbook](cookbook/README.md) has drop-in extras — subagents, skills, MCP
and sandbox setups. Validate any edit with `shell3 health` before reloading.
