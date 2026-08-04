# CLI reference

Five subcommands: `serve` (the service — agent + web interface + cron),
`boot` (setup), `project` (scaffold a Chain of Command project), `health`
(config check), and `ask` (a local driver for the agent). Bare `shell3`
prints help.

Every subcommand except `boot` takes `-c`/`--config <dir>`: a path to a config
directory (`shell3.yaml`, `agent.md`, …); the default is `~/.shell3`. The
working directory is never consulted. (`boot` always scaffolds `~/.shell3`;
for `project` the flag lives on `project new`.) `shell3 --version` prints the
installed build.

## `shell3 serve` — run the agent and its web interface

```sh
shell3 serve                       # ~/.shell3, http://127.0.0.1:8765
shell3 serve -c ~/work-agent
shell3 serve --addr 127.0.0.1:9000 # overrides web.addr
```

Loads the config, serves the single-page app (embedded in the binary) and its
API, arms cron jobs, and runs the
[runs janitor](configuration.md#the-runs-janitor--runs_keep_days) once — then
blocks until interrupted. The runtime is anchored to the config directory, so
history lives under `~/.shell3/.shell3_project/`. The agent's shell runs in
`web.workdir` (default: the config dir).

**Authentication is required.** `web.password` gates every route; serve refuses
to start without it, because reaching the port means reaching a shell. You log
in once per browser and the session lasts a week, renewed as you use it.
`web.totp_secret` adds an authenticator-app code on top. Both are `.env`
secrets — see
[configuration.md](configuration.md#authentication--webpassword). It still binds
`127.0.0.1:8765` by default; a non-loopback `--addr`/`web.addr` starts but warns
that plain http carries the password in clear. And a password is not a reason to
drop a proxy that authenticates in its own right — see
[Security](security.md#the-web-interface).

**Exposure is yours.** shell3 starts nothing on your behalf: put it behind
Tailscale (`tailscale serve --bg 8765` — the recommended default), an SSH
forward, or a reverse proxy, and set `web.url` when the address is stable —
[deploying.md](deploying.md) has the ranking. serve prints a fixed
`web.url` at start, so the address you hand out is in the log; from that
moment the login password is the boundary, and a session is a shell.

**Threads and turns.** Each browser thread is its own session; the
thread→session map persists in
`~/.shell3/.shell3_project/web_threads.jsonl`, so threads survive a restart
(sessions the janitor swept start clean). One main-agent turn runs at a time —
a message sent while a turn is running is refused with a note in the stream
rather than queued. Background jobs (subagents, `bash_bg`, cron) run
independently and come back through the [notifier](configuration.md#the-notifier--notifiermd):
a post lands in the notification bell, a wake runs another turn (in the owning
thread when it is still live, otherwise a fresh one).

### The HTTP API

Everything the app does is a plain endpoint on the same port, so `curl` works
just as well:

| Endpoint | What |
|----------|------|
| `POST /api/chat` | Run one turn, streamed as SSE in the AI SDK "UI message stream" dialect. Body: `{id, messages}`; only the newest user message is read (shell3 keeps its own history). |
| `GET /api/events` | SSE push stream: notifications and approval requests. Parked asks are replayed on connect. |
| `POST /api/login` | Exchange the password (+ TOTP code when enrolled) for a session cookie. The one API route that needs no session. |
| `POST /api/logout` | Revoke the current session server-side. |
| `POST /api/asks/{id}` | Answer a gate approval: `{"allow": true}` or `{"allow": false}`. |
| `GET /api/threads` | List conversations (`?archived=true` for the archive). |
| `POST /api/threads/{id}` | Rename (`{"title": …}`) or archive (`{"archived": true}`) a conversation. |
| `DELETE /api/threads/{id}` | Delete a conversation. |
| `GET /api/threads/{id}/messages` | That thread's stored user/assistant text, for replay when it is reopened. |
| `GET /api/capabilities` | What this install can do: agent name/model, voice (stt/tts), imagegen. |
| `GET /api/status` | Version, config dir, uptime, config load warnings, whether the command gate is armed, the effective system prompt, model params (value + default), the last turn's token usage, agent + tools + context window + subagents + projects + skills + cron + MCP servers, running/capacity jobs. |
| `GET /api/files?path=` | List a directory under the config root. Credential files are flagged `redacted`, never read. |
| `GET /api/files/content?path=` | Read one file (256 KiB cap). Returns `size` plus `redacted` / `binary` / `truncated` flags; `.env` and its dotenv siblings come back redacted without being opened, and a file containing a NUL byte comes back flagged `binary` with empty content. |
| `GET /api/jobs` | Running and finished background jobs (id, kind, label, status, elapsed, exit code). A finished job's `elapsedSeconds` is how long it took, not how long ago it started. |
| `GET /api/jobs/{id}` | One job plus its output: a subagent's stored transcript, or a command's captured stdout (`outputKind` says which). |
| `POST /api/jobs/{id}/cancel` | Cancel a job; cancelling a subagent cascades to the jobs it started. |
| `GET /api/cron` | Declared cron jobs: schedule, agent, workdir, `direct`, prompt, last run and the job id it started. `armed` is false when no scheduler is running. |
| `POST /api/cron/{name}/run` | Fire a scheduled job now. `409` when no scheduler is armed, `404` for an unknown name. |
| `GET /api/runs` | Stored sessions, newest first (100 max) — conversations, subagent children, cron runs, `shell3 ask` sessions. `threadId` is set when the run is a browser conversation. |
| `GET /api/runs/{id}` | One session's full transcript: text, reasoning, tool calls with arguments, tool results. |
| `GET /api/push` | Whether web push is available on this install, and the public VAPID key a browser needs to subscribe (plus the current subscription count). |
| `POST /api/push/subscribe` | Register one browser's push subscription (the body is `PushSubscription.toJSON()`). `501` when push is unavailable. |
| `DELETE /api/push/subscribe` | Forget a subscription: `{"endpoint": …}`. |
| `POST /api/push/test` | Send a test notification to every subscribed browser. `409` when nothing is subscribed. |
| `POST /api/stop` | Cancel the running turn and the background jobs that session started. |
| `POST /api/reload` | Re-read the config and apply it live. `409` while a turn is in flight. |
| `POST /api/stt` | Transcribe an uploaded recording (multipart `audio`) via `media.stt`. `501` when unconfigured. |
| `POST /api/tts` | Speak `{"text": …}` via `media.tts`, returning audio. `501` when unconfigured. |
| `GET /api/media` | List `~/.shell3/media/` — uploads and generated images, newest first. |
| `GET /api/media/{name}` | Serve a file from `~/.shell3/media/` — how generated images render inline. Flat by construction: any path separator is refused. |

### The interface

Six views, plus the approval modal and the notification bell. The sidebar pins
**Chat** and puts the five operational views (Jobs, Cron, Runs, Status, Files)
behind one collapsible **Tools** group, which opens itself when one of them is
showing.

It is set as a printed document — the run log it actually is. Two stocks:
paper, and a cyanotype for the dark theme. Six page types share four devices
(the ruled section head, the dotted leader, the hanging figure column, and the
marker), and the shell3 yellow (`#EAB308`) is the marker — it only ever marks
what is live: the open conversation, a running job, the next cron to fire, the
redacted `.env`. See `webui/README.md` for how that is put together.

- **Chat** — streaming markdown, tool calls, reasoning; a thread list;
  light/dark theme. A sent message is not editable and a reply is not
  regenerated: what was said stays said, which is also why there are no
  branches to pick between. A tool call shows what it ran
  (`bash · echo one`) and opens to its arguments and output; reasoning shows
  the opening of the thought. Reopening a thread replays all of it. Typing
  `/` opens the command menu: **`/compact`** summarises the conversation so
  far and reports the context it freed. **Stop** ends the turn on the server,
  killing the background jobs it started — what was already said stays.
- **Jobs** — running and finished background work: `bash_bg` commands and
  subagents, with elapsed time, a live output tail (pushed over
  `/api/events`), exit code, per-job transcript (subagents) or captured
  stdout (commands), and a cancel button. Refreshes itself.
- **Cron** — every scheduled job: schedule, agent, workdir, whether it is
  `direct` (straight to the agent) or notifier-triaged, its full prompt, the
  last run, and a **Run now** button. The last run links through to the job
  in **Jobs**, so you can see what it actually did. Says so plainly when no
  scheduler is armed.
- **Runs** — every stored session on disk: conversations, subagent children,
  cron runs, and `shell3 ask` sessions alike, replayed at full fidelity —
  tool calls with their arguments, tool results, and reasoning.
- **Status** — the same data as `/api/status`: the agent, its tools (hover a
  tool for its description), the effective system prompt (collapsed), model
  parameters with values and defaults, context window and the last turn's
  token usage against it, config load warnings, subagents, projects, skills,
  cron jobs, MCP server health, job counts, and whether the **command gate**
  is armed or the shell is running ungated. A Reload config button applies
  config edits live. Refreshes itself.
- **Files** — two read-only roots: **config**, a walk of the config directory
  (`.env` and its siblings are listed but never opened; binary and oversized
  files are labelled rather than dumped), and **media**, the contents of
  `~/.shell3/media/` newest-first, with inline previews for images and audio.
- **Notifications** — the bell is fed by `/api/events`: notifier posts, cron
  results, job-failure alerts, and a note when the conversation was compacted
  to fit the context window. The server keeps the 50 most recent and replays
  them when a browser connects, so background work that finished while the
  tab was closed is still there when you come back. (Provider retries are not
  notifications — they go to the app log.)
- **Push** — a toggle in the bell asks the browser for notification permission,
  subscribes, and registers that subscription with the server; a **Test**
  button then sends one through the whole path. Once it is on, everything the
  bell shows is also pushed, so a finished job reaches a closed tab or a
  phone's lock screen. A service worker at `/sw.js` handles delivery and
  click-to-focus (it caches nothing — the app is served by the binary and
  updated by restarting it). Push needs a **secure context**: it works on
  `localhost` and over https, but not over plain http to another host, and the
  toggle says so instead of appearing broken. No configuration —
  the keypair is generated on first start; see
  [configuration.md](configuration.md#push-notifications).
- **Approvals** — a hook `ask` verdict raises a modal with the reason and the
  command; Allow/Deny answers it. No browser attached means no answer, and an
  unanswered ask denies at its timeout.
- **Voice** — the composer's dictation button records and transcribes through
  `media.stt`; the read-aloud control speaks through `media.tts`. With neither
  configured the browser's own Web Speech APIs stand in.

## `shell3 boot` — set up a config

```sh
shell3 boot     # interactive form: model endpoint + key, vision, workdir
```

An interactive form scaffolds the config tree under `~/.shell3/`:
`shell3.yaml` (models + a `web:` block bound to loopback), `agent.md`, a
read-only `agents/explorer.md` subagent, `skills/`, **armed**
`hooks/*.tool-call.sh` gate scripts (credentials, system paths, unread remote
code, publishing and force-pushes refused; ordinary work untouched), and `.env`
(secrets — never commit it).
One step asks whether the model can see images: yes enables the `read_media`
tool and wires `media.describe` to the main model; no leaves media tooling off
until you add a vision model. Another asks where the agent's shell should run
(`web.workdir`; blank = the config dir). Another asks for the **interface
password** — required, 16 characters minimum, with a generated suggestion you
can accept as-is; it is printed once at the end, so save it. A following step
offers a **second factor**, printing a QR code to scan with an authenticator
app; enrolling wires `web.totp_secret` so the login asks for the code.
Declined it, or lost the phone? `shell3 boot --totp` enrols or resets any
time (a fresh secret and QR against the existing config); and losing the
phone is never a lockout, since the secret is a line in `.env` you can
delete.

boot installs nothing and exposes nothing: it configures shell3 and stops
there. Its closing note says as much, and points at
[deploying.md](deploying.md) for keeping `serve` running and reaching it from
elsewhere.
Scriptable via flags (any flag skips its prompt; with no TTY, unset flags take
defaults, except `--model`, which headless boot requires): `--url`, `--model`,
`--name`, `--key`, `--vision`, `--workdir`, `--context-window`,
`--compact-at`, `--proxy`, `--force`. (The TOTP offer is TTY-only; headless
boot skips it.)
`shell3 boot --show` reprints the post-boot summary for the existing config
without writing or asking anything.
See [configuration.md](configuration.md).

## `shell3 project new` — scaffold a project

A **project** is a `projects/<name>/` config dir: a `project.md` brief plus a
`manager.md` subagent that owns the work and whose shell runs in the project's
workdir. `shell3 project new` scaffolds one, then appends an index line to
`projects.md`.

```sh
shell3 project new site --workdir ~/code/site --description "marketing site"
shell3 project new api  --workdir ~/code/api  --copy-skills site
```

- `--workdir` (required) — the directory the manager's shell runs in; must
  already exist.
- `--description` — short project description (default `"<name> project"`).
- `--copy-skills <name>` — seed `skills/` by copying an existing project's.

It writes `projects/<name>/project.md`, `projects/<name>/manager.md`, and an
empty `skills/`, and prints the created paths. `ls`/`cat` see the project
immediately; a reload (`POST /api/reload`) or a restart registers the manager
for dispatch.
The command is designed for the agent to drive from a `bash` call — its `-h`
output is the contract the agent reads before invoking it. See
[configuration.md](configuration.md#projects--projects).

## `shell3 health` — check the config

```sh
shell3 health                # ~/.shell3
shell3 health --config ~/work-agent
```

Loads the config exactly like `serve` would and fails (exit 1) on anything the
server only warns about — a skill `.md` skipped for broken frontmatter, a hook
file naming no subagent, a missing `web.password` or one under 16 characters
(it prints the auth mode: password, or password + TOTP). It also dry-runs every hook script with a probe
payload (a script error fails health; a strict gate that blocks the probe
passes) and connects every MCP server. It also validates every
`projects/<name>/` (brief frontmatter, an existing workdir, a manager whose
name doesn't collide) and prints one line per project. Run it after editing
the config tree, before reloading.

## `shell3 ask` — drive the agent locally

Runs the same config + agent from your terminal and prints everything a chat
surface hides: reasoning, every tool call with raw args, untruncated results,
token usage. It follows subagent/`bash_bg` jobs the turn spawned and renders
their completions. `ask` is host-agnostic — it reads nothing from the `web:`
block (its session runs in the config dir) and shares the same runs store, so
`serve` and `ask` see each other's history.

**Hook approval asks.** In an interactive terminal (no `-p`, a TTY attached),
a tool-call hook `ask` verdict prompts you on the terminal with the reason and
command and reads a `y/N` answer — anything but an explicit yes denies. With
`-p` (scripted) or no TTY there is no human to ask, so the run is **headless**:
the hook payload's `headless` flag is true and every `ask` verdict auto-**denies**
(matching the hooks contract — a headless ask never silently runs).

```sh
shell3 ask                        # no message: opens an interactive multi-turn chat
shell3 ask "list the files here and summarize this project"
shell3 ask -p "same, as a flag"   # -p/--prompt, for scripts and headless runs
shell3 ask --resume               # continue the latest session (multi-turn across invocations)
```

With no message and a terminal attached, `ask` opens an interactive loop:
each completed turn (and any jobs it spawned) reads another message, until
ctrl+c. A headless invocation (no TTY) must pass a message via an argument or
`-p`.

**Background jobs and `-p`.** When a turn spawns a subagent or `bash_bg` job,
`ask` stays alive after the turn ends and waits for those in-process jobs to
complete, rendering each completion's wake turn — so a scripted `ask -p` run
never exits at turn end and silently kills in-flight work. The wait has no
timeout; press ctrl+c (SIGINT) to quit while jobs are still running.

## Reading your history

Conversation history is plain JSONL under the config directory's
`.shell3_project/runs/`:

```sh
rg -n "JWT|expiry" ~/.shell3/.shell3_project/runs   # full-text search all sessions
ls -lt ~/.shell3/.shell3_project/runs/              # sessions, newest first
cat ~/.shell3/.shell3_project/runs/<id>/meta.json   # one session's metadata
```

The agent searches its own past the same way (`rg` over the JSONL, via the
`history` skill); each subagent run has its own stored transcript. The
interface's **Runs** view reads the same store — every session, including
subagent children, cron runs and `shell3 ask` sessions, with tool calls,
arguments, results and reasoning. Old
sessions are swept at `shell3 serve` startup — see
[`runs_keep_days`](configuration.md#the-runs-janitor--runs_keep_days).

## Platform support

Unix-like systems only — Linux and macOS (WSL works). Windows is not
supported: shell3 leans on Unix process groups.
