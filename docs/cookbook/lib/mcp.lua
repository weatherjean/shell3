-- cookbook: declaring an MCP server. Drop into ~/.shell3/lib/, require it in
-- shell3.lua, and add `mcp = { chrome }` to an agent's tools block.
local chrome = shell3.mcp({
  name    = "chrome",
  command = "npx",
  args    = { "-y", "chrome-devtools-mcp@latest", "--autoConnect", "--no-usage-statistics" },
  -- tools = { "navigate_page", "click", "take_snapshot" }, -- optional allowlist
})

return { chrome = chrome }
