package acp

import (
	"context"
	"sync"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// acpSession wraps a live shell3.Session with ACP session identity
// and a per-turn cancellation handle.
type acpSession struct {
	id      string
	workDir string // cwd from NewSession params (or "" for runtime default)

	sess *shell3.Session

	// turnSlot serializes turn ownership: a cap-1 channel used as a
	// context-aware mutex. Holding the token = owning the session's single
	// turn. Both turn drivers (a client Prompt and a Wake-driven drainQueued)
	// acquire it via acquireTurn before touching the shell3 session, so a new
	// prompt WAITS for the in-flight turn to unwind instead of racing it —
	// this is what makes ACP prompt-supersede work: the SDK cancels the old
	// prompt's ctx, the old turn unwinds and releases the slot, and the new
	// prompt (waiting, bounded by its own request ctx) takes over.
	turnSlot chan struct{}

	mu         sync.Mutex
	cancelTurn context.CancelFunc // set while a turn holds turnSlot; invoked by session/cancel
	liveToolID string             // ToolCallID of the tool call currently streaming/executing ("" when none)
}

// newACPSession constructs an acpSession with its turn slot initialized.
func newACPSession(id, workDir string, sess *shell3.Session) *acpSession {
	return &acpSession{
		id:       id,
		workDir:  workDir,
		sess:     sess,
		turnSlot: make(chan struct{}, 1),
	}
}

// acquireTurn blocks until the caller owns the session's single turn slot,
// then registers cancel as the turn's cancel func (invoked by session/cancel
// and CloseSession). It returns ctx.Err() when ctx is cancelled while waiting
// — for a Prompt that means the SDK superseded or cancelled the request; for
// drainQueued it means connection teardown.
//
// Registration is race-free with respect to turn ownership: cancelTurn is
// written under s.mu only after the slot token is held, and cleared (under
// s.mu) by releaseTurn before the token is returned — so the slot owner and
// the registered cancel can never belong to different turns (the TOCTOU the
// old boolean setCancel had).
func (s *acpSession) acquireTurn(ctx context.Context, cancel context.CancelFunc) error {
	select {
	case s.turnSlot <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	s.cancelTurn = cancel
	s.mu.Unlock()
	return nil
}

// releaseTurn clears the registered cancel func and returns the turn slot
// token. Only the goroutine whose acquireTurn returned nil may call it.
func (s *acpSession) releaseTurn() {
	s.mu.Lock()
	s.cancelTurn = nil
	s.mu.Unlock()
	<-s.turnSlot
}

// noteToolEvent tracks which tool call is currently in flight so askerFor can
// attach permission requests to the REAL streamed tool card instead of a
// synthetic one. Set on ToolCall, cleared on the matching ToolResult.
//
// Events are delivered in order and synchronously with the turn goroutine
// (the shell3 route sink blocks on the unbuffered Send channel), so by the
// time a tool's on_tool_call gate can fire an ask, its ToolCall event has
// already been RECEIVED by the forwarding loop — and any previous tool's
// ToolResult fully processed (one goroutine processes events sequentially
// between receives, so a stale id can never be observed). The only race is
// with the tail of the hand-off: the turn goroutine usually keeps running
// after the channel send while the receiver is merely runnable, so the store
// below may not have executed yet when the ask fires. liveToolCallWait
// bridges that window by briefly polling.
func (s *acpSession) noteToolEvent(ev shell3.Event) {
	switch ev.Kind {
	case shell3.ToolCall:
		s.mu.Lock()
		s.liveToolID = ev.ToolCallID
		s.mu.Unlock()
	case shell3.ToolResult:
		s.mu.Lock()
		if s.liveToolID == ev.ToolCallID {
			s.liveToolID = ""
		}
		s.mu.Unlock()
	}
}

// liveToolCallWait returns the ToolCallID of the tool call currently
// executing, polling briefly (up to timeout) for the forwarding goroutine to
// record it — see noteToolEvent for why the id may lag the ask by a
// scheduling beat. Returns "" if no id appears within timeout (an ask fired
// outside a tracked turn, or a stalled forwarder); callers must then fall
// back to a synthetic id. The poll is cheap relative to a human approval
// round-trip.
func (s *acpSession) liveToolCallWait(timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for {
		s.mu.Lock()
		id := s.liveToolID
		s.mu.Unlock()
		if id != "" || time.Now().After(deadline) {
			return id
		}
		time.Sleep(time.Millisecond)
	}
}

// forward sends one shell3 event to the ACP client via conn.
//
// Usage events become usage_update (skipped when ctxWindow == 0).
// All other events are mapped via updatesForEvent and sent individually.
// Sends always use context.Background() so that a flush after turn
// cancellation is not dropped by a cancelled ctx.
func (s *acpSession) forward(_ context.Context, conn *acpsdk.AgentSideConnection, ev shell3.Event, ctxWindow int) {
	s.noteToolEvent(ev)
	if ev.Kind == shell3.Usage {
		if ctxWindow == 0 {
			return
		}
		_ = conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
			SessionId: acpsdk.SessionId(s.id),
			Update: acpsdk.SessionUpdate{
				UsageUpdate: &acpsdk.SessionUsageUpdate{
					Used: ev.TotalTokens,
					Size: ctxWindow,
				},
			},
		})
		return
	}
	for _, u := range updatesForEvent(ev) {
		_ = conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
			SessionId: acpsdk.SessionId(s.id),
			Update:    u,
		})
	}
}
