package patchapp

import (
	"context"
	"testing"
	"time"
)

// startApproval runs RequestApproval on its own goroutine (mirroring the turn
// goroutine in production) and blocks until the pending state is registered,
// so the test can feed key bytes knowing they will hit the approval router.
// The returned channel yields the verdict.
func startApproval(t *testing.T, a *App, question string) <-chan bool {
	t.Helper()
	verdict := make(chan bool, 1)
	go func() { verdict <- a.RequestApproval(context.Background(), question) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		a.mu.Lock()
		pending := a.pendingApproval != nil
		a.mu.Unlock()
		if pending {
			return verdict
		}
		if time.Now().After(deadline) {
			t.Fatal("RequestApproval never registered a pending prompt")
		}
		time.Sleep(time.Millisecond)
	}
}

// awaitVerdict reads the verdict with a timeout so a broken implementation
// fails the test instead of wedging the suite.
func awaitVerdict(t *testing.T, verdict <-chan bool) bool {
	t.Helper()
	select {
	case v := <-verdict:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("RequestApproval did not return within 2s")
		return false
	}
}

// TestApproval_YApproves: pressing 'y' while a prompt is pending resolves true.
func TestApproval_YApproves(t *testing.T) {
	a := newBusyApp()
	verdict := startApproval(t, a, "run rm -rf /tmp/x?")

	a.processInput([]byte("y"))

	if !awaitVerdict(t, verdict) {
		t.Fatal("verdict after 'y' = false; want true")
	}
}

// TestApproval_UppercaseYApproves: 'Y' also approves.
func TestApproval_UppercaseYApproves(t *testing.T) {
	a := newBusyApp()
	verdict := startApproval(t, a, "run tool?")

	a.processInput([]byte("Y"))

	if !awaitVerdict(t, verdict) {
		t.Fatal("verdict after 'Y' = false; want true")
	}
}

// TestApproval_NDenies: pressing 'n' resolves false.
func TestApproval_NDenies(t *testing.T) {
	a := newBusyApp()
	verdict := startApproval(t, a, "run tool?")

	a.processInput([]byte("n"))

	if awaitVerdict(t, verdict) {
		t.Fatal("verdict after 'n' = true; want false")
	}
}

// TestApproval_EscDenies: a lone Escape resolves false.
func TestApproval_EscDenies(t *testing.T) {
	a := newBusyApp()
	verdict := startApproval(t, a, "run tool?")

	a.processInput([]byte{27})

	if awaitVerdict(t, verdict) {
		t.Fatal("verdict after Esc = true; want false")
	}
}

// TestApproval_EnterDenies: Enter is the default — No.
func TestApproval_EnterDenies(t *testing.T) {
	a := newBusyApp()
	verdict := startApproval(t, a, "run tool?")

	a.processInput([]byte{'\r'})

	if awaitVerdict(t, verdict) {
		t.Fatal("verdict after Enter = true; want false")
	}
}

// TestApproval_OtherKeysIgnoredThenYApproves: keys that are not part of the
// y/N protocol are swallowed (no resolution, no editing) and a later 'y'
// still approves.
func TestApproval_OtherKeysIgnoredThenYApproves(t *testing.T) {
	a := newBusyApp()
	verdict := startApproval(t, a, "run tool?")

	// 'a', 'b', backspace, left arrow: all ignored while pending.
	a.processInput([]byte("ab"))
	a.processInput([]byte{127})
	a.processInput([]byte{27, '[', 'D'})

	select {
	case v := <-verdict:
		t.Fatalf("prompt resolved (%v) by a non-protocol key; want still pending", v)
	case <-time.After(50 * time.Millisecond):
	}

	a.processInput([]byte("y"))
	if !awaitVerdict(t, verdict) {
		t.Fatal("verdict after ignored keys + 'y' = false; want true")
	}
}

// TestApproval_TypedCharsDoNotReachEditor: characters typed while a prompt
// is pending must not land in the editor's input line.
func TestApproval_TypedCharsDoNotReachEditor(t *testing.T) {
	a := newBusyApp()
	verdict := startApproval(t, a, "run tool?")

	a.processInput([]byte("hello"))
	a.processInput([]byte("y"))
	awaitVerdict(t, verdict)

	if got := string(a.ed.input); got != "" {
		t.Fatalf("editor input after pending-approval typing = %q; want empty", got)
	}
}

// TestApproval_PasteStartingWhilePendingIsSwallowed: a bracketed paste that
// begins while a prompt is pending is discarded whole — pasted 'y'/'n'
// characters must neither answer the prompt nor reach the editor — and a
// real 'y' afterwards still approves.
func TestApproval_PasteStartingWhilePendingIsSwallowed(t *testing.T) {
	a := newBusyApp()
	verdict := startApproval(t, a, "run tool?")

	a.processInput([]byte("\x1b[200~yny\x1b[201~"))

	select {
	case v := <-verdict:
		t.Fatalf("pasted text resolved the prompt (%v); want still pending", v)
	case <-time.After(50 * time.Millisecond):
	}
	if got := string(a.ed.input); got != "" {
		t.Fatalf("editor input after swallowed paste = %q; want empty", got)
	}

	a.processInput([]byte("y"))
	if !awaitVerdict(t, verdict) {
		t.Fatal("verdict after swallowed paste + 'y' = false; want true")
	}
}

// TestApproval_PasteStartingWhilePendingIsSwallowed_SplitReads: same as above
// but the paste arrives across multiple terminal reads.
func TestApproval_PasteStartingWhilePendingIsSwallowed_SplitReads(t *testing.T) {
	a := newBusyApp()
	verdict := startApproval(t, a, "run tool?")

	a.processInput([]byte("\x1b[200~y"))
	a.processInput([]byte("ny"))
	a.processInput([]byte("\x1b[201~"))

	select {
	case v := <-verdict:
		t.Fatalf("pasted text resolved the prompt (%v); want still pending", v)
	case <-time.After(50 * time.Millisecond):
	}
	if got := string(a.ed.input); got != "" {
		t.Fatalf("editor input after swallowed paste = %q; want empty", got)
	}

	a.processInput([]byte("y"))
	if !awaitVerdict(t, verdict) {
		t.Fatal("verdict after swallowed paste + 'y' = false; want true")
	}
}

// TestApproval_PasteEndingWhilePendingIsDropped: a paste that started BEFORE
// the prompt appeared but completes while it is pending must not commit its
// buffer into the editor, and must not resolve the prompt.
func TestApproval_PasteEndingWhilePendingIsDropped(t *testing.T) {
	a := newBusyApp()
	a.processInput([]byte("\x1b[200~hel"))

	verdict := startApproval(t, a, "run tool?")

	a.processInput([]byte("lo\x1b[201~"))

	select {
	case v := <-verdict:
		t.Fatalf("paste completion resolved the prompt (%v); want still pending", v)
	case <-time.After(50 * time.Millisecond):
	}
	if got := string(a.ed.input); got != "" {
		t.Fatalf("editor input after mid-prompt paste end = %q; want empty", got)
	}

	a.processInput([]byte("y"))
	if !awaitVerdict(t, verdict) {
		t.Fatal("verdict after dropped paste + 'y' = false; want true")
	}
}

// TestApproval_CtrlCDeniesWithoutQuitting: ctrl+c while pending resolves
// false and does NOT prime the double-tap exit or quit the app.
func TestApproval_CtrlCDeniesWithoutQuitting(t *testing.T) {
	a := newBusyApp()
	verdict := startApproval(t, a, "run tool?")

	if exit := a.processInput([]byte{3}); exit {
		t.Fatal("processInput(ctrl+c) while pending requested exit; want no quit")
	}
	if awaitVerdict(t, verdict) {
		t.Fatal("verdict after ctrl+c = true; want false")
	}
	if a.exitFlag {
		t.Fatal("ctrl+c while pending set exitFlag; want app still running")
	}
	// A second ctrl+c right after must not be treated as a double-tap exit.
	if exit := a.processInput([]byte{3}); exit {
		t.Fatal("ctrl+c after a pending-approval ctrl+c exited; want double-tap not primed")
	}
}

// TestApproval_QuitResolvesFalse: Quit while a prompt is pending denies it so
// the blocked turn goroutine cannot wedge teardown.
func TestApproval_QuitResolvesFalse(t *testing.T) {
	a := newBusyApp()
	verdict := startApproval(t, a, "run tool?")

	a.Quit()

	if awaitVerdict(t, verdict) {
		t.Fatal("verdict after Quit = true; want false")
	}
}

// TestApproval_AfterQuit_ReturnsFalseImmediately: RequestApproval on an app
// that is already exiting denies without blocking.
func TestApproval_AfterQuit_ReturnsFalseImmediately(t *testing.T) {
	a := newBusyApp()
	a.Quit()

	done := make(chan bool, 1)
	go func() { done <- a.RequestApproval(context.Background(), "run tool?") }()

	if awaitVerdict(t, done) {
		t.Fatal("RequestApproval after Quit = true; want false")
	}
}

// TestApproval_CtxCancelDeniesAndUnblocks: cancelling the turn ctx while a
// prompt is pending (no y/N key ever arrives) unblocks RequestApproval with a
// false verdict and clears the pending state, so the turn goroutine can't wedge.
func TestApproval_CtxCancelDeniesAndUnblocks(t *testing.T) {
	a := newBusyApp()

	ctx, cancel := context.WithCancel(context.Background())
	verdict := make(chan bool, 1)
	go func() { verdict <- a.RequestApproval(ctx, "run tool?") }()

	// Wait for the prompt to register.
	deadline := time.Now().Add(2 * time.Second)
	for {
		a.mu.Lock()
		pending := a.pendingApproval != nil
		a.mu.Unlock()
		if pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("RequestApproval never registered a pending prompt")
		}
		time.Sleep(time.Millisecond)
	}

	cancel()

	if awaitVerdict(t, verdict) {
		t.Fatal("verdict after ctx cancel = true; want false")
	}
	// Pending state must be cleared so a stray later key can't send on the
	// orphaned channel.
	a.mu.Lock()
	pending := a.pendingApproval != nil
	a.mu.Unlock()
	if pending {
		t.Fatal("pendingApproval not cleared after ctx-cancel deny")
	}
}

// TestApproval_SecondRequestBlocksUntilFirstResolves: concurrent
// RequestApproval calls are serialized — the second prompt only becomes
// pending after the first is answered, and each gets its own verdict.
func TestApproval_SecondRequestBlocksUntilFirstResolves(t *testing.T) {
	a := newBusyApp()
	first := startApproval(t, a, "first?")

	second := make(chan bool, 1)
	go func() { second <- a.RequestApproval(context.Background(), "second?") }()

	// The second request must not resolve or steal the pending slot yet.
	select {
	case v := <-second:
		t.Fatalf("second RequestApproval resolved (%v) before the first; want blocked", v)
	case <-time.After(50 * time.Millisecond):
	}

	a.processInput([]byte("y"))
	if !awaitVerdict(t, first) {
		t.Fatal("first verdict = false; want true")
	}

	// Now the second becomes pending; deny it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		a.mu.Lock()
		pending := a.pendingApproval != nil
		a.mu.Unlock()
		if pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second RequestApproval never became pending after the first resolved")
		}
		time.Sleep(time.Millisecond)
	}
	a.processInput([]byte("n"))
	if awaitVerdict(t, second) {
		t.Fatal("second verdict = true; want false")
	}
}
