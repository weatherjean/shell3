# shell3 web: Lua configuration + password auth

**Date:** 2026-06-02
**Status:** Approved (brainstorming) — ready for implementation plan

## Summary

The `shell3 web` portal currently has no configuration surface beyond two CLI
flags (`--host`, `--port`) and no authentication — any client reaching the port
gets a full agent that runs `bash` and edits files as the server user. This work
adds:

1. A Lua configuration block, `shell3.web{}`, as the single source of truth for
   how the portal serves.
2. Password authentication via a dedicated login page that sets a signed,
   week-long (configurable) cookie.
3. Origin/Host (CSRF + DNS-rebinding) protection on the HTTP routes.
4. A bind-safety guard that refuses to expose an unauthenticated agent on a
   non-loopback address.

Out of scope this pass: TLS, request body-size limits, bounded replay log,
per-client/multi-user sessions, login rate-limiting. These remain tracked in
`internal/web/TODO.md`.

## Decisions (from brainstorming)

- **Scope:** Lua config + password auth + Origin/CSRF protection (one pass).
- **Login UX:** dedicated login page that sets a signed session cookie (not HTTP
  Basic Auth) — needed for "remember for a week" and because the SSE stream
  (`EventSource`) can send same-origin cookies but not custom headers.
- **Config API:** a new top-level `shell3.web{}` function, consistent with the
  existing flat DSL (`shell3.model`, `shell3.agent`, …).
- **Flag precedence:** Lua is the only source of host/port — the `--host` and
  `--port` flags are removed.
- **Bind safety:** keep the guard — refuse to bind a non-loopback host when no
  password is configured ("let's be safe").

## Current state (reference)

- `cmd/shell3/web.go` — the `web` subcommand. Builds a raw `http.Server`
  (`web.go:152-155`) whose handler is `web.NewServer(hub, info).Handler()`. Host
  and port come from `--host` (default `127.0.0.1`) / `--port` (default `8080`)
  flags (`web.go:22-40`).
- `cmd/shell3/boot.go` — `buildChatConfig(...)` (`boot.go:39`) is the shared
  wiring core for both `run` and `web`. It calls `luacfg.Load` (`boot.go:52`),
  so the parsed Lua config (`lc`) is available here. Returns
  `(chat.Config, func(), error)`. Callers: `cmd/shell3/run.go:90`,
  `cmd/shell3/web.go:61`.
- `internal/web/server.go` — `Server{hub, info}`; `Handler()` builds a plain
  `http.ServeMux` (`server.go:46-58`) with: `GET /{$}`, `GET /meta`,
  `GET /prompt`, `GET /events` (SSE), `POST /input`, `POST /cancel`,
  `POST /clear`, `POST /model`, `POST /image`. No middleware, no auth.
- `internal/luacfg/luacfg.go` — `LoadedConfig` struct (`luacfg.go:58`) holds the
  parsed config; the Lua `LState` stays alive for the session.
- `internal/luacfg/register.go` — `registerShell3` (`register.go:5`) registers
  the top-level functions; per-block parsers use `checkKeys` + `optStr`/`optInt`
  helpers with strict key validation.
- `internal/web/TODO.md` — already flags Authentication, TLS, and Origin/CSRF as
  the blockers before exposing the portal on a network.

## Design

### 1. Lua config surface — `shell3.web{}`

A new optional top-level function. If omitted, behavior matches today's
localhost defaults with no auth.

```lua
shell3.web({
  host            = "127.0.0.1",                       -- default "127.0.0.1"
  port            = 8080,                               -- default 8080
  password        = shell3.env.secret("WEB_PASSWORD"),  -- optional; enables auth
  cookie_ttl      = "168h",                             -- default "168h" (7 days)
  allowed_origins = { "http://localhost:8080" },        -- optional; see §4
})
```

Implementation:

- New `luacfg.WebConfig` struct and a `Web WebConfig` field on `LoadedConfig`.
- `WebConfig` fields: `Host string`, `Port int`, `Password string`,
  `CookieTTL time.Duration`, `AllowedOrigins []string`. (Store the raw
  `cookie_ttl` string and parse, or parse at load time and store the
  `time.Duration` — parse at load so a bad duration is a config error.)
- A `luaWeb(L *lua.LState) int` parser registered in `registerShell3`, mirroring
  `luaModel`. Strict `webKeys` validation via `checkKeys`. `allowed_origins` is a
  Lua list of strings.
- `cookie_ttl` parsed with `time.ParseDuration`; invalid value → `L.RaiseError`.
- Since the block is optional, `shell3.web` may never be called; the absence is
  represented by the zero `WebConfig` and defaults are applied during resolution
  (see §2).

### 2. Threading & the CLI

**Guiding principle: keep web behavior inside `internal/web`.** `luacfg` only
parses raw values; `boot.go` stays a thin pass-through; all defaulting,
validation, and serving logic lives in `internal/web`.

- Remove `--host` and `--port` from `newWebCommand` (`web.go:28-40`). Keep
  `--config`.
- `internal/web` owns a `web.Config` struct (host, port, password, cookie TTL,
  allowed origins) plus two methods that hold all the behavior:
  - `Resolve()` — apply defaults (host `127.0.0.1`, port `8080`, cookie_ttl
    `168h`) to unset fields.
  - `Validate()` — the bind-safety check (§5) and any other invariants.
  `internal/web` does **not** import `luacfg`.
- `buildChatConfig` returns the parsed `luacfg.WebConfig` as a new value
  alongside `chat.Config`. `boot.go` does no web logic beyond returning it.
  Signature: `func buildChatConfig(...) (chat.Config, luacfg.WebConfig, func(), error)`.
- `run.go:90` discards the new return value (`cfg, _, cleanup, err := ...`).
- `web.go` is the only mapping point: it copies the `luacfg.WebConfig` fields
  into a `web.Config`, then calls `Resolve()` and `Validate()`, and uses the
  result for the listen address and the auth/origin middleware. This trivial
  field copy is the sole cross-package coupling.

Layering note: do not add a `luacfg` type to `chat.Config` (keeps the chat
package free of config-loader types), and do not make `internal/web` depend on
`luacfg`. `cmd/shell3` is the wiring layer that imports both.

### 3. Authentication — login page + signed cookie

Stateless HMAC cookie; no server-side session store.

- **Signing key derived from the password:** `key = sha256(password)`, cookie
  MAC = `HMAC-SHA256(key, payload)`. Rotating the password instantly invalidates
  all existing cookies — no separate secret to manage or persist.
- **Cookie format:** `base64url(expiryUnix) "." hex(HMAC(key, expiryUnix-bytes))`.
  Verify: recompute MAC with `hmac.Equal` (constant time) and check
  `now < expiry`.
- **Cookie attributes:** `HttpOnly; SameSite=Lax; Path=/; Max-Age=<ttl seconds>`.
  No `Secure` flag this pass (TLS is out of scope; note in TODO that `Secure`
  should be set once TLS lands or when served behind an HTTPS proxy).
- **Routes:**
  - `GET /login` — serve a small embedded HTML form. If the request already has
    a valid cookie, `303 → /`.
  - `POST /login` — read `password` form field; `subtle.ConstantTimeCompare`
    against the configured password; on success set cookie and `303 → /`; on
    failure re-render the form with a generic error message (no
    user-enumeration detail).
  - `POST /logout` — clear the cookie (`Max-Age=0`) and `303 → /login`.
- **Auth middleware** wraps the whole mux:
  - If no password is configured → auth disabled; all routes pass through
    (unchanged localhost behavior).
  - If a password is set → every route except `/login` and `/logout` requires a
    valid cookie:
    - Navigation routes (`GET /`) → `302 → /login`.
    - SSE (`/events`) and API POST routes → `401` (the SPA detects this and
      redirects to `/login`; `EventSource` cannot read a redirect body, so an
      explicit status is required).

### 4. CSRF / Origin protection

Two layers, both active regardless of whether auth is enabled (DNS-rebinding
targets localhost as well):

- **`SameSite=Lax` cookie** — primary CSRF defense; browsers won't attach the
  cookie to cross-site POSTs.
- **Origin + Host allowlist** (defense-in-depth + anti-DNS-rebinding):
  - On state-changing POSTs (`/input`, `/image`, `/cancel`, `/clear`, `/model`,
    `/login`, `/logout`): require an `Origin` header whose value is in the
    allowed set.
  - On all routes: validate the `Host` header against the allowed hosts.
  - The allowed set defaults to the bind origin plus `localhost` / `127.0.0.1`
    equivalents at the configured port; `allowed_origins` from Lua extends it
    (e.g. a public hostname when behind a reverse proxy).
  - Violations → `403`.

### 5. Bind safety

When the resolved `host` is non-loopback (anything other than `127.0.0.1` /
`::1` / `localhost`) AND no password is configured, refuse to start with a clear
error that points at `shell3.web{ password = … }`. Implemented as
`web.Config.Validate()` (so the rule lives in `internal/web`), called from
`runWeb` before `ListenAndServe`. Only triggers in the genuinely dangerous
configuration.

### 6. Components / files touched

- `internal/luacfg/luacfg.go` — `WebConfig` struct (raw parsed values) + `Web`
  field on `LoadedConfig`. Parsing only; no defaulting or validation.
- `internal/luacfg/register.go` (or a new `internal/luacfg/web.go`) — `luaWeb`
  parser, `webKeys`, registration in `registerShell3`.
- `cmd/shell3/boot.go` — thin pass-through: return the parsed `luacfg.WebConfig`
  alongside `chat.Config`. No web logic.
- `cmd/shell3/run.go` — discard the new return value.
- `cmd/shell3/web.go` — remove `--host`/`--port`; map `luacfg.WebConfig` →
  `web.Config`, call `Resolve()` + `Validate()`, wire into the listen address and
  server middleware.
- `internal/web/config.go` *(new)* — `web.Config` struct + `Resolve()`
  (defaults) + `Validate()` (bind safety). All web behavior lives here, not in
  `cmd`/`boot`/`luacfg`. No import of `luacfg`.
- `internal/web/server.go` — accept the resolved `web.Config` in `NewServer`;
  add the middleware chain and `/login` + `/logout` handlers.
- `internal/web/auth.go` *(new)* — cookie sign/verify, password compare, auth +
  origin middleware.
- `internal/web/assets/login.html` *(new)* — login form. Small tweak to
  `index.html` to redirect to `/login` on a `401` from `/events` or a fetch.
- `internal/scaffold/defaults/shell3.lua` — add an example `shell3.web{}` block
  (with `password = shell3.env.secret("WEB_PASSWORD")` and a `.env` note).
- `internal/web/TODO.md` — tick off Authentication and Origin/CSRF; note TLS
  (incl. cookie `Secure` flag), body-size limits, bounded replay log, and
  per-client sessions remain open.

### 7. Testing

- `internal/web/auth_test.go`:
  - Cookie round-trip sign → verify success.
  - Expired cookie rejected.
  - Tampered cookie (bad MAC) rejected.
  - Wrong password rejected; correct password accepted (constant-time path).
  - Password rotation invalidates a previously valid cookie.
- Middleware tests (httptest):
  - No password configured → all routes open.
  - With password → unauthenticated matrix: `GET /` → 302; `/events` → 401;
    POST routes → 401; `/login` reachable.
  - Authenticated cookie → routes pass.
  - Origin/Host: same-origin allowed; foreign Origin on POST → 403; foreign Host
    → 403; `allowed_origins` entry accepted.
- `internal/web` `Config.Validate()` bind-safety: non-loopback host + no
  password → error; loopback or password set → ok. `Config.Resolve()` applies
  defaults to unset fields.
- `luacfg` parse tests: `shell3.web{}` parses fields; bad `cookie_ttl` → error;
  unknown key → error (via `checkKeys`).

## Open questions

None outstanding. Bind-safety guard confirmed kept. Web behavior (defaults,
validation, auth, origin) is scoped to `internal/web`; `luacfg`/`boot`/`chat`
spillover is limited to raw parsing and a thin pass-through.

## Execution

Implement via subagents after the plan is written (parallel/subagent-driven
development), per the agreed workflow.
