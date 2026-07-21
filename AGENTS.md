# shell3

Minimal Unix-composable personal agent written in Go.

**Declarative config.** The config is a **directory** (default `~/.shell3/`),
loaded by `internal/config` — four rules: YAML wires it, markdown prompts it,
files enable it, one bash script gates it. `shell3.yaml` holds wiring only
(`models:`, `telegram:`, `web:`, `mcp:`, `media:`, `background:`; strict
decode — unknown keys fail the load; secrets referenced as `env:KEY`,
substring-substituted from the sibling `.env`, unknown key = load error).
Everything with a prompt is markdown-with-frontmatter: `agent.md` (THE agent —
exactly one because there is exactly one file; frontmatter `model` (required),
`tools: [bash, bash_bg, edit, media]`, `mcp`, `prune`; body = system prompt,
name fixed to "agent"), `agents/<name>.md` (subagents — filename is the name,
required `description` routes the task tool, model defaults to the main
agent's), `skills/<name>.md` (skills — main agent only, subagents carry none),
`cron/<name>.md` (frontmatter `schedule`/`agent`/`notify`; body = prompt),
`heartbeat.md` (frontmatter `every`/`active`; body = checklist; the file
existing arms it). A leftover `shell3.lua` without `shell3.yaml` produces a
migration error pointing at `shell3 boot`; there is no Lua anywhere.

**Bash-first.** The agent's verbs are `bash`, `bash_bg`, and `edit_file` (plus
`read_media` — attach an image, audio, PDF, or video file so a multimodal
model can perceive it (PDF via an OpenAI-compatible `file` part; video via a
`video_url` part, an OpenRouter/Gemini extension plain OpenAI endpoints
reject) — when `media` is in the agent's `tools`). There are NO file-read
tools: reading, listing, and searching are bash commands (`cat`/`sed -n`,
`ls`/`find`, `rg`; history is searched with `rg` over
`.shell3_project/runs/**/*.jsonl`), and a reflexive
`read`/`read_file`/`grep`/`write_file` call gets an unknown-tool error
carrying a bash-first redirect back to bash/edit_file. Specialists are
subagents. A **subagent** is an **in-process background job** spawned via the
`task` tool (`{subagent_type, prompt, description}`; returns immediately); the
runtime (`internal/shell3` jobManager) runs it as a child-session goroutine
under a concurrency cap (`background.max_concurrent`, default 8) and, on
completion, **wakes the parent with a capped result summary** injected into
context — no subprocess, no inbox file, no fsnotify. `bash_bg` is a background
shell command on the same runtime (no pid / log path to poll): completion
**wakes** an idle agent with a notice carrying an output tail (`quiet: true`
opts a job into queueing clean exits for the next turn instead — servers,
watchers; nonzero exits always wake so failures surface proactively). Foreground `bash` is capped at 120s
(`timeout_seconds`) precisely because it blocks the turn — longer work
belongs in `bash_bg`. A subagent may run `bash_bg` jobs of
its own; a job that outlives the subagent's main turn keeps the child session
open ("lingering"), and each completion **resumes the subagent for a follow-up
turn** whose summary reaches the root as an `agent_update` notice (always
wakes; capped at 5 follow-up turns per subagent, after which — or after
cancel/failure — the raw job notice is delivered to the root instead, so a
completion is never lost). `task_cancel <sub>` cascades to the jobs the
subagent started. `Runtime.Reload` refuses while any background task is
running (`/stop` first). Delegation is **single-level by construction** — a
subagent is never given the `task` tool (subagent frontmatter has no way to
express delegation), so subagents can't spawn subagents; there is no depth
field anywhere. Delegation itself is **inferred**: the four task-family tools
(`task`, `task_list`, `task_status <id>`, `task_cancel <id>`; ids like
`sub1`/`bg1`) are advertised iff `agents/` is non-empty — a file in `agents/`
IS the registration, there is no toggle and no allowlist key.

The Mini App dashboard's jobs/runs views list running + finished jobs (and
each subagent's stored transcript); the job-progress stream is
`rt.JobEvents()` / `Session.JobEvents()`. The shell is **unsafe by default**;
the opt-in gate is a **per-agent bash hook script**: `hooks/tool-call.sh`
governs the main agent, `hooks/<name>.tool-call.sh` governs subagent `<name>`
— no fallback, no chaining; an agent with no script runs ungated (a `<name>`
matching no subagent is a warning; `shell3 health` fails on it). The script
runs before **every** tool as `bash <path>` (cwd = config dir, 10s timeout)
with JSON on stdin — `{"name", "command" (bash text for the two bash tools,
null otherwise), "args", "headless" (true when no human asker is attached —
subagents, cron — so an ask auto-denies)}` — and prints a verdict: empty/`{}`
(run) / `{"command": …}` (rewrite — bash tools only) / `{"argv": […]}`
(runner-swap — bash tools only; fails closed for non-bash) /
`{"block": true, "reason": …}` / `{"ask": "prompt", "reason": …}` (human
prompt — Telegram/web Allow/Deny buttons; decline/headless → block).
Precedence when several keys are set: block > argv > ask > command. Nonzero
exit, malformed JSON, or timeout **fails closed**. `hooks/tool-result.sh` /
`hooks/<name>.tool-result.sh` can rewrite a tool's output (e.g. redact
secrets): stdin `{"name","args","output"}`, stdout `{"output": …}`; a failure
here also fails closed (output replaced by an error notice, never passed
through unredacted). The scaffold's example gates ship **commented out** — a
fresh config gates nothing.

`edit_file`'s file I/O lives in `internal/edittool` (plain direct-disk
functions); `bash` always hits disk directly. Skills are **dir-based**: every
flat `*.md` in `skills/` with a frontmatter `description:` (optional `name:`
defaults to the filename) is one skill. An invalid file is skipped with a
warning that `shell3 health` turns into a failure; an absent dir means no
skills. The agent reads a skill's body with `cat` (skills are indexed by
absolute path in the prompt under `## Skills` — there is no `skill` tool).
There is **no custom-tool declaration**: reusable glue is a wrapper script
(canonically `~/.shell3/lib/bin/`) run through bash, documented by the
scaffold's `scripting` skill; a script that needs a secret reads the one key
it needs from `.env` itself at point of use, so secrets never enter the
conversation or the agent environment. External tool servers come in over
**MCP** (`internal/mcp`, official go-sdk, tools only — stdio + streamable
HTTP, no OAuth/resources/prompts/SSE): the `mcp:` block in `shell3.yaml`
(`command:` argv or `url:` + `headers:`; per-server `timeout`, `allow`/`deny`
tool filters), opted into per agent via frontmatter `mcp: [name, …]` or
`mcp: all` (omitted = none). Servers connect synchronously in BuildParts
(parallel, per-server timeout; down server = warning + tools absent, never a
build failure; the Manager's Close rides the Parts closer stack so /reload
reconnects fresh). Tools surface as `mcp_<server>_<tool>` in the opted
personas' tool lists and dispatch through the session HostTool path; calls
get one reconnect retry, then the error returns as tool-result text (never
fatal to a turn). The hook sees them like any tool (`name` prefixed,
`command` null). `shell3 health` connects and fails on any down server, and
dry-runs every hook script with a probe payload (script error = failure; a
deliberate block is fine); the dashboard Status view lists per-server state.
Context is host-managed via two token thresholds: `prune_at` cheaply stubs
old tool outputs (no LLM call), and `compact_at` triggers tail-preserving
compaction — summarizing the head while keeping recent turns verbatim. The
`prune_at` and `keep_recent` knobs are optional, defaulting to fractions of
`compact_at`; no model-driven prune/compact tools.

**Telegram-first.** shell3 is a hosted agent you reach over Telegram.
`shell3 telegram` runs the bot (`internal/telegram`): one authorized chat id,
inline Allow/Deny buttons for hook asks, media in/out — optional voice +
image capability (`internal/media`, four blocks under `media:` in
`shell3.yaml`, each pointing at a model: `stt`/`tts` transcribe inbound voice
notes and speak replies back per a `/voice off|inbound|always` mode,
`describe` captions inbound images before the turn — pointed at a vision
model for text-only mains, or at the main model itself to skip a `read_media`
round-trip (boot's default when the model has vision), `imagegen` adds an
`image_generate` tool for the main agent AND every subagent, registered via a
runtime session decorator (`Runtime.SetSessionDecorator`; reapplied on
Reload) under all front-ends (`api: openai` or `openrouter`, the latter a raw
chat-completions POST with `modalities=["image","text"]`, OpenRouter's
image-output dialect — its dedicated `/api/v1/images` endpoint is avoided
because it pre-authorizes worst-case cost and 402s low balances); all media —
inbound Telegram uploads (`tg-*`) and generated images (`img-*`) — is stored
under `~/.shell3/media/` so every file keeps a durable path (TTS audio
excepted: sent and deleted); restriction policy is the hook script, not a
tools list) — `/stop` `/reload` `/run`, an in-process cron scheduler
(`internal/cron`, jobs are `cron/<name>.md` files; each job dispatches a
declared subagent), a **heartbeat** (`internal/heartbeat`, declared via
`heartbeat.md`; each idle in-window tick Interjects the checklist prompt into
the MAIN session — full context, unlike cron's fresh subagents — and the bot
suppresses replies whose edge carries the `HEARTBEAT_OK` sentinel, so the
chat only hears real alerts; busy/out-of-window ticks are skipped, `/reload`
rearms, and `shell3 dev --heartbeat` fires one tick locally with a
suppression verdict), and a Mini App **dashboard** (`internal/web`, Telegram
initData auth) with status / past runs / subagent transcripts / jobs / cron /
a read-only file explorer (`.env` is redacted, never read from disk). The
dashboard gets a public https URL via `dashboard.tunnel` (e.g.
`cloudflared tunnel --url http://{addr}`) — `internal/tunnel` spawns the
command detached, scrapes the first bare https URL from its output (log:
`~/.shell3/tunnel.log`), and the bot auto-sets the Mini App menu button
(`setChatMenuButton`); an explicit `dashboard.url` overrides. `shell3 boot`
scaffolds the config tree (an interactive form: model, context budget,
whether the model has vision — which wires `media.describe` + the media tool
— bot token, and chat id) and writes secrets to `~/.shell3/.env`. Two local
dev front-ends live in `internal/cli`: `shell3 dev "…"` drives the bot's
agent from the terminal with full verbose output (every tool call/result,
reasoning, token usage; `--resume` continues the last session), and
`shell3 dash` serves the dashboard locally with auth bypassed (localhost
only) for troubleshooting. `shell3 web` is the **Telegram-free fallback
host**: the same dashboard plus a simple chat (send box, Stop, Allow/Deny ask
cards, polling transport, and the bot's slash commands — typing `/` pops a
filtered command list; replies render as ephemeral notices, never history),
gated by a shared secret from `.env` (the `web:` block: addr, secret,
tunnel/url; `X-Auth-Token` header / one-time `?key=` — `shell3 boot`
generates `SHELL3_WEB_SECRET` and startup prints the ready-to-open keyed
URL). It resumes the latest stored session and arms cron (not heartbeat); run
one front-end at a time. The chat loop lives in `internal/web`'s `Driver`;
the server takes a pluggable `AuthFunc` (initData / token / no-auth).

## IMPORTANT: Do Not Read Credential Files

Secrets and credentials (provider API keys, tool tokens) live in a plain
`.env` file beside the active `shell3.yaml` (e.g. `~/.shell3/.env`),
referenced from YAML as `env:KEY`. Never read, display, or include the
contents of any credential file in a response. This applies to all agents,
assistants, and automated tools.

- `.env` beside `shell3.yaml` (e.g. `~/.shell3/.env`) — provider API keys, base URLs, tool secrets

## Project Layout

```
cmd/shell3/            cobra command tree: root (prints help) + telegram/web/dev/dash/boot/health subcommands
internal/agentsetup/   shared config assembly (BuildParts → chat.Config) used by every front-end
internal/config/       config-directory loader (shell3.yaml + agent/skill/cron/heartbeat markdown + hooks/*.sh) + system-prompt assembly
internal/bootstrap/    first-run global + project setup
internal/scaffold/     embedded starter config tree (shell3.yaml.tmpl, agent.md.tmpl, agents/, skills/, hooks/) + boot rendering
internal/adapter/openai/  OpenAI-compatible LLM adapter
internal/modelproxy/   run_proxy spawner (starts a model's proxy command on activation)
internal/paths/        global (~/.shell3/) + local (.shell3_project/) path resolution; no DB fields
internal/runs/         file-native JSONL store: sessions at .shell3_project/runs/<id>/
internal/edittool/     edit_file tool implementation (Go port of opencode's str-replace) + its direct-disk file I/O
internal/notify/       Notification type (bg_done / agent_done) shared by job runtime + chat
internal/tunnel/       dashboard.tunnel spawner: runs the tunnel command, scrapes its https URL
internal/media/        media.stt/tts/describe/imagegen clients (transcribe, speak, describe, generate) + the /voice mode store
internal/mcp/          MCP client (official go-sdk): Manager connects mcp: servers, lists tools, dispatches mcp_* calls
internal/telegram/     Telegram bot front-end (bot loop, commands, confirm buttons, media, mdhtml)
internal/web/          dashboard + chat API server (pluggable auth) and the shell3 web chat driver; static/ is the single-file frontend
internal/cron/         robfig/cron scheduler dispatching subagent jobs on Session.Dispatch
internal/heartbeat/    heartbeat.md engine: tick prompt, active-hours window, HEARTBEAT_OK strip, idle-skip ticker
internal/cli/          non-interactive front-end helpers: shell3 dev + dash renderers, brand banner
internal/chat/         conversation loop, tools, events, JSONL audit sink
internal/llm/          Provider/Streamer interfaces, request params, types (+ fakellm)
internal/persona/      runtime carrier for an agent's prompt/tools/params (data only)
internal/strutil/      rune-safe string truncation helpers (byte-cap + rune-count) shared by runtime and front-ends
internal/applog/       rotating app log
internal/shell3/       session/runtime core consumed by the front-ends; jobs.go hosts the in-process job runtime (subagents + bash_bg)
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
