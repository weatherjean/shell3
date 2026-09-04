package shell3

import (
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/inbox"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/strutil"
)

// NotifyText queues a host reminder and wakes the session if idle. It is for
// host lifecycle context such as /superstop, not background-job delivery.
func (s *Session) NotifyText(text string) {
	s.sess.InterjectHostNotice(text)
	if !s.isBusy() {
		s.wake()
	}
}

// NotifyTextNoWake queues a host reminder for the next ordinary turn.
func (s *Session) NotifyTextNoWake(text string) {
	s.sess.InterjectHostNotice(text)
}

func (rt *Runtime) mainInbox() inbox.Store {
	return inbox.Store{Root: paths.NewLocal(rt.workDir).Root}
}

// persistCommandCompletion writes the single durable delivery record. true
// means the running marker may be deleted; false retains it for restart
// recovery. /superstop already told the user and suppresses the notice, while
// graceful shutdown keeps the marker so the next process reports the loss.
func (m *jobManager) persistCommandCompletion(j *bgJob, exit int, tail string) bool {
	m.mu.Lock()
	suppressed, shutdown := j.suppress, j.shutdownCancel
	m.mu.Unlock()
	if suppressed {
		return true
	}
	if shutdown {
		return false
	}
	if m.rt == nil {
		return true
	}
	status, event := "completed", "bash_bg.completed"
	if exit != 0 {
		status, event = "failed", "bash_bg.failed"
	}
	var body strings.Builder
	fmt.Fprintf(&body, "background job %s %s (exit %d).\ncommand: %s", j.id, status, exit,
		strutil.Truncate(strings.Join(strings.Fields(j.title), " "), 500))
	if tail = strings.TrimSpace(tail); tail != "" {
		fmt.Fprintf(&body, "\noutput tail:\n%s", tail)
	}
	if j.logPath != "" {
		fmt.Fprintf(&body, "\nfull output: %s", j.logPath)
	}
	_, err := m.rt.mainInbox().Notify(inbox.Request{
		To: "main", Source: "bash_bg:" + j.id, Event: event, Correlation: j.id, Body: body.String(),
	})
	if err != nil {
		m.rt.Logger().Warn("background completion persistence failed", "job", j.id, "error", err)
		return false
	}
	return true
}
