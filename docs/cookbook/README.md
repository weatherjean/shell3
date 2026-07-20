# shell3 cookbook

`shell3 boot` writes a lean, working config. This cookbook is everything it
*doesn't* write — drop-in recipes you copy in when you want them. Each
`lib/...` file mirrors the base config's module layout.

The two extensible pieces, in one breath:

- **Skills** are `.md` files with a frontmatter `description:` that the agent
  reads with `cat`. Install: copy into `~/.shell3/skills/`, check
  `shell3 health`, `/reload`.
- **Scripts** are the extension mechanism: reusable glue lives in
  `~/.shell3/lib/bin/` and runs through `bash`; a script that needs an API
  key reads it from `.env` itself at point of use. The scaffold's
  `scripting` skill teaches the pattern (see
  [Scripts & secrets](../configuration.md#scripts--secrets)).

Install without a checkout: every file here is fetchable raw (the scaffold's
`cookbook` skill teaches the agent this, so you can just ask your bot for a
capability and it can install the recipe itself):

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

**Coding-agent skills** (same dir; each drives a coding agent installed on
the machine — the scaffold's `coding-agent` stub tells the bot to bring you
here and let you pick; delete the stub once one of these lands)

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

**Subagents** (`lib/agents/` here → your `~/.shell3/agents/`)

- `review.md` — a read-only review specialist; copying the file in IS the
  registration (the task tool picks it up on `/reload`).

**Provider and host recipes**

- `mcp.md` — MCP servers: stdio + HTTP recipes, allow-lists, gating.
- `models.md` — provider-specific request params via `extra`.
- `proxy.md` — `run_proxy` recipes (Codex via npx, litellm).
- `sandbox.md` — sandbox/route bash via hook argv verdicts.
- `voice-images.md` — voice + images over Telegram; Groq and OpenRouter quickstarts.
