-- lib/skills/history.lua — read past conversations from the SQLite store via
-- bash + a read-only sqlite3 connection. The body lives in history.md (next to
-- this file); the agent reads it with `cat`. Returned for require().
return shell3.skill({
  name        = "history",
  description = "Recall past conversations: read the most-recent session or full-text-search every prior session. Use for references like \"last time\", \"earlier\", or \"what did we decide about X\". Read-only — query the store with bash, never edit it.",
  path        = "lib/skills/history.md",
})
