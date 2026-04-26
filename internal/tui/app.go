package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/weatherjean/shell3/internal/patchtui"
	"golang.org/x/term"
)

// SubmitFunc is the callback invoked when the user presses Enter on a
// non-empty input. The text is the full input including any embedded
// newlines from alt+enter. The App is busy-locked until the callback's
// goroutine completes (for non-slash, non-! inputs); the SubmitFunc is
// responsible for either handling synchronously and returning, or
// launching a goroutine and calling App.SetBusy / App.SetStreamPreview /
// App.Print to feed events back.
type SubmitFunc func(input string)

// App is the top-level TUI controller. It owns the render loop, input
// parser, and terminal mode. Methods that mutate render state are
// goroutine-safe.
type App struct {
	mu sync.Mutex

	r *patchtui.Renderer

	// User input state.
	input  []rune
	cursor int

	// Live streaming preview shown above the input box during a turn.
	streamLines []string

	// Status bar info.
	status statusInfo

	// Busy/streaming.
	busy         bool
	streamCancel context.CancelFunc

	// Quit/exit state.
	lastCtrlC time.Time
	exitFlag  bool

	// Bracketed paste state.
	pasting  bool
	pasteBuf []rune

	// Terminal lifecycle.
	oldTermState *term.State
	paused       bool // set during withReleasedTerminal

	// Submit callback.
	submit SubmitFunc
}

// New returns a new App with the given mode label and initial status text.
func New(mode, statusMsg string) *App {
	return &App{
		r: patchtui.New(),
		status: statusInfo{
			mode:      mode,
			statusMsg: statusMsg,
		},
	}
}

// SetSubmit registers the callback fired on Enter.
func (a *App) SetSubmit(fn SubmitFunc) { a.submit = fn }

// Run takes over the terminal, prints the welcome message, and enters the
// input loop. Returns when the user double-presses ctrl+c or ctx is done.
func (a *App) Run(ctx context.Context) error {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("tui: enter raw mode: %w", err)
	}
	a.oldTermState = oldState
	defer term.Restore(int(os.Stdin.Fd()), oldState) //nolint:errcheck
	defer fmt.Print(pasteOff + "\x1b[?25h\n")

	fmt.Print(pasteOn)

	w, _ := patchtui.Size()
	a.r.Print(renderWelcome(w))
	a.render()

	// Resize handling.
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	go a.tickerLoop(ctx)
	go a.winchLoop(ctx, winch)

	buf := make([]byte, 4096)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			return err
		}
		if a.processInput(buf[:n]) {
			return nil
		}
	}
}

// tickerLoop animates the spinner during streaming and clears the
// "press ctrl+c again" hint after 500ms.
func (a *App) tickerLoop(ctx context.Context) {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.mu.Lock()
		needsRender := false
		if !a.lastCtrlC.IsZero() && time.Since(a.lastCtrlC) > 500*time.Millisecond {
			a.lastCtrlC = time.Time{}
			a.status.ctrlCHint = false
			needsRender = true
		}
		if a.busy {
			needsRender = true
		}
		if needsRender {
			a.render()
		}
		a.mu.Unlock()
	}
}

// winchLoop redraws the frame on terminal resize.
func (a *App) winchLoop(ctx context.Context, winch <-chan os.Signal) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-winch:
		}
		a.mu.Lock()
		a.r.Reset()
		a.render()
		a.mu.Unlock()
	}
}

// render rebuilds the frame and asks the renderer to paint it. Caller
// must hold a.mu. Skipped while paused (during shell exec).
func (a *App) render() {
	if a.paused {
		return
	}
	w, h := patchtui.Size()
	a.r.Render(buildFrame(w, h, frameState{
		streamLines: a.streamLines,
		input:       a.input,
		cursor:      a.cursor,
		busy:        a.busy,
		status:      a.status,
	}))
}

// ── public state mutators (goroutine-safe) ────────────────────────────────────

// Print commits lines to scrollback above the live frame. Goroutine-safe.
func (a *App) Print(lines []string) { a.r.Print(lines) }

// PrintLine is shorthand for a single committed line.
func (a *App) PrintLine(line string) { a.r.Print([]string{line}) }

// Refresh redraws the live frame at its current state. Use after a series
// of Print calls when no other state change will trigger a render.
func (a *App) Refresh() {
	a.mu.Lock()
	a.render()
	a.mu.Unlock()
}

// SetStreamPreview replaces the live streaming content shown above the
// input box. Pass nil to clear it.
func (a *App) SetStreamPreview(lines []string) {
	a.mu.Lock()
	a.streamLines = lines
	a.render()
	a.mu.Unlock()
}

// SetStatus updates the status bar message line (idle state only).
func (a *App) SetStatus(msg string) {
	a.mu.Lock()
	a.status.statusMsg = msg
	a.render()
	a.mu.Unlock()
}

// SetTokens updates the token counter shown in the status bar.
func (a *App) SetTokens(n int) {
	a.mu.Lock()
	a.status.tokens = n
	a.render()
	a.mu.Unlock()
}

// SetBusy marks the app as streaming/thinking. Pass cancel to wire ctrl+c
// to interrupt the current turn. Pass nil for cancel when clearing busy.
func (a *App) SetBusy(busy bool, cancel context.CancelFunc) {
	a.mu.Lock()
	a.busy = busy
	a.status.busy = busy
	a.streamCancel = cancel
	if !busy {
		a.streamLines = nil
	}
	a.render()
	a.mu.Unlock()
}

// WithReleasedTerminal pauses rendering, restores cooked mode, runs fn
// with full TTY access, then re-enters raw mode and re-renders. Safe
// from any goroutine.
func (a *App) WithReleasedTerminal(fn func()) {
	a.mu.Lock()
	oldState := a.oldTermState
	a.paused = true
	a.mu.Unlock()

	fmt.Print("\r\x1b[0J\x1b[?25h" + pasteOff)
	term.Restore(int(os.Stdin.Fd()), oldState) //nolint:errcheck

	fn()

	newState, _ := term.MakeRaw(int(os.Stdin.Fd()))
	fmt.Print(pasteOn)

	a.mu.Lock()
	a.oldTermState = newState
	a.paused = false
	a.r.Reset()
	a.render()
	a.mu.Unlock()
}

// ── input processing ──────────────────────────────────────────────────────────

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

// handleEnter: dispatches the input to the SubmitFunc (or shell exec for !).
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
	userBg := bgRGB(rUserBg, gUserBg, bUserBg)
	userFg := fgRGB(rUserFg, gUserFg, bUserFg)
	yellow := fgRGB(rPrimary, gPrimary, bPrimary)

	lines := strings.Split(text, "\n")
	var out []string
	for i, l := range lines {
		var prefix string
		if i == 0 {
			prefix = userBg + yellow + ansiBold + "> " + ansiReset + userBg + userFg
		} else {
			prefix = userBg + userFg + "  "
		}
		pad := width - 2 - visibleLen(l)
		if pad < 0 {
			pad = 0
		}
		out = append(out, prefix+l+strings.Repeat(" ", pad)+ansiReset)
	}
	return out
}
