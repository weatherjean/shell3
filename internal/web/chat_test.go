//go:build unix

package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
)

// chatServer builds a token-auth server with a live driver over a fake-LLM
// runtime, plus a helper that fires authenticated requests.
func chatServer(t *testing.T, reply string) (*Server, *Driver, func(method, path, body string) *httptest.ResponseRecorder) {
	t.Helper()
	rt := shell3test.NewRuntimeForTest(t, reply)
	sess, err := rt.Session(shell3.SessionOpts{Name: "web", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	d := NewDriver(context.Background(), rt, sess)
	srv := NewServer(rt, sess, TokenAuth("k"))
	srv.SetChat(d)
	h := srv.Handler()
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		req.Header.Set("X-Auth-Token", "k")
		h.ServeHTTP(rr, req)
		return rr
	}
	return srv, d, do
}

func TestStateWithoutChat(t *testing.T) {
	rt := shell3test.NewRuntimeForTest(t, "ok")
	sess, err := rt.Session(shell3.SessionOpts{Name: "web", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(rt, sess, TokenAuth("k"))
	h := srv.Handler()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	req.Header.Set("X-Auth-Token", "k")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"chat":false`) {
		t.Fatalf("state without chat: got %d %q", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(`{"text":"hi"}`))
	req.Header.Set("X-Auth-Token", "k")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("send without chat: want 404, got %d", rr.Code)
	}
}

func TestChatSendRoundTrip(t *testing.T) {
	_, d, do := chatServer(t, "pong-web")
	if rr := do(http.MethodPost, "/api/send", `{"text":"ping"}`); rr.Code != http.StatusAccepted {
		t.Fatalf("send: want 202, got %d (%s)", rr.Code, rr.Body.String())
	}
	waitFor(t, "turn to finish", func() bool { return !d.Busy() })
	if rr := do(http.MethodGet, "/api/state", ""); !strings.Contains(rr.Body.String(), `"busy":false`) {
		t.Fatalf("state after turn: %s", rr.Body.String())
	}
	if rr := do(http.MethodGet, "/api/history", ""); !strings.Contains(rr.Body.String(), "pong-web") {
		t.Fatalf("history missing reply: %s", rr.Body.String())
	}
}

func TestChatSendEmpty(t *testing.T) {
	_, _, do := chatServer(t, "x")
	if rr := do(http.MethodPost, "/api/send", `{"text":"   "}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("empty text: want 400, got %d", rr.Code)
	}
	if rr := do(http.MethodPost, "/api/send", `not-json`); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad json: want 400, got %d", rr.Code)
	}
	if rr := do(http.MethodGet, "/api/send", ""); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET send: want 405, got %d", rr.Code)
	}
}

func TestChatAskFlow(t *testing.T) {
	_, d, do := chatServer(t, "x")
	got := make(chan bool, 1)
	go func() { got <- d.Ask(context.Background(), "rm -rf /", "gate") }()
	waitFor(t, "ask in state", func() bool {
		return strings.Contains(do(http.MethodGet, "/api/state", "").Body.String(), "rm -rf /")
	})
	if rr := do(http.MethodPost, "/api/ask", `{"id":"1","allow":false}`); rr.Code != http.StatusOK {
		t.Fatalf("ask answer: want 200, got %d", rr.Code)
	}
	if <-got {
		t.Fatal("deny answer must return false")
	}
	// Unknown id → 200 no-op.
	if rr := do(http.MethodPost, "/api/ask", `{"id":"999","allow":true}`); rr.Code != http.StatusOK {
		t.Fatalf("unknown ask id: want 200, got %d", rr.Code)
	}
}

func TestChatStop(t *testing.T) {
	_, d, do := chatServer(t, "x")
	do(http.MethodPost, "/api/send", `{"text":"hi"}`)
	if rr := do(http.MethodPost, "/api/stop", ""); rr.Code != http.StatusOK {
		t.Fatalf("stop: want 200, got %d", rr.Code)
	}
	waitFor(t, "turn to end", func() bool { return !d.Busy() })
}

func TestChatEndpointsAuthGated(t *testing.T) {
	srv, _, _ := chatServer(t, "x")
	h := srv.Handler()
	for _, p := range []struct{ method, path string }{
		{http.MethodGet, "/api/state"},
		{http.MethodPost, "/api/send"},
		{http.MethodPost, "/api/stop"},
		{http.MethodPost, "/api/ask"},
	} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(p.method, p.path, strings.NewReader(`{}`)))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s without token: want 401, got %d", p.path, rr.Code)
		}
	}
}

func TestChatSendCommand(t *testing.T) {
	_, d, do := chatServer(t, "never-called")
	rr := do(http.MethodPost, "/api/send", `{"text":"/help"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("command send: want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	if b := rr.Body.String(); !strings.Contains(b, `"reply"`) || !strings.Contains(b, "/reload") {
		t.Fatalf("command reply body: %s", b)
	}
	if d.Busy() {
		t.Fatal("command must not start a turn")
	}
	if rr := do(http.MethodGet, "/api/history", ""); strings.Contains(rr.Body.String(), "/help") {
		t.Fatalf("command leaked into history: %s", rr.Body.String())
	}
}
