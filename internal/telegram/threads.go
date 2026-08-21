//go:build unix

package telegram

import (
	"sync"

	"github.com/weatherjean/shell3/internal/runs"
)

// ThreadIndex remembers one front-end surface's current conversation session.
// The in-memory value is authoritative for the process; the runs store's
// threads table carries it across restarts. The store is resolved per call
// (a /reload swaps generations, closing the old database handle). A nil store
// degrades to memory-only, so a runtime without persistence still tracks the
// conversation within its own lifetime. surface namespaces the front-ends
// ("telegram", "serve") so two transports never cross-resolve.
type ThreadIndex struct {
	store   func() *runs.Store
	surface string
	mu      sync.Mutex
	id      string
}

// NewThreadIndex returns the thread index for one front-end surface. store
// resolves the CURRENT generation's runs store on every call (nil is fine).
func NewThreadIndex(store func() *runs.Store, surface string) *ThreadIndex {
	if store == nil {
		store = func() *runs.Store { return nil }
	}
	return &ThreadIndex{store: store, surface: surface}
}

// SetCurrent records id as the surface's current conversation session.
// A failed write is returned, not swallowed: a stale marker silently forks
// the conversation on the next restart — cron reports land in a session the
// user never sees.
func (ti *ThreadIndex) SetCurrent(id string) error {
	ti.mu.Lock()
	ti.id = id
	ti.mu.Unlock()
	if st := ti.store(); st != nil {
		return st.SetCurrentSession(ti.surface, id)
	}
	return nil
}

// Current returns the current-conversation session id, if any: the in-memory
// value first, then the store (a marker persisted by an earlier process). An
// empty recorded id (a /new that cleared the marker) reads as absent.
func (ti *ThreadIndex) Current() (string, bool) {
	ti.mu.Lock()
	id, seen := ti.id, ti.id != ""
	ti.mu.Unlock()
	if seen {
		return id, true
	}
	st := ti.store()
	if st == nil {
		return "", false
	}
	id, ok := st.CurrentSession(ti.surface)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}
