// Package chat is the agent core for shell3. It owns conversation state,
// drives the user→assistant turn loop, dispatches tool calls, and emits a
// stream of structured Events that observers consume.
//
// The package does no rendering. Presentation (front-end views, stdout printers,
// JSONL audit sinks) lives elsewhere and receives each Event via the
// SessionOpts.Sink callback.
//
// Typical usage flow:
//
//	sess := chat.NewSession(chat.SessionOpts{StoreID: id, Sink: func(ev chat.Event) {
//	    // render or log ev
//	}})
//	sess.Run(ctx, turnCfg, "hello")
//
// Key entry points:
//
//   - NewSession constructs a Session that delivers events to SessionOpts.Sink.
//   - Session.Run executes one user turn end-to-end, persisting to a store if
//     one is configured.
//   - RunTurn is the lower-level loop used by Session.Run.
//   - NewHandlers builds the built-in tool dispatch map.
//
// Concurrency: the sink is invoked synchronously on the goroutine running the
// turn, in emit order — when Run returns, every event has been delivered. Tool
// handlers run synchronously within the turn; background work (BashBgHandler,
// TaskHandler) is handed to internal/shell3's in-process job runtime, which
// supervises it and injects completion notices into later turns.
package chat
