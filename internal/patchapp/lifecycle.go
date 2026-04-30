package patchapp

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

// Pause stops the render loop, halts the input reader, clears the live
// frame, and restores cooked terminal mode so a subprocess (shell command,
// editor, hook) can use the real TTY. Pair every Pause with a [App.Resume];
// nesting is not supported. Safe from any goroutine. The error return
// satisfies hooks.TTYReleaser; the current implementation never returns a
// non-nil error.
func (a *App) Pause() error {
	// Wake the input loop out of its Poll so it can release the read lock.
	// Without this, a Pause from a non-input goroutine would block until
	// the user happened to press a key.
	if a.pauseWakeW != nil {
		_, _ = a.pauseWakeW.Write([]byte{0})
	}
	a.readMu.Lock()

	a.mu.Lock()
	oldState := a.oldTermState
	a.paused = true
	a.r.Erase() // move to frame row 0 before erasing, not just current cursor row
	a.mu.Unlock()

	fmt.Print("\x1b[?25h" + pasteOff)
	term.Restore(int(os.Stdin.Fd()), oldState) //nolint:errcheck
	return nil
}

// Resume re-enters raw terminal mode, re-enables bracketed paste, and
// repaints the live frame. Call after [App.Pause] returns from the
// subprocess. Safe from any goroutine.
func (a *App) Resume() error {
	newState, _ := term.MakeRaw(int(os.Stdin.Fd()))
	fmt.Print(pasteOn)

	a.mu.Lock()
	a.oldTermState = newState
	a.paused = false
	a.r.Reset()
	a.render()
	a.mu.Unlock()

	a.readMu.Unlock()
	return nil
}

// WithReleasedTerminal pauses the render loop, runs fn with full TTY
// access, then resumes. Convenience wrapper around [App.Pause] /
// [App.Resume] for callers with a synchronous body to run.
func (a *App) WithReleasedTerminal(fn func()) {
	_ = a.Pause()
	defer a.Resume() //nolint:errcheck
	fn()
}
