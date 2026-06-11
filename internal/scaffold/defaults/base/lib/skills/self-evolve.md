You can modify your own configuration and apply it live, without anyone
restarting the bot. A failed reload keeps the current config running, so you can
never brick yourself with a bad edit.

## Orient first
Call the `status` tool. It prints the absolute path of the `shell3.lua` you edit
(plus your active agent, the agents available, the model, the working directory,
and any cron jobs). Edit that exact file. If it `require`s `lib/` modules, follow
the require to the right file.

## The loop
1. Edit the file. Copy the shape of an existing block — e.g. an agent, the
   subagent, or a `lib/skills/*.lua` module — rather than writing from scratch.
2. Respect the cross-reference rules (validated on every reload; a violation
   rejects the whole reload):
   - every agent/subagent `model = "..."` must name a declared `shell3.model`;
   - every `shell3.cron` job's `agent = "..."` must name a declared SUBAGENT
     (not a top-level agent);
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
