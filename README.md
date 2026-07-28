<p align="center">
  <img src="docs/assets/shell3-banner.svg" alt="๑ï shell3 /ˈʃɛli/ — your shell, in your browser — minimal Unix-composable personal agent" width="100%">
</p>

A minimal, Unix-composable personal agent you run as a **web app on your own
box**. One binary, one config directory of YAML + markdown, any
OpenAI-compatible endpoint.

**You're the director.** The one agent you message is your single point of
contact: it triages every request, handles the small things itself, and
delegates the real work to project managers and subagents. It runs `bash`,
edits files, schedules work, and serves a browser interface for talking to
it, watching its background work, and browsing its config. It pipes like a
Unix tool and is configured like software, not like a platform.

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
| `shell3 ask "…"`  | Ask the agent locally, full verbose output; no message = an interactive multi-turn loop; `-p` for headless scripting; `--resume` continues the last session. |

Every subcommand takes `--config/-c` to point at a different config directory.

## Features

- **A browser interface, served by the binary.** Six views: **Chat**
  (streaming markdown, tool calls, reasoning, voice in and out), **Jobs**
  (running and finished background work, live output, cancel), **Cron**
  (schedules, last runs, a Run now button), **Runs** (every stored session
  replayed in full), **Status** (system prompt, tools, model params, context
  usage, config warnings, MCP health, gate state) and **Files** (a read-only
  walk of the config and media directories, `.env` redacted). Gated commands
  raise an Allow/Deny modal; the notification bell carries background results
  across page reloads and can enable **web push**, so a finished job reaches
  you with the tab closed (localhost or an https tunnel — push needs a secure
  context). Each thread is its own session; one turn runs at a time.
- **Chain of command.** The agent you message is your single point of contact:
  it triages, does small things itself, and delegates the rest. Projects are
  config dirs (`projects/<name>/`, scaffolded by `shell3 project new`) — each
  has a brief and a dedicated manager subagent whose shell runs in the
  project's workdir.
- **Voice and images (optional).** `media.stt`/`media.tts` back the
  interface's dictation button and read-aloud control (without them the
  browser's own speech APIs stand in); `media.imagegen` adds an
  `image_generate` tool (`api: openai` or `openrouter`) whose output the agent
  shows inline. One free Groq key covers speech both ways — see
  [docs/cookbook/voice-images.md](docs/cookbook/voice-images.md).
- **Any OpenAI-compatible provider.** OpenAI, Ollama, Groq, LM Studio,
  OpenRouter, Moonshot, DeepSeek — reasoning-trace streaming where supported,
  and a `run_proxy` escape hatch for endpoints that need a local shim.
- **One config directory, four rules.** YAML wires it (`shell3.yaml`);
  markdown prompts it (`agent.md`, `notifier.md`, `agents/*.md`,
  `projects/<name>/`, `skills/*.md`, `cron/*.md`); files enable it (a feature
  is on because its file exists); one bash script gates it. Versionable,
  diffable, and the agent can edit its own prompts and skills and reload them
  live — the wiring, the gate scripts, and `.env` stay the operator's.
- **Bash-first, gated by a script you own.** The agent acts through `bash` and
  `edit_file` (plus `read_media` for images/audio/PDF/video on multimodal
  models); reading and searching are just commands (`cat`, `ls`, `rg`) —
  structured `read`/`list_files` tools exist as a per-agent opt-in, typically
  for a subagent on a smaller model. The gate is a bash script per agent,
  armed out of the box (`hooks/tool-call.sh`,
  `hooks/<subagent>.tool-call.sh`) — JSON in, verdict out (block / rewrite /
  runner-swap / ask a human in the browser), fail-closed on script errors; a
  sibling `tool-result.sh` can rewrite outputs (e.g. redact secrets), equally
  fail-closed.
- **MCP servers (tools only).** The `mcp:` block connects stdio or
  streamable HTTP servers on the official Go SDK; agents opt in per server
  (`mcp: [github]` — or `mcp: all` — in their frontmatter), tools surface as
  `mcp_<server>_<tool>`, and calls pass through the same hook gate.
  `shell3 health` and the Status view report each server's state. No OAuth —
  remote auth is a bearer header from `.env`.
- **Subagents & scheduling.** Drop a file in `agents/` and the `task` tool
  appears — delegation is fire-and-forget; background commands run with
  `bash_bg`; recurring prompts are `cron/*.md` files. Every completion is
  triaged by the **notifier** (`notifier.md`): worth telling you → the bell;
  needs action → the agent is woken with it; routine → silence. `direct:
  true` skips the triage; failures always surface.
- **Context managed for you.** A `compact_at` threshold auto-compacts the
  conversation into a summary — and says so in the bell, so amnesia is never a
  mystery; history persists as plain JSONL under `.shell3_project/runs/`,
  searchable with `rg` or readable in the Runs view.

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
