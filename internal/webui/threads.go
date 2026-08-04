//go:build unix

package webui

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// threadIndex is the durable record of every conversation the browser has
// started: which shell3 session backs it, what it is called, and whether it
// has been archived.
//
// Backed by an append-only JSONL file (the runs-store convention), replayed
// last-write-wins on load. The in-memory map is authoritative; disk writes are
// best-effort, so a failed append never loses an in-flight thread.
type threadIndex struct {
	path string
	mu   sync.Mutex
	m    map[string]*threadRecord
	f    *os.File
}

// threadRecord is one conversation. Title is empty until the user renames it —
// the interface shows the creation time instead, which says more about an
// untouched thread than a generic label would.
type threadRecord struct {
	ID        string `json:"t"`
	SessionID string `json:"s"`
	Title     string `json:"title,omitempty"`
	// Preview is the opening of the first thing asked in this thread. It names
	// the conversation until the user renames it — what was asked identifies a
	// thread far better than when it started.
	Preview string `json:"preview,omitempty"`
	Created string `json:"created,omitempty"`
	Updated string `json:"updated,omitempty"`
	// Deleted tombstones a thread: the line stays (the log is append-only) but
	// the thread is gone from every listing.
	Deleted bool `json:"deleted,omitempty"`
}

func newThreadIndex(path string) (*threadIndex, error) {
	ti := &threadIndex{path: path, m: make(map[string]*threadRecord)}

	if r, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			var rec threadRecord
			// A malformed line (a crash mid-append) is skipped, matching the
			// torn-tail tolerance below.
			if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.ID == "" {
				continue
			}
			merged := rec
			// Later lines patch earlier ones: a rename writes no session id,
			// so carry forward whatever the previous record knew.
			if prev, ok := ti.m[rec.ID]; ok {
				if merged.SessionID == "" {
					merged.SessionID = prev.SessionID
				}
				if merged.Created == "" {
					merged.Created = prev.Created
				}
				if merged.Preview == "" {
					merged.Preview = prev.Preview
				}
			}
			// Records written before threads carried timestamps still know
			// their session id, whose runs-store prefix is a creation time.
			if merged.Created == "" {
				merged.Created = timeFromSessionID(merged.SessionID)
			}
			if merged.Updated == "" {
				merged.Updated = merged.Created
			}
			ti.m[rec.ID] = &merged
		}
		r.Close()
	} else if !os.IsNotExist(err) {
		return nil, err
	}

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

// timeFromSessionID recovers a creation time from a runs-store session id
// ("20260725T181054.874072000-a308418b"). Returns "" when the id has another
// shape, which just leaves the thread without a timestamp.
func timeFromSessionID(id string) string {
	stamp, _, ok := strings.Cut(id, ".")
	if !ok {
		return ""
	}
	t, err := time.ParseInLocation("20060102T150405", stamp, time.Local)
	if err != nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// appendLocked writes one record. Caller holds ti.mu.
func (ti *threadIndex) appendLocked(rec *threadRecord) {
	if ti.f == nil {
		return
	}
	if b, err := json.Marshal(rec); err == nil {
		// A failed append loses one index line, never the conversation; the
		// thread is rebuilt as unnamed on the next turn.
		_, _ = ti.f.Write(append(b, '\n'))
	}
}

// record maps threadID to sessionID, creating the thread on first sight and
// stamping its last-activity time on every turn. The first preview wins: a
// thread keeps the name of what opened it, not of the last thing said in it.
func (ti *threadIndex) record(threadID, sessionID, preview string) {
	now := time.Now().UTC().Format(time.RFC3339)

	ti.mu.Lock()
	defer ti.mu.Unlock()

	rec, ok := ti.m[threadID]
	if !ok {
		rec = &threadRecord{ID: threadID, Created: now}
		ti.m[threadID] = rec
	}
	// Nothing changed but the clock, and the clock is not worth a line per turn
	// unless the session or a full minute moved.
	unchanged := rec.SessionID == sessionID && rec.Updated != "" &&
		rec.Updated[:len(rec.Updated)-3] == now[:len(now)-3]

	rec.SessionID = sessionID
	rec.Updated = now
	rec.Deleted = false
	if rec.Preview == "" && preview != "" {
		rec.Preview = preview
		unchanged = false // a new preview is worth a line
	}
	if unchanged {
		return
	}
	ti.appendLocked(rec)
}

// lookup returns the session id backing a thread.
func (ti *threadIndex) lookup(threadID string) (string, bool) {
	ti.mu.Lock()
	defer ti.mu.Unlock()
	rec, ok := ti.m[threadID]
	if !ok || rec.Deleted || rec.SessionID == "" {
		return "", false
	}
	return rec.SessionID, true
}

// get returns a copy of one thread's record.
func (ti *threadIndex) get(threadID string) (threadRecord, bool) {
	ti.mu.Lock()
	defer ti.mu.Unlock()
	rec, ok := ti.m[threadID]
	if !ok || rec.Deleted {
		return threadRecord{}, false
	}
	return *rec, true
}

// list returns the live threads, newest activity first.
func (ti *threadIndex) list() []threadRecord {
	ti.mu.Lock()
	defer ti.mu.Unlock()

	out := make([]threadRecord, 0, len(ti.m))
	for _, rec := range ti.m {
		if rec.Deleted {
			continue
		}
		out = append(out, *rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Updated != out[j].Updated {
			return out[i].Updated > out[j].Updated
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// rename renames a thread. An empty name clears it, so the preview names the
// conversation again rather than leaving it nameless.
func (ti *threadIndex) rename(threadID, title string) (threadRecord, bool) {
	ti.mu.Lock()
	defer ti.mu.Unlock()

	rec, ok := ti.m[threadID]
	if !ok || rec.Deleted {
		return threadRecord{}, false
	}
	rec.Title = title
	ti.appendLocked(rec)
	return *rec, true
}

// remove tombstones a thread. The underlying session and its runs dir are left
// alone — the janitor sweeps those on its own schedule.
func (ti *threadIndex) remove(threadID string) bool {
	ti.mu.Lock()
	defer ti.mu.Unlock()

	rec, ok := ti.m[threadID]
	if !ok || rec.Deleted {
		return false
	}
	rec.Deleted = true
	ti.appendLocked(rec)
	return true
}

func (ti *threadIndex) close() error {
	ti.mu.Lock()
	defer ti.mu.Unlock()
	if ti.f == nil {
		return nil
	}
	err := ti.f.Close()
	ti.f = nil
	return err
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

// PruneThreadIndex rewrites the JSONL file at path, dropping every thread
// whose session no longer exists per sessionExists, and reports how many were
// dropped. Runs once at startup as the other half of the runs janitor sweep
// (see runs.Sweep), before newThreadIndex opens the live file.
//
// Rewriting also compacts the log: each surviving thread ends up as one line
// rather than one per update.
func PruneThreadIndex(path string, sessionExists func(id string) bool) (removed int, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	latest := map[string]threadRecord{}
	var order []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var rec threadRecord
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.ID == "" {
			continue
		}
		prev, seen := latest[rec.ID]
		if !seen {
			order = append(order, rec.ID)
		} else {
			if rec.SessionID == "" {
				rec.SessionID = prev.SessionID
			}
			if rec.Created == "" {
				rec.Created = prev.Created
			}
			if rec.Preview == "" {
				rec.Preview = prev.Preview
			}
		}
		latest[rec.ID] = rec
	}

	var kept []threadRecord
	for _, id := range order {
		rec := latest[id]
		if rec.Deleted || (rec.SessionID != "" && !sessionExists(rec.SessionID)) {
			removed++
			continue
		}
		kept = append(kept, rec)
	}
	if removed == 0 && len(kept) == len(order) {
		return 0, nil
	}

	var buf bytes.Buffer
	for _, rec := range kept {
		lb, err := json.Marshal(rec)
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
	return removed, os.Rename(tmp, path)
}
