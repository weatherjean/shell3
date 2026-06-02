// Package web serves an interactive browser frontend for a single long-lived
// shell3 chat session. The Hub fans the session's event stream out to all
// connected browsers and serializes turn execution; the Server exposes it over
// HTTP (SSE for events, POST for input). It depends only on pkg/chat.
package web

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/weatherjean/shell3/pkg/chat"
)

// ErrBusy is returned by Submit when a turn is already running.
var ErrBusy = errors.New("agent busy")

// subBuffer bounds each subscriber's live channel. A subscriber that falls
// this far behind is dropped (its browser reconnects and replays).
const subBuffer = 256

type subscriber struct {
	ch chan chat.Event
}

// Hub owns the shared session's event fan-out and turn lifecycle. The zero
// value is not usable; construct with NewHub. All methods are safe for
// concurrent use.
type Hub struct {
	sess *chat.Session
	run  func(ctx context.Context, input string) // blocks until the turn completes

	mu     sync.Mutex
	log    []chat.Event
	subs   map[*subscriber]struct{}
	busy   bool
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewHub builds a Hub for sess. run drives one turn to completion (typically a
// closure over sess.Run with a prepared TurnConfig); the Hub owns the per-turn
// context so Cancel works.
func NewHub(sess *chat.Session, run func(ctx context.Context, input string)) *Hub {
	return &Hub{sess: sess, run: run, subs: make(map[*subscriber]struct{})}
}

// Start launches the drain goroutine, which appends every session event to the
// replay log and broadcasts it to subscribers until the event channel closes.
func (h *Hub) Start() {
	go func() {
		for ev := range h.sess.Events() {
			h.broadcast(ev)
		}
	}()
}

func (h *Hub) broadcast(ev chat.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.log = append(h.log, ev)
	for s := range h.subs {
		select {
		case s.ch <- ev:
		default:
			close(s.ch)
			delete(h.subs, s)
		}
	}
}

// Subscribe registers a client, returning a snapshot of the replay log and a
// channel of subsequent live events captured atomically (no missed/duplicated
// event between the two). unsub deregisters and is safe to call once.
func (h *Hub) Subscribe() (replay []chat.Event, ch <-chan chat.Event, unsub func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	replay = make([]chat.Event, len(h.log))
	copy(replay, h.log)
	s := &subscriber{ch: make(chan chat.Event, subBuffer)}
	h.subs[s] = struct{}{}
	var once sync.Once
	unsub = func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if _, ok := h.subs[s]; ok {
				close(s.ch)
				delete(h.subs, s)
			}
		})
	}
	return replay, s.ch, unsub
}

// Submit starts a turn for input. Returns ErrBusy if a turn is in flight.
func (h *Hub) Submit(input string) error {
	h.mu.Lock()
	if h.busy {
		h.mu.Unlock()
		return ErrBusy
	}
	h.busy = true
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.wg.Add(1) // under the lock: Close() must never observe a 0 count between unlock and Add
	h.mu.Unlock()

	go func() {
		defer func() {
			cancel()
			h.mu.Lock()
			h.busy = false
			h.cancel = nil
			h.mu.Unlock()
			h.wg.Done()
		}()
		h.run(ctx, input)
	}()
	return nil
}

// Busy reports whether a turn is currently running.
func (h *Hub) Busy() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.busy
}

// Cancel aborts the in-flight turn, if any.
func (h *Hub) Cancel() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cancel != nil {
		h.cancel()
	}
}

// Close cancels any in-flight turn and waits for its goroutine to finish, so
// the caller can safely tear the session down (End + CloseEvents) with no
// goroutine still emitting events. Safe to call once.
func (h *Hub) Close() {
	h.Cancel()
	h.wg.Wait()
}

// Clear resets the conversation (sess.SetMessages(nil)), empties the replay
// log, and broadcasts a session-reset marker (EventSessionStart with
// meta.reset="true") so connected UIs clear their scrollback. Returns ErrBusy
// if a turn is in flight: clearing mid-turn would race the turn goroutine's
// reads of the session history, so callers must cancel (or wait) first.
func (h *Hub) Clear() error {
	marker := chat.Event{
		Kind: chat.EventSessionStart,
		Time: time.Now().UTC(),
		Meta: map[string]string{"reset": "true"},
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.busy {
		return ErrBusy
	}
	// busy == false under the lock guarantees no turn goroutine is touching the
	// session, so SetMessages here cannot race RunTurn's reads.
	h.sess.SetMessages(nil)
	h.log = []chat.Event{marker}
	for s := range h.subs {
		select {
		case s.ch <- marker:
		default:
			close(s.ch)
			delete(h.subs, s)
		}
	}
	return nil
}
