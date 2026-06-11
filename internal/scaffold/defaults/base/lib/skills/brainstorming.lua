-- lib/skills/brainstorming.lua — design-first skill for the plan agent. The
-- body lives in brainstorming.md (next to this file); the agent reads it with
-- `cat`. Returned for require().
return shell3.skill({
  name        = "brainstorming",
  description = "Turn a rough idea into an agreed design through one-question-at-a-time dialogue, then write a saved design doc. Use before any non-trivial feature, behavior change, or new component.",
  path        = "lib/skills/brainstorming.md",
})
