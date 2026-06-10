//go:build unix

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHistory_RequiresAuth(t *testing.T) {
	s := &Server{validate: func(string) (int64, bool) { return 0, false }}
	s.sess = nil
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/history", nil)
	s.auth(s.handleHistory)(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}
