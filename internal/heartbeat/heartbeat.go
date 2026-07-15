//go:build unix

// Package heartbeat implements the shell3.heartbeat{} config block: a
// periodic check-in turn injected into the main session while it is idle. The
// tick prompt carries the configured checklist and the HEARTBEAT_OK
// convention; the host strips the token from replies and stays silent when
// nothing needed attention. Timing is approximate by design — a busy session
// or an out-of-window tick is skipped, not queued (the next tick covers it);
// exact-time work belongs in shell3.cron.
package heartbeat

import (
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
)

// Token is the "nothing needs attention" sentinel the model replies with.
const Token = "HEARTBEAT_OK"

// defaultPreamble is the instruction each tick carries when no prompt
// override is configured. It restates the reply convention every tick so the
// system prompt needs no heartbeat section.
const defaultPreamble = "[heartbeat] Periodic check-in. Work through the checklist below with your tools. " +
	"Do not repeat or re-do tasks already handled earlier in this conversation. " +
	"If something needs the user's attention, reply with a concise alert. " +
	"If nothing does, reply exactly " + Token + "."

// Prompt renders the message a tick injects: the preamble (default or the
// configured override) followed by the checklist.
func Prompt(hb shell3.Heartbeat) string {
	pre := hb.Prompt
	if pre == "" {
		pre = defaultPreamble
	}
	return pre + "\n\nChecklist:\n" + strings.TrimSpace(hb.Checklist)
}

// Active reports whether now falls inside the configured daily window. An
// unset window is always active. From is inclusive, to exclusive; from > to
// spans midnight (e.g. 22:00-06:00). The comparison happens in the configured
// zone ("" = host-local); a zone that fails to load falls back to host-local
// rather than silencing the heartbeat (the config validated it at load time).
func Active(hb shell3.Heartbeat, now time.Time) bool {
	if hb.ActiveFrom == "" || hb.ActiveTo == "" {
		return true
	}
	loc := now.Location()
	if hb.TZ != "" {
		if l, err := time.LoadLocation(hb.TZ); err == nil {
			loc = l
		}
	}
	cur := now.In(loc).Format("15:04")
	from, to := hb.ActiveFrom, hb.ActiveTo
	if from <= to {
		return cur >= from && cur < to
	}
	// Overnight window: active late evening OR early morning.
	return cur >= from || cur < to
}

// Strip removes a leading or trailing HEARTBEAT_OK sentinel (bare or wrapped
// in markdown emphasis) from a reply. It returns the cleaned text and whether
// the message should be dropped entirely (nothing but the sentinel remained).
// A mid-sentence token is left alone — only edge positions are an ack.
func Strip(reply string) (string, bool) {
	s := strings.TrimSpace(reply)
	for _, tok := range []string{"**" + Token + "**", "*" + Token + "*", "`" + Token + "`", Token} {
		if rest, ok := strings.CutPrefix(s, tok); ok {
			s = strings.TrimSpace(rest)
			break
		}
		if rest, ok := strings.CutSuffix(s, tok); ok {
			s = strings.TrimSpace(rest)
			break
		}
	}
	return s, s == ""
}

// Ticker fires the heartbeat on its interval: each tick that lands inside the
// active window while the session is idle injects the heartbeat prompt (via
// the inject callback — typically Session.Interject, whose idle-wake prods the
// host to run the turn). Busy or out-of-window ticks are skipped outright.
type Ticker struct {
	hb     shell3.Heartbeat
	inject func(prompt string)
	busy   func() bool
	now    func() time.Time // injectable clock for tests

	mu   sync.Mutex
	stop chan struct{}
}

// NewTicker builds a stopped Ticker. busy reports whether the target session
// is mid-turn or has running background jobs; a nil busy means never busy.
func NewTicker(hb shell3.Heartbeat, inject func(string), busy func() bool) *Ticker {
	if busy == nil {
		busy = func() bool { return false }
	}
	return &Ticker{hb: hb, inject: inject, busy: busy, now: time.Now}
}

// Start arms the interval. Idempotent while running.
func (t *Ticker) Start() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stop != nil {
		return
	}
	stop := make(chan struct{})
	t.stop = stop
	go func() {
		tick := time.NewTicker(t.hb.Every)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				if t.busy() || !Active(t.hb, t.now()) {
					continue
				}
				t.inject(Prompt(t.hb))
			}
		}
	}()
}

// Stop disarms the interval. Idempotent.
func (t *Ticker) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stop != nil {
		close(t.stop)
		t.stop = nil
	}
}
