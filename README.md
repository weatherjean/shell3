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
- **One Lua config.** Models, agents, system prompts, tools, and skills all
  live in `shell3.lua` — versionable, diffable, and programmable.
  `shell3 boot` scaffolds a working setup in under a minute.
- **Multiple agents, one conversation.** Declare e.g. a `code` agent and a
  read-only `plan` agent; switch with Tab or `/agent` mid-session while
  keeping history.
- **Custom tools in Lua.** A tool is a name, a JSON schema, and a Lua
  function — no plugins, no separate processes.
- **Bash-first, unsafe by default.** The agent acts through `bash` and
  `edit_file`; everything else is a file it reads or a command it runs. The
  shell is unrestricted by default — `shell3.wrap_bash(fn)` is the single hook
  to inspect, rewrite, or block commands. `shell3.stub_tools{}` redirects
  hallucinated tool names (`read_file`, `grep`, …) back to bash.
- **Context managed for you.** Set `compact_at` (a prompt-token threshold) on a
  model and shell3 auto-compacts the conversation into a structured summary when
  it crosses the line — no model-driven prune/compact tools. History persists in
  SQLite (WAL) and is readable read-only from inside the session via the
  `history` bash skill.
- **Headless & auditable.** Pipe in, pipe out; `--out` streams a lossless
  JSONL log of every token, tool call, and result for downstream tooling.
- **Embeddable, and a runtime.** Everything the TUI does is available as a Go
  library via [`pkg/shell3`](pkg/shell3) — one-shot `Run`, a persistent
  `Session`, or a `Runtime` hosting many named sessions for an always-on bot
  (steering, a wake bus, inbound media, and self-reporting subagents).

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
agent), `~/.shell3/lib/` (tools and skills as small Lua modules), and
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
    bash = true, edit = true, bash_bg = true, media = true,
    custom = { my_tool },          -- Lua-defined tools
  },
})

-- The shell is unrestricted by default. wrap_bash is the single hook to
-- inspect, rewrite, or block every bash/bash_bg command:
shell3.wrap_bash(function(cmd)
  if cmd:match("%.env") then return nil, "refusing to touch .env" end
  return cmd                       -- allow (optionally rewritten)
end)
```

A custom tool is a bash command template. Declared parameters are exported into
the command's environment by their (lowercase) name; declared `secrets` are
exported too (and kept out of the command string). The command's stdout is
returned to the model:

```lua
local weather = shell3.tool({
  name        = "weather",
  description = "Current weather for a city",
  parameters  = {
    type       = "object",
    properties = { city = { type = "string" } },
    required   = { "city" },
  },
  command = [[ curl -sf "https://wttr.in/$city?format=3" ]],
})
```

Config resolution order: `--config/-c` flag → `./shell3.lua` →
`~/.shell3/shell3.lua`. Secrets live in a `.env` beside the config, read via
`shell3.env.secret("KEY")` — plain text, so treat it like any credentials
file. Drop-in recipes (extra agents, planning skills, the browser skill, proxy setups) live
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
history introspection, pruning, and parameter control.

For an always-on personal agent, `NewRuntime` owns one shared build (config,
store, log) and hosts many named sessions — one per chat, each with its own
agent, workdir, and audit log:

```go
rt, _ := shell3.NewRuntime(shell3.RuntimeSpec{WorkDir: home})
defer rt.Close()
chat, _ := rt.Session(shell3.SessionOpts{Name: "tg:1234", Headless: true})
```

A long-lived host runs one select loop over `rt.Events()`: a session whose inbox
gains an item while idle emits a `Wake`, and the host answers with
`Session.RunQueued`. `Session.Interject` steers a running turn (or queues for the
next) from any goroutine and never blocks; `Send`/`SendParts` are the strict
single-turn path. Inbound images and audio ride along as `Part` attachments
(from disk or in-memory bytes). Subagents are a convention, not a subsystem:
declare specialists with `shell3.subagent{name, description, …}` and list them
per-agent via `tools = { subagents = { … } }`; the host injects a "## Delegation"
fragment with the exact `bash_bg` command to spawn one. A subagent is a
backgrounded `shell3` subprocess that runs the chosen agent on a self-contained
task and self-reports completion up its parent pointer — over a per-session
Unix-domain socket if the parent is live, or a SQLite inbox + revive if the
parent has gone dormant. The host turns that into a short pointer notification
(with a transcript path the parent `cat`s on demand) and wakes the next turn.
See the
[package docs](https://pkg.go.dev/github.com/weatherjean/shell3/pkg/shell3).

The TUI rides the same machinery: type while the agent is working and press
Enter to steer mid-turn (an `Interject`), and see a finished subagent surface as
a dim notice that auto-wakes the next turn.

## Removing a project's shell3 data

Project-local state (the project UUID, subagent transcripts, proxy logs) lives
in the project's `.shell3/` directory:

```sh
rm -rf .shell3                    # project-local state
```

Conversation history, sessions, and background jobs are not per-project files —
they live in the single shared database at `~/.shell3/data/shell3.db`, tagged
with the project UUID (`cat .shell3/.ref`). Removing the directory above leaves
those rows in place; delete the whole `~/.shell3/data/shell3.db` to wipe all
history across every project.

## Security

shell3 runs model-chosen shell commands and is **unsafe by default** (full,
unrestricted shell). The only safety surface is the `shell3.wrap_bash(fn)` hook
(allow/block/rewrite, no approval flow); secrets live in a plain `.env` beside
`shell3.lua`, and declared custom-tool `secrets` are exported into the command's
process environment (readable via `/proc/<pid>/environ` by same-user processes —
treat multi-user hosts accordingly). Run shell3 in a sandbox, container, or
throwaway user if you need hard isolation. Vulnerabilities: please use GitHub
Security Advisories.

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
