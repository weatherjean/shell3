# shell3 documentation

shell3 is a minimal, Unix-composable coding agent. It runs an LLM-powered chat
session in your terminal against any **OpenAI-compatible** provider, configured
entirely from a single Lua file (`shell3.lua`).

This document describes the current Lua-config system. It is what `shell3 docs`
prints and what the `shell3_docs` tool returns.

---

## Configuration

shell3 is configured by one Lua file, `shell3.lua`. There are no YAML files, no
`auth`/`secrets` subcommands, and no `--persona`/`--provider`/`--model` flags.

### Discovery order

When you run `shell3`, the config file is resolved in this order:

1. `--config` / `-c <path>` — explicit path.
2. `./shell3.lua` — in the current working directory.
3. `~/.shell3/shell3.lua` — the global config.

If none is found, shell3 exits with:

```
no shell3.lua found — pass --config or create ~/.shell3/shell3.lua
```

The **directory containing the resolved `shell3.lua`** is the config working
directory: it is where the `.env` file is read from and where relative paths
inside the config resolve. (The agent's own `bash` tool still runs in your
current shell directory — these two are intentionally distinct.)

### Secrets via `.env`

Secrets live in a `.env` file **next to the config file**, never in the Lua
source. Read them with `shell3.env.secret("KEY")`:

```lua
api_key = shell3.env.secret("OPENCODE_KEY")
```

`.env` is parsed line by line as `KEY=value` (surrounding double quotes are
stripped; `#` lines and blanks are ignored).

> **Note:** `shell3.env.secret("KEY")` raises a config error if `KEY` is not
> present in `.env`. Define every key you reference (an empty value is fine —
> e.g. `OPENCODE_KEY=`), then check for emptiness in your handler if needed.

On first run shell3 scaffolds `~/.shell3/shell3.lua` and `~/.shell3/.env.example`.
Copy the example to `.env`, fill in the keys, and edit `shell3.lua` to taste.

### Canonical example

The reference config lives in the repo at
`internal/scaffold/defaults/shell3.lua`. It is the gold standard and is covered
by a golden-parse test; the same content is what first-run scaffolds into
`~/.shell3/shell3.lua`. The matching secrets template is
`internal/scaffold/defaults/env.example`.

---

## The four constructors

Everything in `shell3.lua` is built from four constructors on the global
`shell3` table. Each one validates its keys strictly: **unknown keys are an
error**.

### `shell3.model(name, opts)`

Declares a model. `base_url`, `api_key`, and `model` are required.

```lua
shell3.model("main", {
  base_url       = "https://api.openai.com/v1",
  api_key        = shell3.env.secret("OPENCODE_KEY"),
  model          = "o4-mini",
  context_window = 128000,
  reasoning      = "medium",
  max_tokens     = 16000,
  temperature    = 0.2,
  extra = {
    reasoning_summary   = "auto",
    verbosity           = "medium",
    parallel_tool_calls = true,
  },
})
```

| key              | type    | notes                                                          |
| ---------------- | ------- | -------------------------------------------------------------- |
| `base_url`       | string  | **required** — OpenAI-compatible endpoint base URL            |
| `api_key`        | string  | **required** — usually `shell3.env.secret(...)`               |
| `model`          | string  | **required** — the provider's model ID                        |
| `context_window` | integer | context size, used for the TUI usage meter                    |
| `reasoning`      | string  | `none`/`minimal`/`low`/`medium`/`high`/`xhigh` (see Models)   |
| `max_tokens`     | integer | max completion tokens                                          |
| `temperature`    | number  | sampling temperature                                          |
| `extra`          | table   | free-form vendor map sent as extra JSON fields (see Models)   |

You may declare multiple models; each agent picks one by name. To change models
at runtime, declare multiple agents (each with its own `model`) and switch
between them with Tab or `/agent` — see `shell3.agent`.

### `shell3.skill(opts)`

Declares a skill — a named instruction document the agent can pull on demand.
All three keys are required. Returns an opaque handle you pass to an agent's
`skills` list.

```lua
local plan_skill = shell3.skill({
  name        = "writing-plans",
  description = "Plan and get approval before non-trivial changes.",
  body        = [[
# Writing Plans
... full instructions the agent reads via the `skill` tool ...
]],
})
```

The `description` appears in the system prompt's skill index; the `body` is
returned in full when the agent calls the `skill` tool with that name.

### `shell3.tool(opts)`

Declares a custom tool implemented in Lua. `handler` is required and must return
a **string**. `parameters` is a JSON-Schema object table. Returns an opaque
handle you pass to an agent's `tools.custom` list.

```lua
local web_fetch = shell3.tool({
  name        = "web_fetch",
  description = "Fetch a URL and return its plain-text content and links.",
  parameters  = {
    type       = "object",
    properties = {
      url = { type = "string", description = "The URL to fetch." },
    },
    required = { "url" },
  },
  handler = function(args)
    local res, err = shell3.http.get(args.url, { timeout = 15, max_bytes = 524288 })
    if err then return "error: " .. tostring(err) end
    return res.body
  end,
})
```

The `handler` receives a Lua table of the decoded JSON arguments. Use the
handler helpers (`shell3.bash`, `shell3.http.*`, `shell3.urlencode`,
`shell3.env.secret`) to do work.

### `shell3.agent(opts)`

Declares an agent. At least one agent must be declared; call `shell3.agent`
multiple times to register several. Agents accumulate in declaration order and
the **first declared is active** at startup. At runtime, switch the active agent
with **Tab** (when idle) or `/agent <name>`; switching swaps the agent's model,
prompt, tools, guards, and skills while keeping conversation history. Agent
names must be unique. A single-agent config behaves exactly as before.

```lua
shell3.agent({
  name  = "base",
  model = "main",
  prompt = [[
You are an expert coding assistant inside shell3.
... your verbatim system prompt ...
]],

  tools = {
    bash              = true,
    bash_bg           = true,
    shell_interactive = true,
    edit              = true,
    history           = true,
    docs              = true,
    custom            = { web_fetch, brave_search },
    -- skill = false,   -- set to suppress the skill tool + skill index
  },

  skills = { plan_skill, exec_skill },

  on_tool_call = {
    my_custom_guard,
  },
})
```

| key              | type   | notes                                                                |
| ---------------- | ------ | -------------------------------------------------------------------- |
| `name`           | string | agent name (shown in the status line)                                |
| `model`          | string | must match a declared `shell3.model` name                            |
| `prompt`         | string | the verbatim system prompt (see System prompt assembly)              |
| `tools`          | table  | tool gate table (strict keys below)                                  |
| `skills`         | table  | list of skill handles                                                |
| `on_tool_call`   | table  | guard chain (see Guards)                                             |

> All tool flags default off; a bare agent (`tools = {}`) is pure text — just its `prompt`, no tools.

---

## Tools

### The gate table (`tools = { ... }`)

Each gate is a boolean (set `true` to enable). Strict keys — anything else is an
error:

| gate                | enables tool(s)                                                        |
| ------------------- | ---------------------------------------------------------------------- |
| `bash`              | `bash` — non-interactive shell command                                 |
| `bash_bg`           | `bash_bg` — detached background command                                |
| `shell_interactive` | `shell_interactive` — interactive TTY program                          |
| `edit`              | `edit_file` — exact-string-replacement file edits                      |
| `history`           | `history_get`, `history_search`                                        |
| `docs`              | `shell3_docs` — returns this document                                  |
| `prune`             | `prune_tool_result` — replace a prior tool result with a stub (opt-in) |
| `compact`           | `compact_history` — compact conversation into a summary (opt-in)       |
| `custom`            | list of `shell3.tool` handles to expose                                |
| `skill`             | set `false` to suppress the skill tool + index                         |

### Custom tools

Pass a list of `shell3.tool` handles to `tools.custom`. Only listed tools are
exposed to the model; their JSON-Schema `parameters` become the tool's
parameters, and their Lua `handler` runs when called.

### The skill tool

When the agent has **≥1 skill** in its `skills` list **and** `tools.skill` is not
set to `false`, a built-in `skill` tool is added. The model calls it with a skill
name to retrieve that skill's full `body`. The skill index (name + description of
each skill) is injected into the system prompt so the model knows what is
available.

---

## Guards

Guards are middleware that inspect each tool call before it runs. They are listed
in `on_tool_call` and run **in order**; the first non-allow result
short-circuits.

### Custom guards (Lua functions)

A guard function receives a `call` table with `tool` (string) and `params`
(table) and returns a decision table:

```lua
local function guard_no_env_edit(call)
  if call.tool == "edit_file" then
    local path = tostring((call.params or {}).file_path or "")
    if path:match("%.env$") then
      return { action = "block", reason = "editing .env files is not allowed" }
    end
  end
  return { action = "allow" }
end
```

`action` may be:

- `"allow"` — proceed (the default if you return nothing or an unrecognized value).
- `"block"` — deny this call; the model sees the `reason` and may try something else.
- `"cancel"` — abort the entire turn.

---

## Handler helpers

These functions are available on the `shell3` table for use inside custom-tool
handlers and guards.

### `shell3.env.secret(key)`

Returns the value of `key` from `.env`. **Raises a config error if the key is
absent** — declare every key you reference.

### `shell3.bash(cmd [, opts])`

Runs `bash -c cmd` and returns a table `{ exit, stdout, stderr }`.

- `opts.timeout` — seconds (default `10`, clamped to a max of `600`).

```lua
local r = shell3.bash("ls -la", { timeout = 20 })
if r.exit ~= 0 then return "error: " .. r.stderr end
return r.stdout
```

### `shell3.http.get(url [, opts])` / `shell3.http.post(url [, opts])`

Convenience HTTP calls. Both return `(res, err)` where `res` is
`{ status, body, truncated, headers }` and `err` is a string on failure (and
`res` is nil).

- `opts.timeout` — seconds (default `30`, clamped to `1..120`).
- `opts.max_bytes` — response cap (default `1 MiB`, max `16 MiB`); if exceeded,
  the body is truncated and `res.truncated` is `true`.
- `opts.headers` — table of request headers.
- `opts.body` — request body string (mainly for `post`).

`res.headers` keys are lower-cased.

### `shell3.http.request(opts)`

The general form: `shell3.http.request{ url, method, headers, body, timeout, max_bytes }`.
`method` defaults to `GET`. Same return shape as above.

### `shell3.urlencode(s)`

Returns `s` query-escaped (for building URLs).

---

## System prompt assembly

The final system prompt is **assembled at runtime** — there are no Go-template
variables in your `prompt`. shell3 concatenates, in order:

1. The agent's verbatim `prompt` text.
2. `## Skills` — the skill index (name + description), injected only when the
   skill tool is active (≥1 skill and `tools.skill ≠ false`).

Write your `prompt` as plain instructions; the engine appends the standard blocks.

---

## Models

### Reasoning levels

The `reasoning` field (and the runtime `reasoning_effort` parameter) accepts:

```
none | minimal | low | medium | high | xhigh
```

`none` omits the reasoning field entirely. For the OpenAI API (which accepts only
`minimal|low|medium|high`), `xhigh` is clamped down to `high`, so a single config
works across providers.

### The `extra` map

`extra` is a free-form table whose entries are sent as additional top-level JSON
fields on each request via the SDK's `WithJSONSet`. Use it for vendor extensions
the core schema does not model, e.g.:

```lua
extra = {
  reasoning_summary   = "auto",
  verbosity           = "medium",
  parallel_tool_calls = true,
}
```

### Tunable parameters at runtime

The OpenAI-compatible client exposes these via `/parameters`:

| parameter             | values                                         | default    |
| --------------------- | ---------------------------------------------- | ---------- |
| `reasoning_effort`    | `none`/`minimal`/`low`/`medium`/`high`/`xhigh` | `medium`   |
| `parallel_tool_calls` | `true`/`false`                                 | `true`     |
| `temperature`         | number                                         | (provider) |
| `max_tokens`          | integer                                        | `16000`    |

The adapter also surfaces vendor reasoning streams: OpenRouter's `reasoning` and
Moonshot/DeepSeek's `reasoning_content`.

---

## Slash commands

In the interactive TUI, type `/` to see commands. The registered commands:

| command       | description                                                       |
| ------------- | ----------------------------------------------------------------- |
| `/help`       | list available commands (auto; also `/h`, `/list`)                |
| `/clear`      | reset conversation context                                        |
| `/rollback`   | remove the last turn from context                                 |
| `/prune <id>` | replace tool result `<id>` with a stub                            |
| `/print <id>` | show the full (untruncated) output of tool result `<id>`          |
| `/usage`      | show token usage from the last turn                               |
| `/prompt`     | dump the system prompt and active tools                           |
| `/parameters [name value]` | list or set tunable params (e.g. `reasoning_effort`) |
| `/agent [name]` | list configured agents, or switch the active agent (also Tab)     |
| `/info`       | session details: agent, project, skills, tools                    |
| `/image <path> [prompt]` | attach an image to the next turn                       |
| `/exit`       | quit shell3 (also `/quit`)                                         |

---

## Commands

The CLI surface is intentionally tiny.

```
shell3 [message]            run the chat agent (TUI, or headless if piped/--out)
shell3 doctor               validate global + project setup
shell3 docs                 print this documentation
shell3 widget ask|pick|confirm   JSON-in/JSON-out interactive prompt widgets
```

Root flags (on `shell3` itself):

| flag             | meaning                                                       |
| ---------------- | ------------------------------------------------------------ |
| `--config`, `-c` | path to `shell3.lua` (else `./shell3.lua`, else global)      |
| `--out <path>`   | stream a JSONL audit log to `<path>`; enables headless mode  |

### `doctor`

Validates setup and exits non-zero if any check fails. It confirms `~/.shell3/`
exists, the resolved `shell3.lua` parses, reports the number of models and the
agent name, and validates the project's `.shell3/.ref`, `meta.json`, and project
state directory.

### `docs`

Prints this document (the same text the `shell3_docs` tool returns).

### `widget`

`ask`, `pick`, and `confirm` are JSON-in / JSON-out interactive prompt widgets
for hooks and scripts. Each reads a spec on stdin, paints on `/dev/tty`, and
writes a Result on stdout. Exit codes: `0` ok, `1` confirm-no, `2` timeout, `130`
cancel/eof.

---

## On-disk layout

**Global** (`~/.shell3/`):

```
~/.shell3/
├── shell3.lua            # global config (scaffolded on first run)
├── .env.example          # secrets template (scaffolded on first run)
├── .gitignore            # ignores ai-do-not-read.*, shell3.log*, projects/
├── shell3.log            # rotating app log (shell3.log.1 ... archives)
└── projects/
    └── <uuid>/
        ├── shell3.db     # SQLite: history (lazy-created)
        └── meta.json     # project name + cwd
```

**Project** (in the directory where you run shell3):

```
./shell3.lua              # optional project-local config
./.env                    # secrets, read by shell3.env.secret (you create this)
./.shell3/
├── .ref                  # points to this project's ~/.shell3/projects/<uuid>/
└── bg.json               # bash_bg job registry
```

The project's `.shell3/.ref` and `.shell3/bg.json` are gitignored automatically.

---

## Headless mode (`--out`)

Passing `--out <path>` (or piping a message in on a non-TTY stdin) runs shell3
headless and streams a structured **JSONL audit log** of everything it did — each
line is one event, and the final line is `{"kind":"end", ...}`. This makes shell3
composable: scripts, CI jobs, or other shell3 agents can spawn it, wait for the
`end` line, and read what happened.

Headless mode exports `SHELL3_HEADLESS=1` (and `SHELL3_OUT=<path>` when `--out`
is set). See **`docs/headless.md`** in the repo for the full event schema, env
vars, and the spawning-subagents pattern.

---

## Moving your config to a new machine

shell3 config is portable. To replicate your setup:

1. Copy your **`shell3.lua`** and your **`.env`** to the new machine (to
   `~/.shell3/` for a global config, or into the project directory).
2. Run `shell3` — it bootstraps the rest.

Everything under `~/.shell3/projects/` (the SQLite history databases and
project metadata) is **machine-local** and is not meant to travel; the global
`.gitignore` excludes it, along with secrets and logs.
