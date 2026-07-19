package telegram

import (
	"sync"
	"time"
)

// pollHealthLogEvery throttles repeated-failure logging: after the first
// error of an outage is logged, further errors only produce a summary line
// once per this interval, so a long network outage doesn't flood the log.
const pollHealthLogEvery = time.Minute

// pollHealth tracks Telegram transport errors (getUpdates long-poll failures,
// send errors) so outages land in the app log instead of only on stderr —
// the beach-day incident: the bot silently lost api.telegram.org for ~17
// minutes and nothing in shell3.log said why messages weren't arriving.
// The library keeps retrying on its own; this is observability, not recovery.
type pollHealth struct {
	mu      sync.Mutex
	now     func() time.Time // injectable for tests
	fails   int              // errors since the outage began
	started time.Time        // first error of the current outage
	lastLog time.Time        // last time we emitted a line for this outage
}

func newPollHealth() *pollHealth { return &pollHealth{now: time.Now} }

// fail records one transport error. It returns whether to log now and, when
// logging, how many errors the current outage has accumulated.
func (h *pollHealth) fail() (logNow bool, fails int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t := h.now()
	if h.fails == 0 {
		h.fails, h.started, h.lastLog = 1, t, t
		return true, 1
	}
	h.fails++
	if t.Sub(h.lastLog) >= pollHealthLogEvery {
		h.lastLog = t
		return true, h.fails
	}
	return false, h.fails
}

// ok records a healthy sign (an update arrived). If an outage was in
// progress it returns recovered=true with its duration and error count.
func (h *pollHealth) ok() (recovered bool, outage time.Duration, fails int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fails == 0 {
		return false, 0, 0
	}
	outage, fails = h.now().Sub(h.started), h.fails
	h.fails = 0
	return true, outage, fails
}
