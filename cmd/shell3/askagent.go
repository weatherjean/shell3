//go:build unix

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/shell3"
)

// dispatchPollInterval bounds how long runAskAgent waits between Jobs() checks
// when no job event arrives. The event bus is a shared, lossy channel (emitJob
// drops on a full buffer, and any other consumer competes for the same
// events), so the poll — not the event — is what actually decides the job is
// done; the event only shortens the wait.
const dispatchPollInterval = 250 * time.Millisecond

// errAskAgentFailed reports a subagent run that ended on a turn error. The
// message carries the agent's own error text so a calling script sees the
// cause on stderr, not just a nonzero exit.
var errAskAgentFailed = errors.New("ask: subagent run failed")

// runAskAgent is `shell3 ask --agent <name>`: one headless run of a named
// subagent, its final text written to out and nothing else. It exists so batch
// scripts stop hand-rolling HTTP clients against the model — a script that
// shells out here inherits the real adapter (reasoning-channel separation,
// think-leak filtering, truncation detection) and a tool-capable turn, instead
// of reimplementing the parsing half badly.
//
// stdout carries ONLY the agent's reply, so a caller can consume it directly;
// every diagnostic goes to diag (stderr). The run is fire-and-forget on the
// job runtime, so this waits for the job to finish AND its child session to
// close — a subagent whose bash_bg outlives its main turn keeps producing
// output after Done flips true.
func runAskAgent(ctx context.Context, out, diag io.Writer, sess *shell3.Session, agent, prompt string) error {
	// Subscribe before dispatching: a fast job could otherwise finish and emit
	// its terminal event before we start listening, leaving the select with
	// nothing to wake it (the poll would still catch it, just a beat later).
	events := sess.JobEvents()

	id, err := sess.Dispatch(agent, prompt, shell3.DispatchOpts{
		Description: "ask --agent " + agent,
	})
	if err != nil {
		return fmt.Errorf("ask: dispatch %q: %w", agent, err)
	}
	fmt.Fprintf(diag, "agent=%s job=%s\n", agent, id)

	info, err := waitForDispatch(ctx, events, sess.Jobs, id)
	if err != nil {
		return err
	}
	// A truncated reply is a FAILED run here, not a partial success. The
	// truncation notice rides the reply text itself (chat appends it to the
	// assistant message), so a caller consuming stdout would otherwise store
	// "⚠️ [output cut off …]" as if it were content — the notice is written for
	// a human reading a chat bubble, not for a script. Report it on stderr,
	// where a scripted caller actually looks, and exit nonzero.
	if chat.IsTruncatedReply(info.Summary) {
		fmt.Fprintln(diag, info.Summary)
		return fmt.Errorf("%w: reply hit the model's max_tokens limit (raise max_tokens for this model)", errAskAgentFailed)
	}
	// Print whatever the agent produced before reporting a failure: a turn that
	// errored partway still has usable text, and dropping it would leave the
	// caller with an exit code and nothing to inspect.
	if info.Summary != "" {
		fmt.Fprintln(out, info.Summary)
	}
	if info.Error != "" {
		return fmt.Errorf("%w: %s", errAskAgentFailed, info.Error)
	}
	if info.Summary == "" {
		return fmt.Errorf("%w: agent produced no output", errAskAgentFailed)
	}
	return nil
}

// waitForDispatch blocks until job id has finished and its child session has
// closed, returning that job's final JobInfo. Job events only shorten the wait;
// the jobs snapshot is the authority, because the event bus can drop. The
// snapshot arrives as a function so tests can drive it with a canned job list.
func waitForDispatch(ctx context.Context, events <-chan shell3.JobProgress, jobs func() []shell3.JobInfo, id string) (shell3.JobInfo, error) {
	tick := time.NewTicker(dispatchPollInterval)
	defer tick.Stop()
	for {
		if info, ok := findJob(jobs(), id); ok && info.Done && !info.ChildOpen {
			return info, nil
		}
		select {
		case <-ctx.Done():
			return shell3.JobInfo{}, ctx.Err()
		case <-events:
			// Any event is just a hint to re-check; the loop head decides.
		case <-tick.C:
		}
	}
}

func findJob(jobs []shell3.JobInfo, id string) (shell3.JobInfo, bool) {
	for _, j := range jobs {
		if j.ID == id {
			return j, true
		}
	}
	return shell3.JobInfo{}, false
}
