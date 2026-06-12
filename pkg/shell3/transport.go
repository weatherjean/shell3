package shell3

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/weatherjean/shell3/internal/notify"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/socket"
)

// startTransport opens this session's socket listener and marks it live in the
// store registry. Replaces the old sink watcher. A session with no store id, no
// workdir, or no store skips the transport.
func (s *Session) startTransport(rt *Runtime) {
	id := s.sess.ID()
	if id == 0 || s.cfg.WorkDir == "" || s.cfg.Store == nil {
		return
	}
	sock := paths.SockPath(s.cfg.WorkDir, id)
	lis, err := socket.Listen(sock, func(line []byte) {
		var n notify.Notification
		if err := json.Unmarshal(line, &n); err != nil {
			return
		}
		s.injectNotification(rt, n)
	})
	if err != nil {
		return
	}
	s.mu.Lock()
	s.listener = lis
	s.mu.Unlock()
	_ = s.cfg.Store.SetLiveness(id, os.Getpid(), sock, "live")
}

// stopTransport closes the listener and marks the session dormant so future
// reporters route to the inbox + revive instead of a dead socket.
func (s *Session) stopTransport() {
	s.mu.Lock()
	lis := s.listener
	s.listener = nil
	s.mu.Unlock()
	id := s.sess.ID()
	if lis != nil {
		_ = lis.Close()
	}
	if id != 0 && s.cfg.Store != nil {
		_ = s.cfg.Store.SetLiveness(id, 0, "", "dormant")
	}
}

// injectNotification injects a received notification into the running session,
// waking it if idle. Mirrors the old sink watcher's deliverNotification.
func (s *Session) injectNotification(rt *Runtime, n notify.Notification) {
	s.sess.Interject(renderNotification(n))
	if !s.isBusy() {
		rt.emit(HostEvent{Session: s.name, Kind: Wake})
	}
}

// renderNotification renders a notification as the short pointer string injected
// into the agent's next turn. Each Kind names where the detail lives so the
// agent can read it on demand. Mirrors the old sink watcher's formatNotification.
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
		// A subagent (a backgrounded `shell3 --append-sinkfile`) finished and
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
