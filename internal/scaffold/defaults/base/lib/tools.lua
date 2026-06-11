-- lib/tools.lua — example custom tools as bash command templates.
-- Params are exported into the command env by their (lowercase) name; declared
-- secrets are exported too (and kept out of the command string). Returns
-- { web_fetch, brave_search } for require().

local web_fetch = shell3.tool({
  name        = "web_fetch",
  description = "Fetch a URL and return its plain-text content (tags stripped) plus a list of links.",
  parameters  = {
    type = "object",
    properties = { url = { type = "string", description = "The URL to fetch." } },
    required = { "url" },
  },
  command = [[
curl -sfL --max-time 15 "$url" | python3 - "$url" <<'PY'
import sys, re, html
url = sys.argv[1]
data = sys.stdin.read()
links = sorted(set(re.findall(r'href="(https?://[^"]+)"', data)))
text = re.sub(r'(?is)<(script|style)[^>]*>.*?</\1>', ' ', data)
text = re.sub(r'(?s)<!--.*?-->', ' ', text)
text = re.sub(r'(?s)<[^>]+>', ' ', text)
text = html.unescape(text)
text = re.sub(r'\s+', ' ', text).strip()
print("URL:", url)
print()
print(text)
if links:
    print()
    print("Links:")
    print("\n".join(links))
PY
]],
})

local brave_search = shell3.tool({
  name        = "brave_search",
  description = "Search the web via Brave Search; returns titles, URLs, and snippets.",
  parameters  = {
    type = "object",
    properties = {
      query = { type = "string",  description = "The search query." },
      count = { type = "integer", description = "Results to return (1-20, default 10)." },
    },
    required = { "query" },
  },
  secrets = { "BRAVE_API_KEY" },
  command = [[
curl -sf -G "https://api.search.brave.com/res/v1/web/search" \
  -H "Accept: application/json" \
  -H "X-Subscription-Token: $BRAVE_API_KEY" \
  --data-urlencode "q=$query" --data "count=${count:-10}" \
| jq -r '.web.results[]? | .title + "\n" + .url + "\n" + (.description // "") + "\n---"'
]],
})

return { web_fetch = web_fetch, brave_search = brave_search }
