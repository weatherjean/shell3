//go:build unix

package web

import (
	"net/http/httptest"
	"net/http"
	"testing"
)

func TestVerifyInitData_RejectsTampered(t *testing.T) {
	// A syntactically-valid but unsigned/invalid initData must be rejected.
	if verifyInitData("user=%7B%22id%22%3A42%7D&hash=deadbeef", "bot-token", 42) {
		t.Fatal("tampered initData accepted")
	}
}

func TestTokenAuth(t *testing.T) {
	auth := TokenAuth("s3cret")
	req := httptest.NewRequest(http.MethodGet, "/api/history", nil)
	if auth(req) {
		t.Fatal("no token must be rejected")
	}
	req.Header.Set("X-Auth-Token", "wrong")
	if auth(req) {
		t.Fatal("wrong token must be rejected")
	}
	req.Header.Set("X-Auth-Token", "s3cret")
	if !auth(req) {
		t.Fatal("correct header token must pass")
	}
	q := httptest.NewRequest(http.MethodGet, "/api/history?key=s3cret", nil)
	if !auth(q) {
		t.Fatal("correct ?key= must pass")
	}
	if TokenAuth("")(q) {
		t.Fatal("empty secret must reject everything")
	}
}
