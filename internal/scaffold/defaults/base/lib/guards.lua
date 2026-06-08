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

return { no_env_edit = no_env_edit }
