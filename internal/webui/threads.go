//go:build unix

package webui

import (
	"sort"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/runs"
)

// webSurface namespaces the web front-end's rows in the store's shared
// threads table, so its ids never cross-resolve with telegram's or serve's.
const webSurface = "web"

// threadIndex is the durable record of every conversation the browser has
// started: which shell3 session backs it, what it is called, and whether it
// has been archived.
//
// The in-memory map is authoritative for the process; the runs store carries
// it across restarts (surface "web"). store is resolved per call, not
// captured once, so a /reload that swaps generations never leaves the index
// writing to a closed database handle; a nil store (or a failed store call)
// degrades to memory-only for the rest of the process's life — a lost write
// loses one thread's durability, never the live conversation.
type threadIndex struct {
	store func() *runs.Store
	mu    sync.Mutex
	m     map[string]*threadRecord
}

// threadRecord is one conversation. Title is empty until the user renames it —
// the interface shows the creation time instead, which says more about an
// untouched thread than a generic label would.
type threadRecord struct {
	ID        string
	SessionID string
	Title     string
	// Preview is the opening of the first thing asked in this thread. It names
	// the conversation until the user renames it — what was asked identifies a
	// thread far better than when it started.
	Preview string
	Created string
	Updated string
	// Deleted tombstones a thread: the row stays in the store but the thread
	// is gone from every listing.
	Deleted bool
}

// newThreadIndex builds the thread index for the web front-end, preloading
// whatever the store already knows (a previous process's threads). store may
// be nil (the test harness, a runtime with no config Parts) — that degrades
// to an empty, memory-only index rather than failing.
func newThreadIndex(store func() *runs.Store) *threadIndex {
	if store == nil {
		store = func() *runs.Store { return nil }
	}
	ti := &threadIndex{store: store, m: make(map[string]*threadRecord)}

	st := store()
	if st == nil {
		return ti
	}
	metas, err := st.ThreadListMeta(webSurface)
	if err != nil {
		// A read failure just starts empty; nothing durable is lost, and the
		// next write retries against the store.
		return ti
	}
	for _, m := range metas {
		ti.m[m.ID] = &threadRecord{
			ID: m.ID, SessionID: m.SessionID, Title: m.Title, Preview: m.Preview,
			Created: m.Created, Updated: m.Updated, Deleted: m.Deleted,
		}
	}
	return ti
}

// persistLocked writes one record to the store. Caller holds ti.mu. Best
// effort: a failure here is not surfaced, matching every other front-end's
// thread-index pattern of best-effort persistence.
func (ti *threadIndex) persistLocked(rec *threadRecord) {
	st := ti.store()
	if st == nil {
		return
	}
	_ = st.ThreadUpsertMeta(webSurface, runs.ThreadMeta{
		ID: rec.ID, SessionID: rec.SessionID, Title: rec.Title, Preview: rec.Preview,
		Created: rec.Created, Updated: rec.Updated, Deleted: rec.Deleted,
	})
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
	// Nothing changed but the clock, and the clock is not worth a write per
	// turn unless the session or a full minute moved.
	unchanged := rec.SessionID == sessionID && rec.Updated != "" &&
		rec.Updated[:len(rec.Updated)-3] == now[:len(now)-3]

	rec.SessionID = sessionID
	rec.Updated = now
	rec.Deleted = false
	if rec.Preview == "" && preview != "" {
		rec.Preview = preview
		unchanged = false // a new preview is worth a write
	}
	if unchanged {
		return
	}
	ti.persistLocked(rec)
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
	ti.persistLocked(rec)
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
	ti.persistLocked(rec)
	return true
}
