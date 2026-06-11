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
