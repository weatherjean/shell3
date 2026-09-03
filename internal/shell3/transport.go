package shell3

import (
	"fmt"

	"github.com/weatherjean/shell3/internal/notify"
	"github.com/weatherjean/shell3/internal/strutil"
)

// notifyBg builds a bg_done completion notification for a command job.
func notifyBg(id, cmd string, exit *int, preview, detail string) notify.Notification {
	return notify.Notification{
		Kind: notify.KindBgDone, ID: id, Cmd: cmd, Exit: exit,
		Preview: preview, Detail: detail,
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

// renderNotification renders a bounded command result for the next turn.
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
		if n.Detail != "" {
			msg += fmt.Sprintf("\nFull output: %s", n.Detail)
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
		if n.Preview != "" {
			msg += fmt.Sprintf(". %s", n.Preview)
		}
		return msg
	}
}
