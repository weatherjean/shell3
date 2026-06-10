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

-- Ask the human before a risky-but-sometimes-legitimate command, instead of
-- blocking it outright. action="ask" suspends the call until the front-end
-- answers (TUI inline y/N; bot Approve/Deny). With no approver (headless), ask
-- fails closed. The scaffold ships a confirm_destructive guard in this shape.
local function ask_before_push(call)
  if (call.tool or "") == "bash" then
    local cmd = tostring((call.params or {}).command or "")
    if cmd:match("git%s+push") then
      return { action = "ask", reason = "pushing to a remote — confirm" }
    end
  end
  return { action = "allow" }
end

return {
  block_destructive_bash = block_destructive_bash,
  ask_before_push = ask_before_push,
}
