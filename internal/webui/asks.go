//go:build unix

package webui

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/strutil"
)

// askRecord is one parked approval request, kept until it is answered so a
// browser that connects late (or reconnects) still sees it.
type askRecord struct {
	ID string `json:"id"`
	// Prompt is the hook script's own `ask` text — what it wants the human to
	// see. The raw command never reaches here; the hook decides what to show.
	Prompt string `json:"prompt"`
	Reason string `json:"reason"`
	At     string `json:"at"`

	answer chan bool
}

// asks is the command gate's server side: a hook script asked for a human, the
// agent's turn is parked inside Ask, and the browser answers over
// POST /api/asks/{id}.
//
// Fail-safe throughout: no watcher, a cancelled turn, or a shutdown all deny.
// The runtime treats a denial as "blocked, ask the human first", so denying on
// doubt costs a retry; allowing on doubt runs an ungated command.
type asks struct {
	hub *hub
	// timeout bounds how long a turn parks waiting for an answer. Overridden
	// in tests; askTimeout everywhere else.
	timeout time.Duration

	mu      sync.Mutex
	pending map[string]*askRecord
	seq     int
}

// askTimeout is how long an unanswered prompt holds a turn before it denies.
//
// It exists because the turn parked here holds the single-turn gate: without a
// bound, one prompt nobody answers — a tab left open overnight while a
// background job wakes the agent — wedges every later turn, cron run, and
// completion behind it.
//
// Two minutes, because shell3 mostly runs unattended: a prompt raised at 3am
// should cost two minutes of a night's work, not an hour. Someone who IS at the
// browser gets the bell and a push notification the instant it is raised, so
// two minutes is ample when anyone is actually there. The scaffolded gate never
// asks at all, for the same reason — this is the backstop for gates that do.
const askTimeout = 2 * time.Minute

func newAsks(h *hub) *asks {
	return &asks{hub: h, pending: make(map[string]*askRecord), timeout: askTimeout}
}

// Ask blocks the calling turn until the browser answers, the context ends, or
// askTimeout elapses. It satisfies shell3.SessionOpts.Asker.
func (a *asks) Ask(ctx context.Context, prompt, reason string) bool {
	// With no browser attached there is nobody to approve; deny immediately
	// rather than stalling the turn for the full timeout.
	if a.hub.watchers() == 0 {
		return false
	}

	a.mu.Lock()
	a.seq++
	rec := &askRecord{
		ID:     strconv.Itoa(a.seq),
		Prompt: strutil.Truncate(prompt, 4000),
		Reason: strutil.Truncate(reason, 600),
		At:     time.Now().UTC().Format(time.RFC3339),
		answer: make(chan bool, 1),
	}
	a.pending[rec.ID] = rec
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		delete(a.pending, rec.ID)
		a.mu.Unlock()
		a.hub.publish("ask-resolved", map[string]string{"id": rec.ID})
	}()

	a.hub.publish("ask", rec)

	// Denial on timeout, like every other doubtful case here: the runtime reads
	// a denial as "blocked, ask the human first", so the cost is a retry the
	// operator can approve when they are back.
	timer := time.NewTimer(a.timeout)
	defer timer.Stop()

	select {
	case allow := <-rec.answer:
		return allow
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

// Answer records the browser's verdict. A second answer for the same id is
// dropped (the channel is buffered and never re-read), so a double-click can
// neither panic nor flip a decision.
func (a *asks) Answer(id string, allow bool) bool {
	a.mu.Lock()
	rec, ok := a.pending[id]
	a.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case rec.answer <- allow:
	default:
	}
	return true
}

// snapshot lists the still-parked requests, for replay to a new subscriber.
func (a *asks) snapshot() []*askRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]*askRecord, 0, len(a.pending))
	for _, rec := range a.pending {
		out = append(out, rec)
	}
	return out
}
