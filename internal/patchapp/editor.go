package patchapp

import (
	"bytes"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rivo/uniseg"
	"github.com/weatherjean/shell3/internal/patchtui"
)

// processInput consumes a chunk of bytes from stdin, dispatching parsed
// keys to handlers. Returns true if the app should exit (double ctrl+c).
func (a *App) processInput(data []byte) (exit bool) {
	if len(a.ed.inputPending) > 0 {
		merged := make([]byte, 0, len(a.ed.inputPending)+len(data))
		merged = append(merged, a.ed.inputPending...)
		merged = append(merged, data...)
		data = merged
		a.ed.inputPending = a.ed.inputPending[:0]
	}

	for i := 0; i < len(data); {
		// Inside a paste, accumulate decoded runes until paste end. The
		// terminal sends the paste body as UTF-8 bytes; treating each byte as
		// a rune corrupts non-ASCII punctuation when the input is echoed or
		// submitted.
		if a.ed.pasting {
			if i+len(pasteEnd) <= len(data) && string(data[i:i+len(pasteEnd)]) == pasteEnd {
				a.ed.pasting = false
				a.mu.Lock()
				for _, r := range a.ed.pasteBuf {
					a.insertChar(r)
				}
				a.syncDraftLocked()
				a.render()
				a.mu.Unlock()
				a.ed.pasteBuf = a.ed.pasteBuf[:0]
				i += len(pasteEnd)
				continue
			}
			if bytes.HasPrefix([]byte(pasteEnd), data[i:]) {
				a.ed.inputPending = append(a.ed.inputPending, data[i:]...)
				break
			}

			if !utf8.FullRune(data[i:]) {
				a.ed.inputPending = append(a.ed.inputPending, data[i:]...)
				break
			}
			r, size := utf8.DecodeRune(data[i:])
			if r == utf8.RuneError && size == 1 {
				i++
				continue
			}
			if r == '\r' {
				r = '\n'
			}
			if r == '\n' || r >= 32 {
				a.ed.pasteBuf = append(a.ed.pasteBuf, r)
			}
			i += size
			continue
		}

		k, used := parseInput(data[i:])
		if used == 0 {
			a.ed.inputPending = append(a.ed.inputPending, data[i:]...)
			break
		}
		i += used

		// While an approval prompt is pending, every key is routed to the
		// y/N resolver and consumed — no editing, no ctrl+c quit priming.
		a.mu.Lock()
		approvalPending := a.pendingApproval != nil
		a.mu.Unlock()
		if approvalPending {
			a.handleApprovalKey(k)
			continue
		}

		switch k.kind {
		case keyPasteStart:
			a.ed.pasting = true
			a.ed.pasteBuf = a.ed.pasteBuf[:0]
		case keyCtrlC:
			if a.handleCtrlC() {
				return true
			}
		case keyEnter:
			a.handleEnter()
		case keyTab:
			a.handleTab()
		case keyAltEnter:
			a.mu.Lock()
			a.insertChar('\n')
			a.syncDraftLocked()
			a.render()
			a.mu.Unlock()
		case keyEscape:
			a.mu.Lock()
			if a.busy && a.streamCancel != nil {
				a.streamCancel()
				a.lastCtrlC = time.Time{}
				a.status.ctrlCHint = false
			} else if !a.busy {
				if a.ed.historyIdx > 0 || a.ed.historyInDraft {
					// Restore live input from draft; exit history navigation.
					a.ed.input = append([]rune(nil), a.ed.historyDraft...)
					a.ed.cursor = len(a.ed.input)
					a.ed.historyIdx = 0
					a.ed.historyInDraft = false
				} else {
					// Clear input but leave draft intact so up-arrow can recover it.
					a.ed.input = a.ed.input[:0]
					a.ed.cursor = 0
				}
				a.render()
			}
			a.mu.Unlock()
		case keyBackspace:
			a.mu.Lock()
			if a.ed.cursor > 0 {
				a.ed.input = append(a.ed.input[:a.ed.cursor-1], a.ed.input[a.ed.cursor:]...)
				a.ed.cursor--
				a.syncDraftLocked()
				a.render()
			}
			a.mu.Unlock()
		case keyLeft:
			a.mu.Lock()
			if a.ed.cursor > 0 {
				a.ed.cursor--
				a.render()
			}
			a.mu.Unlock()
		case keyRight:
			a.mu.Lock()
			if a.ed.cursor < len(a.ed.input) {
				a.ed.cursor++
				a.render()
			}
			a.mu.Unlock()
		case keyUp:
			a.mu.Lock()
			if !a.busy {
				w, _ := patchtui.Size()
				row, col := inputCursorPos(a.ed.input, a.ed.cursor, w)
				if row > 0 {
					a.ed.cursor = inputOffsetForRowCol(a.ed.input, w, row-1, col)
					a.render()
				} else if a.ed.cursor == 0 || a.ed.historyIdx > 0 || a.ed.historyInDraft {
					a.historyStepBackLocked()
					a.render()
				}
			}
			a.mu.Unlock()
		case keyDown:
			a.mu.Lock()
			if !a.busy {
				w, _ := patchtui.Size()
				row, col := inputCursorPos(a.ed.input, a.ed.cursor, w)
				newCursor := inputOffsetForRowCol(a.ed.input, w, row+1, col)
				if newCursor != a.ed.cursor {
					a.ed.cursor = newCursor
					a.render()
				}
			}
			a.mu.Unlock()
		case keyHome:
			a.mu.Lock()
			a.ed.cursor = 0
			a.render()
			a.mu.Unlock()
		case keyEnd:
			a.mu.Lock()
			a.ed.cursor = len(a.ed.input)
			a.render()
			a.mu.Unlock()
		case keyChar:
			a.mu.Lock()
			a.insertChar(k.r)
			a.syncDraftLocked()
			a.render()
			a.mu.Unlock()
		}
	}
	return a.exitFlag
}

// handleApprovalKey consumes one key while an approval prompt is pending.
// y/Y approves; n/N, Esc, Enter (default No), and ctrl+c deny — ctrl+c here
// answers the prompt instead of cancelling the turn or priming the
// double-tap exit. Every other key is ignored.
func (a *App) handleApprovalKey(k parsedKey) {
	switch k.kind {
	case keyChar:
		switch k.r {
		case 'y', 'Y':
			a.resolveApproval(true)
		case 'n', 'N':
			a.resolveApproval(false)
		}
	case keyEnter, keyEscape, keyCtrlC:
		a.resolveApproval(false)
	}
}

// insertChar inserts r at the cursor. Caller must hold a.mu.
func (a *App) insertChar(r rune) {
	a.ed.input = append(a.ed.input[:a.ed.cursor], append([]rune{r}, a.ed.input[a.ed.cursor:]...)...)
	a.ed.cursor++
}

// syncDraftLocked copies current input into historyDraft. Only called when
// the user is in live mode (not navigating history). Caller must hold a.mu.
func (a *App) syncDraftLocked() {
	if a.ed.historyIdx == 0 && !a.ed.historyInDraft {
		a.ed.historyDraft = append(a.ed.historyDraft[:0], a.ed.input...)
	}
}

// historyStepBackLocked advances one step back through draft→history.
// Caller must hold a.mu.
func (a *App) historyStepBackLocked() {
	if a.ed.historyIdx == 0 && !a.ed.historyInDraft {
		// Check if draft differs from current input (e.g. after Escape cleared it).
		if len(a.ed.historyDraft) > 0 && !slices.Equal(a.ed.historyDraft, a.ed.input) {
			a.ed.historyInDraft = true
			a.ed.input = append([]rune(nil), a.ed.historyDraft...)
			a.ed.cursor = len(a.ed.input)
			return
		}
		// Draft same as input: jump straight into history list.
		if len(a.ed.history) > 0 {
			a.ed.historyIdx = 1
			a.ed.input = []rune(a.ed.history[len(a.ed.history)-1])
			a.ed.cursor = len(a.ed.input)
		}
		return
	}
	if a.ed.historyInDraft {
		// Was showing draft; step into history.
		a.ed.historyInDraft = false
		if len(a.ed.history) > 0 {
			a.ed.historyIdx = 1
			a.ed.input = []rune(a.ed.history[len(a.ed.history)-1])
			a.ed.cursor = len(a.ed.input)
		}
		return
	}
	// Already in history list; go further back.
	if a.ed.historyIdx < len(a.ed.history) {
		a.ed.historyIdx++
		a.ed.input = []rune(a.ed.history[len(a.ed.history)-a.ed.historyIdx])
		a.ed.cursor = len(a.ed.input)
	}
}

// handleCtrlC: cancel running turn, or if idle, prime a double-tap to exit.
func (a *App) handleCtrlC() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.busy && a.streamCancel != nil {
		a.streamCancel()
		a.lastCtrlC = time.Time{}
		a.status.ctrlCHint = false
		return false
	}
	now := time.Now()
	if !a.lastCtrlC.IsZero() && now.Sub(a.lastCtrlC) < 500*time.Millisecond {
		a.exitFlag = true
		return true
	}
	a.lastCtrlC = now
	a.status.ctrlCHint = true
	a.render()
	return false
}

// handleTab fires the Tab callback (agent cycling) when idle, and is a no-op
// while busy. Tab remains fully gated: the callback (tui.applyAgent) mutates
// the shared *chat.Config that the event-drain goroutine reads during a turn —
// the same concurrency contract as slash commands. See handleEnter's busy-gate
// note and busygate_test.go, which lock this behaviour in place.
func (a *App) handleTab() {
	a.mu.Lock()
	busy := a.busy
	fn := a.onTab
	a.mu.Unlock()
	if !busy && fn != nil {
		fn()
	}
}

// handleEnter dispatches the input to the SubmitFunc (or shell exec for !).
//
// BUSY-GATE (load-bearing): while a turn is in flight (a.busy), SubmitFunc,
// slash commands, ! shell execution, and Tab are all gated — the shared
// *chat.Config and *lastUsage are written by the drain goroutine throughout
// the turn with NO mutex, and they must not be read or mutated concurrently
// (see internal/tui/interactive.go's CONCURRENCY INVARIANT and busygate_test.go).
// Removing or weakening those gates reintroduces a data race on cfg/lastUsage.
//
// While busy, plain-text Enter is routed to onInterject instead of SubmitFunc.
// Session.Interject is concurrency-safe (mutex-guarded inbox), so plain-text
// Enter while busy does not break the gate invariant. Slash commands and !
// remain fully gated because their handlers mutate cfg/lastUsage.
func (a *App) handleEnter() {
	a.mu.Lock()
	busy := a.busy
	if busy {
		line := strings.TrimRight(string(a.ed.input), " \t\n")
		trimmed := strings.TrimSpace(line)
		fn := a.onInterject
		a.mu.Unlock()

		// Empty input while busy: no-op.
		if trimmed == "" {
			return
		}

		// Slash or ! command while busy: print a dim notice and keep the input
		// intact. These handlers mutate cfg/lastUsage and must stay gated.
		if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "!") {
			a.PrintLine(patchtui.Dim + "[busy — commands run after the turn finishes]" + patchtui.Reset)
			return
		}

		// Plain text while busy: route to onInterject if registered.
		if fn != nil {
			a.mu.Lock()
			a.ed.input = a.ed.input[:0]
			a.ed.cursor = 0
			a.ed.historyDraft = a.ed.historyDraft[:0]
			// History nav is gated while busy, so historyIdx and historyInDraft
			// are always already zero here — reset kept for symmetry with the
			// submit path.
			a.ed.historyIdx = 0
			a.ed.historyInDraft = false
			a.render()
			a.mu.Unlock()
			fn(trimmed)
			// Echo each line of the steering text so multi-line interjections
			// (typed via alt+enter) don't corrupt the terminal. Each line is
			// fully wrapped in Dim…Reset so no escape bleeds across lines.
			steerLines := patchtui.SplitLines(trimmed)
			if len(steerLines) == 0 {
				steerLines = []string{trimmed}
			}
			for i, sl := range steerLines {
				switch {
				case i == 0 && len(steerLines) == 1:
					a.PrintLine(patchtui.Dim + "[steering: " + sl + "]" + patchtui.Reset)
				case i == 0:
					a.PrintLine(patchtui.Dim + "[steering: " + sl + patchtui.Reset)
				case i == len(steerLines)-1:
					a.PrintLine(patchtui.Dim + "  " + sl + "]" + patchtui.Reset)
				default:
					a.PrintLine(patchtui.Dim + "  " + sl + patchtui.Reset)
				}
			}
		}
		// nil onInterject: preserve input (no-op).
		return
	}

	line := strings.TrimRight(string(a.ed.input), " \t\n")
	a.ed.input = a.ed.input[:0]
	a.ed.cursor = 0
	a.render()
	a.mu.Unlock()

	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}

	a.mu.Lock()
	a.ed.history = append(a.ed.history, line)
	a.ed.historyIdx = 0
	a.ed.historyInDraft = false
	a.ed.historyDraft = a.ed.historyDraft[:0]
	a.mu.Unlock()

	// Echo the user message to scrollback as a styled chat bubble.
	// Slash commands echo too so the output has visible context.
	// One blank line above and below for breathing room.
	w, _ := patchtui.Size()
	bubble := renderUserMessage(line, w)
	lines := make([]string, 0, len(bubble)+2)
	lines = append(lines, "")
	lines = append(lines, bubble...)
	lines = append(lines, "")
	a.Print(lines)

	if strings.HasPrefix(trimmed, "!") {
		a.runShell(strings.TrimSpace(trimmed[1:]))
		return
	}

	// Route registered "/cmd args" through the slash dispatcher; falls
	// through to the SubmitFunc if no slash registry is set up at all.
	if strings.HasPrefix(trimmed, "/") && a.slash != nil {
		a.dispatchSlash(line)
		return
	}

	if a.submit != nil {
		a.submit(line)
	}
}

// runShell executes a !cmd by releasing the terminal and inheriting stdio.
func (a *App) runShell(cmd string) {
	if cmd == "" {
		return
	}
	a.WithReleasedTerminal(func() {
		c := exec.Command("bash", "-c", cmd)
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		_ = c.Run()
	})
}

// renderUserMessage returns the chat-bubble lines for the user message
// committed to scrollback on submit.
func renderUserMessage(text string, width int) []string {
	contentW := width - 2
	if contentW < 1 {
		contentW = 1
	}

	rawLines := strings.Split(text, "\n")
	var out []string
	for i, raw := range rawLines {
		rs := []rune(raw)
		if len(rs) == 0 {
			out = append(out, renderUserBubbleLine(i == 0, "", 0, width))
			continue
		}
		chunks := splitRuneChunksByWidth(rs, contentW)
		for j, ch := range chunks {
			chunk := rs[ch.start:ch.end]
			s := string(chunk)
			out = append(out, renderUserBubbleLine(i == 0 && j == 0, s, uniseg.StringWidth(s), width))
		}
	}
	return out
}
