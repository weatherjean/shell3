//go:build unix

package main

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
)

// TestRearmHeartbeat_ArmsFromConfig pins the arm path: a config with a
// heartbeat yields a running ticker that injects the checklist prompt.
func TestRearmHeartbeat_ArmsFromConfig(t *testing.T) {
	var mu sync.Mutex
	var got []string
	inject := func(p string) { mu.Lock(); got = append(got, p); mu.Unlock() }

	hb := &shell3.Heartbeat{Every: time.Millisecond, Checklist: "- probe"}
	tk := rearmHeartbeat(nil, hb, inject, func() bool { return false })
	if tk == nil {
		t.Fatal("want a ticker for a configured heartbeat")
	}
	defer tk.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("armed ticker never injected")
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(got[0], "- probe") {
		t.Fatalf("injected prompt must carry the checklist, got %q", got[0])
	}
}

// TestRearmHeartbeat_StopsOldAndDisarms pins the disarm path: rearming with a
// nil config stops the old ticker and returns nil.
func TestRearmHeartbeat_StopsOldAndDisarms(t *testing.T) {
	var mu sync.Mutex
	n := 0
	inject := func(string) { mu.Lock(); n++; mu.Unlock() }
	old := rearmHeartbeat(nil, &shell3.Heartbeat{Every: time.Millisecond, Checklist: "- x"}, inject, nil)
	if got := rearmHeartbeat(old, nil, inject, nil); got != nil {
		t.Fatal("nil config must disarm (return nil)")
	}
	// The old ticker is stopped: the injection count stabilizes.
	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	before := n
	mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	after := n
	mu.Unlock()
	if after != before {
		t.Fatalf("old ticker still injecting after disarm (%d -> %d)", before, after)
	}
}
