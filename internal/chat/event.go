package chat

import (
	"fmt"

	"github.com/weatherjean/shell3/internal/llm"
)

// EventKind classifies stream events emitted by a chat session.
type EventKind int

const (
	// EventAssistantToken fires per streamed token, Text the delta.
	// High-volume.
	EventAssistantToken EventKind = iota
	// EventToolCall fires on invocation. ToolName and ToolInput are populated.
	EventToolCall
	// EventToolResult fires on return. ToolName, ToolOutput, and ToolError are
	// populated.
	EventToolResult
	// EventError fires on a non-fatal error (stream failure, gate denial),
	// Text the message.
	EventError
	// EventUsage fires on a provider usage report. Usage.
	EventUsage
	// EventAssistantReasoning fires for thinking tokens where a provider
	// surfaces them separately, Text the delta or block.
	EventAssistantReasoning
	// EventTurnDone fires once a whole turn, tool rounds included, completes.
	// Usage carries cumulative totals.
	EventTurnDone
	// EventRetry fires before a transient failure is retried, Text the reason
	// and attempt count.
	EventRetry
	// EventCompacted fires when the host auto-compacts. Text notes the
	// pre-compaction count; Usage carries the estimated post-compaction size,
	// so a UI can reflect the freed context immediately.
	EventCompacted
)

// Event is one observable occurrence, delivered through SessionOpts.Sink.
// Most fields are set only for certain Kinds — see the EventKind constants.
type Event struct {
	Kind EventKind

	// Text is the payload for token, reasoning, error and reminder.
	Text string
	// Err is the typed error on EventError, for errors.Is/As; Text holds its
	// message. Not serialized.
	Err      error
	ToolName string
	// ToolInput is the raw JSON args.
	ToolInput  string
	ToolOutput string
	ToolError  bool
	Usage      *llm.Usage
}

func emitToolCall(s *Session, name, input string) {
	emit(s, Event{
		Kind:      EventToolCall,
		ToolName:  name,
		ToolInput: input,
	})
}

func emitToolResult(s *Session, name, output string, isErr bool) {
	emit(s, Event{
		Kind:       EventToolResult,
		ToolName:   name,
		ToolOutput: output,
		ToolError:  isErr,
	})
}

func emitAssistantToken(s *Session, text string) {
	emit(s, Event{Kind: EventAssistantToken, Text: text})
}

// emitError emits the terminal error event. err must be non-nil: Text carries
// its message, Err the value itself.
func emitError(s *Session, err error) {
	emit(s, Event{Kind: EventError, Text: err.Error(), Err: err})
}

func emitUsage(s *Session, u llm.Usage) {
	emit(s, Event{Kind: EventUsage, Usage: &u})
}

func emitAssistantReasoning(s *Session, text string) {
	emit(s, Event{Kind: EventAssistantReasoning, Text: text})
}

func recordSystemReminder(s *Session, text string) {
	// Record so persisted history can interleave the reminder in the exact
	// provider-visible order.
	s.recordReminder(text)
}

// emitCompacted announces an auto-compaction: prevTokens tripped the
// threshold, newTokens estimates the rewritten history, which lets a UI drop
// its context meter before the next usage report lands.
func emitCompacted(s *Session, prevTokens, newTokens int) {
	emit(s, Event{
		Kind:  EventCompacted,
		Text:  fmt.Sprintf("context auto-compacted at %d tokens", prevTokens),
		Usage: &llm.Usage{PromptTokens: newTokens, TotalTokens: newTokens},
	})
}

func emitRetry(s *Session, n *llm.RetryNotice) {
	text := fmt.Sprintf("stream failed (%s), retrying (%d/%d)", n.Reason, n.Attempt, n.Max)
	emit(s, Event{Kind: EventRetry, Text: text})
}

func emitTurnDone(s *Session, u llm.Usage) {
	emit(s, Event{Kind: EventTurnDone, Usage: &u})
}

// emit delivers an event to the session sink. Delivery is synchronous and
// inline on the turn goroutine, so events are never dropped and ordering is
// exactly the emit order.
func emit(s *Session, ev Event) {
	if s == nil {
		return
	}
	if s.sink != nil {
		s.sink(ev)
	}
}
