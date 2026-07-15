# CLI reference

shell3 is a hosted agent you run as a Telegram bot. The binary has five
subcommands: `telegram` (the service), `boot` (setup), `health` (config
check), and two local front-ends, `dev` and `dash`, for driving and inspecting
the agent from your terminal. The bare `shell3` command prints help.

## `shell3 telegram` — run the bot

```sh
shell3 telegram              # uses ~/.shell3/shell3.lua
shell3 telegram -c work      # uses ~/.shell3/work.lua
```

Loads the config, connects to Telegram, and answers the single `chat_id`
declared in `shell3.telegram{}`. It also starts the Mini App dashboard (when
`dashboard.enabled`), arms any cron jobs, and blocks until interrupted.

The bot's runtime is anchored to the config directory, so its history and runs
live under `~/.shell3/.shell3_project/`. In-chat commands: `/stop` (cancel the
in-flight turn + tracked jobs), `/reload` (re-read the config and apply it
live — refused while background tasks are running), `/run <job>` (fire a cron
job on demand), `/status`, `/clear`,
`/rollback`.

| Flag | Effect |
|------|--------|
| `-c`, `--config <name\|path>` | Config name (→ `~/.shell3/<name>.lua`) or a path to a `*.lua` file (default `~/.shell3/shell3.lua`) |

## `shell3 boot` — set up a config

```sh
shell3 boot     # interactive: model endpoint + key, then bot token + chat id
```

`boot` scaffolds `~/.shell3/shell3.lua` (the `code` agent, a read-only
`explorer` subagent, and a `shell3.telegram{}` block whose dashboard is
tunneled with cloudflared by default — free, no account; install it from
https://github.com/cloudflare/cloudflared or the dashboard stays local-only),
the `lib/` modules, and `~/.shell3/.env` (secrets — never commit it).
Non-interactive flags let you script it: `--url`, `--model`, `--name`,
`--key`, `--tg-token`, `--tg-chat-id`, `--context-window`, `--compact-at`,
`--proxy`, `--brave-key`, `--force`. See
[configuration.md](configuration.md) for what it produces and how to extend it.

## `shell3 health` — check the config

```sh
shell3 health                # checks ~/.shell3/shell3.lua
shell3 health --config work  # checks ~/.shell3/work.lua
```

Loads the config exactly like the bot would and fails (exit 1) on anything the
bot only tolerates with a warning — e.g. a skill `.md` skipped for broken
frontmatter. Run it after editing `shell3.lua` or `lib/skills/`, before `/reload`.

## `shell3 dev` — drive the agent locally

`dev` runs the bot's config + agent from your terminal and prints **everything**
a chat surface hides: reasoning, the streamed reply, every tool call with its
raw arguments, untruncated tool results, per-roundtrip and total token usage. It
also follows any subagent/`bash_bg` jobs the turn spawns and renders their
completion, so async delegation is fully visible. It exists to drive and polish
the agent without going through Telegram; it's also handy for quick local
queries and troubleshooting.

```sh
shell3 dev "list the files here and summarize what this project is"
shell3 dev --resume "now write a one-line description"   # continue the last session
```

| Flag | Effect |
|------|--------|
| `-c`, `--config <name\|path>` | Config to use (default `~/.shell3/shell3.lua`) |
| `--resume` | Continue the latest session (multi-turn across invocations) |

`dev` auto-approves `on_tool_call` ask verdicts (and prints that it did), so it
runs unattended.

## `shell3 dash` — serve the dashboard locally

```sh
shell3 dash                       # http://127.0.0.1:8765, no auth
shell3 dash --addr 127.0.0.1:9000
```

Serves the Mini App dashboard against the live config's runtime with initData
verification **bypassed**, so every endpoint is browsable/curlable without
Telegram. It reattaches to the latest session, so the Runs tab shows the bot's
real history and subagent transcripts. Because auth is off it exposes history
and files, so it binds to localhost only; the file explorer still redacts `.env`
and `ai-do-not-read.*`.

## Reading your history

Conversation history lives as plain JSONL under `.shell3_project/runs/` in the
config directory (`~/.shell3/.shell3_project/runs/` for the bot). Query it with
standard Unix tools:

```sh
rg -n "JWT|expiry" ~/.shell3/.shell3_project/runs   # full-text search all sessions
ls -lt ~/.shell3/.shell3_project/runs/              # sessions, newest first
cat ~/.shell3/.shell3_project/runs/<id>/meta.json   # a session's metadata
```

The agent searches its own past conversations the same way (via the `history`
skill), using `bash` with `rg` — no special tool needed. The dashboard's Runs
tab renders the same data (each subagent run has its own stored transcript).

## Platform support

shell3 targets Unix-like systems — Linux and macOS. Windows is **not** supported:
it leans on Unix process groups. WSL works.
