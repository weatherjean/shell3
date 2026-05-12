package patchapp

import "context"

// AppView is the narrow surface of [App] that goroutines outside the
// input loop should depend on when feeding events into the live frame.
//
// Defining the consumer-side contract here (rather than at each
// consumer) lets unrelated packages drive the App without importing the
// full *App, and lets tests substitute a recorder. *App satisfies this
// interface for free.
//
// Methods that mutate UI state from any goroutine:
//   - Print / PrintLine — append to scrollback above the live frame
//   - SetTokens         — update the status-bar token counter
//   - SetContextWindow  — set model context window size for % display
//   - SetBusy           — toggle the streaming/spinner state with a cancel
//   - WithReleasedTerminal — yield the TTY so a subprocess can run
//
// Slash command handlers, which run synchronously inside the input loop,
// use a separate narrower interface (see chat package).
type AppView interface {
	Print(lines []string)
	PrintLine(line string)
	SetTokens(n int)
	SetContextWindow(n int)
	SetBusy(busy bool, cancel context.CancelFunc)
	WithReleasedTerminal(fn func())
}

// Compile-time assertion: *App satisfies AppView.
var _ AppView = (*App)(nil)
