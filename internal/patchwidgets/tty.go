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
	// reader is the source readKey reads from. It defaults to f but may be
	// overridden in tests with a bytes-backed or error-injecting reader.
	reader io.Reader
	// pending holds bytes read but not yet consumed by parseKey, so coalesced
	// keystrokes (fast typing, key auto-repeat, a CSI sequence followed by a
	// char) are returned one key at a time across successive readKey calls
	// rather than dropped.
	pending []byte
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
		_ = f.Close()
		return nil, err
	}
	t := &tty{f: f, oldState: state}
	t.reader = f
	return t, nil
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

// readKey reads one key event. If timeout > 0 and elapses with no key,
// it returns (parsedKey{kind:keyTimeout}, nil). On EOF it returns
// (parsedKey{kind:keyEOF}, io.EOF). Any other read failure is returned with
// the underlying error so callers can distinguish a genuine I/O fault from a
// normal end-of-input dismissal.
//
// Bytes read but not consumed by parseKey (coalesced keystrokes) are buffered
// in t.pending and returned by subsequent calls, so no input is dropped.
func (t *tty) readKey(timeout time.Duration) (parsedKey, error) {
	// Drain any buffered bytes from a previous read before touching the tty.
	if len(t.pending) > 0 {
		if k, consumed := parseKey(t.pending); consumed > 0 {
			// Retaining t.pending's (small, 32-byte) backing array across
			// keystrokes is intentional; correctness does not depend on it.
			t.pending = t.pending[consumed:]
			return k, nil
		}
		// Defensive: parseKey only returns consumed==0 on empty input, so with
		// non-empty pending this should not fire today; the guard protects
		// against a future parseKey that needs more bytes before deciding.
	}

	if timeout > 0 && t.f != nil {
		_ = t.f.SetReadDeadline(time.Now().Add(timeout))
		defer t.f.SetReadDeadline(time.Time{}) //nolint:errcheck
	}

	for {
		buf := make([]byte, 32)
		n, err := t.reader.Read(buf)
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
		t.pending = append(t.pending, buf[:n]...)
		if k, consumed := parseKey(t.pending); consumed > 0 {
			t.pending = t.pending[consumed:]
			return k, nil
		}
		// Defensive: parseKey only returns consumed==0 on empty input, and
		// pending is non-empty here (we just appended n>0 bytes), so this
		// should not fire today; the guard protects against a future parseKey
		// that needs more bytes before deciding, reading more rather than spin.
	}
}

func isTimeout(err error) bool {
	type timeoutErr interface{ Timeout() bool }
	var te timeoutErr
	return errors.As(err, &te) && te.Timeout()
}
