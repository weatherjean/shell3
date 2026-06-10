# SearXNG local search tool — design

**Date:** 2026-06-05
**Branch:** `feat/searxng-search`
**Status:** Approved (design)

## Goal

Give shell3 agents a free, private web-search tool backed by a **local
SearXNG container** that the tool itself starts on demand. No external API
keys, no public-instance rate limits, no Go code changes — the whole thing
is a custom Lua tool defined in `shell3.lua`.

## Background / why local

Public SearXNG instances were empirically rejected: every A+ instance on
searx.space returned `429` on a cold first request (the SearXNG bot limiter
blocks non-browser clients), even with a browser User-Agent. The JSON API
(`format=json`) is also disabled by default on most public instances. Running
a local container is the documented, common pattern for agent search backends
and is the only reliable option here.

## Decisions (locked)

- **Bootstrap:** lazy self-bootstrap. The tool handler runs an idempotent
  shell script on every call that brings the container up only if it is down,
  then performs the search. Zero Go changes; survives restarts; the first
  search of a session pays the cold-start cost.
- **Port:** hardcoded constant `47291` (host) → `8080` (container). Single
  point of change.
- **Config location:** the tool is added to `internal/scaffold/defaults/shell3.lua`
  (ships to all new installs via bootstrap) **and** mirrored into the user's
  active `~/.shell3/shell3.lua` so it can be tested immediately on this branch.
- **Base URL:** hardcoded `http://localhost:47291` (not env-configurable).
- **macOS:** Podman on darwin runs in a VM. The lazy script handles
  `podman machine start` when the machine exists but is stopped. The one-time
  `podman machine init` (large VM image download) is a documented manual step,
  not automated.

## Components

### 1. The tool: `searxng_search`

Defined via `shell3.tool{}` and enabled on the default agent's
`tools.custom`. Parameters:

```
{
  type = "object",
  properties = { query = { type = "string", description = "search query" } },
  required = { "query" },
}
```

Handler flow:
1. Run the bootstrap script via `shell3.bash(script, { timeout = 120 })`.
   First run pulls the image (slow); subsequent runs are near-instant no-ops.
   If the script reports failure, return a diagnostic string (see Error
   handling) instead of proceeding.
2. `shell3.http.get("http://localhost:47291/search?format=json&q=" .. shell3.urlencode(query), { timeout = 20, max_bytes = 200000 })`.
3. Return the JSON body (capped by `max_bytes`). Raw JSON for v1 — there is no
   guaranteed JSON decoder in the Lua VM, so pretty-formatting is deferred.

The exact field names returned by `shell3.http.get` / `shell3.bash` will be
copied from the existing `web_fetch` / `brave_search` examples in the scaffold
`shell3.lua` at implementation time.

### 2. Bootstrap script (idempotent)

Run inside the handler. Behaviour:

1. **Podman machine (macOS):** if `podman machine inspect` succeeds and state
   is not `running`, run `podman machine start`. Harmless/skipped on Linux.
2. **Config:** if `~/.shell3/searxng/settings.yml` is missing, create it:
   ```yaml
   use_default_settings: true
   server:
     secret_key: "<generated via: openssl rand -hex 32>"
     bind_address: "0.0.0.0"
   search:
     formats:
       - html
       - json
   ```
   Enabling the `json` format is the critical bit — it is **off** by default.
3. **Container:** if `searxng` is not in `podman ps`, run:
   ```
   podman run -d --name searxng -p 47291:8080 \
     -v ~/.shell3/searxng:/etc/searxng:Z \
     docker.io/searxng/searxng:latest
   ```
   (`:Z` is the Podman SELinux relabel flag.)
4. **Readiness:** poll `http://localhost:47291/` until it answers (≤30s).

The default SearXNG limiter (botdetection) requires Redis/Valkey and is off by
default; we do **not** enable it, so the container does not 429 our own
requests and needs no extra services.

### 3. Error handling

The handler never throws; on failure it returns a clear string so the agent
can relay it:
- `podman` not found → "SearXNG unavailable: podman not installed."
- `podman machine` not initialized → include the one-time setup hint
  (`podman machine init`).
- Container fails to become ready → include the tail of `podman logs searxng`.

## Where it lives

- `internal/scaffold/defaults/shell3.lua` — define the tool and enable it on
  the default agent. A short comment documents the one-time `podman machine init`
  setup.
- `~/.shell3/shell3.lua` (user's active config, not in repo) — mirror the same
  tool so it can be tested on this branch.

## Testing

1. **Automated:** `go test ./...` must still pass. The scaffold `shell3.lua`
   is parsed/validated by existing luacfg/scaffold tests, so a syntax or
   schema error in the new tool is caught there.
2. **Manual integration (on branch):** launch shell3, ask the agent to run a
   web search, confirm the container is brought up and JSON results return.
   Re-run to confirm the second call is a fast no-op. Requires podman installed
   and `podman machine init` done once.

## Out of scope (YAGNI)

- Env-configurable base URL / port.
- A real Go `on_startup` lifecycle hook (could warm the container ahead of
  time; revisit only if cold-start latency is annoying).
- Pretty-formatting / dedup / scoring of results (return raw JSON for now).
- Auto-running `podman machine init`.
