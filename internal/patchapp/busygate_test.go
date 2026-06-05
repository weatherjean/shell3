package patchapp

import "testing"

// These tests pin the BUSY-GATE invariant documented on handleEnter/handleTab:
// while the App is busy (a turn is streaming), no user-input dispatch —
// SubmitFunc, slash handlers, or the Tab callback — may fire. That gate is the
// sole reason the TUI can share *chat.Config between the event-drain goroutine
// and the input goroutine without a mutex (see internal/tui/interactive.go's
// CONCURRENCY INVARIANT). If a future change lets dispatch run while busy, the
// underlying data race becomes silent — these tests turn it into a build break.

// setInputForTest seeds the live input line the way a sequence of keystrokes
// would, so handleEnter has something to submit.
func setInputForTest(a *App, s string) {
	a.mu.Lock()
	a.ed.input = []rune(s)
	a.ed.cursor = len(a.ed.input)
	a.mu.Unlock()
}

func setBusy(a *App, busy bool) {
	a.mu.Lock()
	a.busy = busy
	a.mu.Unlock()
}

func TestBusyGate_SubmitSuppressedWhileBusy(t *testing.T) {
	a := New("m", "s", WelcomeInfo{})
	fired := false
	a.SetSubmit(func(string) { fired = true })
	setInputForTest(a, "hello")
	setBusy(a, true)

	a.handleEnter()

	if fired {
		t.Fatal("SubmitFunc fired while busy — busy-gate broken; cfg is now racy")
	}
}

func TestBusyGate_SubmitFiresWhenIdle(t *testing.T) {
	a := New("m", "s", WelcomeInfo{})
	got := ""
	a.SetSubmit(func(in string) { got = in })
	setInputForTest(a, "hello")

	a.handleEnter()

	if got != "hello" {
		t.Fatalf("idle submit not dispatched: got %q, want %q", got, "hello")
	}
}

func TestBusyGate_SlashSuppressedWhileBusy(t *testing.T) {
	a := New("m", "s", WelcomeInfo{})
	hit := false
	a.RegisterSlash(SlashCommand{Name: "x", Handler: func(string) { hit = true }})
	setInputForTest(a, "/x")
	setBusy(a, true)

	a.handleEnter()

	if hit {
		t.Fatal("slash handler fired while busy — busy-gate broken; cfg is now racy")
	}
}

func TestBusyGate_SlashFiresWhenIdle(t *testing.T) {
	a := New("m", "s", WelcomeInfo{})
	gotArgs := ""
	hit := false
	a.RegisterSlash(SlashCommand{Name: "x", Handler: func(args string) { hit = true; gotArgs = args }})
	setInputForTest(a, "/x  arg1 arg2")

	a.handleEnter()

	if !hit {
		t.Fatal("idle slash not dispatched")
	}
	if gotArgs != "arg1 arg2" {
		t.Fatalf("slash args = %q, want %q", gotArgs, "arg1 arg2")
	}
}

func TestBusyGate_TabSuppressedWhileBusy(t *testing.T) {
	a := New("m", "s", WelcomeInfo{})
	fired := false
	a.SetTab(func() { fired = true })
	setBusy(a, true)

	a.handleTab()

	if fired {
		t.Fatal("Tab callback fired while busy — busy-gate broken; cfg is now racy")
	}
}

func TestBusyGate_TabFiresWhenIdle(t *testing.T) {
	a := New("m", "s", WelcomeInfo{})
	fired := false
	a.SetTab(func() { fired = true })

	a.handleTab()

	if !fired {
		t.Fatal("idle Tab callback not dispatched")
	}
}
