package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/weatherjean/shell3/internal/shell3"
)

// `shell3 ask` renderer colors (dark-palette brand values, matching PrintHeader).
var (
	askReason = lipgloss.NewStyle().Foreground(lipgloss.Color("#87A58C")) // reasoning — muted green
	askCall   = lipgloss.NewStyle().Foreground(lipgloss.Color("#5BB6C9")).Bold(true)
	askMeta   = lipgloss.NewStyle().Foreground(bannerMuted) // dim meta lines
	askErr    = lipgloss.NewStyle().Foreground(lipgloss.Color("#DC2626")).Bold(true)
	askLabel  = lipgloss.NewStyle().Foreground(bannerPrimary).Bold(true) // brand yellow
)

// errAskTurnFailed is returned when any turn event carried an error.
var errAskTurnFailed = errors.New("turn ended with error")

// errJobBusClosed marks a closed Wake or job-progress bus: neither can report
// a future change, so the caller ends the wait rather than busy-spinning.
var errJobBusClosed = errors.New("job event bus closed")

// RunAskTurn drives one turn and renders every public event verbosely:
// reasoning, streamed reply, each tool call with its args, untruncated
// results, retries, per-round usage, final totals. Nothing is hidden or capped
// the way a chat front-end would. errAskTurnFailed if the turn ended in error.
func RunAskTurn(ctx context.Context, w io.Writer, sess *shell3.Session, prompt string) error {
	return renderAskEvents(w, sess.Send(ctx, prompt))
}

// FollowAskJobs mirrors the bot's wake loop for a one-shot run: while the
// session has a running job or a queued notice, it renders the wake turn the
// host would run. It KEEPS THE PROCESS ALIVE until every job completes — a
// one-shot run must never exit at turn end and kill in-flight work.
//
// No timeout; only ctx cancellation cuts it short. It blocks on BOTH the Wake
// bus and the job-progress bus, so a completion that queues without an
// immediate wake is still noticed rather than parking the run forever.
//
// ask installs NO CompletionHost, so the runtime's library fallback delivers
// every raw notice straight to the owning session and this loop narrates
// everything — the verbose debugging view. Mail routing is exercised on the
// real bot loop.
func FollowAskJobs(ctx context.Context, w io.Writer, rt *shell3.Runtime, sess *shell3.Session) error {
	announced := 0 // running count last printed, so the waiting note isn't repeated per progress event
	for {
		// Queued notices first: a wake may never fire for a quiet job, and a
		// one-shot run has no later turn to defer to.
		if sess.HasQueuedInput() {
			fmt.Fprintln(w, askLabel.Render("· background task finished, narrating:"))
			if err := renderAskEvents(w, sess.RunQueued(ctx)); err != nil {
				return err
			}
			announced = 0
			continue
		}
		running := 0
		for _, j := range sess.Jobs() {
			// A subagent reports Done at main-turn end while its child lingers
			// for a bash_bg that outlived the turn. Still running: exiting
			// would drop the follow-up's narration.
			if !j.Done || j.ChildOpen {
				running++
			}
		}
		if running == 0 {
			return nil
		}
		if running != announced {
			fmt.Fprintln(w, askMeta.Render(fmt.Sprintf("· waiting for %d background task(s)…", running)))
			announced = running
		}
		if err := waitForJobChangeFn(ctx, rt, sess); err != nil {
			if errors.Is(err, errJobBusClosed) {
				// A closed bus reports no future change, so this is terminal
				// rather than a loop back into waitForJobChange, which returns
				// immediately again on
				// the same closed channel and busy-spin.
				return nil
			}
			return err
		}
	}
}

// waitForJobChange blocks until something worth re-evaluating happens: a Wake
// for this session (a completion queued a notice), a background job reporting
// Done (covers quiet jobs that complete without a wake), or ctx cancellation
// (SIGINT → returns ctx.Err()). A closed bus returns errJobBusClosed — it is
// terminal (no future event can ever arrive on a closed channel), so the
// caller must stop re-entering this function rather than treat it like an
// ordinary wake, or it busy-spins re-selecting on the same closed channel.
// Intermediate job output chunks are ignored so the caller doesn't re-print
// its waiting note per chunk.
func waitForJobChange(ctx context.Context, rt *shell3.Runtime, sess *shell3.Session) error {
	return waitForChange(ctx, sess.ID(), rt.Events(), sess.JobEvents())
}

// waitForJobChangeFn is a package-level indirection over waitForJobChange so
// tests can substitute a fake wait function — e.g. one that always reports a
// closed bus — to exercise FollowAskJobs's handling of that case without
// needing a live Runtime whose event bus can actually be closed (production
// never closes rt.events / rt.jobEvents; see waitForChange's doc).
var waitForJobChangeFn = waitForJobChange

// waitForChange is waitForJobChange's channel-level body, split out so tests
// can drive it with plain (possibly closed) channels without needing a real
// Runtime/Session pair wired up to produce one.
func waitForChange(ctx context.Context, sessID string, hostEvents <-chan shell3.HostEvent, jobEvents <-chan shell3.JobProgress) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-hostEvents:
			if !ok {
				return errJobBusClosed
			}
			if ev.Session == sessID && ev.Kind == shell3.Wake {
				return nil
			}
		case p, ok := <-jobEvents:
			if !ok {
				return errJobBusClosed
			}
			if p.Done {
				return nil
			}
		}
	}
}

// renderAskEvents drains a turn's event channel, rendering each event verbosely.
// Returns errAskTurnFailed if any event was an error.
func renderAskEvents(w io.Writer, events <-chan shell3.Event) error {
	// block tracks which streaming block is open so we can print a header once
	// and, when it ends, terminate the stream's last line plus a blank spacer
	// line. "" = no open block.
	block := ""
	closeBlock := func() {
		if block != "" {
			fmt.Fprintln(w)
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
			openBlock("reason", askReason.Render("· thinking"))
			fmt.Fprint(w, ev.Text)
		case shell3.Token:
			openBlock("reply", askLabel.Render("assistant"))
			fmt.Fprint(w, ev.Text)
		case shell3.ToolCall:
			closeBlock()
			tag := "tool"
			if ev.IsHostTool {
				tag = "host-tool"
			}
			fmt.Fprintln(w, askCall.Render("→ "+ev.ToolName)+askMeta.Render(" ["+tag+"] "+oneLine(ev.ToolInput)))
		case shell3.ToolResult:
			out := strings.TrimRight(ev.ToolOutput, "\n")
			if ev.ToolError {
				fmt.Fprintln(w, askErr.Render("  ✗ result:"))
			} else {
				fmt.Fprintln(w, askMeta.Render("  result:"))
			}
			fmt.Fprintln(w, indent(out, "    "))
			fmt.Fprintln(w)
		case shell3.Usage:
			closeBlock()
			fmt.Fprintln(w, askMeta.Render(fmt.Sprintf("· roundtrip tokens: %d prompt + %d completion = %d",
				ev.PromptTokens, ev.CompletionTokens, ev.TotalTokens)))
		case shell3.Compacted:
			closeBlock()
			fmt.Fprintln(w, askMeta.Render("· auto-compacted: "+ev.Text))
		case shell3.Retry:
			closeBlock()
			fmt.Fprintln(w, askMeta.Render("· retry: "+ev.Text))
		case shell3.Error:
			closeBlock()
			msg := ""
			if ev.Err != nil {
				msg = ev.Err.Error()
				if h := shell3.RecoveryHint(ev.Err); h != "" {
					msg += "\n" + h
				}
			}
			fmt.Fprintln(w, askErr.Render("error: ")+msg)
			hadError = true
		case shell3.Done:
			closeBlock()
			if ev.TotalTokens > 0 {
				fmt.Fprintln(w, askMeta.Render(fmt.Sprintf("· total: %d prompt + %d completion = %d tokens",
					ev.PromptTokens, ev.CompletionTokens, ev.TotalTokens)))
			}
		}
	}
	closeBlock()

	if hadError {
		return errAskTurnFailed
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
		return pre + askMeta.Render("(empty)")
	}
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = pre + ln
	}
	return strings.Join(lines, "\n")
}
