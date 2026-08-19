---
name: dash-exposing
description: Use when the user wants to reach the web dash from another device (asked via /dash <text> or in conversation) — check what tunnelling tool is ALREADY installed and configured, use it, and never install anything.
---

# Exposing the web dash

The dash listens on 127.0.0.1 only. Its base URL is in `dash_url.txt` in the
config dir (`cat` it for the port). Your job: make that page reachable from
the user's other device using a tunnel that is **already installed and
configured on this machine**.

## HARD RULES — read first

- **NEVER install anything.** No `brew install`, `apt`, `npm`, `pip`,
  `curl | sh`, no downloading binaries. If the tool the user needs isn't
  already here, you STOP and tell them — you do not install it for them.
- **Only use a tool that is BOTH present AND configured** (logged in / has
  credentials). A binary that exists but isn't set up does not count.
- One tunnel is enough. The moment one works, stop — don't try the others.
- Never bind the dash to 0.0.0.0, never proxy it without its token, never
  expose any port but the dash's.

## Step 1 — probe, don't assume

Run these checks and read the results before doing anything. Each has TWO
parts: is the binary there, and is it actually usable.

- **Tailscale** (preferred — private, no public exposure):
  `command -v tailscale` AND `tailscale status` exits 0 (0 = logged in and
  up; an error means installed-but-not-running, which does NOT count).
  If usable: `tailscale ip -4` gives the address the other device uses, or
  run `tailscale serve --bg <port>` for an HTTPS front. No install, no login
  prompts — if it's not already up, skip it.
- **cloudflared** (public URL, but token-gated and zero-config):
  `command -v cloudflared`. The quick-tunnel mode needs no account, so the
  binary being present IS "configured".
- **ngrok**: `command -v ngrok` AND an authtoken is set
  (`ngrok config check` exits 0, or `~/.config/ngrok/ngrok.yml` /
  `~/Library/Application Support/ngrok/ngrok.yml` has an `authtoken`). A bare
  ngrok with no authtoken will fail — treat it as not configured.

## Step 2 — if NOTHING is usable, STOP

Report to the user, in plain language: which tools you found, which were
missing or not set up, and what they would need to do (e.g. "tailscale is
installed but not logged in — run `tailscale up`", or "none of tailscale /
cloudflared / ngrok is installed; install one and set it up, then ask me
again"). Do NOT install anything, do NOT proceed. This is a successful
outcome — an honest "here's what's needed" beats a broken guess.

## Step 3 — start the tunnel (only a usable tool)

Start it as a `bash_bg` job so it lives with the shell3 process, shows up in
the dash, and dies on /superstop:

- Tailscale serve: `tailscale serve --bg <port>` (or just hand over the
  `tailscale ip -4` address — no job needed).
- cloudflared: `cloudflared tunnel --url http://127.0.0.1:<port>` — scrape
  the `https://….trycloudflare.com` line from the job output.
- ngrok: `ngrok http <port>`, then read the public URL from
  `curl -s http://127.0.0.1:4040/api/tunnels`.

## Step 4 — hand off

- Write ONLY the public base URL (scheme + host, no path, no token) as the
  single line of `dash_url.txt`, replacing what's there.
- Tell the user the tunnel is a background job (it dies with shell3 or on
  /superstop), and to tap **/dash** for a fresh tokened link — you cannot
  mint tokens (host-minted, in-memory, ~1h). A URL without `?t=` shows 403.
- If it's a public tunnel (cloudflared/ngrok), say so: the link is reachable
  by anyone who has it until the ~1h token expires.
