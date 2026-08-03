//go:build unix

package telegram

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"sync"
)

// ThreadIndex is a persistent map from transport message id to session id,
// backed by an append-only JSONL file (one `{"m":"123","s":"<id>"}` line per
// record, the runs-store convention). The in-memory map is authoritative;
// disk writes are best-effort — a write error is silently ignored and the
// map is always updated, so a failed append never loses an in-flight thread.
type ThreadIndex struct {
	path string
	mu   sync.Mutex
	m    map[string]string
	f    *os.File
}

type threadLine struct {
	M string `json:"m"`
	S string `json:"s"`
}

// NewThreadIndex loads any existing JSONL at path (tolerating a torn final
// line left by a crash mid-append) and keeps the file open for O_APPEND
// writes.
func NewThreadIndex(path string) (*ThreadIndex, error) {
	ti := &ThreadIndex{path: path, m: make(map[string]string)}

	// Read existing content, skipping any malformed line (a torn final
	// fragment decodes to an error and is dropped).
	if r, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			var l threadLine
			if json.Unmarshal(sc.Bytes(), &l) == nil && l.S != "" && l.M != "" {
				ti.m[l.M] = l.S
			}
		}
		r.Close()
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// Heal a crash-left partial tail so O_APPEND lands on a clean record
	// boundary — otherwise the next line would fuse onto the torn fragment.
	if err := healTornTail(path); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	ti.f = f
	return ti, nil
}

// Record maps msgID to sessionID, appending one JSONL line (best-effort) and
// updating the in-memory map (always).
func (ti *ThreadIndex) Record(msgID, sessionID string) {
	ti.mu.Lock()
	defer ti.mu.Unlock()
	ti.m[msgID] = sessionID
	if ti.f == nil {
		return
	}
	b, err := json.Marshal(threadLine{M: msgID, S: sessionID})
	if err != nil {
		return
	}
	// A failed append loses one index line, never the conversation; the reply
	// simply starts a fresh thread next time.
	_, _ = ti.f.Write(append(b, '\n'))
}

// healTornTail truncates an unterminated final line (a crash-left partial
// append) back to the last complete record, so a following O_APPEND write
// starts on a clean boundary. A file already ending in a newline is untouched.
func healTornTail(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return nil
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	data := make([]byte, info.Size())
	if _, err := f.ReadAt(data, 0); err != nil {
		return err
	}
	return f.Truncate(int64(bytes.LastIndexByte(data, '\n') + 1))
}

// PruneThreadIndex rewrites the JSONL file at path, dropping every entry
// whose session no longer exists per sessionExists, and reports how many
// were dropped. A standalone function rather than a *ThreadIndex method: it
// runs once at `shell3 telegram` startup, before NewThreadIndex opens the
// live file for the process, as the other half of the runs janitor sweep
// (see runs.Sweep) — internal/runs must not import internal/telegram, so the
// two are joined here instead, by the cmd-level glue that already knows both
// the swept-away ids and which sessions still exist on disk. A no-op leaves
// the file untouched (no rewrite, no torn-tail risk) when nothing is dropped.
func PruneThreadIndex(path string, sessionExists func(id string) bool) (removed int, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var kept []threadLine
	for sc.Scan() {
		var l threadLine
		// A malformed/torn line (crash mid-append) is dropped silently here
		// too, matching NewThreadIndex's own tolerance.
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.S == "" {
			continue
		}
		if sessionExists(l.S) {
			kept = append(kept, l)
		} else {
			removed++
		}
	}
	if removed == 0 {
		return 0, nil
	}
	var buf bytes.Buffer
	for _, l := range kept {
		lb, err := json.Marshal(l)
		if err != nil {
			return 0, err
		}
		buf.Write(lb)
		buf.WriteByte('\n')
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return 0, err
	}
	return removed, nil
}

// Lookup returns the session id recorded for msgID, if any.
func (ti *ThreadIndex) Lookup(msgID string) (string, bool) {
	ti.mu.Lock()
	defer ti.mu.Unlock()
	s, ok := ti.m[msgID]
	return s, ok
}
