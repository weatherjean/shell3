package agentsetup

import (
	"context"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/chat"
)

// eventHookTimeout bounds one subscriber run. Longer than the gate's budget
// would let a slow observer starve the queue behind it; shorter would fail
// honest work. It is the same 10s the other hooks get, applied per event.
const eventHookTimeout = 10 * time.Second

// eventDrainBudget bounds how long Close spends delivering the backlog. The
// tail of a session is what an audit subscriber most wants, so shutdown waits
// for it — but a wedged hook must not hold the process open, so the wait ends.
const eventDrainBudget = 5 * time.Second

// eventQueueDepth is how many events may wait for a slow subscriber before
// the oldest are dropped. Deep enough to absorb a burst of tool results;
// shallow enough that a wedged hook cannot pin unbounded memory.
const eventQueueDepth = 256

// eventDispatcher delivers session events to a kit `event:` subscriber off the
// emitting goroutine.
//
// The turn must never wait on an observer, so Post NEVER blocks: when the
// queue is full it drops the OLDEST pending event and counts it. Losing the
// tail of a burst degrades the observer's view into gaps, which is recoverable;
// blocking the producer would stall the turn itself, which is not.
type eventDispatcher struct {
	q   chan eventItem
	run func(ctx context.Context, agent, kind string, payload []byte) error
	log applog.Logger

	mu      sync.Mutex
	dropped int

	closeOnce sync.Once
	closed    chan struct{}
	done      chan struct{}
}

type eventItem struct {
	agent, kind string
	payload     []byte
}

func newEventDispatcher(depth int, run func(context.Context, string, string, []byte) error, log applog.Logger) *eventDispatcher {
	d := &eventDispatcher{
		q:      make(chan eventItem, depth),
		run:    run,
		log:    log,
		closed: make(chan struct{}),
		done:   make(chan struct{}),
	}
	go d.serve()
	return d
}

// serve runs subscribers one at a time. Serial by design: a shell function
// appending to a log is not safe to run concurrently with itself, and the
// operator writing one should not have to think about that.
func (d *eventDispatcher) serve() {
	defer close(d.done)
	for {
		// Prefer closure so a ready backlog cannot delay the bounded drain.
		select {
		case <-d.closed:
			d.drain()
			return
		default:
		}
		select {
		case <-d.closed:
			d.drain()
			return
		case it := <-d.q:
			d.deliver(it)
		}
	}
}

// drain delivers whatever is still queued at Close, within a bounded budget.
// The events in flight at shutdown are the ones an audit subscriber most wants
// (the error, the turn that ended it), so dropping them silently would put a
// gap in exactly the wrong place — but a hook that never returns must not hold
// the process open, so the budget ends the wait.
func (d *eventDispatcher) drain() {
	deadline := time.Now().Add(eventDrainBudget)
	for {
		select {
		case it := <-d.q:
			// The budget bounds the WHOLE drain, not each item: a hook that
			// takes the full per-event timeout every time would otherwise
			// stretch shutdown by timeout×backlog.
			remaining := time.Until(deadline)
			if remaining <= 0 {
				d.countDropped(1)
				continue // keep emptying so the counter reflects the real loss
			}
			d.deliverWithin(it, min(remaining, eventHookTimeout))
		default:
			return
		}
	}
}

func (d *eventDispatcher) deliver(it eventItem) { d.deliverWithin(it, eventHookTimeout) }

func (d *eventDispatcher) deliverWithin(it eventItem, budget time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	err := d.run(ctx, it.agent, it.kind, it.payload)
	cancel()
	if err != nil {
		chat.LogOrNoop(d.log).Warn("event hook failed",
			"agent", it.agent, "event", it.kind, "error", err)
	}
}

// Post queues one event. It never blocks and never panics, including after
// Close — a session may emit a final event while the runtime tears down.
func (d *eventDispatcher) Post(agent, kind string, payload []byte) {
	select {
	case <-d.closed:
		return
	default:
	}
	for {
		select {
		case d.q <- eventItem{agent: agent, kind: kind, payload: payload}:
			return
		default:
		}
		// Full: discard the oldest and retry. A concurrent worker take may
		// win the race, in which case the retry simply succeeds.
		select {
		case <-d.q:
			d.countDropped(1)
		default:
		}
	}
}

// countDropped records discarded events and logs the first, then every
// hundredth: a gap in an audit log must have a visible cause, but a subscriber
// that is permanently behind must not turn the app log into the same flood.
func (d *eventDispatcher) countDropped(n int) {
	d.mu.Lock()
	d.dropped += n
	total := d.dropped
	d.mu.Unlock()
	if total == 1 || total%100 == 0 {
		chat.LogOrNoop(d.log).Warn("event hook is falling behind; dropping events",
			"dropped", total, "queue_depth", cap(d.q))
	}
}

// Close stops the worker. Idempotent; safe to call concurrently with Post.
func (d *eventDispatcher) Close() {
	d.closeOnce.Do(func() {
		close(d.closed)
		<-d.done
	})
}
