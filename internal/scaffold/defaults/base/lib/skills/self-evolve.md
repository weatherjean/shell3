You can modify your own configuration and apply it live, without anyone
restarting the bot. A failed reload keeps the current config running, so you can
never brick yourself with a bad edit.

## Evolve proactively
Improving yourself is part of the job, not a special request. When you hit a
recurring friction, a wrong or stale instruction, or a capability you keep
wishing you had — fix it the moment you notice, don't wait to be asked. A skill
or instruction that drifts out of date is a liability; patch it in place.

## Footprint ladder — pick the smallest change that works
Each rung adds more permanent surface than the one above. Choose the highest
(least-footprint) rung that correctly solves the problem:
1. Edit an existing skill `.md` or an agent's prompt — sharpen what's already there.
2. Add a new skill `.md` (a file you `cat`) and grant it to the agent — see the
   worked example below. This is the default home for a new procedure.
3. Add a declarative custom tool (`shell3.tool{command=...}`) — only when a
   bash-command template with injected params is genuinely reusable.
4. Add or adjust an agent / subagent — when the work needs its own prompt,
   toolset, or an isolated context to delegate to.
5. A Go/core change — last resort; it needs a rebuild and can't be hot-reloaded.
   Don't attempt it live; describe what's needed and hand it to the user.

## Orient first
Your `shell3.lua` path is in the `## Environment` section of your system prompt
(the `config:` line). Edit that exact file. If it `require`s `lib/` modules,
follow the require to the right file.

## The loop
1. Edit the file. Copy the shape of an existing block — e.g. an agent, the
   subagent, or a `lib/skills/*.lua` module — rather than writing from scratch.
2. Respect the cross-reference rules (validated on every reload; a violation
   rejects the whole reload):
   - every agent/subagent `model = "..."` must name a declared `shell3.model`;
   - every cron job (in `shell3.telegram{ cron = {...} }`) must set
     `agent = "..."` to a declared SUBAGENT (not a top-level agent);
   - a skill or tool granted to an agent must be a declared handle.
3. Call the `reload` tool. It validates the whole file, then applies it after
   this turn ends. It acknowledges immediately; the validated result — success
   counts or the exact error — is delivered to the chat the moment this turn
   finishes. You always see the error.
4. If it failed, fix by error type:
   - a Lua error like `[string "shell3.lua"]:42: ...` is a syntax/typo at that
     line — fix the line;
   - a validation error like `unknown subagent "x"` / `unknown model "y"` is a
     bad cross-reference — point it at a declared name.
   The old config keeps running until a reload succeeds, so just edit and retry.

## Worked example — add a skill and grant it
  -- write the skill body to a file, e.g. lib/skills/greet.md
  local greet = shell3.skill({ name = "greet",
    description = "Greet the user warmly", path = "lib/skills/greet.md" })
  -- then add the handle to an agent's skills list: skills = { greet },
Edit, call `reload`; on success the skill is live for that agent's next turn.

## What survives a reload
- Conversation history is always kept — reload never clears the chat.
- Active agent and /set params are restored when they still exist in the new
  config. If your edit REMOVES the agent you are currently using, reload does NOT
  error: it falls back to the configured default agent and says so in the result.
- Model proxies restart on reload (a brief pause); agents,
  models, tools, skills, and cron apply cleanly.
- A changed `context_window` for the already-live session takes effect on the
  next restart, not on this reload.
