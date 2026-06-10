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
	"github.com/weatherjean/shell3/pkg/shell3"
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

	rt := shell3.NewRuntimeForTest(t, "pong from agent")
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

// TestHistory_WrongUserRejected confirms a validly-signed payload for a
// different user id is still rejected (the chat-id binding holds).
func TestHistory_WrongUserRejected(t *testing.T) {
	const token = "test-bot-token"
	const chatID int64 = 8701499393

	rt := shell3.NewRuntimeForTest(t, "x")
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
