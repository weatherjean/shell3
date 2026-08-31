<p align="center">
  <img src="docs/assets/shell3-banner.svg" alt="๑ï shell3 /ˈʃɛli/ — your shell, in your pocket — minimal Unix-composable personal agent" width="100%">
</p>

A minimal, Unix-composable personal agent you run on your own box and reach
over Telegram. One binary, one file of config, any OpenAI-compatible endpoint.

shell3 does two things: **bash it out, or spin up a subagent that drives
through the task with custom tools.** Everything else is configuration.

You message one agent. It works directly, or dispatches an employee — an agent
you defined with its own prompt, its own tools, and its own knowledge. All of
it lives in one file:

```sh
#---
# agent: bookmarks
# use: [bash, web]
#---
bm_prompt() { cat <<'EOF'
One tick = one batch of saved links. Judge each page yourself.
EOF
}

#---
# tool: page-kind
# description: Classify a saved link — article, wiki, shop, dead
# params:
#   url: {type: string, required: true}
#---
bm_page_kind() { curl -sL "$url" | rg -o '<article|mw-content-text|add-to-cart'; }
```

That tool is a real tool: the model sees its description, calls it with
structured arguments, and the gate sees it like any other. See
[docs/kits.md](docs/kits.md).

```sh
shell3 boot        # interactive form: model + endpoint + key, vision, bot token, workdir
shell3 telegram    # connects the bot and listens; message it
```

## How it works

<p align="center">
  <img src="docs/assets/shell3-diagram.svg" alt="Diagram: you message shell3 on Telegram; every tool call passes your hook gate before the agent acts through bash and edit on your shell; the agent delegates to project managers, subagents and cron jobs; every background completion arrives as mail — failures and raw-report results post to the chat, the rest wakes the agent, which messages you only when it matters" width="100%">
</p>

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/weatherjean/shell3/main/install.sh | sh
```

Installs the right prebuilt binary to `~/.local/bin` (make sure it's on your
`PATH`). Yes, the agent's own gate refuses `curl … | sh`; you're a human, so
[read it first](install.sh). Alternatives:
`go install github.com/weatherjean/shell3/cmd/shell3@latest`, `make build`
from a checkout, or the
[releases page](https://github.com/weatherjean/shell3/releases).

Unix-like systems only (Linux, macOS; WSL works). Windows is not supported:
shell3 leans on Unix process groups.

To update, run the install command again. It overwrites the binary and leaves
your config (`~/.shell3/`) and history untouched; restart a running bot to
pick it up. `shell3 --version` shows what you're on. Pin a release with
`curl -fsSL … | VERSION=vX.Y.Z sh`.

## Quickstart

1. Get a bot token from [@BotFather](https://t.me/BotFather) and your numeric
   chat id from [@userinfobot](https://t.me/userinfobot).
2. Run `shell3 boot` and fill in the form. It writes the config tree under
   `~/.shell3/`.
3. Run `shell3 telegram`. The bot greets the chat and listens.

Nothing listens: shell3 connects outbound to Telegram (no tunnel, server, or
login) and obeys only the Telegram user ids you list in `allow_from` —
in a group it answers only when @mentioned or replied to, and each chat keeps
its own conversation. Keeping it running is yours to set up —
[docs/deploying.md](docs/deploying.md) has the few lines it takes (a service
is one paste, [cookbook/service.md](docs/cookbook/service.md)); the full
walkthrough is in [docs/cli.md](docs/cli.md).

## Commands

| Command | What |
|---------|------|
| `shell3 telegram` | Run the bot front-end + cron (the service). |
| `shell3 boot`     | Scaffold the config + `.env` interactively. |
| `shell3 health`   | Load the config strictly; fail on any warning. |
| `shell3 ask "…"`  | Ask the agent locally with full verbose output; `-p` for scripting; `--agent <name>` runs one subagent turn and prints only its reply, for batch scripts. |
| `shell3 ask`      | No message: opens the full-screen terminal chat — the local alternative to Telegram, on its own conversation. `--resume` continues it. |
| `shell3 tool`     | `check`, `run`, and `test` the tools declared in a kit — the author's loop. Takes the kit path as an argument. |

`telegram`, `ask` and `health` take `--config/-c` to point at a
different config directory. `boot` always targets `~/.shell3`, and `tool`
takes the kit file itself as its argument.

## Features

- **Bash-first, gated by a script you own.** The agent acts through `bash`
  and `edit_file`; a per-agent hook script allows, rewrites, runner-swaps, or
  blocks every tool call. Fail-closed, armed out of the box.
- **Chain of command.** The agent delegates to employees you defined,
  `bash_bg` background jobs, and `cron:` schedules; completions arrive as
  mail — the agent hears about finished background work and messages you only
  when it matters, and failures always surface.
- **Telegram-first inspection.** `/status` sends a self-contained HTML snapshot
  of the live agent, rooms, jobs, cron, and inbox without a model turn. Ask for
  an old conversation, subagent run, cron run, or background log and the agent
  finds it in the durable store and sends the exact HTML record. `/superstop`
  is the everything-off switch; no HTTP server or tunnel exists.
- **One file.** `shell3.sh` holds the wiring, every agent, and their tools and
  skills. Prose is prose, code is code, structured data is YAML in a comment
  block. Versionable, diffable, reloadable live.
- **Total recall**: every conversation is stored in one SQLite file, and the
  agent's `history` tool full-text-searches all of it — reference something
  from months ago and it finds it (`runs_keep_days: 0` keeps it forever).
- **Automatic context compaction.** Long sessions summarize their own head
  and keep a verbatim tail, so a conversation you keep returning to doesn't
  run out of room.
- **Any OpenAI-compatible provider**: OpenAI, Ollama, Groq, LM Studio,
  OpenRouter, DeepSeek, and friends. MCP servers too, opt-in per agent and
  gated like every other tool.
- **Media as declared tools, not config**: no built-in transcription, TTS,
  image generation, or perception at all — an attachment's path lands in the
  prompt and the agent sends files back with `send_media_telegram`; voice and
  vision are tools you paste into your kit
  ([guide](internal/scaffold/defaults/base/skills/using-llms.md)).

## Documentation

- **[Configuration](docs/configuration.md)**: the config directory — models,
  agent, subagents, projects, telegram, cron, attachments & media, secrets,
  MCP, hooks, skills.
- **[CLI](docs/cli.md)**: every subcommand and the stored-history views.
- **[Deploying](docs/deploying.md)**: keeping the bot running as a service.
- **[Security & data](docs/security.md)**: threat model, secrets, wiping data.
- **[Cookbook](docs/cookbook/README.md)**: drop-in recipes — subagents,
  skills, sandboxes, MCP.
- **[Internals](docs/internals.md)**: implementation contracts, concurrency,
  durability, and the rationale behind non-obvious design choices.

## Security

The model gets a full shell, limited only by the kit's `gate:` function,
which ships armed. Whoever controls the chat, or the bot token, controls that
shell; use a container or VM for hard isolation. shell3 phones home to
nothing: its only outbound connections are Telegram and the endpoints in your
config. No telemetry, no update checks. Threat model in
[docs/security.md](docs/security.md); report vulnerabilities via
[GitHub Security Advisories](https://github.com/weatherjean/shell3/security/advisories).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md): `make test` (race detector on),
`make lint`, feature branches, tests with every behavior change.

## License

[MIT](LICENSE) © 2026 WeatherJean.

Portions of `internal/edittool` are a Go port of
[opencode](https://github.com/sst/opencode)'s str-replace edit tool, used
under its license; see [internal/edittool/replace.go](internal/edittool/replace.go).
