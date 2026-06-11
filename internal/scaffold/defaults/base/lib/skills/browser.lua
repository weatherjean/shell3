-- lib/skills/browser.lua — drive a real, visible Chrome via puppeteer-core. The
-- body lives in browser.md (next to this file); the agent reads it with `cat`.
-- Returned for require().
return shell3.skill({
  name        = "browser",
  description = "Drive a real, visible Chrome to open pages, read/extract content, fill forms, click, screenshot, or print to PDF. Cross-platform via puppeteer-core over bash. Use for JS-heavy or login-gated pages that web_fetch can't handle.",
  path        = "lib/skills/browser.md",
})
