# ๑ï shell3 /'ʃɛli/

A minimal, Unix-composable coding agent you run as a personal **Telegram bot**.
One binary, one Lua config file, any OpenAI-compatible endpoint.

shell3 is an always-on agent you talk to from Telegram: it runs `bash`, edits
files, schedules work, and spawns subagents on a host you control, and it ships
with a Mini App dashboard for its runs, jobs, and files. It pipes like a Unix
tool and is configured like software, not like a platform.

```sh
shell3 boot        # asks: model + endpoint + key, and your Telegram bot token + chat id
shell3 telegram    # start the bot — now message it from Telegram
```

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/weatherjean/shell3/main/install.sh | sh
```

This downloads the right prebuilt binary for your OS and architecture and
installs it to `~/.local/bin`. Make sure that directory is on your `PATH`.

Other ways to install:

```sh
go install github.com/weatherjean/shell3/cmd/shell3@latest   # with a Go toolchain
make build                                                   # from a checkout
```

Prebuilt binaries also live on the
[releases page](https://github.com/weatherjean/shell3/releases).

shell3 targets Unix-like systems (Linux, macOS). Windows is not supported — it
leans on Unix process groups. WSL works.

## Quickstart

1. Create a bot with [@BotFather](https://t.me/BotFather) and note its token.
   Get your numeric chat id (e.g. from [@userinfobot](https://t.me/userinfobot)).
2. `shell3 boot` — answer the prompts (model endpoint + key, then the bot token
   and chat id). It writes `~/.shell3/shell3.lua`, `~/.shell3/lib/`, and
   `~/.shell3/.env`.
3. `shell3 telegram` — the bot connects and answers your chat. Message it.

`boot` scaffolds the `code` agent, a read-only `explorer` subagent, and a
`shell3.telegram{}` block whose Mini App dashboard is tunneled with
[cloudflared](https://github.com/cloudflare/cloudflared) by default (must be
installed, or the dashboard stays local-only). Full walkthrough in
[docs/cli.md](docs/cli.md).

## Commands

| Command | What |
|---------|------|
| `shell3 telegram` | Run the bot + Mini App dashboard (the service). |
| `shell3 web`      | Run the standalone web front-end (dashboard + chat, token auth) — the Telegram-free fallback. |
| `shell3 boot`     | Scaffold the config + `.env` interactively. |
| `shell3 dev "…"`  | Drive the bot's agent locally with full verbose output (dev / quick queries). `--resume` continues the last session. |
| `shell3 dash`     | Serve the dashboard locally with no auth (dev / troubleshooting), bound to localhost. |

## Features

- **Talk to it from Telegram.** One authorized chat; inline Allow/Deny buttons
  for gated commands; media in and out; `/stop`, `/reload`, `/run`. A Mini App
  dashboard shows status, past runs, background jobs, cron, and a read-only file
  explorer (with `.env` redacted).
- **Voice and images (optional).** Declare `shell3.stt{}`/`shell3.tts{}` and
  voice notes are transcribed in and spoken back out (`/voice` picks the
  mode); `shell3.describe{}` captions images for text-only models;
  `shell3.imagegen{}` adds an `image_generate` tool (`api = "openai"` or
  `"openrouter"`). One free Groq key covers speech in and out. See
  [configuration.md](docs/configuration.md#voice--images--shell3stt--shell3tts--shell3describe--shell3imagegen).
- **Any OpenAI-compatible provider.** OpenAI, Ollama, Groq, LM Studio,
  OpenRouter, Moonshot, DeepSeek — with reasoning-trace streaming where vendors
  support it, and a `run_proxy` escape hatch for endpoints that need a local shim.
- **One Lua config.** The agent, its model, system prompt, tools, skills,
  subagents, cron jobs, and the Telegram block all live in `shell3.lua` —
  versionable, diffable, programmable. Edit it and apply live with the `reload`
  tool.
- **Bash-first, unsafe by default.** The agent acts through `bash` and
  `edit_file` — plus `read_media` for images, audio, PDFs, and video on
  multimodal models;
  reading, listing, and searching are just commands it runs (`cat`, `ls`,
  `rg`). The single opt-in hook is `shell3.on_tool_call(fn)` — chainable,
  verdict-based (block / rewrite / runner-swap / ask a human over Telegram);
  denylists use `shell3.regex`.
- **Subagents & scheduling.** Delegate work to declared subagents with the
  `task` tool (fire-and-forget in-process jobs; you're notified on completion),
  background shell commands with `bash_bg`, and run recurring prompts on a cron
  schedule.
- **Heartbeat.** `shell3.heartbeat{}` periodically hands the main session a
  checklist while it's idle; the agent replies `HEARTBEAT_OK` when nothing
  needs attention and the chat stays silent — you only hear real alerts.
- **Context managed for you.** Set a `compact_at` token threshold and shell3
  auto-compacts the conversation into a summary — no model-driven prune/compact
  tools. History persists as plain JSONL under `.shell3_project/runs/` and is
  searchable with `rg`.

## Documentation

- **[Configuration](docs/configuration.md)** — models, the agent, subagents,
  the `shell3.telegram{}` block (dashboard + tunnel), `shell3.web{}`,
  `shell3.cron`, `shell3.heartbeat`, voice & images (`shell3.stt`/`tts`/
  `describe`/`imagegen`), custom tools, `on_tool_call`, `on_tool_result`,
  skills, proxies.
- **[CLI](docs/cli.md)** — `telegram`, `web`, `boot`, `health`, `dev`, `dash`,
  and the JSONL runs store.
- **[Security & data](docs/security.md)** — the threat model, secrets, and
  removing shell3's data.
- **[Cookbook](docs/cookbook/README.md)** — drop-in recipes: extra subagents,
  planning skills, proxy and sandbox setups.

## Security

shell3 runs model-chosen shell commands and is **unsafe by default** — a full,
unrestricted shell with no approval prompt until you opt in. The single hook is
`shell3.on_tool_call(fn)`: chainable, verdict-based (block / rewrite /
runner-swap / ask a human via inline Allow/Deny buttons in Telegram). Denylists
use `shell3.regex` (Go RE2, compiled at load). The bot answers exactly one
authorized chat id. Run it in a sandbox, container, or throwaway user if you
need hard isolation, and read [docs/security.md](docs/security.md) before
pointing it at anything you care about. Report vulnerabilities via
[GitHub Security Advisories](https://github.com/weatherjean/shell3/security/advisories).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Short version: `make test` (race detector
on), `make lint`, feature branches, and tests with every behavior change.

## License

[MIT](LICENSE) © 2026 WeatherJean.

Portions of `internal/edittool` are a Go port of the str-replace edit tool from
[opencode](https://github.com/sst/opencode), used under its license; see the
package doc comment in
[internal/edittool/replace.go](internal/edittool/replace.go) for details.
