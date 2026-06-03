package patchapp

import "testing"

// A lost-TTY Resume used to store a nil terminal state; a later Pause then fed
// that nil to term.Restore, which dereferences it and panics, crashing the TUI.
// Pause must tolerate a nil oldTermState.
func TestPauseDoesNotPanicOnNilTermState(t *testing.T) {
	a := New("test", "", WelcomeInfo{})

	a.mu.Lock()
	a.term.oldTermState = nil // as a failed-MakeRaw Resume would have left it
	a.mu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Pause panicked on a nil terminal state: %v", r)
		}
	}()
	if err := a.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
}
