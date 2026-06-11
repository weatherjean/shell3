# Remove MCP entirely + replace its sole use (Chrome) with a `browser` skill

Date: 2026-06-11
Status: approved (design), pending implementation plan

## Goal

Remove MCP support from shell3 **completely** — engine, Lua API, cookbook,
bootstrap, scaffold templates, and docs — and replace its only real-world use
(Chrome browser automation) with a self-contained `browser` **skill** driven by
`puppeteer-core` over the agent's existing `bash` tool. No long-lived server, no
`shell3.mcp()`, no `npx`-spawned subprocess that can wedge a turn.

Motivation: the owner doesn't use MCP for anything but Chrome, and the one time
it ran it bricked a turn (an in-flight, non-cancelable MCP call on the bot's
serial message loop — see the `/stop` analysis). A bounded `bash`-invoked
`node` script per action removes that failure mode and the whole subsystem.

## Decisions (locked)

- **Hard removal, no back-compat shim.** `shell3.mcp()` is deleted, not kept as a
  no-op. A config that still calls `shell3.mcp{}` fails loudly at load with a
  clear "unknown function" Lua error — acceptable because the only known caller
  is the telegram scaffold we are also editing.
- **Browser via `puppeteer-core`, headed, cross-platform.** Drives the
  system-installed Chrome (`channel: 'chrome'`), `headless: false` so the user
  watches a real window. No bundled Chromium download.
- **Install is agent-driven.** The skill *instructs* the agent to run
  `npm i puppeteer-core` in a dedicated dir if the module is missing; there is no
  baked-in auto-installer. Node is assumed present (already required by the host).
- **Persistent watched window.** The helper launches one headed Chrome with a
  fixed `--user-data-dir` (a dedicated debugging profile, not the user's everyday
  Chrome) + a debug port, records the WS endpoint, and later actions
  `puppeteer.connect()` to that same window. A `close` verb ends it.
- **Not Apple-specific.** No `osascript`/AppleScript. puppeteer-core + Node are
  cross-platform; Chrome launch flags are identical across macOS/Linux/Windows.
- **Sequencing.** Land the `browser` skill first (prove headed automation works),
  *then* remove MCP — so there is never a window with neither.
- **Branch.** `feat/remove-mcp` off the freshly fast-forwarded `main` (the
  cron → hot-reload → telegram-setup chain is now merged to main).

## Non-goals

- Scripted multi-page automation frameworks beyond what puppeteer-core gives for
  free (the owner explicitly did not ask for a heavy automation harness).
- Attaching to the user's *everyday* logged-in Chrome profile (not portable; a
  dedicated debugging profile is used, into which the user logs in once).
- Re-adding any external tool-server integration. Removing MCP closes the door on
  Gmail/Slack/filesystem/etc. MCP servers, by design.
- A Go-native browser tool (chromedp). Considered and rejected: it's an engine
  change + new Go dependency, the opposite of "just a skill."

## Part A — The `browser` skill (replacement, lands first)

A `shell3.skill{ name = "browser", ... }` whose body teaches the agent to drive a
headed Chrome via `puppeteer-core`, plus a small reusable helper script shipped
beside it so each action is a short, bounded `bash` call rather than re-derived
boilerplate.

**Layout (shipped in scaffold + cookbook):**
- `lib/skills/browser.lua` — the `shell3.skill` module (prompt guidance):
  install-if-missing, the persistent-window model, the verbs, and "write captures
  to the workdir and send them with `send_media_telegram`."
- `lib/browser/cli.js` — the helper: a thin `puppeteer-core` wrapper exposing
  verbs `open <url>`, `eval '<js>'`, `click <selector>`, `type <selector> <text>`,
  `wait <selector>`, `screenshot <path>`, `pdf <path>`, `close`. On first verb it
  launches headed Chrome (`channel:'chrome'`, `headless:false`,
  `--user-data-dir`, debug port), persists the WS endpoint to a state file, and
  reconnects on later verbs.
- `lib/browser/package.json` — declares the `puppeteer-core` dependency; the
  agent runs `npm i` here if `node_modules` is absent (the skill body says so).

**Capabilities** (cover the owner's picks — capture, drive, read): navigate, run
JS in the page (read `innerText`/scrape/interact), click/type/wait, screenshot,
PDF — all against one visible, watchable window.

**Failure containment:** every verb is one `node cli.js <verb>` call via `bash`,
which is cancelable and time-boundable — unlike the removed MCP dispatch. A hung
page fails one tool call, never the turn.

**Wiring:** the telegram scaffold grants the skill via `skills = { …, browser }`.
The old `{{if .Chrome}}` MCP block and `mcp = { chrome }` grant are deleted from
the template (see Part B).

## Part B — Remove MCP (deep inventory)

Delete the package and every reference. Traced touchpoints:

**Delete outright:**
- `internal/mcp/` entire package: `client.go`, `manager.go`, `protocol.go`,
  `environ.go`, and tests `client_test.go`, `manager_test.go`, `protocol_test.go`.
- `internal/chat/mcp_dispatch_test.go`, `internal/luacfg/mcp_test.go`.
- `docs/cookbook/lib/mcp.lua`.

**`internal/luacfg`:**
- `luacfg.go`: remove `MCPServer` type (`:38-39`), `MCPServerNames` fields on the
  agent and subagent structs (`:83`, `:98`), the `MCPServers` map field (`:115`)
  and its initialization in the `LoadedConfig` constructor (`:143`).
- `register.go`: remove the `mcp` builtin registration (`:18`), `luaMCP` (`:179-`),
  `mcpKeys` (`:175`), the `"mcp"` entry in the agent tool-key allowlist (`:171`),
  the `__mcp` handle tagging (`:205`), and the two `tools.mcp` parse blocks that
  populate `MCPServerNames` for agents and subagents (`:301-302`, `:370-371`).

**`internal/chat`:**
- `tools.go`: remove `dispatchMCPTool` (`:65-72`).
- `chat.go`: remove `MCPTool` + `MCPToolNames` from `RuntimeConfig` and `Config`
  and their copy-through (`:35-36`, `:106-110`, `:159`, `:200-201`).
- `toolhandler.go`: remove the `MCPTool`/`MCPToolNames` fields (`:109-112`).
- `turn.go`: remove the MCP dispatch branch in the tool router (`:470-478`) — the
  `else if cfg.MCPToolNames[tc.Name]` arm; custom + turn-scoped dispatch remain.

**`internal/agentsetup/agentsetup.go`:** remove the `internal/mcp` import (`:20`),
the `mcpMgr` fields on `Parts` and `builder` (`:57`, `:408`), `MCPServerCount`
(`:85-86`), `MCPTool` (`:103-108`), the per-agent MCP tool-merge block
(`:174-185`), `MCPToolNames`/`MCPTool` wiring into the chat config (`:233`,
`:328`), the subagent `MCPServerNames` pass-through (`:138`), `buildMCP`
(`:490-510`) and its call in `Build` (`:385`), the `mcpMgr` field in the `Parts`
literal (`:386`), and the `mcpMgr.Shutdown()` closer. Update the cleanup doc
comments (`:47`, `:52`, `:371`).

**`pkg/shell3`:** `runtime.go` — drop "MCP servers" from the doc comment (`:46`)
and confirm no MCP field remains. `reload.go` — **remove the "MCP servers restart
on reload" path** and update the reload semantics doc (reload no longer pauses for
MCP). `shell3.go` (`:2` reference) — clean up.

**`internal/telegram/commands.go`:** remove the MCP reference (a help/line item;
confirm it isn't a functional command).

**`cmd/shell3/boot.go`:** remove the `--chrome` flag (`:48`), the `[y/N]` Chrome
prompt (`:118`), the chrome success line (`:244`), and "MCP" from the
edit-hint line (`:231`). Remove `TelegramValues.Chrome` plumbing.

**`internal/scaffold`:** `scaffold.go` — remove `Chrome` from `TelegramValues` and
any MCP wording. `defaults/telegram/shell3.lua.tmpl` — delete the `{{if .Chrome}}`
MCP block and the `mcp = { chrome }` grant; add `browser` to `skills` and ship
`lib/browser/` + `lib/skills/browser.lua`. `defaults/base/shell3.lua.tmpl` —
remove the MCP example/comment.

**Docs:** strip MCP from `README.md`, `AGENTS.md`, `SECURITY.md`,
`docs/cookbook/README.md`; replace the cookbook `mcp.lua` recipe with a `browser`
recipe; add a CHANGELOG **Removed** entry (MCP support) and **Added** (browser
skill). Historical `docs/dev/superpowers/specs|plans/*` that merely mention MCP
are left as-is (they're dated records).

## Data-flow changes

- **Tool dispatch:** the router in `turn.go` loses its MCP arm; agent tools are
  now built-ins + custom (Lua) tools + skills only. No prefixed `server__tool`
  names exist.
- **Config load:** `LoadedConfig` no longer carries `MCPServers` or
  `MCPServerNames`; the `tools.mcp` key is rejected by the existing
  unknown-key check (`checkKeys`), so a stale `mcp = {…}` grant fails loudly.
- **Reload:** no MCP manager to tear down/respawn; reload gets slightly simpler
  and faster (one less restart pause).
- **Runtime build:** `Build` no longer constructs an MCP manager or registers a
  shutdown closer for it.

## Error handling & edge cases

- A user config calling `shell3.mcp{}` → Lua "attempt to call a nil value
  (field 'mcp')" at load. Acceptable per the hard-removal decision; the scaffold
  no longer emits it.
- A config with a leftover `tools = { mcp = {…} }` → `checkKeys` rejects `mcp` as
  an unknown tool key with the existing clear error.
- `browser` skill when `puppeteer-core` is missing → the skill body tells the
  agent to `npm i puppeteer-core` in `lib/browser/` and retry; a missing Chrome
  surfaces as a puppeteer launch error in that one bash call (never a boot/load
  failure).

## Testing

- **Removal is proven by compilation + the suite:** after deletion,
  `go build ./... && go vet ./... && gofmt -l . && go test -race ./...` is green
  with the MCP packages and tests gone. Update `boot_test.go` and
  `scaffold_test.go` to drop the Chrome/MCP assertions (the telegram-setup tests
  that assert `c.MCPServers["chrome"]` are removed).
- **luacfg:** a regression test asserting `shell3.mcp` is undefined (calling it
  errors) and that `tools.mcp` is rejected — proving the surface is gone.
- **scaffold:** the telegram render test asserts the `browser` skill is present
  and no MCP server is declared; the template still loads through `luacfg`.
- **browser skill:** the `cli.js` helper is exercised in a Node-gated test
  (skipped when `node`/Chrome absent, so CI without a browser still passes) that
  opens a `data:`/local page and reads back `innerText` — enough to prove the
  puppeteer-core path, without networked flakiness.

## Sequencing (implementation order)

1. Add the `browser` skill + `lib/browser/` helper to scaffold + cookbook; wire
   it into the telegram template's `skills`. Prove headed automation manually.
2. Remove MCP per Part B, package-by-package, keeping the tree building between
   logical steps (luacfg → chat → agentsetup → pkg/shell3 → cmd/scaffold → docs).
3. Full sweep green; update the live `~/.shell3/telegram` config to the skill and
   restart the bot for the owner to test.
