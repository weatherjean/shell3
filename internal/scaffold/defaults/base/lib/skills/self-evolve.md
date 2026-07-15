---
name: self-evolve
description: How to safely change your own shell3.lua config or skills. Edit, validate with shell3 health plus a throwaway headless run, and the change applies on /reload or the next shell3 start. The running session is never affected by a bad edit.
---

You can modify your own configuration. Edits apply on `/reload` or the next
shell3 start — the running process keeps its current config, so a bad edit
never breaks the session you are in; it would only surface at the next launch,
and you can validate before that (see the loop below).

## Evolve proactively
Improving yourself is part of the job, not a special request. When you hit a
recurring friction, a wrong or stale instruction, or a capability you keep
wishing you had — fix it the moment you notice, don't wait to be asked. A skill
or instruction that drifts out of date is a liability; patch it in place.

## Footprint ladder — pick the smallest change that works
Each rung adds more permanent surface than the one above. Choose the highest
(least-footprint) rung that correctly solves the problem:
1. Edit an existing skill `.md` or an agent's prompt — sharpen what's already there.
2. Add a new skill: write a `.md` into a directory the agent lists under
   `skills = { ... }` (usually `lib/skills/`) — see the worked example below.
   No Lua change needed. This is the default home for a new procedure.
3. Add a declarative custom tool (`shell3.tool{command=...}`) — only when a
   bash-command template with injected params is genuinely reusable.
4. Add or adjust an agent / subagent — when the work needs its own prompt,
   toolset, or an isolated context to delegate to.
5. A Go/core change — last resort; it needs a rebuild. Describe what's needed
   and hand it to the user.

## Orient first
Your `shell3.lua` path is in the `## Environment` section of your system prompt
(the `config:` line). Edit that exact file. If it `require`s `lib/` modules,
follow the require to the right file. Your granted skills are the `*.md` files
in the directories your agent lists under `skills = { ... }`.

## The loop
1. Edit the file. Copy the shape of an existing block — e.g. an agent, the
   subagent, or an existing `lib/skills/*.md` — rather than writing from scratch.
2. Respect the cross-reference rules (validated at load; a violation fails the
   next start):
   - every agent/subagent `model = "..."` must name a declared `shell3.model`;
   - a cron job `agent = "..."` must reference a declared subagent;
   - every directory in `skills = { ... }` must exist.
3. Validate without touching the live session:
     shell3 health --config <that shell3.lua>
   It fails on anything the loader would only warn about — most importantly a
   skill `.md` it had to skip (missing/broken frontmatter). For a full
   end-to-end check, follow with a throwaway one-shot run:
     shell3 dev --config <that shell3.lua> "reply with just: ok"
4. If it failed, fix by error type:
   - a Lua error like `[string "shell3.lua"]:42: ...` is a syntax/typo at that
     line — fix the line;
   - a validation error like `unknown subagent "x"` / `unknown model "y"` is a
     bad cross-reference — point it at a declared name;
   - a `skill file ... skipped` warning means the `.md` needs frontmatter with
     a `description` and a non-empty body.
   The running process is unaffected either way — edit and re-validate.
5. Tell the user the change is ready — it goes live on `/reload` or the next
   time they start shell3. Do not promise it is live in the current session.

## Worked example — add a skill
Write the skill body to a new file in a granted skills dir, with frontmatter:

    lib/skills/greet.md
    ---
    description: Greet the user warmly when a conversation starts.
    ---
    When greeting, use the user's name if you know it...

Validate with `shell3 health`; the skill is live for every agent listing that
directory from the next `/reload` or shell3 start.
