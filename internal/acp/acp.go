// Package acp implements the ACP (Agent Client Protocol) front-end for shell3.
// This file contains the connection wiring that bridges an ACP peer to the
// shell3 Runtime via the Agent interface.
package acp

import (
	"context"
	"io"
	"log/slog"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// Options configures a Run invocation.
type Options struct {
	// DefaultAgent is the agent name to use when creating new sessions.
	// Empty string selects the first declared agent.
	DefaultAgent string
	// Logger, when non-nil, is attached to the ACP connection for debug logging.
	Logger *slog.Logger
	// onReady is an internal hook called (synchronously in Run, before blocking)
	// with the newly-built acpAgent so tests can inspect its internal state.
	// Not part of the public API.
	onReady func(*acpAgent)
}

// Run wires the shell3 Runtime to an ACP peer over in/out.
// It blocks until the peer disconnects (conn.Done() closes) or ctx is cancelled,
// then returns nil. The caller is responsible for closing in/out.
//
// Typical usage: pass os.Stdin/os.Stdout for a stdio ACP server.
func Run(ctx context.Context, rt *shell3.Runtime, in io.Reader, out io.Writer, opts Options) error {
	a := newACPAgent(rt, opts)
	conn := acpsdk.NewAgentSideConnection(a, out, in)
	a.mu.Lock()
	a.conn = conn
	a.mu.Unlock()
	if opts.Logger != nil {
		conn.SetLogger(opts.Logger)
	}
	if opts.onReady != nil {
		opts.onReady(a)
	}
	// Start the connection-lifetime event pump BEFORE blocking. It forwards
	// out-of-turn Notices and Wake-driven queued turns to the client. pumpCtx is
	// cancelled when Run returns (via the defer below), so the pump exits on peer
	// disconnect too — not only when the caller cancels ctx.
	pumpCtx, cancelPump := context.WithCancel(ctx)
	defer cancelPump()
	go a.pump(pumpCtx)
	go a.pumpJobs(pumpCtx)
	// Block until the peer closes the connection or ctx is cancelled.
	select {
	case <-conn.Done():
	case <-ctx.Done():
	}
	return nil
}
