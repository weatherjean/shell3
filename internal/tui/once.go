package tui

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// RunOnce executes a single turn and prints output to stdout. No TUI.
//
// It drives the turn through shell3.Run, which does Start + Send + Close: the
// returned channel streams the turn's public Events and closes when the turn
// drains (Close runs automatically). The JSONL audit log, if any, is owned by
// pkg/shell3 via spec.OutPath — RunOnce no longer opens its own sink.
//
// Completion reporting (a subagent self-reporting its lifecycle to its parent)
// is owned by pkg/shell3: Session.report fires during Close over the
// socket/SQLite-inbox transport. RunOnce no longer self-reports.
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
			// Show tool body on stdout, trimmed and followed by a blank line
			// for separation. Headers are skipped here — RunOnce is for
			// pipeline use where minimal output is preferable.
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
		return fmt.Errorf("turn ended with error")
	}
	return nil
}
