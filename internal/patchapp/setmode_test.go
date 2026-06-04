package patchapp

import "testing"

func TestSetModeUpdatesBadge(t *testing.T) {
	a := New("build", "status", WelcomeInfo{})
	a.SetMode("plan")
	a.mu.Lock()
	got := a.status.mode
	a.mu.Unlock()
	if got != "plan" {
		t.Fatalf("mode = %q, want plan", got)
	}
}

func TestSetTabRegistersHandler(t *testing.T) {
	a := New("build", "status", WelcomeInfo{})
	called := false
	a.SetTab(func() { called = true })
	if a.onTab == nil {
		t.Fatal("SetTab did not register handler")
	}
	a.onTab()
	if !called {
		t.Fatal("handler not invoked")
	}
}
