package patchapp

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rivo/uniseg"
	"github.com/weatherjean/shell3/internal/patchtui"
)

// processInput consumes a chunk of bytes from stdin, dispatching parsed
// keys to handlers. Returns true if the app should exit (double ctrl+c).
func (a *App) processInput(data []byte) (exit bool) {
	if len(a.inputPending) > 0 {
		merged := make([]byte, 0, len(a.inputPending)+len(data))
		merged = append(merged, a.inputPending...)
		merged = append(merged, data...)
		data = merged
		a.inputPending = a.inputPending[:0]
	}

	for i := 0; i < len(data); {
		// Inside a paste, accumulate decoded runes until paste end. The
		// terminal sends the paste body as UTF-8 bytes; treating each byte as
		// a rune corrupts non-ASCII punctuation when the input is echoed or
		// submitted.
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
			if bytes.HasPrefix([]byte(pasteEnd), data[i:]) {
				a.inputPending = append(a.inputPending, data[i:]...)
				break
			}

			if !utf8.FullRune(data[i:]) {
				a.inputPending = append(a.inputPending, data[i:]...)
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
				a.pasteBuf = append(a.pasteBuf, r)
			}
			i += size
			continue
		}

		k, used := parseInput(data[i:])
		if used == 0 {
			a.inputPending = append(a.inputPending, data[i:]...)
			break
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
