-- lib/skills/history.lua — search and read past conversations via rg over
-- .shell3_project/runs/ and `shell3 read-session`. The body lives in history.md
-- (next to this file); the agent reads it with `cat`. Returned for require().
return shell3.skill({
  name        = "history",
  description = "Recall past conversations: read the most-recent session or full-text-search every prior session. Use for references like \"last time\", \"earlier\", or \"what did we decide about X\". Read-only — query the store with bash, never edit it.",
  path        = "lib/skills/history.md",
})
