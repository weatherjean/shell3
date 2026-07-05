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

	// The :background modal owns every key while open (so esc/q close it and
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

	switch m.mode {
	case modeCommand:
		return m.handleCommandKey(s)
	case modeNormal:
		return m.handleNormalKey(s)
	default:
		return m.handleInsertKey(msg, s)
	}
}

func (m *model) handleInsertKey(msg tea.KeyPressMsg, s string) (tea.Model, tea.Cmd) {
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
		// Compose the draft in $EDITOR (same as :edit).
		return m, m.openEditor()
	case "esc":
		// Leave the draft intact; just switch to NORMAL (use shift+backspace or
		// dd to clear).
		m.enterNormal()
		return m, nil
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

func (m *model) handleNormalKey(s string) (tea.Model, tea.Cmd) {
	// esc cancels any pending prefix.
	if s == "esc" {
		m.pending = 0
		return m, nil
	}

	// Multi-key prefixes: gg, zM, zR, dd.
	if m.pending != 0 {
		p := m.pending
		m.pending = 0
		switch {
		case p == 'g' && s == "g":
			m.cursorLine = 0
			m.follow = false
			m.refresh(false)
			m.vp.SetYOffset(0)
		case p == 'z' && s == "M":
			m.tr.FoldAll(true)
			m.refresh(false)
		case p == 'z' && s == "R":
			m.tr.FoldAll(false)
			m.refresh(false)
		case p == 'd' && s == "d":
			// dd clears the input draft.
			m.ta.Reset()
			m.relayout()
			m.notice = "input cleared"
		}
		return m, nil
	}

	switch s {
	case "g", "z", "d":
		m.pending = rune(s[0])
	case "y":
		m.notice = "yanked to clipboard"
		return m, copyToClipboard(m.tr.raw(m.blockAtLine(m.cursorLine)))
	case "j", "down":
		m.moveLine(1) // move the line cursor (scrolls only at the edge)
	case "k", "up":
		m.moveLine(-1)
	case "}":
		m.jumpBlock(1) // next block
	case "{":
		m.jumpBlock(-1) // previous block
	case "G":
		// G / shift+g jumps to the bottom AND locks autoscroll (follow).
		m.cursorLine = m.totalLines - 1
		m.follow = true
		m.refresh(false)
		m.vp.GotoBottom()
		m.notice = "following"
	case "ctrl+d":
		m.moveLine(m.vp.Height() / 2)
	case "ctrl+u":
		m.moveLine(-m.vp.Height() / 2)
	case "enter", " ":
		b := m.blockAtLine(m.cursorLine)
		if m.tr.ToggleFold(b) {
			m.refresh(false)
			if b < len(m.blockStarts) {
				m.cursorLine = m.blockStarts[b]
			}
			m.refresh(false)
			m.ensureLineVisible()
		}
	case "tab":
		m.cycleAgent()
	case "i", "a":
		return m, m.enterInsert()
	case ":":
		m.mode = modeCommand
		m.cmdline = ""
	case "?":
		m.helpOpen = true
	}
	return m, nil
}

// jumpBlock moves the line cursor to the start of the adjacent block. Pressing
// } while already on the last block jumps to the very bottom (and re-locks
// autoscroll), so a final long block can be reached without line-stepping.
func (m *model) jumpBlock(d int) {
	cur := m.blockAtLine(m.cursorLine)
	if d > 0 && cur >= len(m.blockStarts)-1 {
		m.cursorLine = m.totalLines - 1
		m.follow = true
		m.refresh(false)
		m.vp.GotoBottom()
		return
	}
	b := min(max(cur+d, 0), len(m.blockStarts)-1)
	m.cursorLine = m.blockStarts[b]
	m.follow = false
	m.refresh(false)
	m.ensureLineVisible()
	m.syncFollow()
}

// moveLine moves the line cursor by d, redraws, and keeps it visible.
func (m *model) moveLine(d int) {
	m.cursorLine += d
	if m.cursorLine < 0 {
		m.cursorLine = 0
	}
	m.cursorLine = min(m.cursorLine, m.totalLines-1)
	m.follow = false // navigating: don't let refresh yank to the bottom
	m.refresh(false)
	m.ensureLineVisible()
	m.syncFollow()
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

func (m *model) enterNormal() {
	m.mode = modeNormal
	m.ta.Blur()
	m.ta.Placeholder = "press i to type" // hint: NORMAL doesn't capture text
	m.cursorLine = 1 << 30               // clamp to last line in refresh
	m.refresh(false)
	m.ensureLineVisible()
}

func (m *model) enterInsert() tea.Cmd {
	m.mode = modeInsert
	m.ta.Placeholder = ""
	m.refresh(false)
	return m.ta.Focus()
}
