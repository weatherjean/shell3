package shell3

import "errors"

// ErrBusy reports a call needing an idle session while a turn is in flight.
// Send returns it as an immediate Error event; Compact, Clear and
// RegisterHostTool return it directly. Drain the Send channel, then retry.
var ErrBusy = errors.New("shell3: a turn is in flight; drain the Send channel before calling this")

// ErrClosed reports a Send on a closed session: the channel emits one Error
// event and closes. A host event (a Wake-driven drain) can still hold the
// session, and must not run a turn against the ended store record.
var ErrClosed = errors.New("shell3: session is closed")

// ErrRuntimeClosed reports an operation on a Runtime whose Close has already
// run; Runtime.Session returns it instead of creating a session.
var ErrRuntimeClosed = errors.New("shell3: runtime is closed")
