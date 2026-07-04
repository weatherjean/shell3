You can modify your own configuration. Edits apply on the next shell3 start —
the running process keeps its current config, so a bad edit never breaks the
session you are in; it would only surface at the next launch, and you can
validate before that (see the loop below).

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
5. A Go/core change — last resort; it needs a rebuild. Describe what's needed
   and hand it to the user.

## Orient first
Your `shell3.lua` path is in the `## Environment` section of your system prompt
(the `config:` line). Edit that exact file. If it `require`s `lib/` modules,
follow the require to the right file.

## The loop
1. Edit the file. Copy the shape of an existing block — e.g. an agent, the
   subagent, or a `lib/skills/*.lua` module — rather than writing from scratch.
2. Respect the cross-reference rules (validated at load; a violation fails the
   next start):
   - every agent/subagent `model = "..."` must name a declared `shell3.model`;
   - a skill or tool `agent = "..."` must reference a declared subagent;
   - a skill or tool granted to an agent must be a declared handle.
3. Validate the edit without touching the live session by loading the config in
   a throwaway headless run:
     shell3 run --config <that shell3.lua> --prompt "reply with just: ok"
   A clean reply means the file loads; a Lua or validation error prints instead.
4. If it failed, fix by error type:
   - a Lua error like `[string "shell3.lua"]:42: ...` is a syntax/typo at that
     line — fix the line;
   - a validation error like `unknown subagent "x"` / `unknown model "y"` is a
     bad cross-reference — point it at a declared name.
   The running process is unaffected either way — edit and re-validate.
5. Tell the user the change is ready and takes effect the next time they start
   shell3. Do not promise it is live in the current session.

## Worked example — add a skill and grant it
  -- write the skill body to a file, e.g. lib/skills/greet.md
  local greet = shell3.skill({ name = "greet",
    description = "Greet the user warmly", path = "lib/skills/greet.md" })
  -- then add the handle to an agent's skills list: skills = { greet },
Validate with the headless run above; the skill is live for that agent from the
next shell3 start.
