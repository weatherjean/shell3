-- lib/guards.lua — on_tool_call guards. Returned for require() in shell3.lua.
local function no_env_edit(call)
  local tool   = call.tool or ""
  local params = call.params or {}
  if tool == "edit_file" then
    local path = tostring(params.file_path or "")
    if path:match("%.env$") then
      return { action = "block", reason = "editing .env files is not allowed; manage secrets manually" }
    end
  end
  return { action = "allow" }
end

-- confirm_destructive asks the human before running obviously destructive bash
-- (recursive force-remove, force-push). Returning action="ask" suspends the
-- call until the front-end answers: the TUI shows an inline y/N prompt, a bot
-- shows Approve/Deny. With no approver wired (headless), ask fails closed (deny).
local function confirm_destructive(call)
  if (call.tool or "") == "bash" then
    local cmd = tostring((call.params or {}).command or "")
    if cmd:match("rm%s+%-rf") or cmd:match("git%s+push%s+.-%-%-force") then
      return { action = "ask", reason = "destructive command — confirm before running" }
    end
  end
  return { action = "allow" }
end

return { no_env_edit = no_env_edit, confirm_destructive = confirm_destructive }
