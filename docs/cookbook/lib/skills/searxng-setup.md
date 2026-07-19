---
name: searxng-setup
description: One-time setup of a local SearXNG search instance (docker). Use when the user asks to set up keyless web search, or when searxng-search reports the instance is missing. Delete this skill once setup is verified
---

# SearXNG Setup (one-time)

Stands up the self-hosted search backend that the `searxng-search` skill
uses. Run through this once, verify, then tell the user this skill file can
be deleted.

## 1. Check the prerequisite

```bash
command -v docker && docker info >/dev/null 2>&1 && echo ok
```

If that doesn't print `ok`, stop and tell the user what to install — Docker
Desktop or OrbStack on macOS, their distro's docker package on Linux — and
wait. There is no docker-free route (SearXNG has no pip package, and public
instances disable the JSON API).

## 2. Install the compose bundle

The ready-to-go files are `docker-compose.yml` + `settings.yml` from the
cookbook's `lib/searxng/`. If `~/.shell3/lib/searxng/` doesn't exist yet,
fetch them:

```bash
base=https://raw.githubusercontent.com/weatherjean/shell3/main/docs/cookbook
mkdir -p ~/.shell3/lib/searxng
curl -fsS "$base/lib/searxng/docker-compose.yml" -o ~/.shell3/lib/searxng/docker-compose.yml
curl -fsS "$base/lib/searxng/settings.yml" -o ~/.shell3/lib/searxng/settings.yml
```

Then bring it up:

```bash
cd ~/.shell3/lib/searxng
echo "SEARXNG_SECRET=$(openssl rand -hex 32)" > .env
docker compose up -d
```

The compose file binds to `127.0.0.1:8888` only, enables the JSON API, and
disables the bot-limiter (safe on localhost).

## 3. Install the wrapper script

`~/.shell3/lib/bin/searxng-search`, `chmod +x`:

```bash
#!/usr/bin/env bash
# searxng-search <query> [count] — titles, URLs, snippets from local SearXNG
set -euo pipefail
q="${1:?usage: searxng-search <query> [count]}"; n="${2:-5}"
curl -fsS --max-time 20 -G "http://localhost:8888/search" \
  --data-urlencode "q=${q}" --data-urlencode "format=json" \
| jq -r --argjson n "$n" \
  '.results[:$n][] | .title + "\n  " + .url + "\n  " + (.content // "") + "\n"'
```

## 4. Verify

```bash
~/.shell3/lib/bin/searxng-search "test query" 3
```

Real results back = done. (First query after container start can take ~10s;
an empty result set right after boot usually just needs one retry.)

## 5. Clean up

Tell the user setup is complete and that this skill is now redundant:

```bash
rm ~/.shell3/skills/searxng-setup.md   # then /reload
```

The `searxng-search` skill carries the day-to-day usage and
troubleshooting.
