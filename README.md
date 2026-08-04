<p align="center">
  <img src="docs/assets/shell3-banner.svg" alt="๑ï shell3 /ˈʃɛli/ — your shell, in your browser — minimal Unix-composable personal agent" width="100%">
</p>

A minimal, Unix-composable personal agent you run as a web app on your own
box. One binary, one config directory of YAML + markdown, any
OpenAI-compatible endpoint.

You message one agent. It handles small things itself and delegates the rest
to project managers and subagents. It runs `bash`, edits files, and schedules
work; the binary serves the browser interface you do all of it from.

```sh
shell3 boot        # interactive form: model + endpoint + key, vision, workdir
shell3 serve       # http://127.0.0.1:8765 — open it and start talking
```

## How it works

<p align="center">
  <img src="docs/assets/shell3-diagram.svg" alt="Diagram: you chat with shell3 in a browser; every tool call passes your hook gate before the agent acts through bash and edit on your shell; the agent delegates to project managers, subagents and cron jobs; every background completion is triaged by the notifier into a bell notification, a wake of the agent, or silence" width="100%">
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
your config (`~/.shell3/`) and history untouched; restart a running server to
pick it up. `shell3 --version` shows what you're on. Pin a release with
`curl -fsSL … | VERSION=vX.Y.Z sh`.

## Quickstart

1. Run `shell3 boot` and fill in the form. It writes the config tree under
   `~/.shell3/` and asks for the password the interface requires, optionally
   with an authenticator second factor.
2. Run `shell3 serve`, open <http://127.0.0.1:8765>, and start talking.
3. Want it on your phone? `tailscale serve --bg 8765 && shell3 serve` —
   a stable https URL on your [tailnet](https://tailscale.com) (free), and
   nothing on the public internet.

`shell3 serve` binds loopback. Keeping it running and reaching it from
elsewhere are yours to set up — [docs/deploying.md](docs/deploying.md) has the
few lines each takes (a service is one paste,
[cookbook/service.md](docs/cookbook/service.md)); the full walkthrough is in
[docs/cli.md](docs/cli.md).

## Commands

| Command | What |
|---------|------|
| `shell3 serve`    | Run the agent + web interface + cron (the service). |
| `shell3 boot`     | Scaffold the config + `.env` interactively. |
| `shell3 project new` | Scaffold a `projects/<name>/` config dir (brief + manager). |
| `shell3 health`   | Load the config strictly; fail on any warning. |
| `shell3 ask "…"`  | Ask the agent locally with full verbose output; no message = interactive loop; `-p` for scripting; `--resume` continues the last session. |

Every subcommand takes `--config/-c` to point at a different config directory.

## Features

- **A browser interface, served by the binary.** Chat with voice, live
  background jobs, cron, full session replays, status, and a read-only file
  explorer. Gated commands raise an Allow/Deny modal; web push reaches you
  with the tab closed.
- **Bash-first, gated by a script you own.** The agent acts through `bash`
  and `edit_file`; a per-agent hook script allows, rewrites, asks, or blocks
  every tool call. Fail-closed, armed out of the box.
- **Chain of command.** The agent delegates to project managers, subagents,
  `bash_bg` background jobs, and `cron/*.md` schedules; a notifier triages
  every completion, and failures always surface.
- **One config directory, four rules.** YAML wires it, markdown prompts it,
  files enable it, one bash script gates it. Versionable, diffable, reloadable
  live. Conversation history lands in one SQLite file the agent can search
  itself with the `history` tool.
- **Any OpenAI-compatible provider**: OpenAI, Ollama, Groq, LM Studio,
  OpenRouter, DeepSeek, and friends. MCP servers too, opt-in per agent and
  gated like every other tool.
- **Voice and images (optional)**: dictation, read-aloud, and an
  `image_generate` tool the interface renders inline
  ([recipes](docs/cookbook/voice-images.md)).

## Documentation

- **[Configuration](docs/configuration.md)**: the config directory — models,
  agent, subagents, projects, the web block, cron, voice & images, secrets,
  MCP, hooks, skills.
- **[CLI](docs/cli.md)**: every subcommand and the SQLite runs store.
- **[Deploying](docs/deploying.md)**: keeping serve running, and reaching it
  from elsewhere.
- **[Security & data](docs/security.md)**: threat model, secrets, wiping data.
- **[Cookbook](docs/cookbook/README.md)**: drop-in recipes — subagents,
  skills, sandboxes, MCP.

## Security

The model gets a full shell, limited only by the `hooks/tool-call.sh` gate,
which ships armed. `shell3 serve` requires a password (plus an optional
authenticator code), but whoever logs in gets that same shell — the gate
script, not the login, is what limits what can happen; use a container or VM
for hard isolation. shell3 phones home to nothing: its only outbound
connections are the endpoints in your config. No telemetry, no update checks.
Threat model in [docs/security.md](docs/security.md); report vulnerabilities
via [GitHub Security Advisories](https://github.com/weatherjean/shell3/security/advisories).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md): `make test` (race detector on),
`make lint`, feature branches, tests with every behavior change.

## License

[MIT](LICENSE) © 2026 WeatherJean.

Portions of `internal/edittool` are a Go port of
[opencode](https://github.com/sst/opencode)'s str-replace edit tool, used
under its license; see [internal/edittool/replace.go](internal/edittool/replace.go).
