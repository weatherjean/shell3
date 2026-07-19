---
name: camoufox-fetch
description: Fetch bot-protected or JS-heavy pages as rendered text via Camoufox, an anti-detect Firefox in a one-shot docker container. Use when plain curl/web-fetch gets blocked (Cloudflare, DataDome) or returns an empty JS shell
---

# Camoufox Fetch Skill

Camoufox is a hardened Firefox fork (fingerprints patched at the C++ level,
driven through Playwright) that reads pages plain HTTP fetching can't:
Cloudflare-walled docs, JS-rendered apps, sites that 403 curl. It runs as a
one-shot docker container per fetch — no host python, fresh fingerprint
every call. Reach for it only after a normal fetch fails: each call boots a
full browser (~15-30s) inside a ~2 GB image.

## Setup (once)

**Prerequisite: docker.** Check first: `command -v docker && docker info
>/dev/null 2>&1 && echo ok`. If missing, stop and tell the user what to
install (Docker Desktop or OrbStack on macOS, the distro package on Linux).

Fetch the image sources and build (the build downloads the browser, so it
takes a few minutes and ~2 GB of image):

```bash
base=https://raw.githubusercontent.com/weatherjean/shell3/main/docs/cookbook
mkdir -p ~/.shell3/lib/camoufox
curl -fsS "$base/lib/camoufox/Dockerfile" -o ~/.shell3/lib/camoufox/Dockerfile
curl -fsS "$base/lib/camoufox/fetch.py"   -o ~/.shell3/lib/camoufox/fetch.py
docker build -t shell3-camoufox ~/.shell3/lib/camoufox
```

Wrapper — `~/.shell3/lib/bin/camoufox-fetch`, `chmod +x`:

```bash
#!/usr/bin/env bash
# camoufox-fetch <url> [selector] — rendered page text via anti-detect Firefox
exec docker run --rm shell3-camoufox "$@"
```

Verify: `~/.shell3/lib/bin/camoufox-fetch "https://example.com"` should
print the page text.

## Usage

```bash
~/.shell3/lib/bin/camoufox-fetch "https://protected.example.com/article"
~/.shell3/lib/bin/camoufox-fetch "https://app.example.com/docs" "main"   # scope to a selector
```

- Pass a CSS selector to skip nav/footer noise; output is capped at 20k
  chars — refine the selector rather than raising the cap.
- **Typical workflow:** `searxng-search` to find results → `camoufox-fetch`
  to read the actual pages (works where curl gets blocked).
- Escalation ladder: `web-fetch` (curl) → `camoufox-fetch` (anti-detect FF)
  → the **browser skill** (headed Chrome) when you need clicks, forms, or
  a login session.
- A blocked page usually shows challenge text ("Verifying you are
  human…") — retry once (each run is a fresh fingerprint); persistent walls
  from a datacenter IP need a residential proxy (add
  `proxy={...}, geoip=True` to `fetch.py`'s `Camoufox(...)` — geoip derives
  locale and timezone from the proxy IP).

## Notes

- Each call is an ephemeral container: fresh fingerprint, no state, no
  idle RAM between fetches.
- Update occasionally (fingerprint/browser fixes ship upstream):
  `docker build --no-cache -t shell3-camoufox ~/.shell3/lib/camoufox`.
- No docker on this host? There is a pip route: with Python 3.10+ (or
  `uv venv --seed --python 3.12 <env>`), `pip install -U "camoufox[geoip]"`,
  `python -m camoufox fetch` (~1 GB into the user cache), then run
  `fetch.py` with that interpreter.
- Project status (July 2026): actively maintained again after a 2025 gap —
  Clover Labs took over, current builds track Firefox 152, PyPI package
  `camoufox` 0.5.x.
