package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/weatherjean/shell3/internal/sink"
	"github.com/weatherjean/shell3/pkg/shell3"
)

// previewMax caps the agent_done preview at ≤200 chars (the design budget for a
// sink pointer); the full result lives in the transcript the parent reads.
const previewMax = 200

// RunOnce executes a single turn and prints output to stdout. No TUI.
//
// It drives the turn through shell3.Run, which does Start + Send + Close: the
// returned channel streams the turn's public Events and closes when the turn
// drains (Close runs automatically). The JSONL audit log, if any, is owned by
// pkg/shell3 via spec.OutPath — RunOnce no longer opens its own sink.
//
// When spec.AppendSinkFile is set (a subagent invocation: `shell3
// --append-sinkfile <sink> --id <id> --out <transcript> …`), RunOnce appends
// exactly ONE agent_done Notification to that sink on completion — the child
// self-reporting its lifecycle to the parent session's sink. Status is derived
// from whether the run errored; the preview is the final assistant text
// (≤200 chars); the transcript pointer is spec.OutPath. This is the only place
// that can see both the run's outcome and its final text.
func RunOnce(ctx context.Context, spec shell3.Spec) error {
	events, err := shell3.Run(ctx, spec)
	if err != nil {
		// A failed Start never ran a turn; still self-report an error so a
		// waiting parent is not left hanging on a child that never started.
		selfReport(spec, "", true)
		return err
	}

	hadError := false
	var finalText strings.Builder // accumulates the latest assistant text for the preview
	for ev := range events {
		switch ev.Kind {
		case shell3.Token:
			fmt.Print(ev.Text)
			finalText.WriteString(ev.Text)
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

	selfReport(spec, strings.TrimSpace(finalText.String()), hadError)

	if hadError {
		return fmt.Errorf("turn ended with error")
	}
	return nil
}

// selfReport appends the single agent_done Notification when this run is a
// subagent invocation (spec.AppendSinkFile set). A no-op otherwise. The
// transcript pointer (spec.OutPath) is the durable guarantee; an empty preview
// is acceptable when the run produced no assistant text. Best-effort: a sink
// append failure must not fail the run (the parent still has the transcript).
func selfReport(spec shell3.Spec, finalText string, errored bool) {
	if spec.AppendSinkFile == "" {
		return
	}
	status := "ok"
	if errored {
		status = "error"
	}
	_ = sink.Append(spec.AppendSinkFile, sink.Notification{
		Kind:       "agent_done",
		ID:         spec.ID,
		Status:     status,
		Transcript: spec.OutPath,
		Preview:    truncatePreview(finalText),
	})
}

// truncatePreview clamps s to previewMax runes, cutting on a rune boundary so
// the preview never carries a split multibyte rune. Appends an ellipsis when cut.
func truncatePreview(s string) string {
	if len(s) <= previewMax {
		return s
	}
	cut := previewMax
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
