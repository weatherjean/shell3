---
name: cookbook
description: Install ready-made capabilities from the shell3 cookbook — web search, anti-bot browsing, review subagents, MCP/proxy/sandbox recipes. Use when the user asks for a capability you don't have, before writing a new skill from scratch
---

The shell3 repo ships a cookbook of drop-in recipes — skills, subagents, and
compose bundles you can install into this config. Before building a new
capability yourself (see the self-evolve skill), check whether a recipe
already exists.

## Browse the index

```bash
curl -fsS https://raw.githubusercontent.com/weatherjean/shell3/main/docs/cookbook/README.md
```

Each entry says what it does and where its files live; entries carry their
own install commands where extra files are needed.

## Install a recipe

Skills and subagents are single files — fetch them into the matching dir of
your config directory (the `config:` line of your Environment reminder;
`~/.shell3` below):

```bash
base=https://raw.githubusercontent.com/weatherjean/shell3/main/docs/cookbook
curl -fsS "$base/lib/skills/<name>.md"  -o ~/.shell3/skills/<name>.md
curl -fsS "$base/lib/agents/<name>.md"  -o ~/.shell3/agents/<name>.md
```

Some recipes ship extra assets under `lib/` (e.g. a docker compose bundle);
their README entry or skill body lists the exact files — fetch each the same
way into `~/.shell3/lib/...`.

## After installing

1. Skim what you installed (`cat` it) so you know what it does.
2. `shell3 health` — a malformed skill surfaces here.
3. Tell the user to `/reload` (or do it if you can) — new files load then.
4. If a recipe needs secrets or services (API keys, docker), its own setup
   section walks you through it — follow it with the user, don't guess.

Recipes are fetched from `main` — re-fetching later picks up upstream fixes.
