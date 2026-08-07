//go:build unix

package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"github.com/weatherjean/shell3/internal/shell3"
)

// streamWriter emits the AI SDK "UI message stream" — the SSE dialect
// assistant-ui speaks. Each chunk is one `data: {json}` line; the stream ends
// with `data: [DONE]`.
//
// Text and reasoning arrive as deltas that must be bracketed by start/end
// chunks carrying a block id. A tool call in the middle of a reply closes the
// open text block and the next token opens a fresh one, which is what makes
// the client render prose, then the tool, then more prose.
type streamWriter struct {
	sink chunkSink

	mu       sync.Mutex
	blocks   int    // block id counter
	openKind string // "text", "reasoning", or "" when nothing is open
	openID   string
	// openCalls are tool calls announced but not yet answered, in order. A
	// turn stopped mid-tool would otherwise leave them open forever, and the
	// client reads an unanswered call as one IT is expected to resolve — it
	// offers the user an Allow/Deny prompt for work that was already killed.
	openCalls []string
	failed    bool
}

// chunkSink is where a streamWriter's protocol chunks land: straight onto an
// HTTP response for a stream with exactly one reader, or into a turnBroker
// when the turn must outlive — and be re-attachable by — its readers.
type chunkSink interface {
	// writeChunk takes one marshaled protocol chunk. An error marks the
	// writer failed, which stops all further output.
	writeChunk(payload []byte) error
	// done ends the stream (the [DONE] marker, or closing the broker).
	done()
}

// writeSSEHeaders starts an SSE response the AI SDK client accepts.
func writeSSEHeaders(w http.ResponseWriter, flusher http.Flusher) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	// Tell the AI SDK client this is the v1 UI message stream, and any proxy
	// in front of us not to buffer it.
	h.Set("x-vercel-ai-ui-message-stream", "v1")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
}

// httpSink writes chunks straight to one HTTP response.
type httpSink struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func (h *httpSink) writeChunk(payload []byte) error {
	if _, err := fmt.Fprintf(h.w, "data: %s\n\n", payload); err != nil {
		return err
	}
	h.flusher.Flush()
	return nil
}

func (h *httpSink) done() {
	fmt.Fprint(h.w, "data: [DONE]\n\n")
	h.flusher.Flush()
}

func newStreamWriter(w http.ResponseWriter) (*streamWriter, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	writeSSEHeaders(w, flusher)
	return &streamWriter{sink: &httpSink{w: w, flusher: flusher}}, true
}

// newBrokeredStream is a streamWriter whose chunks land in a turnBroker, for
// a turn that must keep producing after its first reader is gone.
func newBrokeredStream(b *turnBroker) *streamWriter {
	return &streamWriter{sink: b}
}

// chunk writes one protocol chunk. Write errors mark the stream failed so the
// caller can stop early when the browser has gone away.
func (s *streamWriter) chunk(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return
	}
	if err := s.sink.writeChunk(b); err != nil {
		s.failed = true
	}
}

func (s *streamWriter) broken() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed
}

func (s *streamWriter) start(messageID string) {
	s.chunk(map[string]any{"type": "start", "messageId": messageID})
}

// closeBlock ends whichever text/reasoning block is open, if any.
func (s *streamWriter) closeBlock() {
	s.mu.Lock()
	kind, id := s.openKind, s.openID
	s.openKind, s.openID = "", ""
	s.mu.Unlock()
	if kind == "" {
		return
	}
	s.chunk(map[string]any{"type": kind + "-end", "id": id})
}

// delta appends to the open block of the given kind, opening one first (and
// closing a block of the other kind) when necessary.
func (s *streamWriter) delta(kind, text string) {
	if text == "" {
		return
	}
	s.mu.Lock()
	if s.openKind != kind {
		s.mu.Unlock()
		s.closeBlock()
		s.mu.Lock()
		s.blocks++
		s.openKind = kind
		s.openID = strconv.Itoa(s.blocks)
		id := s.openID
		s.mu.Unlock()
		s.chunk(map[string]any{"type": kind + "-start", "id": id})
		s.mu.Lock()
	}
	id := s.openID
	s.mu.Unlock()

	s.chunk(map[string]any{"type": kind + "-delta", "id": id, "delta": text})
}

func (s *streamWriter) toolCall(id, name, input string) {
	s.closeBlock()
	// Tool args are raw JSON. Pass them through as a value so the client can
	// render structured arguments; fall back to a string for malformed input.
	var args any
	if json.Unmarshal([]byte(input), &args) != nil {
		args = input
	}
	s.mu.Lock()
	s.openCalls = append(s.openCalls, id)
	s.mu.Unlock()
	s.chunk(map[string]any{
		"type": "tool-input-available", "toolCallId": id, "toolName": name, "input": args,
	})
}

func (s *streamWriter) toolResult(id, output string, isError bool) {
	s.closeCall(id)
	if isError {
		s.chunk(map[string]any{
			"type": "tool-output-error", "toolCallId": id, "errorText": output,
		})
		return
	}
	s.chunk(map[string]any{"type": "tool-output-available", "toolCallId": id, "output": output})
}

func (s *streamWriter) errorText(text string) {
	s.closeBlock()
	s.chunk(map[string]any{"type": "error", "errorText": text})
}

// closeCall marks a tool call answered.
func (s *streamWriter) closeCall(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, open := range s.openCalls {
		if open == id {
			s.openCalls = append(s.openCalls[:i], s.openCalls[i+1:]...)
			return
		}
	}
}

// abandonOpenCalls answers every tool call the turn never came back to. A turn
// that ends mid-tool — stopped, cancelled, or failed — has really left those
// calls unfinished, and saying so is both true and what keeps the client from
// treating them as still pending.
func (s *streamWriter) abandonOpenCalls() {
	s.mu.Lock()
	open := s.openCalls
	s.openCalls = nil
	s.mu.Unlock()

	// Reported as ordinary output rather than a tool error: an error on a tool
	// the client has no definition for is one it cannot handle, and it tears
	// down the conversation rather than showing it.
	for _, id := range open {
		s.chunk(map[string]any{
			"type": "tool-output-available", "toolCallId": id,
			"output": "stopped before it finished",
		})
	}
}

// finish closes any open block, emits the terminal chunks, and ends the SSE
// body. Safe to call once per response.
func (s *streamWriter) finish() {
	s.abandonOpenCalls()
	s.closeBlock()
	s.chunk(map[string]any{"type": "finish"})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return
	}
	s.sink.done()
}

// turnUsage is a turn's token count, as reported by the provider.
type turnUsage struct {
	Prompt, Completion, Total int
}

// pumpUsage translates one turn's events onto the stream and reports the
// turn's token totals, which the chat stream drops but the Status view
// reports. It returns when the event channel closes — the authoritative
// end-of-turn signal, since a terminal Done/Error event is best-effort and may
// be dropped when a turn is cancelled.
//
// Tool calls without an id still need to pair with their result, so calls are
// numbered positionally when the runtime does not supply an id.
//
// onNotice, when set, is called for host events that are not chat content but
// that a user should still learn about — a compaction just discarded part of
// the conversation, a provider call is being retried.
func pumpUsage(
	s *streamWriter,
	events <-chan shell3.Event,
	onNotice func(kind, text string),
) (usage turnUsage, sawError bool) {
	synthetic := 0
	lastCall := ""

	callID := func(id string) string {
		if id != "" {
			return id
		}
		synthetic++
		return "call-" + strconv.Itoa(synthetic)
	}

	for ev := range events {
		if s.broken() {
			continue // drain the channel; the turn must still run to completion
		}
		switch ev.Kind {
		case shell3.Token:
			s.delta("text", ev.Text)
		case shell3.Reasoning:
			s.delta("reasoning", ev.Text)
		case shell3.ToolCall:
			lastCall = callID(ev.ToolCallID)
			s.toolCall(lastCall, ev.ToolName, ev.ToolInput)
		case shell3.ToolResult:
			id := ev.ToolCallID
			if id == "" {
				id = lastCall
			}
			s.toolResult(id, ev.ToolOutput, ev.ToolError)
		case shell3.Error:
			// A cancelled turn is not a failure — someone pressed stop. Say so
			// in the reply instead of showing them "context canceled" in red.
			if errors.Is(ev.Err, context.Canceled) {
				s.delta("text", "\n\n_Stopped._")
				continue
			}
			sawError = true
			text := "the turn failed"
			if ev.Err != nil {
				text = ev.Err.Error()
			}
			if hint := shell3.RecoveryHint(ev.Err); hint != "" {
				text += "\n\n" + hint
			}
			s.errorText(text)
		case shell3.Compacted:
			// The conversation was just summarized to fit the context window —
			// consequential enough that silently doing it is the wrong call.
			if onNotice != nil {
				onNotice("compacted", ev.Text)
			}
		case shell3.Retry:
			if onNotice != nil {
				onNotice("retry", ev.Text)
			}
		case shell3.SystemReminder:
			// Injected host context, not something the user wrote or the agent
			// said. It belongs in the run transcript, not the chat.
		case shell3.Usage, shell3.Done:
			// Not chat content, but worth keeping: the Status view reports the
			// last turn's cost. Done carries the final totals and wins.
			if ev.TotalTokens > 0 {
				usage = turnUsage{
					Prompt:     ev.PromptTokens,
					Completion: ev.CompletionTokens,
					Total:      ev.TotalTokens,
				}
			}
		}
	}
	return usage, sawError
}
