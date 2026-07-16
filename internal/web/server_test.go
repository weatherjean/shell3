//go:build unix

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHistory_RequiresAuth(t *testing.T) {
	s := &Server{auth: func(*http.Request) bool { return false }}
	s.sess = nil
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/history", nil)
	s.gated(s.handleHistory)(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}
