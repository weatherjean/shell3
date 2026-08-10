//go:build unix

package telegram

import (
	"sync"

	"github.com/weatherjean/shell3/internal/runs"
)

// ThreadIndex is a persistent map from transport message id to session id.
// The in-memory map is authoritative for the process; the runs store's
// threads table carries it across restarts. The store is resolved per call
// (a /reload swaps generations, closing the old database handle), and
// writes to it are best-effort — a failed write loses one index entry,
// never the conversation (the reply simply starts a fresh thread next
// time). A nil store degrades to memory-only, so a runtime without
// persistence still threads within its own lifetime. surface namespaces
// the front-ends ("telegram", "serve") so two transports' ids never
// cross-resolve.
type ThreadIndex struct {
	store   func() *runs.Store
	surface string
	mu      sync.Mutex
	m       map[string]string
}

// NewThreadIndex returns the thread index for one front-end surface. store
// resolves the CURRENT generation's runs store on every call (nil is fine).
func NewThreadIndex(store func() *runs.Store, surface string) *ThreadIndex {
	if store == nil {
		store = func() *runs.Store { return nil }
	}
	return &ThreadIndex{store: store, surface: surface, m: make(map[string]string)}
}

// Record maps msgID to sessionID.
func (ti *ThreadIndex) Record(msgID, sessionID string) {
	ti.mu.Lock()
	ti.m[msgID] = sessionID
	ti.mu.Unlock()
	if st := ti.store(); st != nil {
		_ = st.ThreadRecord(ti.surface, msgID, sessionID)
	}
}

// Any reports whether ANY conversation exists on this surface — in memory or
// persisted by an earlier process. The thread-choice ask keys off it: with no
// history there is nothing the user could have meant to continue.
func (ti *ThreadIndex) Any() bool {
	ti.mu.Lock()
	n := len(ti.m)
	ti.mu.Unlock()
	if n > 0 {
		return true
	}
	st := ti.store()
	return st != nil && st.ThreadAny(ti.surface)
}

// Lookup returns the session id recorded for msgID, if any: the in-memory
// map first, then the store (entries persisted by an earlier process).
func (ti *ThreadIndex) Lookup(msgID string) (string, bool) {
	ti.mu.Lock()
	s, ok := ti.m[msgID]
	ti.mu.Unlock()
	if ok {
		return s, true
	}
	st := ti.store()
	if st == nil {
		return "", false
	}
	return st.ThreadLookup(ti.surface, msgID)
}
