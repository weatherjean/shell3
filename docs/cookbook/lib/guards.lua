-- cookbook: extra on_tool_call guards. Drop into ~/.shell3/lib/, require, and add
-- to an agent's on_tool_call = { ... }.

-- Block obviously destructive bash.
local function block_destructive_bash(call)
  if (call.tool or "") == "bash" then
    local cmd = tostring((call.params or {}).command or "")
    if cmd:match("rm%s+%-rf%s+/") or cmd:match("git%s+push%s+.-%-%-force") then
      return { action = "block", reason = "destructive command blocked by guard" }
    end
  end
  return { action = "allow" }
end

return { block_destructive_bash = block_destructive_bash }
