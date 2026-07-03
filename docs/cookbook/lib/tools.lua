-- cookbook: a custom tool template. Drop into ~/.shell3/lib/, require, add to an
-- agent's tools = { custom = { my_tool } }.
--
-- A custom tool is a bash command template, not a Lua function. Declared params
-- are exported into the command env by their (lowercase) name; declared secrets
-- are exported too (and kept out of the command string). Optionally set
-- background = true (an in-process background job, like bash_bg — the agent
-- gets a completion notice) and timeout = N (seconds, foreground only).

-- A trivial template: params arrive as $-named env vars.
local my_tool = shell3.tool({
  name        = "my_tool",
  description = "What this tool does.",
  parameters  = {
    type = "object",
    properties = { arg = { type = "string", description = "An argument." } },
    required = { "arg" },
  },
  command = [[
echo "you passed: $arg"
]],
})

-- A tool that calls an API with a secret. The secret is exported into the
-- command env (as $WEATHER_API_KEY) but never interpolated into the string;
-- use curl --data-urlencode for user-supplied params, and jq to shape output.
local weather = shell3.tool({
  name        = "weather",
  description = "Fetch the current weather for a city.",
  parameters  = {
    type = "object",
    properties = { city = { type = "string", description = "City name." } },
    required = { "city" },
  },
  secrets = { "WEATHER_API_KEY" },
  command = [[
curl -sf -G "https://api.example.com/v1/current" \
  -H "Authorization: Bearer $WEATHER_API_KEY" \
  --data-urlencode "q=$city" \
| jq -r '.location.name + ": " + (.current.temp_c|tostring) + "°C, " + .current.condition'
]],
})

return { my_tool = my_tool, weather = weather }
