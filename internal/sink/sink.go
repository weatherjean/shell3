// Package sink is the producer side of shell3's per-session notification
// channel — a per-session append-only JSONL file under
// <workdir>/.shell3/sink/<session>.jsonl (see internal/paths.SinkPath).
//
// A sink carries short *pointer* notifications, never full payloads: a
// background job exited, a subagent finished, etc. Heavy output (logs,
// transcripts) stays in files the agent reads itself with bash. The host
// (pkg/shell3.Session) tails the file and injects each new line as a
// system-reminder into the agent's next turn.
//
// Producers run in different goroutines AND different processes (the host's
// bash_bg reaper; a child `shell3 --append-sinkfile`), so Append writes each
// notification as a single line via O_APPEND with one Write call. Lines are
// small pointers — always under PIPE_BUF — so an O_APPEND write of a whole
// line is atomic on POSIX and never interleaves with a concurrent producer's
// line. No flock is needed (it would only matter for a producer writing a line
// larger than PIPE_BUF, which this package never does).
package sink

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Notification is one pointer record appended to a sink. Every field except
// Kind and TS is omitempty: a given Kind populates only the fields it needs
// (bg_done uses ID/Exit/Log/Cmd; agent_done uses ID/Status/Transcript/Preview).
// The shape is the wire contract the host watcher decodes and later phases
// (subagent self-report) also write to — keep it stable.
type Notification struct {
	// Kind discriminates the notification, e.g. "bg_done", "agent_done".
	Kind string `json:"kind"`
	// ID is the producer's id for the thing that finished (a bg job id like
	// "bg_9c", or a subagent id like "a3f").
	ID string `json:"id,omitempty"`
	// Status is a coarse outcome label, e.g. "ok"/"error" for a subagent.
	Status string `json:"status,omitempty"`
	// Exit is a process exit code (bg_done). A pointer so a genuine 0 is
	// distinguishable from "no exit code" (omitempty drops a nil, keeps a 0).
	Exit *int `json:"exit,omitempty"`
	// Log is the path to the full output log the agent can cat (bg_done).
	Log string `json:"log,omitempty"`
	// Transcript is the path to the full JSONL transcript (agent_done).
	Transcript string `json:"transcript,omitempty"`
	// Preview is a short human-readable result preview (agent_done).
	Preview string `json:"preview,omitempty"`
	// Cmd is the command that produced this notification (bg_done).
	Cmd string `json:"cmd,omitempty"`
	// TS is the RFC3339Nano UTC timestamp the line was appended. Append stamps
	// it when zero so producers need not set it.
	TS string `json:"ts"`
}

// Append marshals n to a single JSON line and appends it to sinkPath. The
// parent directory is created if missing (the sink file is created lazily on
// the first append). TS is stamped to the current UTC time in RFC3339Nano when
// the caller left it empty.
//
// The whole line — JSON plus its trailing newline — is written in one Write
// call on a file opened O_APPEND, so concurrent producers (other goroutines or
// processes) never interleave partial lines and the host watcher only ever
// sees complete, newline-terminated records. A zero-length sinkPath is a no-op
// (returns nil): callers thread "" to mean "no sink configured".
func Append(sinkPath string, n Notification) error {
	if sinkPath == "" {
		return nil
	}
	if n.TS == "" {
		n.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}
	data, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("sink: marshal notification: %w", err)
	}
	// Build the line (JSON + '\n') and write it in a single call so the append
	// is atomic; a separate write for the newline could interleave with another
	// producer's line.
	line := append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(sinkPath), 0o755); err != nil {
		return fmt.Errorf("sink: mkdir: %w", err)
	}
	f, err := os.OpenFile(sinkPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("sink: open: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("sink: write: %w", err)
	}
	return nil
}
