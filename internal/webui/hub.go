//go:build unix

package webui

import (
	"encoding/json"
	"sync"
)

// hub fans server-pushed events out to every open /api/events stream. Events
// that happen while nobody is watching are dropped, not queued: the browser
// re-reads durable state (jobs, runs) on reconnect, and a stale "job finished"
// toast an hour later is worse than none.
//
// The one exception is an approval request, which blocks a turn until it is
// answered — asks are held in the asks registry and replayed to every new
// subscriber, so closing the tab never strands a parked turn.
type hub struct {
	mu   sync.Mutex
	subs map[int]chan sseEvent
	next int
}

// sseEvent is one `event:`/`data:` pair on the wire.
type sseEvent struct {
	Name string
	Data []byte
}

func newHub() *hub {
	return &hub{subs: make(map[int]chan sseEvent)}
}

// subscribe registers a listener and returns its channel plus a cancel func.
// The channel is buffered; a subscriber that falls behind loses events rather
// than stalling the publisher (which may be a job-runtime goroutine).
func (h *hub) subscribe() (<-chan sseEvent, func()) {
	ch := make(chan sseEvent, 32)
	h.mu.Lock()
	id := h.next
	h.next++
	h.subs[id] = ch
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if sub, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(sub)
		}
	}
}

// publish marshals payload and delivers it to every current subscriber.
func (h *hub) publish(name string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	ev := sseEvent{Name: name, Data: data}

	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sub := range h.subs {
		select {
		case sub <- ev:
		default: // slow reader; drop rather than block the publisher
		}
	}
}

// watchers reports how many streams are open. Used to decide whether an
// approval request can reach a human at all.
func (h *hub) watchers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
