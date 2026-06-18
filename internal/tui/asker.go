package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ANSI styling for the approval prompt. The prompt is written directly to the
// terminal (the TUI renderer is paused via WithReleasedTerminal), so raw escape
// codes are appropriate here rather than the patchtui renderer's styles.
const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiYellow = "\033[33m"
	ansiCyan   = "\033[36m"
	ansiGreen  = "\033[32m"
	ansiRed    = "\033[31m"
)

// confirmPrompt renders a bash_safety approval request for command to w and
// reads a single y/n keypress from in (no Enter required). y/Y allows; n/N,
// Enter, Esc, or Ctrl-C blocks; any other key is ignored. Fail-safe: when in
// doubt, do not run.
//
// Called from the TUI Asker closure inside app.WithReleasedTerminal, so the
// terminal is the agent's; the prompt/result are printed in cooked mode and
// only the keypress read switches to raw mode. If ctx is cancelled (e.g. the
// bash_safety ask timeout fires, or the turn is stopped) before a keypress, it
// returns false (blocked) — fail-safe.
func confirmPrompt(ctx context.Context, in *os.File, w io.Writer, command string) bool {
	fmt.Fprintf(w, "\n%s%s⚠  bash_safety%s %sneeds your approval to run:%s\n",
		ansiBold, ansiYellow, ansiReset, ansiDim, ansiReset)
	fmt.Fprintf(w, "    %s%s%s\n", ansiBold+ansiCyan, command, ansiReset)
	fmt.Fprintf(w, "%sAllow?%s %s[y/N]%s ", ansiBold, ansiReset, ansiDim, ansiReset)

	yes := readYesNoKey(ctx, in)
	if yes {
		fmt.Fprintf(w, "%s✓ allowed%s\n", ansiGreen, ansiReset)
	} else {
		fmt.Fprintf(w, "%s✗ blocked%s\n", ansiRed, ansiReset)
	}
	return yes
}

// readYesNoKey puts in into raw mode and returns on the first decisive keypress
// (no Enter needed): y/Y → true; n/N, Enter, Esc, Ctrl-C → false; other keys are
// ignored. When in is not a terminal (raw mode unavailable — e.g. a pipe under
// test), it falls back to a line read. If ctx is cancelled before a decisive
// key arrives, it returns false (fail-safe deny).
//
// The blocking tty read runs on its own goroutine so a ctx cancel can return
// without it: a tty Read can't be unblocked by closing the fd or a deadline, so
// that goroutine parks until the user eventually presses a key (or the process
// exits). That is an accepted, bounded leak — at most one parked reader per
// timed-out prompt, freed by the next keystroke.
func readYesNoKey(ctx context.Context, in *os.File) bool {
	old, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return lineYesNo(ctx, in)
	}
	defer term.Restore(int(in.Fd()), old) //nolint:errcheck

	res := make(chan bool, 1)
	go func() {
		var b [1]byte
		for {
			n, err := in.Read(b[:])
			if err != nil || n == 0 {
				res <- false
				return
			}
			if yes, decided := keyVerdict(b[0]); decided {
				res <- yes
				return
			}
		}
	}()
	select {
	case <-ctx.Done():
		return false
	case yes := <-res:
		return yes
	}
}

// keyVerdict maps a keypress to a confirm decision. decided=false means the key
// is not y/n/cancel and the caller should keep waiting.
func keyVerdict(b byte) (yes bool, decided bool) {
	switch b {
	case 'y', 'Y':
		return true, true
	case 'n', 'N', 3, 27, '\r', '\n': // n, Ctrl-C, Esc, Enter → no (fail-safe)
		return false, true
	default:
		return false, false
	}
}

// lineYesNo is the non-terminal fallback: read one line, yes only on y/yes. The
// read runs on its own goroutine so a ctx cancel returns false without waiting
// for the line (same accepted bounded-leak tradeoff as readYesNoKey).
func lineYesNo(ctx context.Context, r io.Reader) bool {
	res := make(chan bool, 1)
	go func() {
		sc := bufio.NewScanner(r)
		if !sc.Scan() {
			res <- false
			return
		}
		switch strings.ToLower(strings.TrimSpace(sc.Text())) {
		case "y", "yes":
			res <- true
		default:
			res <- false
		}
	}()
	select {
	case <-ctx.Done():
		return false
	case yes := <-res:
		return yes
	}
}
