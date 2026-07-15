# shell3 cookbook

`shell3 boot` writes a lean, working config. This cookbook is everything it
*doesn't* write by default — drop-in recipes you copy in when you want them.

Each `lib/...` file here mirrors the base config's module layout.

A reminder on how the two extensible pieces work, since the recipes lean on both:

- **Skills** are plain `.md` files with YAML frontmatter (a required
  `description`, an optional `name` defaulting to the filename) that the agent
  reads with `cat`. Installing one is a single step: copy the `.md` into a
  directory your agent lists under `skills = { ... }` — the scaffold's agent
  already lists `lib/skills/`, so `~/.shell3/lib/skills/` just works. Verify
  with `shell3 health`, then `/reload`.
- **Custom tools** are declarative bash-command templates
  (`shell3.tool{ command=... }`), not Lua handlers: copy the file into
  `~/.shell3/lib/`, `require` it in your `shell3.lua`, and wire it into an
  agent. Parameters arrive as `$`-named environment variables, and declared
  `secrets` are exported into the command's environment.

See [../configuration.md](../configuration.md) for the full reference on models,
agents, tools, and skills.

> **Secret exposure:** declared `secrets` are passed to the command via its
> process environment. On a shared host, same-user processes can read another
> process's environment (e.g. `/proc/<pid>/environ` on Linux), so a tool secret
> is visible to anything that user can run. This is the natural cost of the
> bash-template design and is fine for a local single-user setup; on a
> multi-user host, treat tool secrets as readable by that user's other processes.

## Usage

```bash
# skills: drop the .md into a granted skills dir — that's the whole install
cp writing-plans.md ~/.shell3/lib/skills/
shell3 health   # confirms it parsed; then /reload in the bot
```

## Contents

**Skills** (`lib/skills/`)

- `writing-plans.md` — a planning and approval gate before non-trivial changes.
- `executing-plans.md` — safe execution plus a git workflow once a plan is agreed.
- `codebase-discovery.md` — navigating unfamiliar code and pruning context aggressively.
- `web-search.md` — guidance for web research with the `brave_search` / `web_fetch` tools.

(The `browser` skill — a real, headed Chrome via puppeteer-core — ships in the
base scaffold now, enabled by default.)

**Tools and agents** (`lib/`)

- `tools.lua` — a custom-tool template to copy from.
- `extra-agents.lua` — adding more subagents (e.g. a read-only `review` specialist).

**Provider and host recipes**

- `models.md` — provider-specific request params via `extra` (e.g. MiniMax M3 `reasoning_split`).
- `proxy.md` — `run_proxy` recipes (Codex via npx, litellm).
- `sandbox.md` — sandbox or route bash via `on_tool_call` argv verdicts (docker, ssh, firejail).
