# MCP servers

Recipes for `shell3.mcp{}` — the tools-only MCP client (stdio + streamable
HTTP, official Go SDK). Reference:
[../configuration.md#mcp-servers--shell3mcp](../configuration.md#mcp-servers--shell3mcp).

## GitHub over stdio, key from `.env`

Install the server binary (`brew install github-mcp-server` or grab a
release), put `GITHUB_TOKEN=ghp_…` in `~/.shell3/.env`, then:

```lua
shell3.mcp({
  github = {
    command = { "github-mcp-server", "stdio" },
    env     = { GITHUB_PERSONAL_ACCESS_TOKEN = shell3.env.secret("GITHUB_TOKEN") },
    -- Trim the surface: this server ships dozens of tools, and every one
    -- costs schema tokens each turn. Allow only what you use.
    allow   = { "search_issues", "get_issue", "list_pull_requests", "get_pull_request" },
  },
})

shell3.agent({
  -- ...
  tools = { bash = true, edit = true, mcp = { "github" } },
})
```

The secret goes into the server child's environment only — it never appears
in the conversation or the agent's own environment.

## A remote server over streamable HTTP

```lua
shell3.mcp({
  linear = {
    url     = "https://mcp.linear.app/mcp",
    headers = { Authorization = "Bearer " .. shell3.env.secret("LINEAR_API_KEY") },
    timeout = 30,
  },
})
```

There is no OAuth flow: if the service only does OAuth, mint a long-lived
token in its settings UI and paste that into `.env`.

## npx-packaged servers

Anything on npm runs without installing:

```lua
shell3.mcp({
  everything = { command = { "npx", "-y", "@modelcontextprotocol/server-everything" } },
})
```

First connect pays the npx download; raise `timeout` if the default 10s is
too tight on a cold cache.

## Gate MCP calls like any other tool

MCP tools hit the `on_tool_call` chain with `t.name = "mcp_<server>_<tool>"`
and `t.command = nil`:

```lua
local MCP_WRITE = shell3.regex([[^mcp_github_(create|update|delete|merge)]])
shell3.on_tool_call(function(t)
  if MCP_WRITE:match(t.name) then
    if t.headless then return { block = true, reason = "write needs approval; rerun interactively" } end
    return { ask = "GitHub write:\n" .. t.name .. "\n" .. t.args, reason = "denied" }
  end
end)
```

## Checking and troubleshooting

- `shell3 health` connects every declared server and fails on any that is
  down, printing per-server tool counts.
- The dashboard's **Status** view lists each server: up/down, tool count,
  last error.
- A server that is down at startup is a warning, not a failure — the bot
  runs, that server's tools are absent until the next `/reload`.
- A server that dies mid-session gets one automatic reconnect at the next
  call; after that the model sees the error text as tool output.
