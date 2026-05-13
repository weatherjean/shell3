package patchapp

import "github.com/weatherjean/shell3/pkg/llm"

// Event is the sealed interface implemented by all turn events emitted by
// runTurn. It is sent on a channel from the turn goroutine and consumed by
// the App, which translates events into render state changes.
type Event interface{ event() }

// ChunkEvent is one streaming text delta from the LLM.
type ChunkEvent struct{ Text string }

// ReasoningChunkEvent is one streaming reasoning/thinking delta from the LLM.
// Committed line-by-line to scrollback in dim gray (see chat.drainTurn).
// Not saved to conversation history.
type ReasoningChunkEvent struct{ Text string }

// AppendEvent is pre-formatted text (typically tool output) to commit to
// scrollback in the order it arrives.
type AppendEvent struct{ Text string }

// UsageEvent carries token usage for an intermediate LLM request inside a
// multi-step turn. Providers generally emit usage at the end of each streamed
// request, so this lets the footer refresh before a long tool chain finishes.
type UsageEvent struct{ Usage llm.Usage }

// TurnDoneEvent signals the current LLM turn completed successfully.
type TurnDoneEvent struct{ Usage llm.Usage }

// TurnErrEvent carries an error from the turn pipeline.
type TurnErrEvent struct{ Err error }

// TTYExecEvent requests the App to release the terminal, run Cmd with
// stdio inherited, and write the result to ReplyC. The sender must block
// on <-ReplyC before continuing.
type TTYExecEvent struct {
	Cmd     string
	WorkDir string
	ReplyC  chan<- string
}

func (ChunkEvent) event()          {}
func (ReasoningChunkEvent) event() {}
func (AppendEvent) event()   {}
func (UsageEvent) event()    {}
func (TurnDoneEvent) event() {}
func (TurnErrEvent) event()  {}
func (TTYExecEvent) event()  {}
