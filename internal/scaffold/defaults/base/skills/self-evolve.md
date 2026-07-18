---
name: self-evolve
description: How to safely change your own shell3 config (shell3.yaml, agent.md, skills, hooks) — a bad edit never affects the running session.
---

You can modify your own configuration. Edits apply on `/reload` or the next
shell3 start — the running process keeps its current config, so a bad edit
never breaks the session you are in.

## Evolve proactively
Improving yourself is part of the job, not a special request. When you hit a
recurring friction, a wrong or stale instruction, or a capability you keep
wishing you had — fix it the moment you notice, don't wait to be asked. A skill
or instruction that drifts out of date is a liability; patch it in place.

## The config directory
Your config is a directory (the `config:` line of your Environment reminder):
prose lives in markdown files, wiring lives in `shell3.yaml`, a feature is on
because its file exists.

    shell3.yaml        wiring: models, telegram/web, mcp servers, media
    agent.md           you: frontmatter (model, tools) + your prompt as the body
    agents/<name>.md   one subagent per file (description + prompt)
    skills/<name>.md   one skill per file
    hooks/*.sh         tool-call/-result gate scripts (bash, JSON in/out)
    cron/<name>.md     scheduled jobs (schedule + agent + prompt body)
    heartbeat.md       periodic check-in checklist

## Footprint ladder — pick the smallest change that works
Choose the highest (least-footprint) rung that correctly solves the problem:
1. Edit an existing skill `.md` or an agent prompt — sharpen what's already there.
2. Add a new skill: write a `.md` into `skills/`. This is the default home for
   a new procedure:

       skills/greet.md
       ---
       description: Greet the user warmly when a conversation starts.
       ---
       When greeting, use the user's name if you know it...

3. Add a wrapper script under `~/.shell3/lib/bin/` (see the scripting skill) —
   when a bash workflow or a secret-using API call is genuinely reusable.
4. Add or adjust a subagent (`agents/<name>.md`) — when the work needs its own
   prompt, toolset, or an isolated context to delegate to.
5. A Go/core change — last resort; it needs a rebuild. Describe what's needed
   and hand it to the user.

## The loop
1. Orient: your config directory is on the `config:` line of your Environment
   reminder. Edit files inside that exact directory.
2. Edit, copying the shape of an existing block or file.
3. Validate without touching the live session:
     shell3 health --config <that directory>
   It fails on what the loader would only warn about — e.g. a skill `.md` it
   skipped for missing/broken frontmatter, or a hook file naming no subagent.
4. Fix what health reports and re-run until clean (a `skill file ... skipped`
   warning means the `.md` needs a frontmatter `description` and a body).
5. Tell the user the change is ready — it goes live on `/reload` or the next
   shell3 start. Do not promise it is live in the current session.
