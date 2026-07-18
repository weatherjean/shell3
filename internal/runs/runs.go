// Package runs is the file-native store: per-project JSONL under .shell3_project/.
package runs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
)

// Meta is the per-session metadata written to runs/<id>/meta.json.
type Meta struct {
	ID        string    `json:"id"`
	Workdir   string    `json:"workdir"`
	ConfigDir string    `json:"config_dir"`
	Model     string    `json:"model"`
	Status    string    `json:"status"` // "live" | "ended"
	ParentID  string    `json:"parent_id,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
	LastAt    time.Time `json:"last_at"`
}

// Store is rooted at a project's .shell3_project/ directory.
type Store struct {
	root string

	// touchMu guards lastTouch, the per-session debounce for AppendMessage's
	// LastAt bump (see touchDebounce).
	touchMu   sync.Mutex
	lastTouch map[string]time.Time
}

// touchDebounce bounds how often AppendMessage rewrites meta.json for a
// LastAt bump. Recency sorting doesn't need sub-second precision.
const touchDebounce = time.Second

// Open ensures root/runs/ exists and returns a Store. root is the
// .shell3_project/ directory (not the repo root).
func Open(root string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(root, "runs"), 0o755); err != nil {
		return nil, fmt.Errorf("runs: open %s: %w", root, err)
	}
	return &Store{root: root, lastTouch: map[string]time.Time{}}, nil
}

func (s *Store) runsDir() string { return filepath.Join(s.root, "runs") }

// sessDir resolves a session directory. IDs arrive from user-controlled
// surfaces (the dashboard, shell3 dev --resume), so anything that is not a plain
// path component is rejected by mapping it to an impossible directory —
// "../../../etc" must never escape the store.
func (s *Store) sessDir(id string) string {
	// filepath.Base is a no-op on "." and "..", so they need their own check
	// — ".." would otherwise resolve to the store root's parent.
	if id == "" || id == "." || id == ".." || id != filepath.Base(id) {
		return filepath.Join(s.runsDir(), "invalid-session-id")
	}
	return filepath.Join(s.runsDir(), id)
}

// newID is a sortable wall-clock timestamp plus a random suffix. The suffix
// prevents collisions between sessions minted within the same nanosecond by
// concurrent subagent processes, which would otherwise share a runs/<id>/ dir
// and clobber each other's meta.json.
func newID() string {
	var b [4]byte
	_, _ = rand.Read(b[:]) // on the astronomically unlikely error, fall back to timestamp-only
	return time.Now().UTC().Format("20060102T150405.000000000") + "-" + hex.EncodeToString(b[:])
}

// NewSession mints an ID, creates runs/<id>/, writes meta.json, returns the ID.
func (s *Store) NewSession(m Meta) (string, error) {
	id := newID()
	dir := s.sessDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("runs: new session: %w", err)
	}
	now := time.Now().UTC()
	m.ID, m.Status, m.StartedAt, m.LastAt = id, "live", now, now
	if err := s.writeMeta(m); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) writeMeta(m Meta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("runs: marshal meta: %w", err)
	}
	// Atomic replace so a concurrent ListSessions never reads a half-written file.
	tmp := filepath.Join(s.sessDir(m.ID), "meta.json.tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("runs: write meta: %w", err)
	}
	return os.Rename(tmp, filepath.Join(s.sessDir(m.ID), "meta.json"))
}

func (s *Store) readMeta(id string) (Meta, error) {
	var m Meta
	b, err := os.ReadFile(filepath.Join(s.sessDir(id), "meta.json"))
	if err != nil {
		return m, fmt.Errorf("runs: read meta %s: %w", id, err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("runs: decode meta %s: %w", id, err)
	}
	return m, nil
}

// AppendMessage appends one JSON-encoded message line to runs/<id>/messages.jsonl.
// LastAt is bumped at most once per touchDebounce: this is the hot per-turn
// write path, and a full meta read+marshal+tmp-write+rename per message would
// quadruple its file ops for a recency stamp nobody reads at sub-second
// resolution.
func (s *Store) AppendMessage(id string, m llm.Message) error {
	if err := appendLine(s.messagesPath(id), "message", m); err != nil {
		return err
	}
	s.touchMu.Lock()
	last := s.lastTouch[id]
	now := time.Now()
	if now.Sub(last) < touchDebounce {
		s.touchMu.Unlock()
		return nil
	}
	s.lastTouch[id] = now
	s.touchMu.Unlock()
	return s.TouchSession(id)
}

// LoadMessages reads runs/<id>/messages.jsonl in order. Interior lines are
// decoded strictly (corruption there is a real fault worth surfacing), but a
// malformed FINAL line is tolerated: a crash mid-append leaves a half-written
// tail, and failing the whole load would make the session unresumable —
// exactly when resume matters most.
func (s *Store) LoadMessages(id string) ([]llm.Message, error) {
	b, err := os.ReadFile(s.messagesPath(id))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("runs: load messages %s: %w", id, err)
	}
	out, err := decodeLinesTolerantTail[llm.Message](string(b))
	if err != nil {
		return nil, fmt.Errorf("runs: decode message in %s: %w", id, err)
	}
	return out, nil
}

// EndSession marks the session ended.
func (s *Store) EndSession(id string) error {
	m, err := s.readMeta(id)
	if err != nil {
		return err
	}
	m.Status, m.EndedAt, m.LastAt = "ended", time.Now().UTC(), time.Now().UTC()
	return s.writeMeta(m)
}

// TouchSession bumps LastAt.
func (s *Store) TouchSession(id string) error {
	m, err := s.readMeta(id)
	if err != nil {
		return err
	}
	m.LastAt = time.Now().UTC()
	return s.writeMeta(m)
}

// ListSessions returns metas newest-first (by ID, which sorts chronologically).
func (s *Store) ListSessions(limit int) ([]Meta, error) {
	ents, err := os.ReadDir(s.runsDir())
	if err != nil {
		return nil, fmt.Errorf("runs: list: %w", err)
	}
	var ids []string
	for _, e := range ents {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	var out []Meta
	for _, id := range ids {
		if limit > 0 && len(out) >= limit {
			break
		}
		// A session whose meta.json is missing or corrupt is skipped by design:
		// listing must stay best-effort over a store the user can freely edit
		// or delete (runs data is disposable). It becomes invisible here — and
		// therefore unresumable via resume-latest — until the dir is removed.
		if m, err := s.readMeta(id); err == nil {
			out = append(out, m)
		}
	}
	return out, nil
}

// ReminderLine is one persisted system-reminder, anchored to the message index
// it precedes (mirrors chat.ReminderRecord) for faithful session replay.
type ReminderLine struct {
	Seq  int    `json:"seq"`
	Text string `json:"text"`
}

func (s *Store) remindersPath(id string) string {
	return filepath.Join(s.sessDir(id), "reminders.jsonl")
}

func (s *Store) messagesPath(id string) string {
	return filepath.Join(s.sessDir(id), "messages.jsonl")
}

// AppendReminder appends one reminder as a JSON line to runs/<id>/reminders.jsonl.
func (s *Store) AppendReminder(id string, seq int, text string) error {
	return appendLine(s.remindersPath(id), "reminder", ReminderLine{Seq: seq, Text: text})
}

// LoadReminders reads runs/<id>/reminders.jsonl in order. Missing file → (nil,nil).
// Malformed lines are skipped, never fatal.
func (s *Store) LoadReminders(id string) ([]ReminderLine, error) {
	b, err := os.ReadFile(s.remindersPath(id))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("runs: load reminders %s: %w", id, err)
	}
	return decodeLines[ReminderLine](string(b)), nil
}

// TruncateReminders removes the sidecar (used by /clear, /rollback).
func (s *Store) TruncateReminders(id string) error {
	if err := os.Remove(s.remindersPath(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("runs: truncate reminders: %w", err)
	}
	return nil
}

// Transcript returns the raw contents of runs/<id>/messages.jsonl, or ""
// when the file is absent or unreadable. Used by jobManager.transcript to
// surface the child session's persisted message log after completion.
func (s *Store) Transcript(id string) string {
	b, err := os.ReadFile(s.messagesPath(id))
	if err != nil {
		return ""
	}
	return string(b)
}

// LatestSession returns the newest session ID matching workdir+configDir.
func (s *Store) LatestSession(workdir, configDir string) (string, bool, error) {
	metas, err := s.ListSessions(0)
	if err != nil {
		return "", false, err
	}
	for _, m := range metas {
		if m.Workdir == workdir && m.ConfigDir == configDir {
			return m.ID, true, nil
		}
	}
	return "", false, nil
}
