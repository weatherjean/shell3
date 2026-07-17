# shell3 cookbook

`shell3 boot` writes a lean, working config. This cookbook is everything it
*doesn't* write — drop-in recipes you copy in when you want them. Each
`lib/...` file mirrors the base config's module layout.

The two extensible pieces, in one breath:

- **Skills** are `.md` files with a frontmatter `description:` that the agent
  reads with `cat`. Install: copy into `~/.shell3/lib/skills/` (the scaffold
  agent already lists that dir), check `shell3 health`, `/reload`.
- **Custom tools** are declarative bash-command templates
  (`shell3.tool{ command=… }`), not Lua handlers. Install: copy into
  `~/.shell3/lib/`, `require` it from `shell3.lua`, wire it into the agent.
  Parameters arrive as `$`-named env vars; declared `secrets` are exported
  into the command's environment (see the
  [secrets note](../configuration.md#custom-tools) before using them on a
  shared host).

Full reference: [../configuration.md](../configuration.md).

## Contents

**Skills** (`lib/skills/`; the scaffold already ships `browser` — headed
Chrome via puppeteer-core)

- `writing-plans.md` — a planning + approval gate before non-trivial changes.
- `executing-plans.md` — safe execution and a git workflow once a plan is agreed.
- `codebase-discovery.md` — navigating unfamiliar code, pruning context aggressively.
- `web-search.md` — web research with the `brave_search` / `web_fetch` tools.

**Tools and agents** (`lib/`)

- `tools.lua` — a custom-tool template to copy from.
- `extra-agents.lua` — more subagents (e.g. a read-only `review` specialist).

**Provider and host recipes**

- `models.md` — provider-specific request params via `extra`.
- `proxy.md` — `run_proxy` recipes (Codex via npx, litellm).
- `sandbox.md` — sandbox/route bash via `on_tool_call` argv verdicts.
- `voice-images.md` — voice + images over Telegram; Groq and OpenRouter quickstarts.
