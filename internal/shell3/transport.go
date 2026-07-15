package shell3

import (
	"fmt"
	"time"

	"github.com/weatherjean/shell3/internal/notify"
	"github.com/weatherjean/shell3/internal/strutil"
)

// notifyBg builds a bg_done completion notification for a command job.
func notifyBg(id, cmd string, exit *int, preview string) notify.Notification {
	return notify.Notification{
		Kind: notify.KindBgDone, ID: id, Cmd: cmd, Exit: exit,
		Preview: preview, TS: time.Now().UTC().Format(time.RFC3339),
	}
}

// injectNoticeNoWake adds a completion notice to the session's inbox WITHOUT
// emitting a Wake (bash_bg completions are informational; the agent sees them on
// its next turn).
func (s *Session) injectNoticeNoWake(n notify.Notification) {
	s.sess.InterjectNotice(renderNotification(n))
}

// injectNotification injects a received notification into the running session,
// waking it if idle.
func (s *Session) injectNotification(rt *Runtime, n notify.Notification) {
	s.sess.InterjectNotice(renderNotification(n))
	if !s.isBusy() {
		rt.emit(HostEvent{Session: s.sess.ID(), Kind: Wake})
	}
}

// renderNotification renders a notification as the short pointer string injected
// into the agent's next turn. Each Kind names where the detail lives so the
// agent can read it on demand.
// agentDoneResultCap bounds (in runes) how much of a subagent's final summary is
// injected into the parent's context on completion, so a long final message can't
// blow up the parent. The full result stays available via `task_status <id>` and
// the dashboard's job transcript view.
const agentDoneResultCap = 2000

// renderAgentNotice renders the agent_done/agent_update notice: verb and label
// vary; the relay contract is shared. The notice carries the subagent's own
// summary (Preview) — the human has NOT seen it, so the reminder must tell the
// model to RELAY it (an earlier "act on it directly" wording let the model
// treat the task as done and stay silent, dropping the answer). The summary is
// CAPPED so a huge final message can't blow up the parent's context; the model
// fetches the rest with `task_status <id>`.
func renderAgentNotice(n notify.Notification, verb, defaultStatus, label string) string {
	status := n.Status
	if status == "" {
		status = defaultStatus
	}
	msg := fmt.Sprintf("subagent %s %s (%s).", n.ID, verb, status)
	if n.Preview == "" {
		return msg + fmt.Sprintf(" It produced no summary text; call `task_status %s` to read its output, then relay it to the user.", n.ID)
	}
	result, cut := strutil.CutRunes(n.Preview, agentDoneResultCap)
	if cut {
		result += fmt.Sprintf("… (truncated; call `task_status %s` for the full result)", n.ID)
	}
	msg += " " + label + " " + result
	msg += " That is the subagent's own summary — relay it to the user now (they have NOT seen it yet): summarize or present it in your reply."
	return msg
}

func renderNotification(n notify.Notification) string {
	// Defense in depth: these fields carry untrusted text (command output,
	// subagent summaries, error strings). chat.reminderBlock neutralizes again
	// at injection time, but defang here too so no future caller of the
	// rendered string can be tricked into emitting a forged envelope.
	n.Preview = strutil.NeutralizeReminderTags(n.Preview)
	n.Cmd = strutil.NeutralizeReminderTags(n.Cmd)
	n.Status = strutil.NeutralizeReminderTags(n.Status)
	switch n.Kind {
	case notify.KindBgDone:
		exit := "?"
		if n.Exit != nil {
			exit = fmt.Sprintf("%d", *n.Exit)
		}
		msg := fmt.Sprintf("background job %s exited (code %s).", n.ID, exit)
		if n.Status != "" {
			// e.g. "started by subagent sub1" on the degrade path, where an
			// orphaned job's notice is delivered to the root session instead.
			msg += fmt.Sprintf(" (%s)", n.Status)
		}
		if n.Cmd != "" {
			msg += fmt.Sprintf(" cmd: %s", n.Cmd)
		}
		if n.Preview != "" {
			msg += fmt.Sprintf(" Output tail: %s", n.Preview)
		}
		return msg
	case notify.KindAgentDone:
		// A subagent finished; its completion was injected in-process and the
		// parent surfaces this on an idle wake turn with no user message. n.ID is
		// the job id (e.g. sub1), matching the task_* tools and the "started
		// subagent sub1" spawn message.
		return renderAgentNotice(n, "finished", "done", "Result:")
	case notify.KindAgentUpdate:
		// A follow-up from a subagent that already reported done: one of its
		// background jobs finished afterwards, the child session ran a follow-up
		// turn over the result, and Preview carries that turn's summary. Same
		// relay contract and cap as agent_done.
		return renderAgentNotice(n, "follow-up", "background job finished", "Update:")
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
		if n.Preview != "" {
			msg += fmt.Sprintf(". %s", n.Preview)
		}
		return msg
	}
}
