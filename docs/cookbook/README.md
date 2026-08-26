# shell3 cookbook

`shell3 boot` writes a lean, working config. This cookbook is everything it
*doesn't* write — drop-in recipes you copy in when you want them. Each
`lib/...` file mirrors the base config's layout.

A recipe here is **a kit section you paste into `shell3.sh`** — a `tool:`
block with its shell body, a skill file, or a whole `agent:`. Paste it,
run `shell3 tool check ~/.shell3/shell3.sh`, reload. See
[kits.md](../kits.md) and [tools.md](../tools.md).

A tool that needs an API key reads it from `.env` itself at point of use, so
secrets never enter the conversation or the agent's environment.

Every file here is fetchable raw, no checkout needed. The scaffold's
`cookbook` skill teaches the agent this, so you can ask your agent for a
capability and it can install the recipe itself (after telling you what it
would install and getting your yes):

```bash
base=https://raw.githubusercontent.com/weatherjean/shell3/main/docs/cookbook
curl -fsS "$base/lib/skills/web-search.md" -o ~/.shell3/skills/web-search.md
```

Full reference: [../configuration.md](../configuration.md).

## Contents

**Skills** (`lib/skills/` here → your `~/.shell3/skills/`; the scaffold
already ships `browser` — headed Chrome via puppeteer-core)

- `executing-plans.md` — git workflow for approved plans that touch a repo
  (the plan + approval gate itself is built in: the scaffold's `planning` skill).
- `codebase-discovery.md` — navigating unfamiliar code, pruning context aggressively.
- `web-search.md` — web research via `brave-search` / `web-fetch` wrapper scripts.
- `searxng-setup.md` — one-time setup of the local SearXNG instance; delete after it's done.
- `searxng-search.md` — keyless web search via that instance (the permanent skill).
- `camoufox-fetch.md` — fetch bot-protected / JS-heavy pages with Camoufox (anti-detect Firefox).

**Coding-agent skills** (same dir; each delegates implementation work to a
full coding agent CLI installed on the machine, as an alternative to the
scaffold's own `writing-code` skill for in-line TDD work.)

- `claude-code.md` — delegate implementation work to Claude Code (`claude -p`).
- `codex.md` — the same pattern for the OpenAI Codex CLI (`codex exec`, sandbox levels).
- `opencode.md` — the same pattern for opencode (`opencode run`; run mode auto-approves everything).
- `pi.md` — the same pattern for pi (`pi -p`; minimal by design, **no sandbox at all**).

**Docker bundles** (`lib/<name>/` here → your `~/.shell3/lib/<name>/`)

- `searxng/` — ready-to-go `docker-compose.yml` + `settings.yml` for the
  local search instance: JSON API on, bot-limiter off, localhost-only.
  Install:

  ```bash
  base=https://raw.githubusercontent.com/weatherjean/shell3/main/docs/cookbook
  mkdir -p ~/.shell3/lib/searxng
  curl -fsS "$base/lib/searxng/docker-compose.yml" -o ~/.shell3/lib/searxng/docker-compose.yml
  curl -fsS "$base/lib/searxng/settings.yml" -o ~/.shell3/lib/searxng/settings.yml
  ```

- `camoufox/` — `Dockerfile` + `fetch.py` for the one-shot anti-bot fetcher
  image used by the `camoufox-fetch` skill (install lines in the skill).

**Employees** (`lib/agents/` here → `agent:` blocks in your `~/.shell3/shell3.sh`)

- `review.md` — a review specialist instructed never to edit (name it in a
  `gate:` block to enforce that); pasting its `agent:` block into your
  `shell3.sh` IS the registration (the task tool picks it up on the next
  reload).

**Provider and host recipes**

- `service.md` — run the bot as a service: a systemd user unit, one paste.
- `mcp.md` — MCP servers: stdio + HTTP recipes, allow-lists, gating.
- `sandbox.md` — sandbox/route bash via `gate:` argv verdicts.
