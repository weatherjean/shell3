//go:build unix

package telegram

import (
	"strings"
	"sync"

	"github.com/weatherjean/shell3/internal/runs"
)

// SessionIndex persists a room's current conversation before caching it.
// Surface keys isolate transports and rooms; the store closure follows reloads.
// A nil store provides memory-only tracking.
type SessionIndex struct {
	store   func() *runs.Store
	surface string
	mu      sync.Mutex
	id      string
	seen    bool
}

// NewSessionIndex returns the session index for one front-end surface. store
// resolves the CURRENT generation's runs store on every call (nil is fine).
func NewSessionIndex(store func() *runs.Store, surface string) *SessionIndex {
	if store == nil {
		store = func() *runs.Store { return nil }
	}
	return &SessionIndex{store: store, surface: surface}
}

// SetCurrent publishes the marker only after a successful durable write.
func (ti *SessionIndex) SetCurrent(id string) error {
	ti.mu.Lock()
	defer ti.mu.Unlock()
	if st := ti.store(); st != nil {
		if err := st.SetCurrentSession(ti.surface, id); err != nil {
			return err
		}
	}
	ti.id, ti.seen = id, true
	return nil
}

// Current returns the current-conversation session id, if any: the in-memory
// value first, then the store (a marker persisted by an earlier process). An
// empty recorded id (a /new that cleared the marker) reads as absent.
func (ti *SessionIndex) Current() (string, bool) {
	ti.mu.Lock()
	defer ti.mu.Unlock()
	if ti.seen {
		return ti.id, ti.id != ""
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

// roomSurface namespaces an opaque chat ID under its transport.
func roomSurface(host string, chatID string) string {
	return host + ":" + chatID
}

// forSurface derives a sibling index over the same store, keyed on another
// surface. The store closure is shared, so a /reload generation swap is
// picked up by every derived index at once.
func (ti *SessionIndex) forSurface(surface string) *SessionIndex {
	if ti == nil {
		// A Bot built without persistence (library use, tests): rooms still
		// need an index, they just have nothing to survive a restart with.
		return NewSessionIndex(nil, surface)
	}
	return &SessionIndex{store: ti.store, surface: surface}
}

// currentStore resolves the active generation's store.
func (ti *SessionIndex) currentStore() *runs.Store {
	if ti == nil {
		return nil
	}
	return ti.store()
}

// chatIDFromSurface recovers a room ID without interpreting it.
func chatIDFromSurface(host, surface string) (string, bool) {
	rest, ok := strings.CutPrefix(surface, host+":")
	return rest, ok && rest != ""
}

// hostSurface returns the transport namespace, defaulting to Telegram.
func (ti *SessionIndex) hostSurface() string {
	if ti == nil || ti.surface == "" {
		return "telegram"
	}
	// The host constructs its index with a bare surface; a room index derived
	// from it carries "<host>:<chatid>", so strip anything after the colon.
	host, _, _ := strings.Cut(ti.surface, ":")
	return host
}
