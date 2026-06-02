package web

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// newAuthServer builds a server with the given config (no LLM turns needed here).
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
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	u, _ := url.Parse(ts.URL)
	// Wrong password → no cookie, redirect to /login?e=1.
	res, _ := client.PostForm(ts.URL+"/login", url.Values{"password": {"wrong"}})
	res.Body.Close()
	if len(jar.Cookies(u)) != 0 {
		t.Fatal("wrong password should not set a cookie")
	}
	// Correct password → cookie set.
	res, _ = client.PostForm(ts.URL+"/login", url.Values{"password": {"pw"}})
	res.Body.Close()
	if len(jar.Cookies(u)) == 0 {
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
