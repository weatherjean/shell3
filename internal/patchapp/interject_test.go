package patchapp

import (
	"testing"
)

// newBusyApp is a test helper that builds an App and marks it busy, so tests
// can exercise the busy-state input paths without starting a real render loop.
func newBusyApp() *App {
	a := New("test", "", WelcomeInfo{})
	a.r.SetOutput(discardWriter{})
	setBusy(a, true)
	return a
}

// TestTypingWhileBusy_CharInsertWorks: typing a character while busy should
// insert it into the editor (not be swallowed). This is the opposite of the
// old swallow-while-busy behavior.
func TestTypingWhileBusy_CharInsertWorks(t *testing.T) {
	a := newBusyApp()

	// Type "abc" while busy.
	a.processInput([]byte("abc"))

	got := string(a.ed.input)
	if got != "abc" {
		t.Fatalf("typed chars while busy = %q; want %q", got, "abc")
	}
}

// TestTypingWhileBusy_BackspaceWorks: backspace while busy should remove the
// last inserted character.
func TestTypingWhileBusy_BackspaceWorks(t *testing.T) {
	a := newBusyApp()
	a.processInput([]byte("abc"))
	// Backspace (0x7f = DEL / backspace in terminals).
	a.processInput([]byte{127})

	got := string(a.ed.input)
	if got != "ab" {
		t.Fatalf("input after backspace while busy = %q; want \"ab\"", got)
	}
}

// TestTypingWhileBusy_CursorMovementWorks: left/right arrow keys while busy
// should move the cursor in the pre-typed buffer.
func TestTypingWhileBusy_CursorMovementWorks(t *testing.T) {
	a := newBusyApp()
	a.processInput([]byte("abc"))

	cursorBefore := a.ed.cursor // should be 3

	// Left arrow = ESC [ D
	a.processInput([]byte{27, '[', 'D'})
	if a.ed.cursor != cursorBefore-1 {
		t.Fatalf("cursor after left while busy = %d; want %d", a.ed.cursor, cursorBefore-1)
	}

	// Right arrow = ESC [ C
	a.processInput([]byte{27, '[', 'C'})
	if a.ed.cursor != cursorBefore {
		t.Fatalf("cursor after right while busy = %d; want %d", a.ed.cursor, cursorBefore)
	}
}

// TestTypingWhileBusy_PasteWorks: bracketed paste while busy should land in
// the editor (not be silently dropped).
func TestTypingWhileBusy_PasteWorks(t *testing.T) {
	a := newBusyApp()
	a.processInput([]byte(pasteStart + "pasted" + pasteEnd))

	got := string(a.ed.input)
	if got != "pasted" {
		t.Fatalf("pasted text while busy = %q; want \"pasted\"", got)
	}
}

// TestEnterWhileBusy_EmptyInput_Noop: pressing Enter while busy with empty
// input should do nothing (no callback, no output).
func TestEnterWhileBusy_EmptyInput_Noop(t *testing.T) {
	a := newBusyApp()
	called := false
	a.SetInterject(func(string) { called = true })

	a.handleEnter()

	if called {
		t.Fatal("onInterject fired on empty input while busy; want no-op")
	}
	if len(a.ed.input) != 0 {
		t.Fatalf("input should remain empty; got %q", string(a.ed.input))
	}
}

// TestEnterWhileBusy_SlashInput_Preserved: pressing Enter while busy with a
// slash command should print a dim notice and keep the input intact (not
// call onInterject and not submit the slash handler).
func TestEnterWhileBusy_SlashInput_Preserved(t *testing.T) {
	a := newBusyApp()
	interjected := false
	a.SetInterject(func(string) { interjected = true })
	slashHit := false
	a.RegisterSlash(SlashCommand{Name: "clear", Handler: func(string) { slashHit = true }})

	// Seed the input with "/clear".
	setInputForTest(a, "/clear")
	// Re-mark busy since setInputForTest needs the lock but doesn't change busy.
	setBusy(a, true)

	a.handleEnter()

	if interjected {
		t.Fatal("onInterject should NOT fire for slash input while busy")
	}
	if slashHit {
		t.Fatal("slash handler should NOT fire while busy")
	}
	// Input should be preserved — user can submit after the turn ends.
	if got := string(a.ed.input); got != "/clear" {
		t.Fatalf("input should be preserved as \"/clear\"; got %q", got)
	}
}

// TestEnterWhileBusy_BangInput_Preserved: pressing Enter while busy with a
// bang command (!) should print a dim notice and keep the input intact.
func TestEnterWhileBusy_BangInput_Preserved(t *testing.T) {
	a := newBusyApp()
	interjected := false
	a.SetInterject(func(string) { interjected = true })

	setInputForTest(a, "!ls")
	setBusy(a, true)

	a.handleEnter()

	if interjected {
		t.Fatal("onInterject should NOT fire for ! input while busy")
	}
	if got := string(a.ed.input); got != "!ls" {
		t.Fatalf("input should be preserved as \"!ls\"; got %q", got)
	}
}

// TestEnterWhileBusy_PlainText_CallsInterject: pressing Enter while busy
// with plain text should call onInterject with the text and clear the editor.
func TestEnterWhileBusy_PlainText_CallsInterject(t *testing.T) {
	a := newBusyApp()
	var received string
	a.SetInterject(func(text string) { received = text })

	setInputForTest(a, "stop, change approach")
	setBusy(a, true)

	a.handleEnter()

	if received != "stop, change approach" {
		t.Fatalf("onInterject received %q; want %q", received, "stop, change approach")
	}
	if len(a.ed.input) != 0 {
		t.Fatalf("input should be cleared after interject; got %q", string(a.ed.input))
	}
}

// TestEnterWhileBusy_PlainText_NilCallback_Preserved: pressing Enter while
// busy with plain text and NO onInterject callback set should preserve the
// input (fall back to the historical no-op behavior).
func TestEnterWhileBusy_PlainText_NilCallback_Preserved(t *testing.T) {
	a := newBusyApp()
	// No SetInterject call.

	setInputForTest(a, "some text")
	setBusy(a, true)

	a.handleEnter()

	if got := string(a.ed.input); got != "some text" {
		t.Fatalf("without onInterject, input should be preserved; got %q", got)
	}
}

// TestHistoryNavGatedWhileBusy: Up/Down arrow keys are still gated while
// busy — history navigation must not run during a turn.
func TestHistoryNavGatedWhileBusy(t *testing.T) {
	a := newBusyApp()
	a.ed.history = []string{"old command"}
	// cursor should stay at 0 (no history navigation runs).

	// Up arrow = ESC [ A
	a.processInput([]byte{27, '[', 'A'})
	if a.ed.historyIdx != 0 {
		t.Fatalf("history navigation ran while busy (historyIdx=%d); want 0", a.ed.historyIdx)
	}
}

// TestTabGatedWhileBusy: the Tab callback is not fired while busy.
func TestTabGatedWhileBusy(t *testing.T) {
	a := newBusyApp()
	fired := false
	a.SetTab(func() { fired = true })

	a.handleTab()

	if fired {
		t.Fatal("Tab callback fired while busy; want gated")
	}
}
