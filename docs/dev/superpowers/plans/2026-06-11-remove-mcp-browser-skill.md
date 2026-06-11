# Remove MCP + `browser` Skill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace shell3's only real MCP use (Chrome) with a self-contained `browser` skill driven by `puppeteer-core`, then remove MCP support from the codebase entirely.

**Architecture:** A `browser` skill = a `lib/skills/browser.lua` prompt module + a `lib/browser/cli.js` puppeteer-core helper (verbs over a persistent, headed, dedicated-profile Chrome on a debug port) + `lib/browser/package.json`; every action is a bounded `bash`→`node` call (no long-lived server, no hang). Then MCP is deleted: the `internal/mcp` package, the `shell3.mcp()` Lua builtin + `MCPServer`/`MCPServers`/`MCPServerNames`, MCP tool dispatch in `chat`, the MCP manager in `agentsetup`, the reload MCP-restart path, and all docs/scaffold/bootstrap references.

**Tech Stack:** Go, `puppeteer-core` (Node), `text/template`+`embed` (scaffold), `gopher-lua` (luacfg). No new Go dependency; `puppeteer-core` is a Node dep the skill installs on demand.

**Source of truth:** `docs/dev/superpowers/specs/2026-06-11-remove-mcp-browser-skill-design.md`. Signatures/line numbers below are verbatim from `feat/remove-mcp` at branch creation (verified 2026-06-11); confirm with a quick read before editing since earlier edits in the same task shift line numbers.

**Build approach:** Browser skill lands first (Tasks 1–2) so there's never a window with neither browser nor MCP. Then MCP removal: the engine core is one interdependent task that only compiles green when fully removed (Task 3); the user-facing surface (Task 4) and docs (Task 5) follow; Task 6 sweeps and updates the live bot. After each task: `go build ./... && go vet ./... && gofmt -l . && go test -race ./...`.

**Branch:** Already on `feat/remove-mcp` (off the merged `main`). Do NOT touch the real `~/.shell3` except in Task 6's explicit live-update step. Never read or display any `.env`.

---

## Task 1: The `browser` skill (files + telegram wiring)

**Files:**
- Create: `internal/scaffold/defaults/base/lib/browser/cli.js`
- Create: `internal/scaffold/defaults/base/lib/browser/package.json`
- Create: `internal/scaffold/defaults/base/lib/skills/browser.lua`
- Modify: `internal/scaffold/defaults/telegram/shell3.lua.tmpl` (require + grant the skill; the Chrome MCP block is removed later in Task 4)
- Modify: `internal/scaffold/scaffold_test.go` (assert the rendered telegram config has the `browser` skill)

The scaffold copies every file under `defaults/base/lib/` recursively (see `RenderBaseConfig`/`RenderTelegramConfig`'s `fs.WalkDir(baseFS, baseRoot+"/lib", …)`), so placing the helper under `defaults/base/lib/browser/` ships it to both base and telegram installs.

- [ ] **Step 1: Create the puppeteer-core helper** `internal/scaffold/defaults/base/lib/browser/cli.js` with EXACTLY this content:

```js
#!/usr/bin/env node
// lib/browser/cli.js — headed Chrome automation via puppeteer-core (no MCP).
// One headed Chrome (a dedicated debugging profile) persists across calls on a
// fixed debug port; each invocation reconnects, acts, and leaves the window open.
//
// Usage: node cli.js <verb> [args]
//   open <url>            navigate the active tab
//   eval '<js-expr>'      evaluate JS in the page, print the result
//   click <selector>      click the first match
//   type <selector> <txt> type text into the first match
//   wait <selector>       wait until the selector appears (30s)
//   screenshot <path>     full-page PNG to <path>
//   pdf <path>            print the page to <path>
//   close                 close the persistent Chrome
//
// Chrome is located via $CHROME_PATH or common per-OS install paths.

const fs = require('fs');
const path = require('path');
const { spawn } = require('child_process');

let puppeteer;
try {
  puppeteer = require('puppeteer-core');
} catch (e) {
  console.error('puppeteer-core not installed. Run: (cd ' + __dirname + ' && npm i)');
  process.exit(3);
}

const PORT = 9333;
const BROWSER_URL = 'http://127.0.0.1:' + PORT;
const PROFILE = path.join(__dirname, 'profile');

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function chromePath() {
  if (process.env.CHROME_PATH) return process.env.CHROME_PATH;
  const byOS = {
    darwin: [
      '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
      '/Applications/Chromium.app/Contents/MacOS/Chromium',
    ],
    linux: [
      '/usr/bin/google-chrome', '/usr/bin/google-chrome-stable',
      '/usr/bin/chromium', '/usr/bin/chromium-browser',
    ],
    win32: [
      'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
      'C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe',
    ],
  };
  for (const p of byOS[process.platform] || []) {
    if (fs.existsSync(p)) return p;
  }
  throw new Error('Chrome not found; set CHROME_PATH to your Chrome binary');
}

function spawnChrome() {
  const child = spawn(chromePath(), [
    '--remote-debugging-port=' + PORT,
    '--user-data-dir=' + PROFILE,
    '--no-first-run',
    '--no-default-browser-check',
  ], { detached: true, stdio: 'ignore' });
  child.unref();
}

async function connect() {
  try {
    return await puppeteer.connect({ browserURL: BROWSER_URL });
  } catch (e) {
    spawnChrome();
    for (let i = 0; i < 40; i++) {
      await sleep(300);
      try {
        return await puppeteer.connect({ browserURL: BROWSER_URL });
      } catch (_) { /* not up yet */ }
    }
    throw new Error('could not reach Chrome on ' + BROWSER_URL);
  }
}

async function activePage(browser) {
  const pages = await browser.pages();
  return pages.length ? pages[pages.length - 1] : await browser.newPage();
}

async function main() {
  const [verb, ...rest] = process.argv.slice(2);
  if (!verb) { console.error('usage: node cli.js <verb> [args]'); process.exit(2); }

  if (verb === 'close') {
    try {
      const b = await puppeteer.connect({ browserURL: BROWSER_URL });
      await b.close();
      console.log('closed');
    } catch (e) {
      console.log('not running');
    }
    return;
  }

  const browser = await connect();
  const page = await activePage(browser);
  let out = '';
  switch (verb) {
    case 'open':
      await page.goto(rest[0], { waitUntil: 'domcontentloaded' });
      out = 'opened ' + page.url();
      break;
    case 'eval':
      out = String(await page.evaluate(rest.join(' ')));
      break;
    case 'click':
      await page.click(rest[0]);
      out = 'clicked ' + rest[0];
      break;
    case 'type':
      await page.type(rest[0], rest.slice(1).join(' '));
      out = 'typed into ' + rest[0];
      break;
    case 'wait':
      await page.waitForSelector(rest[0], { timeout: 30000 });
      out = 'visible ' + rest[0];
      break;
    case 'screenshot':
      await page.screenshot({ path: rest[0], fullPage: true });
      out = 'screenshot ' + rest[0];
      break;
    case 'pdf':
      await page.pdf({ path: rest[0] });
      out = 'pdf ' + rest[0];
      break;
    default:
      await browser.disconnect();
      console.error('unknown verb: ' + verb);
      process.exit(2);
  }
  console.log(out);
  await browser.disconnect(); // leave the window open for the next call
}

main().catch((e) => { console.error('error: ' + e.message); process.exit(1); });
```

- [ ] **Step 2: Create** `internal/scaffold/defaults/base/lib/browser/package.json` with EXACTLY:

```json
{
  "name": "shell3-browser",
  "private": true,
  "version": "1.0.0",
  "description": "puppeteer-core helper for the shell3 browser skill",
  "dependencies": {
    "puppeteer-core": "^23.0.0"
  }
}
```

- [ ] **Step 3: Create the skill module** `internal/scaffold/defaults/base/lib/skills/browser.lua` with EXACTLY:

```lua
-- lib/skills/browser.lua — drive a real, visible Chrome (no MCP). Returned for require().
return shell3.skill({
  name        = "browser",
  description = "Drive a real, visible Chrome to open pages, read/extract content, fill forms, click, screenshot, or print to PDF. Cross-platform via puppeteer-core over bash. Use for JS-heavy or login-gated pages that web_fetch can't handle.",
  body        = [[
You can drive a real, headed Chrome window (you and the user can both watch it).
It is NOT an MCP server — it is a small Node helper you call with `bash`, so every
action is one bounded command that can never hang the conversation.

## One-time setup (do this if it's missing)
The helper lives in your config's `lib/browser/` dir (next to your shell3.lua —
call the `status` tool if unsure of the path). If `lib/browser/node_modules` does
not exist, install the dependency once:
    (cd <config-dir>/lib/browser && npm i)
If a command prints "puppeteer-core not installed", run that, then retry.

## Driving the browser
Run one verb per call (the window persists across calls on a fixed debug port):
    node <config-dir>/lib/browser/cli.js open "https://example.com"
    node <config-dir>/lib/browser/cli.js eval "document.title"
    node <config-dir>/lib/browser/cli.js eval "document.body.innerText.slice(0,4000)"
    node <config-dir>/lib/browser/cli.js click "#submit"
    node <config-dir>/lib/browser/cli.js type "input[name=q]" "hello world"
    node <config-dir>/lib/browser/cli.js wait ".results"
    node <config-dir>/lib/browser/cli.js screenshot /tmp/page.png
    node <config-dir>/lib/browser/cli.js pdf /tmp/page.pdf
    node <config-dir>/lib/browser/cli.js close   -- when done, to free the window

## Reading and reporting
- To read a page, prefer `eval "document.body.innerText"` (rendered text) over raw
  HTML. Slice long output so you don't flood the chat.
- Captures (screenshot/pdf): write to a file, then deliver it with the media tool
  (send_media_telegram) if you have one; otherwise just report the path.

## Notes
- It uses a dedicated Chrome profile (not the user's everyday browser). Log in once
  inside the watched window and the session persists for later runs.
- Cross-platform: it finds Chrome automatically; if it can't, set CHROME_PATH.
- A flaky page fails one `bash` call (you'll see "error: ..."), never the turn —
  just adjust and retry.
]],
})
```

- [ ] **Step 4: Wire the skill into the telegram template.** In `internal/scaffold/defaults/telegram/shell3.lua.tmpl`, find the requires near the top:

```lua
local tools  = require("lib.tools")   -- { web_fetch, brave_search }
local guards = require("lib.guards")  -- { no_env_edit, confirm_destructive }
```

and add a third line:

```lua
local tools   = require("lib.tools")   -- { web_fetch, brave_search }
local guards  = require("lib.guards")  -- { no_env_edit, confirm_destructive }
local browser = require("lib.skills.browser")
```

Then find the agent's skills grant (currently line ~165):

```lua
  skills = { self_evolve, scheduling_jobs },
```

and replace with:

```lua
  skills = { self_evolve, scheduling_jobs, browser },
```

> Leave the `{{if .Chrome}}` MCP block and `mcp = { chrome }` grant in place for now — they're removed in Task 4 so this task stays a pure addition.

- [ ] **Step 5: Write the failing test.** Append to `internal/scaffold/scaffold_test.go`:

```go
func TestRenderTelegramConfigHasBrowserSkill(t *testing.T) {
	dir := t.TempDir()
	if err := RenderTelegramConfig(dir, TelegramValues{
		Values:  Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m-1"},
		ChatID:  "1", WorkDir: dir,
	}, false); err != nil {
		t.Fatal(err)
	}
	// Helper files shipped alongside the skill.
	for _, p := range []string{"lib/skills/browser.lua", "lib/browser/cli.js", "lib/browser/package.json"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TELEGRAM_BOT_TOKEN=tok\nMAIN_API_KEY=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := luacfg.Load(filepath.Join(dir, "shell3.lua"), dir)
	if err != nil {
		t.Fatalf("telegram config failed to load: %v", err)
	}
	defer c.Close()
	var found bool
	for _, s := range c.Skills {
		if s.Name == "browser" {
			found = true
		}
	}
	if !found {
		t.Errorf("browser skill not declared; skills = %v", c.Skills)
	}
}
```

- [ ] **Step 6: Run to verify failure**

Run: `go test ./internal/scaffold/ -run TestRenderTelegramConfigHasBrowserSkill -v`
Expected: FAIL — `browser` skill not declared (the require/grant or files not yet wired) — or PASS only once Steps 1–4 are complete. If it fails because `c.Skills` lacks a `Name` field, check the `luacfg.Skill` type (`Name`, `Description`, `Body`) and adjust.

- [ ] **Step 7: Verify the helper JS is syntactically valid** (no Chrome needed):

Run: `node --check internal/scaffold/defaults/base/lib/browser/cli.js`
Expected: no output, exit 0. (If `node` is absent, note it; the file ships regardless.)

- [ ] **Step 8: Run the whole scaffold package + sweep**

Run: `go test ./internal/scaffold/ && go build ./... && go vet ./internal/scaffold/ && gofmt -l internal/scaffold/`
Expected: PASS / clean.

- [ ] **Step 9: Commit**

```bash
git add internal/scaffold/defaults/base/lib/browser internal/scaffold/defaults/base/lib/skills/browser.lua internal/scaffold/defaults/telegram/shell3.lua.tmpl internal/scaffold/scaffold_test.go
git commit -m "feat(scaffold): browser skill (puppeteer-core helper) + grant it in the telegram template"
```

---

## Task 2: Prove headed automation manually (no code)

**Files:** none (verification only).

This is a manual smoke against a throwaway HOME so a real headed Chrome is exercised before MCP is removed. Requires Node + Chrome on the machine.

- [ ] **Step 1: Render a telegram config into a temp dir and install the dep**

```bash
TMP=$(mktemp -d)
HOME=$TMP go run ./cmd/shell3 boot --telegram \
  --url http://localhost:9999/v1 --model test --name main \
  --tg-token T --chat-id 1
(cd "$TMP/.shell3/telegram/lib/browser" && npm i)
```
Expected: boot succeeds; `npm i` installs `puppeteer-core`.

- [ ] **Step 2: Drive a headed Chrome and read content back**

```bash
B="$TMP/.shell3/telegram/lib/browser/cli.js"
node "$B" open "https://example.com"
node "$B" eval "document.title"
node "$B" screenshot /tmp/shell3-browser-smoke.png
node "$B" close
```
Expected: a visible Chrome window opens, `eval` prints `Example Domain`, the screenshot file exists. Then clean up:

```bash
chmod -R u+w "$TMP" 2>/dev/null; rm -rf "$TMP" /tmp/shell3-browser-smoke.png
```

> No commit (verification only). If a verb misbehaves, fix `cli.js` under `internal/scaffold/defaults/base/lib/browser/cli.js`, re-run `node --check`, recommit Task 1, and re-smoke.

---

## Task 3: Remove the MCP engine (luacfg + chat + agentsetup + pkg/shell3 + the package)

**Files:**
- Delete: `internal/mcp/` (entire dir: `client.go`, `manager.go`, `protocol.go`, `environ.go`, `client_test.go`, `manager_test.go`, `protocol_test.go`)
- Delete: `internal/chat/mcp_dispatch_test.go`, `internal/luacfg/mcp_test.go`
- Modify: `internal/luacfg/luacfg.go`, `internal/luacfg/register.go`
- Modify: `internal/chat/tools.go`, `internal/chat/chat.go`, `internal/chat/toolhandler.go`, `internal/chat/turn.go`
- Modify: `internal/agentsetup/agentsetup.go`
- Modify: `pkg/shell3/reload.go`, `pkg/shell3/runtime.go`, `pkg/shell3/shell3.go`
- Modify: `internal/telegram/commands.go`
- Create test: a regression in `internal/luacfg/register_test.go` (or a new `internal/luacfg/no_mcp_test.go`)

These are interdependent — the tree compiles green only when MCP is gone from all of them, so this is one task. Make the edits in leaf-to-root order, then build and let the compiler flag any straggler.

- [ ] **Step 1: Delete the MCP package and its dedicated tests**

```bash
git rm -r internal/mcp
git rm internal/chat/mcp_dispatch_test.go internal/luacfg/mcp_test.go
```

- [ ] **Step 2: Strip MCP from `internal/luacfg/luacfg.go`.**
  - Delete the `MCPServer` struct (the block):
    ```go
    // MCPServer is a declared external MCP server (stdio transport).
    type MCPServer struct {
    	Name    string
    	Command string
    	Args    []string
    	Env     map[string]string
    	Tools   []string // optional allowlist
    }
    ```
  - In the `Agent` struct, delete the line `MCPServerNames          []string`.
  - In the `Subagent` struct, delete the line `MCPServerNames                       []string`.
  - In `LoadedConfig`, delete the field line `MCPServers map[string]MCPServer`.
  - In the constructor, change:
    ```go
    c := &LoadedConfig{Tools: map[string]CustomTool{}, MCPServers: map[string]MCPServer{}, Secrets: env, L: lua.NewState()}
    ```
    to:
    ```go
    c := &LoadedConfig{Tools: map[string]CustomTool{}, Secrets: env, L: lua.NewState()}
    ```

- [ ] **Step 3: Strip MCP from `internal/luacfg/register.go`.**
  - Delete the registration line `L.SetField(tbl, "mcp", L.NewFunction(c.luaMCP))`.
  - Delete the entire `luaMCP` function.
  - Delete the `mcpKeys` var.
  - In `toolGateKeys`, remove `"mcp": true,` (keep the rest of the map intact):
    ```go
    var toolGateKeys = map[string]bool{
    	"bash": true, "bash_bg": true, "shell_interactive": true, "edit": true,
    	"history": true, "custom": true, "skill": true,
    	"prune": true, "compact": true, "media": true,
    	"subagents": true,
    }
    ```
  - Delete the agent `tools.mcp` parse block:
    ```go
    		if mc, ok := tt.RawGetString("mcp").(*lua.LTable); ok {
    			a.MCPServerNames = handleNames(mc, "__mcp")
    		}
    ```
  - Delete the subagent `tools.mcp` parse block (identical, assigns `s.MCPServerNames`).

- [ ] **Step 4: Strip MCP from `internal/chat`.**
  - `tools.go`: delete the entire `dispatchMCPTool` function.
  - `turn.go`: in the router, delete the MCP arm so it reads:
    ```go
    			if h, ok := turnScoped[tc.Name]; ok {
    				handler = h
    			} else if cfg.CustomToolNames[tc.Name] {
    				res = dispatchCustomTool(ctx, cfg.CustomTool, tc.Name, tc.RawArgs)
    			} else if h, ok := cfg.Handlers[tc.Name]; ok {
    				handler = h
    			} else {
    				res = errResult(fmt.Sprintf("error: unknown tool %q", tc.Name))
    			}
    ```
    and update the comment above it to drop "the prefixed MCP and".
  - `chat.go`: delete the `MCPToolNames map[string]bool` field (+ its comment) from `ActiveAgent`; delete the `MCPTool func(...)` and `MCPToolNames map[string]bool` fields (+ comments) from `Config`; delete `c.MCPToolNames = rt.MCPToolNames` in `ApplyActiveAgent`; delete the `MCPTool:` and `MCPToolNames:` lines in the `NewTurnConfig` literal.
  - `toolhandler.go`: delete the `MCPTool func(...)` and `MCPToolNames map[string]bool` fields (+ comments) from `TurnConfig`.

- [ ] **Step 5: Strip MCP from `internal/agentsetup/agentsetup.go`.**
  - Delete the import `"github.com/weatherjean/shell3/internal/mcp"`.
  - Delete the `mcpMgr *mcp.Manager` field from `Parts` and from `builder`.
  - Delete the `MCPServerCount` method.
  - Delete the `MCPTool` method.
  - Delete the per-agent MCP tool-merge block (the `var mcpNames map[string]bool` block using `p.mcpMgr`), and remove the `MCPToolNames: mcpNames,` line from the `chat.ActiveAgent` return literal.
  - Remove `MCPTool: p.MCPTool,` from the `chat.Config{...}` literal.
  - In `subagentToAgent`, delete the `MCPServerNames: sa.MCPServerNames,` line.
  - Delete the entire `buildMCP` method and the `b.buildMCP()` call in `BuildParts`.
  - In the `Parts{...}` literal, delete `mcpMgr: b.mcpMgr,`.
  - Update the doc comments that mention "MCP servers" / "MCP manager" (the `Parts` doc, the `BuildParts` cleanup doc) to drop MCP.

- [ ] **Step 6: Strip MCP from `pkg/shell3`.**
  - `reload.go`: delete the `MCP int` field from `ReloadResult` (and drop "MCP restart" from the `Notes` comment); delete `MCP: newParts.MCPServerCount(),` from the `ReloadResult` literal; update the doc comments that list "MCP servers" in the reload teardown (lines ~33-37 and the `oldCleanup()` comment ~104).
  - `runtime.go`: in the `RuntimeSpec` doc, drop "MCP servers,".
  - `shell3.go`: in the package/`Session.Close` doc comments, drop the "MCP" mentions.

- [ ] **Step 7: Fix `internal/telegram/commands.go`.** Update `formatReload` to drop the MCP count:

```go
func formatReload(r shell3.ReloadResult) string {
	msg := fmt.Sprintf("✅ reloaded — %d agents, %d models, %d jobs", r.Agents, r.Models, r.Jobs)
	if len(r.Notes) > 0 {
		msg += "\n• " + strings.Join(r.Notes, "\n• ")
	}
	return msg
}
```

If `commands_test.go` asserts the old "%d MCP" string, update that expectation too.

- [ ] **Step 8: Build and chase down stragglers**

Run: `go build ./... 2>&1 | head -40`
Expected: initially may list any remaining references (e.g. a test using `MCPServers`, `MCPTool`, or `mcp.`). Fix each by removing the MCP usage. Repeat until the build is clean. Then `go vet ./...`.

- [ ] **Step 9: Add the regression test.** Create `internal/luacfg/no_mcp_test.go`:

```go
package luacfg

import (
	"os"
	"path/filepath"
	"testing"
)

// After removal, shell3.mcp must not exist and `tools = { mcp = ... }` must be
// rejected as an unknown tool key.
func TestMCPBuiltinRemoved(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(cfg, []byte(`shell3.mcp({ name = "x", command = "y" })`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(cfg, dir); err == nil {
		t.Fatal("expected error: shell3.mcp should be undefined after removal")
	}

	cfg2 := filepath.Join(dir, "two.lua")
	body := `shell3.model("m", { base_url = "http://x/v1", api_key = "k", model = "z" })
shell3.agent({ name = "a", model = "m", prompt = "p", tools = { mcp = {} } })
`
	if err := os.WriteFile(cfg2, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(cfg2, dir); err == nil {
		t.Fatal("expected error: tools.mcp should be rejected as an unknown key")
	}
}
```

> If `Load` requires the api_key to come from `.env` (not an inline string), write a `.env` with `K=` and use `shell3.env.secret("K")` instead — mirror an existing luacfg test's model declaration. The point of the second case is only that `mcp` under `tools` is rejected.

- [ ] **Step 10: Full sweep**

Run: `go build ./... && go vet ./... && gofmt -l . && go test -race ./...`
Expected: all green; `internal/mcp` is gone; no references remain.

- [ ] **Step 11: Commit**

```bash
git add -A
git commit -m "refactor: remove MCP support entirely (package, Lua builtin, dispatch, manager, reload path)"
```

---

## Task 4: Remove the user-facing Chrome/MCP surface (boot + templates)

**Files:**
- Modify: `cmd/shell3/boot.go`, `cmd/shell3/boot_test.go`
- Modify: `internal/scaffold/scaffold.go` (drop `TelegramValues.Chrome`)
- Modify: `internal/scaffold/defaults/telegram/shell3.lua.tmpl` (delete the Chrome MCP block + grant)
- Modify: `internal/scaffold/defaults/base/shell3.lua.tmpl` (drop the MCP mention in the self_evolve body)
- Modify: `internal/scaffold/scaffold_test.go` (delete the Chrome-MCP tests)

- [ ] **Step 1: Delete the Chrome MCP block from the telegram template.** In `internal/scaffold/defaults/telegram/shell3.lua.tmpl`, delete the entire block:

```lua
{{if .Chrome}}-- ---------------------------------------------------------------------------
-- Chrome DevTools MCP (browser automation; needs Node/npx). Started lazily on
-- first use. Add a `tools = {...}` allowlist to restrict which tools are exposed.
-- ---------------------------------------------------------------------------
local chrome = shell3.mcp({
  name    = "chrome",
  command = "npx",
  args    = { "-y", "chrome-devtools-mcp@latest", "--autoConnect", "--no-usage-statistics" },
})
{{end}}
```

Make sure the line that previously followed `{{end}}` (the `-- ---` agent header comment) keeps a clean leading newline so the rendered Lua stays well-formed.

- [ ] **Step 2: Delete the `mcp = { chrome }` grant from the same template.** Change:

```lua
    subagents         = { explorer },
    custom            = { tools.web_fetch, tools.brave_search },
{{if .Chrome}}    mcp               = { chrome },
{{end}}  },
```

to:

```lua
    subagents         = { explorer },
    custom            = { tools.web_fetch, tools.brave_search },
  },
```

- [ ] **Step 3: Drop the MCP mention from the base template.** In `internal/scaffold/defaults/base/shell3.lua.tmpl`, inside the self_evolve skill body, change:

```lua
- MCP servers and model proxies restart on reload (a brief pause); agents,
  models, tools, skills, and cron apply cleanly.
```

to:

```lua
- Model proxies restart on reload (a brief pause); agents, models, tools,
  skills, and cron apply cleanly.
```

- [ ] **Step 4: Remove `Chrome` from `TelegramValues`.** In `internal/scaffold/scaffold.go`, delete the field line:

```go
	Chrome           bool   // declare the chrome DevTools MCP + grant it to the agent
```

- [ ] **Step 5: Remove the Chrome flags/prompt/print from `cmd/shell3/boot.go`.**
  - In `bootFlags`, delete the `chrome bool` field.
  - Delete the flag registration line:
    ```go
    	cmd.Flags().BoolVar(&f.chrome, "chrome", false, "Enable the Chrome DevTools MCP (browser automation; needs Node/npx)")
    ```
  - Delete the `var chrome bool` declaration and the `[y/N]` chrome prompt block:
    ```go
    		if !chrome && tty {
    			ans, err := value("", "Enable Chrome browser MCP (browser automation; needs Node/npx)? [y/N]", "n", in, tty, false)
    			if err != nil {
    				return err
    			}
    			chrome = strings.EqualFold(strings.TrimSpace(ans), "y") || strings.EqualFold(strings.TrimSpace(ans), "yes")
    		}
    ```
    (also remove the earlier `chrome = f.chrome` assignment if present.)
  - In the `scaffold.RenderTelegramConfig(...)` call, delete the `Chrome: chrome,` line.
  - Change `printTelegramBootSuccess` to drop the `chrome bool` parameter and the chrome line:
    ```go
    func printTelegramBootSuccess(dir, cfgPath, envPath string) {
    	fmt.Println()
    	fmt.Println("shell3 Telegram host is configured.")
    	fmt.Printf("  config:  %s\n", cfgPath)
    	fmt.Printf("  modules: %s\n", filepath.Join(dir, "lib"))
    	fmt.Printf("  secrets: %s  (TELEGRAM_BOT_TOKEN + model key — never commit this)\n", envPath)
    	fmt.Println()
    	fmt.Println("Run:  shell3 telegram")
    }
    ```
    and update its call site to `printTelegramBootSuccess(dir, cfgPath, envPath)`.
  - In `printBootSuccess`, change the edit-hint line:
    ```go
    	fmt.Println("Edit shell3.lua (and lib/) to add tools, skills, MCP, or agents —")
    ```
    to:
    ```go
    	fmt.Println("Edit shell3.lua (and lib/) to add tools, skills, or agents —")
    ```
  - If `strings` becomes unused after deleting the prompt parse, remove the import (the build will tell you).

- [ ] **Step 6: Delete the Chrome-MCP scaffold tests.** In `internal/scaffold/scaffold_test.go`, delete `TestRenderTelegramConfigChrome` entirely, and in `TestRenderTelegramConfigLoads` delete the assertion block that references `c.MCPServers` (the `if len(c.MCPServers) != 0 { ... }` check) — that field no longer exists.

- [ ] **Step 7: Fix the boot test.** In `cmd/shell3/boot_test.go`, in `TestBootTelegramEndToEnd`, remove `chrome: true` from the `bootFlags` literal and delete the assertion:

```go
	if _, ok := c.MCPServers["chrome"]; !ok {
		t.Errorf("--chrome should declare the chrome MCP server, got %v", c.MCPServers)
	}
```

- [ ] **Step 8: Build, vet, gofmt, test**

Run: `go build ./... && go vet ./... && gofmt -l . && go test ./internal/scaffold/ ./cmd/shell3/`
Expected: clean / PASS. (The Task 1 `TestRenderTelegramConfigHasBrowserSkill` still passes.)

- [ ] **Step 9: Commit**

```bash
git add cmd/shell3/boot.go cmd/shell3/boot_test.go internal/scaffold/
git commit -m "refactor(boot,scaffold): drop --chrome MCP flag, Chrome template block, and Chrome tests"
```

---

## Task 5: Docs — cookbook, README/AGENTS/SECURITY, CHANGELOG

**Files:**
- Delete: `docs/cookbook/lib/mcp.lua`
- Create: `docs/cookbook/lib/browser.lua` (a copy of the shipped skill, as the recipe)
- Modify: `docs/cookbook/README.md`, `README.md`, `AGENTS.md`, `SECURITY.md`, `CHANGELOG.md`

- [ ] **Step 1: Replace the cookbook MCP recipe.**

```bash
git rm docs/cookbook/lib/mcp.lua
cp internal/scaffold/defaults/base/lib/skills/browser.lua docs/cookbook/lib/browser.lua
```

- [ ] **Step 2: Update cookbook README.** In `docs/cookbook/README.md`, find the line/section referencing `lib/mcp.lua` (the MCP recipe) and replace it with a one-line entry for `lib/browser.lua` — "Drive a real headed Chrome via puppeteer-core (the `browser` skill); see `lib/browser.lua`." Remove any other MCP wording in that file.

- [ ] **Step 3: Strip MCP from `README.md`, `AGENTS.md`, `SECURITY.md`.** In each, locate MCP mentions (grep below) and remove or reword them so no feature claim implies MCP support. Where a list includes "MCP servers" as a capability, delete that item; where SECURITY.md discusses MCP subprocess trust, remove that paragraph (the attack surface is gone).

Run to find them: `grep -niE 'mcp' README.md AGENTS.md SECURITY.md`
Edit each hit; keep surrounding prose coherent.

- [ ] **Step 4: CHANGELOG.** Under `## [Unreleased]`, add a `### Removed` entry and a `### Added` entry:

```markdown
### Added

- `browser` skill: drive a real, headed, cross-platform Chrome via `puppeteer-core`
  over `bash` (open/eval/click/type/wait/screenshot/pdf), shipped in the scaffold
  (`lib/browser/` + `lib/skills/browser.lua`). Each action is a bounded command —
  no long-lived server.

### Removed

- MCP support, entirely: the `internal/mcp` package, the `shell3.mcp()` Lua
  builtin, `MCPServer`/`MCPServers`/`MCPServerNames`, MCP tool dispatch, the MCP
  manager in agent setup, the reload MCP-restart path, and the `boot --telegram
  --chrome` flag. Browser automation is now the `browser` skill (above). Configs
  that still call `shell3.mcp{}` fail loudly at load.
```

- [ ] **Step 5: Verify no stray MCP references remain in shipped code/docs.**

Run: `grep -rniE 'mcp' --include='*.go' --include='*.lua' --include='*.tmpl' internal cmd pkg docs/cookbook README.md AGENTS.md SECURITY.md CHANGELOG.md | grep -viE 'CHANGELOG|fail loudly|removed'`
Expected: no hits in code/templates/cookbook (CHANGELOG's "Removed" note is allowed). Historical `docs/dev/superpowers/specs|plans/*` are dated records and may keep their MCP mentions.

- [ ] **Step 6: Commit**

```bash
git add docs CHANGELOG.md README.md AGENTS.md SECURITY.md
git commit -m "docs: replace MCP cookbook/docs with the browser skill; CHANGELOG removed/added"
```

---

## Task 6: Final sweep + update the live bot

**Files:** none in-repo; this updates the running install for the owner to test.

- [ ] **Step 1: Full sweep**

Run: `go build ./... && go vet ./... && gofmt -l . && go test -race ./...`
Expected: all clean/green.

- [ ] **Step 2: Reinstall the binary**

Run: `make install`
Expected: builds `~/go/bin/shell3` from `feat/remove-mcp`.

- [ ] **Step 3: Switch the live telegram config to the browser skill.** The live config is `~/.shell3/telegram/shell3.lua` (the running bot's). Edit it to: remove the commented-out chrome MCP block and the `-- mcp = { chrome }` line (now dead), add `local browser = require("lib.skills.browser")` near the other requires, add `browser` to the agent's `skills = { ... }`, and copy the new helper into place:

```bash
cp -r internal/scaffold/defaults/base/lib/browser ~/.shell3/telegram/lib/
cp internal/scaffold/defaults/base/lib/skills/browser.lua ~/.shell3/telegram/lib/skills/
(cd ~/.shell3/telegram/lib/browser && npm i)
```
Then make the two `shell3.lua` edits above with the Edit tool. (Do NOT read or print `.env`.)

- [ ] **Step 4: Restart the bot and verify clean load**

```bash
pkill -f 'shell3 telegram'; sleep 2
: > ~/.shell3/telegram/run.log
nohup ~/go/bin/shell3 telegram > ~/.shell3/telegram/run.log 2>&1 & disown
sleep 4
pgrep -fl 'shell3 telegram'; cat ~/.shell3/telegram/run.log
```
Expected: bot alive, log shows "listening for chat …", no config error. Report to the owner that the `browser` skill is live (ask the bot to open a page to watch it).

- [ ] **Step 5: No commit** (live-env change only). Summarize what shipped.

---

## Self-Review

**Spec coverage:**
- `browser` skill (puppeteer-core, headed, persistent dedicated profile, verbs, install-if-missing, cross-platform via CHROME_PATH) → Task 1 (files) + Task 2 (smoke). ✓
- Skill shipped in scaffold (both renders copy `base/lib/`), wired into telegram template, cookbook recipe → Task 1 + Task 5. ✓
- Remove `internal/mcp` package + tests → Task 3 Step 1. ✓
- Remove `shell3.mcp()` builtin, `MCPServer`, `MCPServers`, `MCPServerNames`, tool-key, `__mcp`, parse blocks → Task 3 Steps 2-3. ✓
- Remove chat MCP dispatch (router arm, `dispatchMCPTool`, Config/TurnConfig/ActiveAgent fields, copy-throughs) → Task 3 Step 4. ✓
- Remove agentsetup MCP (import, fields, `MCPServerCount`, `MCPTool`, merge block, wiring, `buildMCP`, closer) → Task 3 Step 5. ✓
- Remove reload MCP-restart + `ReloadResult.MCP` + runtime/shell3 doc mentions → Task 3 Step 6. ✓
- Fix `formatReload` (functional MCP count) → Task 3 Step 7. ✓
- Remove `boot --chrome` flag/prompt/print, `TelegramValues.Chrome`, template Chrome block + grant, base-template MCP comment, Chrome tests → Task 4. ✓
- Docs (cookbook replace, README/AGENTS/SECURITY, CHANGELOG removed/added) → Task 5. ✓
- Hard removal (configs using `shell3.mcp{}` fail loudly) → asserted by Task 3 Step 9 regression. ✓
- `shell3.mcp` undefined + `tools.mcp` rejected → Task 3 Step 9. ✓
- Sequencing skill-first-then-removal; live update + restart → Tasks 1-2 before 3; Task 6. ✓

**Placeholder scan:** All code steps show complete content; removal steps quote the exact verbatim blocks to delete. The one conditional ("if `Load` requires api_key via .env, mirror an existing test") gives a concrete fallback. No TBD/TODO.

**Type consistency:** `TelegramValues` loses `Chrome` (Task 4) — its only readers are `boot.go` (Task 4 Step 5) and the deleted Chrome tests (Task 4 Steps 6-7). `ReloadResult.MCP` removed (Task 3 Step 6) — its readers are `reload.go` literal (same step) and `formatReload` (Task 3 Step 7). `MCPToolNames`/`MCPTool` removed from chat are produced only in agentsetup (Task 3 Step 5). `c.Skills` (`luacfg.Skill{Name,Description,Body}`) used in Task 1's test matches the verified `luaSkill` shape. `browser` skill handle via `return shell3.skill({...})` + `require` matches the verified `brainstorming.lua` pattern and `handleNames(sk, "__skill")` parsing.

**Template gotcha:** Task 1 adds the skill as a pure addition (Chrome block still present); Task 4 removes the Chrome `{{if}}` blocks. Keep newlines around the deleted `{{end}}` so rendered Lua stays valid — the scaffold load tests catch a broken render.
