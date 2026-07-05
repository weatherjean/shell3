package acp

import (
	"context"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// pump forwards runtime host events to the client for the whole connection
// lifetime, independent of any in-flight prompt. Out-of-turn session/update
// notifications are valid ACP and are rendered by OpenACP + passthrough clients
// with no turn guard.
//
// conn is snapshotted ONCE at pump start and used for the pump's lifetime
// (including the goroutines it spawns) — Run sets a.conn once before starting
// the pump, so the single synchronized read suffices.
//
// The pump returns when ctx is cancelled (Run's ctx / connection teardown) or
// when the runtime's Events channel closes.
func (a *acpAgent) pump(ctx context.Context) {
	conn := a.connection()
	if conn == nil {
		return
	}

	events := a.rt.Events()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return // runtime closed the bus
			}
			s := a.sessionByName(ev.Session)
			if s == nil {
				continue // session owned by another front-end / a child session
			}
			if ev.Kind == shell3.Wake {
				// A Wake means the session's inbox gained an item while idle (an
				// async subagent completed, or steering that outlived a turn). Drain
				// it as a fresh turn on its own goroutine so the pump keeps servicing
				// the bus for other sessions.
				go a.drainQueued(ctx, s, conn)
			}
		}
	}
}

// drainQueued runs one Wake-driven turn (RunQueued) and forwards its events to
// the client out-of-turn, using the pump's captured conn.
//
// Turn ownership: a Wake turn and a client Prompt turn are mutually exclusive
// owners of the session's turn slot. drainQueued WAITS for the slot (bounded
// by the pump ctx — the connection lifetime), so if a client Prompt owns the
// turn the drain simply runs after it finishes; RunQueued then no-ops when the
// finished turn already drained the inbox. Registering the cancel func happens
// atomically with slot acquisition (see acquireTurn), so session/cancel always
// targets whichever turn actually owns the session — the old non-blocking
// setCancel had a window where a turn could run unregistered (uncancellable).
//
// cancel() is deferred to release the context resource regardless.
func (a *acpAgent) drainQueued(ctx context.Context, s *acpSession, conn *acpsdk.AgentSideConnection) {
	drainCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := s.acquireTurn(ctx, cancel); err != nil {
		return // pump ctx cancelled while waiting: connection teardown
	}
	defer s.releaseTurn()

	ctxWindow := s.sess.Snapshot().ContextWindow
	for ev := range s.sess.RunQueued(drainCtx) {
		if ev.Kind == shell3.Error {
			// A failed wake-turn (e.g. an LLM error while relaying a subagent
			// result) has no requesting Prompt to report through — surface it
			// to the operator's log instead of dropping it on the floor.
			if a.opts.Logger != nil && ev.Err != nil {
				a.opts.Logger.Warn("wake-driven turn failed", "session", s.id, "error", ev.Err)
			}
			continue
		}
		s.forward(conn, ev, ctxWindow)
	}
}
