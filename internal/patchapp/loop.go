package patchapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/weatherjean/shell3/internal/patchtui"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// Run takes over the terminal, prints the welcome message, and enters the
// input loop. Returns when the user double-presses ctrl+c or ctx is done.
func (a *App) Run(ctx context.Context) error {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("patchapp: enter raw mode: %w", err)
	}
	a.oldTermState = oldState
	defer term.Restore(int(os.Stdin.Fd()), oldState) //nolint:errcheck
	defer fmt.Print(pasteOff + "\x1b[?25h\n")

	// Self-pipe so Pause from another goroutine can interrupt the Poll on
	// stdin. SetReadDeadline is unreliable on terminal stdin, hence Poll.
	pr, pw, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("patchapp: pipe: %w", err)
	}
	a.pauseWakeR = pr
	a.pauseWakeW = pw
	defer pr.Close()
	defer pw.Close()

	fmt.Print(pasteOn)

	w, _ := patchtui.Size()
	a.r.Print(renderWelcome(w, a.welcome))
	a.render()

	// Resize handling.
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	go a.tickerLoop(ctx)
	go a.winchLoop(ctx, winch)

	stdinFd := int(os.Stdin.Fd())
	wakeFd := int(pr.Fd())
	buf := make([]byte, 4096)
	wakeBuf := make([]byte, 64)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Quit signal from another goroutine (e.g. /exit handler).
		a.mu.Lock()
		exiting := a.exitFlag
		a.mu.Unlock()
		if exiting {
			return nil
		}
		// RLock blocks while Pause holds the write lock. While paused, any
		// wake bytes are drained on Resume; we just wait.
		a.readMu.RLock()
		fds := []unix.PollFd{
			{Fd: int32(stdinFd), Events: unix.POLLIN},
			{Fd: int32(wakeFd), Events: unix.POLLIN},
		}
		_, perr := unix.Poll(fds, -1)
		if perr != nil && !errors.Is(perr, unix.EINTR) {
			a.readMu.RUnlock()
			return fmt.Errorf("patchapp: poll: %w", perr)
		}
		// Drain any wake bytes (from Pause). If only the wake fd fired,
		// loop back; the next RLock blocks until Resume.
		if fds[1].Revents&unix.POLLIN != 0 {
			_, _ = pr.Read(wakeBuf)
		}
		if fds[0].Revents&unix.POLLIN == 0 {
			a.readMu.RUnlock()
			continue
		}
		n, rerr := unix.Read(stdinFd, buf)
		a.readMu.RUnlock()
		if rerr != nil {
			if errors.Is(rerr, unix.EINTR) {
				continue
			}
			return rerr
		}
		if n == 0 {
			return nil
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
			a.renderStatusOnly()
		}
		a.mu.Unlock()
	}
}

// winchLoop redraws the frame on terminal resize. On the first signal in a
// burst the live frame is erased immediately so stale bar widths don't linger;
// the re-render is debounced 500ms so rapid drag-resize signals collapse into
// one paint.
func (a *App) winchLoop(ctx context.Context, winch <-chan os.Signal) {
	var t *time.Timer
	var pending <-chan time.Time
	resizing := false

	for {
		select {
		case <-ctx.Done():
			if t != nil {
				t.Stop()
			}
			return
		case <-winch:
			if !resizing {
				resizing = true
				a.mu.Lock()
				a.r.Erase()
				a.mu.Unlock()
			}
			if t != nil {
				t.Stop()
			}
			t = time.NewTimer(500 * time.Millisecond)
			pending = t.C
		case <-pending:
			pending = nil
			resizing = false
			a.mu.Lock()
			a.r.Reset()
			a.render()
			a.mu.Unlock()
		}
	}
}
