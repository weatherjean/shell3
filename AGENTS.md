# shell3

Minimal Unix-composable personal agent written in Go.

> This file is the repo's standing context for coding agents (`CLAUDE.md`
> symlinks here). It is written for models, not people: dense, exhaustive,
> and kept in lockstep with the code, because shell3 is developed largely by
> the kind of agent it is. Humans want [README.md](README.md) and
> [docs/](docs/).

**Declarative config.** The config is a **directory** (default `~/.shell3/`),
loaded by `internal/config` — four rules: YAML wires it, markdown prompts it,
files enable it, one bash script gates it. `shell3.yaml` holds wiring only
(`models:`, `web:`, `mcp:`, `media:`, `background:`, `runs_keep_days`;
strict decode — unknown keys fail the load; secrets referenced as `env:KEY`,
substring-substituted from the sibling `.env`, unknown key = load error).
Everything with a prompt is markdown-with-frontmatter: `agent.md` (THE agent —
exactly one because there is exactly one file; frontmatter `model` (required),
`tools: [bash, bash_bg, edit, media, read, list_files, history]`, `mcp`, `prune`,
`context` (main-agent-only: a list of config-dir-relative paths, globs
allowed, read fresh at session creation — so every fresh turn sees current
file contents, not a load-time snapshot — into a `## Context` prompt section,
one `### <path>` sub-section per file; a missing literal entry is a load
error, a zero-match glob is legal (`shell3 health` warns), a file that
vanishes between load and session build gets a one-line
`(context file missing: <path>)` stub instead of failing the turn); body =
system prompt, name fixed to "agent"), `agents/<name>.md` (subagents —
filename is the name, required `description` routes the task tool, model
defaults to the main agent's), `projects/<name>/` (Chain of Command projects —
`project.md` brief with frontmatter `description`/`workdir` + body, plus
`manager.md`, a subagent named after the project run with its shell in
`workdir`, plus an optional manager-only `skills/`; managers join the flat
subagent namespace so a name collision or the reserved "agent" is a load
error; `projects.md` beside `shell3.yaml` is the standing portfolio brief,
appended verbatim to the main agent's system prompt),
`skills/<name>.md` (skills — main agent only, subagents carry none),
`notifier.md` (the reserved background-completion triage persona — optional;
frontmatter `model` only, tools/mcp/context rejected, body = triage policy;
"notifier" is reserved in `agents/` like "agent"; absent file = degraded
mode, every completion posts raw), `cron/<name>.md` (frontmatter
`schedule`/`agent`/`direct`; body = prompt; `agent` names either a subagent
from `agents/` or a project's `manager.md`, which then runs in that
project's workdir). There is no Lua anywhere, and no migration shims: an
unknown key is simply an unknown-key load error.

**Bash-first.** The agent's verbs are `bash`, `bash_bg`, and `edit_file` (plus
`read_media` — attach an image, audio, PDF, or video file so a multimodal
model can perceive it (PDF via an OpenAI-compatible `file` part; video via a
`video_url` part, an OpenRouter/Gemini extension plain OpenAI endpoints
reject) — when `media` is in the agent's `tools`). The main agent is bash-first
by default: reading, listing, and searching are bash commands (`cat`/`sed -n`,
`ls`/`find`, `rg`), and a reflexive
`read_file`/`grep`/`write_file` call gets an unknown-tool error carrying a
bash-first redirect back to bash/edit_file. Structured `read` and `list_files`
tools exist as an opt-in (`tools: [read, list_files]`, typically for a
subagent on a smaller model); left out of `tools`, those names hit the same
redirect. `history` (opt-in via `tools`, on the scaffold's main agent by
default) recalls past conversations from the runs store: `{query}` is
ranked FTS5 search over user+assistant text across ALL sessions (tool
output is not indexed; a syntax-invalid query is retried as one quoted
phrase), `{session, around, limit}` reads the transcript around a hit;
read-only, store-nil-safe, handled by `chat.HistoryHandler`. Specialists are subagents. A **subagent** is an **in-process background job** spawned via the
`task` tool (`{subagent_type, prompt, description}`; returns immediately); the
runtime (`internal/shell3` jobManager) runs it as a child-session goroutine
under a concurrency cap (`background.max_concurrent`, default 8) — no
subprocess, no inbox file, no fsnotify. `bash_bg` is a background shell
command on the same runtime; its full output also tees to
`runs/<session>/jobs/<id>.log` (1 MiB cap, janitor-swept). **Completion
delivery is unified through the notifier** (`internal/shell3/notifier.go`):
every finished job — bash_bg, subagent, follow-up, cron — becomes a
`CompletionEvent` and, unless spawned with `direct: true`, runs one small
headless triage turn of the `notifier.md` persona (fixed tools: `read`,
`list_files`, and the verdict pair `send {text}` → post to the user /
`wake {note}` → deliver to the main agent; calling neither = silent). The
spawner can pass `note: "…"` as triage context. Hard rules are host code:
a failed job left silent posts `⚠️ <label> failed: …` anyway; at most one
wake per completion; triage turns timebox at 60s and degrade to a raw post
on error/timeout; no `notifier.md` → every completion posts raw. Delivery
lands through a front-end `CompletionHost`
(`Runtime.SetCompletionHost`: `PostCompletion` (⏰ for cron origins, 🔔
otherwise; threaded+anchored into the owning session's chat thread when one
is live), `WakeOwner` (queue+wake a live owner; its liveness check pairs
with the bot's retire lock so notes never land in a closing session),
`StartFreshTurn` (no owner — cron, orphans: a fresh main-agent session runs
over the note and replies as a new replyable thread, serialized FIFO on the
single-turn gate)); with no host installed (library/tests, `shell3 ask`)
the raw notice goes straight to the owning session — ask deliberately stays
in that mode so its verbose view sees everything. `direct: true` (bash_bg
arg, task arg, cron frontmatter) skips triage: the owner wakes with the
notice (cron: a fresh main-agent turn, since the pinned cron parent runs no
turns). Foreground `bash` is capped at 120s
(`timeout_seconds`) precisely because it blocks the turn — longer work
belongs in `bash_bg`. A subagent may run `bash_bg` jobs of
its own; a job that outlives the subagent's main turn keeps the child session
open ("lingering"), and each completion **resumes the subagent for a follow-up
turn** whose summary is triaged like any completion (direct jobs deliver it
to the root as an `agent_update` notice; capped at 5 follow-up turns per
subagent, after which — or after cancel/failure — the raw job event is
triaged instead, so a completion is never lost). `task_cancel <sub>`
cascades to the jobs the subagent started. `Runtime.Reload` no longer refuses while background work is
running: it always proceeds — idle front-end sessions swap onto the new
Parts in place, while a subagent child session or a still-running `bash_bg`
job keeps the Parts (store/MCP handles) it was built with; the old
generation's teardown is deferred ("parked") and runs once every such job
drains, or immediately if nothing is running. Only a busy front-end *turn*
still blocks a reload (`s.isBusy()`). Delegation is
**single-level by construction** — a
subagent is never given the `task` tool (subagent frontmatter has no way to
express delegation), so subagents can't spawn subagents; there is no depth
field anywhere. Delegation itself is **inferred**: the four task-family tools
(`task`, `task_list`, `task_status <id>`, `task_cancel <id>`; ids like
`sub1`/`bg1`) are advertised iff `agents/` is non-empty — a file in `agents/`
IS the registration, there is no toggle and no allowlist key.

`GET /api/jobs` lists running + finished jobs and `/api/jobs/<id>/cancel`
cancels one; the job-progress stream is `rt.JobEvents()` /
`Session.JobEvents()`. Note `Session.Jobs()` reports the whole job runtime,
not one session's share — filter by `JobInfo.ParentID` for per-session work. The shell is **unrestricted except by the hook**;
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
prompt — an Allow/Deny modal in the browser; decline/headless → block).
Precedence when several keys are set: block > argv > ask > command. Nonzero
exit, malformed JSON, or timeout **fails closed**. `hooks/tool-result.sh` /
`hooks/<name>.tool-result.sh` can rewrite a tool's output (e.g. redact
secrets): stdin `{"name","args","output"}`, stdout `{"output": …}`; a failure
here also fails closed (output replaced by an error notice, never passed
through unredacted). **The scaffold's gates ship armed** (`internal/scaffold`,
covered by `internal/scaffold/hooks_test.go`, which drives the shipped scripts
with real payloads): credential paths, system-path writes, unread remote code,
publishing, force-pushes, self-termination, and edits to the gate scripts are
refused; everything else runs. They never ask — shell3 mostly runs unattended,
where an ask parks the turn until it times out and denies anyway — and every
refusal instructs the model not to work around it but to raise it with the
operator (a subagent's refusal tells it to stop and hand up to the main agent
instead). `explorer` gets an allowlist gate, which is what makes "read-only"
true in fact rather than only in its description, and closes the delegate-the-
forbidden-thing route around the main gate.

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
deliberate block is fine); the Status view lists per-server state.
Context is host-managed via two token thresholds: `prune_at` cheaply stubs
old tool outputs (no LLM call), and `compact_at` triggers tail-preserving
compaction — summarizing the head while keeping recent turns verbatim. The
`prune_at` and `keep_recent` knobs are optional, defaulting to fractions of
`compact_at`; no model-driven prune/compact tools. A *forced* compaction (the
web UI's `/compact`) skips the threshold and caps the verbatim tail at the
floor rather than the configured fraction — the automatic tail is a slice of a
large window, so a forced compaction sized that way would refuse as "nothing to
compact" across the whole range where anyone would ask for one.

**Web-first.** shell3 is a hosted agent you reach in a browser. `shell3 serve`
runs everything (`internal/webui`): the agent, the single-page interface, and
cron. It binds `127.0.0.1:8765` by default (`web.addr`) and **authenticates**:
`web.password` (required — `serve` refuses to start without it, `health` fails
on it; a `web.password: env:SHELL3_WEB_PASSWORD` reference like every other
secret) is exchanged at a login screen for a server-side session, and the
optional `web.totp_secret` adds a second factor so a leaked password alone is
not a session. **Exposure is the operator's**: shell3 binds `web.addr` and
does nothing else — it exposes nothing beyond that bind and supervises
nothing. `web.url` is the one exposure-related key and it is purely
informational (the address the operator actually serves it on; `serve` prints
it at start via `announcePublicURL`, nothing depends on it). A network-facing
bind with no https warns that the password and cookie cross in clear and
points at `docs/deploying.md`, which is where every "how do I keep it running
/ reach it from my phone" answer lives. Auth is **not** a reason to skip a
proxy: a login here is a shell.

Authentication lives in `internal/webui/auth.go` + `sessions.go`. Every route
is declared in one table (`Server.routes()`) carrying a `public` flag, so a new
endpoint has to state its status to compile and `auth_test` walks that same
table asserting every private route 401s unauthenticated — the guarantee is
structural, not a list someone maintains. Exactly two things are public: `POST
/api/login`, and the static shell (`index.html`, `/assets/*`, the icons) that
draws the login screen; `/sw.js` is gated by an exact pattern that beats the
catch-all. Sessions are opaque 32-byte tokens whose SHA-256 hashes live in
`.shell3_project/web_sessions.json` (0600, atomic replace, expired entries
pruned at load), 7-day sliding expiry renewed in place on use, so a restart
does not log anyone out. Each record carries a fingerprint of the password it
was created under: changing `SHELL3_WEB_PASSWORD` invalidates every session,
which is what makes "I think I was breached" actionable. Cookie is `HttpOnly`,
`SameSite=Lax` (Strict would drop the cookie when the interface's URL is
opened from a messenger), `Secure` whenever the request arrived over https. Failed logins
get an escalating global delay (no lockout — that would let anyone hold the
login closed, and TOTP already covers guessing), every attempt is logged with
IP and user-agent, and every success raises a bell + push notification, since
that notice is how a breach gets noticed at all. TOTP codes are single-use
within their window. `shell3 boot` asks for the password (16-character floor,
offers a generated one) and offers TOTP enrolment with a QR code in the
terminal; losing the phone is not a lockout because the secret is a line in
`.env` on your own machine. The interface is built with **assistant-ui** (React, `webui/`), built by
`make webui` into `internal/webui/dist` and embedded in the binary — the
staged build is committed because `go install` cannot run npm. It is **set as a
printed document**, the run log it already is: two stocks (paper, and a
cyanotype for dark), Newsreader over Inter Tight over IBM Plex Mono — all
self-hosted, since the binary must work offline — and four shared devices (the
ruled section head, the dotted leader, the hanging figure column, and the
marker). The yellow is the marker and marks only what is live. Tokens and the
devices live in `webui/src/index.css`; the shadcn names are mapped onto them so
untouched components still pick up the right stock.

The browser talks to the agent over `POST /api/chat`, which streams the turn
as SSE in the AI SDK **UI message stream** dialect (`internal/webui/stream.go`
bridges `shell3.Event` → protocol chunks: text and reasoning arrive as
delta blocks bracketed by start/end, a tool call closes the open text block
and emits `tool-input-available` / `tool-output-available`, and channel close —
not a terminal `Done` event — is the authoritative end of turn). Host
narration (retries, compaction, usage) is dropped from the chat and stays in
the logs. A message that is exactly a **slash command** is answered by the
server without the model ever seeing it (`internal/webui/command.go`; typing
`/` in the composer opens the menu): `/compact` summarises the conversation's
head and reports the tokens freed, running the same forced compaction as the
`compact_at` threshold but narrowing the verbatim tail to the floor, since
someone asking for space now is below that threshold by definition. A request
names its conversation with `threadId`, NOT the AI SDK's `id` — the client
library mints that per runtime. Stop is `POST /api/stop`, not an aborted
request: the turn ends server-side, its jobs are killed, and the stream closes
properly (unfinished tool calls are answered "stopped before it finished", a
cancelled turn reads as `_Stopped._` rather than `context canceled`), where a
browser-side abort would strand the last tool call looking like it was waiting
on the user. `/api/events` is the server-push channel (notifications + approval
requests, and live job progress); the rest is introspection:
`/api/capabilities`, `/api/status`, `/api/threads[/{id}[/messages]]`,
`/api/jobs[/{id}[/cancel]]`, `/api/cron[/{name}/run]`, `/api/runs[/{id}]`,
`/api/files`, `/api/files/content`, `/api/media[/{name}]`, `/api/stop`,
`/api/reload`, `/api/stt`, `/api/tts`, `/api/push[/subscribe|/test]`, plus
`/api/login` and `/api/logout`. The browser gates itself the same way: an auth
probe runs before the chat mounts, and any 401 — including the events stream
failing to open — returns it to the login screen. A 401 is deliberately told
apart from "no backend at all", because sample data standing in for a live
server that merely wants a login is the one thing this UI must not do.

The UI has six views. **Chat**; **Jobs** (running and finished background work,
live output tail, a subagent's transcript or a command's captured stdout, exit
code, cancel); **Cron** (each job's schedule, workdir, direct-vs-notifier
delivery, full prompt, last run, a Run now button firing `Scheduler.Run`, and a
link into the job that last executed it); **Runs** (every stored session —
conversations, subagent children, cron runs, `shell3 ask` sessions — replayed
at full fidelity with tool calls, arguments, results, and reasoning, which the
chat view deliberately omits); **Status** (the effective system prompt, model
params, config warnings, context window, tool descriptions, last-turn token
usage with a context-fill bar, and whether the command gate is armed); and a
read-only **Files** explorer over two roots — the config dir (`.env` is
redacted, never read from disk; reads report `redacted`/`binary`/`truncated`)
and the media dir (uploads and generated images, newest first, with inline
previews). Plus a notification bell, a light/dark toggle, and voice. Chat is pinned in
the sidebar; the five operational views sit under an always-visible
"Elsewhere" group at its foot. The operational views poll while the
tab is visible; sample data appears only when there is no backend at all, never
in place of a live one that failed.

**Web push** (`internal/webui/push.go`) carries notifications to a browser with
no tab open. A VAPID keypair is generated once into
`.shell3_project/web_push_keys.json` (0600) and per-browser subscriptions into
`web_push_subs.json`; `/sw.js` (which caches nothing on purpose) shows the
notification and focuses an existing tab on click. Every bell notification is
pushed too, and an endpoint the push service reports gone (404/410) is pruned.
Push needs a secure context — localhost or proxied https, never plain http to
another host — so the toggle in the bell explains itself rather than failing
silently.

Chat is **thread-scoped**: a browser thread maps to a shell3 session through a
persistent index (the runs store's `threads` table, surface-namespaced "web",
carrying the browser-facing title/preview/created_at/updated_at/deleted
metadata on top of the plain msg_id→session_id mapping the other surfaces use;
`internal/webui/threads.go` — in-memory map authoritative for the process,
store writes best-effort, and the store is resolved per call so a /reload
generation swap never leaves the index on a closed handle), so a thread
continues its conversation across turns and process restarts; sessions stay resident
between turns and the oldest idle one retires past `keepLiveSessions` (a
session with running jobs is never retired — its jobs would lose their
parent). One main-agent turn runs at a time: a message arriving mid-turn is
refused with an error chunk rather than queued, and the running turn is never
steered. A job that finishes *during* a turn wakes the session and its
follow-up is appended to the same reply (bounded by `maxWakeDrains`).

The **command gate** is a modal: a hook script's `ask` verdict publishes an
approval request over `/api/events`, the turn parks inside `asks.Ask` until
`POST /api/asks/{id}` answers it, and still-parked requests replay to every
new subscriber so reloading the page never strands a turn. Fail-safe
throughout — no browser attached, a cancelled turn, or a timeout all deny.

**Completion delivery** is the notifier's, unchanged; the front-end supplies
`CompletionHost` (`internal/webui/completion.go`): a `send` verdict becomes a
notification in the bell (the 50 most recent are replayed to a browser on
connect, so closing the tab does not lose them), a `wake` verdict runs another turn — the owning
session if still live, a fresh one otherwise — through the same single-turn
gate, so a cron result never runs concurrently with someone typing. Wake and
session retirement share one lock, so a note lands or the session retires,
never both.

**Media** (`internal/media`, four blocks under `media:` in `shell3.yaml`, each
pointing at a model): `stt` transcribes browser recordings (`POST /api/stt`;
the UI records with MediaRecorder), `tts` speaks replies (`POST /api/tts`),
`describe` captions uploaded images before the turn — pointed at a vision
model for text-only mains, or at the main model itself to skip a `read_media`
round-trip (boot's default when the model has vision) — and `imagegen` adds an
`image_generate` tool for the main agent AND every subagent, registered via a
runtime session decorator (`Runtime.SetSessionDecorator`; reapplied on Reload)
(`api: openai` or `openrouter`, the latter a raw chat-completions POST with
`modalities=["image","text"]`, OpenRouter's image-output dialect — its
dedicated `/api/v1/images` endpoint is avoided because it pre-authorizes
worst-case cost and 402s low balances). All media — uploads and generated
images (`img-*`) — is stored under `~/.shell3/media/` so every file keeps a
durable path (TTS audio included, cached as `tts-*`) and is served back at
`/api/media/<name>`; the agent shows a generated image by writing
`![](/api/media/<file>)`. Restriction policy is the hook script, not a tools
list. When no media model is configured the UI falls back to the browser's own
Web Speech APIs, so dictation still works.

An in-process cron scheduler (`internal/cron`, jobs are `cron/<name>.md`
files; each job dispatches its declared agent — a subagent from `agents/`, or
a project's `manager.md`, which then runs in that project's workdir — from a
hidden pinned "cron" parent session that is the dispatch parent + the jobs/runs
source but runs NO turns of its own and is never woken; a run's result is a
notifier event carrying the job name (`DispatchOpts.CronJob`) and the job's
prompt as the triage note (`DispatchOpts.Note` — the judge knows what the job
is FOR); a failed run always surfaces as `⚠️ <job> failed: <error>`).

Sessions, messages, reminders, and every surface's thread index live in **one
SQLite database** (`internal/runs`, modernc.org/sqlite — pure Go, no cgo):
`.shell3_project/shell3.db`, with an FTS5 index over user+assistant message
text backing the `history` tool. Job logs stay plain files under
`runs/<session>/jobs/<id>.log`. The schema is stamped with `PRAGMA
user_version`; a database whose stamp doesn't match the running binary is
**deleted and recreated empty**, with one loud stderr line — shell3 data is
disposable by design, so there are no migrations.

A **runs janitor** runs once at `shell3 serve` startup (never on `ask`):
`runs_keep_days` (top-level `shell3.yaml` key, default 30, `0` = keep forever)
deletes sessions whose `last_at` is past the cutoff — rows, FTS entries,
thread entries, and job-log dirs together — plus empty crash leftovers and
orphaned `runs/<id>/` dirs (pre-database leftovers), printing `janitor:
removed N runs, M thread entries` (silent when both are zero). SQL in
`runs.Sweep`, on its own connection (the runtime's store is already open by
then — the sweep does not need it closed), before the server listens.

`shell3 boot` scaffolds the config tree (an interactive form: model, context
budget, whether the model has vision — which wires `media.describe` + the media
tool — the agent's workdir, and the interface password) and writes secrets to
`~/.shell3/.env`; one TTY-only offer wires TOTP enrolment → `web.totp_secret`
(`boot --totp` re-runs just that step later — enrol after declining, or reset
with a fresh secret; removal stays manual: delete the key from `.env`).
It installs **nothing** and exposes **nothing**: the finale prints the local
URL, the one-line tailnet start (`tailscale serve --bg 8765 && shell3
serve`), on Linux a copy-paste systemd-unit + `tailscale serve` block, and
points at `docs/deploying.md` (or the agent) for the rest — it only ever
*prints*; running any of it is the operator's. `--show` reprints that
finale, rendered to the terminal's own background.
`shell3 ask "…"` is the terminal front-end (`internal/cli`): it drives the same
agent with full verbose output (every tool call/result, reasoning, token usage;
no message = an interactive multi-turn loop; `-p` for headless; `--resume`
continues the latest session; host-agnostic — reads nothing from the `web:`
block).
`serve`, `ask`, `boot`, `project`, and `health` are the whole command tree —
there is no Telegram front-end, no separate dashboard command, and no command
that exposes or supervises the process.

## IMPORTANT: Do Not Read Credential Files

Secrets and credentials (provider API keys, tool tokens) live in a plain
`.env` file beside the active `shell3.yaml` (e.g. `~/.shell3/.env`),
referenced from YAML as `env:KEY`. Never read, display, or include the
contents of any credential file in a response. This applies to all agents,
assistants, and automated tools.

- `.env` beside `shell3.yaml` (e.g. `~/.shell3/.env`) — provider API keys, base URLs, tool secrets

## Project Layout

```
cmd/shell3/            cobra command tree: root (prints help) + serve/ask/boot/project/health subcommands
internal/agentsetup/   shared config assembly (BuildParts → chat.Config) used by every front-end
internal/config/       config-directory loader (shell3.yaml + agent/notifier/subagent/project/skill/cron markdown + hooks/*.sh) + system-prompt assembly
internal/bootstrap/    first-run global + project setup
internal/scaffold/     embedded starter config tree (defaults/base + defaults/project for `shell3 project new`) + boot/project rendering
internal/adapter/openai/  OpenAI-compatible LLM adapter
internal/modelproxy/   run_proxy spawner (starts a model's proxy command on activation)
internal/paths/        global (~/.shell3/) + local (.shell3_project/) path resolution
internal/runs/         SQLite runs store (modernc.org/sqlite, pure Go): sessions/messages/reminders/threads + FTS5 index in .shell3_project/shell3.db; job logs stay files under runs/<id>/jobs/; sweep.go is the startup janitor
internal/edittool/     edit_file tool implementation (Go port of opencode's str-replace) + its direct-disk file I/O
internal/notify/       Notification type (bg_done / agent_done) shared by job runtime + chat
internal/media/        media.stt/tts/describe/imagegen clients (transcribe, speak, describe, generate)
internal/mcp/          MCP client (official go-sdk): Manager connects mcp: servers, lists tools, dispatches mcp_* calls
internal/webui/        the web front-end: HTTP API, SSE chat bridge, turn gate, thread index, command gate, completion delivery; dist/ is the embedded build of webui/
webui/                 the interface itself (React + assistant-ui + Vite); `make webui` builds it into internal/webui/dist
internal/cron/         robfig/cron scheduler dispatching subagent jobs on Session.Dispatch
internal/cli/          terminal front-end helpers: shell3 ask renderers, brand banner
internal/chat/         conversation loop, tools, events, JSONL audit sink
internal/llm/          Provider/Streamer interfaces, request params, types (+ fakellm)
internal/persona/      runtime carrier for an agent's prompt/tools/params (data only)
internal/strutil/      rune-safe string truncation helpers (byte-cap + rune-count) shared by runtime and front-ends
internal/applog/       rotating app log
internal/shell3/       session/runtime core consumed by the front-ends; jobs.go hosts the in-process job runtime (subagents + bash_bg); notifier.go is the completion-triage funnel (CompletionEvent/CompletionHost)
```

## Development

```bash
make build      # go build ./cmd/shell3
make webui      # build the interface (webui/) into internal/webui/dist — commit the result
make install    # go install ./cmd/shell3
go test ./...   # run all tests
```

The staged `internal/webui/dist` is committed: `go install` cannot run npm, so
a binary built from a clean checkout must already carry the interface. Re-run
`make webui` (and commit the result) whenever `webui/src` changes.

## AI artifacts are not committed

Design specs, implementation plans, and other AI-generated working notes are
**gitignored, never committed** — `docs/dev/*` (except its `README.md`),
`docs/superpowers/`, `docs/dev/superpowers/`, and `ai-do-not-read.*`. Keep them
local; the repo carries only shipped documentation (top-level `README.md`,
`docs/`, `docs/cookbook/`). If you generate a design/plan doc, leave it in
`docs/dev/` where the ignore rule keeps it out of commits.
