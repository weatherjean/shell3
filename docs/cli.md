# CLI reference

Five subcommands: `telegram` (the service — agent + bot + cron), `boot`
(setup), `project` (scaffold a Chain of Command project), `health` (config
check), and `ask` (a local driver for the agent). Bare `shell3` prints help.

Every subcommand except `boot` takes `-c`/`--config <dir>`: a path to a config
directory (`shell3.yaml`, `agent.md`, …); the default is `~/.shell3`. The
working directory is never consulted. (`boot` always scaffolds `~/.shell3`;
for `project` the flag lives on `project new`.) `shell3 --version` prints the
installed build.

## `shell3 telegram` — run the agent and its bot

```sh
shell3 telegram                    # ~/.shell3
shell3 telegram -c ~/work-agent
shell3 telegram --console          # dev transport: the same bot loop over stdin/stdout
```

Loads the config, connects to the Telegram Bot API, arms cron jobs, and runs
the [runs janitor](configuration.md#the-runs-janitor--runs_keep_days) once —
then long-polls until interrupted. It needs `telegram.token` and
`telegram.chat_id`; without them it refuses to start. The runtime is anchored
to the config directory, so history lives under
`~/.shell3/.shell3_project/`. The agent's shell runs in `telegram.workdir`
(default: the config dir).

At startup it registers the `/` command menu with Telegram, clears any menu
button an older build left behind, and greets the chat — so a bot that came up
says so in the chat itself, not only on stderr.

**Nothing listens.** shell3 makes outbound connections only; there is no port,
no login, no tunnel. Access control is the `chat_id`: updates from any other
chat are dropped before a turn starts. See
[Security](security.md#the-telegram-boundary).

**Threads and turns.** Every inbound message starts its **own** session. To
continue one, use Telegram's reply on any message in that thread — the
message→session map persists in
`~/.shell3/.shell3_project/telegram_threads.jsonl`, so threads survive a
restart (sessions the janitor swept answer that they can't be resumed). One
main-agent turn runs at a time: a message sent while a turn is running is
answered with a note and disregarded, not queued — `/stop` and reply to your
last prompt to steer instead. Background jobs (subagents, `bash_bg`, cron) run
independently and come back through the
[notifier](configuration.md#the-notifier--notifiermd): a post lands in the chat
(🔔, or ⏰ for a cron origin), a wake runs another turn — in the owning thread
when its session is still live, otherwise as a fresh replyable message.

`--console` swaps the Telegram transport for stdin/stdout and drives the same
bot loop with no credentials and no network: a plain line is a fresh message,
`@<id> text` is a reply into that thread, `/…` is a command, EOF quits.

### Commands

Answered by the bot itself — no model call, no tokens. `/status`, `/jobs`,
`/job`, `/cron` and a `/run_N` replay render markdown, sent inline when small
and as a `.md` document (plus a short summary) when it would blow past
Telegram's message cap. The `/runs` listing is always inline — its entries are
tappable commands, and Telegram only linkifies those in message text.

| Command | What |
|---------|------|
| `/stop` | Cancel the running turn. Background jobs are **not** killed — they keep running and still report back. |
| `/status` | Version, config dir, agent + model, context window and messages in context, whether the command gate is armed, model params, tool descriptions, subagents, skills, MCP server health, cron jobs, projects, config warnings, and the effective system prompt. Reports whichever session is live — on an idle bot that is the headless cron dispatch parent, which shows `0` messages in context. |
| `/jobs` | Running and finished background work: id, kind, label, status, elapsed, exit code. |
| `/job <id>` | One job plus its output — a subagent's stored transcript, or a command's captured stdout. |
| `/cancel <id>` | Cancel a job; cancelling a subagent cascades to the jobs it started. |
| `/cron` | Declared cron jobs: schedule, agent, workdir, `direct`, the full prompt, and the last run. |
| `/run <name>` | Fire a scheduled job now. |
| `/runs [page\|id]` | Stored sessions, newest first, 8 per page — each entry a tappable `/run_N` that replays that run in full (tool calls with arguments, results, and reasoning). `/runs 2` pages older; `/runs <id>` replays by id directly. |
| `/reload` | Re-read the config and apply it live. Takes the turn slot, so it is refused rather than raced while a turn runs. |
| `/voice off\|inbound\|always` | Whether replies come back spoken (needs `media.tts`). Bare `/voice` opens a three-button menu. The choice persists in `~/.shell3/voice_mode.json`. |

### Attachments and media

Every file you send is saved to `~/.shell3/media/` as `tg-*` and its path is
put in the prompt, so the agent can re-open it later. Before the turn runs:
a **voice note** is transcribed through `media.stt` and the transcript injected
(echoed back to the chat when `media.stt.echo` is set), and a **photo** is
captioned through `media.describe` and injected as `[image: …]`. A failure at
either step surfaces in the chat as a ⚠️ line and the turn still runs with the
file path. See [Voice & images](configuration.md#voice--images--media).

Going the other way, the agent has a `send_media_telegram` tool: it pushes a
local file to the chat as a `photo`, `voice`, `audio`, `video`, or `document`
(the default), refusing a kind the file's extension or size can't satisfy.
`image_generate`
saves to `~/.shell3/media/` and the agent delivers the file with that tool.

### Approvals

A hook script's `ask` verdict posts the command and reason with **Allow** /
**Deny** inline buttons and parks the turn until one is tapped. Fail-safe: a
send failure, a cancelled turn, or a timeout all deny. Subagents and cron jobs
are headless, so an `ask` there denies immediately.

## `shell3 serve` — the agent over stdio JSONL

Runs the same bot loop as `shell3 telegram` — fresh-turn threading, host
commands, hook approvals, completion delivery, cron — but the transport is
newline-delimited JSON on stdin/stdout. This is the bring-your-own front-end
seam: a Discord bridge or a custom dashboard backend spawns `shell3 serve`
and translates its own surface to the wire events. No Telegram credentials
are needed (`telegram.workdir` is still honored when present); there is no
port and no listener — owning the process's stdio is the access model.

Serve keeps its own thread index (`serve_threads.jsonl`), swept by the same
runs janitor at startup. See **[the protocol reference](serve.md)** for the
full event vocabulary.

```sh
printf '%s\n' '{"type":"message","text":"/status"}' | shell3 serve
```

## `shell3 boot` — set up a config

```sh
shell3 boot     # interactive form: model endpoint + key, vision, bot token, workdir
```

An interactive form scaffolds the config tree under `~/.shell3/`:
`shell3.yaml` (models + a `telegram:` block), `agent.md`, a read-only
`agents/explorer.md` subagent, `skills/`, **armed** `hooks/*.tool-call.sh` gate
scripts (credentials, system paths, unread remote code, publishing and
force-pushes refused; ordinary work untouched), and `.env` (secrets — never
commit it). `--force` overwrites an existing config.

The form asks for the model endpoint, tag, name and key; whether the model can
see images (yes enables the `read_media` tool and wires `media.describe` to the
main model; no leaves media tooling off until you add a vision model); the
context window and auto-compaction threshold; an optional proxy command; the
**Telegram bot token** (from [@BotFather](https://t.me/BotFather)) and **chat
id** (your numeric id — [@userinfobot](https://t.me/userinfobot) prints it);
and where the agent's shell should run (`telegram.workdir`; blank = the config
dir). The token goes to `.env` as `TELEGRAM_TOKEN`, referenced from
`shell3.yaml` as `env:TELEGRAM_TOKEN` like every other secret; both fields may
be left blank and filled in later. Secrets echo visibly — boot runs on your own
terminal, and a paste you can't see is a truncated paste waiting to happen.

On Linux with systemd, and only when the token and chat id are both set, a
final step offers to install shell3 as a **systemd user service**
(`shell3.service`, running `shell3 telegram`): the unit is written to
`~/.config/systemd/user/`, enabled, lingering is turned on
(`loginctl enable-linger`, so it runs without an active login and starts at
boot), started immediately, and verified — boot polls the unit and reports a
crash-loop instead of claiming success.
Caveat spelled out at the end of boot too: a user service cannot prevent the
machine from **sleeping**; on a laptop, disable suspend (or host shell3 on an
always-on box) or the bot is gone while the lid is closed.
`shell3 boot --service` re-runs just this step against the existing config —
the repair path after updating the binary or when the unit points at a stale
one; it rewrites the unit, restarts it, and verifies it came up.

Scriptable via flags (any flag skips its prompt; with no TTY, unset flags take
defaults, except `--model`, which headless boot requires): `--url`, `--model`,
`--name`, `--key`, `--vision`, `--workdir`, `--context-window`,
`--compact-at`, `--proxy`, `--tg-token`, `--tg-chat-id`, `--force`. (The
service offer is TTY-only; headless boot skips it.)
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
immediately; a `/reload` or a restart registers the manager for dispatch.
The command is designed for the agent to drive from a `bash` call — its `-h`
output is the contract the agent reads before invoking it. See
[configuration.md](configuration.md#projects--projects).

## `shell3 health` — check the config

```sh
shell3 health                # ~/.shell3
shell3 health --config ~/work-agent
```

Loads the config exactly like the bot would and fails (exit 1) on anything the
running bot only warns about — a skill `.md` skipped for broken frontmatter, a
hook file naming no subagent. It also dry-runs every hook script with a probe
payload (a script error fails health; a strict gate that blocks the probe
passes) and connects every MCP server. It validates every `projects/<name>/`
(brief frontmatter, an existing workdir, a manager whose name doesn't collide)
and prints one line per project, and says so when `notifier.md` is absent
(background completions then post raw). It runs the Telegram front-end's own
start-up check too, so a `telegram:` block `shell3 telegram` would refuse —
a blank `token` or `chat_id`, a non-numeric `chat_id` — fails here, naming the
field; no `telegram:` block at all is reported but not failed, since an
`shell3 ask`-only config is legitimate. Run it after editing the config tree,
before reloading.

## `shell3 ask` — drive the agent locally

Runs the same config + agent from your terminal and prints everything the chat
hides: reasoning, every tool call with raw args, untruncated results, token
usage. It follows subagent/`bash_bg` jobs the turn spawned and renders their
completions. `ask` is host-agnostic — it reads nothing from the `telegram:`
block (its session runs in the config dir) and shares the same runs store, so
the bot and `ask` see each other's history.

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
`history` skill); each subagent run has its own stored transcript. The `/runs`
pages and `/run_N` replays read the same store — every session, including subagent children,
cron runs and `shell3 ask` sessions, with tool calls, arguments, results and
reasoning. Old sessions are swept at `shell3 telegram` startup — see
[`runs_keep_days`](configuration.md#the-runs-janitor--runs_keep_days).

## Platform support

Unix-like systems only — Linux and macOS (WSL works). Windows is not
supported: shell3 leans on Unix process groups.
