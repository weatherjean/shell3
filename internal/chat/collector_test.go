package chat

import "sync"

// collector is a synchronous event sink for tests. Because a Session delivers
// events inline on the goroutine that runs the turn, every event emitted up to
// a given point has been recorded by the time the emitting call returns — no
// draining, channels, or timeouts are needed. The mutex makes all() safe to
// call from a different goroutine than the one running the turn.
type collector struct {
	mu     sync.Mutex
	events []Event
}

func (c *collector) sink(ev Event) {
	c.mu.Lock()
	c.events = append(c.events, ev)
	c.mu.Unlock()
}

// all returns a snapshot of every event recorded so far, in emit order.
func (c *collector) all() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Event, len(c.events))
	copy(out, c.events)
	return out
}

// newCollectorSession builds a Session whose events are recorded by the
// returned collector. opts.Sink is overwritten.
func newCollectorSession(opts SessionOpts) (*Session, *collector) {
	c := &collector{}
	opts.Sink = c.sink
	return NewSession(opts), c
}
