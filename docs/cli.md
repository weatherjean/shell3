# CLI reference

Six subcommands: `telegram` (the service), `web` (the Telegram-free fallback
host), `boot` (setup), `health` (config check), and two local dev front-ends,
`dev` and `dash`. Bare `shell3` prints help.

Every subcommand takes `-c`/`--config <name|path>`: a name resolves to
`~/.shell3/<name>.lua`, a `*.lua` value is a literal path, and the default is
`~/.shell3/shell3.lua`. The working directory is never consulted.

## `shell3 telegram` — run the bot

```sh
shell3 telegram              # ~/.shell3/shell3.lua
shell3 telegram -c work      # ~/.shell3/work.lua
```

Loads the config, connects to Telegram, and answers the single `chat_id` from
`shell3.telegram{}`. Also starts the Mini App dashboard (when
`dashboard.enabled`), arms cron jobs and the heartbeat, and blocks until
interrupted. The runtime is anchored to the config directory, so history lives
under `~/.shell3/.shell3_project/`.

In-chat commands:

| Command | Effect |
|---------|--------|
| `/stop` | Cancel the in-flight turn and kill background jobs. |
| `/reload` | Re-read the config and apply it live. Refused while background tasks run. |
| `/run <job>` | Fire a cron job now. |
| `/set <name> <value>` | Tune a model parameter; bare `/set` lists them. |
| `/clear` | Reset the conversation. Refused while background tasks run (`/stop` first). |
| `/compact` | Force one context compaction; replies with the token delta. |
| `/rollback` | Undo the last turn. |
| `/voice [off\|inbound\|always]` | Voice-reply mode (needs `shell3.tts{}`); bare `/voice` shows a menu. Persists in `~/.shell3/voice_mode.json`. |

## `shell3 web` — standalone web front-end

```sh
shell3 web                        # addr + secret from shell3.web{}
shell3 web --addr 127.0.0.1:9000  # override the listen address
```

The dashboard plus a simple chat (send box, Stop, Allow/Deny cards), served
over plain HTTP and gated by `shell3.web{ secret = … }`. Open
`http://<addr>/?key=<secret>` once — the page stores the key for every API
call. It resumes the latest stored session (a conversation started over
Telegram continues in the browser) and keeps cron jobs running; the heartbeat
does not tick here. All slash commands above work in the send box, plus a
web-only `/help`; typing `/` pops a filtered command list, and command replies
render as ephemeral notices, not history. Run **one front-end at a time** —
`telegram` and `web` own the same runs store.

## `shell3 boot` — set up a config

```sh
shell3 boot     # interactive form: model endpoint + key, vision, bot token + chat id
```

An interactive form scaffolds `~/.shell3/shell3.lua` (the `code` agent, a
read-only `explorer` subagent, a `shell3.telegram{}` block with a cloudflared
dashboard tunnel), the `lib/` modules, and `~/.shell3/.env` (secrets — never
commit it). One step asks whether the model can see images: yes wires
`shell3.describe{}` to the main model (inbound Telegram images are captioned
out of the box) and enables the `read_media` tool; no leaves media tooling
off until you add a vision model.
Scriptable via flags (any flag skips its prompt; with no TTY, unset flags take
defaults): `--url`, `--model`, `--name`, `--key`, `--vision`, `--tg-token`,
`--tg-chat-id`, `--context-window`, `--compact-at`, `--proxy`, `--brave-key`,
`--force`. See [configuration.md](configuration.md).

## `shell3 health` — check the config

```sh
shell3 health                # ~/.shell3/shell3.lua
shell3 health --config work
```

Loads the config exactly like the bot would and fails (exit 1) on anything the
bot only warns about — e.g. a skill `.md` skipped for broken frontmatter. Run
it after editing `shell3.lua` or `lib/skills/`, before `/reload`.

## `shell3 dev` — drive the agent locally

Runs the bot's config + agent from your terminal and prints everything a chat
surface hides: reasoning, every tool call with raw args, untruncated results,
token usage. It follows subagent/`bash_bg` jobs the turn spawned and renders
their completions, and auto-approves `on_tool_call` asks (printing that it
did) so it runs unattended.

```sh
shell3 dev                        # no message: asks for one interactively
shell3 dev "list the files here and summarize this project"
shell3 dev -p "same, as a flag"   # -p/--prompt, for scripts and headless runs
shell3 dev --resume "now write a one-line description"   # continue the last session
shell3 dev --heartbeat   # fire the configured heartbeat once, print the suppression verdict
```

## `shell3 dash` — serve the dashboard locally

```sh
shell3 dash                       # http://127.0.0.1:8765, no auth
shell3 dash --addr 127.0.0.1:9000
```

Serves the Mini App dashboard with auth **bypassed**, so every endpoint is
browsable/curlable without Telegram. Reattaches to the latest session (the
Runs tab shows real history and subagent transcripts). Because auth is off it
binds to localhost only; the file explorer still redacts `.env`.

## Reading your history

Conversation history is plain JSONL under the config directory's
`.shell3_project/runs/`:

```sh
rg -n "JWT|expiry" ~/.shell3/.shell3_project/runs   # full-text search all sessions
ls -lt ~/.shell3/.shell3_project/runs/              # sessions, newest first
cat ~/.shell3/.shell3_project/runs/<id>/meta.json   # one session's metadata
```

The agent searches its own past the same way (`rg` over the JSONL, via the
`history` skill). The dashboard's Runs tab renders the same data; each
subagent run has its own stored transcript.

## Platform support

Unix-like systems only — Linux and macOS (WSL works). Windows is not
supported: shell3 leans on Unix process groups.
