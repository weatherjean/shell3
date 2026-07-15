---
name: self-evolve
description: How to safely change your own shell3.lua config or skills — a bad edit never affects the running session.
---

You can modify your own configuration. Edits apply on `/reload` or the next
shell3 start — the running process keeps its current config, so a bad edit
never breaks the session you are in.

## Evolve proactively
Improving yourself is part of the job, not a special request. When you hit a
recurring friction, a wrong or stale instruction, or a capability you keep
wishing you had — fix it the moment you notice, don't wait to be asked. A skill
or instruction that drifts out of date is a liability; patch it in place.

## Footprint ladder — pick the smallest change that works
Choose the highest (least-footprint) rung that correctly solves the problem:
1. Edit an existing skill `.md` or an agent's prompt — sharpen what's already there.
2. Add a new skill: write a `.md` into a directory the agent lists under
   `skills = { ... }` (usually `lib/skills/`). No Lua change needed. This is
   the default home for a new procedure:

       lib/skills/greet.md
       ---
       description: Greet the user warmly when a conversation starts.
       ---
       When greeting, use the user's name if you know it...

3. Add a declarative custom tool (`shell3.tool{command=...}`) — only when a
   bash-command template with injected params is genuinely reusable.
4. Add or adjust an agent / subagent — when the work needs its own prompt,
   toolset, or an isolated context to delegate to.
5. A Go/core change — last resort; it needs a rebuild. Describe what's needed
   and hand it to the user.

## The loop
1. Orient: your `shell3.lua` path is on the `config:` line of the
   `## Environment` section of your system prompt. Edit that exact file (or a
   `lib/` module it requires, or a skill `.md` in a granted skills dir).
2. Edit, copying the shape of an existing block or skill file.
3. Validate without touching the live session:
     shell3 health --config <that shell3.lua>
   It fails on what the loader would only warn about — e.g. a skill `.md` it
   skipped for missing/broken frontmatter.
4. Fix what health reports and re-run until clean (a `skill file ... skipped`
   warning means the `.md` needs a frontmatter `description` and a body).
5. Tell the user the change is ready — it goes live on `/reload` or the next
   shell3 start. Do not promise it is live in the current session.
