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

// editorState is the user-input cluster: the live line, cursor, the up-arrow
// history-recall state machine, the incomplete-UTF8 carry, and bracketed-paste
// buffering.
//
// Locking is split into two groups:
//   - input, cursor, history, historyIdx, historyDraft, historyInDraft are
//     guarded by App.mu: they are mutated by the input handlers and read by
//     render() (which runs under a.mu), so every access takes the lock.
//   - inputPending, pasting, pasteBuf are owned exclusively by the single
//     input-loop goroutine (processInput). They are NOT protected by a.mu and
//     are safe only because nothing outside that one goroutine touches them.
type editorState struct {
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

	// Incomplete UTF-8/control-sequence bytes carried between terminal reads.
	inputPending []byte

	// Bracketed paste state.
	pasting  bool
	pasteBuf []rune
}

// terminalState is the terminal/stdin-lifecycle cluster. Its fields do NOT
// share one lock — the locking is unchanged from when they lived on App:
//   - readMu: its own lock, gating the stdin Read so a paused subprocess owns
//     the TTY without our reader stealing keystrokes / DSR replies.
//   - pauseWakeR/W: a self-pipe assigned once in Run before its readers start,
//     then only written (Quit, Pause); effectively immutable afterward. Used to
//     interrupt the input loop's Poll when Pause is called from another
//     goroutine (SetReadDeadline is unreliable for terminals).
//   - oldTermState, paused: guarded by App.mu (set in Pause/Resume; paused is
//     read in render()).
type terminalState struct {
	readMu sync.RWMutex

	pauseWakeR *os.File
	pauseWakeW *os.File

	oldTermState *term.State
	paused       bool
}

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

	r *patchtui.Renderer

	// User input state (live line, cursor, history recall, paste, UTF-8 carry).
	ed editorState

	// Terminal/stdin lifecycle (own readMu lock + self-pipe + raw-mode state).
	term terminalState

	// Status bar info.
	status statusInfo

	// Busy/streaming.
	busy         bool
	streamCancel context.CancelFunc

	// Pending tool-approval prompt. Non-nil while a RequestApproval call is
	// blocked waiting for a y/N answer; the input goroutine resolves it via
	// resolveApproval. Guarded by mu. Buffered (cap 1) so the resolver never
	// blocks.
	pendingApproval chan bool

	// approvalMu serializes whole RequestApproval calls. Guard chains are
	// sequential per turn, so today there is never more than one outstanding
	// request — but two sessions could share an App in the future, and a
	// second concurrent request must block until the first resolves rather
	// than clobber pendingApproval.
	approvalMu sync.Mutex

	// Quit/exit state.
	lastCtrlC time.Time
	exitFlag  bool

	// Submit callback.
	submit SubmitFunc

	// onTab is fired when Tab is pressed while not busy. Nil = no-op.
	onTab func()

	// onInterject is fired when Enter is pressed while busy with plain (non-slash,
	// non-!) text. If nil, the input is preserved (historical no-op behavior).
	onInterject func(text string)

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

// SetTab registers the callback fired on Tab (ignored while busy).
func (a *App) SetTab(fn func()) { a.onTab = fn }

// SetInterject registers the callback fired when Enter is pressed while busy
// with plain text in the editor (mid-turn steering). The callback runs on the
// input goroutine and must not block; nil restores the historical
// swallow-input-while-busy behavior.
func (a *App) SetInterject(fn func(text string)) { a.onInterject = fn }

// SetMode updates the agent badge shown in the status bar. Goroutine-safe.
func (a *App) SetMode(name string) {
	a.mu.Lock()
	a.status.mode = name
	a.render()
	a.mu.Unlock()
}

// Quit asks the input loop to exit cleanly. Run will return after the
// current input batch finishes processing, allowing the caller's deferred
// teardown (DB close, hook OnSessionEnd, etc.) to execute. Safe from any
// goroutine; calling Quit before Run is a no-op until Run starts.
func (a *App) Quit() {
	a.mu.Lock()
	a.exitFlag = true
	a.mu.Unlock()
	// Deny any pending approval prompt so a turn goroutine blocked in
	// RequestApproval cannot wedge teardown.
	a.resolveApproval(false)
	if a.term.pauseWakeW != nil {
		_, _ = a.term.pauseWakeW.Write([]byte{0})
	}
}

// RequestApproval prints question as a dim [approve? y/N] prompt, blocks
// until the user answers on the input goroutine (y/Y approves; n/N, Esc,
// Enter, or ctrl+c denies), and returns the verdict. Designed to be called
// from the turn goroutine while busy. Concurrent calls are serialized by
// approvalMu: a second request blocks until the first resolves. If the app
// is exiting (Quit) the prompt resolves false so the caller cannot wedge.
func (a *App) RequestApproval(question string) bool {
	a.approvalMu.Lock()
	defer a.approvalMu.Unlock()

	// Build the full prompt block up front. Each line of a multi-line
	// question is echoed separately so it doesn't corrupt the terminal —
	// same convention as the steering echo in handleEnter — and each line
	// is fully wrapped in Dim…Reset so no escape bleeds across lines.
	qLines := patchtui.SplitLines(question)
	if len(qLines) == 0 {
		qLines = []string{question}
	}
	lines := make([]string, 0, len(qLines))
	for i, ql := range qLines {
		if i == 0 {
			lines = append(lines, patchtui.Dim+"[approve? y/N] "+ql+patchtui.Reset)
		} else {
			lines = append(lines, patchtui.Dim+"  "+ql+patchtui.Reset)
		}
	}

	// Register the pending prompt and print the block inside ONE mu critical
	// section (the print is Print's body inlined — calling Print here would
	// self-deadlock on mu): a single commit keeps concurrent Print calls from
	// interleaving inside the block, and registering while the block lands on
	// screen means no keystroke can be routed to the resolver before the
	// prompt is visible. mu is released before blocking on the channel.
	a.mu.Lock()
	if a.exitFlag {
		a.mu.Unlock()
		return false
	}
	ch := make(chan bool, 1)
	a.pendingApproval = ch
	w, _ := patchtui.Size()
	wrapped := wrapCommittedLines(lines, w)
	if a.term.paused {
		a.r.Print(wrapped)
	} else {
		a.r.PrintAndRender(wrapped, a.liveFrameLocked())
	}
	a.mu.Unlock()

	verdict := <-ch
	if verdict {
		a.PrintLine(patchtui.Dim + "[approved]" + patchtui.Reset)
	} else {
		a.PrintLine(patchtui.Dim + "[denied]" + patchtui.Reset)
	}
	return verdict
}

// resolveApproval delivers verdict to a pending RequestApproval, if any,
// and clears the pending state. Safe to call from any goroutine and when
// nothing is pending (no-op). The channel is buffered, so the send never
// blocks; clearing under mu makes a double resolution impossible.
func (a *App) resolveApproval(verdict bool) {
	a.mu.Lock()
	ch := a.pendingApproval
	a.pendingApproval = nil
	a.mu.Unlock()
	if ch != nil {
		ch <- verdict
	}
}

// liveFrameLocked builds the current live frame. Caller must hold a.mu.
func (a *App) liveFrameLocked() []string {
	w, _ := patchtui.Size()
	return buildFrame(w, frameState{
		input:  a.ed.input,
		cursor: a.ed.cursor,
		busy:   a.busy,
		status: a.status,
	})
}

// render rebuilds the frame and asks the renderer to paint it. Caller
// must hold a.mu. Skipped while paused (during shell exec).
func (a *App) render() {
	if a.term.paused {
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
	if a.term.paused {
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
