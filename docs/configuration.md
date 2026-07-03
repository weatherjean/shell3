# Configuration

Everything shell3 does is decided by one file: `shell3.lua`. It's plain Lua, so
it's versionable, diffable, and programmable — you configure shell3 like
software, not like a platform with a settings panel.

`shell3 boot` writes a working config for you. This page is for when you want to
go beyond the defaults: add a model, write a tool, gate the shell, or understand
how shell3 finds your config in the first place.

## Where the config lives

`boot` creates three things under `~/.shell3/`:

- `shell3.lua` — the config: your models, agents, tools, and skills.
- `lib/` — tools and skills as small Lua modules the config `require`s.
- `.env` — your secrets (API keys, tokens). **Never commit this file.**

### How the config path is resolved

The `--config` / `-c` flag takes a **name**, not a path — unless it ends in
`.lua`:

| You pass | shell3 loads |
|----------|--------------|
| *(nothing)* | `~/.shell3/shell3.lua` |
| `-c code` | `~/.shell3/code.lua` |
| `-c ./my.lua` | that literal file |

The current working directory is never consulted for config. This keeps a
session's behavior predictable no matter where you launch it from.

## Models

A model is an endpoint plus the parameters shell3 should send it. Any
OpenAI-compatible endpoint works:

```lua
shell3.model("main", {
  base_url       = "https://api.openai.com/v1",
  api_key        = shell3.env.secret("MAIN_API_KEY"),  -- read from .env
  model          = "gpt-5.2",
  context_window = 128000,   -- the model's real token budget
  compact_at     = 100000,   -- auto-compact threshold (see below); 0 = off
  -- reasoning   = "medium", -- if the model supports reasoning effort
})
```

Set `context_window` to the model's *actual* budget — the wrong number throws
off the context-usage reminders and the compaction trigger.

`compact_at` is an absolute prompt-token threshold. When a turn's prompt crosses
it, shell3 summarizes the head of the conversation and keeps a verbatim recent
tail — so the model retains its immediate working context. This is
host-managed: there are no model-driven prune or compact tools to call. Leave
it unset (or `0`) to disable.

Two optional knobs tune the compaction policy:

```lua
shell3.model("main", {
  compact_at   = 100000,  -- full tail-preserving compaction
  keep_recent  = 33000,   -- verbatim tail size (tokens); default round(compact_at * 0.33)
  prune_at     = 60000,   -- lower threshold: stubs old tool outputs, no LLM call
                          --   default round(compact_at * 0.6); 0 = disabled
})
```

`keep_recent` controls how many recent prompt tokens are preserved verbatim
across a compaction. Only the head (everything before that boundary) is
summarized. Defaults to `round(compact_at * 0.33)`; clamped to
`round(compact_at * 0.5)` if you set it at or above `compact_at`.

`prune_at` is a cheaper first response: when the prompt crosses this lower
threshold shell3 stubs old tool outputs inline (no LLM call needed), buying
room before a full compaction is required. Defaults to
`round(compact_at * 0.6)`; set to `0` to skip this tier entirely. Ignored if
set at or above `compact_at`.

### Provider-specific knobs

Some providers want a non-standard request field shell3 doesn't model directly.
The `extra` table is the escape hatch — its keys are injected verbatim into the
top-level request JSON:

```lua
extra = { reasoning_split = true },           -- MiniMax: route thinking to reasoning_content
extra = { verbosity = "high" },               -- gpt-5-style verbosity
extra = { provider = { order = {"anthropic"} } },  -- OpenRouter routing (nested objects work)
```

Only set `extra` when you need it — strict endpoints reject unknown fields. See
[cookbook/models.md](cookbook/models.md) for more.

### Local proxies (`run_proxy`)

If a model needs a shim in front of its endpoint — say a Codex subscription
fronted by `npx`, or a litellm gateway — set `run_proxy`. shell3 starts that
command (detached, fire-and-forget) the first time an agent uses the model, and
logs go to `~/.shell3/proxy-<model>.log`:

```lua
shell3.model("codex", {
  run_proxy = "npx @some/codex-proxy --port 8787",
  base_url  = "http://localhost:8787/v1",
  -- ...
})
```

If a proxy is already listening, the spawn just fails to bind and the first
request proceeds against whatever is there. See
[cookbook/proxy.md](cookbook/proxy.md).

## Agents

An agent is a name, a model, a system prompt, and a set of tools. Declare as
many as you like and switch between them mid-session with `Tab` (when idle) or
`/agent` — your conversation history comes along. Names are deduplicated rather
than rejected: agents and subagents share one namespace, so a second entry
named `code` auto-suffixes to `code2` (then `code3`, …) while the first keeps
its bare name.

```lua
shell3.agent({
  name   = "code",
  model  = "main",
  prompt = [[You are a careful pair-programmer…]],
  tools  = {
    bash              = true,
    read              = true,   -- paged text-file reading (see below)
    bash_bg           = true,   -- background / long-running work
    shell_interactive = true,   -- only for truly interactive programs
    edit              = true,   -- the edit_file tool
    media             = true,   -- inbound/outbound images + audio
    custom            = { my_tool },          -- Lua-defined tools (below)
    subagents         = { explorer },         -- delegatable specialists
  },
  skills = { writing_plans },                 -- skill handles (below)
})
```

The convention that ships with `boot` is two agents: a full-access `code` agent
and a read-only `plan` agent that investigates and designs but cannot edit —
design with `plan`, switch to `code` to build. But it's only a convention: add a
`review` agent, a `docs` agent, whatever fits your work. See
[cookbook/lib/extra-agents.lua](cookbook/lib/extra-agents.lua).

### The `read` tool

`read = true` gives the agent a paged, text-file reader. It accepts a `path`
(absolute or workdir-relative), an optional 1-indexed `offset` (default 1), and
an optional `limit` in lines (default 2000). Output is raw file content — no
line-number prefixes — so `edit_file` can exact-match strings straight from the
output.

Truncation is capped at **2000 lines or 50 KB**, whichever comes first. When
the file is longer, a machine-readable footer tells the model exactly where to
resume:

```
[Showing lines 1-2000 of 8421. Use offset=2001 to continue.]
```

The tool is **text-only**: binary files (detected by a ~4 KB NUL-byte / high
non-printable scan) are refused with a redirect to `read_media` or `bash xxd`.
Directories, missing files, and offsets past EOF are clean errors. Search still
belongs in bash — reach for `rg` or `grep` when you want to match patterns
across files rather than read one file's content.

### The `list_files` tool

`list_files = true` gives the agent a directory lister that returns an indented
tree (directories first, suffixed `/`). It accepts a `path` (absolute or
workdir-relative, default the project root), a `depth` (max levels to recurse,
**default 2**), and an `ignore` list of glob patterns. A pattern without `/`
matches the base name (`*.test.go`); with `/` it matches the path relative to the
listed directory (`src/gen/*`).

It does **no automatic filtering** — hidden and vendored files are listed unless
you `ignore` them — so start shallow and narrow as you go: widen `depth` only
when needed, pass a deeper `path`, or add `ignore` globs like
`{ "node_modules", "*.lock" }`. Output is capped at **1000 entries** with a
truncation notice; missing paths and non-directories are clean errors.

Together, `read` + `list_files` make a **fully read-only agent that needs no
bash** — it can browse the tree and read files but never execute a command. Drop
`bash`/`edit` from such an agent's `tools` and it can still investigate and
report (for content *search*, it would still need `bash` for `rg`/`grep`):

```lua
tools = { read = true, list_files = true }  -- browse + read, no shell
```

## Subagents & delegation

A subagent is a delegatable specialist: declared with `shell3.subagent({...})`
instead of `shell3.agent({...})`, so it never appears in the `Tab`/`/agent`
rotation — only the agents that list it may spawn it. The shape is the same as
an agent's (name, model, prompt, tools) plus a `description`, which is what the
parent model reads when deciding what to delegate:

```lua
local explorer = shell3.subagent({
  name        = "explorer",
  description = "Read-only investigation of the codebase. No edits.",
  model       = "main",
  prompt      = [[You are a focused code explorer…]],
  tools       = { bash = true, read = true, list_files = true },
})

shell3.agent({
  name       = "code",
  delegation = true,                      -- inject the task-tool guidance
  tools      = { subagents = { explorer } },  -- who this agent may spawn
  -- ...
})
```

When an agent sets **both** `delegation = true` and a non-empty
`tools.subagents` list, shell3 advertises four tools to it — `task` (spawn one:
`{subagent_type, prompt, description}`; returns immediately), `task_list`,
`task_status <id>`, and `task_cancel <id>` — and injects a "Delegation"
system reminder naming the allowed subagents. One without the other advertises
nothing: the tool and the guidance always appear together.

A spawned subagent runs as an **in-process background job** — a child-session
goroutine, not a subprocess — and on completion the parent is woken with a
capped result summary injected into its context. Subagents run headless (an
`{ask=...}` gate verdict is auto-denied; see
[the gate section](#deny-prompt-confirmation-and-headless-degradation)), and
delegation is single-level: a subagent is not given the `task` tool.

Two optional config-global knobs cap the machinery:

```lua
shell3.background({ max_concurrent = 8 })  -- concurrent background jobs (default 8)
shell3.subagents({ max_depth = 3 })        -- max subagent nesting depth (default 3)
```

The same job runtime backs `bash_bg`, which is gated separately by
`tools = { bash_bg = true }` — not by `delegation`. See
[library.md](library.md#subagents) for the runtime view.

## Custom tools

A custom tool is **not** a Lua function — it's a bash command template. You give
it a name, a JSON schema for its parameters, and a command. Declared parameters
arrive in the command's environment as lowercase `$`-named variables; declared
`secrets` are exported too (and kept out of the command string). The command's
stdout is what the model sees.

```lua
local weather = shell3.tool({
  name        = "weather",
  description = "Current weather for a city.",
  parameters  = {
    type       = "object",
    properties = { city = { type = "string", description = "City name." } },
    required   = { "city" },
  },
  secrets = { "WEATHER_API_KEY" },           -- exported as $WEATHER_API_KEY
  command = [[
curl -sf -G "https://api.example.com/v1/current" \
  -H "Authorization: Bearer $WEATHER_API_KEY" \
  --data-urlencode "q=$city" \
| jq -r '.location.name + ": " + (.current.temp_c|tostring) + "°C"'
]],
})
```

Two habits keep tools safe and tidy: use `curl --data-urlencode` for any
user-supplied parameter (never interpolate model text straight into a URL), and
shape the output with `jq` so you return a clean line, not a wall of JSON.

Optional fields: `background = true` (runs the command as an in-process
background job, like `bash_bg`: it shows up in the TUI `:background` modal and
the agent is notified with a completion notice on a later turn) and
`timeout = N` (seconds; foreground tools only). See [cookbook/lib/tools.lua](cookbook/lib/tools.lua) for a full
template, including the `web_fetch` and `brave_search` tools the base config
ships with.

> **A note on secrets:** declared `secrets` are passed via the command's process
> environment. On a shared host, same-user processes can read another process's
> environment (`/proc/<pid>/environ` on Linux). That's fine for a local,
> single-user setup; on a multi-user box, treat tool secrets as readable by
> anything that user can run. More in [security.md](security.md).

## Opt-in command gate — `on_tool_call`

shell3 is **unsafe by default**: bash commands run with no restrictions.
`on_tool_call` fires before **every** tool the model calls — `bash`, `bash_bg`,
`shell_interactive`, `read`, `list_files`, `edit_file`, and custom tools — and the
handler decides per tool by switching on `t.name`. It is off until you register it;
a fresh config gates nothing. `t.command` carries the bash command for the three
bash tools and is **nil** for everything else, so a denylist that matches
`t.command` must first check `t.name` (see the idiom below). Handlers are
**chainable** — multiple `on_tool_call` calls run in declaration order; the first
**terminal** verdict wins.

### The `t` event

Each handler receives a table `t`:

| Field | Description |
|-------|-------------|
| `t.name` | The **real** tool name: `"bash"`, `"bash_bg"`, `"shell_interactive"`, `"read"`, `"list_files"`, `"edit_file"`, or a custom tool's name. |
| `t.command` | The bash command string — only for the three bash tools; **nil** for every other tool. |
| `t.args` | Raw arguments JSON string (every tool). Gate a non-bash tool by inspecting this, e.g. a `read`/`edit_file` path. |

### Verdict contract

A handler returns one of:

| Return value | Effect |
|---|---|
| `nil` | Pass; continue to the next handler (or run). |
| `{ command = "..." }` | Rewrite the bash command text; continue the chain. **Bash tools only** — on a non-bash tool this fails closed. |
| `{ argv = { ... } }` | **Terminal**: exec this argv exactly (runner swap). **`bash`/`bash_bg` only** — `shell_interactive` and non-bash tools fail closed. |
| `{ block = true, reason = "..." }` | **Terminal**: block; `reason` is surfaced to the model. Works for any tool. |
| `{ ask = "prompt", reason = "...", ask_timeout = N }` | Prompt a human; allowed → run, declined/headless → block with `reason`. Works for any tool. `ask_timeout` optional (seconds, default 300). |

A handler that raises a Lua error **fails closed** (blocks). Only `{block=true}`
blocks via the block verdict; a returned table that contains none of the recognized
keys (`block`/`argv`/`ask`/`command`) fails closed (is blocked) as a safety
default; return `nil` to pass.

### Writing a denylist with `shell3.regex`

`shell3.regex(pattern)` compiles a Go RE2 pattern **at config load** — a bad
pattern is a load error, never a runtime surprise. Returns an object with
`:match(s) -> bool (unanchored)`.

Recommended idiom for a hard-block / ask-human denylist:

```lua
local re   = shell3.regex
-- (?s) so .* spans newlines; match t.command (the whole string) so
-- chaining can't hide a flagged fragment behind a benign prefix.
local HARD = { re([[(?s)rm\s+-rf\s+/]]), re([[(?s)mkfs]]), re([[(?s)dd\s+if=]]) }
local ASK  = { re([[(?s)rm\s+-rf]]), re([[(?s)\bgit\s+push]]), re([[(?s)curl\b.*\|\s*(ba)?sh]]) }
local ENV  = re([[\.env]]) -- hoisted like the lists above: compiled once at load

shell3.on_tool_call(function(t)
  -- Gate the bash family. This guard is REQUIRED: t.command is nil for non-bash
  -- tools, so matching it without the check would error (→ fail closed).
  if t.name == "bash" or t.name == "bash_bg" or t.name == "shell_interactive" then
    for _, p in ipairs(HARD) do
      if p:match(t.command) then return { block = true, reason = "hard_deny" } end
    end
    for _, p in ipairs(ASK) do
      if p:match(t.command) then
        return { ask = "Run?\n" .. t.command, reason = "denied" }
      end
    end
  end
  -- Other tools fall through to nil (run). Gate them by name + args if you want,
  -- e.g. refuse to read the secrets file:
  if t.name == "read" and ENV:match(t.args) then
    return { block = true, reason = "no reading .env" }
  end
end)
```

Because the gate sees every tool, the same hook can enforce things like "never
read `.env`" or "no `edit_file` under `/etc`" — match on `t.name` and `t.args`.
`{command}`/`{argv}` rewrites only make sense for the bash family, so returning one
for a non-bash tool fails closed.

> **No implicit `(?s)`.** `shell3.regex` does not prepend `(?s)` automatically —
> add it yourself on patterns where `.*` must span newlines. `^`/`$` anchor to the
> start/end of the whole command (not each line); prefer `\b`-anchored fragments
> (`\bgit\s+push`) over `^`/`$` unless you specifically mean the command's ends.

### Deny-prompt confirmation and headless degradation

When a handler returns `{ask=...}`, a human must confirm. The interactive **TUI
shows an inline `y/N` prompt**; **ACP clients receive a `session/request_permission`
request** (see [acp.md](acp.md#permissions-on_tool_call--sessionrequest_permission)).
**Headless subagents** have no attached human, so an `{ask=...}` verdict
is auto-denied with its `reason`; the block reason flows back to the parent agent
in the in-process completion notice so the parent — where a human *is* attached — can decide
how to proceed. A prompt nobody answers falls back to deny after the timeout
(`ask_timeout`, default 300 s). `{block=true}` never prompts — it blocks
everywhere, headless or not.

Because there's no allowlist, ordinary reads (`cat`, `rg`, `ls`, …) match no
pattern and just run — a headless subagent explores freely. Only commands you
explicitly gate in a handler are affected.

### Runner swap (container, SSH, firejail)

The `{ argv = { ... } }` verdict lets you choose the program that runs the
agent's command — the command arrives as one argv element so nothing re-parses or
re-quotes it:

```lua
shell3.on_tool_call(function(t)
  -- Wrap every bash command in the container. shell_interactive has no argv form,
  -- so listing it here blocks it (fail closed) rather than running it un-sandboxed.
  if t.name == "bash" or t.name == "bash_bg" or t.name == "shell_interactive" then
    return { argv = {"docker", "exec", "mycontainer", "bash", "-c", t.command} }
  end
end)
```

A malformed argv table (empty, or any non-string element) fails **closed**: the
command is blocked, never run unwrapped. A runner swap also has no interactive-PTY
form, so a `shell_interactive` call under an `{argv=...}` policy fails **closed**
(blocked) — or set `shell_interactive = false` for that agent. A custom
command-template tool's command is your trusted template (not model input), so it is
never rewritten — but the tool **call** still fires `on_tool_call` (by its name, with
`t.command` nil), so you can `block`/`ask` it. The full recipe set is in
[cookbook/sandbox.md](cookbook/sandbox.md).

### Tool-result rewriting — `on_tool_result`

The symmetric post-execution hook `shell3.on_tool_result(fn)` runs after a tool
produces output. Like `on_tool_call`, it fires for **every** tool, and `r.name` is
the real tool name — `"bash"`, `"bash_bg"`, `"read"`, `"edit_file"`, or a custom
tool's name. The
handler receives `r` with `r.name`, `r.args` (raw arguments JSON), and `r.output`.
Return `{ output = "..." }` to replace what the model sees; return `nil` to pass
through unchanged. Primary use: secret redaction — for which you usually want to
cover all output, not just one tool:

```lua
shell3.on_tool_result(function(r)
  return { output = (r.output:gsub("API_KEY=%S+", "API_KEY=[redacted]")) }
end)
```

Errors in `on_tool_result` handlers fail **open** — a broken result-rewriter
must not destroy tool output — so they are logged and the original passes through.
(Contrast `on_tool_call`, which fails closed: blocking is safe, silently nuking
output is not.) The flip side: if your redactor errors, the **unredacted** output
reaches the model, so keep redaction handlers simple and total.

## Redirecting hallucinated tools — `stub_tools`

Models trained on other harnesses reflexively reach for `read_file`, `grep`, or
`write_file`. Register those names as stubs and shell3 returns a one-line
redirect instead of erroring — a self-correcting nudge back to bash and
`edit_file`:

```lua
shell3.stub_tools({
  read_file  = "Use the read tool.",
  grep       = "Use bash: rg <pattern>.",
  write_file = "Use edit_file (empty old_string creates/overwrites).",
})
```

Stubs are config-global (every agent sees them). Later keys override earlier
ones, and a stub whose name collides with a real tool is ignored.

## Theming — `shell3.theme`

The TUI **senses the terminal background** and adapts: it never paints its own
canvas, so backgrounds pass through, and it switches between a dark and a light
foreground palette so text stays legible on either. Terminals that don't answer
the background query keep the dark palette.

Override individual colors with `shell3.theme` — a table of colour tokens to
`#RRGGBB` hex values. Overrides sit on top of the sensed palette:

```lua
shell3.theme({
  primary = "#EAB308", -- brand: prompt, edit_file, headings, NORMAL badge
  green   = "#78AA78", -- bash / INSERT badge
  red     = "#DC2626", -- errors / bash_bg / ctrl-c
  cyan    = "#5BB6C9", -- : commands, bg count
  pink    = "#D98FB8", -- other tools
  reason  = "#87A58C", -- reasoning / help headers
  fg      = "#E5E7EB", -- body text
  fg_dim  = "#9CA3AF", -- secondary text
  muted   = "#6B7280", -- chrome: chevrons, hints, reminders
})
```

Every token is optional — declare only the ones you want to change. An unknown
token or a value that isn't `#RRGGBB` is skipped with a startup warning rather
than failing the load. Overrides are config-global and apply to both the light
and dark palette.

### Custom welcome card — `shell3.welcome`

The centered splash shown before your first message can be replaced entirely.
`shell3.welcome` takes a string that is rendered **verbatim** (centered in the
viewport), so it may embed ANSI escapes for terminal colors — use `\27` for the
escape byte:

```lua
shell3.welcome(
  "\27[38;5;208m✦ my agent ✦\27[0m\n" ..
  "ready when you are"
)
```

The content is passed through untouched, so anything your terminal understands
works — 16-color, 256-color, or truecolor escapes, box-drawing, ASCII art. It is
config-global; a later `shell3.welcome` call replaces an earlier one, and an
empty string keeps the built-in card.

Keep the card within the viewport: it's centered as-is, so art taller or wider
than the window can't be centered and will clip or wrap. Size it for a small
terminal.

The string is built at config-load time by the full Lua VM, so it can come from a
command — `shell3.welcome(io.popen("cat art.ansi"):read("*a"))`, or a `pwd` /
`git branch` card. See [the cookbook](cookbook/welcome.md) for ready-to-copy
recipes.

## Skills

A skill is a plain `.md` file the agent reads with `cat` when it's relevant —
there is no `skill` tool. You declare the skill with a name, a one-line
description (which the agent sees and uses to decide whether to read the body),
and a path to the markdown:

```lua
local plans = shell3.skill({
  name        = "writing-plans",
  description = "Planning + approval gate before any non-trivial change.",
  path        = "lib/skills/writing-plans.md",
})
-- then grant it to an agent: skills = { plans },
```

Skills granted to an agent are listed by absolute path in its system prompt
under `## Skills`, so the model knows they exist and when to reach for them. The
[cookbook](cookbook/) ships ready-to-use skills for planning, executing plans,
codebase discovery, and web search.

## Putting it together

For a full example, read the base config `boot` writes —
`~/.shell3/shell3.lua`. For drop-in additions (extra agents, more skills, the
browser skill, proxy and sandbox setups), see the
[cookbook](cookbook/README.md).
