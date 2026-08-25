// Package chat is the agent core: conversation state, the user→assistant turn
// loop, tool dispatch, and a stream of structured Events. It does no
// rendering — front-ends receive each Event via SessionOpts.Sink.
//
// NewSession builds a Session; Session.Run executes one user turn end-to-end
// over the lower-level RunTurn loop; NewHandlers builds the built-in tool map.
//
// The sink is invoked synchronously on the turn's goroutine in emit order, so
// when Run returns every event has been delivered. Tool handlers run inside
// the turn; background work (BashBgHandler, TaskHandler) goes to
// internal/shell3's job runtime, which injects completion notices into later
// turns.
package chat
