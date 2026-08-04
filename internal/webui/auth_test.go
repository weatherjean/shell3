//go:build unix

package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/weatherjean/shell3/internal/shell3"
)

const testPassword = "a-sufficiently-long-password"

// newAuthServer is a test server with authentication actually configured.
func newAuthServer(t *testing.T, secret string) *Server {
	t.Helper()
	srv := newTestServer(t, "ok")
	srv.rt.SetWebForTest(shell3.WebConfig{Password: testPassword, TOTPSecret: secret})
	return srv
}

// request runs one request through the real mux, so route registration and the
// gate are exercised the way a browser would.
func request(t *testing.T, srv *Server, method, path string, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// login returns the session cookie for a successful login.
func login(t *testing.T, srv *Server, body string) *http.Cookie {
	t.Helper()
	rec := request(t, srv, http.MethodPost, "/api/login", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	t.Fatal("login succeeded without setting a session cookie")
	return nil
}

// THE test for "all routes are secure". It walks the same table Handler() uses
// to build the mux, so a route added later cannot quietly escape the gate: the
// table is the single place its auth status is declared, and this asserts every
// declaration is honoured.
func TestEveryPrivateRouteRefusesAnUnauthenticatedRequest(t *testing.T) {
	srv := newAuthServer(t, "")

	private := 0
	for _, rt := range srv.routes() {
		if rt.public {
			continue
		}
		private++
		// A subtree pattern needs something under it: asking for the bare
		// "/api/asks" gets a 307 to "/api/asks/" from the mux itself, before
		// any handler — which tests the redirect, not the gate.
		path := rt.pattern
		if strings.HasSuffix(path, "/") {
			path += "probe"
		}
		// GET is enough: the gate runs before any method check.
		rec := request(t, srv, http.MethodGet, path, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s = %d, want 401: this route is not behind the gate", path, rec.Code)
		}
	}
	if private == 0 {
		t.Fatal("no private routes found; the table cannot be asserting anything")
	}
}

// The counterpart: the login route and the static shell must stay reachable, or
// there is no way to log in and nothing to draw the login screen with.
func TestPublicRoutesStayReachableWithoutASession(t *testing.T) {
	srv := newAuthServer(t, "")

	for _, path := range []string{"/", "/index.html"} {
		rec := request(t, srv, http.MethodGet, path, "")
		if rec.Code == http.StatusUnauthorized {
			t.Errorf("GET %s = 401, but the login screen needs it", path)
		}
	}
	// Wrong password, but it must be the login handler answering, not the gate.
	rec := request(t, srv, http.MethodPost, "/api/login", `{"password":"nope"}`)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "error") {
		t.Errorf("POST /api/login = %d, body %s; want the handler's own refusal", rec.Code, rec.Body.String())
	}
}

// The service worker is not part of the login screen, so it is gated like any
// other resource — it is registered after login, when the cookie exists.
func TestServiceWorkerIsGated(t *testing.T) {
	srv := newAuthServer(t, "")
	if rec := request(t, srv, http.MethodGet, "/sw.js", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /sw.js = %d, want 401", rec.Code)
	}
}

func TestLoginWithTheRightPasswordOpensEveryRoute(t *testing.T) {
	srv := newAuthServer(t, "")
	cookie := login(t, srv, `{"password":"`+testPassword+`"}`)

	rec := request(t, srv, http.MethodGet, "/api/capabilities", "", cookie)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /api/capabilities with a session = %d, want 200", rec.Code)
	}
}

func TestSessionCookieIsHTTPOnlyAndLax(t *testing.T) {
	srv := newAuthServer(t, "")
	cookie := login(t, srv, `{"password":"`+testPassword+`"}`)

	if !cookie.HttpOnly {
		t.Error("the session cookie is readable from JavaScript")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Secure {
		t.Error("Secure set on a plain-http request; the browser would drop the cookie")
	}
}

// Over https the cookie must be Secure, or a later downgrade to http leaks it.
func TestSessionCookieIsSecureBehindHTTPS(t *testing.T) {
	srv := newAuthServer(t, "")
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"`+testPassword+`"}`))
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && !c.Secure {
			t.Error("cookie issued over https without Secure")
		}
	}
}

func TestLoginRejectsTheWrongPassword(t *testing.T) {
	srv := newAuthServer(t, "")
	rec := request(t, srv, http.MethodPost, "/api/login", `{"password":"wrong"}`)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			t.Error("a failed login issued a session")
		}
	}
}

func TestLogoutRevokesTheSession(t *testing.T) {
	srv := newAuthServer(t, "")
	cookie := login(t, srv, `{"password":"`+testPassword+`"}`)

	if rec := request(t, srv, http.MethodPost, "/api/logout", "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("logout = %d", rec.Code)
	}
	if rec := request(t, srv, http.MethodGet, "/api/capabilities", "", cookie); rec.Code != http.StatusUnauthorized {
		t.Errorf("the cookie still works after logout: %d", rec.Code)
	}
}

// With a second factor configured, the password alone is not a session. The
// screen learns a code is needed from this response, since it cannot read the
// gated capabilities endpoint before logging in.
func TestLoginAsksForACodeWhenTOTPIsConfigured(t *testing.T) {
	srv := newAuthServer(t, newTOTPSecret(t))
	rec := request(t, srv, http.MethodPost, "/api/login", `{"password":"`+testPassword+`"}`)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var body struct {
		NeedCode bool `json:"needCode"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.NeedCode {
		t.Error("the password verified but the response did not ask for a code")
	}
}

func TestLoginAcceptsAValidTOTPCode(t *testing.T) {
	secret := newTOTPSecret(t)
	srv := newAuthServer(t, secret)
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	login(t, srv, `{"password":"`+testPassword+`","code":"`+code+`"}`)
}

func TestLoginRejectsAWrongTOTPCode(t *testing.T) {
	srv := newAuthServer(t, newTOTPSecret(t))
	rec := request(t, srv, http.MethodPost, "/api/login", `{"password":"`+testPassword+`","code":"000000"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// A code sniffed in its 30-second window must not be usable a second time.
func TestLoginRejectsAReplayedTOTPCode(t *testing.T) {
	secret := newTOTPSecret(t)
	srv := newAuthServer(t, secret)
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	body := `{"password":"` + testPassword + `","code":"` + code + `"}`

	login(t, srv, body)
	if rec := request(t, srv, http.MethodPost, "/api/login", body); rec.Code != http.StatusUnauthorized {
		t.Errorf("the same code worked twice: %d", rec.Code)
	}
}

// The login route must not be an oracle: a wrong password and a wrong code look
// identical, so probing cannot reveal that a password was correct.
func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	secret := newTOTPSecret(t)
	srv := newAuthServer(t, secret)

	badPassword := request(t, srv, http.MethodPost, "/api/login", `{"password":"wrong","code":"000000"}`)
	badCode := request(t, srv, http.MethodPost, "/api/login", `{"password":"`+testPassword+`","code":"000000"}`)

	if badPassword.Body.String() != badCode.Body.String() {
		t.Errorf("a wrong password says %q but a wrong code says %q", badPassword.Body.String(), badCode.Body.String())
	}
	if badPassword.Code != badCode.Code {
		t.Errorf("statuses differ: %d vs %d", badPassword.Code, badCode.Code)
	}
}

// Guessing has to get slower. Not a lockout: a hard lock would let anyone hold
// the login closed against the operator, and TOTP already covers guessing.
func TestLoginDelayGrowsWithFailuresAndIsCapped(t *testing.T) {
	if first, second := loginDelay(1), loginDelay(2); second <= first {
		t.Errorf("delay did not grow: %v then %v", first, second)
	}
	if got := loginDelay(0); got != 0 {
		t.Errorf("delay with no failures = %v, want none", got)
	}
	if got := loginDelay(50); got != maxLoginDelay {
		t.Errorf("delay after 50 failures = %v, want the cap %v", got, maxLoginDelay)
	}
}

func TestSuccessfulLoginClearsTheBackoff(t *testing.T) {
	srv := newAuthServer(t, "")
	request(t, srv, http.MethodPost, "/api/login", `{"password":"wrong"}`)
	request(t, srv, http.MethodPost, "/api/login", `{"password":"wrong"}`)
	login(t, srv, `{"password":"`+testPassword+`"}`)

	if got := srv.auth.failures(); got != 0 {
		t.Errorf("failures after a success = %d, want 0", got)
	}
}

// A login is how the operator finds out about a breach, so it has to be visible
// without going to look for it.
func TestLoginRaisesANotification(t *testing.T) {
	srv := newAuthServer(t, "")
	login(t, srv, `{"password":"`+testPassword+`"}`)

	found := false
	for _, n := range srv.recentNotifications() {
		if strings.Contains(strings.ToLower(n.Title+n.Body), "signed in") {
			found = true
		}
	}
	if !found {
		t.Error("a successful login raised no notification")
	}
}

// Library and test use: no password configured means no gate. `shell3 serve`
// refuses to start in that state, so this is not a reachable server mode — but
// it keeps every other test in this package from needing to log in first.
func TestWithoutAPasswordTheGateIsOpen(t *testing.T) {
	srv := newTestServer(t, "ok")
	if rec := request(t, srv, http.MethodGet, "/api/capabilities", ""); rec.Code != http.StatusOK {
		t.Errorf("GET /api/capabilities with no password configured = %d, want 200", rec.Code)
	}
}

// recentNotifications copies the replay buffer. A test reads it to check what
// the operator would have seen; production code has the hub for that.
func (s *Server) recentNotifications() []notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]notification(nil), s.recent...)
}

// newTOTPSecret generates an enrolment secret the way boot does.
func newTOTPSecret(t *testing.T) string {
	t.Helper()
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "shell3", AccountName: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return key.Secret()
}

// The sign-in notice is glanced at, not studied: a raw user-agent string buries
// "someone signed in" under four lines of version numbers. An unrecognised
// client keeps its raw name, since guessing would be worse than verbose.
func TestDeviceLabelReadsLikeADevice(t *testing.T) {
	cases := []struct{ agent, want string }{
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) " +
			"Chrome/149.0.0.0 Safari/537.36", "Chrome on macOS"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) " +
			"Version/17.0 Mobile/15E148 Safari/604.1", "Safari on iPhone"},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0", "Firefox on Linux"},
		{"curl/7.71.1", "curl/7.71.1"},
		{"", "unknown device"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if c.agent != "" {
			req.Header.Set("User-Agent", c.agent)
		}
		if got := deviceLabel(req); got != c.want {
			t.Errorf("deviceLabel(%.40q…) = %q, want %q", c.agent, got, c.want)
		}
	}
}
