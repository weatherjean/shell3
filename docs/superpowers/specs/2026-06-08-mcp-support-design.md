# MCP client support for shell3

**Status:** Approved design
**Date:** 2026-06-08

## Goal

Let shell3 act as an MCP (Model Context Protocol) **client**: connect to external
MCP servers, advertise their tools to the LLM, and dispatch tool calls to them —
so an agent can use any MCP server (e.g. `chrome-devtools-mcp` for browser
control) as ordinary tools. shell3 has no MCP code today.

## Scope

| Decision | Choice |
| --- | --- |
| Capabilities | **Tools only** (`tools/list`, `tools/call`). No resources/prompts. |
| Transport | **stdio only** (subprocess, newline-delimited JSON-RPC). No HTTP/SSE. |
| Lifecycle | **Lazy session** on first tool call, via **discover-and-cache** (below). |
| Tool naming | **Prefixed** `<server>__<tool>` (e.g. `chrome__navigate_page`). |
| Guards | MCP calls route through the existing `on_tool_call` guard chain. |

### Non-goals (MVP)

Resources, prompts, HTTP/SSE transport, MCP sampling, server-initiated requests,
Windows process-tree kill. All deferrable; none block the Chrome use case.

## Background: how MCP works

MCP is JSON-RPC 2.0 between a client (host app) and a server (separate process).
stdio transport: spawn the server, exchange newline-delimited JSON-RPC messages
(one JSON object per line) over its stdin/stdout. Lifecycle:

1. Spawn the server subprocess.
2. Send `initialize` (client capabilities + protocol version); receive server
   capabilities. Send the `notifications/initialized` notification.
3. `tools/list` → array of `{ name, description, inputSchema }` (inputSchema is
   JSON Schema).
4. `tools/call` with `{ name, arguments }` → `{ content[], structuredContent?,
   isError? }`. Session is long-lived and stateful (server keeps e.g. the browser
   open between calls). Kill the process to end it.

The server never talks to the LLM. The host does three jobs: (a) speak JSON-RPC
to the server, (b) advertise the server's tool schemas to the LLM, (c) forward
LLM tool calls to the server and feed results back.

## The lazy-spawn tension and its resolution

"Lazy spawn" (don't run a server until its tool is called) conflicts with the LLM
needing each tool's name + schema *before* it can call it. Resolution:
**discover-and-cache.**

- **First run of a given server config:** spawn once, `initialize` → `tools/list`,
  write schemas to `.shell3/mcp/<server>.tools.json` (cache keyed by a hash of
  command + args + env-keys), then **kill the process**. No long-lived session.
- **Subsequent runs:** load schemas from cache instantly; no spawn at startup.
- **First actual tool call in a session:** spawn the real long-lived session and
  keep it for the rest of the session.
- A `--refresh`-style path (CLI flag or cache delete) re-probes when a server's
  tools change.

Net: the server (and, for chrome, the browser) only runs during a session when a
tool is actually used. Cost: one cold discovery spawn the first time a server is
added (for `chrome-devtools-mcp`, this briefly launches Chrome; cached
thereafter).

Cache invalidation: if the config hash differs from the cached hash, re-probe and
rewrite. A corrupt/unreadable cache file triggers a re-probe.

## Architecture

Four isolated, independently testable pieces.

### 1. `internal/mcp/` — JSON-RPC stdio client (new)

Stdlib only (`os/exec`, `encoding/json`, `bufio`, `context`); no external SDK.

```
type ToolSchema struct {
    Name        string
    Description string
    InputSchema map[string]any // JSON Schema
}

type Result struct {
    Text    string // flattened content
    IsError bool
}

type Client interface {
    Start(ctx context.Context) error            // spawn + initialize handshake
    ListTools(ctx context.Context) ([]ToolSchema, error)
    CallTool(ctx context.Context, name string, args map[string]any) (Result, error)
    Close() error
}
```

Responsibilities:
- Spawn subprocess (`command`, `args`, `env`), wire stdin/stdout/stderr.
- Newline-delimited JSON-RPC framing; request/response correlation by `id`;
  handle notifications (no `id`).
- Handshake: `initialize` (advertise protocol version + empty/tools client caps),
  await result, send `notifications/initialized`.
- Drain stderr into a capped ring buffer for diagnostics.
- Clean shutdown: cancel context, close stdin, kill the process (Unix process
  group, `SIGKILL` fallback after grace).

### 2. `internal/mcp/manager.go` — lifecycle, cache, dispatch

Holds declared servers + cached schemas; owns discovery-and-cache, lazy session
creation, session reuse, and shutdown-all.

```
type Manager interface {
    // ToolDefinitions returns prefixed llm.ToolDefinition for all servers,
    // populated from cache (probing once if cache is absent/stale).
    ToolDefinitions(ctx context.Context) ([]llm.ToolDefinition, error)
    // Dispatch strips the prefix, ensures the session, forwards tools/call,
    // flattens the result to a string.
    Dispatch(ctx context.Context, prefixedName, argsJSON string) (string, error)
    // ToolNames reports which prefixed names this manager owns (for routing).
    ToolNames() map[string]bool
    Shutdown() // close all live sessions
}
```

- Prefix mapping: `chrome__navigate_page` ⇄ (server `chrome`, tool
  `navigate_page`). Prefix is `<name>__`; tool names containing `__` are split on
  the first occurrence.
- Allowlist: if a server declares `tools = {...}`, only those tool names are
  discovered/advertised.
- Result flattening: concatenate text blocks from `content`; if
  `structuredContent` is present and content is empty, JSON-encode it. `isError:
  true` is returned as an error-flagged string (not a Go error — tool-level
  errors are normal model-visible output).

### 3. `internal/luacfg/` — config surface

```lua
local chrome = shell3.mcp({
  name    = "chrome",
  command = "npx",
  args    = { "-y", "chrome-devtools-mcp@latest", "--autoConnect", "--no-usage-statistics" },
  env     = { FOO = shell3.env.secret("FOO") },   -- optional
  tools   = { "navigate_page", "click", "take_snapshot" }, -- optional allowlist; omit = all
})

shell3.agent({
  tools = {
    bash = true,
    mcp  = { chrome },   -- attach servers, mirroring custom = { ... }
  },
})
```

- New `shell3.mcp{}` registration (mirrors `shell3.tool{}`): validates keys
  (`name`, `command`, `args`, `env`, `tools`), returns a handle table carrying
  `__mcp = name`.
- New `MCPServer` struct in `LoadedConfig`; servers stored in a map by name.
- Agent `tools.mcp` resolves handles to server names (mirrors how `custom`
  resolves `__tool`). Add `mcp` to the agent tool-gate key allowlist.
- Required-field validation: `name`, `command` required; `args` defaults to `{}`.

### 4. Wiring in `internal/chat` + `internal/agentsetup`

Reuse the existing generic-dispatch seam rather than a parallel path.

- `agentsetup.Build` constructs the `Manager` from the active agent's declared
  servers and stores it on `chat.Config` via a **dedicated seam** parallel to the
  custom-tool seam: `MCPTool func(ctx, name, argsJSON) (string, error)` +
  `MCPToolNames map[string]bool`. (Kept separate from `CustomTool` so MCP
  lifecycle/cache concerns don't tangle with Lua-handler dispatch; routing is
  identical in shape.)
- The advertised LLM tool schema merges `Manager.ToolDefinitions()` alongside
  built-ins and custom tools.
- Dispatch: when a called name is MCP-owned, route to `Manager.Dispatch`.
- Guard chain: MCP dispatch passes through `ToolGuard` exactly like every other
  tool, so existing `on_tool_call` policies and approval gates apply unchanged.
- Shutdown: the chat loop / `pkg/shell3` calls `Manager.Shutdown()` on session
  end so no server processes leak.

## Data flow (one tool call)

LLM emits `chrome__navigate_page{url}` → chat dispatch matches an MCP-owned name →
`on_tool_call` guard chain runs → manager ensures/reuses the chrome session →
`tools/call navigate_page` over stdio → result `content` flattened to string →
returned to the model as the tool result.

## Error handling

- **Discovery failure** (server won't start / bad handshake): log + surface a
  clear config error; that server's tools are not advertised; shell3 still runs.
- **Call timeout / transport death:** tear down the poisoned session so the next
  call reconnects; return a readable error string to the model.
- **Tool-level error** (`isError: true`): returned as model-visible text, not a
  fatal error.
- **Shutdown:** cancel context, close stdin, kill process (Unix process group).

## Testing

- **`internal/mcp` client:** a fake MCP server (small in-repo Go binary or script
  speaking the stdio protocol) exercises handshake, `tools/list`, `tools/call`,
  timeout, and crash-recovery. No Chrome needed.
- **luacfg:** parse `shell3.mcp{}` + `tools.mcp` into structs; allowlist
  filtering; missing-field errors.
- **Manager:** discovery-cache hit/miss + invalidation, lazy spawn, prefix
  mapping, result flattening, guard routing — against the fake server.
- **Integration (optional, build-tagged, skipped by default):** drive real
  `chrome-devtools-mcp`.

## Validation

- `go test ./...` passes (new packages + touched packages).
- `make build` succeeds.
- Manual: a `shell3.lua` declaring the chrome MCP server lets an agent call
  `chrome__navigate_page` / `chrome__take_snapshot` against a real browser.

## Open follow-ups (post-MVP)

Resources/prompts, HTTP/SSE transport, Windows process-tree kill, per-tool
descriptions/renaming, concurrency limits on parallel MCP calls.
