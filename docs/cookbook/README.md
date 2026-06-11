# shell3 cookbook

Drop-in recipes for features the base config (written by `shell3 boot`) leaves
out. Each `lib/...` file mirrors the base config's module layout: copy it into
`~/.shell3/lib/`, `require` it in `shell3.lua`, and wire it into an agent.

Skills are plain `.md` files the agent reads with `cat`; each `lib/skills/<name>.lua`
just declares `shell3.skill{ name, description, path="lib/skills/<name>.md" }` and the
prose lives in the sibling `.md`. Custom tools are declarative bash-command templates
(`shell3.tool{ command=... }`), not Lua handlers — params arrive as `$`-named env vars
and declared `secrets` are exported into the command env.

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
- `lib/tools.lua` — custom tool template.
- `lib/extra-agents.lua` — adding more agents.
- `proxy.md` — run_proxy recipes (Codex/npx, litellm).
