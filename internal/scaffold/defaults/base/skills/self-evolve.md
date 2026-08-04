---
name: self-evolve
description: How to safely change your own shell3 config (agent.md, skills, subagents, cron) — what is yours to edit, what is the operator's, and how a change goes live.
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
    projects.md        your standing portfolio brief
    agents/<name>.md   one subagent per file (description + prompt)
    skills/<name>.md   one skill per file
    projects/<name>/   a project: project.md brief + its manager subagent
    cron/<name>.md     scheduled jobs (schedule + agent + prompt body);
                       results go to notifier.md, which stays silent when
                       there's nothing worth reporting
    notifier.md        the completion-triage persona — edit its body to
                       change what gets posted vs. silenced
    lib/bin/           your reusable wrapper scripts (see the scripting skill)

## Not yours to edit
- `shell3.yaml` (models, the `web:` block, mcp servers, media) and
  `hooks/*.sh` (the tool-call gate) belong to the operator. The gate refuses
  your writes to both — you may read them to explain your own rules. When one
  of them needs to change, say exactly what and why, and let the user do it.
- `.env` holds the secrets those files reference as `env:KEY` — never read it.
  A script reads the one key it needs at point of use (scripting skill).
- The web password and the optional second factor live in `.env`;
  `shell3 boot --totp` enrols or resets the factor, and it is the user's
  command to run. How this interface is reached from elsewhere is the user's
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
5. Tell the user the change is ready and ask them to reload: the Status view
   has a **Reload config** button. You cannot reload yourself — a reload is
   refused while a turn is running, which is exactly the turn you are in. Do
   not claim the change is live in the current session.
