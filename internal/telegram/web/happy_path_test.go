//go:build unix

package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	initdata "github.com/telegram-mini-apps/init-data-golang"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
)

// signInitData assembles a full, validly-signed Telegram initData query string
// for the given user JSON. SignQueryString returns only the hash, so we append
// auth_date (which is part of the signed data) and hash ourselves.
func signInitData(t *testing.T, token, userJSON string) string {
	t.Helper()
	authDate := time.Now()
	params := url.Values{"user": {userJSON}}
	hash, err := initdata.SignQueryString(params.Encode(), token, authDate)
	if err != nil {
		t.Fatalf("sign initData: %v", err)
	}
	params.Set("auth_date", strconv.FormatInt(authDate.Unix(), 10))
	params.Set("hash", hash)
	return params.Encode()
}

// TestHistory_ValidInitDataReturnsHistory exercises the real verifyInitData
// happy path end-to-end: a correctly-signed initData (forged with the library's
// own signer against a dummy token) authorizes /api/history and the seeded
// conversation comes back. Complements server_test.go, which covers rejection.
func TestHistory_ValidInitDataReturnsHistory(t *testing.T) {
	const token = "test-bot-token"
	const chatID int64 = 8701499393

	rt := shell3test.NewRuntimeForTest(t, "pong from agent")
	sess, err := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	// Seed one turn so History() is non-empty.
	for range sess.Send(context.Background(), "ping") {
	}

	srv := NewServer(rt, sess, token, chatID)

	// Forge a validly-signed initData for the configured chat id.
	signed := signInitData(t, token, `{"id":8701499393,"first_name":"T"}`)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/history", nil)
	req.Header.Set("X-Init-Data", signed)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 with valid initData, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "pong from agent") {
		t.Fatalf("history missing seeded reply; body: %s", rr.Body.String())
	}
}

// TestStatusAndSubagents_AuthAndShape covers the two new dashboard endpoints:
// rejected without initData, and returning well-formed JSON with valid initData.
func TestStatusAndSubagents_AuthAndShape(t *testing.T) {
	const token = "test-bot-token"
	const chatID int64 = 8701499393

	rt := shell3test.NewRuntimeForTest(t, "ok")
	sess, err := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(rt, sess, token, chatID)
	signed := signInitData(t, token, `{"id":8701499393,"first_name":"T"}`)

	for _, path := range []string{"/api/status", "/api/jobs"} {
		// Unauthenticated → 401.
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s without auth: want 401, got %d", path, rr.Code)
		}
		// Authenticated → 200 + JSON.
		rr = httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Init-Data", signed)
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s with auth: want 200, got %d (%s)", path, rr.Code, rr.Body.String())
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("%s content-type: want application/json, got %q", path, ct)
		}
	}
	// /api/jobs returns a JSON array even with no jobs.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	req.Header.Set("X-Init-Data", signed)
	srv.Handler().ServeHTTP(rr, req)
	if got := strings.TrimSpace(rr.Body.String()); got != "[]" {
		t.Fatalf("empty jobs: want [], got %q", got)
	}
}

// TestNewEndpoints_AuthAndShape covers the subagent/sessions/session endpoints
// and usage in /api/status.
func TestNewEndpoints_AuthAndShape(t *testing.T) {
	const token = "test-bot-token"
	const chatID int64 = 8701499393

	rt := shell3test.NewRuntimeForTest(t, "ok")
	sess, err := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(rt, sess, token, chatID)
	us := NewUsageStore()
	us.Set(120, 30, 150)
	srv.SetUsage(us)
	signed := signInitData(t, token, `{"id":8701499393,"first_name":"T"}`)

	get := func(path string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Init-Data", signed)
		srv.Handler().ServeHTTP(rr, req)
		return rr
	}

	// All gated.
	for _, p := range []string{"/api/jobs", "/api/job?id=sub1", "/api/sessions", "/api/session?id=1"} {
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, p, nil))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s without auth: want 401, got %d", p, rr.Code)
		}
	}
	// Unknown job id → 200 with an empty transcript (jobs are in-memory; no path
	// traversal surface exists anymore).
	if rr := get("/api/job?id=sub999"); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"transcript":""`) {
		t.Fatalf("unknown job transcript: got %d %q", rr.Code, rr.Body.String())
	}
	// Missing id → 400.
	if rr := get("/api/job"); rr.Code != http.StatusBadRequest {
		t.Fatalf("missing job id: want 400, got %d", rr.Code)
	}
	// Sessions list (nil store in test runtime → 200 []).
	if rr := get("/api/sessions"); rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Fatalf("sessions: got %d %q", rr.Code, rr.Body.String())
	}
	// Usage surfaces in status.
	if rr := get("/api/status"); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"total":150`) {
		t.Fatalf("status usage: got %d %q", rr.Code, rr.Body.String())
	}
}

func TestCron_AuthAndShape(t *testing.T) {
	const token = "test-bot-token"
	const chatID int64 = 8701499393
	rt := shell3test.NewRuntimeForTest(t, "ok")
	sess, err := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(rt, sess, token, chatID)
	srv.SetCronSource(func() []CronJob {
		return []CronJob{{Name: "nightly", Schedule: "0 9 * * *", Agent: "explorer", Notify: true}}
	})
	signed := signInitData(t, token, `{"id":8701499393,"first_name":"T"}`)

	// gated
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/cron", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
	// authed
	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/cron", nil)
	req.Header.Set("X-Init-Data", signed)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"nightly"`) {
		t.Fatalf("cron: got %d %q", rr.Code, rr.Body.String())
	}
}

// TestHistory_WrongUserRejected confirms a validly-signed payload for a
// different user id is still rejected (the chat-id binding holds).
func TestHistory_WrongUserRejected(t *testing.T) {
	const token = "test-bot-token"
	const chatID int64 = 8701499393

	rt := shell3test.NewRuntimeForTest(t, "x")
	sess, err := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(rt, sess, token, chatID)

	signed := signInitData(t, token, `{"id":999,"first_name":"Mallory"}`)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/history", nil)
	req.Header.Set("X-Init-Data", signed)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for wrong user id, got %d", rr.Code)
	}
}
