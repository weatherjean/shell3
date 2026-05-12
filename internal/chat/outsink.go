package chat

import (
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/patchapp"
	"github.com/weatherjean/shell3/internal/patchtui"
)

// outSink writes one JSONL event per call to a writer. Safe for concurrent
// writes; serializes through an internal mutex. All text payloads have ANSI
// escape sequences stripped before serialization.
//
// Streaming text and reasoning deltas are accumulated per message and flushed
// as a single "text" or "reasoning" event whenever a boundary event arrives
// (tool, usage, turn_done, error, tty_exec_request) or on WriteEnd. This keeps
// the JSONL coherent — one event per logical message — instead of one event
// per token.
type outSink struct {
	mu        sync.Mutex
	w         io.Writer
	now       func() time.Time
	textBuf   strings.Builder
	reasonBuf strings.Builder
}

func newOutSink(w io.Writer, fixed time.Time) *outSink {
	now := func() time.Time { return time.Now().UTC() }
	if !fixed.IsZero() {
		now = func() time.Time { return fixed }
	}
	return &outSink{w: w, now: now}
}

type outEvent struct {
	TS         string `json:"ts"`
	Kind       string `json:"kind"`
	Text       string `json:"text,omitempty"`
	Raw        string `json:"raw,omitempty"`
	Input      string `json:"input,omitempty"`
	Persona    string `json:"persona,omitempty"`
	Model      string `json:"model,omitempty"`
	Out        string `json:"out,omitempty"`
	Headless   *bool  `json:"headless,omitempty"`
	Cmd        string `json:"cmd,omitempty"`
	WorkDir    string `json:"workdir,omitempty"`
	Error      string `json:"error,omitempty"`
	Status     string `json:"status,omitempty"`
	Prompt     int    `json:"prompt,omitempty"`
	Completion int    `json:"completion,omitempty"`
	Total      int    `json:"total,omitempty"`
}

// writeLocked serializes one event. Caller must hold s.mu.
func (s *outSink) writeLocked(e outEvent) {
	e.TS = s.now().Format(time.RFC3339Nano)
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = s.w.Write(data)
}

// flushBuffersLocked emits any accumulated reasoning then text as single events.
// Order (reasoning first) matches the model's logical sequence: think, then speak.
// Caller must hold s.mu.
func (s *outSink) flushBuffersLocked() {
	if s.reasonBuf.Len() > 0 {
		s.writeLocked(outEvent{Kind: "reasoning", Text: s.reasonBuf.String()})
		s.reasonBuf.Reset()
	}
	if s.textBuf.Len() > 0 {
		s.writeLocked(outEvent{Kind: "text", Text: s.textBuf.String()})
		s.textBuf.Reset()
	}
}

// WriteStart emits the first line of the JSONL stream.
func (s *outSink) WriteStart(input, persona, model, out string, headless bool) {
	if s == nil {
		return
	}
	h := headless
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeLocked(outEvent{Kind: "start", Input: input, Persona: persona, Model: model, Out: out, Headless: &h})
}

// WriteEnd flushes any pending accumulated text/reasoning and emits the final line.
func (s *outSink) WriteEnd(status string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushBuffersLocked()
	s.writeLocked(outEvent{Kind: "end", Status: status})
}

// WriteEvent maps a patchapp.Event to its JSONL form. Streaming text and
// reasoning deltas accumulate into per-message buffers; every other event
// kind flushes pending buffers before emitting itself.
func (s *outSink) WriteEvent(ev patchapp.Event) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch v := ev.(type) {
	case patchapp.ChunkEvent:
		s.textBuf.WriteString(patchtui.StripANSI(v.Text))
	case patchapp.ReasoningChunkEvent:
		s.reasonBuf.WriteString(patchtui.StripANSI(v.Text))
	case patchapp.AppendEvent:
		s.flushBuffersLocked()
		s.writeLocked(outEvent{Kind: "tool", Raw: patchtui.StripANSI(v.Text)})
	case patchapp.UsageEvent:
		s.flushBuffersLocked()
		s.writeLocked(usageEv("usage", v.Usage))
	case patchapp.TurnDoneEvent:
		s.flushBuffersLocked()
		s.writeLocked(usageEv("turn_done", v.Usage))
	case patchapp.TurnErrEvent:
		s.flushBuffersLocked()
		msg := ""
		if v.Err != nil {
			msg = v.Err.Error()
		}
		s.writeLocked(outEvent{Kind: "error", Error: msg})
	case patchapp.TTYExecEvent:
		s.flushBuffersLocked()
		s.writeLocked(outEvent{Kind: "tty_exec_request", Cmd: v.Cmd, WorkDir: v.WorkDir})
	}
}

func usageEv(kind string, u llm.Usage) outEvent {
	return outEvent{
		Kind:       kind,
		Prompt:     u.PromptTokens,
		Completion: u.CompletionTokens,
		Total:      u.TotalTokens,
	}
}
