# shell3

Minimal Unix-composable coding agent written in Go.

**Bash-first.** The agent's verbs are `bash`, `bash_bg`, and `edit_file` (plus
`read_media` — attach an image, audio, PDF, or video file so a multimodal
model can perceive it (PDF via an OpenAI-compatible `file` part; video via a
`video_url` part, an OpenRouter/Gemini extension plain OpenAI endpoints
reject) — when `tools = { media = true }`). There are NO file-read tools: reading,
listing, and searching are bash commands (`cat`/`sed -n`, `ls`/`find`, `rg`;
history is searched with `rg` over `.shell3_project/runs/**/*.jsonl`), and a
reflexive `read`/`read_file`/`grep`/`write_file` call gets an unknown-tool
error carrying a bash-first redirect back to bash/edit_file. Exactly
**one** `shell3.agent{}` may be declared (a second declaration fails the load);
specialists are subagents. A **subagent** is an **in-process background job**
spawned via the `task` tool (`{subagent_type, prompt, description}`; returns
immediately); the runtime (`internal/shell3` jobManager) runs it as a child-session
goroutine under a concurrency cap (`shell3.background{max_concurrent=N}`, default
8) and, on completion, **wakes the parent with a capped result summary** injected
into context — no subprocess, no inbox file, no fsnotify. `bash_bg` is a background
shell command on the same runtime (no pid / log path to poll): a clean exit queues
its notice for the agent's next turn, a **nonzero exit wakes** an idle agent so
failures surface proactively. A subagent may run `bash_bg` jobs of its own; a job
that outlives the subagent's main turn keeps the child session open ("lingering"),
and each completion **resumes the subagent for a follow-up turn** whose summary
reaches the root as an `agent_update` notice (always wakes; capped at 5 follow-up
turns per subagent, after which — or after cancel/failure — the raw job notice is
delivered to the root instead, so a completion is never lost). `task_cancel <sub>`
cascades to the jobs the subagent started. `Runtime.Reload` refuses while any
background task is running (`/stop` first). Delegation is **single-level by
construction** — a subagent is never given the `task` tool (the `luacfg.Subagent`
shape has no subagents field), so subagents can't spawn subagents; there is no
depth field anywhere.
Active tasks are managed with `task_list`, `task_status <id>`, and
`task_cancel <id>` (ids like `sub1`/`bg1`); these three plus `task` itself — four
tools in all — are only advertised when the agent sets `delegation = true` and
`tools.subagents = { … }` (`bash_bg` is gated separately, by
`tools = { bash_bg = true }`).
The Mini App dashboard's jobs/runs views list running + finished jobs (and each
subagent's stored transcript); the job-progress stream is `rt.JobEvents()` /
`Session.JobEvents()`. The shell is
**unsafe by default**; the single opt-in hook that gates it is
`shell3.on_tool_call(fn)` — a chainable handler that runs before **every** tool with
the real `t.name` (`bash`/`bash_bg`/`edit_file`/`read_media`/custom;
`t.command` is the bash text for the two bash tools, nil otherwise; `t.headless`
is true when no human asker is attached — subagents, cron jobs — so an ask
would auto-deny) and returns a
verdict: `nil` (run) / `{command=...}` (rewrite, continue chain — bash tools only) /
`{argv={...}}` (runner-swap, terminal — `bash`/`bash_bg` only; fails closed for
non-bash) / `{block=true, reason=...}` (block) /
`{ask="prompt", reason=...}` (prompt a human — over Telegram, inline Allow/Deny
buttons; allow→run, decline/headless→block).
Denylists are written with `shell3.regex(pat):match(s)` (Go RE2; compiled at load;
use `(?s)` so `.*` spans newlines; match the whole command so chaining can't hide a
flagged fragment) — guard on `t.name` before matching `t.command` (nil for non-bash).
`shell3.on_tool_result(fn)` can rewrite a tool's output (e.g. redact secrets).
`edit_file`'s file I/O lives in `internal/edittool` (plain direct-disk
functions); `bash` always hits disk directly. The
scaffold's example gate ships **commented out** — a fresh config gates nothing —
and, once enabled, covers only the bash family, so `edit_file` runs
ungated (a config choice, not a hardcoded exemption). Skills
are **dir-based**: an agent lists directories (`skills = { "lib/skills" }`,
resolved against the config dir) and every flat `*.md` inside with a
frontmatter `description:` (optional `name:` defaults to the filename) is one
skill — no Lua declaration. A missing dir fails the load; an invalid file is
skipped with a warning that `shell3 health` turns into a failure. The agent
reads a skill's body with `cat` (skills are indexed by absolute path in the
prompt under `## Skills` — there is no `skill` tool), and custom tools are declarative
bash-command templates (`shell3.tool{command=...}`, params injected as lowercase
env vars plus a `secrets` list; no Lua `handler`) — the
`shell3.bash`/`http`/`urlencode` helpers are gone. Context is host-managed via
two token thresholds: `prune_at` cheaply stubs old tool outputs (no LLM call),
and `compact_at` triggers tail-preserving compaction — summarizing the head while
keeping recent turns verbatim. The `prune_at` and `keep_recent` knobs are
optional, defaulting to fractions of `compact_at`; no model-driven prune/compact
tools.

**Telegram-first.** shell3 is a hosted agent you reach over Telegram.
`shell3 telegram` runs the bot (`internal/telegram`): one authorized chat id,
inline Allow/Deny buttons for `on_tool_call` asks, media in/out — optional
voice + image capability (`internal/media`, four top-level blocks pointing at
a `shell3.model`: `shell3.stt`/`shell3.tts` transcribe inbound voice notes and
speak replies back per a `/voice off|inbound|always` mode, `shell3.describe`
captions inbound images before the turn — pointed at a vision model for
text-only mains, or at the main model itself to skip a `read_media`
round-trip (boot's default when the model has vision), `shell3.imagegen` adds an
`image_generate` tool for the main agent AND every subagent, registered via a
runtime session decorator (`Runtime.SetSessionDecorator`; reapplied on Reload)
under all front-ends (`api = "openai"` or `"openrouter"`, the latter a raw
chat-completions POST with `modalities=["image","text"]`, OpenRouter's
image-output dialect — its dedicated `/api/v1/images` endpoint is avoided
because it pre-authorizes worst-case cost and 402s low balances); all media —
inbound Telegram uploads (`tg-*`) and generated images (`img-*`) — is stored
under `~/.shell3/media/` so every file keeps a durable path (TTS audio
excepted: sent and deleted); restriction policy is `on_tool_call`, not a
tools={} key) — `/stop`
`/reload` `/run`, an in-process cron scheduler (`internal/cron`, jobs declared
via top-level `shell3.cron({...})`; each job dispatches a declared subagent),
a **heartbeat** (`internal/heartbeat`, declared via top-level
`shell3.heartbeat({every=..., checklist=..., active={from,to,tz}})`; each idle
in-window tick Interjects the checklist prompt into the MAIN session — full
context, unlike cron's fresh subagents — and the bot suppresses replies whose
edge carries the `HEARTBEAT_OK` sentinel, so the chat only hears real alerts;
busy/out-of-window ticks are skipped, `/reload` rearms, and `shell3 dev
--heartbeat` fires one tick locally with a suppression verdict),
and a Mini App **dashboard**
(`internal/web`, Telegram initData auth) with status / past runs /
subagent transcripts / jobs / cron / a read-only file explorer (`.env` is
redacted, never read from disk). The dashboard gets a
public https URL via `dashboard = { tunnel = "cloudflared tunnel --url
http://{addr}" }` — `internal/tunnel` spawns the command detached, scrapes the
first bare https URL from its output (log: `~/.shell3/tunnel.log`), and the bot
auto-sets the Mini App menu button (`setChatMenuButton`); an explicit
`dashboard.url` overrides. Config lives at the root
`~/.shell3/shell3.lua`; `shell3 boot` scaffolds it (an interactive form: model,
context budget, whether the model has vision — which wires `shell3.describe` +
the media tool — bot token, and chat id) and writes secrets to
`~/.shell3/.env`. Two local dev
front-ends live in `internal/cli`: `shell3 dev "…"` drives the bot's agent from
the terminal with full verbose output (every tool call/result, reasoning, token
usage; `--resume` continues the last session), and `shell3 dash` serves the
dashboard locally with auth bypassed (localhost only) for troubleshooting.
`shell3 web` is the **Telegram-free fallback host**: the same dashboard plus a
simple chat (send box, Stop, Allow/Deny ask cards, polling transport, and the
bot's slash commands — typing `/` pops a filtered command list; replies render
as ephemeral notices, never history), gated by
a shared secret from `.env` (top-level `shell3.web{ addr, secret, tunnel/url }`;
`X-Auth-Token` header / one-time `?key=` — `shell3 boot` generates
`SHELL3_WEB_SECRET` and startup prints the ready-to-open keyed URL). It
resumes the latest stored session
and arms cron (not heartbeat); run one front-end at a time. The chat loop lives
in `internal/web`'s `Driver`; the server takes a pluggable `AuthFunc`
(initData / token / no-auth).

## IMPORTANT: Do Not Read Credential Files

Secrets and credentials (provider API keys, tool tokens) live in a plain `.env` file beside the active `shell3.lua` (e.g. `~/.shell3/.env`), read from Lua via `shell3.env.secret("KEY")`. Never read, display, or include the contents of any credential file in a response. This applies to all agents, assistants, and automated tools.

- `.env` beside `shell3.lua` (e.g. `~/.shell3/.env`) — provider API keys, base URLs, tool secrets

## Project Layout

```
cmd/shell3/            cobra command tree: root (prints help) + telegram/web/dev/dash/boot/health subcommands
internal/agentsetup/   shared config assembly (Build → chat.Config) used by every front-end
internal/luacfg/       Lua config loader (shell3.lua → model/agent/subagents/tools/skills, telegram, web, cron, heartbeat, on_tool_call) + system-prompt assembly
internal/bootstrap/    first-run global + project setup
internal/scaffold/     embedded starter shell3.lua (with shell3.telegram{}) + .env template
internal/adapter/openai/  OpenAI-compatible LLM adapter
internal/modelproxy/   run_proxy spawner (starts a model's proxy command on activation)
internal/paths/        global (~/.shell3/) + local (.shell3_project/) path resolution; no DB fields
internal/runs/         file-native JSONL store: sessions at .shell3_project/runs/<id>/
internal/edittool/     edit_file tool implementation (Go port of opencode's str-replace) + its direct-disk file I/O
internal/notify/       Notification type (bg_done / agent_done) shared by job runtime + chat
internal/tunnel/       dashboard.tunnel spawner: runs the tunnel command, scrapes its https URL
internal/media/        shell3.stt/tts/describe/imagegen clients (transcribe, speak, describe, generate) + the /voice mode store
internal/telegram/     Telegram bot front-end (bot loop, commands, confirm buttons, media, mdhtml)
internal/web/          dashboard + chat API server (pluggable auth) and the shell3 web chat driver; static/ is the single-file frontend
internal/cron/         robfig/cron scheduler dispatching subagent jobs on Session.Dispatch
internal/heartbeat/    shell3.heartbeat{} engine: tick prompt, active-hours window, HEARTBEAT_OK strip, idle-skip ticker
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
