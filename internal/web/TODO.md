# internal/web — production-hardening TODO

`shell3 web` is a working MVP, safe for **localhost / behind an authenticating
reverse proxy**. Before exposing it directly on a network (`--host 0.0.0.0`),
the items below need addressing. Tracked here deliberately; tackle as a focused
pass after the current feature work.

## 🔴 Security — blockers before exposing on a network

- [ ] **Authentication.** Any client that reaches the port gets a full agent that
      runs `bash` and edits files as the server user. Add a shared-token / basic
      auth (e.g. `--token`, `Authorization` check on every route incl. SSE), or
      require and document a mandatory authenticating reverse proxy and refuse to
      bind non-loopback without `--i-know-its-unauthenticated` style opt-in.
- [ ] **TLS.** Serve HTTPS (flag for cert/key) or, again, mandate a TLS-terminating
      proxy. Tokens over plain HTTP on a LAN are sniffable.
- [ ] **Origin / DNS-rebinding protection.** `POST /cancel` and `/clear` take no
      body and `/input` is JSON; validate the `Origin`/`Host` header against an
      allowlist so a malicious web page (or a DNS rebind to `localhost`) can't
      drive the agent.
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
