# shell3 cookbook

Drop-in recipes for features the base config (written by `shell3 boot`) leaves
out. Each `lib/...` file mirrors the base config's module layout: copy it into
`~/.shell3/lib/`, `require` it in `shell3.lua`, and wire it into an agent.

## Usage

    -- in ~/.shell3/shell3.lua
    local plans = require("lib.skills.writing-plans")
    local mcp   = require("lib.mcp")
    -- then, in an agent:
    --   skills = { plans },
    --   tools  = { mcp = { mcp.chrome } },

## Contents

- `lib/skills/writing-plans.lua` — planning/approval gate before non-trivial changes.
- `lib/skills/executing-plans.lua` — safe execution + git workflow after a plan.
- `lib/skills/codebase-discovery.lua` — navigating unfamiliar code.
- `lib/skills/web-search.lua` — guidance for web research.
- `lib/mcp.lua` — declaring an MCP server and attaching its tools.
- `lib/guards.lua` — extra on_tool_call guards (block destructive bash).
- `lib/tools.lua` — custom tool template.
- `lib/extra-agents.lua` — adding more agents.
- `proxy.md` — run_proxy recipes (Codex/npx, opencode-go, litellm).
