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
		rt.emit(HostEvent{Session: s.name, Kind: Wake})
	}
}

// renderNotification renders a notification as the short pointer string injected
// into the agent's next turn. Each Kind names where the detail lives so the
// agent can read it on demand.
// agentDoneResultCap bounds (in runes) how much of a subagent's final summary is
// injected into the parent's context on completion, so a long final message can't
// blow up the parent. The full result stays available via `task_status <id>` and
// the :background modal transcript.
const agentDoneResultCap = 2000

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
		// A subagent finished; its completion was injected in-process and the
		// parent surfaces this on an idle wake turn with no user message. The
		// notice carries the subagent's own result summary (Preview) — the human
		// has NOT seen it, so the reminder must tell the model to RELAY it (an
		// earlier "act on it directly, no need to read anything else" wording let
		// the model treat the task as done and stay silent, dropping the answer).
		// The summary is CAPPED here so a subagent that ends with a huge final
		// message can't blow up the parent's context; the model fetches the rest
		// with `task_status <id>`. n.ID is the job id (e.g. sub1), matching the
		// task_* tools and the "started subagent sub1" spawn message.
		status := n.Status
		if status == "" {
			status = "done"
		}
		msg := fmt.Sprintf("subagent %s finished (%s).", n.ID, status)
		if n.Preview != "" {
			result, cut := strutil.CutRunes(n.Preview, agentDoneResultCap)
			if cut {
				result += fmt.Sprintf("… (result truncated; call `task_status %s` for the full result, or open the :background modal for the transcript)", n.ID)
			}
			msg += " Result: " + result
			msg += " That result is the subagent's own summary — relay it to the user now (they have NOT seen it yet): summarize or present it in your reply."
		} else {
			msg += fmt.Sprintf(" It produced no final summary text; call `task_status %s` to read its output, then relay it to the user.", n.ID)
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
