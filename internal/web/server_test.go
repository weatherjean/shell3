//go:build unix

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
)

// TestHeartbeatFromConfig covers the config→DTO mapping: nil in, nil out; a
// declared block carries the interval, window, and the host's armed flag.
func TestHeartbeatFromConfig(t *testing.T) {
	if got := HeartbeatFromConfig(nil, true); got != nil {
		t.Fatalf("nil config: want nil, got %+v", got)
	}
	hb := &shell3.Heartbeat{Every: 30 * time.Minute, Checklist: "check disks", ActiveFrom: "08:00", ActiveTo: "22:00", TZ: "Europe/Berlin"}
	got := HeartbeatFromConfig(hb, false)
	want := &HeartbeatStatus{Every: "30m0s", Checklist: "check disks", ActiveFrom: "08:00", ActiveTo: "22:00", TZ: "Europe/Berlin", Armed: false}
	if *got != *want {
		t.Fatalf("HeartbeatFromConfig = %+v, want %+v", got, want)
	}
}

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
