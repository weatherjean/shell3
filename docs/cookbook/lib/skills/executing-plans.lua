-- cookbook: drop into ~/.shell3/lib/skills/ then require("lib.skills.executing-plans").
-- The body lives in executing-plans.md (next to this file); the agent reads it with `cat`.
return shell3.skill({
  name        = "executing-plans",
  description = "Execute approved plans with safe git workflow, scoped commits, and validation.",
  path        = "lib/skills/executing-plans.md",
})
