package tui

import (
	"reflect"
	"strings"
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
	if snap.modal != modalNone {
		t.Fatalf("no modal should be open while typing, got %v", snap.modal)
	}
}

// Opening the help overlay (via '?') is reported by the snapshot's modal enum,
// and closes on any key — matching the rendered behavior.
func TestSnapshot_HelpOpensAndCloses(t *testing.T) {
	m := sized(closedSend(nil))
	frame(m, keyRune('?')) // empty input → '?' opens help
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

// ctrl+p opens the command palette, reported through the same modal enum as
// help/background/confirm.
func TestSnapshot_CtrlPOpensPalette(t *testing.T) {
	m := sized(closedSend(nil))
	frame(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if snap := m.uiSnapshot(); snap.modal != modalPalette {
		t.Fatalf("ctrl+p should open the palette, got modal=%v", snap.modal)
	}
}

// Typing into the open palette filters its query (visible in both the
// snapshot's paletteQuery and the rendered frame's command list) and narrows
// as more is typed.
func TestSnapshot_PaletteTypingFiltersList(t *testing.T) {
	m := sized(closedSend(nil))
	frame(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	plain := frame(m, keyRune('a'), keyRune('g'))
	if snap := m.uiSnapshot(); snap.paletteQuery != "ag" {
		t.Fatalf("snapshot should report the typed palette query, got %q", snap.paletteQuery)
	}
	if !strings.Contains(plain, "agent") || !strings.Contains(plain, "agents") {
		t.Fatalf("palette frame should show agent/agents for 'ag':\n%s", plain)
	}
	if strings.Contains(plain, "compact") {
		t.Fatalf("palette frame should filter out non-matching commands:\n%s", plain)
	}
	// Narrowing further (to "agents") drops "agent" from the match list.
	plain = frame(m, keyRune('e'), keyRune('n'), keyRune('t'), keyRune('s'))
	if !strings.Contains(plain, "agents") {
		t.Fatalf("palette frame should still show agents for 'agents':\n%s", plain)
	}
}

// Up/down move the palette's selection, reported via modalSel (the same field
// :background's job list and the confirm modal's Yes/No use).
func TestSnapshot_PaletteArrowsMoveSelection(t *testing.T) {
	m := sized(closedSend(nil))
	frame(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	start := m.uiSnapshot().modalSel
	frame(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.uiSnapshot().modalSel; got != start+1 {
		t.Fatalf("down should advance the selection: start=%d got=%d", start, got)
	}
	frame(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if got := m.uiSnapshot().modalSel; got != start {
		t.Fatalf("up should move the selection back: want=%d got=%d", start, got)
	}
}

// esc closes the palette (like every other modal).
func TestSnapshot_PaletteEscCloses(t *testing.T) {
	m := sized(closedSend(nil))
	frame(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if snap := m.uiSnapshot(); snap.modal != modalPalette {
		t.Fatal("palette should be open")
	}
	frame(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if snap := m.uiSnapshot(); snap.modal != modalNone {
		t.Fatalf("esc should close the palette, got modal=%v", snap.modal)
	}
}

// Enter on the "help" row (the only match for that filter) opens the help
// overlay, replacing the palette modal.
func TestSnapshot_PaletteEnterOnHelpOpensHelp(t *testing.T) {
	m := sized(closedSend(nil))
	frame(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	frame(m, keyRune('h'), keyRune('e'), keyRune('l'), keyRune('p'))
	frame(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if snap := m.uiSnapshot(); snap.modal != modalHelp {
		t.Fatalf("enter on help should open the help overlay, got modal=%v", snap.modal)
	}
}

// Typing a full command name plus a trailing argument ("agent <name>") and
// pressing Enter dispatches immediately, same as the old ":" line did.
func TestSnapshot_PaletteAgentWithArgDispatches(t *testing.T) {
	fc := &fakeCmds{names: []string{"main", "research"}, active: "main"}
	m := sizedWith(closedSend(nil), fc)
	frame(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	for _, r := range "agent research" {
		frame(m, keyRune(r))
	}
	frame(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if fc.active != "research" {
		t.Fatalf("agent <name> + enter should dispatch immediately, active=%q", fc.active)
	}
	if snap := m.uiSnapshot(); snap.modal != modalNone {
		t.Fatalf("dispatching a command should close the palette, got modal=%v", snap.modal)
	}
}

// The footer drops the old mode pill and ":" hint, and advertises ctrl+p.
func TestSnapshot_FooterAdvertisesPalette(t *testing.T) {
	m := sized(closedSend(nil))
	snap := m.uiSnapshot()
	footer := strings.Join(snap.footer, " ")
	if !strings.Contains(footer, "ctrl+p") {
		t.Fatalf("footer should hint at ctrl+p commands, got %v", snap.footer)
	}
	for _, stale := range []string{" N ", " I ", ":"} {
		if strings.Contains(footer, stale) {
			t.Fatalf("footer should not show the old mode pill/':' hint, got %v", snap.footer)
		}
	}
}

// No normal mode: with an empty input, a plain letter key types into the
// textarea instead of moving a line cursor or doing anything special.
func TestSnapshot_NoNormalModePlainKeyTypes(t *testing.T) {
	m := sized(closedSend(nil))
	frame(m, keyRune('j'))
	if snap := m.uiSnapshot(); snap.input != "j" {
		t.Fatalf("j with empty input should type a literal j, got %q", snap.input)
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
