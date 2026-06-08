-- cookbook: a custom tool template. Drop into ~/.shell3/lib/, require, add to an
-- agent's tools = { custom = { my_tool } }.
local my_tool = shell3.tool({
  name        = "my_tool",
  description = "What this tool does.",
  parameters  = {
    type = "object",
    properties = { arg = { type = "string", description = "An argument." } },
    required = { "arg" },
  },
  handler = function(args)
    return "you passed: " .. tostring(args.arg or "")
  end,
})

return { my_tool = my_tool }
