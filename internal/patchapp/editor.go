package patchapp

import (
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/patchtui"
)

// processInput consumes a chunk of bytes from stdin, dispatching parsed
// keys to handlers. Returns true if the app should exit (double ctrl+c).
func (a *App) processInput(data []byte) (exit bool) {
	for i := 0; i < len(data); {
		// Inside a paste — accumulate raw bytes until paste end.
		if a.pasting {
			if i+len(pasteEnd) <= len(data) && string(data[i:i+len(pasteEnd)]) == pasteEnd {
				a.pasting = false
				a.mu.Lock()
				if !a.busy {
					for _, r := range a.pasteBuf {
						a.insertChar(r)
					}
					a.render()
				}
				a.mu.Unlock()
				a.pasteBuf = a.pasteBuf[:0]
				i += len(pasteEnd)
				continue
			}
			r := rune(data[i])
			if r == '\r' {
				r = '\n'
			}
			if r == '\n' || r >= 32 {
				a.pasteBuf = append(a.pasteBuf, r)
			}
			i++
			continue
		}

		k, used := parseInput(data[i:])
		if used == 0 {
			used = 1
		}
		i += used

		switch k.kind {
		case keyPasteStart:
			a.pasting = true
			a.pasteBuf = a.pasteBuf[:0]
		case keyCtrlC:
			if a.handleCtrlC() {
				return true
			}
		case keyEnter:
			a.handleEnter()
		case keyAltEnter:
			a.mu.Lock()
			if !a.busy {
				a.insertChar('\n')
				a.render()
			}
			a.mu.Unlock()
		case keyEscape:
			a.mu.Lock()
			if !a.busy {
				a.input = a.input[:0]
				a.cursor = 0
				a.render()
			}
			a.mu.Unlock()
		case keyBackspace:
			a.mu.Lock()
			if !a.busy && a.cursor > 0 {
				a.input = append(a.input[:a.cursor-1], a.input[a.cursor:]...)
				a.cursor--
				a.render()
			}
			a.mu.Unlock()
		case keyLeft:
			a.mu.Lock()
			if !a.busy && a.cursor > 0 {
				a.cursor--
				a.render()
			}
			a.mu.Unlock()
		case keyRight:
			a.mu.Lock()
			if !a.busy && a.cursor < len(a.input) {
				a.cursor++
				a.render()
			}
			a.mu.Unlock()
		case keyUp:
			a.mu.Lock()
			if !a.busy {
				w, _ := patchtui.Size()
				row, col := inputCursorPos(a.input, a.cursor, w)
				if row > 0 {
					a.cursor = inputOffsetForRowCol(a.input, w, row-1, col)
					a.render()
				}
			}
			a.mu.Unlock()
		case keyDown:
			a.mu.Lock()
			if !a.busy {
				w, _ := patchtui.Size()
				row, col := inputCursorPos(a.input, a.cursor, w)
				newCursor := inputOffsetForRowCol(a.input, w, row+1, col)
				if newCursor != a.cursor {
					a.cursor = newCursor
					a.render()
				}
			}
			a.mu.Unlock()
		case keyHome:
			a.mu.Lock()
			if !a.busy {
				a.cursor = 0
				a.render()
			}
			a.mu.Unlock()
		case keyEnd:
			a.mu.Lock()
			if !a.busy {
				a.cursor = len(a.input)
				a.render()
			}
			a.mu.Unlock()
		case keyChar:
			a.mu.Lock()
			if !a.busy {
				a.insertChar(k.r)
				a.render()
			}
			a.mu.Unlock()
		}
	}
	return a.exitFlag
}

// insertChar inserts r at the cursor. Caller must hold a.mu.
func (a *App) insertChar(r rune) {
	a.input = append(a.input[:a.cursor], append([]rune{r}, a.input[a.cursor:]...)...)
	a.cursor++
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

// handleEnter dispatches the input to the SubmitFunc (or shell exec for !).
func (a *App) handleEnter() {
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		return
	}
	line := strings.TrimRight(string(a.input), " \t\n")
	a.input = a.input[:0]
	a.cursor = 0
	a.render()
	a.mu.Unlock()

	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}

	// Echo the user message to scrollback as a styled chat bubble.
	// Slash commands echo too so the output has visible context.
	// One blank line above and below for breathing room.
	w, _ := patchtui.Size()
	lines := []string{""}
	lines = append(lines, renderUserMessage(line, w)...)
	lines = append(lines, "")
	a.r.Print(lines)

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
	userBg := patchtui.BgRGB(rUserBg, gUserBg, bUserBg)
	userFg := patchtui.FgRGB(rUserFg, gUserFg, bUserFg)
	yellow := patchtui.FgRGB(rPrimary, gPrimary, bPrimary)

	lines := strings.Split(text, "\n")
	var out []string
	for i, l := range lines {
		var prefix string
		if i == 0 {
			prefix = userBg + yellow + patchtui.Bold + "> " + patchtui.Reset + userBg + userFg
		} else {
			prefix = userBg + userFg + "  "
		}
		pad := width - 2 - patchtui.VisibleLen(l)
		if pad < 0 {
			pad = 0
		}
		out = append(out, prefix+l+strings.Repeat(" ", pad)+patchtui.Reset)
	}
	return out
}
