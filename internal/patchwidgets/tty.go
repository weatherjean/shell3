package patchwidgets

import (
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/term"
)

// ErrNoTTY is returned by widget calls when /dev/tty cannot be opened.
// Widgets require a controlling terminal — they have no headless fallback.
var ErrNoTTY = errors.New("patchwidgets: no controlling tty")

// tty bundles a /dev/tty file handle in raw mode with the previous state
// so callers can restore on close. All widget rendering and key reads go
// through tty so the process stdin/stdout remain untouched.
type tty struct {
	f        *os.File
	oldState *term.State
}

// openTTY opens /dev/tty and puts it in raw mode. The returned tty must
// be closed with [tty.Close] to restore the original terminal state.
func openTTY() (*tty, error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, ErrNoTTY
	}
	state, err := term.MakeRaw(int(f.Fd()))
	if err != nil {
		f.Close()
		return nil, err
	}
	return &tty{f: f, oldState: state}, nil
}

// Close restores the original terminal state and closes the tty handle.
func (t *tty) Close() {
	if t == nil || t.f == nil {
		return
	}
	if t.oldState != nil {
		_ = term.Restore(int(t.f.Fd()), t.oldState)
	}
	_ = t.f.Close()
}

// Write paints to the tty.
func (t *tty) Write(p []byte) (int, error) { return t.f.Write(p) }

// readKey reads one key event. If timeout > 0 and elapses with no key,
// it returns (parsedKey{kind:keyTimeout}, nil). On EOF it returns
// (parsedKey{kind:keyEOF}, io.EOF).
func (t *tty) readKey(timeout time.Duration) (parsedKey, error) {
	buf := make([]byte, 32)
	if timeout > 0 {
		_ = t.f.SetReadDeadline(time.Now().Add(timeout))
		defer t.f.SetReadDeadline(time.Time{}) //nolint:errcheck
	}
	n, err := t.f.Read(buf)
	if err != nil {
		if isTimeout(err) {
			return parsedKey{kind: keyTimeout}, nil
		}
		if errors.Is(err, io.EOF) {
			return parsedKey{kind: keyEOF}, io.EOF
		}
		return parsedKey{kind: keyEOF}, err
	}
	if n == 0 {
		return parsedKey{kind: keyEOF}, io.EOF
	}
	k, _ := parseKey(buf[:n])
	return k, nil
}

func isTimeout(err error) bool {
	type timeoutErr interface{ Timeout() bool }
	var te timeoutErr
	return errors.As(err, &te) && te.Timeout()
}
