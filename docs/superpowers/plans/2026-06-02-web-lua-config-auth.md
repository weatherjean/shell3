# Web Lua Config + Password Auth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Configure the `shell3 web` portal entirely from `shell3.lua` and add password authentication (signed week-long cookie) plus Origin/Host CSRF protection.

**Architecture:** `luacfg` parses a new optional `shell3.web{}` block into a raw `WebConfig` (no logic). `buildChatConfig` passes it through. `cmd/shell3/web.go` maps it into a `web.Config`, calls `Resolve()` (defaults) + `Validate()` (bind safety), and hands it to `web.NewServer`. All serving behavior — defaults, validation, auth middleware, origin middleware, login/logout — lives in `internal/web`. The cookie is a stateless HMAC token whose key is derived from the password.

**Tech Stack:** Go, `github.com/yuin/gopher-lua`, Go stdlib `net/http`, `crypto/hmac`, `crypto/sha256`, `crypto/subtle`. Tests use `net/http/httptest` and the existing `writeFile`/`Load` luacfg test helpers.

**Spec:** `docs/superpowers/specs/2026-06-02-web-lua-config-auth-design.md`

---

## File Structure

**Create:**
- `internal/web/config.go` — `web.Config` struct + `Resolve()` (defaults), `Validate()` (bind safety), `Addr()`, and the host/origin allow helpers. No `luacfg` import.
- `internal/web/config_test.go` — unit tests for the above.
- `internal/web/auth.go` — cookie sign/verify, password compare, auth + origin middleware, login/logout handlers.
- `internal/web/auth_test.go` — unit + middleware tests.
- `internal/web/assets/login.html` — login form page.
- `internal/luacfg/web.go` — `luaWeb` parser + `webKeys`.

**Modify:**
- `internal/luacfg/luacfg.go` — add `WebConfig` struct + `Web` field on `LoadedConfig`; add `time` import.
- `internal/luacfg/convert.go` — add `stringList` helper.
- `internal/luacfg/register.go` — register `shell3.web`.
- `internal/web/server.go` — `Server` carries `cfg Config`; `NewServer` takes it; `Handler()` wraps middleware and registers `/login` + `/logout`.
- `internal/web/server_test.go` — update `newTestServer` for the new `NewServer` signature.
- `internal/web/assets.go` — embed `login.html`.
- `internal/web/assets/index.html` — redirect to `/login` on a 401.
- `internal/web/TODO.md` — tick off auth + Origin/CSRF.
- `cmd/shell3/boot.go` — return `luacfg.WebConfig` from `buildChatConfig`.
- `cmd/shell3/run.go` — discard the new return value.
- `cmd/shell3/web.go` — drop `--host`/`--port`; map + resolve + validate; wire into server.
- `internal/scaffold/defaults/shell3.lua` — example `shell3.web{}` block.
- `internal/scaffold/defaults/env.example` — add `WEB_PASSWORD=`.

**Behavioral conventions (relied on by tests and by the wiring):**
- `web.Config` with empty `Password` → **auth guard disabled** (current localhost behavior).
- `web.Config` with empty `AllowedOrigins` → **origin/host guard disabled**.
- `Config.Resolve()` always populates `AllowedOrigins`, so in production both guards are active. Tests opt into a guard by setting the relevant field.

---

## Task 1: Parse `shell3.web{}` in luacfg

**Files:**
- Modify: `internal/luacfg/luacfg.go` (add struct + field + `time` import)
- Modify: `internal/luacfg/convert.go` (add `stringList`)
- Create: `internal/luacfg/web.go`
- Modify: `internal/luacfg/register.go:5-24`
- Test: `internal/luacfg/web_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/luacfg/web_test.go`:

```go
package luacfg

import (
	"testing"
	"time"
)

func TestLoadWeb(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.web({
  host = "0.0.0.0",
  port = 9000,
  password = "hunter2",
  cookie_ttl = "24h",
  allowed_origins = { "https://app.example.com" },
})
shell3.agent({ name="a", model="m", prompt="p", tools={} })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	w := c.Web
	if !w.Set {
		t.Fatal("Web.Set = false, want true")
	}
	if w.Host != "0.0.0.0" || w.Port != 9000 || w.Password != "hunter2" {
		t.Fatalf("bad web config: %+v", w)
	}
	if w.CookieTTL != 24*time.Hour {
		t.Fatalf("cookie_ttl = %v, want 24h", w.CookieTTL)
	}
	if len(w.AllowedOrigins) != 1 || w.AllowedOrigins[0] != "https://app.example.com" {
		t.Fatalf("allowed_origins = %v", w.AllowedOrigins)
	}
}

func TestLoadWebOmitted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={} })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Web.Set {
		t.Fatal("Web.Set = true with no shell3.web block")
	}
}

func TestLoadWebBadTTL(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua",
		`shell3.web({ cookie_ttl = "not-a-duration" })`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil || !contains(err.Error(), "cookie_ttl") {
		t.Fatalf("want cookie_ttl parse error, got %v", err)
	}
}

func TestLoadWebUnknownKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `shell3.web({ nope = 1 })`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil || !contains(err.Error(), `unknown key "nope"`) {
		t.Fatalf("want strict-key failure, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/luacfg/ -run TestLoadWeb -v`
Expected: FAIL — compile error (`c.Web` undefined / `shell3.web` not registered).

- [ ] **Step 3: Add the `WebConfig` struct + field**

In `internal/luacfg/luacfg.go`, add `"time"` to the import block, then add the struct after the `Model` struct (around line 19) and the field to `LoadedConfig`:

```go
// WebConfig is the raw parsed shell3.web{} block. Defaulting, validation, and
// all serving behavior live in internal/web — this struct only carries values.
type WebConfig struct {
	Set            bool // true if shell3.web{} was called
	Host           string
	Port           int
	Password       string
	CookieTTL      time.Duration
	AllowedOrigins []string
}
```

Add to the `LoadedConfig` struct (after `Secrets map[string]string`):

```go
	Web     WebConfig
```

- [ ] **Step 4: Add the `stringList` helper**

In `internal/luacfg/convert.go`, add:

```go
// stringList collects the string values of a Lua array table, skipping non-strings.
func stringList(t *lua.LTable) []string {
	var out []string
	t.ForEach(func(_, v lua.LValue) {
		if s, ok := v.(lua.LString); ok {
			out = append(out, string(s))
		}
	})
	return out
}
```

- [ ] **Step 5: Add the `luaWeb` parser**

Create `internal/luacfg/web.go`:

```go
package luacfg

import (
	"time"

	lua "github.com/yuin/gopher-lua"
)

var webKeys = map[string]bool{
	"host": true, "port": true, "password": true,
	"cookie_ttl": true, "allowed_origins": true,
}

func (c *LoadedConfig) luaWeb(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "web", webKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	w := WebConfig{
		Set:      true,
		Host:     optStr(opts, "host"),
		Port:     optInt(opts, "port"),
		Password: optStr(opts, "password"),
	}
	if ttl := optStr(opts, "cookie_ttl"); ttl != "" {
		d, err := time.ParseDuration(ttl)
		if err != nil {
			L.RaiseError("web: invalid cookie_ttl %q: %v", ttl, err)
		}
		w.CookieTTL = d
	}
	if ao, ok := opts.RawGetString("allowed_origins").(*lua.LTable); ok {
		w.AllowedOrigins = stringList(ao)
	}
	c.Web = w
	return 0
}
```

- [ ] **Step 6: Register `shell3.web`**

In `internal/luacfg/register.go`, add this line in `registerShell3` after the `agent` registration (line 12):

```go
	L.SetField(tbl, "web", L.NewFunction(c.luaWeb))
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/luacfg/ -run TestLoadWeb -v`
Expected: PASS (all four).

- [ ] **Step 8: Commit**

```bash
git add internal/luacfg/luacfg.go internal/luacfg/convert.go internal/luacfg/web.go internal/luacfg/register.go internal/luacfg/web_test.go
git commit -m "feat(luacfg): parse shell3.web{} config block"
```

---

## Task 2: `web.Config` defaults, bind-safety, allow helpers

**Files:**
- Create: `internal/web/config.go`
- Test: `internal/web/config_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/web/config_test.go`:

```go
package web

import (
	"testing"
	"time"
)

func TestConfigResolveDefaults(t *testing.T) {
	c := Config{}.Resolve()
	if c.Host != "127.0.0.1" {
		t.Errorf("host = %q, want 127.0.0.1", c.Host)
	}
	if c.Port != 8080 {
		t.Errorf("port = %d, want 8080", c.Port)
	}
	if c.CookieTTL != 7*24*time.Hour {
		t.Errorf("cookie ttl = %v, want 168h", c.CookieTTL)
	}
	if !containsStr(c.AllowedOrigins, "http://127.0.0.1:8080") ||
		!containsStr(c.AllowedOrigins, "http://localhost:8080") {
		t.Errorf("default origins missing: %v", c.AllowedOrigins)
	}
}

func TestConfigResolveKeepsExplicit(t *testing.T) {
	c := Config{Host: "0.0.0.0", Port: 9000, CookieTTL: time.Hour,
		AllowedOrigins: []string{"https://app.test"}}.Resolve()
	if c.Port != 9000 || c.CookieTTL != time.Hour {
		t.Fatalf("explicit values overwritten: %+v", c)
	}
	if !containsStr(c.AllowedOrigins, "https://app.test") {
		t.Fatalf("user origin dropped: %v", c.AllowedOrigins)
	}
}

func TestConfigAddr(t *testing.T) {
	if a := (Config{Host: "127.0.0.1", Port: 8080}).Addr(); a != "127.0.0.1:8080" {
		t.Fatalf("addr = %q", a)
	}
}

func TestConfigValidateBindSafety(t *testing.T) {
	if err := (Config{Host: "0.0.0.0", Port: 8080}).Resolve().Validate(); err == nil {
		t.Fatal("want error binding 0.0.0.0 without password")
	}
	if err := (Config{Host: "0.0.0.0", Password: "x"}).Resolve().Validate(); err != nil {
		t.Fatalf("0.0.0.0 + password should be ok, got %v", err)
	}
	if err := (Config{}).Resolve().Validate(); err != nil {
		t.Fatalf("loopback default should be ok, got %v", err)
	}
}

func TestHostAndOriginAllowed(t *testing.T) {
	c := Config{AllowedOrigins: []string{"http://app.test:8080"}}
	if !c.hostAllowed("app.test:8080") {
		t.Error("host should be allowed")
	}
	if c.hostAllowed("evil.test:8080") {
		t.Error("foreign host should be rejected")
	}
	if !c.originAllowed("http://app.test:8080") {
		t.Error("origin should be allowed")
	}
	if c.originAllowed("http://evil.test:8080") {
		t.Error("foreign origin should be rejected")
	}
	if c.originAllowed("") {
		t.Error("empty origin must be rejected on POST")
	}
}

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/ -run 'TestConfig|TestHostAndOrigin' -v`
Expected: FAIL — `Config` undefined.

- [ ] **Step 3: Write `internal/web/config.go`**

```go
package web

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHost      = "127.0.0.1"
	defaultPort      = 8080
	defaultCookieTTL = 7 * 24 * time.Hour
)

// Config is the resolved web-serving configuration. It owns all defaulting,
// validation, and request-allow logic so cmd/boot/luacfg stay free of web
// behavior. An empty Password disables the auth guard; empty AllowedOrigins
// disables the origin/host guard.
type Config struct {
	Host           string
	Port           int
	Password       string
	CookieTTL      time.Duration
	AllowedOrigins []string
}

// Resolve fills unset fields with defaults and prepends the same-origin
// defaults to AllowedOrigins. Safe to call once during wiring.
func (c Config) Resolve() Config {
	if c.Host == "" {
		c.Host = defaultHost
	}
	if c.Port == 0 {
		c.Port = defaultPort
	}
	if c.CookieTTL == 0 {
		c.CookieTTL = defaultCookieTTL
	}
	c.AllowedOrigins = append(c.defaultOrigins(), c.AllowedOrigins...)
	return c
}

// defaultOrigins are the loopback + configured-host origins always trusted.
func (c Config) defaultOrigins() []string {
	port := strconv.Itoa(c.Port)
	hosts := []string{"127.0.0.1", "localhost", "::1"}
	if c.Host != "" && c.Host != "0.0.0.0" && c.Host != "::" {
		hosts = append(hosts, c.Host)
	}
	var out []string
	for _, h := range hosts {
		out = append(out, "http://"+net.JoinHostPort(h, port))
	}
	return out
}

// Addr is the listen address for net/http.
func (c Config) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// Validate refuses to bind a non-loopback host without a password.
func (c Config) Validate() error {
	if c.Password == "" && !isLoopback(c.Host) {
		return fmt.Errorf("refusing to bind %s without a password: set "+
			"shell3.web{ password = ... } in shell3.lua, or bind a loopback "+
			"address (127.0.0.1) for local-only use", c.Host)
	}
	return nil
}

func isLoopback(host string) bool {
	switch host {
	case "", "localhost", "127.0.0.1", "::1":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// hostAllowed reports whether the request Host header matches a trusted origin.
func (c Config) hostAllowed(host string) bool {
	for _, o := range c.AllowedOrigins {
		if stripScheme(o) == host {
			return true
		}
	}
	return false
}

// originAllowed reports whether the request Origin header is trusted. An empty
// Origin is never allowed (callers only consult this on state-changing POSTs).
func (c Config) originAllowed(origin string) bool {
	if origin == "" {
		return false
	}
	for _, o := range c.AllowedOrigins {
		if o == origin {
			return true
		}
	}
	return false
}

func stripScheme(o string) string {
	if i := strings.Index(o, "://"); i >= 0 {
		return o[i+3:]
	}
	return o
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/web/ -run 'TestConfig|TestHostAndOrigin' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/web/config.go internal/web/config_test.go
git commit -m "feat(web): Config with defaults, bind-safety, origin allowlist"
```

---

## Task 3: Cookie sign/verify + password compare

**Files:**
- Create: `internal/web/auth.go` (token + password helpers only this task)
- Test: `internal/web/auth_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/web/auth_test.go`:

```go
package web

import (
	"testing"
	"time"
)

func TestTokenRoundTrip(t *testing.T) {
	exp := time.Now().Add(time.Hour)
	tok := makeToken("secret", exp)
	if !validToken("secret", tok, time.Now()) {
		t.Fatal("fresh token should be valid")
	}
}

func TestTokenExpired(t *testing.T) {
	exp := time.Now().Add(-time.Minute)
	tok := makeToken("secret", exp)
	if validToken("secret", tok, time.Now()) {
		t.Fatal("expired token must be invalid")
	}
}

func TestTokenWrongPassword(t *testing.T) {
	tok := makeToken("secret", time.Now().Add(time.Hour))
	if validToken("different", tok, time.Now()) {
		t.Fatal("token must be invalid under a rotated password")
	}
}

func TestTokenTampered(t *testing.T) {
	tok := makeToken("secret", time.Now().Add(time.Hour)) + "x"
	if validToken("secret", tok, time.Now()) {
		t.Fatal("tampered token must be invalid")
	}
	if validToken("secret", "garbage", time.Now()) {
		t.Fatal("malformed token must be invalid")
	}
}

func TestCheckPassword(t *testing.T) {
	if !checkPassword("secret", "secret") {
		t.Fatal("matching password should pass")
	}
	if checkPassword("secret", "nope") {
		t.Fatal("wrong password should fail")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/ -run 'TestToken|TestCheckPassword' -v`
Expected: FAIL — `makeToken`/`validToken`/`checkPassword` undefined.

- [ ] **Step 3: Write `internal/web/auth.go` (token + password parts)**

```go
package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

const cookieName = "shell3_session"

// signKey derives the HMAC key from the password; rotating the password
// invalidates every previously issued cookie.
func signKey(password string) []byte {
	sum := sha256.Sum256([]byte(password))
	return sum[:]
}

// makeToken returns a signed cookie value that expires at exp.
func makeToken(password string, exp time.Time) string {
	payload := strconv.FormatInt(exp.Unix(), 10)
	mac := hmac.New(sha256.New, signKey(password))
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + sig
}

// validToken reports whether tok is correctly signed for password and not yet
// expired as of now.
func validToken(password, tok string, now time.Time) bool {
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 {
		return false
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	payload := string(payloadBytes)
	mac := hmac.New(sha256.New, signKey(password))
	mac.Write([]byte(payload))
	want := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(want), []byte(parts[1])) != 1 {
		return false
	}
	exp, err := strconv.ParseInt(payload, 10, 64)
	if err != nil {
		return false
	}
	return now.Before(time.Unix(exp, 0))
}

// checkPassword constant-time compares the submitted password to configured.
func checkPassword(configured, submitted string) bool {
	return subtle.ConstantTimeCompare([]byte(configured), []byte(submitted)) == 1
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/web/ -run 'TestToken|TestCheckPassword' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/web/auth.go internal/web/auth_test.go
git commit -m "feat(web): HMAC session-cookie token and password compare"
```

---

## Task 4: Auth + origin middleware and login/logout handlers

**Files:**
- Modify: `internal/web/auth.go` (add middleware + handlers)
- Modify: `internal/web/server.go:36-58` (Server carries cfg; NewServer signature; Handler wraps middleware + new routes)
- Modify: `internal/web/server_test.go:24-49` (`newTestServer` signature)
- Modify: `internal/web/assets.go` (embed login.html — done in Task 5; for now reference an existing var)
- Test: `internal/web/auth_test.go` (add middleware tests)

> Note: `handleLoginPage` writes `loginHTML`, which is embedded in Task 5. To keep this task compiling and testable on its own, add a temporary fallback in this task and replace it in Task 5. Specifically, in Task 5 you will add the `//go:embed assets/login.html` var; in THIS task, declare `var loginHTML = []byte("<!doctype html><form method=post action=/login><input name=password type=password><button>log in</button></form>")` at the top of `auth.go`. Task 5 step 3 removes this literal and adds the embed.

- [ ] **Step 1: Write the failing middleware tests**

Add to `internal/web/auth_test.go`:

```go
import (
	"net/http"
	"net/http/httptest"
	"strings"
)

// helper: build a server with the given config (no LLM turns needed here).
func newAuthServer(t *testing.T, cfg Config) *httptest.Server {
	t.Helper()
	srv, cleanup := serverForTest(cfg) // defined in server_test.go
	t.Cleanup(cleanup)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestAuthDisabledWhenNoPassword(t *testing.T) {
	ts := newAuthServer(t, Config{}) // no password, no origins → all open
	res, err := http.Get(ts.URL + "/meta")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
}

func TestAuthRedirectsRootToLogin(t *testing.T) {
	ts := newAuthServer(t, Config{Password: "pw", CookieTTL: time.Hour})
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/login" {
		t.Fatalf("status=%d loc=%q, want 302 -> /login", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestAuth401ForEventsAndAPI(t *testing.T) {
	ts := newAuthServer(t, Config{Password: "pw", CookieTTL: time.Hour})
	for _, path := range []string{"/events", "/meta"} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s = %d, want 401", path, res.StatusCode)
		}
	}
}

func TestLoginSetsCookieAndGrantsAccess(t *testing.T) {
	ts := newAuthServer(t, Config{Password: "pw", CookieTTL: time.Hour})
	jar, _ := newJar()
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	// Wrong password → no cookie, redirect to /login?e=1.
	res, _ := client.PostForm(ts.URL+"/login", map[string][]string{"password": {"wrong"}})
	res.Body.Close()
	if len(jar.Cookies(mustURL(ts.URL))) != 0 {
		t.Fatal("wrong password should not set a cookie")
	}
	// Correct password → cookie set.
	res, _ = client.PostForm(ts.URL+"/login", map[string][]string{"password": {"pw"}})
	res.Body.Close()
	if len(jar.Cookies(mustURL(ts.URL))) == 0 {
		t.Fatal("correct password should set a cookie")
	}
	// Now /meta is reachable.
	res, _ = client.Get(ts.URL + "/meta")
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/meta after login = %d, want 200", res.StatusCode)
	}
}

func TestOriginGuardRejectsForeignHostAndOrigin(t *testing.T) {
	ts := newAuthServer(t, Config{AllowedOrigins: []string{"http://app.test:7000"}})
	// Foreign Host header → 403 even on GET.
	req, _ := http.NewRequest("GET", ts.URL+"/meta", nil)
	req.Host = "evil.test:7000"
	res, _ := http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("foreign host = %d, want 403", res.StatusCode)
	}
	// Allowed Host header → ok.
	req, _ = http.NewRequest("GET", ts.URL+"/meta", nil)
	req.Host = "app.test:7000"
	res, _ = http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("allowed host = %d, want 200", res.StatusCode)
	}
	// POST with foreign Origin → 403.
	req, _ = http.NewRequest("POST", ts.URL+"/cancel", nil)
	req.Host = "app.test:7000"
	req.Header.Set("Origin", "http://evil.test:7000")
	res, _ = http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("foreign origin POST = %d, want 403", res.StatusCode)
	}
}
```

Add the small test helpers at the bottom of `auth_test.go`:

```go
import "net/http/cookiejar"
import "net/url"

func newJar() (*cookiejar.Jar, error) { return cookiejar.New(nil) }
func mustURL(s string) *url.URL       { u, _ := url.Parse(s); return u }
```

(Consolidate imports into a single block when writing the file.)

- [ ] **Step 2: Add `serverForTest` to `server_test.go`**

`newTestServer` builds a full hub; the auth tests need a `*Server` plus cleanup with a configurable `Config`. Refactor `newTestServer` to delegate. Replace the body of `newTestServer` (server_test.go:24-49) so it calls a shared builder, and add `serverForTest`:

```go
func serverForTest(cfg Config, scripts ...fakellm.Script) (*Server, func()) {
	client := fakellm.New(scripts...)
	sess := chat.NewSession(chat.SessionOpts{BufSize: 256})
	tc := chat.TurnConfig{
		LLM:         client,
		Personality: persona.Persona{Name: "test"},
		Handlers:    chat.NewHandlers(chat.Config{}),
		Log:         chat.LogOrNoop(nil),
	}
	h := NewHub(sess, func(ctx context.Context, msg llm.Message) { sess.Run(ctx, tc, msg.Content) })
	h.Start()
	info := Info{
		Persona: "test", Project: "p", Prompt: "SYS PROMPT", Tools: []string{"bash"},
		Models: []string{"main"}, Model: func() string { return "fake" },
	}
	cleanup := func() { h.Close(); sess.End("ok"); sess.CloseEvents() }
	return NewServer(h, info, cfg), cleanup
}

func newTestServer(t *testing.T, scripts ...fakellm.Script) *httptest.Server {
	t.Helper()
	srv, cleanup := serverForTest(Config{}, scripts...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); cleanup() })
	return ts
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/web/ -run 'TestAuth|TestLogin|TestOrigin' -v`
Expected: FAIL — `NewServer` takes 2 args; middleware/handlers undefined.

- [ ] **Step 4: Update `server.go` — Server carries cfg, new routes, middleware wrap**

In `internal/web/server.go`, change the `Server` struct and `NewServer` (lines 37-43):

```go
type Server struct {
	hub  *Hub
	info Info
	cfg  Config
}

// NewServer wraps a Hub with the session info shown in the UI and the resolved
// web Config (auth + origin policy).
func NewServer(hub *Hub, info Info, cfg Config) *Server {
	return &Server{hub: hub, info: info, cfg: cfg}
}
```

Replace `Handler()` (lines 46-58):

```go
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /meta", s.handleMeta)
	mux.HandleFunc("GET /prompt", s.handlePrompt)
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("POST /input", s.handleInput)
	mux.HandleFunc("POST /cancel", s.handleCancel)
	mux.HandleFunc("POST /clear", s.handleClear)
	mux.HandleFunc("POST /model", s.handleModel)
	mux.HandleFunc("POST /image", s.handleImage)
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLoginSubmit)
	mux.HandleFunc("POST /logout", s.handleLogout)
	return s.originGuard(s.authGuard(mux))
}
```

- [ ] **Step 5: Add middleware + handlers to `auth.go`**

Append to `internal/web/auth.go` (and add `"net/http"` + `"time"` to its imports):

```go
// authGuard enforces the session cookie. Disabled entirely when no password is
// configured. /login and /logout are always reachable.
func (s *Server) authGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Password == "" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/login" || r.URL.Path == "/logout" {
			next.ServeHTTP(w, r)
			return
		}
		if s.authenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

// originGuard validates Host on every request and Origin on state-changing
// POSTs. Disabled when AllowedOrigins is empty (e.g. in tests / unconfigured).
func (s *Server) originGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(s.cfg.AllowedOrigins) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		if !s.cfg.hostAllowed(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost && !s.cfg.originAllowed(r.Header.Get("Origin")) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authenticated(r *http.Request) bool {
	ck, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	return validToken(s.cfg.Password, ck.Value, time.Now())
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Password == "" || s.authenticated(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(loginHTML)
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if s.cfg.Password == "" || !checkPassword(s.cfg.Password, r.PostFormValue("password")) {
		http.Redirect(w, r, "/login?e=1", http.StatusSeeOther)
		return
	}
	exp := time.Now().Add(s.cfg.CookieTTL)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    makeToken(s.cfg.Password, exp),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.cfg.CookieTTL.Seconds()),
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
```

Also add the temporary `loginHTML` literal at the top of `auth.go` (removed in Task 5):

```go
var loginHTML = []byte("<!doctype html><form method=post action=/login>" +
	"<input name=password type=password><button>log in</button></form>")
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/web/ -run 'TestAuth|TestLogin|TestOrigin|TestServer' -v`
Expected: PASS (new auth/origin tests AND the pre-existing `TestServer_*` tests, which now use `Config{}` → guards disabled).

- [ ] **Step 7: Commit**

```bash
git add internal/web/auth.go internal/web/server.go internal/web/server_test.go internal/web/auth_test.go
git commit -m "feat(web): auth + origin middleware, login/logout routes"
```

---

## Task 5: Login page asset + SPA 401 redirect

**Files:**
- Create: `internal/web/assets/login.html`
- Modify: `internal/web/assets.go`
- Modify: `internal/web/auth.go` (replace literal with embed)
- Modify: `internal/web/assets/index.html:335-339, 430`

- [ ] **Step 1: Create the login page**

Create `internal/web/assets/login.html`:

```html
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>shell3 — sign in</title>
  <style>
    body { background:#0b0e14; color:#cbd5e1; font:15px/1.5 ui-monospace,Menlo,monospace;
           display:flex; min-height:100vh; align-items:center; justify-content:center; margin:0; }
    form { background:#11151f; border:1px solid #1e2536; border-radius:10px; padding:28px;
           width:300px; box-shadow:0 8px 30px rgba(0,0,0,.4); }
    h1 { font-size:16px; margin:0 0 16px; color:#e2e8f0; }
    input { width:100%; box-sizing:border-box; padding:9px 10px; margin-bottom:12px;
            background:#0b0e14; border:1px solid #2a3346; border-radius:6px; color:#e2e8f0; }
    button { width:100%; padding:9px; background:#2563eb; border:0; border-radius:6px;
             color:#fff; font:inherit; cursor:pointer; }
    .err { color:#f87171; margin:0 0 12px; font-size:13px; display:none; }
  </style>
</head>
<body>
  <form method="post" action="/login">
    <h1>shell3</h1>
    <p class="err" id="err">Incorrect password.</p>
    <input type="password" name="password" placeholder="password" autofocus autocomplete="current-password">
    <button type="submit">Sign in</button>
  </form>
  <script>
    if (location.search.indexOf('e=1') >= 0) {
      document.getElementById('err').style.display = 'block';
    }
  </script>
</body>
</html>
```

- [ ] **Step 2: Embed it**

In `internal/web/assets.go`, add below the existing `indexHTML` embed:

```go
//go:embed assets/login.html
var loginHTML []byte
```

- [ ] **Step 3: Remove the temporary literal**

In `internal/web/auth.go`, delete the temporary `var loginHTML = []byte("...")` declaration added in Task 4 (the embed in `assets.go` now provides it).

- [ ] **Step 4: Add the SPA 401 redirect**

In `internal/web/assets/index.html`, replace the `connect()` function (lines 335-339):

```javascript
function connect() {
  const es = new EventSource('/events');
  es.onopen = () => { resetView(); };
  es.onmessage = (m) => { try { handle(JSON.parse(m.data)); } catch (e) {} };
  es.onerror = async () => {
    // A 401 from /events closes the stream; confirm via /meta and bounce to login.
    try { const r = await fetch('/meta'); if (r.status === 401) location = '/login'; } catch (e) {}
  };
}
```

And update the startup `/meta` fetch (line 430) to redirect on 401:

```javascript
  try { const r = await fetch('/meta'); if (r.status === 401) { location = '/login'; return; } meta = await r.json(); } catch (e) {}
```

(Match the surrounding `let meta` declaration; keep the existing variable name.)

- [ ] **Step 5: Verify build + tests**

Run: `go build ./... && go test ./internal/web/ -v`
Expected: build succeeds; all web tests PASS (login page now embedded).

- [ ] **Step 6: Commit**

```bash
git add internal/web/assets/login.html internal/web/assets.go internal/web/auth.go internal/web/assets/index.html
git commit -m "feat(web): embedded login page + SPA redirect on 401"
```

---

## Task 6: Wire web.Config through cmd (boot, run, web)

**Files:**
- Modify: `cmd/shell3/boot.go:39, 44, 48, 54, 169`
- Modify: `cmd/shell3/run.go:90`
- Modify: `cmd/shell3/web.go:22-41, 61, 152-164`

- [ ] **Step 1: Change `buildChatConfig` to return the web config**

In `cmd/shell3/boot.go`, change the signature (line 39):

```go
func buildChatConfig(configPath, cwd, homeDir, outPath string, headless bool, log applog.Logger) (chat.Config, luacfg.WebConfig, func(), error) {
```

Update each early error return to add the extra value:
- Line 44 (`EnsureGlobal`): `return chat.Config{}, luacfg.WebConfig{}, func() {}, err`
- Line 48 (`EnsureProject`): `return chat.Config{}, luacfg.WebConfig{}, func() {}, err`
- Line 54 (`luacfg.Load`): `return chat.Config{}, luacfg.WebConfig{}, func() {}, err`

Update the final return (line 169):

```go
	return cfg, lc.Web, cleanup, nil
```

- [ ] **Step 2: Update the `run` caller**

In `cmd/shell3/run.go`, line 90:

```go
	cfg, _, cleanup, err := buildChatConfig(configPath, cwd, homeDir, f.outPath, headless, log)
```

- [ ] **Step 3: Build to verify both callers updated**

Run: `go build ./cmd/...`
Expected: FAIL only in `web.go` (still using old call + flags). If `run.go` errors, fix the discard.

- [ ] **Step 4: Update `web.go` — drop flags, map + resolve + validate**

In `cmd/shell3/web.go`, replace the `webFlags` struct (lines 22-26):

```go
type webFlags struct {
	configPath string
}
```

Replace `newWebCommand` flag registration (lines 37-39) with just the config flag:

```go
	cmd.Flags().StringVarP(&f.configPath, "config", "c", "", "Path to shell3.lua (default: ./shell3.lua, else ~/.shell3/shell3.lua)")
```

(Delete the `--host` and `--port` lines.)

In `runWeb`, update the `buildChatConfig` call (line 61):

```go
	cfg, webCfg, cleanup, err := buildChatConfig(configPath, cwd, homeDir, "", false, log)
```

After `defer cleanup()` (line 65), resolve + validate the web config:

```go
	wc := web.Config{
		Host:           webCfg.Host,
		Port:           webCfg.Port,
		Password:       webCfg.Password,
		CookieTTL:      webCfg.CookieTTL,
		AllowedOrigins: webCfg.AllowedOrigins,
	}.Resolve()
	if err := wc.Validate(); err != nil {
		return err
	}
```

Replace the server construction (lines 152-155):

```go
	srv := &http.Server{
		Addr:    wc.Addr(),
		Handler: web.NewServer(hub, info, wc).Handler(),
	}
```

Update the listening message (line 164):

```go
	fmt.Fprintf(os.Stderr, "shell3 web listening on http://%s\n", wc.Addr())
```

Remove the now-unused `"net"` import (it was only used for `net.JoinHostPort`).

- [ ] **Step 5: Build + run full test suite**

Run: `go build ./... && go test ./...`
Expected: PASS across all packages.

- [ ] **Step 6: Manual smoke check (loopback, no auth)**

Run: `go run ./cmd/shell3 web --help`
Expected: help text shows only `--config` (no `--host`/`--port`).

- [ ] **Step 7: Commit**

```bash
git add cmd/shell3/boot.go cmd/shell3/run.go cmd/shell3/web.go
git commit -m "feat(web): drive host/port/auth from shell3.web config"
```

---

## Task 7: Scaffold example, env template, TODO update, final verification

**Files:**
- Modify: `internal/scaffold/defaults/shell3.lua` (add `shell3.web{}` example before the Agent section, ~line 612)
- Modify: `internal/scaffold/defaults/env.example`
- Modify: `internal/web/TODO.md`

- [ ] **Step 1: Add the example web block to the scaffold**

In `internal/scaffold/defaults/shell3.lua`, insert before the `-- Agent` section header (currently around line 612):

```lua
-- ---------------------------------------------------------------------------
-- Web portal (optional) — `shell3 web`
-- ---------------------------------------------------------------------------
-- Configures the browser frontend. Omit this block entirely for the default
-- localhost:8080 with no authentication. Set a password to enable the login
-- page (signed cookie) and to allow binding a non-loopback host.
shell3.web({
  host       = "127.0.0.1",                       -- use "0.0.0.0" to expose; requires password
  port       = 8080,
  password   = shell3.env.secret("WEB_PASSWORD"),  -- empty in .env → auth disabled
  cookie_ttl = "168h",                             -- session length (7 days)
  -- allowed_origins = { "https://shell3.example.com" }, -- extra trusted origins behind a proxy
})

```

- [ ] **Step 2: Add the secret to the env template**

In `internal/scaffold/defaults/env.example`, append:

```
# Password for the `shell3 web` portal login. Leave empty to disable auth
# (only safe when binding 127.0.0.1). Required to bind a non-loopback host.
WEB_PASSWORD=
```

- [ ] **Step 3: Verify the scaffold config still loads**

Run: `go test ./internal/luacfg/ ./internal/scaffold/ -v`
Expected: PASS (if there's a scaffold-loads test it now also exercises the web block; otherwise luacfg parsing covers it).

Additionally, sanity-check the bundled scaffold parses by pointing `Load` at it (one-off):

Run: `go test ./internal/luacfg/ -run TestLoadWeb -v`
Expected: PASS.

- [ ] **Step 4: Update the web TODO**

In `internal/web/TODO.md`, mark the Authentication and Origin items done and trim the lead-in. Replace the `## 🔴 Security` section's first three bullets:

```markdown
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
```

- [ ] **Step 5: Full verification**

Run: `make build && go test ./... && gofmt -l internal/web internal/luacfg cmd/shell3`
Expected: build succeeds; all tests PASS; `gofmt -l` prints nothing (no unformatted files).

- [ ] **Step 6: Commit**

```bash
git add internal/scaffold/defaults/shell3.lua internal/scaffold/defaults/env.example internal/web/TODO.md
git commit -m "docs(web): scaffold shell3.web example, env template, TODO"
```

---

## Self-Review Notes

- **Spec coverage:** §1 config surface → Task 1 + Task 7 (scaffold). §2 threading/CLI → Task 6. §3 auth (cookie/login/logout/middleware) → Tasks 3, 4, 5. §4 Origin/CSRF → Tasks 2 (helpers) + 4 (middleware). §5 bind safety → Task 2 (`Validate`) + Task 6 (called in `runWeb`). §6 files → all tasks. §7 testing → tests in Tasks 1–4. §8 out-of-scope items remain in TODO (Task 7).
- **Type consistency:** `web.Config` fields (`Host`, `Port`, `Password`, `CookieTTL`, `AllowedOrigins`) match between `config.go`, the `luacfg.WebConfig` mapping in `web.go`, and tests. `makeToken`/`validToken`/`checkPassword`/`cookieName` consistent across `auth.go` and `auth_test.go`. `NewServer(hub, info, cfg)` signature consistent across `server.go`, `server_test.go`, and `web.go`.
- **Guard-disable conventions** (empty Password → no auth; empty AllowedOrigins → no origin guard) are stated once in File Structure and relied on consistently by tests and wiring.
- **Login asset ordering:** Task 4 uses a temporary `loginHTML` literal so it compiles/tests standalone; Task 5 replaces it with the embed and removes the literal — called out explicitly in both tasks.
