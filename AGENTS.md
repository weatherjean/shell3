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
(`models:`, `telegram:`, `mcp:`, `media:`, `background:`, `runs_keep_days`,
`media_keep_days`;
strict decode — unknown keys fail the load; secrets referenced as `env:KEY`,
substring-substituted from the sibling `.env`, unknown key = load error).
Everything with a prompt is markdown-with-frontmatter: `agent.md` (THE agent —
exactly one because there is exactly one file; frontmatter `model` (required),
`tools: [bash, bash_bg, edit, media, read, list_files, history]`, `mcp`, `prune`,
`context` (main-agent-only: a list of config-dir-relative paths, globs
allowed, re-read at every turn start (`RefreshPrompt`, wired through
`TurnConfig` into `assembleTurnContext`) — so even the long-lived Telegram
conversation sees current file contents, never a session-creation
snapshot — into a `## Context` prompt section,
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
`cron/<name>.md` (frontmatter
`schedule`/`agent`/`direct`/`workdir`; body = prompt; `agent` names either a
subagent from `agents/` or a project's `manager.md`, which then runs in that
project's workdir — an explicit `workdir` overrides even a manager's).
There is no Lua anywhere, and no migration shims: an
unknown key is simply an unknown-key load error. (`notifier.md`, the old
triage persona, is gone with the mail model — a leftover file loads with a
warning naming the removal.)

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
read-only, store-nil-safe, handled by `chat.HistoryHandler`. Specialists are
subagents. A **subagent** is an **in-process background job** spawned via
the `task` tool (`{subagent_type, prompt, description}`; returns immediately); the
runtime (`internal/shell3` jobManager) runs it as a child-session goroutine
under a concurrency cap (`background.max_concurrent`, default 8) — no
subprocess, no inbox file, no fsnotify. `bash_bg` is a background shell
command on the same runtime; its full output also tees to
`runs/<session>/jobs/<id>.log` (1 MiB cap, janitor-swept). **Completion
delivery is mail** (`internal/shell3/completion.go`): every finished job —
bash_bg, subagent, follow-up, cron — becomes a `CompletionEvent` and routes
deterministically, no triage turn, no judge model. Failed: the ⚠️ floor
post always reaches the user, and a live owning session is additionally
mailed (woken) so the agent can react — but an ownerless failure (cron)
stops at the post, never burning a main-model turn per broken tick.
`direct: true` (bash_bg arg, task arg, cron frontmatter): the raw result
posts straight to the user, and the owning session gets the notice queued
WITHOUT a wake — the next turn has it in context without spending one now.
Default: the completion is **mail to the agent** — `WakeOwner` queues+wakes
the owning session, or `StartFreshTurn` runs a fresh main-agent session
when none is live (cron, orphans). A mail turn's reply posts to the user
as ✉️ agent mail — one channel, no separate tool — unless the model
replies NO_REPLY (matched leniently: `isNoReply`), which keeps the turn
silent. The spawner can pass `note: "…"` as context carried into the mail.
Delivery lands through a front-end `CompletionHost`
(`Runtime.SetCompletionHost`: `PostCompletion` (⏰ for cron origins, 🔔
otherwise; threaded+anchored into the owning session's chat thread when one
is live), `WakeOwner` (its liveness check pairs with the bot's retire lock
so mail never lands in a closing session), `StartFreshTurn` (serialized
FIFO on the single-turn gate)); with no host installed (library/tests,
`shell3 ask`) the raw notice goes straight to the owning session — ask
deliberately stays in that mode so its verbose view sees everything.
Foreground `bash` is capped at 120s
(`timeout_seconds`) precisely because it blocks the turn — longer work
belongs in `bash_bg`. A subagent may run `bash_bg` jobs of
its own; a job that outlives the subagent's main turn keeps the child session
open ("lingering"), and each completion **resumes the subagent for a follow-up
turn** whose summary routes like any completion mail (capped at 5 follow-up
turns per subagent, after which — or after cancel/failure — the raw job
event routes instead, so a completion is never lost). `task_cancel <sub>`
cascades to the jobs the subagent started. `Runtime.Reload` no longer
refuses while background work is running: it always proceeds — idle
front-end sessions swap onto the new
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

`/jobs` lists running + finished jobs and `/cancel <id>` cancels one; the
job-progress stream is `rt.JobEvents()` / `Session.JobEvents()`. Note `Session.Jobs()` reports the whole job runtime,
not one session's share — filter by `JobInfo.ParentID` for per-session
work. The shell is **unrestricted except by the hook**;
the opt-in gate is a **per-agent bash hook script**: `hooks/tool-call.sh`
governs the main agent, `hooks/<name>.tool-call.sh` governs subagent `<name>`
— no fallback, no chaining; an agent with no script runs ungated (a `<name>`
matching no subagent is a warning; `shell3 health` fails on it). The script
runs before **every** tool as `bash <path>` (cwd = config dir, 10s timeout)
with JSON on stdin — `{"name", "command" (bash text for the two bash tools,
null otherwise), "args", "headless" (true when no human is attached —
subagents, cron)}` — and prints a verdict: empty/`{}`
(run) / `{"command": …}` (rewrite — bash tools only) / `{"argv": […]}`
(runner-swap — bash tools only; fails closed for non-bash) /
`{"block": true, "reason": …}`. There is NO ask verdict: shell3 runs
unattended, where an ask is a denial with a delay — a legacy hook printing
`{"ask": …}` fails closed with a reason naming the removal, never silently
allows. Precedence when several keys are set: block > argv > command.
Nonzero exit, malformed JSON, or timeout **fails closed**. `hooks/tool-result.sh` /
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
instead).

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
deliberate block is fine); `/status` lists per-server state.
Context is host-managed via two token thresholds: `prune_at` cheaply stubs
old tool outputs (no LLM call), and `compact_at` triggers tail-preserving
compaction — summarizing the head while keeping recent turns verbatim. The
`prune_at` and `keep_recent` knobs are optional, defaulting to fractions of
`compact_at`; no model-driven prune/compact tools. A *forced* compaction
(`Session.Compact` / `chat.CompactStandalone`) skips the threshold and caps the
verbatim tail at the floor rather than the configured fraction — the automatic
tail is a slice of a large window, so a forced compaction sized that way would
refuse as "nothing to compact" across the whole range where anyone would ask
for one. It is a runtime seam with no front-end command bound to it: the bot
compacts automatically and `/status` reports usage.

**Telegram-first.** shell3 is a personal agent you reach in one Telegram chat.
`shell3 telegram` runs everything (`internal/telegram`): the agent, the bot,
and cron. There is **no listener** — the process long-polls the Bot API
outbound, so there is no port, no login, no tunnel. The `telegram:` block in
`shell3.yaml` is `token` (an `env:TELEGRAM_TOKEN` reference like every other
secret), `chat_id` (the single chat the bot answers — updates from anywhere
else are dropped before a turn starts, messages in `handleMsg` and
inline-button callbacks in `handleCallback` alike, and that IS the access
model), and `workdir` (the agent's shell; default = the config dir). Missing
token or chat_id, or a non-numeric chat_id, refuses to start — naming the
field at fault, and `shell3 health` fails on the same check (an absent
`telegram:` block is reported, not failed: an `ask`-only config is legitimate). The transport is an
interface (`client.go`): `client_botapi.go` wraps go-telegram/bot,
`client_console.go` drives the same bot loop over stdin/stdout for
`shell3 telegram --console` (headless event testing, no credentials, no
network). `pollhealth.go` records getUpdates/send failures into the app log
with throttled repeats and a recovery line, so a transport outage is visible
after the fact; outbound sends retry transient network failures on a short
backoff (`withSendRetry`, ~4.5s of patience) and never retry API
rejections. At startup the host registers the `/` command menu
(`BotCommands`), clears any menu button an older build left behind, and greets
the chat.

The turn model is **one conversation**: the bot holds ONE long-lived main
session that every message — bare or reply — continues. A Telegram reply is a
context hint (the quoted text is injected as a capped blockquote,
`withReplyContext`), never a session switch; `/new` is the only way to start
over (old conversation stays in `/runs` and the history index). The current
session id persists in the runs store's `threads` table under the reserved
key `current-session` (surface-namespaced "telegram"/"serve";
`ThreadIndex.SetCurrent`/`Current` in `threads.go` — store resolved per call
so a /reload generation swap never leaves the marker on a closed handle), so
a restart resumes the same conversation (`mainSession`, falling back to a
fresh session when the marker is absent or swept). The session never retires;
host-managed compaction keeps its context bounded. Exactly one main-agent
turn runs at a time (a turn slot in `bot.go`); sending always succeeds — a
message arriving mid-turn queues silently (`mailQueue`) and the WHOLE backlog
drains as one batch turn (it is all the same conversation), anchored at the
newest message. A TEXT message arriving mid-turn STEERS the running turn —
injected at the next round boundary via `chat.Session.Interject`
(`dispatchMail`); a steer landing after the final boundary is answered by
`startSteerCatchup`'s own POSTED turn (`chat.Session.HasSteer` /
`shell3.Session.HasQueuedSteer`), so it is never silently absorbed; media
messages queue instead (preflight needs a turn goroutine). Inbound text
rides a 400ms debounce (`burstWindow`, `b.debounce` in tests) merging
Telegram's split-message fragments into one turn. `/inbox` renders the
queued state — the user's pending mail plus waiting agent mail — with zero
tokens. During a user turn the bot renders tool activity as ONE
self-editing **progress bubble** (`progress.go`: posted silently on the
first ToolCall, edits throttled at 1.5s, last 6 lines shown, one-line
tool summaries) that is DELETED after a clean turn and kept as a
breadcrumb after an error; wake turns show no bubble. `DeleteMessage`
joins the tgClient surface (console renders `[delete #id]`, serve emits a
`delete` event). `postReply`
chunks the reply at 4000 **UTF-16 code units** (Telegram's real accounting —
emoji count double; `utf16Len`/`chunk` in render.go) on rune boundaries and
replies each chunk to the conversation's anchor, capped at `replyMaxChunks` (2) bubbles — a
longer reply posts its first chunk plus the full text as a `reply.md` document
— and records every sent message id so the anchor advances.
`drainTurn` treats only the FINAL assistant segment as the reply — text before
a tool call is progress narration — and errors always surface. Markdown is
converted for Telegram by `mdhtml`. The console transport answers inline menus
(the /voice keyboard) with an `&<data>` input line.

**Commands are host-answered** (`commands.go`, no model call, zero tokens):
`/stop` (cancel the turn; background jobs keep running), `/new` (start a
fresh conversation; refused mid-turn), `/run <job>`,
`/status`, `/jobs`, `/job <id>`, `/cancel <id>`, `/cron`, `/runs [page|id]`
(paginated inline listing, 8 per page, each entry a tappable `/run_N` that
replays that run — taps resolve only against the map the last render stored,
so a stale index errors instead of opening the wrong run),
`/reload`, `/voice off|inbound|always`, `/quiet on|off` (persisted to
`~/.shell3/quiet_mode.json` by `QuietStore`: ⏰ cron and 🔔 completion posts
send with Telegram `disable_notification`, arriving without a ping; ✉️
agent mail is ALWAYS silent regardless of the toggle (mail is not a page);
replies to the user's own messages and ⚠️ failures always ring; the flag
rides a variadic
`SendOpt{Silent}` on the tgClient send methods, rendered by the console
transport as a 🔕 tag and by the JSONL transport as `"silent":true`). The dash views are rendered as markdown
by `internal/render` (`Status`, `Jobs`, `JobDetail`, `Cron`, `RunsPage`,
`RunReplay`) and delivered by `sendMarkdownDoc`: inline when under
`mdInlineThreshold`, otherwise as a `.md` document plus a capped text summary
(the `/runs` listing is always inline — Telegram only linkifies commands in
message text).
`/reload` takes the turn slot, so it is refused rather than raced.

Three **host tools** ride the session decorator (`Runtime.SetSessionDecorator`,
re-applied by `Runtime.Reload`; `DecorateChatSession` skips headless subagent
children): `send_media_telegram` (push a local file to the chat as
photo/voice/audio/video/document, validating extension and size per kind, and
refusing `.env` and its dotenv siblings), `status`, and `reload` (records a pending reload and returns; the
host applies it at end-of-turn, since a mid-turn reload would tear down the
running turn). `image_generate` is registered on EVERY session, headless
children included.

**Completion delivery** is mail (see internal/shell3/completion.go above);
the bot is the `CompletionHost` (`bot.go`): `PostCompletion` posts
`⏰ <job>: …` for a cron origin (direct cron, ⚠️ floors) and `🔔 …`
otherwise, threaded onto the conversation's anchor; `WakeOwner` queues+wakes
iff the owner IS the current main conversation; `StartFreshTurn` is the
catch-all that queues the note into the main conversation (creating it on
demand) — cron results, orphans, and jobs outliving a `/new` all land there,
so a completion is never lost. A wake turn's reply is the agent speaking:
`runWakeTurn` posts it ✉️-prefixed (ALWAYS silent, a plain message — never
a Telegram reply; strict final-segment — no narration fallback), and
NO_REPLY/empty keeps the turn silent; there is no mail_user tool (removed:
two exits meant the same answer could send twice). The wake is UPGRADED to
a POSTED turn (`runPostedQueuedTurn`) when the inbox holds user steering
(`HasQueuedSteer`), so a steer racing a turn's end still gets its answer;
text arriving DURING a wake turn queues rather than steering into it
(`turnQuiet`). Callbacks (the `/voice` menu) drain
on their own bot-lifetime goroutine (`callbacks.go`).

**Media** (`internal/media`, four blocks under `media:` in `shell3.yaml`, each
pointing at a model): a turn's attachments are saved to `~/.shell3/media/` as
`tg-*` (`attachments.go`) and their paths always go into the prompt. Before the
turn, `preflight.go` transcribes an inbound voice note via `stt` (injecting the
transcript; `stt.echo` also posts it back to the chat) and captions an inbound
photo via `describe` (injecting `[image: …]`) — the fast local scan runs on the
update loop, the network half only on a turn goroutine under
`preflightTimeout`, and any failure becomes a compact ⚠️ chat notice while the
turn still runs with the file path. `deliverReply` (`voice.go`) is the single
reply exit for a user turn: per the resolved voice mode (`tts.mode`, overridden
by `/voice` and persisted to `~/.shell3/voice_mode.json` by `ModeStore`) it
speaks the reply *instead of* posting text — as a voice bubble when the
synthesized container is ogg/opus, an audio file otherwise — and falls back to
text on any failure. `imagegen` adds `image_generate` (`api: openai` or
`openrouter`, the latter a raw chat-completions POST with
`modalities=["image","text"]` — its dedicated `/api/v1/images` endpoint is
avoided because it pre-authorizes worst-case cost and 402s low balances); the
agent delivers the result with `send_media_telegram`. Restriction policy is the
hook script, not a tools list.

An in-process cron scheduler (`internal/cron`, jobs are `cron/<name>.md`
files; each job dispatches its declared agent — a subagent from `agents/`, or
a project's `manager.md`, which then runs in that project's workdir — from a
hidden pinned "cron" parent session that is the dispatch parent + the jobs/runs
source but runs NO turns of its own and is never woken; a run's result is
completion mail carrying the job name (`DispatchOpts.CronJob`) and the job's
prompt as context (`DispatchOpts.Note` — the agent knows what the job is FOR):
by default a fresh main-agent turn whose reply posts as ✉️ agent mail only
when warranted (NO_REPLY stays silent), with `direct: true` a raw ⏰ post
costing no agent turn; a failed
run always surfaces as `⚠️ <job> failed: <error>` and spends no turn).

Sessions, messages, reminders, and every surface's thread index live in **one
SQLite database** (`internal/runs`, modernc.org/sqlite — pure Go, no cgo):
`.shell3_project/shell3.db`, with an FTS5 index over user+assistant message
text backing the `history` tool. Job logs stay plain files under
`runs/<session>/jobs/<id>.log`. The schema is stamped with `PRAGMA
user_version`; a database whose stamp doesn't match the running binary is
**deleted and recreated empty**, with one loud stderr line — shell3 data is
disposable by design, so there are no migrations.

A **runs janitor** runs once at `shell3 telegram`/`shell3 serve` startup
(never on `ask`): `runs_keep_days` (top-level `shell3.yaml` key, default 30,
`0` = keep forever) deletes sessions whose `last_at` is past the cutoff —
rows, FTS entries, thread entries, and job-log dirs together — plus empty
crash leftovers and orphaned `runs/<id>/` dirs (pre-database leftovers),
printing `janitor: removed N runs, M thread entries` (silent when both are
zero). The empty-trash rule spares dispatch parents (a session other rows
name as `parent_id` — the pinned cron parent is always message-less), and
stale `status='live'` rows past the grace hour flip to `ended` (nothing
from a previous process can still be live at startup; recent ones may be a
concurrent `ask`). SQL in `runs.Sweep`, on its own connection (the runtime's store is
already open by then — the sweep does not need it closed), before the bot
starts polling. A sibling **media janitor** runs the same start-time-only
shape, gated by `media_keep_days` (top-level `shell3.yaml` key, default 0 =
keep forever, so this is opt-in): deletes regular files in the media dir past
the cutoff — attachments, generated images, and TTS cache alike, since none
are distinguished from each other by the sweep. A swept file's stored path in
an old transcript no longer resolves.

`shell3 boot` scaffolds the config tree (an interactive form: model, vision —
which wires `media.describe` + the media tool — context budget, an optional
proxy command, the Telegram bot token + chat id, and the agent's workdir) and
writes secrets to `~/.shell3/.env` (the token as `TELEGRAM_TOKEN`, referenced
from the rendered yaml as `env:TELEGRAM_TOKEN`; both Telegram fields may be
left blank and filled in later, and a non-numeric chat id is rejected at the
form). It installs **nothing** and exposes **nothing**: the finale prints how
to run the bot and points at `docs/deploying.md` (or the agent) for service
management — it only ever *prints*; running any of it is the operator's.
`--show` reprints that finale, rendered to the terminal's own background.
`--prompts` refreshes the scaffold's prompt files in an existing install
(agent.md body, `agents/`, scaffold-shipped `skills/`) after an upgrade:
frontmatter wiring, shell3.yaml, .env, hooks, cron, projects, memory, and
user-authored skills are untouched; replaced files back up to
`.backup/prompts-<ts>/`; the Vision prompt variant is inferred from whether
the install's own agent.md tools include `media`; a reload applies it
(`runPromptRefresh` in cmd/shell3/bootprompts.go, rendered by
`scaffold.PromptFiles`).
`shell3 ask "…"` is the terminal front-end (`internal/cli`): it drives the same
agent with full verbose output (every tool call/result, reasoning, token usage;
no message = an interactive multi-turn loop; `-p` for headless; `--resume`
continues the latest session; host-agnostic — reads nothing from the
`telegram:` block, and installs no CompletionHost, so its verbose view sees
every completion raw).
`telegram`, `serve`, `ask`, `boot`, `project`, and `health` are the whole
command tree — there is no web interface, no dashboard command, and no
command that exposes or supervises the process. `shell3 serve` is the BYO
front-end seam: the same bot loop over newline-delimited JSON on stdin/stdout
(`internal/telegram/client_jsonl.go`, a third tgClient beside the Bot API and
console transports; docs/serve.md is the wire reference). Message ids are
opaque strings end to end (the Telegram client stringifies the API's ints),
so a front-end's own ids thread natively; serve keeps its own thread surface
in the runs store; markdown goes on the wire (the JSONL client's SendHTML
returns ErrNoHTML, steering the bot's fallback to the plain path); media
crosses as file paths, outbound spooled under `.shell3_project/serve_out/`.
Running serve ALONGSIDE telegram is unsupported by design — run two processes
with two config dirs instead.

## IMPORTANT: Do Not Read Credential Files

Secrets and credentials (provider API keys, tool tokens) live in a plain
`.env` file beside the active `shell3.yaml` (e.g. `~/.shell3/.env`),
referenced from YAML as `env:KEY`. Never read, display, or include the
contents of any credential file in a response. This applies to all agents,
assistants, and automated tools.

- `.env` beside `shell3.yaml` (e.g. `~/.shell3/.env`) — provider API keys,
  base URLs, tool secrets

## Project Layout

```
cmd/shell3/            cobra command tree: root (prints help) + telegram/serve/ask/boot/project/health subcommands
internal/agentsetup/   shared config assembly (BuildParts → chat.Config) used by every front-end
internal/config/       config-directory loader (shell3.yaml + agent/subagent/project/skill/cron markdown + hooks/*.sh) + system-prompt assembly
internal/bootstrap/    first-run global + project setup
internal/scaffold/     embedded starter config tree (defaults/base + defaults/project for `shell3 project new`) + boot/project rendering
internal/adapter/openai/  OpenAI-compatible LLM adapter
internal/modelproxy/   run_proxy spawner (starts a model's proxy command on activation)
internal/paths/        global (~/.shell3/) + local (.shell3_project/) path resolution
internal/runs/         SQLite runs store (modernc.org/sqlite, pure Go): sessions/messages/reminders/threads + FTS5 index in .shell3_project/shell3.db; job logs stay files under runs/<id>/jobs/; sweep.go is the startup janitor
internal/edittool/     edit_file tool implementation (Go port of opencode's str-replace) + its direct-disk file I/O
internal/notify/       Notification type (bg_done / agent_done) shared by job runtime + chat
internal/media/        media.stt/tts/describe/imagegen clients (transcribe, speak, describe, generate)
internal/mediadir/     resolves the media dir (<configDir>/media, $SHELL3_MEDIA_DIR overrides); split out of internal/media to break an import cycle (agentsetup → media → shell3 → agentsetup) and to stay free of the unix build tag
internal/mcp/          MCP client (official go-sdk): Manager connects mcp: servers, lists tools, dispatches mcp_* calls
internal/telegram/     the chat front-end: bot loop + transports (Bot API, console, serve's stdio JSONL), turn slot, thread index, host commands + tools, approval keyboard, media preflight, completion delivery
internal/render/       markdown renderers for the dash views (/status, /jobs, /job, /cron, /runs) shared by the bot
internal/cron/         robfig/cron scheduler dispatching subagent jobs on Session.Dispatch
internal/cli/          terminal front-end helpers: shell3 ask renderers, brand banner
internal/chat/         conversation loop, tools, events, JSONL audit sink
internal/llm/          Provider/Streamer interfaces, request params, types (+ fakellm)
internal/persona/      runtime carrier for an agent's prompt/tools/params (data only)
internal/strutil/      rune-safe string truncation helpers (byte-cap + rune-count) shared by runtime and front-ends
internal/applog/       rotating app log
internal/shell3/       session/runtime core consumed by the front-ends; jobs.go hosts the in-process job runtime (subagents + bash_bg); completion.go is the deterministic mail router (CompletionEvent/CompletionHost)
```

## Development

```bash
make build      # go build ./cmd/shell3
make install    # go install ./cmd/shell3
make lint       # gofmt + go vet + golangci-lint
go test ./...   # run all tests
```

`shell3 telegram --console` drives the whole bot loop over stdin/stdout with no
credentials and no network — the way to exercise the front-end by hand.

## AI artifacts are not committed

Design specs, implementation plans, and other AI-generated working notes are
**gitignored, never committed** — `docs/dev/*` (except its `README.md`),
`docs/superpowers/`, `docs/dev/superpowers/`, and `ai-do-not-read.*`. Keep them
local; the repo carries only shipped documentation (top-level `README.md`,
`docs/`, `docs/cookbook/`). If you generate a design/plan doc, leave it in
`docs/dev/` where the ignore rule keeps it out of commits.
