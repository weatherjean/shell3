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

// A stray Resume (no preceding Pause) used to unconditionally Unlock readMu,
// which panics on an unheld RWMutex. Resume on a not-paused App must be a
// no-op: it returns early before touching the terminal or the read lock.
func TestResumeOnNotPausedIsNoOp(t *testing.T) {
	a := New("test", "", WelcomeInfo{})

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Resume panicked on a non-paused App: %v", r)
		}
	}()

	if err := a.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	a.mu.Lock()
	paused := a.term.paused
	a.mu.Unlock()
	if paused {
		t.Fatalf("paused = true after no-op Resume; want false (unchanged)")
	}

	// readMu must still be free: a stray Resume must not have Unlocked an
	// unheld lock or otherwise corrupted it. A non-blocking TryLock confirms
	// the mutex is in a sane, acquirable state.
	if !a.term.readMu.TryLock() {
		t.Fatalf("readMu not acquirable after no-op Resume; lock left in a bad state")
	}
	a.term.readMu.Unlock()
}
