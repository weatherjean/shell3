package chat

import (
	"fmt"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
)

// EventKind classifies stream events emitted by a chat session.
type EventKind int

const (
	// EventSessionStart is the zero value. Nothing emits it; it stays so a
	// zero Event is never mistaken for a real one.
	EventSessionStart EventKind = iota
	// EventSessionEnd fires at teardown; Meta["status"] is "ok" or an error.
	EventSessionEnd
	// EventUserMessage fires on user input. Role, Text.
	EventUserMessage
	// EventAssistantToken fires per streamed token, Text the delta.
	// High-volume.
	EventAssistantToken
	// EventAssistantMessage fires once streaming completes, Text the whole
	// message.
	EventAssistantMessage
	// EventToolCall fires on invocation. ToolName, ToolInput, ToolCallID.
	EventToolCall
	// EventToolResult fires on return. ToolName, ToolOutput, ToolCallID,
	// ToolError.
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
	// EventSystemReminder fires on an injected <system-reminder>, Text the
	// rendered block.
	EventSystemReminder
	// EventRetry fires before a transient failure is retried, Text the reason
	// and attempt count.
	EventRetry
	// EventCompacted fires when the host auto-compacts. Text notes the
	// pre-compaction count; Usage carries the estimated post-compaction size,
	// so a UI can reflect the freed context immediately.
	EventCompacted

	// numEventKinds must stay LAST: it bounds kit_events_test.go's iteration,
	// and a hardcoded bound there would leave a kind added below it invisible
	// to the check that exists to catch drift.
	numEventKinds
)

func (k EventKind) String() string {
	switch k {
	case EventSessionStart:
		return "session_start"
	case EventSessionEnd:
		return "session_end"
	case EventUserMessage:
		return "user_message"
	case EventAssistantToken:
		return "assistant_token"
	case EventAssistantMessage:
		return "assistant_message"
	case EventToolCall:
		return "tool_call"
	case EventToolResult:
		return "tool_result"
	case EventError:
		return "error"
	case EventUsage:
		return "usage"
	case EventAssistantReasoning:
		return "assistant_reasoning"
	case EventTurnDone:
		return "turn_done"
	case EventSystemReminder:
		return "system_reminder"
	case EventRetry:
		return "retry"
	case EventCompacted:
		return "compacted"
	}
	return "unknown"
}

// Event is one observable occurrence, delivered through SessionOpts.Sink.
// Most fields are set only for certain Kinds — see the EventKind constants.
type Event struct {
	Kind EventKind
	Time time.Time
	// SessionID is the runs session id, "" with no store.
	SessionID string

	// Text is the payload for token, message, reasoning, error and reminder.
	Text string
	// Err is the typed error on EventError, for errors.Is/As; Text holds its
	// message. Not serialized.
	Err      error `json:"-"`
	Role     string
	ToolName string
	// ToolInput is the raw JSON args.
	ToolInput  string
	ToolOutput string
	ToolError  bool
	// ToolCallID links a tool_call to its tool_result.
	ToolCallID string
	Usage      *llm.Usage
	// Meta carries small extras, such as session_end's status.
	Meta map[string]string
}

func emitSessionEnd(s *Session, status string) {
	emit(s, Event{
		Kind:      EventSessionEnd,
		Time:      time.Now(),
		SessionID: s.id,
		Meta:      map[string]string{"status": status},
	})
}

func emitToolCall(s *Session, callID, name, input string) {
	emit(s, Event{
		Kind:       EventToolCall,
		Time:       time.Now(),
		SessionID:  s.id,
		ToolName:   name,
		ToolInput:  input,
		ToolCallID: callID,
	})
}

func emitToolResult(s *Session, callID, name, output string, isErr bool) {
	emit(s, Event{
		Kind:       EventToolResult,
		Time:       time.Now(),
		SessionID:  s.id,
		ToolName:   name,
		ToolOutput: output,
		ToolError:  isErr,
		ToolCallID: callID,
	})
}

func emitAssistantToken(s *Session, text string) {
	emit(s, Event{Kind: EventAssistantToken, Time: time.Now(), SessionID: s.id, Text: text})
}

func emitAssistantMessage(s *Session, text string) {
	emit(s, Event{Kind: EventAssistantMessage, Time: time.Now(), SessionID: s.id, Role: "assistant", Text: text})
}

func emitUserMessage(s *Session, text string) {
	emit(s, Event{Kind: EventUserMessage, Time: time.Now(), SessionID: s.id, Role: "user", Text: text})
}

// emitError emits the terminal error event. err must be non-nil: Text carries
// its message, Err the value itself.
func emitError(s *Session, err error) {
	emit(s, Event{Kind: EventError, Time: time.Now(), SessionID: s.id, Text: err.Error(), Err: err})
}

func emitUsage(s *Session, u llm.Usage) {
	emit(s, Event{Kind: EventUsage, Time: time.Now(), SessionID: s.id, Usage: &u})
}

func emitAssistantReasoning(s *Session, text string) {
	emit(s, Event{Kind: EventAssistantReasoning, Time: time.Now(), SessionID: s.id, Text: text})
}

func emitSystemReminder(s *Session, text string) {
	// Record before emitting, so History() can interleave the reminder as a
	// system-role entry; a live front-end consumes the event instead.
	s.recordReminder(text)
	emit(s, Event{Kind: EventSystemReminder, Time: time.Now(), SessionID: s.id, Text: text})
}

// emitCompacted announces an auto-compaction: prevTokens tripped the
// threshold, newTokens estimates the rewritten history, which lets a UI drop
// its context meter before the next usage report lands.
func emitCompacted(s *Session, prevTokens, newTokens int) {
	emit(s, Event{
		Kind:      EventCompacted,
		Time:      time.Now(),
		SessionID: s.id,
		Text:      fmt.Sprintf("context auto-compacted at %d tokens", prevTokens),
		Usage:     &llm.Usage{PromptTokens: newTokens, TotalTokens: newTokens},
	})
}

func emitRetry(s *Session, n *llm.RetryNotice) {
	text := fmt.Sprintf("stream failed (%s), retrying (%d/%d)", n.Reason, n.Attempt, n.Max)
	emit(s, Event{Kind: EventRetry, Time: time.Now(), SessionID: s.id, Text: text})
}

func emitTurnDone(s *Session, u llm.Usage) {
	emit(s, Event{Kind: EventTurnDone, Time: time.Now(), SessionID: s.id, Usage: &u})
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
	// The kit subscriber observes the same event, and does so even when no
	// Sink is installed: a headless session still has observable events. Read
	// under msgMu because a reload swaps the observer (SetOnEvent) while a
	// turn is emitting.
	s.msgMu.RLock()
	onEvent := s.onEvent
	s.msgMu.RUnlock()
	if onEvent != nil {
		onEvent(ev)
	}
}
