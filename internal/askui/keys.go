package askui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	// Any key other than a second ctrl+c disarms the quit prompt.
	if k != "ctrl+c" {
		m.quitArmed = false
	}

	switch k {
	case "ctrl+c":
		return m.handleInterrupt()
	case "ctrl+o":
		// All-or-nothing, and the only fold binding: the input is always live,
		// so a plain key can't be one, and a keyboard block cursor costs more
		// than it returns when the mouse already folds a single block (a click
		// on its header — see mouse.go).
		m.tr.foldAll(m.tr.anyUnfolded())
		m.refresh(false)
		return m, nil
	case "pgup":
		m.vp.PageUp()
		m.syncFollow()
		return m, nil
	case "pgdown":
		m.vp.PageDown()
		m.syncFollow()
		return m, nil
	case "enter":
		return m.submit()
	}

	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	m.relayout() // the input may have grown/shrunk a line
	return m, cmd
}

// handleInterrupt is ctrl+c: it cancels a running turn, and otherwise quits on
// the second consecutive press. A running turn is never quit outright — the
// first press stops the model, and only a further press ends the program.
func (m *model) handleInterrupt() (tea.Model, tea.Cmd) {
	if m.busy && m.cancel != nil {
		m.canceling = true
		m.notice = "stopping…"
		m.cancel()
		m.cancel = nil
		return m, nil
	}
	if m.quitArmed {
		return m, tea.Quit
	}
	m.quitArmed = true
	return m, nil
}

// submit sends the input as a turn, or — when a turn is already running —
// interjects it as steering, which the session delivers at the next round
// boundary (and, if it lands after the last one, runs as a follow-up turn when
// the current one ends; see handleEvent).
func (m *model) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.ta.Value())
	if text == "" {
		return m, nil
	}
	m.ta.Reset()
	m.follow = true

	if m.busy {
		if m.steer == nil {
			return m, nil
		}
		m.steer(text)
		m.tr.addSteer(text)
		m.notice = "steering the running turn"
		m.refresh(true)
		m.relayout()
		return m, nil
	}

	m.tr.addUser(text)
	m.refresh(true)
	m.relayout()
	ch, cancel := m.send(text)
	m.busy = true
	m.cancel = cancel
	m.notice = ""
	return m, tea.Batch(waitEvent(ch), m.startSpin())
}
