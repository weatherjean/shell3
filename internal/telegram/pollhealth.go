package telegram

import (
	"sync"
	"time"
)

// pollHealthLogEvery throttles repeated-failure logging: after the first
// error of an outage is logged, further errors only produce a summary line
// once per this interval, so a long network outage doesn't flood the log.
const pollHealthLogEvery = time.Minute

// pollQuietRecovery exceeds one long-poll attempt so silence can signal
// recovery even when no inbound update arrives.
const pollQuietRecovery = 5 * time.Minute

// pollHealth records transport outages; the library owns retries.
type pollHealth struct {
	mu      sync.Mutex
	now     func() time.Time // injectable for tests
	fails   int              // errors since the outage began
	started time.Time        // first error of the current outage
	lastErr time.Time        // most recent error of the current outage
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
		h.fails, h.started, h.lastErr, h.lastLog = 1, t, t, t
		return true, 1
	}
	h.fails++
	h.lastErr = t
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

// sweep closes an outage that has gone quiet: no new error for
// pollQuietRecovery. The reported duration ends at the LAST error, not at the
// moment of detection — the transport was healthy again from the moment it
// stopped failing, and dating recovery from the sweep would inflate every
// outage by the quiet window. Returns the same triple as ok, and like ok
// reports a given outage exactly once. Safe to call on a healthy transport.
func (h *pollHealth) sweep() (recovered bool, outage time.Duration, fails int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fails == 0 || h.now().Sub(h.lastErr) < pollQuietRecovery {
		return false, 0, 0
	}
	outage, fails = h.lastErr.Sub(h.started), h.fails
	h.fails = 0
	return true, outage, fails
}
