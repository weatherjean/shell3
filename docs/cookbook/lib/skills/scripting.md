---
name: scripting
description: Use when a task needs a reusable script or an API call that requires a secret (API key, token) — write a safe wrapper script instead of inlining the secret.
---

# Safe scripts & secrets

Reusable logic becomes a script. Secrets never enter the conversation.

## Where scripts live

- Reusable scripts: `~/.shell3/lib/bin/`, run by path — `~/.shell3/lib/bin/weather Tokyo`.
  Create with edit_file, then `chmod +x`.
- One-off logic: plain bash, no script.
- A big workflow: also add a skill (`*.md` beside this one) saying when and
  how to run it.

## Secrets

Secrets live in `~/.shell3/.env` as `KEY=value` lines. The rules:

1. The SCRIPT reads the one key it needs, at point of use:
   `key="$(grep '^OPENWEATHER_KEY=' ~/.shell3/.env | cut -d= -f2-)"`
2. Never read `.env` in the conversation. Never `export` a secret, pass one
   as an argument, embed one in a command string, or print one. If output
   might contain a secret, redact it before returning.
3. Missing secret? Ask the user to add the key to `~/.shell3/.env`; use it
   only through a script.

## Template

```bash
#!/usr/bin/env bash
# weather <city> — current weather via OpenWeather
set -euo pipefail
city="${1:?usage: weather <city>}"
key="$(grep '^OPENWEATHER_KEY=' ~/.shell3/.env | cut -d= -f2-)"
curl -fsS --max-time 15 \
  "https://api.openweathermap.org/data/2.5/weather?q=${city}&appid=${key}&units=metric"
```

## Hygiene

- `set -euo pipefail`; quote every expansion.
- `--max-time` (or a timeout) on every network call.
- Exit non-zero on failure so the caller can tell.
