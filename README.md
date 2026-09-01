<p align="center">
  <img src="docs/assets/shell3-banner.svg" alt="shell3 — your shell, in your pocket" width="100%">
</p>

A small personal agent you run on your own Unix box and reach through Telegram
or the terminal. One Go binary, one shell kit, any OpenAI-compatible model.

shell3 gives the model a gated shell, durable conversations, background jobs,
subagents, cron, MCP, and tools you declare as Bash functions.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/weatherjean/shell3/main/install.sh | sh
```

This installs the matching binary to `~/.local/bin`. You can also use
`go install github.com/weatherjean/shell3/cmd/shell3@latest`, build from source,
or download a binary from [Releases](https://github.com/weatherjean/shell3/releases).

Linux, macOS, and WSL are supported. Native Windows is not.

## Quickstart

1. Create a bot with [@BotFather](https://t.me/BotFather) and get your numeric
   user ID from [@userinfobot](https://t.me/userinfobot).
2. Run `shell3 boot` and complete the setup form.
3. Run `shell3 telegram`, then message your bot.

```sh
shell3 boot
shell3 telegram
```

The bot connects outbound to Telegram; shell3 opens no server or tunnel.
To keep it running, see [Deploying](docs/deploying.md).

## How it works

<p align="center">
  <img src="docs/assets/shell3-diagram.svg" alt="Messages enter a shell3 session; every tool call passes the gate; background jobs and subagents return through completion routing" width="100%">
</p>

The config is a directory centered on one definitions-only `shell3.sh` kit.
Agents, tools, policy, commands, events, and schedules are declaration blocks
bound to shell functions.

```sh
#---
# agent: bookmarks
# description: Organize saved links
# use: [bash, web]
#---
bookmarks_prompt() { cat <<'EOF'
Process one batch of saved links and update memory.md.
EOF
}

#---
# tool: page-kind
# description: Classify a saved page
# params:
#   url: {type: string, required: true}
#---
page_kind() { curl -sL "$url" | rg -o '<article|mw-content-text|add-to-cart'; }
```

See [Kits](docs/kits.md) and [Tools](docs/tools.md).

## Commands

| Command | Purpose |
|---|---|
| `shell3 telegram` | Run Telegram, cron, and completion delivery. |
| `shell3 ask` | Open the terminal chat. |
| `shell3 ask "…"` | Run one local turn; use `-p` for scripts. |
| `shell3 boot` | Create `~/.shell3`. |
| `shell3 health` | Validate the config. |
| `shell3 tool check\|run\|test <kit>` | Develop declared tools. |

`telegram`, `ask`, and `health` accept `--config <dir>`.

## Core behavior

- The model acts through `bash`, `bash_bg`, and `edit_file`.
- A `gate:` function runs before every tool call for named agents. Invalid or
  failed gate output blocks the call.
- Subagents and background commands are in-process jobs with bounded depth and
  concurrency.
- Every completion remains observable and survives restart through the outbox.
- Conversations, tool calls, usage, prompts, cron status, and job logs are
  stored locally.
- Long conversations prune old tool output and can compact automatically.
- Telegram keeps one conversation per chat. `/status`, `/stop`, `/superstop`,
  `/new`, `/run`, `/btw`, `/reload`, and `/quiet` are host commands.
- Attachments are saved for declared perception tools; shell3 does not choose
  image, speech, or generation providers.

## Security

The agent has a real shell. The shipped gate is a speed bump, not a security
boundary. Use a container or VM when you need isolation. Anyone who controls an
allowed Telegram account or the bot token can drive the agent.

Secrets stay in the config directory's `.env`. shell3 has no telemetry or
update checks. See [Security & data](docs/security.md).

## Documentation

- [Configuration](docs/configuration.md)
- [Kits](docs/kits.md)
- [CLI](docs/cli.md)
- [Tools](docs/tools.md)
- [Deploying](docs/deploying.md)
- [Cookbook](docs/cookbook/README.md)
- [Internals](docs/internals.md)

## Contributing and license

See [CONTRIBUTING.md](CONTRIBUTING.md). shell3 is [MIT licensed](LICENSE).
The edit tool includes code ported from
[opencode](https://github.com/sst/opencode); see
[internal/edittool/replace.go](internal/edittool/replace.go).
