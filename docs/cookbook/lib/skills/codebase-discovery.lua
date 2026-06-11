-- cookbook: drop into ~/.shell3/lib/skills/ then require("lib.skills.codebase-discovery").
-- The body lives in codebase-discovery.md (next to this file); the agent reads it with `cat`.
return shell3.skill({
  name        = "codebase-discovery",
  description = "Discover relevant code fast via broad search then aggressive context pruning.",
  path        = "lib/skills/codebase-discovery.md",
})
