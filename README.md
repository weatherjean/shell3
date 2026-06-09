# ๑ï shell3 /'ʃɛli/

A minimal, Unix-composable coding agent. One binary, one Lua config file, any
OpenAI-compatible endpoint.

shell3 puts a language model in your terminal with `bash`, file editing, and
whatever tools you define — then stays out of the way. It pipes like a Unix
tool, embeds like a Go library, and is configured like software, not like a
platform.

```sh
shell3                                  # interactive session (TUI)
shell3 "explain the failing test"       # one-shot, prints and exits
git diff | shell3 "write a commit msg"  # reads stdin like any filter
shell3 "audit deps" --out audit.jsonl   # headless, with a JSONL audit log
```

## Features

- **Any OpenAI-compatible provider** — OpenAI, Ollama, Groq, LM Studio,
  OpenRouter, Moonshot, DeepSeek… Reasoning-trace streaming included where
  vendors support it. Endpoints that need a local shim can declare
  `run_proxy`, and shell3 starts the proxy on first use.
- **One Lua config.** Models, agents, system prompts, tools, skills, and
  guards all live in `shell3.lua` — versionable, diffable, and programmable.
  `shell3 boot` scaffolds a working setup in under a minute.
- **Multiple agents, one conversation.** Declare e.g. a `code` agent and a
  read-only `plan` agent; switch with Tab or `/agent` mid-session while
  keeping history.
- **Custom tools in Lua.** A tool is a name, a JSON schema, and a Lua
  function — no plugins, no separate processes. MCP servers (stdio) are
  also supported for tools that live elsewhere.
- **Guards.** `on_tool_call` middleware sees every tool call before it runs
  and can allow, block, or cancel the turn. The scaffold ships with a guard
  that blocks edits to your `.env`.
- **Context hygiene built in.** Prune any tool result by id, compact the
  conversation into a structured summary, and get context-usage reminders as
  the window fills. History persists in SQLite and is searchable from inside
  the session.
- **Headless & auditable.** Pipe in, pipe out; `--out` streams a lossless
  JSONL log of every token, tool call, and result for downstream tooling.
- **Embeddable.** Everything the TUI does is available as a Go library via
  [`pkg/shell3`](pkg/shell3) — one-shot `Run` or a persistent multi-turn
  `Session` streaming typed events.

## Install

Prebuilt binaries for Linux and macOS are on the
[releases page](https://github.com/weatherjean/shell3/releases), or:

```sh
go install github.com/weatherjean/shell3/cmd/shell3@latest
```

From a checkout: `make build` (stamps the version from git).

shell3 targets Unix-like systems (Linux, macOS). Windows is not supported —
it leans on Unix process groups and TTY semantics.

## Quickstart

```sh
shell3 boot     # asks: endpoint URL, model, name, API key — writes the config
shell3          # start a session
```

`boot` creates `~/.shell3/shell3.lua` (the config, with a `code` and a `plan`
agent), `~/.shell3/lib/` (tools, guards, and skills as small Lua modules), and
`~/.shell3/.env` (your secrets — never commit this file).

Inside a session: type to chat, Tab to switch agents, `/help` for the slash
commands (`/clear`, `/rollback`, `/prune <id>`, `/agent`, `/parameters`, …).

## Configuration

The config is plain Lua. A model + agent in full:

```lua
shell3.model("main", {
  base_url       = "https://api.openai.com/v1",
  api_key        = shell3.env.secret("MAIN_API_KEY"),  -- from .env
  model          = "gpt-5.2",
  context_window = 128000,
})

shell3.agent({
  name   = "code",
  model  = "main",
  prompt = [[You are a careful pair-programmer…]],
  tools  = {
    bash = true, edit = true, bash_bg = true,
    history = true, prune = true, compact = true, media = true,
    custom = { my_tool },          -- Lua-defined tools
    -- mcp = { mcp.chrome },       -- MCP servers
  },
  on_tool_call = { guards.no_env_edit },
})
```

A custom tool is just a function:

```lua
local weather = shell3.tool({
  name        = "weather",
  description = "Current weather for a city",
  parameters  = {
    type       = "object",
    properties = { city = { type = "string" } },
    required   = { "city" },
  },
  handler = function(args)
    local res, err = shell3.http.get("https://wttr.in/" .. shell3.urlencode(args.city) .. "?format=3")
    if err then return "error: " .. tostring(err) end
    return res.body
  end,
})
```

Config resolution order: `--config/-c` flag → `./shell3.lua` →
`~/.shell3/shell3.lua`. Secrets live in a `.env` beside the config, read via
`shell3.env.secret("KEY")` — plain text, so treat it like any credentials
file. Drop-in recipes (MCP, extra agents, planning skills, proxy setups) live
in [docs/cookbook](docs/cookbook).

If a model needs a local proxy in front of its endpoint (e.g. a Codex
subscription fronted by `npx openai-oauth`), set `run_proxy` on the model and
shell3 spawns it (detached) the first time an agent uses that model; logs go
to `./.shell3/proxy-<model>.log`.

## Headless / scripting

Pass a message as an argument or on stdin and shell3 runs one turn and exits.
`--out` adds a structured JSONL audit log — every assistant token, tool call
with raw arguments, tool result, usage count, and the terminal status:

```sh
shell3 "summarize the diff" --out run.jsonl
```

Headless mode strips the interactive-shell tool from the model's schema and
tells it no human is present.

## Library

The same engine embeds in Go programs:

```go
events, err := shell3.Run(ctx, shell3.Spec{Prompt: "what does this repo do?"})
if err != nil { log.Fatal(err) }
for ev := range events {
    if ev.Kind == shell3.Token { fmt.Print(ev.Text) }
}
```

`Start` gives a persistent multi-turn `Session` with agent switching,
history introspection, pruning, and parameter control. See the
[package docs](https://pkg.go.dev/github.com/weatherjean/shell3/pkg/shell3).

## Removing a project's shell3 data

```sh
cat .shell3/.ref                  # the project UUID
rm -rf ~/.shell3/projects/<uuid>  # project state in the global dir
rm -rf .shell3                    # project-local state
```

## Security

shell3 runs model-chosen shell commands — read
[SECURITY.md](SECURITY.md) for the threat model (guards, secret isolation,
process containment, audit logs) before pointing it at anything you care
about. Vulnerabilities: please use GitHub Security Advisories.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Short version: `make test` (race
detector on), `make lint`, feature branches, and tests with every behavior
change.

## License

[MIT](LICENSE) © 2026 WeatherJean.

Portions of `internal/edittool` are a Go port of the str-replace edit tool
from [opencode](https://github.com/sst/opencode), used under its license; see
the package doc comment in
[internal/edittool/replace.go](internal/edittool/replace.go) for details.
