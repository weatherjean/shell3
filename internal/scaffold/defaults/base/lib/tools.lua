-- lib/tools.lua — example custom tools. Returned for require() in shell3.lua.
local web_fetch = shell3.tool({
  name        = "web_fetch",
  description = "Fetch a URL and return its plain-text content (tags stripped) and a list of links.",
  parameters  = {
    type       = "object",
    properties = {
      url = {
        type        = "string",
        description = "The URL to fetch.",
      },
    },
    required = { "url" },
  },
  handler = function(args)
    local url = args.url or ""
    if url == "" then return "error: url is required" end

    local res, err = shell3.http.get(url, { timeout = 15, max_bytes = 524288 })
    if err then return "error fetching " .. url .. ": " .. tostring(err) end
    if res.status ~= 200 then
      return "HTTP " .. tostring(res.status) .. " fetching " .. url
    end

    local body = res.body or ""

    -- Strip HTML tags.
    local text = body:gsub("<style[^>]*>.-</style>", " ")
                     :gsub("<script[^>]*>.-</script>", " ")
                     :gsub("<!--.--->", " ")
                     :gsub("<[^>]+>", " ")
                     :gsub("&nbsp;", " ")
                     :gsub("&amp;", "&")
                     :gsub("&lt;", "<")
                     :gsub("&gt;", ">")
                     :gsub("&quot;", '"')
                     :gsub("%s+", " ")
                     :match("^%s*(.-)%s*$")

    -- Extract links.
    local links = {}
    for href in body:gmatch('href="(https?://[^"]+)"') do
      links[#links + 1] = href
    end
    -- Deduplicate.
    local seen = {}
    local uniq = {}
    for _, l in ipairs(links) do
      if not seen[l] then seen[l] = true; uniq[#uniq + 1] = l end
    end

    local out = "URL: " .. url .. "\n\n" .. text
    if #uniq > 0 then
      out = out .. "\n\nLinks:\n" .. table.concat(uniq, "\n")
    end
    return out
  end,
})

local brave_search = shell3.tool({
  name        = "brave_search",
  description = "Search the web using Brave Search API; returns titles, URLs, and snippets.",
  parameters  = {
    type       = "object",
    properties = {
      query = {
        type        = "string",
        description = "The search query.",
      },
      count = {
        type        = "integer",
        description = "Number of results to return (1-20, default 10).",
      },
    },
    required = { "query" },
  },
  handler = function(args)
    local query = args.query or ""
    if query == "" then return "error: query is required" end
    local count = tostring(args.count or 10)

    local key = shell3.env.secret("BRAVE_API_KEY")
    if key == "" then return "error: brave_search is unavailable (no API key configured)" end

    local encoded = shell3.urlencode(query)
    local cmd = string.format(
      "curl -sf -H 'Accept: application/json' -H 'X-Subscription-Token: %s' " ..
      "'https://api.search.brave.com/res/v1/web/search?q=%s&count=%s' " ..
      "| jq -r '.web.results[]? | .title + \"\\n\" + .url + \"\\n\" + (.description // \"\") + \"\\n---\"'",
      key, encoded, count
    )
    local result = shell3.bash(cmd, { timeout = 20 })
    if result.exit ~= 0 then
      return "search error (exit " .. tostring(result.exit) .. "): " .. (result.stderr or "")
    end
    return result.stdout or "(no results)"
  end,
})

return { web_fetch = web_fetch, brave_search = brave_search }
