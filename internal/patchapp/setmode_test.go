package patchapp

import (
	"strings"
	"testing"
)

func TestSetModeUpdatesBadge(t *testing.T) {
	a := New("build", "status", WelcomeInfo{})
	a.SetMode("plan")
	a.mu.Lock()
	frame := strings.Join(a.liveFrameLocked(), "\n")
	a.mu.Unlock()
	if !strings.Contains(frame, "plan") {
		t.Fatalf("status bar did not render new mode badge; frame:\n%s", frame)
	}
}

func TestTabFiresHandler(t *testing.T) {
	a := New("build", "status", WelcomeInfo{})
	called := false
	a.SetTab(func() { called = true })
	a.processInput([]byte{9}) // Tab
	if !called {
		t.Fatal("Tab key did not fire the registered handler")
	}
}
