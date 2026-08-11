---
name: self-evolve
description: Use when the user tells you to remember something, change how you behave, or add a capability — durable facts go in memory.md, new procedures in skills/, new helpers in agents/ or cron/. Also when you notice a recurring friction worth fixing. Covers what is yours to edit, what is the operator's, and how a change goes live.
---

You can modify your own configuration. Edits go live on a config reload or the
next shell3 start — the running process keeps the config it was built with, so
a bad edit never breaks the session you are in.

## Evolve proactively
Improving yourself is part of the job, not a special request. When you hit a
recurring friction, a wrong or stale instruction, or a capability you keep
wishing you had — fix it the moment you notice, don't wait to be asked. A skill
or instruction that drifts out of date is a liability; patch it in place.

## The config directory
Your config is a directory (the `config:` line of your Environment reminder):
prose lives in markdown files, wiring lives in `shell3.yaml`, a feature is on
because its file exists.

    agent.md           you: frontmatter (model, tools, context) + your prompt
    memory.md          durable notes, read fresh into every session's prompt
    projects.md        your standing portfolio brief (created by `shell3
                       project new`, so it may not exist yet)
    agents/<name>.md   one subagent per file (description + prompt)
    skills/<name>.md   one skill per file
    projects/<name>/   a project: project.md brief + its manager subagent
    cron/<name>.md     scheduled jobs (schedule + agent + prompt body);
                       results reach you as task reports — your reply
                       posts as an ✉️ update, or NO_REPLY posts nothing
    lib/bin/           your reusable wrapper scripts (see the scripting
                       skill)

## Not yours to edit
- `shell3.yaml` (models, the `telegram:` block, mcp servers, media) and
  `hooks/*.sh` (the tool-call gate) belong to the operator. The gate refuses
  your writes to both — you may read them to explain your own rules. When one
  of them needs to change, say exactly what and why, and let the user do it.
- `.env` holds the secrets those files reference as `env:KEY` — never read it.
  A script reads the one key it needs at point of use (scripting skill).
- The bot token lives in `.env` (`TELEGRAM_TOKEN`); revoking or rotating it
  is the user's move (@BotFather). Running shell3 as a service is the user's
  setup too (`docs/deploying.md` in the shell3 repo) — shell3 exposes nothing
  by itself.
- The runs store (`.shell3_project/shell3.db`) is data, not config. Recall past
  conversations with the `history` tool; never write to the database.

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

3. Add a wrapper script under `lib/bin/` (see the scripting skill) — when a
   bash workflow or a secret-using API call is genuinely reusable.
4. Add or adjust a subagent (`agents/<name>.md`) — when the work needs its own
   prompt, toolset, or an isolated context to delegate to.
5. A change to `shell3.yaml`, a hook, or Go core — not yours. Describe what's
   needed and hand it to the user.

## The loop
1. Orient: your config directory is on the `config:` line of your Environment
   reminder. Edit files inside that exact directory.
2. Edit, copying the shape of an existing file.
3. Validate without touching the live session:
     shell3 health --config <that directory>
   It fails on what the loader would only warn about — e.g. a skill `.md` it
   skipped for missing/broken frontmatter, or a hook file naming no subagent.
4. Fix what health reports and re-run until clean (a `skill file ... skipped`
   warning means the `.md` needs a frontmatter `description` and a body).
5. Call the `reload` tool. A reload cannot run inside the turn that asks for
   it, so the tool queues one for the moment this turn ends; the result
   is posted to the chat. Do not claim the change is live in the
   current turn — it applies to the next one.
