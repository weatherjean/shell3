// Package chat is the agent core for shell3. It owns conversation state,
// drives the user→assistant turn loop, dispatches tool calls, and emits a
// stream of structured Events that observers consume.
//
// The package does no rendering. Presentation (TUI widgets, stdout printers,
// JSONL audit sinks) lives elsewhere and subscribes to Session.Events.
//
// Typical embedding flow:
//
//	sess := chat.NewSession(chat.SessionOpts{StoreID: id})
//	go func() {
//	    for ev := range sess.Events() {
//	        // render or log ev
//	    }
//	}()
//	sess.Start(meta)
//	sess.Run(ctx, turnCfg, "hello")
//	sess.End("ok")
//	sess.CloseEvents()
//
// Key entry points:
//
//   - NewSession constructs a Session with an event channel.
//   - Session.Run executes one user turn end-to-end, persisting to a store if
//     one is configured.
//   - RunTurn is the lower-level loop used by Session.Run; embedders can call
//     it directly when they need to manage history or persistence themselves.
//   - NewHandlers builds the built-in tool dispatch map from a Config.
//
// Concurrency: each Session owns one event channel, written by the turn loop
// and closed exactly once via Session.CloseEvents. Tool handlers run
// synchronously within the turn; background processes (BashBgHandler) are the
// exception and are detached from the session lifecycle.
package chat
