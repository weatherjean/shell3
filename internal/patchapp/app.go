package patchapp

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/patchtui"
	"golang.org/x/term"
)

// SubmitFunc is the callback invoked when the user presses Enter on a
// non-empty input. The text is the full input including any embedded
// newlines from alt+enter. The App is busy-locked until the callback's
// goroutine completes (for non-slash, non-! inputs); the SubmitFunc is
// responsible for either handling synchronously and returning, or
// launching a goroutine and calling [App.SetBusy] / [App.Print] to feed
// events back.
type SubmitFunc func(input string)

// App is the top-level TUI controller. It owns the render loop, input
// parser, and terminal mode. Methods that mutate render state are
// goroutine-safe.
//
// File layout for the App methods:
//   - app.go        — type, constructor, state mutators, render helper
//   - loop.go       — Run, ticker, resize loops
//   - lifecycle.go  — Pause, Resume, WithReleasedTerminal
//   - editor.go     — input processing, key handlers, !shell passthrough
type App struct {
	mu sync.Mutex

	// readMu gates the stdin Read in the input loop. Held read-locked only
	// while a Read is in progress; Pause acquires it write-locked to keep
	// the reader out while a subprocess (nvim, !cmd, hook) owns the TTY.
	// Without this gate, our Read steals keystrokes and DSR replies meant
	// for the subprocess.
	readMu sync.RWMutex

	// pauseWake is a self-pipe used to interrupt the input loop's Poll when
	// Pause is called from another goroutine. os.Stdin.SetReadDeadline is
	// unreliable for terminals, so we multiplex stdin with this pipe via
	// unix.Poll and wake by writing a byte. nil before Run starts.
	pauseWakeR *os.File
	pauseWakeW *os.File

	r *patchtui.Renderer

	// User input state.
	input  []rune
	cursor int

	// Message history for up-arrow recall. history[0] is oldest.
	// historyDraft always mirrors live input (updated on every keystroke);
	// Escape clears input but leaves historyDraft intact so it can be
	// recovered. historyInDraft is true when the user has pressed Up and is
	// viewing the saved draft (one step before entering the history list).
	// historyIdx > 0 means the user is viewing a history entry (1 = most
	// recent); historyIdx is 0 in both live and in-draft modes.
	history        []string
	historyIdx     int
	historyDraft   []rune
	historyInDraft bool

	// Status bar info.
	status statusInfo

	// Busy/streaming.
	busy         bool
	streamCancel context.CancelFunc

	// Quit/exit state.
	lastCtrlC time.Time
	exitFlag  bool

	// Incomplete UTF-8/control-sequence bytes carried between terminal reads.
	inputPending []byte

	// Bracketed paste state.
	pasting  bool
	pasteBuf []rune

	// Terminal lifecycle.
	oldTermState *term.State
	paused       bool // set during Pause/Resume

	// Submit callback.
	submit SubmitFunc

	// Slash command registry. Keyed by lowercased name and each alias;
	// multiple keys may point to the same SlashCommand value.
	slash map[string]*SlashCommand

	// Welcome card data printed once on session start.
	welcome WelcomeInfo
}

// New returns a new App with the given mode label, initial status text, and
// welcome info rendered once on session start.
func New(mode, statusMsg string, welcome WelcomeInfo) *App {
	return &App{
		r: patchtui.New(),
		status: statusInfo{
			mode:      mode,
			statusMsg: statusMsg,
		},
		welcome: welcome,
	}
}

// SetSubmit registers the callback fired on Enter.
func (a *App) SetSubmit(fn SubmitFunc) { a.submit = fn }

// Quit asks the input loop to exit cleanly. Run will return after the
// current input batch finishes processing, allowing the caller's deferred
// teardown (DB close, hook OnSessionEnd, etc.) to execute. Safe from any
// goroutine; calling Quit before Run is a no-op until Run starts.
func (a *App) Quit() {
	a.mu.Lock()
	a.exitFlag = true
	a.mu.Unlock()
	if a.pauseWakeW != nil {
		_, _ = a.pauseWakeW.Write([]byte{0})
	}
}

// liveFrameLocked builds the current live frame. Caller must hold a.mu.
func (a *App) liveFrameLocked() []string {
	w, _ := patchtui.Size()
	return buildFrame(w, frameState{
		input:  a.input,
		cursor: a.cursor,
		busy:   a.busy,
		status: a.status,
	})
}

// render rebuilds the frame and asks the renderer to paint it. Caller
// must hold a.mu. Skipped while paused (during shell exec).
func (a *App) render() {
	if a.paused {
		return
	}
	a.r.Render(a.liveFrameLocked())
}

// ── public state mutators (goroutine-safe) ────────────────────────────────────

// Print commits lines to scrollback above the live frame. Goroutine-safe.
func (a *App) Print(lines []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w, _ := patchtui.Size()
	wrapped := wrapCommittedLines(lines, w)
	if a.paused {
		a.r.Print(wrapped)
		return
	}
	a.r.PrintAndRender(wrapped, a.liveFrameLocked())
}

// PrintLine is shorthand for a single committed line.
func (a *App) PrintLine(line string) { a.Print([]string{line}) }

// Refresh redraws the live frame at its current state. Use after a series
// of [App.Print] calls when no other state change will trigger a render.
func (a *App) Refresh() {
	a.mu.Lock()
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

// SetContextWindow sets the model's context window size used to compute
// the token percentage shown in the status bar. Pass 0 to hide the percentage.
func (a *App) SetContextWindow(n int) {
	a.mu.Lock()
	a.status.contextWindow = n
	a.render()
	a.mu.Unlock()
}

// SetTokens updates the token counter shown in the status bar.
func (a *App) SetTokens(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.status.tokens == n {
		return
	}
	a.status.tokens = n
	a.render()
}

// SetBusy marks the app as streaming/thinking. Pass cancel to wire ctrl+c
// to interrupt the current turn. Pass nil for cancel when clearing busy.
func (a *App) SetBusy(busy bool, cancel context.CancelFunc) {
	a.mu.Lock()
	a.busy = busy
	a.streamCancel = cancel
	a.render()
	a.mu.Unlock()
}
