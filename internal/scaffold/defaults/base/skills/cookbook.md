---
name: cookbook
description: Ready-made capabilities from the shell3 cookbook — web search, anti-bot browsing, review subagents, MCP/proxy/sandbox recipes. Check here FIRST whenever the user asks to set up, add, or use any capability you don't already have, before designing a solution or writing a skill from scratch
---

The shell3 repo ships a cookbook of drop-in recipes — skills, subagents, and
compose bundles you can install into this config. When the user asks for a
capability you don't have ("set up web search", "can you browse X", "add a
reviewer"), your FIRST move is this index — before asking scoping questions,
before designing anything yourself (see the self-evolve skill for genuinely
new capabilities).

## Browse the index

```bash
curl -fsS https://raw.githubusercontent.com/weatherjean/shell3/main/docs/cookbook/README.md
```

Each entry says what it does and where its files live; entries carry their
own install commands where extra files are needed.

## Before installing: confirm

Installing a recipe changes this config and can pull in real infrastructure.
Once you've found the recipe, tell the user in one short message what it
would install — the files, and any services it stands up (docker images,
containers, ports, disk) — and get a yes before fetching anything. The user
asking "can you do X?" is a question, not an install order. Skip the ask only
when they've already named the recipe and told you to install it.

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
