package runs

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fsnotify/fsnotify"
)

const inboxMaxLine = 4096 // PIPE_BUF; single write() <= this is atomic on local fs.

// Pointer is one completion notification appended to inbox.jsonl. It carries no
// payload — only where to look. The run's own jsonl holds the detail.
type Pointer struct {
	TS      string `json:"ts"`
	RunID   string `json:"run_id"`
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Summary string `json:"summary"`
	Exit    *int   `json:"exit,omitempty"`
}

func (s *Store) inboxPath() string       { return filepath.Join(s.root, "inbox.jsonl") }
func (s *Store) inboxOffsetPath() string { return filepath.Join(s.root, "inbox.jsonl.offset") }

// readPersistedOffset reads the byte offset from the sidecar file.
// Returns 0 if the sidecar does not exist (first run).
func (s *Store) readPersistedOffset() int64 {
	b, err := os.ReadFile(s.inboxOffsetPath())
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// persistOffset writes the current byte offset to the sidecar file atomically.
func (s *Store) persistOffset(offset int64) {
	b := []byte(strconv.FormatInt(offset, 10) + "\n")
	tmp := s.inboxOffsetPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, s.inboxOffsetPath())
}

// AppendInbox appends one pointer to THIS store's own inbox.jsonl. See
// AppendPointer for the atomicity contract.
func (s *Store) AppendInbox(p Pointer) error { return AppendPointer(s.inboxPath(), p) }

// AppendPointer appends one pointer as a single atomic line to the inbox.jsonl
// at inboxPath, creating the file (and its parent dir) if needed. Rejects lines
// that would exceed inboxMaxLine, since only sub-PIPE_BUF writes are guaranteed
// atomic. Used both for a store's own inbox and for a subagent reporting UP to
// its parent's inbox (the `shell3 run --inbox <path>` path), which is why it
// takes an absolute path rather than being bound to a Store's root.
func AppendPointer(inboxPath string, p Pointer) error {
	b, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("runs: marshal pointer: %w", err)
	}
	b = append(b, '\n')
	if len(b) > inboxMaxLine {
		return fmt.Errorf("runs: inbox line too large (%d > %d); keep summaries short", len(b), inboxMaxLine)
	}
	if err := os.MkdirAll(filepath.Dir(inboxPath), 0o755); err != nil {
		return fmt.Errorf("runs: mkdir inbox dir: %w", err)
	}
	f, err := os.OpenFile(inboxPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("runs: open inbox: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(b); err != nil { // one write() == one atomic append
		return fmt.Errorf("runs: append inbox: %w", err)
	}
	return nil
}

// Watch replays inbox lines not yet delivered (based on persisted offset), then
// streams new ones until ctx is done. Delivery is at-least-once: the offset is
// persisted after each delivery so a clean restart resumes exactly where Watch
// left off, but a crash mid-handler re-delivers the one in-flight line — so
// onPointer must be idempotent. Malformed lines are skipped (offset still
// advances past them). The sidecar inbox.jsonl.offset holds the durable byte
// position; absence means offset 0 (first run).
func (s *Store) Watch(ctx context.Context, onPointer func(Pointer)) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("runs: watcher: %w", err)
	}
	defer w.Close()
	// Watch the dir so we catch inbox.jsonl being created later.
	if err := w.Add(s.root); err != nil {
		return fmt.Errorf("runs: watch %s: %w", s.root, err)
	}

	// Start from the last persisted position so already-delivered pointers are
	// never replayed on restart, but pointers appended while down ARE delivered.
	offset := s.readPersistedOffset()

	drain := func() {
		f, err := os.Open(s.inboxPath())
		if err != nil {
			return
		}
		defer f.Close()
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, inboxMaxLine), inboxMaxLine)
		for sc.Scan() {
			line := sc.Bytes()
			offset += int64(len(line)) + 1 // + newline
			var p Pointer
			if json.Unmarshal(line, &p) == nil {
				onPointer(p)
			}
			// Persist after delivery (including malformed-skip advances) so a
			// crash mid-handler re-delivers only the one in-flight line (at-least-once),
			// while a clean restart never replays already-delivered pointers.
			s.persistOffset(offset)
		}
	}

	drain() // deliver anything appended while watcher was down
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-w.Events:
			if filepath.Clean(ev.Name) == filepath.Clean(s.inboxPath()) {
				drain()
			}
		case _, ok := <-w.Errors:
			// fsnotify surfaces transient errors (e.g. event-queue overflow) here.
			// They are recoverable: a re-drain reads to EOF and catches anything a
			// dropped event would have missed, so we keep watching rather than
			// permanently tearing down notification delivery. A closed Errors
			// channel (watcher gone) ends the loop.
			if !ok {
				return nil
			}
			drain()
		}
	}
}
