# Configuration

Your config is a **directory** (default `~/.shell3/`) with four rules:

1. **YAML wires it**: connections and knobs live in `shell3.yaml`.
2. **Markdown prompts it**: anything with a prompt body is a `.md` file with
   frontmatter.
3. **Files enable it**: a feature is on because its file exists. No enable
   flags.
4. **One script gates it**: policy is a bash hook script, not a config
   language.

`shell3 boot` writes a working tree; this page is for going beyond it.

```
~/.shell3/
  shell3.yaml            # wiring: models, telegram, mcp, media, background, runs_keep_days
  .env                   # secrets — never commit this file
  agent.md               # THE agent: frontmatter (model, tools, context) + prompt body
  notifier.md            # the completion-triage persona (optional; see Notifier)
  memory.md              # a context: file the scaffold wires in by default
  agents/<name>.md       # subagents; the file IS the registration
  projects/<name>/       # a project: project.md brief + manager.md subagent (+ skills/)
  projects.md            # the agent's standing project index
  skills/<name>.md       # skills; drop a file in, reload
  hooks/tool-call.sh     # command gate for the main agent
  hooks/<name>.tool-call.sh   # command gate for subagent <name>
  hooks/*.tool-result.sh # output rewriters (same per-agent split)
  cron/<name>.md         # scheduled jobs
```

`--config`/`-c` takes a path to a config directory; the default is
`~/.shell3`. The working directory is never consulted, so behavior doesn't
depend on where you launch from.

Secrets are referenced from YAML as `env:KEY`, resolved from the `.env`
beside `shell3.yaml`, anywhere inside a string value (`"Bearer env:LINEAR_KEY"`
works). A reference naming a missing key fails the load.

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
    # temperature: 0.7             # omitted = provider default
    # max_tokens: 4096             # cap on a single reply; omitted = adapter default
```

Set `context_window` to the model's actual budget; a wrong number skews the
context-usage reminders and the compaction trigger.

### Context management

When a session's prompt crosses `compact_at` tokens, shell3 summarizes the
head of the conversation and keeps a verbatim recent tail. This is
host-managed; there are no model-driven prune/compact tools. Two optional
knobs:

```yaml
    keep_recent: 33000   # verbatim tail (tokens); default compact_at * 0.33;
                         #   a value ≥ compact_at is clamped to compact_at / 2
    prune_at: 60000      # cheaper first tier: stub old tool outputs, no LLM call
                         #   default compact_at * 0.6; 0 (or ≥ compact_at) disables;
                         #   setting it without compact_at is a load error
```

An agent can skip the prune tier with `prune: false` in its frontmatter;
omitted/`true` inherits.

Each inbound message starts its own session, so long histories only build up
in a thread you keep replying into. Compaction happens silently; `/status`
reports the live session's context window and message count.

### Provider-specific knobs — `extra`

Keys in `extra` are injected verbatim into the top-level request JSON:

```yaml
    extra: { reasoning_split: true }                 # MiniMax: thinking → reasoning_content
    extra: { verbosity: high }                       # gpt-5-style verbosity
    extra: { provider: { order: [anthropic] } }      # OpenRouter routing (nesting works)
```

Only set it when needed — strict endpoints reject unknown fields. Example:
MiniMax-M3 emits its chain-of-thought inline as `<think>…</think>` by
default; `extra: { reasoning_split: true }` routes it into the standard
`reasoning_content` field so shell3 renders it as reasoning instead of
leaking `<think>` tags into the answer.

### Local proxies — `run_proxy`

If a model needs a shim in front of its endpoint (a Codex subscription via
`npx`, a litellm gateway), set `run_proxy`. shell3 starts the command
detached, fire-and-forget, on the model's first use; logs go to
`~/.shell3/proxy-<model>.log`. If a proxy is already listening, the spawn
fails to bind and the first request proceeds against the existing one.

```yaml
models:
  codex:
    run_proxy: "npx @some/codex-proxy --port 8787"
    # run_proxy: "litellm --config ~/.shell3/litellm.yaml --port 8787"
    base_url: http://localhost:8787/v1
    # ...
```

## The agent — `agent.md`

One markdown file: frontmatter for the wiring, body for the system prompt.
There is exactly one agent because there is exactly one `agent.md`;
specialists are [subagents](#subagents--delegation).

```markdown
---
model: main
tools: [bash, bash_bg, edit, media, history]
context: [memory.md]
---
You are a personal assistant running inside shell3…
```

Frontmatter keys: `model` (required), `tools` (any of `bash`, `bash_bg`,
`edit`, `media`, `read`, `list_files`, `history`), `mcp` (see
[MCP](#mcp-servers)), `prune`, `context` (see below).

### Giving the agent a memory — `context:`

A new thread starts with no conversation history. `context:` gives the agent
a standing memory: a list of paths, relative to the config directory, globs
allowed:

```yaml
context: [memory.md, notes/*.md]
```

- Each file's contents are appended to the system prompt under a `## Context`
  heading, one `### <path>` sub-section per file.
- Files are read fresh at session creation, not at config load: edit
  `memory.md` in one thread and the next message sees the change, no reload
  needed.
- A literal entry that doesn't exist fails config load. A glob matching zero
  files is legal (`shell3 health` warns). A file that disappears between load
  and session build gets a `(context file missing: <path>)` stub in the
  prompt, never a turn failure.
- List order is preserved; a glob's matches are sorted lexically within its
  entry.
- Main agent only — subagent frontmatter rejects `context`. `projects.md` is
  a separate mechanism (read at config load, not per session).

`shell3 boot` scaffolds `context: [memory.md]` plus a starter `memory.md`.

The main agent is **bash-first**: it reads with `cat`/`sed -n`, lists with
`ls`/`find`, searches with `rg`, all through `bash`. A hallucinated
`read_file`/`grep` call gets an error redirecting it back to bash/edit_file.
The `read` and `list_files` tools exist as an opt-in for agents that do
better with structured file tools (typically a subagent on a smaller model);
list them in `tools` to enable them. A read-only agent is a policy, not a
tool set: gate `bash` in its [hook script](#the-command-gate--hookssh).

### The history tool

`history` in `tools` gives an agent recall over the whole runs store: every
conversation ever stored, across all sessions, full-text-indexed (FTS5,
user and assistant text only — tool output is not indexed). Two calls:

- `{"query": "certificate renewal"}` — ranked search across all sessions;
  bare words AND together, `"quoted phrases"` match exactly, `OR`/`NOT`/
  `prefix*` work.
- `{"session": "<id>", "around": <seq>}` — read the transcript around a hit.

Read-only by construction, and it goes through the tool-call hook like
everything else (`name` is `history`, `command` null). The scaffold enables
it on the main agent; subagents get it only if their frontmatter says so.

## Subagents & delegation

A subagent is a delegatable specialist: one file in `agents/`. The filename
is its name and the file is the registration; the `task` tools appear
automatically as soon as any subagent (or project manager) exists, with no
toggle. `description` is required: it's what the main model reads when
deciding to delegate. `agent` and `notifier` are reserved names.

```markdown
---
description: Read-only investigation of the codebase. No edits.
tools: [bash]
---
You are a focused code explorer…
```

`model` is optional (defaults to the main agent's). With at least one
subagent, the agent gets four tools: `task` (spawn: `{subagent_type, prompt,
description}`; returns immediately), `task_list`, `task_status <id>`,
`task_cancel <id>`. Subagent names and descriptions are baked into the `task`
tool's schema.

A spawned subagent is an in-process background job (a child-session
goroutine, not a subprocess). Subagents run headless (an `ask` gate verdict
auto-denies), and delegation is single-level: a subagent never gets the
`task` tool.

`bash_bg` runs on the same job runtime, enabled separately by `bash_bg` in
`tools`. Completions are triaged by the
[notifier](#the-notifier--notifiermd), and recorded in the runs store and
the jobs list whatever it decides. Both `task` and `bash_bg` accept two
extra args:

- `direct: true` skips the notifier: the spawning agent is woken with the
  completion notice. The right choice when the user asked for the work and is
  waiting on it.
- `note: "…"` rides along as triage context ("the user is waiting on this")
  for jobs the notifier judges.

A bash_bg job's full output is persisted to
`.shell3_project/runs/<session>/jobs/<id>.log` (capped at 1 MiB, swept with
its run) so the notifier and `task_status` can read past the in-memory tail.

A subagent's still-running `bash_bg` job keeps its session open past its main
turn; each completion resumes the subagent for a follow-up turn whose summary
is triaged like any completion — or, for a `direct` job, delivered straight
to the main agent. Follow-ups are capped at 5 per subagent; past the cap, or
after a cancel, the raw job event is triaged instead, so no completion is
lost. `task_cancel <sub-id>` cascades to the jobs that subagent started. One
global knob caps concurrency:

```yaml
background:
  max_concurrent: 8    # concurrent background jobs (default 8)
```

## Projects — `projects/`

A **project** groups long-running work under a dedicated manager. It's a
`projects/<name>/` directory with two files, plus an optional `skills/`:

```
projects/site/
  project.md         # the brief: frontmatter (description, workdir) + body
  manager.md         # the manager: a subagent named after the project
  skills/<name>.md   # optional; reach only this manager
```

`project.md`'s frontmatter requires `description` and `workdir` (`~/` is
expanded; the directory must exist). The body is the brief the manager reads
when it opens the project:

```markdown
---
description: The marketing site
workdir: ~/code/site
---
# site
State the goal, the current status, and what's next. Keep it short — deep
memory goes in sibling files in this folder.
```

`manager.md` is a subagent, parsed exactly like an `agents/<name>.md` file
but named after the project and run with its shell in the project's `workdir`
(not the config dir). Managers join the same flat subagent namespace as
`agents/`, so a project name that collides with a subagent — or the reserved
names `agent` and `notifier` — is a load error. Per-project `skills/` reach
only that manager; global `skills/` stay main-agent-only.

Scaffold a project with
[`shell3 project new`](cli.md#shell3-project-new--scaffold-a-project); it also
appends an index line to `projects.md`. That file is the agent's standing
project index — its body is injected into the main agent's system prompt so
every new thread knows which projects exist and which manager owns each.
Register a new manager with `/reload` or a restart; `shell3 health` validates
and lists every project.

## Scripts & secrets

There is no custom-tool declaration: reusable glue is a **script** the agent
runs through `bash`, documented by a skill when it needs one. The scaffold's
`scripting` skill teaches the pattern — reusable scripts live in
`~/.shell3/lib/bin/`, and a script that needs an API key reads it from
`~/.shell3/.env` itself, at point of use:

```bash
key="$(grep '^WEATHER_API_KEY=' ~/.shell3/.env | cut -d= -f2-)"
```

The secret enters exactly one process for exactly one call and never appears
in the conversation. Pair it with the hook example's `.env` deny and, if you
like, a [`tool-result.sh`](#output-rewriting--tool-resultsh) redaction as
backstop. More in [security.md](security.md).

## MCP servers

For tools behind the [Model Context Protocol](https://modelcontextprotocol.io),
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

Servers connect at startup (and on reload), in parallel, each under its own
timeout. Their tools join the opted-in agents' tool lists as
`mcp_<server>_<tool>` (`mcp_github_search_issues`). A server that is down at
startup is a warning (shell3 runs, that server's tools are absent until the
next reload); `shell3 health` treats it as a failure. `/status` lists every
server: up/down, tool count, last error. At call time a dead server gets one
automatic reconnect; if that fails the model sees the error as tool output,
so a broken server never kills a turn.

MCP calls flow through the same [tool-call hook](#the-command-gate--hookssh)
as everything else: `name` is the prefixed tool name and `command` is null,
so gate them by name.

## The command gate — `hooks/*.sh`

shell3 gives the model a real shell; the hook script is what limits it. A
scaffolded config ships with the gate armed
([security.md](security.md#the-armed-scaffold-gate)); an agent with no hook
file runs ungated. Hooks are per-agent, with no fallback or chaining: each
agent is governed by exactly one script per kind, or none.

- `hooks/tool-call.sh` governs the main agent.
- `hooks/<name>.tool-call.sh` governs subagent `<name>` (including when cron
  dispatches it). A subagent with no hook file runs **ungated**; the main
  hook never applies to it.

A hook file whose `<name>` matches no subagent is a warning (`shell3 health`
fails on it — it's usually a typo). `/status` reports whether the main gate
is armed.

Every tool call (`bash`, `bash_bg`, `edit_file`, `read_media`, host tools
like `image_generate`, and `mcp_*`) runs the governing script as
`bash hooks/….sh` with JSON on stdin:

```json
{"name": "bash", "command": "rm -rf /", "args": "{…}", "headless": false}
```

| Field | Description |
|-------|-------------|
| `name` | The tool name: `"bash"`, `"bash_bg"`, `"edit_file"`, `"read_media"`, `"image_generate"`, `"mcp_…"`. |
| `command` | The bash command string — the two bash tools only; **null** for every other tool. |
| `args` | Raw arguments JSON (every tool). Gate non-bash tools by inspecting this. |
| `headless` | `true` when no human is attached (subagents, cron jobs) — an ask verdict would auto-deny. |

The script prints a verdict to stdout:

| Output | Effect |
|--------|--------|
| empty or `{}` | Run. |
| `{"block": true, "reason": "…"}` | Block; `reason` goes to the model. Any tool. |
| `{"ask": "prompt", "reason": "…", "ask_timeout": N}` | Ask a human (Allow/Deny buttons in the chat); declined/headless/timeout → block. Any tool. `ask_timeout` bounds the wait (default 300 s). |
| `{"command": "…"}` | Rewrite the bash command. Bash tools only — fails closed elsewhere. |
| `{"argv": ["…"]}` | Exec exactly this argv (runner swap). `bash`/`bash_bg` only. |

A script that exits nonzero, prints malformed JSON, or runs past 10 s fails
**closed** (blocks, with the failure as the reason). The script's cwd is the
config directory. Compose everything in the one script; there is no chain.

The scaffold's `hooks/tool-call.sh` ships armed — what it refuses, and why,
is in [security.md](security.md#the-armed-scaffold-gate). `jq` keeps the JSON
handling clean:

```bash
in=$(cat)
name=$(printf '%s' "$in" | jq -r .name)
cmd=$(printf '%s' "$in" | jq -r '.command // empty')
if [ "$name" = "bash" ] || [ "$name" = "bash_bg" ]; then
  case "$cmd" in
    *'rm -rf /'*|*mkfs*|*'dd if='*)
      printf '{"block": true, "reason": "hard_deny"}'; exit 0 ;;
    *'git push'*)
      printf '{"ask": "Run?\n%s", "reason": "denied"}' "$cmd"; exit 0 ;;
    *.env*)
      printf '{"block": true, "reason": "read secrets via a lib/bin script (scripting skill)"}'; exit 0 ;;
  esac
fi
exit 0
```

There's no allowlist by default: ordinary reads (`cat`, `rg`, `ls`) match
nothing and just run. A hook is any program bash can start — exec into Python
if a gate outgrows shell.

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

The post-execution hook: `hooks/tool-result.sh` (main agent) /
`hooks/<name>.tool-result.sh` (subagent) receives
`{"name": …, "args": …, "output": …}` on stdin; print `{"output": "…"}` to
replace what the model sees, `{}` or nothing to pass through. The main use is
secret redaction:

```bash
in=$(cat)
printf '%s' "$in" | jq -c '{output: (.output | gsub("API_KEY=\\S+"; "API_KEY=[redacted]"))}'
```

A failing script here also fails **closed**: the tool output is replaced by
an error notice, never passed through unredacted. Background jobs (`bash_bg`)
are out of scope — the hook sees only the "started job…" pointer, not the
streamed output — so redact at the source if a background command can emit
secrets.

## Telegram — `telegram:`

The bot's credentials, the one chat it answers, and where the agent's shell
runs.

```yaml
telegram:
  token: env:TELEGRAM_TOKEN         # TELEGRAM_TOKEN in .env, from @BotFather
  chat_id: "8701499393"             # the single chat the bot answers
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

```yaml
media:
  stt: { model: groq-whisper, echo: true }        # voice notes → text
  tts: { model: groq-tts, voice: Fritz-PlayAI, mode: inbound }
  describe: { model: some-vision-model }          # for text-only main models
  imagegen: { model: some-image-model, size: 1024x1024 }
```

- **`stt: { model, language?, echo? }`** transcribes an inbound voice note
  before the turn and injects the transcript as the message. The recording is
  stored under `~/.shell3/media/` (as `tg-*`) and its path goes into the
  prompt too. `echo: true` also posts the transcript back to the chat. A
  failure injects a could-not-transcribe marker and posts a ⚠️ notice; the
  turn still runs.
- **`tts: { model, voice?, format?, mode? }`** speaks the reply instead of
  posting it as text. `mode` is the default: `off`, `inbound` (speak only
  when the message came in as a voice note), or `always`; `/voice` overrides
  it at runtime and the override persists in `~/.shell3/voice_mode.json`.
  `voice` and `format` are passed to the model; an `opus`/`ogg` result is
  sent as a Telegram voice bubble, anything else as an audio file.
  Synthesized audio is cached under `~/.shell3/media/` (as `tts-*`). Any
  failure falls back to text, so a reply is never lost.
- **`describe: { model, prompt? }`** captions an inbound photo before the
  turn, injecting `[image: <description>]`. Point it at a vision model when
  the main model is text-only, or at the main model itself (`shell3 boot`
  wires this when you say your model has vision). Every file you send is
  stored under `~/.shell3/media/` and its path goes into the prompt, so the
  agent can re-open it later with `read_media` either way.
- **`imagegen: { model, size?, api? }`** adds an `image_generate{prompt,
  size?}` tool to **every** agent, subagents included. `api: openai`
  (default) uses `images/generations`; `openrouter` uses OpenRouter's
  chat-completions image dialect instead (`size` is ignored there — details
  in [cookbook/voice-images.md](cookbook/voice-images.md)). Generated files
  land in `~/.shell3/media/` and the tool returns the path; the main agent
  delivers it with `send_media_telegram` (kind `photo`), while a subagent
  reports the path for the parent to send. Gate it like any tool
  (`name == "image_generate"` in the hook payload).

**Media storage.** Everything you send the bot (`tg-*`), generated images
(`img-*`), and synthesized speech (`tts-*`) live in `~/.shell3/media/` —
stable paths, re-readable with `read_media` and re-sendable with
`send_media_telegram` long after the message has scrolled away. The folder
grows until you prune it.

**`read_media` modalities** (needs `media` in the agent's `tools`): images
(`.jpg/.jpeg/.png/.gif/.webp`, vision models), audio
(`.wav/.mp3/.ogg/.opus/.oga`, audio models), PDFs (`.pdf` ≤ 20 MB, an
OpenAI-compatible `file` part — works on OpenAI and OpenRouter), and video
(`.mp4/.webm/.mov` ≤ 40 MB, a `video_url` part — an OpenRouter/Gemini
extension plain OpenAI endpoints reject; OpenRouter additionally requires at
least $1.00 of account balance for any request carrying video).

Provider recipes — a one-key Groq quickstart for STT+TTS, the OpenRouter
variant — are in [cookbook/voice-images.md](cookbook/voice-images.md).

## Scheduled jobs — `cron/`

One file per job; the filename is the job name. Each fires a declared agent
on `schedule` (cron expression or `@daily`/`@hourly`/…), with the body as its
prompt. `agent` names either a subagent from `agents/` or a project's
`manager.md` — a project's cron job runs its manager in that project's
workdir. The scheduler runs inside `shell3 telegram`, dispatching each job
from a hidden, pinned `cron` parent session.

```markdown
---
schedule: "@daily"
agent: explorer
# direct: true          # optional; skip the notifier (see below)
# workdir: /some/path   # optional; defaults to the dispatched agent's own
---
Summarize anything noteworthy from the last day.
```

A cron run's result goes to the [notifier](#the-notifier--notifiermd), which
decides per run whether to post it (a ⏰ message titled with the job name) or
stay silent. A periodic checklist therefore only speaks up when something
needs attention: write its prompt to report findings plainly and let the
notifier silence the all-quiet runs. A failed run always surfaces as an
alert, whatever the notifier does.

`direct: true` skips the notifier: the result is handed straight to the main
agent as a fresh main-agent turn in a new thread, and the agent's reply comes
back as a notification — for jobs whose results should be acted on, not just
reported. `workdir` overrides the job's working directory (a project manager
otherwise runs in its project's `workdir`, everything else in the config
dir). A reload arms changed files.

`/cron` lists every job with its schedule, agent, workdir, `direct` flag,
full prompt, and last run; `/run <name>` fires one by hand, through the usual
notifier route.

The scaffold ships an example as `cron/checklist.md.example` — drop the
`.example` suffix and reload to activate it.

## The notifier — `notifier.md`

Every background completion (a `bash_bg` exit, a subagent's result, a
lingering follow-up, a cron run) funnels through one reserved persona:
`notifier.md` beside `agent.md`.

```markdown
---
model: mini            # any declared model; a small one is ideal
---
You are the notifier … (the triage policy, in plain prose)
```

Per completion it runs one small headless turn over the event: kind, job id,
title, exit status, output tail, the spawner's `note:` (for cron runs, the
job's own prompt), and the on-disk output path. Its toolset is fixed: `read`
and `list_files` to inspect further, plus two verdict tools. `send {text}`
posts a notification to you; `wake {note}` hands the result to the main
agent, resuming the owning session or running a fresh thread when there is
none. Calling neither means **silent**: the result stays in the run's stored
transcript.

The hard rules live in the host, not the prompt: a failed job the notifier
leaves silent posts a `<label> failed: <error>` alert anyway; one completion
triggers at most one wake; a triage turn is timeboxed (60 s) and degrades to
a raw post on error or timeout; and with no `notifier.md` at all, every
completion posts raw (deterministic, zero tokens; `shell3 health` points
this out). The notifier is hookable like any agent
(`hooks/notifier.tool-call.sh`) and its turns are ordinary stored runs, so
you can audit why something was silenced. Tune the policy by editing
`notifier.md` — or tell the agent to.

## The runs janitor — `runs_keep_days`

History is kept **forever by default** — it's one database, and the history
tool's recall is the point. An optional top-level `shell3.yaml` key bounds
it:

```yaml
runs_keep_days: 30   # default 0 = keep forever
```

At `shell3 telegram` startup (before the bot connects, never on
`shell3 ask`) a sweep deletes sessions whose last activity is older than the
cutoff — their messages, search-index entries, thread entries, and job-log
files together — plus empty crash leftovers and orphaned `runs/<id>/`
directories from older versions. It prints one line, `janitor: removed N
runs, M thread entries` (silent when both are zero), and a janitor fault is
a warning, never a reason to refuse startup. Start-time only; no daemon, no
timers.

## Skills — `skills/`

A skill is a plain `.md` file the agent reads with `cat` when relevant: no
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
warning; `shell3 health` hardens those into errors. Skills are indexed by
absolute path in the system prompt under `## Skills`. Subagents carry no
skills; put a subagent's standing instructions in its prompt body.

## Putting it together

Read the tree `boot` writes (`~/.shell3/`) for a full example; the
[cookbook](cookbook/README.md) has drop-in extras. Validate any edit with
`shell3 health` before reloading.
