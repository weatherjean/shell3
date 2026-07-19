---
name: searxng-search
description: Keyless web search via the local SearXNG instance — aggregated results from Google/Brave/DDG/etc with no API key
---

# SearXNG Search Skill

Use this skill when a task needs current information, external facts, or
source-grounded answers. Searches go through the self-hosted SearXNG
instance on `localhost:47821` — keyless, private, aggregated across
Google/Brave/Startpage/DDG.

## Usage

```bash
~/.shell3/lib/bin/searxng-search "query" 3     # quick lookup / verify one fact
~/.shell3/lib/bin/searxng-search "query" 10    # normal research
```

- Narrow with inline bangs in the query: `!news`, `!it`, or a specific
  engine (`!go` Google, `!ddg`). Extra API params (`time_range=day|month|year`,
  `language=en`, `categories=…`) can be added to the wrapper's curl as
  `--data-urlencode` args.
- **A snippet is not the page.** Always fetch full pages before quoting details.
  Escalation ladder:
  1. `web-fetch` (plain curl) — works for most sites
  2. `camoufox-fetch` — for Cloudflare/DataDome/JS-heavy pages
  3. **browser skill** (headed Chrome) — for login-gated or interactive pages
- Prefer official docs and primary sources; cross-check important claims;
  refine queries instead of repeating them.

## Reliability

- Individual engines get bot-blocked from time to time (DuckDuckGo CAPTCHAs
  are chronic). That's why SearXNG aggregates — don't pin a single engine;
  per-engine failures self-heal after a cooldown.
- Empty results / connection refused → check the instance:
  `docker ps | grep searxng`, `docker logs searxng --tail 20`, and
  `curl -s 'http://localhost:47821/search?q=test&format=json' | head -c 300`.
  A stopped container restarts with
  `cd ~/.shell3/lib/searxng && docker compose up -d`.
- If the instance was never set up (no `~/.shell3/lib/searxng/`), tell the
  user to install the cookbook's `searxng-setup` skill and run it.
- Engines break and get fixed upstream constantly; occasionally refresh:
  `cd ~/.shell3/lib/searxng && docker compose pull && docker compose up -d`.
