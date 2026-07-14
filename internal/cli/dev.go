package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/weatherjean/shell3/internal/shell3"
)

// dev renderer colors (dark-palette brand values, matching PrintHeader).
var (
	devReason = lipgloss.NewStyle().Foreground(lipgloss.Color("#87A58C")) // reasoning — muted green
	devCall   = lipgloss.NewStyle().Foreground(lipgloss.Color("#5BB6C9")).Bold(true)
	devMeta   = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")) // dim meta lines
	devErr    = lipgloss.NewStyle().Foreground(lipgloss.Color("#DC2626")).Bold(true)
	devLabel  = lipgloss.NewStyle().Foreground(lipgloss.Color("#EAB308")).Bold(true) // brand yellow
)

// errDevTurnFailed is returned when any turn event carried an error.
var errDevTurnFailed = errors.New("turn ended with error")

// RunDevTurn drives one turn on sess and renders every public event verbosely to
// w: reasoning, streamed reply, each tool call with its args, full (untruncated)
// tool results, retries, per-roundtrip token usage, and the final totals. It is
// the dev CLI's window into exactly what the agent does — nothing is hidden or
// capped the way a chat front-end would. Returns errDevTurnFailed if the turn
// ended in an error event.
func RunDevTurn(ctx context.Context, w io.Writer, sess *shell3.Session, prompt string) error {
	return renderDevEvents(w, sess.Send(ctx, prompt))
}

// FollowDevJobs mirrors the Telegram host's wake loop for a one-shot dev run:
// while the session has a running background job (a spawned subagent or bash_bg)
// or queued input, it waits for the completion Wake and renders the follow-up
// turn the host would run to narrate the result. It returns when the session is
// idle with no running jobs, ctx is cancelled, or a wait exceeds waitEach.
func FollowDevJobs(ctx context.Context, w io.Writer, rt *shell3.Runtime, sess *shell3.Session, waitEach time.Duration) error {
	for {
		running := 0
		for _, j := range sess.Jobs() {
			if !j.Done {
				running++
			}
		}
		if running == 0 && !sess.HasQueuedInput() {
			return nil
		}
		if running > 0 {
			fmt.Fprintln(w, devMeta.Render(fmt.Sprintf("· waiting on %d background job(s)…", running)))
		}
		// Wait for a Wake for this session (the runtime keys HostEvent.Session on
		// Session.ID()). A bounded wait keeps a hung/never-finishing job from
		// parking the CLI forever.
		woke := false
		timer := time.NewTimer(waitEach)
		for !woke {
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
				fmt.Fprintln(w, devMeta.Render("· stopped waiting (timeout); background jobs may still be running"))
				return nil
			case ev, ok := <-rt.Events():
				if !ok {
					timer.Stop()
					return nil
				}
				if ev.Session == sess.ID() && ev.Kind == shell3.Wake {
					woke = true
				}
			}
		}
		timer.Stop()
		if sess.HasQueuedInput() {
			fmt.Fprintln(w, devLabel.Render("· wake — job finished, narrating:"))
			if err := renderDevEvents(w, sess.RunQueued(ctx)); err != nil {
				return err
			}
		}
	}
}

// renderDevEvents drains a turn's event channel, rendering each event verbosely.
// Returns errDevTurnFailed if any event was an error.
func renderDevEvents(w io.Writer, events <-chan shell3.Event) error {
	// block tracks which streaming block is open so we can print a header once
	// and a trailing newline when it ends. "" = no open block.
	block := ""
	closeBlock := func() {
		if block != "" {
			fmt.Fprintln(w)
			block = ""
		}
	}
	openBlock := func(kind, header string) {
		if block == kind {
			return
		}
		closeBlock()
		fmt.Fprintln(w, header)
		block = kind
	}

	hadError := false
	for ev := range events {
		switch ev.Kind {
		case shell3.Reasoning:
			// Print reasoning text raw (only the block header is styled): streamed
			// per-token styling would emit an ANSI escape per token, which is noise
			// when the output is piped.
			openBlock("reason", devReason.Render("· thinking"))
			fmt.Fprint(w, ev.Text)
		case shell3.Token:
			openBlock("reply", devLabel.Render("assistant"))
			fmt.Fprint(w, ev.Text)
		case shell3.ToolCall:
			closeBlock()
			tag := "tool"
			if ev.IsCustomTool {
				tag = "custom-tool"
			}
			fmt.Fprintln(w, devCall.Render("→ "+ev.ToolName)+devMeta.Render(" ["+tag+"] "+oneLine(ev.ToolInput)))
		case shell3.ToolResult:
			out := strings.TrimRight(ev.ToolOutput, "\n")
			if ev.ToolError {
				fmt.Fprintln(w, devErr.Render("  ✗ result:"))
			} else {
				fmt.Fprintln(w, devMeta.Render("  result:"))
			}
			fmt.Fprintln(w, indent(out, "    "))
		case shell3.Usage:
			closeBlock()
			fmt.Fprintln(w, devMeta.Render(fmt.Sprintf("· roundtrip tokens: %d prompt + %d completion = %d",
				ev.PromptTokens, ev.CompletionTokens, ev.TotalTokens)))
		case shell3.Compacted:
			closeBlock()
			fmt.Fprintln(w, devMeta.Render("· auto-compacted: "+ev.Text))
		case shell3.Retry:
			closeBlock()
			fmt.Fprintln(w, devMeta.Render("· retry: "+ev.Text))
		case shell3.Error:
			closeBlock()
			msg := ""
			if ev.Err != nil {
				msg = ev.Err.Error()
				if h := shell3.RollbackHint(ev.Err); h != "" {
					msg += "\n" + h
				}
			}
			fmt.Fprintln(w, devErr.Render("error: ")+msg)
			hadError = true
		case shell3.Done:
			closeBlock()
			if ev.TotalTokens > 0 {
				fmt.Fprintln(w, devMeta.Render(fmt.Sprintf("· total: %d prompt + %d completion = %d tokens",
					ev.PromptTokens, ev.CompletionTokens, ev.TotalTokens)))
			}
		}
	}
	closeBlock()

	if hadError {
		return errDevTurnFailed
	}
	return nil
}

// oneLine collapses whitespace so a tool's raw JSON args render on a single line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// indent prefixes every line of s with pre.
func indent(s, pre string) string {
	if s == "" {
		return pre + devMeta.Render("(empty)")
	}
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = pre + ln
	}
	return strings.Join(lines, "\n")
}
