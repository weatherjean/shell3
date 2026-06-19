package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/weatherjean/shell3/pkg/shell3"
)

// RunOnce executes a single turn and streams output to stdout with no TUI. It is
// used for headless and pipeline invocations. The turn drives through
// shell3.Run, whose channel streams public events and closes when the turn
// drains. Returns an error if the turn ended in failure.
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
		return fmt.Errorf("turn ended with error")
	}
	return nil
}

// PrintHeader writes the two-line shell3 brand banner to w, used as a uniform
// top banner for non-interactive commands.
func PrintHeader(w io.Writer) {
	brand := lipgloss.NewStyle().Foreground(cPrimary).Bold(true)
	dim := lipgloss.NewStyle().Foreground(cMuted)
	sub := lipgloss.NewStyle().Foreground(cFgDim)
	fmt.Fprintln(w, brand.Render("๑ï shell3")+"  "+dim.Render("/ˈʃɛli/"))
	fmt.Fprintln(w, sub.Render("minimal Unix-composable coding agent"))
	fmt.Fprintln(w)
}
