package chat

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// OutSink writes one JSONL event per call to a writer. Safe for concurrent
// writes; serializes through an internal mutex.
//
// ANSI stripping is the producer's responsibility: chat.Event payloads are
// expected to be plain text by the time they reach the sink.
type OutSink struct {
	mu  sync.Mutex
	w   io.Writer
	now func() time.Time
}

func newOutSink(w io.Writer, fixed time.Time) *OutSink {
	now := func() time.Time { return time.Now().UTC() }
	if !fixed.IsZero() {
		now = func() time.Time { return fixed }
	}
	return &OutSink{w: w, now: now}
}

type outEvent struct {
	TS       string `json:"ts"`
	Kind     string `json:"kind"`
	Input    string `json:"input,omitempty"`
	Persona  string `json:"persona,omitempty"`
	Model    string `json:"model,omitempty"`
	Out      string `json:"out,omitempty"`
	Headless *bool  `json:"headless,omitempty"`
	Status   string `json:"status,omitempty"`
}

// writeLocked serializes one event. Caller must hold s.mu.
func (s *OutSink) writeLocked(e outEvent) {
	e.TS = s.now().Format(time.RFC3339Nano)
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = s.w.Write(data)
}

// WriteStart emits the first line of the JSONL stream.
func (s *OutSink) WriteStart(input, persona, model, out string, headless bool) {
	if s == nil {
		return
	}
	h := headless
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeLocked(outEvent{Kind: "start", Input: input, Persona: persona, Model: model, Out: out, Headless: &h})
}

// WriteEnd emits the final line of the JSONL stream.
func (s *OutSink) WriteEnd(status string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeLocked(outEvent{Kind: "end", Status: status})
}

// WriteChatEvent serializes a chat.Event as one JSONL line. ANSI stripping
// is the producer's responsibility, not the sink's.
func (s *OutSink) WriteChatEvent(ev Event) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := map[string]any{
		"ts":   ev.Time.UTC().Format(time.RFC3339Nano),
		"kind": ev.Kind.String(),
	}
	if ev.SessionID != 0 {
		rec["session_id"] = ev.SessionID
	}
	if ev.Text != "" {
		rec["text"] = ev.Text
	}
	if ev.Role != "" {
		rec["role"] = ev.Role
	}
	if ev.ToolName != "" {
		rec["tool"] = ev.ToolName
	}
	if ev.ToolInput != "" {
		rec["input"] = ev.ToolInput
	}
	if ev.ToolOutput != "" {
		rec["output"] = ev.ToolOutput
	}
	if ev.ToolCallID != "" {
		rec["call_id"] = ev.ToolCallID
	}
	if ev.ToolError {
		rec["tool_error"] = true
	}
	if ev.Usage != nil {
		rec["usage"] = map[string]int{
			"prompt":     ev.Usage.PromptTokens,
			"completion": ev.Usage.CompletionTokens,
			"total":      ev.Usage.TotalTokens,
		}
	}
	if len(ev.Meta) > 0 {
		rec["meta"] = ev.Meta
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	_, _ = s.w.Write(append(b, '\n'))
}
