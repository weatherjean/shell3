# internal/web — production-hardening TODO

`shell3 web` is a working MVP with built-in password authentication (signed
cookie via `shell3.web{ password = ... }`), safe for **localhost or direct
network exposure with a password set**. TLS is not built in — for HTTPS use a
TLS-terminating reverse proxy. Before exposing without TLS, the items below
need addressing. Tracked here deliberately; tackle as a focused pass after the
current feature work.

## 🔴 Security — blockers before exposing on a network

- [x] **Authentication.** Password login via `shell3.web{ password = ... }` sets a
      signed, HMAC session cookie (key derived from the password; rotating the
      password invalidates all sessions). Auth is enforced on every route incl.
      SSE; loopback with no password stays open for local use. Binding a
      non-loopback host without a password is refused at startup.
- [ ] **TLS.** Serve HTTPS (flag for cert/key) or mandate a TLS-terminating
      proxy. Also set the cookie `Secure` flag once served over HTTPS.
- [x] **Origin / DNS-rebinding protection.** `Host` is validated against the
      configured origin allowlist on every request and `Origin` on every POST;
      the session cookie is `SameSite=Lax`.
- [ ] **Command execution risk.** The agent runs arbitrary shell with the process's
      privileges; `confirm_dangerous` is a denylist, not a sandbox. Consider a
      restricted mode / container guidance for exposed deployments.

## 🟠 Robustness — for genuinely long-running servers

- [ ] **Bound the in-memory replay log.** `Hub.log` only resets on `/clear`, so a
      long session grows unboundedly. Cap to last N events or M bytes (ring
      buffer), and note dropped-prefix on reconnect.
- [ ] **Request body size limit.** Wrap `/input` (and `/model`) bodies with
      `http.MaxBytesReader`.
- [ ] **SSE backpressure tuning.** Per-subscriber buffer is 256 with drop-on-full;
      revisit for very chatty turns / slow clients.

## 🟡 Multi-user & quality

- [ ] **Per-client sessions.** Currently one shared session for all browsers
      (by design). For multi-user, add a session registry; the Hub is the natural
      per-session unit.
- [ ] **SPA tests.** The embedded JS (markdown renderer, autoscroll, slash
      handling) is manual-only — add a headless/browser or unit test harness.
- [ ] **Markdown renderer** is intentionally minimal; extend for edge cases as
      needed.
- [ ] **Observability.** Add request logging / basic metrics.
- [ ] **Token/context counter** in the thinking bar (from `usage` events).
