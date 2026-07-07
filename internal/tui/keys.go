package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := msg.String()

	// A pending on_tool_call ask is modal and takes priority over everything.
	if m.confirm != nil {
		return m.handleConfirmKey(s)
	}

	// The help overlay is modal: any key dismisses it.
	if m.helpOpen {
		m.helpOpen = false
		return m, nil
	}

	// The background-jobs modal owns every key while open (so esc/q close it and
	// ctrl+c can't arm quit underneath).
	if m.bg.open {
		return m.handleBackgroundKey(s)
	}

	// Ctrl+C is global: cancel a running turn, else require a second press.
	if s == "ctrl+c" {
		if m.busy && m.cancel != nil {
			m.cancel()
			m.canceling = true
			m.notice = "cancelling…"
			return m, nil
		}
		if m.quitArmed {
			return m, tea.Quit
		}
		m.quitArmed = true
		m.notice = "press ctrl+c again to quit"
		return m, nil
	}
	if m.quitArmed {
		m.quitArmed = false
		m.notice = ""
	}

	// esc also dismisses a mouse selection (without early-returning, so it still
	// does its mode-specific work below).
	if s == "esc" && m.hasSel {
		m.hasSel = false
		m.refresh(false)
	}

	// The ctrl+p command palette owns every key while open (esc closes it, like
	// the other modals above).
	if m.palette.open {
		return m.handlePaletteKey(msg, s)
	}
	if s == "ctrl+p" {
		m.openPalette()
		return m, nil
	}

	return m.handleInputKey(msg, s)
}

// handleInputKey drives the single always-live input: Enter sends (or queues
// steering while a turn is busy); every other key not special-cased below is
// forwarded to the textarea, so typing "just works" with no separate mode to
// leave first.
func (m *model) handleInputKey(msg tea.KeyPressMsg, s string) (tea.Model, tea.Cmd) {
	switch s {
	case "enter":
		text := strings.TrimSpace(m.ta.Value())
		if text == "" {
			return m, nil
		}
		// Mid-turn: queue the message as steering instead of refusing it. The
		// session delivers it at the next round boundary (or we auto-run it when
		// the turn ends — see handleEvent).
		if m.busy {
			if m.steer == nil {
				return m, nil
			}
			m.steer(text)
			m.tr.AddSteer(text)
			m.ta.Reset()
			m.relayout()
			m.follow = true
			m.refresh(true)
			m.notice = "steering queued"
			return m, nil
		}
		m.tr.AddUser(text)
		m.ta.Reset()
		m.relayout()
		m.follow = true // stick to the bottom to watch the reply stream in
		m.refresh(true)
		m.busy = true
		m.notice = ""
		ch, cancel := m.send(text)
		m.cancel = cancel
		return m, tea.Batch(waitEvent(ch), m.startSpin())
	case "?":
		// On an empty input, '?' opens help (matching the footer hint) rather
		// than typing a literal '?'. With text present it inserts normally.
		if strings.TrimSpace(m.ta.Value()) == "" {
			m.helpOpen = true
			return m, nil
		}
	case "ctrl+u", "shift+backspace":
		// Clear the whole input draft. ctrl+u is the reliable binding; most
		// terminals can't distinguish shift+backspace from plain backspace
		// (same 0x7f byte), so we intercept ctrl+u before the textarea sees it.
		m.ta.Reset()
		m.relayout()
		m.notice = "input cleared"
		return m, nil
	case "ctrl+o":
		// Compose the draft in $EDITOR (same as the palette's edit command).
		return m, m.openEditor()
	case "tab":
		// Tab cycles agents (gated while busy); never inserts a literal tab.
		m.cycleAgent()
		return m, nil
	case "pgup", "pgdown":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		m.syncFollow()
		return m, cmd
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	m.relayout()
	return m, cmd
}

// cycleAgent switches to the next agent in the registry (Tab). No-op while busy
// (a turn owns the agent) or when fewer than two agents exist.
func (m *model) cycleAgent() {
	if m.busy || m.cmds == nil {
		return
	}
	names := m.cmds.AgentNames()
	if len(names) < 2 {
		return
	}
	cur := 0
	active := m.cmds.ActiveAgent()
	for i, n := range names {
		if n == active {
			cur = i
			break
		}
	}
	next := names[(cur+1)%len(names)]
	if err := m.cmds.SwitchAgent(next); err != nil {
		m.notice = "error: " + err.Error()
		return
	}
	m.applyAgent()
	m.notice = "agent: " + m.agentName
}

// applyAgent refreshes the agent name and status line (model label, context
// window) from a fresh snapshot after the active agent changes.
func (m *model) applyAgent() {
	snap := m.cmds.Snapshot()
	m.agentName = snap.Agent
	m.modelName = snap.Model
	m.contextWindow = snap.ContextWindow
}
