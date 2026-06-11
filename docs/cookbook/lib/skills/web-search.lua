-- cookbook: drop into ~/.shell3/lib/skills/ then require("lib.skills.web-search").
-- The body lives in web-search.md (next to this file); the agent reads it with `cat`.
return shell3.skill({
  name        = "web-search",
  description = "Use Brave Search and page fetching for current, external, or source-grounded information.",
  path        = "lib/skills/web-search.md",
})
