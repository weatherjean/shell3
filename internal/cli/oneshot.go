// Package cli holds the non-interactive front-end helpers shared by the cobra
// subcommands (shell3 run / boot / read-session): the one-shot turn renderer
// and the brand banner. It carries no interactive terminal machinery — the
// interactive TUI was removed when shell3 became a Telegram-first hosted agent.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/weatherjean/shell3/internal/shell3"
)

// RunOnce executes a single turn and streams output to stdout with no TUI. It is
// used for headless and pipeline invocations (shell3 run). The turn drives
// through shell3.Run, whose channel streams public events and closes when the
// turn drains. Returns an error if the turn ended in failure.
func RunOnce(ctx context.Context, spec shell3.Spec) error {
	events, err := shell3.Run(ctx, spec)
	if err != nil {
		return err
	}

	hadError := false
	for ev := range events {
		switch ev.Kind {
		case shell3.Token:
			fmt.Print(ev.Text)
		case shell3.ToolResult:
			// Tool output, trimmed and wrapped in blank lines for separation.
			fmt.Println()
			fmt.Print(strings.TrimRight(ev.ToolOutput, "\n"))
			fmt.Println()
		case shell3.Retry:
			fmt.Fprintln(os.Stderr, "retry:", ev.Text)
		case shell3.Error:
			msg := ""
			if ev.Err != nil {
				msg = ev.Err.Error()
				if h := shell3.RollbackHint(ev.Err); h != "" {
					msg += "\n" + h
				}
			}
			fmt.Fprintln(os.Stderr, "error:", msg)
			hadError = true
		case shell3.Done:
			fmt.Println()
		}
	}

	if hadError {
		return errTurnFailed
	}
	return nil
}

// errTurnFailed is the terminal error a one-shot run returns when any turn
// event carried an error; callers can match it with errors.Is.
var errTurnFailed = errors.New("turn ended with error")
