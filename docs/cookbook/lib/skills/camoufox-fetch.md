---
name: camoufox-fetch
description: Fetch bot-protected or JS-heavy pages as rendered text via Camoufox, an anti-detect Firefox. Use when plain curl/web-fetch gets blocked (Cloudflare, DataDome) or returns an empty JS shell
---

# Camoufox Fetch Skill

Camoufox is a hardened Firefox fork (fingerprints patched at the C++ level,
driven through Playwright) that reads pages plain HTTP fetching can't:
Cloudflare-walled docs, JS-rendered apps, sites that 403 curl. Reach for it
only after a normal fetch fails — it's a full browser per call, so it is
slow (~5-15s) and heavy.

## Setup (once)

**Prerequisite: Python 3.10+.** Check first: `python3 -V`. If the system
python is older, don't fight it — ask the user whether to install
[uv](https://docs.astral.sh/uv/) (one command, no root:
`curl -LsSf https://astral.sh/uv/install.sh | sh`), then create a dedicated
env and use its interpreter everywhere below:

```bash
uv venv --python 3.12 ~/.shell3/lib/camoufox-env
```

Also confirm the user is OK with the download — the browser is ~1.2 GB on
disk. Then:

```bash
~/.shell3/lib/camoufox-env/bin/pip install -U "camoufox[geoip]"   # or plain pip if python3 >= 3.10
~/.shell3/lib/camoufox-env/bin/python -m camoufox fetch
```

Wrapper script — `~/.shell3/lib/bin/camoufox-fetch` (create and `chmod +x`
if missing):

```python
#!/usr/bin/env python3
# camoufox-fetch <url> [selector] — rendered page text via anti-detect Firefox
# shebang: point at the interpreter that has camoufox installed
# (e.g. ~/.shell3/lib/camoufox-env/bin/python if you made the uv env above)
import sys
from camoufox.sync_api import Camoufox

url = sys.argv[1] if len(sys.argv) > 1 else sys.exit("usage: camoufox-fetch <url> [selector]")
sel = sys.argv[2] if len(sys.argv) > 2 else "body"
with Camoufox(headless=True, humanize=True) as browser:
    page = browser.new_page()
    page.goto(url, wait_until="domcontentloaded", timeout=60_000)
    page.wait_for_timeout(2_500)  # let JS challenges / hydration settle
    print(page.inner_text(sel)[:20_000])
```

## Usage

```bash
camoufox-fetch "https://protected.example.com/article"
camoufox-fetch "https://app.example.com/docs" "main"   # scope to a selector
```

- Pass a CSS selector to skip nav/footer noise; output is capped at 20k
  chars — refine the selector rather than raising the cap.
- Escalation ladder: `web-fetch` (curl) → this script → the browser skill
  (headed Chrome) when you need clicks, forms, or a login session.
- A blocked page usually shows challenge text ("Verifying you are
  human…") — retry once; persistent walls from a datacenter IP need a
  residential proxy (`Camoufox(proxy={...}, geoip=True)` derives locale and
  timezone from the proxy IP).

## Notes

- Linux servers: `headless="virtual"` runs it head-fully under Xvfb
  (`apt install xvfb`) — noticeably stealthier than plain headless.
- Each call launches a fresh browser (fresh fingerprint, no state). For many
  fetches in one task there is an experimental
  `python -m camoufox server` websocket mode any Playwright client can
  connect to, but the one-shot script is the reliable default.
- Project status (July 2026): actively maintained again after a 2025 gap —
  Clover Labs took over maintenance, current builds track Firefox 152, and
  the PyPI package is `camoufox` 0.5.x. Update with
  `pip install -U "camoufox[geoip]" && python -m camoufox fetch`.
