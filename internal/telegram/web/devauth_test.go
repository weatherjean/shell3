//go:build unix

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
)

// SetDevNoAuth must make an API request with NO initData succeed (the local
// `shell3 dash` mode), where the same request is 401 on a normal server.
func TestSetDevNoAuth_BypassesInitData(t *testing.T) {
	const token = "test-bot-token"
	const chatID int64 = 8701499393

	rt := shell3test.NewRuntimeForTest(t, "ok")
	sess, err := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}

	// Baseline: without dev-no-auth, an unsigned request is rejected.
	srv := NewServer(rt, sess, token, chatID)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no initData should be 401, got %d", rr.Code)
	}

	// With dev-no-auth, the same unsigned request is accepted.
	srv.SetDevNoAuth()
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("dev-no-auth should accept unsigned request, got %d (body: %s)", rr.Code, rr.Body.String())
	}
}
