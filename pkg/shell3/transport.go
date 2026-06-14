package shell3

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/weatherjean/shell3/internal/bgjobs"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/jobstore"
	"github.com/weatherjean/shell3/internal/notify"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/proc"
	"github.com/weatherjean/shell3/internal/socket"
	"github.com/weatherjean/shell3/internal/store"
)

type reportRoute int

const (
	routeNone   reportRoute = iota // no parent (root) or not found
	routeSocket                    // parent live → push over its socket
	routeRevive                    // parent dormant → inbox + revive
)

// routeReport decides how to deliver a completion to parentID based on its
// current liveness. Pure decision (no side effects) so it is unit-testable.
// routeReport returns the delivery route plus the parent's recorded pid. The pid
// lets reportTo reclaim a parent stuck "live" whose process has died: such a row
// is NOT routed to the socket (proc.Alive is false), so it falls to revive, and
// the pid is handed to ClaimRevive as the dead pid to reclaim.
func routeReport(st *store.Store, parentID int64) (reportRoute, string, int) {
	if parentID == 0 {
		return routeNone, "", 0
	}
	pid, sock, status, err := st.Liveness(parentID)
	if err != nil {
		return routeRevive, "", 0 // treat unknown as dormant; revive is the safe path
	}
	if status == "live" && sock != "" && proc.Alive(pid) {
		return routeSocket, sock, pid
	}
	return routeRevive, "", pid
}

// report delivers this session's completion notification to its parent: live
// parent → socket; dormant parent → SQLite inbox + revive (one winner). Revive
// failure falls back to escalating one hop up so the human at root still learns
// of it. Called once during Close, before stopTransport marks us dormant.
func (s *Session) report(n notify.Notification) {
	st := s.cfg.Store
	if st == nil {
		return
	}
	parentID, err := st.ParentSessionID(s.sess.ID())
	if err != nil || parentID == 0 {
		return
	}
	s.reportTo(st, parentID, n)
}

func (s *Session) reportTo(st *store.Store, parentID int64, n notify.Notification) {
	if n.Origin == 0 {
		n.Origin = s.sess.ID()
	}
	route, sock, pid := routeReport(st, parentID)
	switch route {
	case routeSocket:
		payload, _ := json.Marshal(n)
		if err := socket.Send(sock, payload); err == nil {
			return
		}
		// Socket vanished between the liveness read and the send: fall through to
		// revive as if dormant.
		fallthrough
	case routeRevive:
		payload, _ := json.Marshal(n)
		if err := st.AppendInbox(parentID, payload); err != nil {
			// A dropped inbox row is a lost result — make the failure visible
			// rather than silently black-holing it.
			chat.LogOrNoop(s.cfg.Log).Warn("report: append inbox failed", "parent", parentID, "error", err)
		}
		// Reclaim a parent stuck "live" whose process is gone (kill -9 / crash):
		// pass its pid only when confirmed dead, so a healthy parent is never
		// double-revived. A dormant parent uses deadPID 0 (the dormant branch).
		reclaimPID := 0
		if pid > 0 && !proc.Alive(pid) {
			reclaimPID = pid
		}
		won, err := st.ClaimRevive(parentID, reclaimPID)
		if err == nil && won {
			if spawnErr := s.spawnRevive(st, parentID); spawnErr == nil {
				return
			}
			// Revive spawn failed: release the claim and escalate one hop so the
			// result is not black-holed.
			if serr := st.SetLiveness(parentID, 0, "", "dormant"); serr != nil {
				chat.LogOrNoop(s.cfg.Log).Warn("report: release revive claim failed", "parent", parentID, "error", serr)
			}
		}
		if err == nil && !won {
			return // winner will deliver; our inbox append is enough
		}
		if grand, gerr := st.ParentSessionID(parentID); gerr == nil && grand != 0 {
			s.reportTo(st, grand, n)
		}
	}
}

// revivePrompt drains a dormant parent's inbox and renders the combined
// notifications into a single wake prompt. ClaimRevive already elected a single
// winner before this runs; we drain in the spawner and pass the text as
// --prompt. Any late arrivals remain in the inbox for the next report cycle.
func revivePrompt(st *store.Store, parentID int64) (string, error) {
	payloads, err := st.DrainInbox(parentID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("<system-reminder>\nYou were resumed to handle completed delegated work. The following subagent results arrived while you were idle:\n</system-reminder>\n\n")
	for _, p := range payloads {
		var n notify.Notification
		if err := json.Unmarshal(p, &n); err != nil {
			continue
		}
		b.WriteString(renderNotification(n))
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// spawnRevive relaunches the dormant parent as a background `shell3 run --resume
// <parentID>` whose prompt is the drained inbox. The revived process loads the
// parent's full message history (newSession resume path), processes the results,
// and on its own completion reports up ITS parent pointer — continuing the
// cascade toward root.
func (s *Session) spawnRevive(st *store.Store, parentID int64) error {
	prompt, err := revivePrompt(st, parentID)
	if err != nil {
		return err
	}
	bin := shell3Binary()
	cfgPath := ""
	// Prefer the dormant parent's OWN recorded config so it revives under the
	// same configuration it ran with; fall back to the reviver's runtime config
	// only when the parent has none recorded.
	if st != nil {
		if p, e := st.SessionConfigPath(parentID); e == nil {
			cfgPath = p
		}
	}
	if cfgPath == "" && s.runtime != nil {
		if p, e := s.runtime.ConfigPath(); e == nil {
			cfgPath = p
		}
	}
	argv := []string{
		bin, "run",
		"--resume", fmt.Sprintf("%d", parentID),
		"--prompt", prompt,
	}
	if cfgPath != "" {
		argv = append(argv, "--config", cfgPath)
	}
	_, err = bgjobs.Start(jobstore.New(st), argv, "revive session "+fmt.Sprintf("%d", parentID),
		s.cfg.WorkDir, nil)
	return err
}

// startTransport opens this session's socket listener and marks it live in the
// store registry. A session with no store id, no workdir, or no store skips the
// transport.
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
