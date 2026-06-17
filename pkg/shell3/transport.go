package shell3

import (
	"fmt"
	"time"

	"github.com/weatherjean/shell3/internal/notify"
	"github.com/weatherjean/shell3/internal/runs"
)

// report delivers this session's completion to the live host by appending ONE
// pointer line to the project's inbox.jsonl (the file the runtime watches; see
// Runtime.injectPointer). It is the entire completion path: background subagents
// are fire-and-forget, so there is no socket, no liveness, and no dormant-parent
// revive — a finishing subagent just drops a pointer and exits.
//
// A pointer is appended only for a subagent: a run with a recorded ParentSession.
// A root session (no parent) reports nothing — there is no one above it to
// notify. The pointer carries no payload: Path points at this run's transcript
// and Summary is the short preview, so the detail stays in the run's own jsonl.
// Called once during Close, after the turn has joined.
func (s *Session) report(n notify.Notification) {
	if s.parentSession == "" {
		return
	}
	p := runs.Pointer{
		TS:      time.Now().UTC().Format(time.RFC3339),
		RunID:   s.sess.ID(),
		Kind:    n.Kind,
		Path:    n.Transcript,
		Summary: n.Preview,
		Exit:    n.Exit,
	}
	// reportInbox (from `shell3 run --inbox <path>`) targets the PARENT's inbox
	// directly, so the completion lands in the file the parent watches regardless
	// of this subagent's own working directory. Falls back to this run's own
	// store inbox when unset (parent and child share a runtime root).
	if s.reportInbox != "" {
		_ = runs.AppendPointer(s.reportInbox, p)
		return
	}
	if s.cfg.Store != nil {
		_ = s.cfg.Store.AppendInbox(p)
	}
}

// injectNotification injects a received notification into the running session,
// waking it if idle.
func (s *Session) injectNotification(rt *Runtime, n notify.Notification) {
	s.sess.Interject(renderNotification(n))
	if !s.isBusy() {
		rt.emit(HostEvent{Session: s.name, Kind: Wake})
	}
}

// renderNotification renders a notification as the short pointer string injected
// into the agent's next turn. Each Kind names where the detail lives so the
// agent can read it on demand.
func renderNotification(n notify.Notification) string {
	switch n.Kind {
	case notify.KindBgDone:
		exit := "?"
		if n.Exit != nil {
			exit = fmt.Sprintf("%d", *n.Exit)
		}
		msg := fmt.Sprintf("background job %s exited (code %s).", n.ID, exit)
		if n.Log != "" {
			msg += fmt.Sprintf(" Output log: %s.", n.Log)
		}
		if n.Cmd != "" {
			msg += fmt.Sprintf(" cmd: %s", n.Cmd)
		}
		return msg
	case notify.KindAgentDone:
		// A subagent (a backgrounded `shell3 run --parent-session`) finished and
		// self-reported. The notification ITSELF carries the subagent's result
		// summary (Preview); the transcript pointer is only for when the summary
		// isn't enough. So we frame the preview as the answer to act on and make
		// reading the transcript explicitly optional — with the exact one-liner to
		// extract the final answer, so the agent never has to reverse-engineer the
		// JSONL audit schema.
		status := n.Status
		if status == "" {
			status = "done"
		}
		msg := fmt.Sprintf("subagent %s finished (%s).", n.ID, status)
		if n.Preview != "" {
			msg += " Result: " + n.Preview
		}
		if n.Transcript != "" {
			extract := fmt.Sprintf("jq -rs 'map(select(.kind==\"assistant_message\"))[-1].text' %s", n.Transcript)
			if n.Preview != "" {
				msg += fmt.Sprintf(" That result is the subagent's own summary — act on it directly; you do NOT need to read anything else to get the answer. Only if you need its full output or intermediate steps, read the transcript: %s", extract)
			} else {
				msg += fmt.Sprintf(" Read its result from the transcript: %s", extract)
			}
		}
		return msg
	default:
		// Unknown / future kinds: deliver a generic pointer rather than dropping
		// it, so a producer ahead of the host still gets noticed.
		msg := fmt.Sprintf("notification %s", n.Kind)
		if n.ID != "" {
			msg += " " + n.ID
		}
		if n.Status != "" {
			msg += fmt.Sprintf(" (%s)", n.Status)
		}
		if n.Transcript != "" {
			msg += fmt.Sprintf(". Transcript: %s", n.Transcript)
		}
		if n.Preview != "" {
			msg += fmt.Sprintf(". %s", n.Preview)
		}
		return msg
	}
}
