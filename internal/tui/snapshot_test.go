package tui

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Typing into the input is visible both in the snapshot's input field and in
// the rendered frame — the two views of state must agree.
func TestSnapshot_InputReflectsTypedText(t *testing.T) {
	m := sized(closedSend(nil))
	frame(m, keyRune('h'), keyRune('i'))
	snap := m.uiSnapshot()
	if snap.input != "hi" {
		t.Fatalf("snapshot input should reflect what was typed, got %q", snap.input)
	}
	if snap.mode != modeInsert {
		t.Fatalf("typing should stay in INSERT, got %v", snap.mode)
	}
	if snap.modal != modalNone {
		t.Fatalf("no modal should be open while typing, got %v", snap.modal)
	}
}

// Opening the help overlay (via '?') is reported by the snapshot's modal enum,
// and closes on any key — matching the rendered behavior.
func TestSnapshot_HelpOpensAndCloses(t *testing.T) {
	m := sized(closedSend(nil))
	frame(m, tea.KeyPressMsg{Code: tea.KeyEscape}) // → NORMAL, where ? opens help
	frame(m, keyRune('?'))
	if snap := m.uiSnapshot(); snap.modal != modalHelp {
		t.Fatalf("snapshot should report modalHelp open, got %v", snap.modal)
	}
	frame(m, keyRune('j')) // any key dismisses help
	if snap := m.uiSnapshot(); snap.modal != modalNone {
		t.Fatalf("snapshot should report no modal once help closes, got %v", snap.modal)
	}
}

// The confirm (on_tool_call ask) modal reports through the same enum, with
// modalSel tracking the selected button.
func TestSnapshot_ConfirmModalAndSelection(t *testing.T) {
	m := sized(closedSend(nil))
	reply := make(chan bool, 1)
	frame(m, confirmMsg{req: &confirmReq{command: "rm -rf x", reply: reply}})
	snap := m.uiSnapshot()
	if snap.modal != modalConfirm {
		t.Fatalf("snapshot should report modalConfirm, got %v", snap.modal)
	}
	if snap.modalSel != 0 {
		t.Fatalf("Yes is selected by default, want modalSel=0, got %d", snap.modalSel)
	}
	frame(m, tea.KeyPressMsg{Code: 'l'}) // → select No
	if snap := m.uiSnapshot(); snap.modalSel != 1 {
		t.Fatalf("after selecting No, want modalSel=1, got %d", snap.modalSel)
	}
}

// blockCount tracks the transcript's block count as items are added, and
// scrollY/follow reflect the viewport's autoscroll state.
func TestSnapshot_BlockCountAndScrollFollow(t *testing.T) {
	m := sized(closedSend(nil))
	if snap := m.uiSnapshot(); snap.blockCount != 0 {
		t.Fatalf("empty transcript should have blockCount 0, got %d", snap.blockCount)
	}
	m.tr.AddUser("one")
	m.tr.AddUser("two")
	m.refresh(true)
	snap := m.uiSnapshot()
	if snap.blockCount != 2 {
		t.Fatalf("two user items should produce blockCount 2, got %d", snap.blockCount)
	}
	if !snap.follow {
		t.Fatal("should still be following (locked to bottom) after adding content")
	}
}

// Two snapshots of the same static state, taken back to back, must be
// identical — proving the struct excludes wall-clock-only animation state
// (spinner phase, cursor blink) that would otherwise make it flaky.
func TestSnapshot_DeterministicAcrossRepeatedCalls(t *testing.T) {
	m := sized(closedSend(nil))
	m.busy = true // exercises the "thinking" footer segment, which is spinner-driven when rendered
	a := m.uiSnapshot()
	m.spinner++ // simulate time passing (a spinnerTick would bump this)
	b := m.uiSnapshot()
	if !reflect.DeepEqual(a, b) { // uiState holds a []string (footer), so compare deeply
		t.Fatalf("snapshot should be stable across a spinner tick: %+v vs %+v", a, b)
	}
}
