//go:build unix

package web

import (
	"context"
	"sort"
	"strconv"
	"sync"

	"github.com/weatherjean/shell3/internal/shell3"
)

// Driver runs the conversation loop for the standalone web front-end (shell3
// web): it starts turns for POST /api/send (interjecting when one is already
// running), runs queued wake turns (subagent/cron/bg completions), and parks
// on_tool_call asks for the browser to answer. Replies need no delivery path —
// they land in session history, which the page polls. It mirrors
// telegram.Bot's turn slot; extracting a shared driver is deliberate future
// work.
type Driver struct {
	rt   *shell3.Runtime
	sess *shell3.Session

	// baseCtx parents every turn: cancelling it (host shutdown) cancels any
	// in-flight turn. Fixed at construction.
	baseCtx context.Context

	mu         sync.Mutex         // guards cancelTurn + turnActive
	cancelTurn context.CancelFunc // non-nil while a turn runs
	turnActive bool

	askMu  sync.Mutex
	asks   map[string]*pendingAsk
	askSeq int // monotonic id source

	// runJob fires a cron job by name (nil if no scheduler); reload performs a
	// full config reload (nil if unset). Guarded by mu: the reload coordinator
	// rewires runJob while HTTP handlers read both.
	runJob func(name string) error
	reload func() (shell3.ReloadResult, error)

	// onUsage, if set, receives each completed turn's token totals (per turn,
	// not accumulated). Wired by the host to the dashboard usage store.
	// Set it before the first turn (i.e. before Run and before serving /api/send);
	// it is read from turn goroutines without further synchronization.
	onUsage func(prompt, completion, total int)
}

// Ask is one pending on_tool_call approval, waiting for POST /api/ask.
type Ask struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Reason  string `json:"reason"`
}

type pendingAsk struct {
	Ask
	ch chan bool
}

// NewDriver wires a Driver. ctx parents every turn (nil → Background). sess
// must be the runtime's persistent "web" session; rt and sess may be nil only
// for ask-only use (Ask/Asks/Answer — tests), which never calls Run or Send.
func NewDriver(ctx context.Context, rt *shell3.Runtime, sess *shell3.Session) *Driver {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Driver{rt: rt, sess: sess, baseCtx: ctx, asks: make(map[string]*pendingAsk)}
}

// SetUsageRecorder registers a callback invoked with each turn's token totals.
// Call before Run / the first Send.
func (d *Driver) SetUsageRecorder(fn func(prompt, completion, total int)) { d.onUsage = fn }

// Run consumes the runtime's wake bus until ctx is cancelled, running queued
// follow-up turns so subagent/cron/bg completions surface in history. Call it
// on its own goroutine. Wake turns drain synchronously HERE (mirroring
// telegram.Bot.runWakeTurn), so an end-of-turn re-emitted Wake is only
// received after this turn's slot is released — it cannot race the release
// and strand a queued notice.
func (d *Driver) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-d.rt.Events():
			if !ok {
				return
			}
			// HostEvent.Session is keyed on the session's store ID.
			if ev.Session != d.sess.ID() || ev.Kind != shell3.Wake {
				continue
			}
			// If a turn holds the slot, drop the wake: the running turn drains
			// the inbox itself, and its unwind re-emits a Wake while the inbox
			// is non-empty (the same contract telegram.Bot relies on).
			turnCtx, ok := d.takeSlot()
			if !ok {
				continue
			}
			d.drain(d.sess.RunQueued(turnCtx))
			d.releaseSlot()
		}
	}
}

// Send routes one user message: interject if a turn is running, else start a
// turn. Asynchronous — the reply lands in session history.
func (d *Driver) Send(text string) {
	turnCtx, ok := d.takeSlot()
	if !ok {
		d.sess.Interject(text) // steer the running turn; never blocks
		return
	}
	ch := d.sess.Send(turnCtx, text)
	go func() {
		d.drain(ch)
		d.releaseSlot()
	}()
}

// takeSlot claims the single turn slot: ok is false when a turn already runs.
func (d *Driver) takeSlot() (turnCtx context.Context, ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.turnActive {
		return nil, false
	}
	turnCtx, d.cancelTurn = context.WithCancel(d.baseCtx)
	d.turnActive = true
	return turnCtx, true
}

// releaseSlot frees the turn slot and its context.
func (d *Driver) releaseSlot() {
	d.mu.Lock()
	cancel := d.cancelTurn
	d.cancelTurn = nil
	d.turnActive = false
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// drain consumes a turn's event channel (channel close = end of turn),
// reporting the Done event's token totals to onUsage.
func (d *Driver) drain(ch <-chan shell3.Event) {
	for ev := range ch {
		if ev.Kind == shell3.Done && d.onUsage != nil {
			d.onUsage(ev.PromptTokens, ev.CompletionTokens, ev.TotalTokens)
		}
	}
}

// Stop cancels the running turn, if any.
func (d *Driver) Stop() {
	d.mu.Lock()
	cancel := d.cancelTurn
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Busy reports whether a turn is running. Background jobs are deliberately
// excluded: the UI maps Busy to the Stop button (which only cancels turns)
// and to its fast-poll cadence, and a job completion wakes a turn that flips
// Busy back on within one poll anyway.
func (d *Driver) Busy() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.turnActive
}

// Ask is the session's Asker: it parks the approval for the browser and
// blocks until Answer or ctx cancellation. Fail-safe: a cancelled turn denies.
func (d *Driver) Ask(ctx context.Context, command, reason string) bool {
	d.askMu.Lock()
	d.askSeq++
	id := strconv.Itoa(d.askSeq)
	p := &pendingAsk{Ask: Ask{ID: id, Command: command, Reason: reason}, ch: make(chan bool, 1)}
	d.asks[id] = p
	d.askMu.Unlock()
	defer func() {
		d.askMu.Lock()
		delete(d.asks, id)
		d.askMu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return false
	case yes := <-p.ch:
		return yes
	}
}

// Asks snapshots the pending approvals, oldest first.
func (d *Driver) Asks() []Ask {
	d.askMu.Lock()
	out := make([]Ask, 0, len(d.asks))
	for _, p := range d.asks {
		out = append(out, p.Ask)
	}
	d.askMu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		a, _ := strconv.Atoi(out[i].ID)
		b, _ := strconv.Atoi(out[j].ID)
		return a < b
	})
	return out
}

// Answer resolves one pending ask. An unknown or already-answered id is a
// harmless no-op (double-click safe).
func (d *Driver) Answer(id string, allow bool) {
	d.askMu.Lock()
	p := d.asks[id]
	d.askMu.Unlock()
	if p != nil {
		select {
		case p.ch <- allow:
		default:
		}
	}
}
