# CLI reference

Five subcommands: `telegram` (the service — agent + bot + cron), `boot`
(setup), `tool` (check, run and test the tools a kit declares), `health`
(config check), and `ask` (a local driver for the agent). Bare `shell3`
prints help.

Every subcommand except `boot` and `tool` takes `-c`/`--config <dir>`: a path
to a config directory (one holding a `shell3.sh` kit); the default is
`~/.shell3`. The working directory is never consulted.
(`boot` always scaffolds `~/.shell3`; `tool` takes a kit path as its
argument.) `shell3 --version` prints the installed build.

## `shell3 telegram` — run the agent and its bot

```sh
shell3 telegram                    # ~/.shell3
shell3 telegram -c ~/work-agent
shell3 telegram --console          # dev transport: the same bot loop over stdin/stdout
```

Loads the config, connects to the Telegram Bot API, arms cron jobs, and runs
the [runs janitor](configuration.md#the-runs-janitor--runs_keep_days) once,
then long-polls until interrupted. It needs `telegram.token` and
`telegram.chat_id`; without them it refuses to start. The runtime is anchored
to the config directory, so history lives under
`~/.shell3/.shell3_project/`. The agent's shell runs in `telegram.workdir`
(default: the config dir).

At startup it registers the `/` command menu with Telegram and greets the
chat, so you can see there that the bot came up.

**Nothing listens.** shell3 makes outbound connections only; access control
is the token and the `chat_id` — see
[Security](security.md#the-telegram-boundary).

**One conversation.** Every message you send continues the **same**
long-lived conversation — just type; no replying, no threading rules. A
Telegram reply adds the quoted text as context for the agent but never
switches conversations. `/new` starts a fresh one (the old conversation
stays in the dash's runs listing and the agent's searchable history); a restart resumes
where you left off, and automatic compaction keeps the context bounded
however long it runs. One main-agent turn runs at a time, but sending
always succeeds — and a **text message sent mid-turn steers the running
turn**: the agent sees it at its next step, so "stop, wrong file" redirects
work in flight (messages with attachments queue and run after). While the
agent works you see a **progress bubble** — one message listing the tools
it's running, updating in place — which deletes itself once the answer
arrives (it stays behind only when the turn failed, as a breadcrumb).
`/stop` cancels the running turn (the dash shows what's queued).
Background jobs (subagents, `bash_bg`, cron) run independently and come back
as [task reports](configuration.md#task-reports): a failure or a
`direct` result posts to the chat (🔔, or ⏰ for a cron origin); everything
else hands the agent a report, whose reply reaches you as an ✉️ update — only when
the result warrants it (it stays silent otherwise). Every message you
didn't directly cause carries a marker (⏰ cron, 🔔 completion, ⚠️ failure,
✉️ update); bare text is always a direct reply to something you sent. A ⚠️
also stands in for a reply the model mangled — see
[malformed output](configuration.md#provider-specific-knobs--extra).
✉️ updates always arrive without a
notification ping (an update is not a page); `/quiet on` extends that to ⏰/🔔
posts too. Replies to you and ⚠️ failures always ring.

`--console` swaps the Telegram transport for stdin/stdout and drives the same
bot loop with no credentials and no network: a plain line is a fresh message,
`@<id> text` is a reply quoting that message, `/…` is a command, EOF quits.

### Commands

Answered by the bot itself: no model call, no tokens. What used to be the
view commands (`/status`, `/jobs`, `/runs`) is now the **web dash** — a
read-only page on `127.0.0.1` that `/dash` links you to (see
[the dash](#the-web-dash) below).

| Command | What |
|---------|------|
| `/dash` | The dashboard URL with a fresh access token (valid ~1h). `/dash <text>` instead asks the agent for dash help — most usefully `/dash help exposing`, which sets up a tunnel via the dash-exposing skill. |
| `/stop` | Cancel the running turn. Background jobs are **not** killed — they keep running and still report back. |
| `/superstop` | Cancel the turn AND kill every background job — subagents (cascading to their jobs), `bash_bg` commands, in-flight cron dispatches; the cron schedule stays armed. One ⚠️ summary lists what died (id, kind, runtime); the same summary is queued into the conversation so the agent knows next turn, and the killed jobs' own completion posts are suppressed. |
| `/new` | Start a fresh conversation. The old one stays in the dash's runs listing and the history index; running jobs keep going and report into the new conversation. Refused mid-turn (`/stop` first). |
| `/run <name>` | Fire a scheduled job now. |
| `/btw <question>` | Ask outside the conversation: answered in its own child session, never entering the main context. |
| `/reload` | Re-read the config and apply it live. Takes the turn slot, so it is refused rather than raced while a turn runs. |
| `/quiet on\|off` | Deliver ⏰/🔔 background posts silently — no notification ping (✉️ updates are always silent); replies to you and ⚠️ failures always ring. Bare `/quiet` reports the state, which persists in `~/.shell3/quiet_mode.json`. |

Anything the kit declares with a `command:` block joins this list — its shell
function answers the verb, the text after it arrives as `$ARG`, and stdout is
the reply. See [kits.md](kits.md#commands).

### The web dash

`shell3 telegram` always binds a read-only HTTP dashboard
on `127.0.0.1` (`dash_port` in the wiring, default 7333, `0` disables). It
renders from live runtime state and the runs store: the index (version,
agent, model, context usage, gate, tools, skills, MCP health, warnings, and
a one-tap link to the live conversation's transcript; background jobs, each
bash_bg job's id linking to its captured output log; every cron job with
schedule/outcome/rolling 7-day dispatched-run cost, each linking to its
detail; and the queued inbox), a paginated runs listing, a full run replay
(tool calls with arguments, results, reasoning — folded, escaped,
self-contained), and a read-only browser of the config directory (the kit,
skills — syntax left as escaped text; credential files are
listed but their contents redacted, binary and >256 KB files flagged not
dumped). No polling, no scripts beyond the replay's fold-all buttons; a
floating ↻ button bottom-right re-requests the page.

Every request needs `?t=<token>`: `/dash` mints one (32 random bytes, ~1h,
several may be live at once, all forgotten on restart). Anything else gets a
bare 403. The base URL lives in `dash_url.txt` beside the config — seeded
with the localhost address, overwritten by the dash-exposing skill when you
ask `/dash help exposing` to set up a tunnel (tailscale, cloudflared, or
ngrok, in that order of preference). HTTPS is the tunnel's business; the
listener itself never binds beyond loopback.

### Attachments and media

Files you send are saved under `~/.shell3/media/` and their paths go into
the prompt; the agent reads them back with `read_media` and sends files to
you with `send_media_telegram`. There is no built-in transcription or
captioning step — voice notes and images are handled at the agent's
discretion, via wrapper scripts you install. See
[Voice & images](cookbook/voice-images.md).

## `shell3 boot` — set up a config

```sh
shell3 boot     # interactive form: model endpoint + key, vision, bot token, workdir
```

An interactive form scaffolds the config tree under `~/.shell3/`:
`shell3.sh` — the kit, holding the wiring (models + a `telegram:` block), the
main agent and a general-purpose `assistant` employee — plus
`skills/`, and `.env` (secrets — never commit it). The kit's `gate:` function
ships **armed** (credentials, system paths, unread remote code, publishing and
force-pushes refused; ordinary work untouched). `--force` overwrites an existing config.

The form asks for the model endpoint, tag, name and key; whether the model can
see images (yes adds the `media` tool — `read_media` — to the agent's
frontmatter so it can open image/audio/PDF files directly; no leaves
that tool out until you add a vision model); the
context window and auto-compaction threshold; an optional proxy command; the
**Telegram bot token** (from [@BotFather](https://t.me/BotFather)) and **chat
id** (your numeric id — [@userinfobot](https://t.me/userinfobot) prints it);
and where the agent's shell should run (`telegram.workdir`; blank = the config
dir). The token goes to `.env` as `TELEGRAM_TOKEN`, referenced from the kit's
wiring block as `env:TELEGRAM_TOKEN` like every other secret; both fields may
be left blank and filled in later. Secrets echo visibly, so you can see that a
paste landed intact.

**To use the bot in groups**, turn privacy mode OFF in @BotFather
(`/setprivacy` → your bot → Disable) or promote the bot to admin in each
group. Telegram does not deliver a plain `@yourbot do X` message to a
privacy-mode bot at all, so the @mention trigger cannot work without it. With
privacy off the group's messages reach shell3, which answers only the ones
addressed to it by someone on `allow_from` and discards the rest. List the
user ids that may drive the agent in `telegram.allow_from` — in a group the
chat id is not a user id, so without it nobody is allowed and shell3 refuses
to start.

Without any BotFather change, `/ask <message>` opens a thread in a group and
plain replies to the bot continue it; that is the zero-setup path. Promoting
the bot to admin in a group buys two things: plain @mentions work, and the
bot can read that group's description, which becomes standing context for the
room. Both are optional — see
[configuration.md](configuration.md#telegram--telegram).

boot installs nothing and exposes nothing: it configures shell3 and stops
there. Its closing note says as much, and points at
[deploying.md](deploying.md) for running the bot as a service.

Scriptable via flags (any flag skips its prompt; with no TTY, unset flags take
defaults, except `--model`, which headless boot requires): `--url`, `--model`,
`--name`, `--key`, `--vision`, `--workdir`, `--context-window`,
`--compact-at`, `--proxy`, `--tg-token`, `--tg-chat-id`, `--force`.
`shell3 boot --show` reprints the post-boot summary for the existing config
without writing or asking anything. `shell3 boot --prompts` refreshes the
scaffold-shipped prompt files (`skills/`) in an existing install after you
upgrade the binary — run it to pick up new built-in skills like
`dash-exposing`; your kit (cron jobs included), `.env` and hand-written
skills are left untouched (replaced files back up under `.backup/`).
See [configuration.md](configuration.md).


## `shell3 tool` — check, run and test a kit's tools

```sh
shell3 tool check ~/.shell3/shell3.sh                       # syntax, lint, every manifest
shell3 tool run   ~/.shell3/shell3.sh fetch-thing '{"url":"…"}'   # one call, no model
shell3 tool test  ~/.shell3/shell3.sh [tool]                # the declared tests
```

The author's loop for the tools a kit declares. `check` catches an
unterminated block, a duplicate name, a mistyped param, an unquoted
description with a comma in it. `run` invokes one tool with JSON arguments —
no session, no tokens — which is how you probe a tool against something real
before trusting it. `test` runs the `test:` blocks, whose harness can stub any
command so a tool's parsing is tested without its network calls.

It takes a kit path rather than `--config`: the kit is the unit being checked.
See [tools.md](tools.md).

## `shell3 health` — check the config

```sh
shell3 health                # ~/.shell3
shell3 health --config ~/work-agent
```

Loads the config exactly like the bot would and fails (exit 1) on anything
the running bot only warns about — a skill `.md` skipped for broken
frontmatter, a hook file naming no subagent. It also dry-runs every hook
script with a probe payload (a script error fails health; a strict gate that
blocks the probe passes), connects every MCP server, and validates every
the config directory. A `telegram:` block `shell3 telegram` would
refuse — blank `token` or `chat_id`, a non-numeric `chat_id` — fails here,
naming the field; no `telegram:` block at all is reported but not failed,
since an `ask`-only config is legitimate. Run it after editing the config
tree, before reloading.

## `shell3 ask` — drive the agent locally

Runs the same config + agent from your terminal and prints everything the chat
hides: reasoning, every tool call with raw args, untruncated results, token
usage. It follows subagent/`bash_bg` jobs the turn spawned and renders their
completions. `ask` is host-agnostic: it reads nothing from the `telegram:`
block (its session runs in the config dir) and shares the same runs store, so
the bot and `ask` see each other's history.

**Headless runs.** With `-p` (scripted), with `--agent`, or with no TTY there
is no human attached, so the run is **headless**: gate scripts see
`headless: true` in their payload and can refuse accordingly. The chat UI is
never headless — it only opens with a real terminal on both ends and someone
typing into it, whatever `stderr` is redirected to. There is no approval
prompt in either mode: a gate allows, rewrites, runner-swaps, soft-denies to
the reviewer, or blocks, and that's the whole vocabulary.

```sh
shell3 ask                        # no message: opens the full-screen chat UI
shell3 ask "list the files here and summarize this project"
shell3 ask -p "same, as a flag"   # -p/--prompt, for scripts and headless runs
shell3 ask --resume               # continue ask's own last conversation
```

### The chat UI

With **no message** and a terminal attached, `ask` opens a full-screen chat
(`internal/askui`) — the terminal alternative to the Telegram front-end. The
input is always live, so there are no modes: type and press enter. Assistant
replies render as markdown; each tool call and thinking block is a collapsible
one-line summary you expand when you want the detail; the footer carries the
model, its context fill, the running background-job count, and the active
agent. It adapts to a light or dark terminal.

| key | does |
| --- | --- |
| `enter` | send |
| `shift+↵` | newline (`alt+↵` / `ctrl+j` also work) |
| `enter` **during a turn** | steer the running turn (delivered at the next round boundary) |
| `ctrl+o` | fold every tool and thinking block · again unfolds them all |
| `pgup` / `pgdn` | scroll a page (scrolling up unsticks the view from the bottom) |
| `ctrl+c` | stop the running turn · with nothing running, again to quit |

The mouse does three things at once, and none of them takes a mode:

| mouse | does |
| --- | --- |
| wheel | scroll the transcript |
| drag | select lines — the selection highlights as you go, and dragging past the top or bottom edge scrolls to extend it |
| release | copy the selection (OSC 52 **and** the native clipboard, so it works over SSH and in terminals without OSC 52) |
| click | fold or unfold the one block you clicked |

Copy is WYSIWYG: host chrome that is never highlighted — system reminders, the
thinking indicator line, the gaps between blocks — is never copied either.
Reasoning *content* is ordinary selectable text; only its indicator line is
excluded.

The keyboard's `ctrl+o` is deliberately all-or-nothing rather than a per-block
toggle: the input is always live, so a plain key can't be a binding, and
click-to-fold already covers "that one block". Your terminal's own click-drag
selection is unavailable while the app runs (a full-screen app that captures
the mouse always takes it); the app's own selection replaces it, and holding
Shift still falls through to the terminal's if you prefer it.

A headless invocation (no TTY, or a terminal reporting no size) must pass a
message via an argument or `-p` — there is nothing to draw on.

### One conversation per front-end

`ask` keeps its **own** conversation, separate from every Telegram room's. A
plain `ask` starts a fresh session; `--resume` follows ask's own thread marker
(the `ask` surface in the runs store's `threads` table), never "the newest
session in this workdir" — which, with the bot running, would be whatever chat
it last answered in. Run the bot and `ask` at the same time and neither
inherits the other's context; both still share the runs store, so each sees
the other's history through the `history` tool and the dash.

**Background jobs and `-p`.** When a turn spawns a subagent or `bash_bg` job,
`ask` stays alive after the turn ends and waits for those in-process jobs to
complete, rendering each completion's mail turn, so a scripted `ask -p` run
never exits at turn end and silently kills in-flight work. The wait has no
timeout; press ctrl+c (SIGINT) to quit while jobs are still running.

### `--agent <name>` — one subagent turn, for scripts

`--agent` runs a single headless turn of a kit-declared employee and
prints **only its reply** on stdout; the config path, job id, and any error go
to stderr. It is the seam for batch work — a script that has its own loop
(a database to walk, a queue to drain) but needs a model call per item:

```sh
shell3 ask --agent drafter -p "$(build_one_prompt)" > draft.txt
```

The point is that the script does **not** hand-roll an HTTP client against the
model. Shelling out here runs the real adapter, so the call inherits
reasoning-channel separation (thinking never lands in the answer text),
think-leak filtering, truncation detection, the tool-call hook, and a
tool-capable turn. A hand-written client re-derives all of that, usually
badly: the classic failure is sharing one `max_tokens` budget between thinking
and answer, so the model spends it on reasoning, gets cut mid-thought, and the
caller's regex — finding an unclosed think tag — deletes the answer along with
it.

The run is always headless (hooks see `headless: true`) however the message
arrives. It exits nonzero if the agent errored or produced no output, printing
whatever partial text it did produce first. `--agent` needs a message and
cannot be combined with `--resume`: each run dispatches a fresh child session,
so there is no conversation to continue.

## Reading your history

Conversation history lives in one SQLite database,
`~/.shell3/.shell3_project/shell3.db`, with full-text search over every
user and assistant message. Query it read-only with the `sqlite3` CLI:

```sh
sqlite3 -readonly ~/.shell3/.shell3_project/shell3.db \
  "SELECT session_id, seq, snippet(messages_fts,0,'[',']','…',16)
   FROM messages_fts WHERE messages_fts MATCH 'JWT OR expiry'
   ORDER BY rank LIMIT 10"
sqlite3 -readonly ~/.shell3/.shell3_project/shell3.db \
  "SELECT id, model, status, last_at FROM sessions ORDER BY id DESC LIMIT 20"
```

The agent searches its own past with the built-in `history` tool (see
[configuration.md](configuration.md#recalling-past-conversations--the-history-tool)); a `bash_bg` job's
full output is a plain file at
`.shell3_project/runs/<session>/jobs/<job>.log`. The dash's runs pages
and replays read the same store: every session, including subagent
children, cron runs and `shell3 ask` sessions, with tool calls, arguments,
results and reasoning. Sessions older than 30 days are swept at startup —
see [`runs_keep_days`](configuration.md#the-runs-janitor--runs_keep_days)
to change that, or set `0` to keep everything forever.

## Platform support

Unix-like systems only — Linux and macOS (WSL works). Windows is not
supported: shell3 leans on Unix process groups.
