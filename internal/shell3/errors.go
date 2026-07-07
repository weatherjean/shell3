package shell3

import "errors"

// ErrBusy reports a call that requires the session to be idle while a turn is
// still in flight. Send returns it as an immediate Error event; Clear,
// Rollback, SwitchAgent, SetParam, RegisterHostTool, and Prune return it (or
// surface it) directly. Drain the in-flight Send channel to completion, then
// retry.
var ErrBusy = errors.New("shell3: a turn is in flight; drain the Send channel before calling this")

// ErrClosed reports a Send on a session whose Close has already run — the
// returned channel emits a single Error event carrying it and closes, exactly
// like the ErrBusy rejection. A host event (e.g. a Wake-driven queued drain)
// may still hold a reference to the session after it closed; the send is
// rejected instead of running a turn against the ended store record.
var ErrClosed = errors.New("shell3: session is closed")

// ErrRuntimeClosed reports an operation on a Runtime whose Close has already
// run; Runtime.Session returns it instead of creating a session.
var ErrRuntimeClosed = errors.New("shell3: runtime is closed")
