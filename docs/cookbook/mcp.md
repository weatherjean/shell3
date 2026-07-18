# MCP servers

Recipes for the `mcp:` block — the tools-only MCP client (stdio + streamable
HTTP, official Go SDK). Reference:
[../configuration.md#mcp-servers](../configuration.md#mcp-servers).

## GitHub over stdio, key from `.env`

Install the server binary (`brew install github-mcp-server` or grab a
release), put `GITHUB_TOKEN=ghp_…` in `~/.shell3/.env`, then in `shell3.yaml`:

```yaml
mcp:
  github:
    command: [github-mcp-server, stdio]
    env: { GITHUB_PERSONAL_ACCESS_TOKEN: env:GITHUB_TOKEN }
    # Trim the surface: this server ships dozens of tools, and every one
    # costs schema tokens each turn. Allow only what you use.
    allow: [search_issues, get_issue, list_pull_requests, get_pull_request]
```

and opt the agent in (`agent.md` frontmatter):

```markdown
---
model: main
tools: [bash, edit]
mcp: [github]
---
```

The secret goes into the server child's environment only — it never appears
in the conversation or the agent's own environment.

## A remote server over streamable HTTP

```yaml
mcp:
  linear:
    url: https://mcp.linear.app/mcp
    headers: { Authorization: "Bearer env:LINEAR_API_KEY" }
    timeout: 30
```

There is no OAuth flow: if the service only does OAuth, mint a long-lived
token in its settings UI and paste that into `.env`.

## npx-packaged servers

Anything on npm runs without installing:

```yaml
mcp:
  everything:
    command: [npx, -y, "@modelcontextprotocol/server-everything"]
```

First connect pays the npx download; raise `timeout` if the default 10s is
too tight on a cold cache.

## Gate MCP calls like any other tool

MCP tools hit the tool-call hook with `name` = `mcp_<server>_<tool>` and
`command` null — gate them by name in `hooks/tool-call.sh`:

```bash
in=$(cat)
name=$(printf '%s' "$in" | jq -r .name)
headless=$(printf '%s' "$in" | jq -r .headless)
case "$name" in
  mcp_github_create*|mcp_github_update*|mcp_github_delete*|mcp_github_merge*)
    if [ "$headless" = "true" ]; then
      printf '{"block": true, "reason": "write needs approval; rerun interactively"}'
    else
      printf '{"ask": "GitHub write:\n%s", "reason": "denied"}' "$name"
    fi
    exit 0 ;;
esac
exit 0
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
