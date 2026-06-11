# shell3 cookbook

Drop-in recipes for features the base config (written by `shell3 boot`) leaves
out. Each `lib/...` file mirrors the base config's module layout: copy it into
`~/.shell3/lib/`, `require` it in `shell3.lua`, and wire it into an agent.

## Usage

    -- in ~/.shell3/shell3.lua
    local plans   = require("lib.skills.writing-plans")
    local browser = require("lib.skills.browser")
    -- then, in an agent:
    --   skills = { plans, browser },

## Contents

- `lib/skills/writing-plans.lua` — planning/approval gate before non-trivial changes.
- `lib/skills/executing-plans.lua` — safe execution + git workflow after a plan.
- `lib/skills/codebase-discovery.lua` — navigating unfamiliar code.
- `lib/skills/web-search.lua` — guidance for web research.
- `lib/browser.lua` — Drive a real headed Chrome via puppeteer-core (the `browser` skill); see `lib/browser.lua`.
- `lib/guards.lua` — extra on_tool_call guards: a `block` verdict (block destructive bash) and an `ask` verdict (the ask verdict surfaces an approval prompt in the TUI / bot before a risky command runs).
- `lib/tools.lua` — custom tool template.
- `lib/extra-agents.lua` — adding more agents.
- `proxy.md` — run_proxy recipes (Codex/npx, litellm).
