package shell3

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/sink"
)

// sinkPollInterval is how often the watcher checks the sink file for newly
// appended lines. The sink is a low-traffic pointer channel (a line per
// background-job / subagent completion), so a quarter-second poll is plenty
// responsive while staying dependency-free (no fsnotify) and cheap. It is a
// var so a test can shorten it.
var sinkPollInterval = 250 * time.Millisecond

// sinkPath returns this session's notification sink path, or "" when the
// session has no workdir or name (then bash_bg appends nowhere and the watcher
// is never started). It is the single place the path is derived, so the
// producer (turnConfig → bash_bg) and consumer (startSinkWatcher) always agree.
func (s *Session) sinkPath() string {
	if s.cfg.WorkDir == "" || s.name == "" {
		return ""
	}
	return paths.SinkPath(s.cfg.WorkDir, s.name)
}

// startSinkWatcher launches the host-side consumer of this session's sink: a
// goroutine that tails the sink file by byte offset and, for each newly
// appended complete JSON line, injects a short POINTER notification into the
// session (Interject) and Wakes the session if it is idle. The injected message
// names the artifact (a log / transcript path); the agent reads the heavy
// content itself with bash, so the agent's context stays small.
//
// rt is captured up front (not read from s.runtime inside the goroutine): a
// concurrent Close nils s.runtime, which would race a read here. The goroutine stops when stop is
// closed (on Close); Close also removes the sink file. A nil sinkPath ("" from
// sinkPath) skips the watcher entirely.
func (s *Session) startSinkWatcher(rt *Runtime, sinkPath string) {
	if sinkPath == "" || rt == nil {
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	s.mu.Lock()
	s.sinkStop = stop
	s.sinkDone = done
	s.sinkFile = sinkPath
	s.mu.Unlock()
	go func() {
		defer close(done)
		s.tailSink(rt, sinkPath, stop)
	}()
}

// stopSinkWatcher signals the watcher goroutine to stop, waits for it to exit,
// and removes the sink file. Called from doClose. Safe to call when no watcher
// was started (the handles are nil). Mirrors the sinkCleanup ordering: the
// goroutine is joined before the file is removed so no late append/read races
// the unlink.
func (s *Session) stopSinkWatcher() {
	s.mu.Lock()
	stop, done, file := s.sinkStop, s.sinkDone, s.sinkFile
	s.sinkStop, s.sinkDone = nil, nil
	s.mu.Unlock()
	if stop != nil {
		close(stop)
	}
	if done != nil {
		<-done
	}
	if file != "" {
		// Best-effort: a missing file (never written to) is not an error.
		_ = os.Remove(file)
	}
}

// tailSink polls sinkPath, advancing a byte offset past complete (newline-
// terminated) lines only. A partial trailing line — a producer mid-write — is
// left unconsumed until its newline arrives, so a notification is never decoded
// from half a line. The file is created lazily by the first producer, so a
// not-exist error is treated as "nothing yet" and retried on the next tick.
func (s *Session) tailSink(rt *Runtime, sinkPath string, stop <-chan struct{}) {
	var offset int64
	for {
		offset = s.drainSink(rt, sinkPath, offset)
		select {
		case <-stop:
			// Final drain so a notification appended just before Close isn't lost
			// to a race between the producer's write and the stop signal.
			s.drainSink(rt, sinkPath, offset)
			return
		case <-time.After(sinkPollInterval):
		}
	}
}

// drainSink reads sinkPath from offset, delivers every complete line found, and
// returns the new offset (advanced past only the complete lines consumed). A
// trailing partial line leaves the offset before it so it is re-read whole on
// the next tick.
func (s *Session) drainSink(rt *Runtime, sinkPath string, offset int64) int64 {
	// The sink is a small pointer file; read it whole and slice from the offset
	// rather than holding an open handle across polls (simpler, and robust to a
	// producer creating the file lazily). A not-exist / transient error means
	// "nothing yet" — return the offset unchanged and retry next tick.
	data, err := os.ReadFile(sinkPath)
	if err != nil {
		return offset
	}
	if int64(len(data)) <= offset {
		return offset
	}
	rest := data[offset:]
	// Only consume up to the last newline; bytes after it are a partial line
	// still being written and must wait for their terminator.
	nl := bytes.LastIndexByte(rest, '\n')
	if nl < 0 {
		return offset // no complete line yet
	}
	complete := rest[:nl+1]
	for _, line := range bytes.Split(complete, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var n sink.Notification
		if err := json.Unmarshal(line, &n); err != nil {
			continue // skip a malformed line rather than wedging the watcher
		}
		s.deliverNotification(rt, n)
	}
	return offset + int64(len(complete))
}

// deliverNotification injects one sink notification as a short pointer message
// and Wakes the session if idle (Interject + Wake-if-idle). The message points
// at the artifact; it does
// NOT inline the log/transcript contents (the agent cats those itself with
// bash), keeping the agent's context small.
func (s *Session) deliverNotification(rt *Runtime, n sink.Notification) {
	s.sess.Interject(formatNotification(n))
	if !s.isBusy() {
		rt.emit(HostEvent{Session: s.name, Kind: Wake})
	}
}

// formatNotification renders a sink notification as the short pointer string
// injected into the agent's next turn. Each Kind names where the detail lives
// so the agent can read it on demand.
func formatNotification(n sink.Notification) string {
	switch n.Kind {
	case sink.KindBgDone:
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
	case sink.KindAgentDone:
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
		// Unknown / future kinds (e.g. agent_done in a later phase): deliver a
		// generic pointer rather than dropping it, so a producer ahead of the
		// host still gets noticed.
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
