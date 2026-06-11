-- cookbook: a third agent. Declare additional agents in shell3.lua; switch with
-- Tab (when idle) or /agent. This file is illustrative — paste the block into
-- shell3.lua where `tools`, `guards`, and skills locals are in scope.
shell3.agent({
  name   = "review",
  model  = "main",
  prompt = [[ You review diffs for correctness and clarity. You do not edit. ]],
  tools  = { bash = true, edit = false, prune = true, compact = true },
})
