-- cookbook: drop into ~/.shell3/lib/skills/ then require("lib.skills.writing-plans").
-- The body lives in writing-plans.md (next to this file); the agent reads it with `cat`.
return shell3.skill({
  name        = "writing-plans",
  description = "Mandatory planning and approval gate before any non-trivial code, config, or docs change.",
  path        = "lib/skills/writing-plans.md",
})
