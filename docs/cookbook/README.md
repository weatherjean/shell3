# shell3 cookbook

`shell3 boot` writes a lean, working config. This cookbook is everything it
*doesn't* write by default — drop-in recipes you copy in when you want them.

Each `lib/...` file here mirrors the base config's module layout. The pattern is
always the same three steps:

1. copy the file into `~/.shell3/lib/`,
2. `require` it in your `shell3.lua`,
3. wire it into an agent.

A reminder on how the two extensible pieces work, since the recipes lean on both:

- **Skills** are plain `.md` files the agent reads with `cat`. Each
  `lib/skills/<name>.lua` just declares
  `shell3.skill{ name, description, path="lib/skills/<name>.md" }`, and the actual
  prose lives in the sibling `.md`.
- **Custom tools** are declarative bash-command templates
  (`shell3.tool{ command=... }`), not Lua handlers. Parameters arrive as
  `$`-named environment variables, and declared `secrets` are exported into the
  command's environment.

See [../configuration.md](../configuration.md) for the full reference on models,
agents, tools, and skills.

> **Secret exposure:** declared `secrets` are passed to the command via its
> process environment. On a shared host, same-user processes can read another
> process's environment (e.g. `/proc/<pid>/environ` on Linux), so a tool secret
> is visible to anything that user can run. This is the natural cost of the
> bash-template design and is fine for a local single-user setup; on a
> multi-user host, treat tool secrets as readable by that user's other processes.

## Usage

```lua
-- in ~/.shell3/shell3.lua
local plans   = require("lib.skills.writing-plans")
local browser = require("lib.skills.browser")

-- then, in an agent:
--   skills = { plans, browser },
```

## Contents

**Skills** (`lib/skills/`)

- `writing-plans.lua` — a planning and approval gate before non-trivial changes.
- `executing-plans.lua` — safe execution plus a git workflow once a plan is agreed.
- `codebase-discovery.lua` — navigating unfamiliar code and pruning context aggressively.
- `web-search.lua` — guidance for web research with the `brave_search` / `web_fetch` tools.

**Tools and agents** (`lib/`)

- `browser.lua` — drive a real, headed Chrome via puppeteer-core (the `browser` skill).
- `tools.lua` — a custom-tool template to copy from.
- `extra-agents.lua` — adding more agents (e.g. a read-only `review` agent).

**Provider and host recipes**

- `models.md` — provider-specific request params via `extra` (e.g. MiniMax M3 `reasoning_split`).
- `proxy.md` — `run_proxy` recipes (Codex via npx, litellm).
- `sandbox.md` — sandbox or route bash via `on_tool_call` argv verdicts (docker, ssh, firejail).
