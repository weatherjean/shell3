<p align="center">
  <img src="docs/assets/shell3-banner.svg" alt="๑ï shell3 /ˈʃɛli/ — your shell, in your browser — minimal Unix-composable personal agent" width="100%">
</p>

A minimal, Unix-composable personal agent you run as a **web app on your own
box**. One binary, one config directory of YAML + markdown, any
OpenAI-compatible endpoint.

The one agent you message is your single point of contact: it triages every
request, handles small things itself, and delegates the rest to project
managers and subagents. It runs `bash`, edits files, schedules work, and
serves a browser interface for chat, background jobs, and config.

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
`PATH`). Alternatives: `go install github.com/weatherjean/shell3/cmd/shell3@latest`,
`make build` from a checkout, or the
[releases page](https://github.com/weatherjean/shell3/releases).

Unix-like systems only (Linux, macOS — WSL works). Windows is not supported:
shell3 leans on Unix process groups.

## Update

Run the install command again — it overwrites the binary; your config
(`~/.shell3/`) and history are untouched. A running server keeps executing
the old binary until restarted (`systemctl --user restart shell3.service`
when running as a service, otherwise restart `shell3 serve`).
`shell3 --version` confirms what you're on; pin a release with
`curl -fsSL … | VERSION=vX.Y.Z sh`. If you installed via `go install`,
update the same way.

## Quickstart

1. `shell3 boot` — fill in the form. It writes the config tree under
   `~/.shell3/` (`shell3.yaml`, `agent.md`, `notifier.md`, `memory.md`,
   `agents/`, `skills/`, `cron/`, `hooks/`, `.env`) and asks for the password
   the interface requires, optionally with an authenticator second factor.
2. `shell3 serve` — open <http://127.0.0.1:8765> and start talking.

For access from elsewhere, `web.tunnel` (or `shell3 serve --tunnel`, a
cloudflared quick tunnel) starts a tunnel and prints the public URL. What is
behind that login is a shell on the machine, so an authenticated proxy or
private network in front is still worth having. Full walkthrough in
[docs/cli.md](docs/cli.md).

## Commands

| Command | What |
|---------|------|
| `shell3 serve`    | Run the agent + web interface + cron (the service). |
| `shell3 boot`     | Scaffold the config + `.env` interactively. |
| `shell3 project new` | Scaffold a `projects/<name>/` config dir (brief + manager). |
| `shell3 health`   | Load the config strictly; fail on any warning. |
| `shell3 url`      | Print where the interface is reachable (tunnel URL when one runs). |
| `shell3 ask "…"`  | Ask the agent locally, full verbose output; no message = an interactive multi-turn loop; `-p` for headless scripting; `--resume` continues the last session. |

Every subcommand takes `--config/-c` to point at a different config directory.

## Features

- **A browser interface, served by the binary** — chat (with voice), live
  background jobs, cron, full session replays, status, and a read-only file
  explorer. Gated commands raise an Allow/Deny modal; web push reaches you
  with the tab closed.
- **Bash-first, gated by a script you own** — the agent acts through `bash`
  and `edit_file`; a per-agent hook script allows, rewrites, asks, or blocks
  every tool call. Fail-closed, and armed out of the box.
- **Chain of command** — the agent triages, does small things itself, and
  delegates the rest to project managers (`shell3 project new`) and
  subagents.
- **One config directory, four rules** — YAML wires it, markdown prompts it,
  files enable it, one bash script gates it. Versionable, diffable,
  reloadable live; the gate scripts and `.env` stay the operator's.
- **Subagents & scheduling** — fire-and-forget `task` delegation, `bash_bg`
  background commands, `cron/*.md` schedules. A notifier triages every
  completion: bell, wake the agent, or silence; failures always surface.
- **Any OpenAI-compatible provider** — OpenAI, Ollama, Groq, LM Studio,
  OpenRouter, Moonshot, DeepSeek, …
- **MCP servers** — stdio or streamable HTTP, opt-in per agent, gated like
  every other tool.
- **Voice and images (optional)** — dictation, read-aloud, and an
  `image_generate` tool; one free Groq key covers speech both ways
  ([docs/cookbook/voice-images.md](docs/cookbook/voice-images.md)).
- **Context managed for you** — auto-compaction past a threshold (announced
  in the bell); history is plain JSONL you can `rg`.

## Documentation

- **[Configuration](docs/configuration.md)** — the config directory: models,
  agent, subagents, projects, the web block, cron, voice & images,
  scripts & secrets, MCP servers, hook scripts, skills.
- **[CLI](docs/cli.md)** — every subcommand and the JSONL runs store.
- **[Security & data](docs/security.md)** — threat model, secrets, wiping data.
- **[Cookbook](docs/cookbook/README.md)** — drop-in recipes: subagents,
  skills, proxies, sandboxes.

## Security

shell3 gives the model a full, unrestricted shell, limited only by the
`hooks/tool-call.sh` gate — which a scaffolded config **ships armed**:
credentials, system paths, unread remote code (`curl … | sh`), publishing,
force-pushes, and anything that would kill shell3 itself are refused, while
ordinary work runs untouched. Read it and tune it to your deployment.
`shell3 serve` requires a password (plus an optional authenticator code), but
whoever logs in gets that same shell — the gate script, not the login, is
what limits what can happen. Keep an authenticated proxy or private network
in front when exposing it, use a container, VM, or throwaway user for hard
isolation, and read [docs/security.md](docs/security.md) first. Report
vulnerabilities via
[GitHub Security Advisories](https://github.com/weatherjean/shell3/security/advisories).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md): `make test` (race detector on),
`make lint`, feature branches, tests with every behavior change.

## License

[MIT](LICENSE) © 2026 WeatherJean.

Portions of `internal/edittool` are a Go port of
[opencode](https://github.com/sst/opencode)'s str-replace edit tool, used
under its license; see [internal/edittool/replace.go](internal/edittool/replace.go).
