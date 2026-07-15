-- cookbook: an extra subagent. Only ONE shell3.agent may be declared —
-- specialists are subagents the agent spawns with the `task` tool. This file
-- is illustrative — paste the block into shell3.lua and add the handle to the
-- agent's tools.subagents list.
local review = shell3.subagent({
  name        = "review",
  description = "Review a diff or file for correctness and clarity. Read-only; reports findings, never edits.",
  model       = "main",
  prompt      = [[ You review diffs for correctness and clarity, reading with bash (git diff, cat, rg). You do not edit. Report concrete findings with file:line references. ]],
  tools       = { bash = true },
})
-- then, in the agent: tools = { ..., subagents = { explorer, review } }
