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

// currentSessionKey is the reserved msg_id under which the surface's ONE
// long-lived conversation records its session id — the marker a restart
// resumes from.
const currentSessionKey = "current-session"

// SetCurrent records id as the surface's current conversation session.
func (ti *ThreadIndex) SetCurrent(id string) { ti.Record(currentSessionKey, id) }

// Current returns the persisted current-conversation session id, if any —
// an empty recorded id (a /new that cleared the marker) reads as absent.
func (ti *ThreadIndex) Current() (string, bool) {
	id, ok := ti.Lookup(currentSessionKey)
	if !ok || id == "" {
		return "", false
	}
	return id, true
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
