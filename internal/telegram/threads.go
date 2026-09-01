//go:build unix

package telegram

import (
	"strconv"
	"strings"
	"sync"

	"github.com/weatherjean/shell3/internal/runs"
)

// ThreadIndex remembers one front-end surface's current conversation session.
// The in-memory value is authoritative for the process; the runs store's
// threads table carries it across restarts. The store is resolved per call
// (a /reload swaps generations, closing the old database handle). A nil store
// degrades to memory-only, so a runtime without persistence still tracks the
// conversation within its own lifetime. surface namespaces the front-end
// ("telegram" is the only one today) so a future transport could never
// cross-resolve another's conversation.
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

// roomSurface keys one room under its FRONT-END's surface namespace. The
// prefix is the host's own surface ("telegram"), so two front-ends sharing a
// runs store could never cross-resolve each other's rooms — the property the
// single-surface keys had, kept now that each surface has many rooms.
func roomSurface(host string, chatID int64) string {
	return host + ":" + strconv.FormatInt(chatID, 10)
}

// forSurface derives a sibling index over the same store, keyed on another
// surface. The store closure is shared, so a /reload generation swap is
// picked up by every derived index at once.
func (ti *ThreadIndex) forSurface(surface string) *ThreadIndex {
	if ti == nil {
		// A Bot built without persistence (library use, tests): rooms still
		// need an index, they just have nothing to survive a restart with.
		return NewThreadIndex(nil, surface)
	}
	return &ThreadIndex{store: ti.store, surface: surface}
}

// currentStore resolves the runs store this index writes to, or nil. The
// closure is re-evaluated per call so a /reload generation swap is picked up
// rather than pinned.
func (ti *ThreadIndex) currentStore() *runs.Store {
	if ti == nil {
		return nil
	}
	return ti.store()
}

// chatIDFromSurface parses a "<host>:<chatid>" surface key back into its chat
// id. It is how a completion whose room is not live yet — every completion
// recovered at boot — finds the room it belongs to.
func chatIDFromSurface(host, surface string) (int64, bool) {
	rest, ok := strings.CutPrefix(surface, host+":")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// hostSurface is the front-end namespace this index belongs to ("telegram")
// — the prefix every room key of that host is built from. A nil
// index (a Bot built without persistence) reports the Telegram default so a
// test-built bot keys its rooms the way the real one does.
func (ti *ThreadIndex) hostSurface() string {
	if ti == nil || ti.surface == "" {
		return "telegram"
	}
	// The host constructs its index with a bare surface; a room index derived
	// from it carries "<host>:<chatid>", so strip anything after the colon.
	host, _, _ := strings.Cut(ti.surface, ":")
	return host
}
