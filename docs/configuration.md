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
it, shell3 summarizes the conversation so far and continues against that
summary. This is host-managed: there are no model-driven prune or compact tools
to call. Leave it unset (or `0`) to disable.

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
logs go to `./.shell3/proxy-<model>.log`:

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
`/agent` — your conversation history comes along.

```lua
shell3.agent({
  name   = "code",
  model  = "main",
  prompt = [[You are a careful pair-programmer…]],
  tools  = {
    bash              = true,
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

Optional fields: `background = true` (fire-and-forget) and `timeout = N`
(seconds). See [cookbook/lib/tools.lua](cookbook/lib/tools.lua) for a full
template, including the `web_fetch` and `brave_search` tools the base config
ships with.

> **A note on secrets:** declared `secrets` are passed via the command's process
> environment. On a shared host, same-user processes can read another process's
> environment (`/proc/<pid>/environ` on Linux). That's fine for a local,
> single-user setup; on a multi-user box, treat tool secrets as readable by
> anything that user can run. More in [security.md](security.md).

## Gating the shell — `wrap_bash`

shell3 is **unsafe by default**: `bash` and `bash_bg` run with no restrictions.
The single place to inspect, rewrite, or block commands is `shell3.wrap_bash`.
It receives the command string and returns one of:

- **a string** → run it under `bash -c` (you can rewrite the text),
- **a table** (list of strings) → an argv list exec'd directly — this swaps the
  *runner*, not just the text, and the command arrives as one argv element so
  nothing re-parses or re-quotes it,
- **nil / false `[, reason]`** → block it.

```lua
shell3.wrap_bash(function(cmd)
  if cmd:match("rm%s+%-rf%s+/") then return nil, "refusing rm -rf /" end
  return cmd                       -- allow (optionally rewritten)
end)
```

The table form is what makes this a real wrapper — you choose the program that
runs the agent's command, so you can route everything through a container, an
SSH host, or `firejail`:

```lua
shell3.wrap_bash(function(cmd)
  return {"docker", "exec", "mycontainer", "bash", "-c", cmd}
end)
```

A malformed argv table (empty, or any non-string element) fails **closed**: the
command is blocked, never run unwrapped. Custom command-template tools bypass
`wrap_bash` by design — that command is your trusted template, not model input —
so bake any sandboxing into the tool's own command. The full recipe set is in
[cookbook/sandbox.md](cookbook/sandbox.md).

## Redirecting hallucinated tools — `stub_tools`

Models trained on other harnesses reflexively reach for `read_file`, `grep`, or
`write_file`. Register those names as stubs and shell3 returns a one-line
redirect instead of erroring — a self-correcting nudge back to bash and
`edit_file`:

```lua
shell3.stub_tools({
  read_file  = "Use bash: cat <path> (or sed -n for ranges).",
  grep       = "Use bash: rg <pattern>.",
  write_file = "Use edit_file (empty old_string creates/overwrites).",
})
```

Stubs are config-global (every agent sees them). Later keys override earlier
ones, and a stub whose name collides with a real tool is ignored.

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
