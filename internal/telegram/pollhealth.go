package telegram

import (
	"sync"
	"time"
)

// pollHealthLogEvery throttles repeated-failure logging: after the first
// error of an outage is logged, further errors only produce a summary line
// once per this interval, so a long network outage doesn't flood the log.
const pollHealthLogEvery = time.Minute

// pollQuietRecovery is how long the transport must go without a new error
// before an open outage is declared over. It exists because the only healthy
// signal the library hands us is an inbound UPDATE (see ok, called from
// onUpdate): a chat nobody talks in produces none, so an outage stayed open
// in the log for hours after the transport came back, and its reported
// duration ran until the next human message rather than until the last
// error. A broken long-poll errors continuously, so silence is the signal.
//
// The value must comfortably exceed one poll attempt (long-poll timeout plus
// the HTTP client's own), or a slow poll would read as recovery. The
// deliberate tradeoff: an INTERMITTENT fault (the live install saw errors
// ~20 minutes apart) now logs as a series of short outage/recovery pairs
// instead of one long outage. That is noisier and true; the previous
// behaviour was quieter and false.
const pollQuietRecovery = 5 * time.Minute

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
